package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/evaluator"
)

func TestBuildExecutionPlanResolvesInvocationMetadataWithoutWorkspaceMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	skillDir := filepath.Join(root, "directory-name")
	evalsDir := filepath.Join(skillDir, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatalf("create evals directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: declared-name\n---\n"), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	workspaceDir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(workspaceDir, "iteration-2"), 0o755); err != nil {
		t.Fatalf("create existing iteration: %v", err)
	}

	r := NewRunner(&config.EvalConfig{
		Benchmark: config.BenchmarkConfig{Enabled: true},
	}, config.NewLoader(filepath.Join(evalsDir, "eval.yaml")), nil, credential.ResolvedAgentConfig{})
	cases := []*config.CaseConfig{
		{ID: "case-1", Title: "Case 1"},
		{ID: "case-2", Title: "Case 2"},
	}

	plan := r.BuildExecutionPlan(cases, EvaluateOptions{OutputDir: workspaceDir})

	if plan.SkillDir != skillDir {
		t.Errorf("SkillDir = %q, want %q", plan.SkillDir, skillDir)
	}
	if plan.SkillName != "directory-name" {
		t.Errorf("SkillName = %q, want directory-name", plan.SkillName)
	}
	if plan.ReportName != "declared-name" {
		t.Errorf("ReportName = %q, want declared-name", plan.ReportName)
	}
	if plan.WorkspaceDir != workspaceDir {
		t.Errorf("WorkspaceDir = %q, want %q", plan.WorkspaceDir, workspaceDir)
	}
	if plan.CaseCount != 2 {
		t.Errorf("CaseCount = %d, want 2", plan.CaseCount)
	}
	if plan.TaskPlan.StartIteration != 3 || len(plan.TaskPlan.Iterations) != 1 {
		t.Fatalf("task iterations = start %d count %d, want start 3 count 1", plan.TaskPlan.StartIteration, len(plan.TaskPlan.Iterations))
	}
	if plan.TaskPlan.TaskTotal != 4 {
		t.Errorf("TaskTotal = %d, want 4", plan.TaskPlan.TaskTotal)
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, "iteration-3")); !os.IsNotExist(err) {
		t.Fatalf("BuildExecutionPlan mutated future workspace, stat err = %v", err)
	}
}

func TestEvaluatePlanForwardsIterationAndGlobalTaskBoundaries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	skillDir := filepath.Join(root, "test-skill")
	evalsDir := filepath.Join(skillDir, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatalf("create evals directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: test-skill\n---\n"), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	r := NewRunner(&config.EvalConfig{
		Environment: config.Environment{Type: "none"},
		Cases:       config.CasesConfig{Parallelism: 1},
	}, config.NewLoader(filepath.Join(evalsDir, "eval.yaml")), nil, credential.ResolvedAgentConfig{})
	observer := &recordingExecutionObserver{}
	opts := EvaluateOptions{
		OutputDir:         filepath.Join(root, "workspace"),
		Iteration:         2,
		TaskObserver:      observer,
		IterationObserver: observer,
	}
	plan := r.BuildExecutionPlan([]*config.CaseConfig{{
		ID:    "case-1",
		Title: "Case 1",
		Input: config.Input{Prompt: "hello"},
	}}, opts)

	results, err := r.EvaluatePlan(context.Background(), plan, &runnerTestAgent{}, opts)
	if err != nil {
		t.Fatalf("EvaluatePlan returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	want := []string{
		"iteration-start:1",
		"task-start:task-1:1/2:1",
		"task-complete:task-1:1/2:1",
		"iteration-complete:1:1",
		"iteration-start:2",
		"task-start:task-2:2/2:2",
		"task-complete:task-2:2/2:2",
		"iteration-complete:2:1",
	}
	if got := observer.snapshot(); !equalStringSlices(got, want) {
		t.Fatalf("observer events = %v, want %v", got, want)
	}
}

func TestEvaluatePlanStopsBeforeFutureIterationAfterCancellation(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "test-skill")
	evalsDir := filepath.Join(skillDir, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatalf("create evals directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: test-skill\n---\n"), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	workspaceDir := filepath.Join(root, "workspace")
	futureArtifact := filepath.Join(workspaceDir, "iteration-2", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(futureArtifact), 0o755); err != nil {
		t.Fatalf("create future iteration directory: %v", err)
	}
	if err := os.WriteFile(futureArtifact, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write future iteration artifact: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	origNewEvaluator := newEvaluator
	t.Cleanup(func() { newEvaluator = origNewEvaluator })
	evaluateCalls := 0
	newEvaluator = func(evaluator.EvalOptions) evaluator.Evaluator {
		return evaluatorStub{evaluateAll: func(context.Context, []*config.CaseConfig) ([]evaluator.EvalResult, error) {
			evaluateCalls++
			cancel()
			return []evaluator.EvalResult{{CaseID: "case-1", Configuration: evaluator.ConfigurationWithSkill}}, nil
		}}
	}

	r := NewRunner(&config.EvalConfig{
		Environment: config.Environment{Type: "none"},
		Cases:       config.CasesConfig{Parallelism: 1},
	}, config.NewLoader(filepath.Join(evalsDir, "eval.yaml")), nil, credential.ResolvedAgentConfig{})
	observer := &recordingExecutionObserver{}
	opts := EvaluateOptions{
		OutputDir:         workspaceDir,
		Iteration:         2,
		IterationObserver: observer,
	}
	plan := r.BuildExecutionPlan([]*config.CaseConfig{{
		ID:    "case-1",
		Title: "Case 1",
	}}, opts)

	results, err := r.EvaluatePlan(ctx, plan, &runnerTestAgent{}, opts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("EvaluatePlan error = %v, want context.Canceled", err)
	}
	if len(results) != 0 {
		t.Fatalf("len(results) = %d, want no completed iterations", len(results))
	}
	if evaluateCalls != 1 {
		t.Fatalf("evaluator calls = %d, want 1", evaluateCalls)
	}
	if got, want := observer.snapshot(), []string{"iteration-start:1"}; !equalStringSlices(got, want) {
		t.Fatalf("observer events = %v, want %v", got, want)
	}
	if data, readErr := os.ReadFile(futureArtifact); readErr != nil || string(data) != "keep" {
		t.Fatalf("future iteration artifact changed: data=%q err=%v", data, readErr)
	}
}

type recordingExecutionObserver struct {
	mu     sync.Mutex
	events []string
}

func (o *recordingExecutionObserver) OnIterationStart(_ context.Context, iteration evaluator.IterationPlan) {
	o.append(fmt.Sprintf("iteration-start:%d", iteration.Number))
}

func (o *recordingExecutionObserver) OnIterationComplete(_ context.Context, iteration evaluator.IterationPlan, results []evaluator.EvalResult) {
	o.append(fmt.Sprintf("iteration-complete:%d:%d", iteration.Number, len(results)))
}

func (o *recordingExecutionObserver) OnTaskStart(_ context.Context, task evaluator.PlannedTask) {
	o.append(fmt.Sprintf("task-start:%s:%d/%d:%d", task.ID, task.GlobalIndex, task.GlobalTotal, task.Iteration))
}

func (o *recordingExecutionObserver) OnTaskComplete(_ context.Context, task evaluator.PlannedTask, _ evaluator.EvalResult) {
	o.append(fmt.Sprintf("task-complete:%s:%d/%d:%d", task.ID, task.GlobalIndex, task.GlobalTotal, task.Iteration))
}

func (o *recordingExecutionObserver) append(event string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *recordingExecutionObserver) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.events...)
}

func equalStringSlices(got, want []string) bool {
	return slices.Equal(got, want)
}
