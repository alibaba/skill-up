package evaluator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/judge"
)

func TestBuildPlanExpandsTasksDeterministically(t *testing.T) {
	t.Parallel()

	cases := []*config.CaseConfig{
		{ID: "case/alpha", Title: "Alpha"},
		{ID: "用例-beta", Title: "Beta"},
	}

	plan := BuildPlan(cases, 3, 2, []string{ConfigurationWithSkill, ConfigurationWithoutSkill})

	if plan.StartIteration != 3 {
		t.Fatalf("StartIteration = %d, want 3", plan.StartIteration)
	}
	if plan.TaskTotal != 8 {
		t.Fatalf("TaskTotal = %d, want 8", plan.TaskTotal)
	}
	if len(plan.Iterations) != 2 {
		t.Fatalf("len(Iterations) = %d, want 2", len(plan.Iterations))
	}

	type expectedTask struct {
		id            string
		index         int
		iteration     int
		caseID        string
		configuration string
	}
	want := []expectedTask{
		{id: "task-1", index: 1, iteration: 3, caseID: "case/alpha", configuration: "with_skill"},
		{id: "task-2", index: 2, iteration: 3, caseID: "case/alpha", configuration: "without_skill"},
		{id: "task-3", index: 3, iteration: 3, caseID: "用例-beta", configuration: "with_skill"},
		{id: "task-4", index: 4, iteration: 3, caseID: "用例-beta", configuration: "without_skill"},
		{id: "task-5", index: 5, iteration: 4, caseID: "case/alpha", configuration: "with_skill"},
		{id: "task-6", index: 6, iteration: 4, caseID: "case/alpha", configuration: "without_skill"},
		{id: "task-7", index: 7, iteration: 4, caseID: "用例-beta", configuration: "with_skill"},
		{id: "task-8", index: 8, iteration: 4, caseID: "用例-beta", configuration: "without_skill"},
	}

	got := make([]PlannedTask, 0, plan.TaskTotal)
	for iterationIndex, iteration := range plan.Iterations {
		if iteration.Number != 3+iterationIndex {
			t.Errorf("Iterations[%d].Number = %d, want %d", iterationIndex, iteration.Number, 3+iterationIndex)
		}
		got = append(got, iteration.Tasks...)
	}
	if len(got) != len(want) {
		t.Fatalf("planned task count = %d, want %d", len(got), len(want))
	}
	for i, expected := range want {
		task := got[i]
		if task.ID != expected.id {
			t.Errorf("task[%d].ID = %q, want %q", i, task.ID, expected.id)
		}
		if task.GlobalIndex != expected.index {
			t.Errorf("task[%d].GlobalIndex = %d, want %d", i, task.GlobalIndex, expected.index)
		}
		if task.GlobalTotal != plan.TaskTotal {
			t.Errorf("task[%d].GlobalTotal = %d, want %d", i, task.GlobalTotal, plan.TaskTotal)
		}
		if task.Iteration != expected.iteration {
			t.Errorf("task[%d].Iteration = %d, want %d", i, task.Iteration, expected.iteration)
		}
		if task.Case.ID != expected.caseID {
			t.Errorf("task[%d].Case.ID = %q, want %q", i, task.Case.ID, expected.caseID)
		}
		if task.Configuration != expected.configuration {
			t.Errorf("task[%d].Configuration = %q, want %q", i, task.Configuration, expected.configuration)
		}
	}
}

func TestBuildPlanDefaultsInvalidIterationBounds(t *testing.T) {
	t.Parallel()

	plan := BuildPlan([]*config.CaseConfig{{ID: "case-1"}}, 0, 0, []string{ConfigurationWithSkill})

	if plan.StartIteration != 1 {
		t.Fatalf("StartIteration = %d, want 1", plan.StartIteration)
	}
	if len(plan.Iterations) != 1 || plan.Iterations[0].Number != 1 {
		t.Fatalf("Iterations = %#v, want one iteration numbered 1", plan.Iterations)
	}
	if plan.TaskTotal != 1 || len(plan.Iterations[0].Tasks) != 1 {
		t.Fatalf("plan task shape = total %d, tasks %d; want 1 and 1", plan.TaskTotal, len(plan.Iterations[0].Tasks))
	}
}

func TestEvaluatePlanNotifiesTaskAndProgressObservers(t *testing.T) {
	t.Parallel()

	cases := []*config.CaseConfig{{
		ID:    "case-1",
		Title: "Case 1",
		Input: config.Input{Prompt: "hello"},
	}}
	plan := BuildPlan(cases, 4, 1, []string{ConfigurationWithSkill, ConfigurationWithoutSkill})
	taskObserver := &recordingTaskObserver{}
	progressObserver := &recordingProgressObserver{}
	e := newTestEvaluator(EvalOptions{
		Concurrency:  1,
		WithBaseline: true,
		Agent:        &mockAgent{name: "test", output: "ok"},
		EvalCfg: &config.EvalConfig{
			Environment: config.Environment{Type: "none"},
		},
		Observer:     progressObserver,
		TaskObserver: taskObserver,
	})

	results, err := e.EvaluatePlan(context.Background(), plan.Iterations[0].Tasks)
	if err != nil {
		t.Fatalf("EvaluatePlan returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Configuration != "with_skill" || results[1].Configuration != "without_skill" {
		t.Fatalf("result configurations = %q, %q; want with_skill, without_skill", results[0].Configuration, results[1].Configuration)
	}

	wantTaskEvents := []string{
		"start:task-1:4:with_skill",
		"complete:task-1:4:with_skill",
		"start:task-2:4:without_skill",
		"complete:task-2:4:without_skill",
	}
	if got := taskObserver.snapshot(); !equalStrings(got, wantTaskEvents) {
		t.Fatalf("task events = %v, want %v", got, wantTaskEvents)
	}
	wantProgressEvents := []string{
		"start:1/2:case-1",
		"complete:1/2:case-1",
		"start:2/2:case-1",
		"complete:2/2:case-1",
	}
	if got := progressObserver.snapshot(); !equalStrings(got, wantProgressEvents) {
		t.Fatalf("progress events = %v, want %v", got, wantProgressEvents)
	}
}

func TestEvaluatePlanStopsSchedulingAfterCancellation(t *testing.T) {
	t.Parallel()

	cases := []*config.CaseConfig{
		{ID: "case-1", Title: "Case 1", Input: config.Input{Prompt: "one"}},
		{ID: "case-2", Title: "Case 2", Input: config.Input{Prompt: "two"}},
		{ID: "case-3", Title: "Case 3", Input: config.Input{Prompt: "three"}},
	}
	plan := BuildPlan(cases, 1, 1, []string{ConfigurationWithSkill})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	taskObserver := &cancelOnFirstTaskStartObserver{
		recordingTaskObserver: &recordingTaskObserver{},
		cancel:                cancel,
	}
	e := newTestEvaluator(EvalOptions{
		Concurrency: 1,
		Agent:       &mockAgent{name: "test", output: "ok"},
		EvalCfg: &config.EvalConfig{
			Environment: config.Environment{Type: "none"},
		},
		TaskObserver: taskObserver,
	})

	results, err := e.EvaluatePlan(ctx, plan.Iterations[0].Tasks)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("EvaluatePlan error = %v, want context.Canceled", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want only the scheduled task", len(results))
	}
	wantTaskEvents := []string{
		"start:task-1:1:with_skill",
		"complete:task-1:1:with_skill",
	}
	if got := taskObserver.snapshot(); !equalStrings(got, wantTaskEvents) {
		t.Fatalf("task events = %v, want %v", got, wantTaskEvents)
	}
}

type recordingTaskObserver struct {
	mu     sync.Mutex
	events []string
}

type cancelOnFirstTaskStartObserver struct {
	*recordingTaskObserver

	cancel context.CancelFunc
	once   sync.Once
}

func (o *cancelOnFirstTaskStartObserver) OnTaskStart(_ context.Context, task PlannedTask) {
	o.record("start", task)
	o.once.Do(o.cancel)
}

func (o *recordingTaskObserver) OnTaskStart(_ context.Context, task PlannedTask) {
	o.record("start", task)
}

func (o *recordingTaskObserver) OnTaskComplete(_ context.Context, task PlannedTask, _ EvalResult) {
	o.record("complete", task)
}

func (o *recordingTaskObserver) record(kind string, task PlannedTask) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, fmt.Sprintf("%s:%s:%d:%s", kind, task.ID, task.Iteration, task.Configuration))
}

func (o *recordingTaskObserver) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.events...)
}

type recordingProgressObserver struct {
	mu     sync.Mutex
	events []string
}

func (o *recordingProgressObserver) OnCaseStart(index, total int, caseID, _ string) {
	o.record("start", index, total, caseID)
}

func (o *recordingProgressObserver) OnCaseComplete(index, total int, caseID string, _ judge.Status, _ float64) {
	o.record("complete", index, total, caseID)
}

func (o *recordingProgressObserver) record(kind string, index, total int, caseID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, fmt.Sprintf("%s:%d/%d:%s", kind, index, total, caseID))
}

func (o *recordingProgressObserver) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.events...)
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
