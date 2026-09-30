package evaluator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/runtime"
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
		{ID: "semantic", Type: "agent_judge", Model: "test", Criteria: []string{"quality"}},
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
	if result.Status != judge.StatusError || result.Error == nil || ag.runCall.Load() != 0 || result.Configuration != ConfigurationWithoutSkill {
		t.Fatalf("status=%s config=%q error=%v runs=%d", result.Status, result.Configuration, result.Error, ag.runCall.Load())
	}
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
