package evaluator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestMultiJudgeEvaluatesOneExecutionWithSharedWorkspace(t *testing.T) {
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
	result := e.executeCase(context.Background(), caseCfg, "with_skill", rt, nil)
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
	if err != nil || string(data) != "changed" {
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
		if err != nil || string(data) != "changed" {
			t.Fatalf("semantic judge saw %q, %v", data, err)
		}
		assertWorkspaceAttachment(t, current.Workspace())
		return &agent.SessionResult{FinalMessage: `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":["shared output"],"failures":[]}]}`}, nil
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
	members := []config.JudgeConfig{{ID: "first", Type: "agent_judge", Criteria: []string{"one"}}, {ID: "second", Type: "agent_judge", Criteria: []string{"one", "two", "three", "four"}}}
	e := newTestEvaluator(EvalOptions{Agent: ag, OutputDir: t.TempDir(), EvalCfg: &config.EvalConfig{
		Environment: config.Environment{Type: "none"}, Judges: &members,
	}})
	exitCode := 0
	caseCfg := &config.CaseConfig{ID: "gate", Input: config.Input{Prompt: "hello"}, Expect: config.Expect{MustContain: []string{"required"}, MustNotContain: []string{"forbidden"}, ExitCode: &exitCode}}
	result := e.executeCase(context.Background(), caseCfg, "with_skill", rt, nil)
	if result.Status != judge.StatusFail || len(result.JudgeResults) != 2 || ag.runCall.Load() != 1 || result.Grading == nil {
		t.Fatalf("status=%s outcomes=%+v runs=%d", result.Status, result.JudgeResults, ag.runCall.Load())
	}
	wantSummary := judge.ResultSummary{Total: len(members), Failed: len(members)}
	if result.Grading.Status != judge.StatusFail || result.Grading.Summary != wantSummary {
		t.Fatalf("gate compatibility grading=%+v", result.Grading.Summary)
	}
	for i, outcome := range result.JudgeResults {
		if outcome.Status != judge.StatusSkip || outcome.SkipReason != "gate_failed" || outcome.Result != nil || !result.Grading.AssertionResults[i].Skipped {
			t.Fatalf("outcome=%+v", outcome)
		}
	}
}

func TestMultiJudgeReusesRuntimeWithoutSnapshotCapability(t *testing.T) {
	ag := &mockAgent{name: "test", output: "done"}
	members := []config.JudgeConfig{{ID: "check", Type: "rule_based"}}
	e := newTestEvaluator(EvalOptions{Agent: ag, EvalCfg: &config.EvalConfig{Judges: &members}})
	result := e.executeCase(context.Background(), &config.CaseConfig{ID: "case", Input: config.Input{Prompt: "hello"}}, ConfigurationWithoutSkill, &mockRuntime{workspace: t.TempDir()}, nil)
	if result.Status != judge.StatusPass || result.Error != nil || ag.runCall.Load() != 1 || result.Configuration != ConfigurationWithoutSkill || len(result.JudgeResults) != 1 {
		t.Fatalf("result=%+v runs=%d", result, ag.runCall.Load())
	}
}

func TestMultiJudgeStartsFreshAgentSessionsInSameRuntime(t *testing.T) {
	rt := &mockRuntime{workspace: t.TempDir()}
	ag := &mockResumerAgent{mockAgent: mockAgent{name: "test"}}
	ag.runFunc = func(_ context.Context, current runtime.Runtime, _ agent.ExecOptions, messages []transcript.Message) (*agent.SessionResult, error) {
		if current != rt || len(messages) != 1 || messages[0].Role != transcript.RoleUser {
			t.Fatalf("runtime=%T messages=%+v", current, messages)
		}
		output := "completed"
		if ag.runCall.Load() > 1 {
			output = `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":["checked"],"failures":[]}]}`
		}
		return &agent.SessionResult{SessionID: fmt.Sprintf("session-%d", ag.runCall.Load()), FinalMessage: output}, nil
	}
	members := []config.JudgeConfig{{ID: "first", Type: "agent_judge", Criteria: []string{"quality"}}, {ID: "second", Type: "agent_judge", Criteria: []string{"quality"}}}
	e := newTestEvaluator(EvalOptions{Agent: ag, OutputDir: t.TempDir(), EvalCfg: &config.EvalConfig{Judges: &members}})
	result := e.executeCase(context.Background(), &config.CaseConfig{ID: "case", Input: config.Input{Prompt: "task"}}, ConfigurationWithoutSkill, rt, nil)
	if result.Status != judge.StatusPass || ag.runCall.Load() != 3 || ag.turnCall != 0 {
		t.Fatalf("result=%+v runs=%d resumes=%d", result, ag.runCall.Load(), ag.turnCall)
	}
	if result.SessionID != "session-1" || result.JudgeResults[0].Session.SessionID != "session-2" || result.JudgeResults[1].Session.SessionID != "session-3" {
		t.Fatalf("sessions=%+v", result)
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
	if err != nil || string(data) != "changed" {
		t.Fatalf("judge attachment=%q, %v", data, err)
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
