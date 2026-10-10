package evaluator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/internal/usersimulator"
	"github.com/alibaba/skill-up/pkg/transcript"
)

const windowsGOOS = "windows"

func TestMultiJudgeEvaluatesOneExecutionWithIndependentWorkspaces(t *testing.T) {
	if goruntime.GOOS == windowsGOOS {
		t.Skip("POSIX script fixture")
	}
	workspace := t.TempDir()
	script := makeFailingJudgeScript(t)
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", WorkspaceDir: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	ag := makeMultiJudgeAgent(t)
	members := []config.JudgeConfig{
		{ID: "functional", Type: "script", ScriptPath: script},
		{ID: "semantic", Type: "agent_judge", Model: "test", Criteria: []string{"quality"}, Context: &config.JudgeContextConfig{
			Attachments: []config.JudgeContextAttachment{{Path: filepath.Join(workspace, "result.txt")}},
		}},
	}
	e := newTestEvaluator(EvalOptions{
		Agent: ag, OutputDir: t.TempDir(),
		EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judges: &members},
	})
	caseCfg := &config.CaseConfig{ID: "case", Input: config.Input{Prompt: "create file"}}
	provider, ok := rt.(runtime.JudgeSnapshotProvider)
	if !ok {
		t.Fatal("none runtime lacks snapshot capability")
	}
	result := e.executeCase(context.Background(), caseCfg, "with_skill", &snapshotCapableRuntime{Runtime: rt, provider: provider}, nil)
	if result.AggregationStrategy != "all_required" {
		t.Fatalf("aggregation strategy=%q", result.AggregationStrategy)
	}
	if result.Status != judge.StatusFail || len(result.JudgeResults) != 2 {
		t.Fatalf("status=%s outcomes=%+v error=%v", result.Status, result.JudgeResults, result.Error)
	}
	if result.JudgeResults[0].Status != judge.StatusFail || result.JudgeResults[1].Status != judge.StatusPass {
		t.Fatalf("outcomes=%+v", result.JudgeResults)
	}
	if ag.runCall.Load() != 2 {
		t.Fatalf("agent runs=%d", ag.runCall.Load())
	}
	data, err := os.ReadFile(filepath.Join(workspace, "result.txt"))
	if err != nil || string(data) != "original" {
		t.Fatalf("source workspace=%q, %v", data, err)
	}
}

func makeMultiJudgeAgent(t *testing.T) *mockAgent {
	t.Helper()
	ag := &mockAgent{name: "test"}
	ag.runFunc = func(_ context.Context, current runtime.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
		if ag.runCall.Load() == 1 {
			if err := os.WriteFile(filepath.Join(current.Workspace(), "result.txt"), []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			return &agent.SessionResult{FinalMessage: "done"}, nil
		}
		data, err := os.ReadFile(filepath.Join(current.Workspace(), "result.txt"))
		if err != nil || string(data) != "original" {
			t.Fatalf("semantic judge saw %q, %v", data, err)
		}
		assertWorkspaceAttachment(t, current.Workspace())
		return &agent.SessionResult{FinalMessage: `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":["original input"],"failures":[]}]}`}, nil
	}
	return ag
}

func makeFailingJudgeScript(t *testing.T) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "check.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf changed > result.txt\nexit 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestAggregateJudgeOutcomesDoesNotFlattenCriteria(t *testing.T) {
	pass := judge.NewResult([]judge.AssertionResult{{Passed: true}, {Passed: true}, {Passed: false}}, 1, 1)
	pass.Status = judge.StatusPass // an agent_judge threshold may permit one failed criterion
	result, status := judge.DefaultOutcomeAggregator().Aggregate([]JudgeOutcome{
		{ID: "script", Type: "script", Status: judge.StatusFail},
		{ID: "semantic", Type: "agent_judge", Status: judge.StatusPass, Result: pass},
	}, 1, 1)
	if status != judge.StatusFail || result.Summary.Total != 2 || result.Summary.PassRate != 0.5 {
		t.Fatalf("status=%s summary=%+v", status, result.Summary)
	}
}

func TestMultiJudgeGateFailureSkipsEveryMember(t *testing.T) {
	workspace := t.TempDir()
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", WorkspaceDir: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	ag := &mockAgent{name: "test", output: "missing"}
	members := []config.JudgeConfig{{ID: "first", Type: "rule_based"}, {ID: "second", Type: "rule_based"}}
	e := newTestEvaluator(EvalOptions{Agent: ag, OutputDir: t.TempDir(), EvalCfg: &config.EvalConfig{
		Environment: config.Environment{Type: "none"}, Judges: &members,
	}})
	caseCfg := &config.CaseConfig{ID: "gate", Input: config.Input{Prompt: "hello"}, Expect: config.Expect{MustContain: []string{"required"}}}
	result := e.executeCase(context.Background(), caseCfg, "with_skill", rt, nil)
	if result.Status != judge.StatusFail || len(result.JudgeResults) != 2 || ag.runCall.Load() != 1 || result.Grading == nil {
		t.Fatalf("status=%s outcomes=%+v runs=%d", result.Status, result.JudgeResults, ag.runCall.Load())
	}
	if result.Grading.Summary.Total != 1 || result.Grading.Summary.Failed != 1 {
		t.Fatalf("gate compatibility grading=%+v", result.Grading.Summary)
	}
	for _, outcome := range result.JudgeResults {
		if outcome.Status != judge.StatusSkip || outcome.SkipReason != "gate_failed" {
			t.Fatalf("outcome=%+v", outcome)
		}
	}
}

func TestMultiJudgeUnsupportedRuntimeStopsBeforeAgentRun(t *testing.T) {
	ag := &mockAgent{name: "test", output: "done"}
	members := []config.JudgeConfig{{ID: "check", Type: "rule_based"}}
	e := newTestEvaluator(EvalOptions{Agent: ag, EvalCfg: &config.EvalConfig{Judges: &members}})
	caseCfg := &config.CaseConfig{ID: "case", Input: config.Input{Prompt: "hello"}}
	result := e.executeCase(context.Background(), caseCfg, ConfigurationWithoutSkill, &mockRuntime{workspace: t.TempDir()}, nil)
	if result.Status != judge.StatusError || result.Error == nil || ag.runCall.Load() != 0 || result.Configuration != ConfigurationWithoutSkill || len(result.JudgeResults) != len(members) {
		t.Fatalf("status=%s config=%q error=%v runs=%d", result.Status, result.Configuration, result.Error, ag.runCall.Load())
	}
	assertSkippedJudgeOutcomes(t, result.JudgeResults, members, "unsupported_runtime")
}

func TestMultiJudgeSnapshotFailureKeepsConfiguration(t *testing.T) {
	if goruntime.GOOS == windowsGOOS {
		t.Skip("symlink fixture")
	}
	workspace := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(workspace, "outside")); err != nil {
		t.Fatal(err)
	}
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", WorkspaceDir: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	members := []config.JudgeConfig{{ID: "check", Type: "rule_based"}}
	ag := &mockAgent{name: "test", output: "done"}
	e := newTestEvaluator(EvalOptions{Agent: ag, OutputDir: t.TempDir(), EvalCfg: &config.EvalConfig{
		Environment: config.Environment{Type: "none"}, Judges: &members,
	}})
	result := e.executeCase(context.Background(), &config.CaseConfig{ID: "case", Input: config.Input{Prompt: "hello"}}, ConfigurationWithoutSkill, rt, nil)
	if result.Status != judge.StatusError || result.Configuration != ConfigurationWithoutSkill || len(result.JudgeResults) != 1 {
		t.Fatalf("status=%s config=%q outcomes=%+v", result.Status, result.Configuration, result.JudgeResults)
	}
	if result.Grading == nil || result.Grading.Status != judge.StatusError || result.Grading.Summary.Total != len(members) || result.Error == nil {
		t.Fatalf("snapshot error=%v grading=%+v", result.Error, result.Grading)
	}
}

func TestMultiJudgeMemberTimeoutKeepsLaterResult(t *testing.T) {
	if goruntime.GOOS == windowsGOOS {
		t.Skip("POSIX sleep fixture")
	}
	script := filepath.Join(t.TempDir(), "slow.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", WorkspaceDir: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	limit := 1
	exitCode := 0
	members := []config.JudgeConfig{
		{ID: "slow", Type: "script", ScriptPath: script, TimeoutSeconds: &limit},
		{ID: "fast", Type: "rule_based", Success: []config.Rule{{ExitCode: &exitCode}}},
	}
	ag := &mockAgent{name: "test", output: "done"}
	e := newTestEvaluator(EvalOptions{Agent: ag, OutputDir: t.TempDir(), EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judges: &members}})
	result := e.executeCase(context.Background(), &config.CaseConfig{ID: "timeout", Input: config.Input{Prompt: "hello"}}, "with_skill", rt, nil)
	if result.Status != judge.StatusError || len(result.JudgeResults) != 2 || result.JudgeResults[0].Status != judge.StatusError || result.JudgeResults[1].Status != judge.StatusPass {
		t.Fatalf("status=%s outcomes=%+v", result.Status, result.JudgeResults)
	}
	if result.JudgeResults[0].Error == "" {
		t.Fatal("timed-out judge lost diagnostic reason")
	}
}

func TestMultiJudgeCaseDeadlinePreservesTimeoutError(t *testing.T) {
	if goruntime.GOOS == windowsGOOS {
		t.Skip("POSIX sleep fixture")
	}
	script := filepath.Join(t.TempDir(), "slow.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", WorkspaceDir: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	members := []config.JudgeConfig{{ID: "slow", Type: "script", ScriptPath: script}}
	ag := &mockAgent{name: "test", output: "done"}
	e := newTestEvaluator(EvalOptions{Agent: ag, OutputDir: t.TempDir(), EvalCfg: &config.EvalConfig{
		Environment: config.Environment{Type: "none"}, Judges: &members,
	}})
	caseCfg := &config.CaseConfig{ID: "timeout", Input: config.Input{Prompt: "hello"}, Constraints: config.Constraints{TimeoutSeconds: 1}}
	result := e.executeCase(context.Background(), caseCfg, "with_skill", rt, nil)
	if result.Status != judge.StatusError || !errors.Is(result.Error, context.DeadlineExceeded) {
		t.Fatalf("status=%s error=%v", result.Status, result.Error)
	}
	if got, ok := retryReasonForResult(result); !ok || got != "timeout" {
		t.Fatalf("retry reason=%q ok=%t", got, ok)
	}
}

// This adapter proves that evaluator dispatch depends on the capability rather
// than the concrete NoneRuntime type, while exercising real snapshot isolation.
type snapshotCapableRuntime struct {
	runtime.Runtime

	provider runtime.JudgeSnapshotProvider
}

func (r *snapshotCapableRuntime) CaptureJudgeSnapshot(ctx context.Context) (runtime.JudgeSnapshot, error) {
	return r.provider.CaptureJudgeSnapshot(ctx)
}

func TestMultiJudgePersistedContextReferencesResolve(t *testing.T) {
	ws, err := report.NewIterationWorkspace(t.TempDir(), "sample", 1)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	members := []config.JudgeConfig{
		{ID: "quality", Type: "agent_judge", Criteria: []string{"quality"}},
		{ID: "style", Type: "agent_judge", Criteria: []string{"style"}},
	}
	ag := &mockAgent{name: "test", output: `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":["checked"],"failures":[]}]}`}
	e := newTestEvaluator(EvalOptions{Agent: ag, OutputDir: ws.IterationDir(), EvalCfg: &config.EvalConfig{Judges: &members}})
	result := e.executeCase(context.Background(), &config.CaseConfig{ID: "case", Input: config.Input{Prompt: "hello"}}, ConfigurationWithSkill, rt, nil)
	if result.Status != judge.StatusPass || len(result.JudgeResults) != 2 {
		t.Fatalf("result=%+v", result)
	}
	if err := ws.WriteEvaluation("case", ConfigurationWithSkill, &report.GroupedEvaluation{
		Version: 1, JudgeResults: result.JudgeResults,
		Aggregation: report.GroupAggregation{Strategy: result.AggregationStrategy, Status: result.Status},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(ws.ConfigDir("case", ConfigurationWithSkill), "evaluation.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted report.GroupedEvaluation
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	outputs := filepath.Join(ws.ConfigDir("case", ConfigurationWithSkill), "outputs")
	for _, outcome := range persisted.JudgeResults {
		assertPersistedJudgeContext(t, outputs, outcome)
	}
}

func assertPersistedJudgeContext(t *testing.T, outputs string, outcome judge.Outcome) {
	t.Helper()
	metadata := outcome.Result.JudgeContext
	want := "judge/" + outcome.ID + "/context"
	if metadata == nil || metadata.MaterializedDir != want || metadata.Manifest == nil {
		t.Fatalf("metadata=%+v want=%s", metadata, want)
	}
	manifestData, err := os.ReadFile(filepath.Join(outputs, filepath.FromSlash(metadata.MaterializedDir), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest judge.ContextManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.MaterializedDir != want {
		t.Fatalf("manifest dir=%q", manifest.MaterializedDir)
	}
	for _, source := range []*judge.ContextManifest{metadata.Manifest, &manifest} {
		checked := 0
		for _, material := range source.Materials {
			if material.Path == "" {
				continue
			}
			checked++
			if _, err := os.Stat(filepath.Join(outputs, filepath.FromSlash(material.Path))); err != nil {
				t.Fatalf("judge %s material %s: %v", outcome.ID, material.Path, err)
			}
		}
		if checked == 0 {
			t.Fatal("no persisted material paths checked")
		}
	}
}

func TestMultiJudgeMultiTurnEarlyExitPreservesSkippedOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		runErr     error
		wantStatus judge.Status
		wantReason string
	}{
		{name: "post_condition", wantStatus: judge.StatusFail, wantReason: "post_condition_failed"},
		{name: "agent_error", runErr: errors.New("execution failed"), wantStatus: judge.StatusError, wantReason: "agent_execution_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, err := runtime.NewRuntime(runtime.Config{Type: "none", WorkspaceDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if err := rt.Create(context.Background()); err != nil {
				t.Fatal(err)
			}
			ag := &mockResumerAgent{mockAgent: mockAgent{name: "test"}, runTurnFunc: func(context.Context, runtime.Runtime, agent.ExecOptions, transcript.Message, string) (*agent.SessionResult, error) {
				return &agent.SessionResult{FinalMessage: "placeholder", SessionID: "session", Turns: 1}, tc.runErr
			}}
			members := []config.JudgeConfig{{ID: "first", Type: "agent_judge", Model: "test"}, {ID: "second", Type: "agent_judge", Model: "test"}}
			e := newTestEvaluator(EvalOptions{Agent: ag, EvalCfg: &config.EvalConfig{Judges: &members}})
			c := &config.CaseConfig{ID: "early-exit", Input: config.Input{Turns: []config.Turn{
				{Role: "user", Content: "first", PostCondition: &config.PostCondition{MustContainAll: []string{"required"}, OnFail: "fail"}},
				{Role: "user", Content: "second"},
			}}}
			result := e.executeCase(context.Background(), c, ConfigurationWithSkill, rt, nil)
			if result.Status != tc.wantStatus || result.Configuration != ConfigurationWithSkill || result.AggregationStrategy != "all_required" || len(result.JudgeResults) != len(members) {
				t.Fatalf("unexpected result: %+v", result)
			}
			if ag.turnCall != 1 || ag.runCall.Load() != 0 {
				t.Fatalf("unexpected agent or judge calls: turns=%d runs=%d", ag.turnCall, ag.runCall.Load())
			}
			assertSkippedJudgeOutcomes(t, result.JudgeResults, members, tc.wantReason)
		})
	}
}

func assertSkippedJudgeOutcomes(t *testing.T, outcomes []JudgeOutcome, members []config.JudgeConfig, reason string) {
	t.Helper()
	for i, outcome := range outcomes {
		if outcome.ID != members[i].ID || outcome.Type != members[i].Type || outcome.Status != judge.StatusSkip || outcome.SkipReason != reason || outcome.Result != nil {
			t.Fatalf("unexpected outcome: %+v", outcome)
		}
	}
}

func TestMultiJudgeSingleTurnExecutionErrorPreservesSkippedOutcomes(t *testing.T) {
	for _, execErr := range []error{errors.New("execution failed"), context.DeadlineExceeded} {
		t.Run(execErr.Error(), func(t *testing.T) {
			rt, err := runtime.NewRuntime(runtime.Config{Type: "none", WorkspaceDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if err := rt.Create(context.Background()); err != nil {
				t.Fatal(err)
			}
			ag := &mockAgent{name: "test", runFunc: func(context.Context, runtime.Runtime, agent.ExecOptions, []transcript.Message) (*agent.SessionResult, error) {
				return nil, execErr
			}}
			members := []config.JudgeConfig{{ID: "first", Type: "agent_judge", Model: "test"}, {ID: "second", Type: "agent_judge", Model: "test"}}
			e := newTestEvaluator(EvalOptions{Agent: ag, EvalCfg: &config.EvalConfig{Judges: &members}})
			c := &config.CaseConfig{ID: "execution-error", Input: config.Input{Prompt: "first"}}
			result := e.executeCase(context.Background(), c, ConfigurationWithSkill, rt, nil)
			if result.Status != judge.StatusError || result.AggregationStrategy != "all_required" || len(result.JudgeResults) != len(members) || ag.runCall.Load() != 1 {
				t.Fatalf("unexpected result: %+v", result)
			}
			assertSkippedJudgeOutcomes(t, result.JudgeResults, members, "agent_execution_failed")
		})
	}
}

func assertWorkspaceAttachment(t *testing.T, workspace string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workspace, ".skill-up", "judge", "context", "attachments", "01-result.txt"))
	if err != nil || string(data) != "original" {
		t.Fatalf("judge attachment=%q, %v", data, err)
	}
}

func TestJudgeConfigForWorkspacePreservesOriginalAndExternalAttachments(t *testing.T) {
	source, fork := t.TempDir(), t.TempDir()
	workspacePath := filepath.Join(source, "nested", "result.txt")
	externalPath := filepath.Join(t.TempDir(), "reference.txt")
	relativePath := filepath.Join("fixtures", "reference.txt")
	member := config.JudgeConfig{Context: &config.JudgeContextConfig{
		Profile: "standard", Attachments: []config.JudgeContextAttachment{
			{Path: workspacePath, Label: "output"}, {Path: externalPath}, {Path: relativePath},
		},
	}}
	mapped := judgeConfigForWorkspace(member, source, fork)
	want := []string{filepath.Join(fork, "nested", "result.txt"), externalPath, relativePath}
	for i, path := range want {
		if mapped.Context.Attachments[i].Path != path {
			t.Fatalf("attachment %d = %q, want %q", i, mapped.Context.Attachments[i].Path, path)
		}
	}
	if mapped.Context.Profile != member.Context.Profile || mapped.Context.Attachments[0].Label != "output" || member.Context.Attachments[0].Path != workspacePath {
		t.Fatalf("configuration changed: original=%+v mapped=%+v", member.Context, mapped.Context)
	}
}

func TestMultiJudgeUserSimulatorGroupedOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		simulator usersimulator.Simulator
		status    judge.Status
		turns     int
	}{
		{"completed", &scriptedSimulator{decisions: []usersimulator.Decision{{Action: "reply", Message: "follow up"}, {Action: "stop"}}}, judge.StatusPass, 2},
		{"simulator_error", failingSimulator{}, judge.StatusError, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, err := runtime.NewRuntime(runtime.Config{Type: "none", WorkspaceDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if err := rt.Create(context.Background()); err != nil {
				t.Fatal(err)
			}
			ag := &mockResumerAgent{mockAgent: mockAgent{name: "test"}}
			members := []config.JudgeConfig{{ID: "first", Type: "rule_based"}, {ID: "second", Type: "rule_based"}}
			e := newTestEvaluator(EvalOptions{Agent: ag, Simulator: tc.simulator, EvalCfg: &config.EvalConfig{
				Judges: &members, Cases: config.CasesConfig{Defaults: config.CaseDefaults{MaxTurns: 3}},
			}})
			c := &config.CaseConfig{ID: "simulated", Input: config.Input{Prompt: "start"}, UserSimulator: &config.UserSimulatorScenario{Scenario: "follow up"}}
			result := e.executeCase(context.Background(), c, ConfigurationWithSkill, rt, nil)
			if result.Status != tc.status || result.TurnsTotal != tc.turns || len(result.JudgeResults) != len(members) || ag.turnCall != tc.turns {
				t.Fatalf("unexpected grouped result: %+v", result)
			}
			assertSimulatedJudgeStatuses(t, result, members)
		})
	}
}

func assertSimulatedJudgeStatuses(t *testing.T, result EvalResult, members []config.JudgeConfig) {
	t.Helper()
	if result.Status == judge.StatusError {
		assertSkippedJudgeOutcomes(t, result.JudgeResults, members, "agent_execution_failed")
		return
	}
	for _, outcome := range result.JudgeResults {
		if outcome.Status != judge.StatusPass || outcome.Result.TurnsTotal != result.TurnsTotal {
			t.Fatalf("unexpected completed outcome: %+v", outcome)
		}
	}
}

func TestJudgeInputForWorkspaceMapsGeneratedFilesWithoutChangingSource(t *testing.T) {
	source, fork := t.TempDir(), t.TempDir()
	workspacePath := filepath.Join(source, "result.txt")
	archivePath := filepath.Join(t.TempDir(), "archived.txt")
	input := judge.Input{WorkspacePath: source, GeneratedFiles: []string{workspacePath, archivePath, "relative.txt"}}
	mapped := judgeInputForWorkspace(input, fork)
	want := []string{filepath.Join(fork, "result.txt"), archivePath, "relative.txt"}
	for i, path := range want {
		if mapped.GeneratedFiles[i] != path {
			t.Fatalf("generated file %d = %q, want %q", i, mapped.GeneratedFiles[i], path)
		}
	}
	if mapped.WorkspacePath != fork || input.WorkspacePath != source || input.GeneratedFiles[0] != workspacePath {
		t.Fatalf("source input changed: %+v, mapped: %+v", input, mapped)
	}
}

func TestJudgeWorkspacePathMapsCanonicalSourceAlias(t *testing.T) {
	if goruntime.GOOS == windowsGOOS {
		t.Skip("symlink fixture requires host privileges on Windows")
	}
	source, fork := t.TempDir(), t.TempDir()
	alias := filepath.Join(t.TempDir(), "workspace-alias")
	if err := os.Symlink(source, alias); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(canonical, "result.txt")
	if mapped := judgeWorkspacePath(path, alias, fork); mapped != filepath.Join(fork, "result.txt") {
		t.Fatalf("canonical workspace path mapped to %q", mapped)
	}
}

func TestMultiJudgeUnsupportedSessionResumerPreservesSkippedOutcomes(t *testing.T) {
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	ag := &mockAgent{name: "test"}
	members := []config.JudgeConfig{{ID: "check", Type: "rule_based"}}
	e := newTestEvaluator(EvalOptions{Agent: ag, EvalCfg: &config.EvalConfig{Judges: &members}})
	c := &config.CaseConfig{ID: "simulated", Input: config.Input{Prompt: "start"}, UserSimulator: &config.UserSimulatorScenario{Scenario: "follow up"}}
	result := e.executeCase(context.Background(), c, ConfigurationWithSkill, rt, nil)
	if result.Status != judge.StatusError || len(result.JudgeResults) != len(members) || ag.runCall.Load() != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	assertSkippedJudgeOutcomes(t, result.JudgeResults, members, "session_resumption_unsupported")
}
