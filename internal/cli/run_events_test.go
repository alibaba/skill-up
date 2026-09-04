package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/evalevent"
	"github.com/alibaba/skill-up/internal/evaluator"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/runner"
)

type evaluationEventOptionsTestCase struct {
	name       string
	values     map[string][]string
	wantErr    string
	wantPath   string
	wantAttrs  map[string]string
	wantEnable bool
}

func TestEvaluationEventOptionsFromFlags(t *testing.T) {
	t.Parallel()

	tests := []evaluationEventOptionsTestCase{
		{name: "disabled"},
		{
			name:    "attribute requires log",
			values:  map[string][]string{eventAttributeFlagName: {"com.example.build_id=1"}},
			wantErr: "--event-attribute requires --event-log",
		},
		{
			name:    "empty log",
			values:  map[string][]string{eventLogFlagName: {""}},
			wantErr: "path must not be empty",
		},
		{
			name:    "stdout rejected",
			values:  map[string][]string{eventLogFlagName: {"-"}},
			wantErr: "does not support stdout",
		},
		{
			name: "invalid attribute",
			values: map[string][]string{
				eventLogFlagName:       {"events.jsonl"},
				eventAttributeFlagName: {"missing-delimiter"},
			},
			wantErr: "expected non-empty key=value",
		},
		{
			name: "duplicate attribute",
			values: map[string][]string{
				eventLogFlagName:       {"events.jsonl"},
				eventAttributeFlagName: {"com.example.build_id=1", "com.example.build_id=2"},
			},
			wantErr: "duplicate --event-attribute key",
		},
		{
			name: "reserved attribute",
			values: map[string][]string{
				eventLogFlagName:       {"events.jsonl"},
				eventAttributeFlagName: {"skill-up.internal=value"},
			},
			wantErr: "reserved skill-up namespace",
		},
		{
			name: "valid",
			values: map[string][]string{
				eventLogFlagName: {"events.jsonl"},
				eventAttributeFlagName: {
					"com.example.build_id=build=1",
					"com.example.retry=2",
				},
			},
			wantPath: "events.jsonl",
			wantAttrs: map[string]string{
				"com.example.build_id": "build=1",
				"com.example.retry":    "2",
			},
			wantEnable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkEvaluationEventOptions(t, tt)
		})
	}
}

func checkEvaluationEventOptions(t *testing.T, tt evaluationEventOptionsTestCase) {
	t.Helper()
	cmd := newEvaluationEventFlagCommand()
	for name, values := range tt.values {
		for _, value := range values {
			if err := cmd.Flags().Set(name, value); err != nil {
				t.Fatal(err)
			}
		}
	}

	got, err := evaluationEventOptionsFromFlags(cmd)
	if tt.wantErr != "" {
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled != tt.wantEnable || got.Path != tt.wantPath {
		t.Fatalf("options = %+v, want enabled=%t path=%q", got, tt.wantEnable, tt.wantPath)
	}
	if len(got.Attributes) != len(tt.wantAttrs) {
		t.Fatalf("attributes = %v, want %v", got.Attributes, tt.wantAttrs)
	}
	for key, value := range tt.wantAttrs {
		if got.Attributes[key] != value {
			t.Errorf("attribute %q = %q, want %q", key, got.Attributes[key], value)
		}
	}
}

func newEvaluationEventFlagCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "run"}
	cmd.Flags().String(eventLogFlagName, "", "")
	cmd.Flags().StringArray(eventAttributeFlagName, nil, "")
	return cmd
}

func TestValidateEvaluationEventLogPathSafePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, "events.jsonl")
	got, err := validateEvaluationEventLogPath(path, eventPathTestPlan(workspace))
	if err != nil {
		t.Fatal(err)
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(resolvedWorkspace, "events.jsonl")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestValidateEvaluationEventLogPathMissingParent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	_, err := validateEvaluationEventLogPath(
		filepath.Join(root, "missing", "events.jsonl"),
		eventPathTestPlan(workspace),
	)
	if err == nil || !strings.Contains(err.Error(), "parent directory") {
		t.Fatalf("error = %v, want missing parent error", err)
	}
}

func TestValidateEvaluationEventLogPathInsideIteration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	iteration := filepath.Join(workspace, "iteration-1")
	if err := os.MkdirAll(iteration, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := validateEvaluationEventLogPath(filepath.Join(iteration, "events.jsonl"), eventPathTestPlan(workspace))
	if err == nil || !strings.Contains(err.Error(), "inside scheduled iteration directory") {
		t.Fatalf("error = %v, want iteration conflict", err)
	}
}

func TestValidateEvaluationEventLogPathInsideCaseInsensitiveIteration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	actualIteration := filepath.Join(workspace, "Iteration-1")
	if err := os.MkdirAll(actualIteration, 0o755); err != nil {
		t.Fatal(err)
	}
	scheduledIteration := filepath.Join(workspace, "iteration-1")
	actualInfo, err := os.Stat(actualIteration)
	if err != nil {
		t.Fatal(err)
	}
	scheduledInfo, err := os.Stat(scheduledIteration)
	if os.IsNotExist(err) {
		t.Skip("filesystem is case-sensitive")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(actualInfo, scheduledInfo) {
		t.Skip("filesystem resolves case variants to different directories")
	}

	_, err = validateEvaluationEventLogPath(
		filepath.Join(actualIteration, "events.jsonl"),
		eventPathTestPlan(workspace),
	)
	if err == nil || !strings.Contains(err.Error(), "inside scheduled iteration directory") {
		t.Fatalf("error = %v, want case-insensitive iteration conflict", err)
	}
}

func TestValidateEvaluationEventLogPathWorkspaceCollision(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	_, err := validateEvaluationEventLogPath(workspace, eventPathTestPlan(workspace))
	if err == nil || !strings.Contains(err.Error(), "conflicts with the evaluation workspace") {
		t.Fatalf("error = %v, want workspace conflict", err)
	}
}

func TestValidateEvaluationEventLogPathSymlinkIntoIteration(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires additional privileges on Windows")
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	iteration := filepath.Join(workspace, "iteration-1")
	if err := os.MkdirAll(iteration, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(iteration, alias); err != nil {
		t.Fatal(err)
	}
	_, err := validateEvaluationEventLogPath(filepath.Join(alias, "events.jsonl"), eventPathTestPlan(workspace))
	if err == nil || !strings.Contains(err.Error(), "inside scheduled iteration directory") {
		t.Fatalf("error = %v, want symlink iteration conflict", err)
	}
}

func eventPathTestPlan(workspace string) runner.ExecutionPlan {
	return runner.ExecutionPlan{
		WorkspaceDir: workspace,
		TaskPlan: evaluator.Plan{Iterations: []evaluator.IterationPlan{
			{Number: 1},
		}},
	}
}

func TestStartEvaluationEventStreamValidatesBeforeTruncating(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "events.jsonl")
	const original = "keep me\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := startEvaluationEventStream(
		context.Background(),
		path,
		nil,
		evalevent.Plan{},
		"test-engine",
		"test-skill",
	)
	if err == nil || !strings.Contains(err.Error(), "invalid event plan") {
		t.Fatalf("error = %v, want invalid plan", err)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != original {
		t.Fatalf("event log was truncated: %q", content)
	}
}

func TestRunEvalRejectsDryRunBeforeOpeningEventLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	const original = "existing stream\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newRunPhaseTestCommand(t)
	if err := cmd.Flags().Set("dry-run", testFlagBoolTrue); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set(eventLogFlagName, path); err != nil {
		t.Fatal(err)
	}
	err := runEval(cmd, []string{testEvalPath})
	if err == nil || !strings.Contains(err.Error(), "--event-log cannot be used with --dry-run") {
		t.Fatalf("error = %v, want dry-run conflict", err)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != original {
		t.Fatalf("event log was modified: %q", content)
	}
}

func TestEvaluationEventStreamContinuesStateAfterSinkFailure(t *testing.T) {
	t.Parallel()

	sink := &failOnPublishSink{failAt: 3}
	publisher, err := evalevent.NewPublisher(evalevent.PublisherConfig{Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	plan := evalevent.Plan{Iterations: []evalevent.Iteration{{
		Number: 1,
		Tasks: []evalevent.Task{{
			ID:            "task-1",
			Iteration:     1,
			CaseID:        "case-1",
			Configuration: evalevent.ConfigurationWithSkill,
			Index:         1,
			Title:         "Case 1",
		}},
	}}}
	lifecycle, err := evalevent.NewLifecycle(publisher, plan, evalevent.LifecycleOptions{HeartbeatInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &evaluationEventAdapter{lifecycle: lifecycle}
	stream := &evaluationEventStream{publisher: publisher, lifecycle: lifecycle, adapter: adapter}
	ctx := context.Background()
	if err := lifecycle.Start(ctx, "test-engine", "test-skill"); err != nil {
		t.Fatal(err)
	}

	iteration := evaluator.IterationPlan{Number: 1}
	task := evaluator.PlannedTask{ID: "task-1"}
	adapter.setPhase(ctx, evalevent.RunPhaseExecuting)
	adapter.OnIterationStart(ctx, iteration)
	adapter.OnTaskStart(ctx, task)
	adapter.OnTaskComplete(ctx, task, evaluator.EvalResult{
		Status: judge.StatusPass,
		Grading: &judge.Result{Summary: judge.ResultSummary{
			PassRate: 1,
		}},
	})
	adapter.OnIterationComplete(ctx, iteration, nil)

	err = stream.finish(ctx, evalevent.RunStatusCompleted)
	if err == nil || !strings.Contains(err.Error(), "forced event write failure") {
		t.Fatalf("finish error = %v, want sink failure", err)
	}
	if strings.Count(err.Error(), "publish run_progress event") != 1 {
		t.Fatalf("sink failure was aggregated more than once: %v", err)
	}
	if sink.publishCount() != 3 {
		t.Fatalf("publish attempts = %d, want 3", sink.publishCount())
	}
}

func TestEvaluationEventAdapterCanFinalizeAfterInvocationCancellation(t *testing.T) {
	t.Parallel()

	sink := &failOnPublishSink{}
	publisher, err := evalevent.NewPublisher(evalevent.PublisherConfig{Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	plan := evalevent.Plan{Iterations: []evalevent.Iteration{{
		Number: 1,
		Tasks: []evalevent.Task{{
			ID:            "task-1",
			Iteration:     1,
			CaseID:        "case-1",
			Configuration: evalevent.ConfigurationWithSkill,
			Index:         1,
		}},
	}}}
	lifecycle, err := evalevent.NewLifecycle(publisher, plan, evalevent.LifecycleOptions{HeartbeatInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &evaluationEventAdapter{lifecycle: lifecycle}
	stream := &evaluationEventStream{publisher: publisher, lifecycle: lifecycle, adapter: adapter}
	if err := lifecycle.Start(context.Background(), "test-engine", "test-skill"); err != nil {
		t.Fatal(err)
	}

	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	iteration := evaluator.IterationPlan{Number: 1}
	task := evaluator.PlannedTask{ID: "task-1"}
	adapter.setPhase(cancelledContext, evalevent.RunPhaseExecuting)
	adapter.OnIterationStart(cancelledContext, iteration)
	adapter.OnTaskStart(cancelledContext, task)
	adapter.OnTaskComplete(cancelledContext, task, evaluator.EvalResult{Status: judge.StatusError})
	adapter.OnIterationComplete(cancelledContext, iteration, nil)

	if err := stream.finish(context.WithoutCancel(cancelledContext), evalevent.RunStatusCancelled); err != nil {
		t.Fatal(err)
	}
	if sink.publishCount() != 11 {
		t.Fatalf("published events = %d, want 11", sink.publishCount())
	}
}

type failOnPublishSink struct {
	mu       sync.Mutex
	failAt   int
	writes   int
	closed   bool
	closeErr error
}

func (s *failOnPublishSink) Publish(_ context.Context, _ evalevent.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	if s.writes == s.failAt {
		return errors.New("forced event write failure")
	}
	return nil
}

func (s *failOnPublishSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.closeErr
}

func (s *failOnPublishSink) publishCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

func TestEvaluationEventPlan(t *testing.T) {
	t.Parallel()

	caseConfig := &config.CaseConfig{ID: "case-1", Title: "Case 1"}
	taskPlan := evaluator.BuildPlan(
		[]*config.CaseConfig{caseConfig},
		3,
		2,
		[]string{evaluator.ConfigurationWithSkill, evaluator.ConfigurationWithoutSkill},
	)
	got, err := evaluationEventPlan(runner.ExecutionPlan{TaskPlan: taskPlan})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Iterations) != 2 || got.Iterations[0].Number != 3 || got.Iterations[1].Number != 4 {
		t.Fatalf("iterations = %+v", got.Iterations)
	}
	if len(got.Iterations[0].Tasks) != 2 || got.Iterations[0].Tasks[0].Index != 1 || got.Iterations[1].Tasks[1].Index != 4 {
		t.Fatalf("tasks = %+v", got.Iterations)
	}
	if got.Iterations[0].Tasks[1].Configuration != evalevent.ConfigurationWithoutSkill {
		t.Fatalf("configuration = %q", got.Iterations[0].Tasks[1].Configuration)
	}
}

func TestEvaluationEventPlanRejectsInconsistentTaskMetadata(t *testing.T) {
	t.Parallel()

	caseConfig := &config.CaseConfig{ID: "case-1"}
	taskPlan := evaluator.BuildPlan(
		[]*config.CaseConfig{caseConfig},
		1,
		1,
		[]string{evaluator.ConfigurationWithSkill},
	)
	taskPlan.Iterations[0].Tasks[0].Iteration = 2
	_, err := evaluationEventPlan(runner.ExecutionPlan{TaskPlan: taskPlan})
	if err == nil || !strings.Contains(err.Error(), "does not match iteration") {
		t.Fatalf("error = %v, want iteration mismatch", err)
	}
}

func TestEventRunStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		cancelledContext bool
		err              error
		want             evalevent.RunStatus
	}{
		{name: "completed", want: evalevent.RunStatusCompleted},
		{name: "error", err: errors.New("boom"), want: evalevent.RunStatusError},
		{name: "cancelled error", err: context.Canceled, want: evalevent.RunStatusCancelled},
		{name: "cancelled context", cancelledContext: true, want: evalevent.RunStatusCancelled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			if tt.cancelledContext {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			if got := eventRunStatus(ctx, tt.err); got != tt.want {
				t.Fatalf("status = %q, want %q", got, tt.want)
			}
		})
	}
}
