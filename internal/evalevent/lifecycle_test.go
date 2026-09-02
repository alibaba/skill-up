package evalevent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

//nolint:gocyclo,cyclop,funlen // The test intentionally verifies the complete lifecycle sequence and payloads.
func TestLifecyclePublishesTypedSequenceAndFakeClockHeartbeat(t *testing.T) {
	t.Parallel()

	clock := newManualClock(time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC))
	sink := newRecordingSink()
	publisher, err := NewPublisher(PublisherConfig{Sink: sink, Now: clock.Now})
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	lifecycle, err := NewLifecycle(publisher, Plan{Iterations: []Iteration{{
		Number: 3,
		Tasks: []Task{{
			ID: "opaque-1", Iteration: 3, CaseID: "目录/case%一", Configuration: ConfigurationWithSkill,
			Index: 1, Title: "Case one",
		}},
	}}}, LifecycleOptions{Clock: clock, HeartbeatInterval: 30 * time.Second})
	if err != nil {
		t.Fatalf("NewLifecycle() error = %v", err)
	}
	ctx := context.Background()
	if err := lifecycle.Start(ctx, "qoder-cli", "test-skill"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := lifecycle.SetPhase(ctx, RunPhaseExecuting); err != nil {
		t.Fatalf("SetPhase() error = %v", err)
	}
	if err := lifecycle.IterationStarted(ctx, 3); err != nil {
		t.Fatalf("IterationStarted() error = %v", err)
	}
	if err := lifecycle.CaseStarted(ctx, "opaque-1"); err != nil {
		t.Fatalf("CaseStarted() error = %v", err)
	}

	clock.Advance(30 * time.Second)
	waitForEventCount(t, sink, 7)
	clock.Advance(2500 * time.Millisecond)
	passRate := 0.5
	if err := lifecycle.CaseCompleted(ctx, "opaque-1", CaseStatusFail, &passRate); err != nil {
		t.Fatalf("CaseCompleted() error = %v", err)
	}
	if err := lifecycle.IterationCompleted(ctx, 3); err != nil {
		t.Fatalf("IterationCompleted() error = %v", err)
	}
	if err := lifecycle.Finish(ctx, RunStatusCompleted); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if err := publisher.Close(); err != nil {
		t.Fatalf("Publisher.Close() error = %v", err)
	}

	events := sink.snapshot()
	wantTypes := []Type{
		EventRunStarted,
		EventRunProgress,
		EventRunProgress,
		EventIterationStarted,
		EventCaseStarted,
		EventRunProgress,
		EventRunProgress,
		EventCaseCompleted,
		EventRunProgress,
		EventIterationCompleted,
		EventRunProgress,
		EventRunFinished,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d", len(events), len(wantTypes))
	}
	for i, event := range events {
		if event.Type != wantTypes[i] {
			t.Errorf("event[%d].Type = %q, want %q", i, event.Type, wantTypes[i])
		}
		if event.SequenceNumber != uint64(i+1) {
			t.Errorf("event[%d].SequenceNumber = %d, want %d", i, event.SequenceNumber, i+1)
		}
		if event.LastEvent != (i == len(events)-1) {
			t.Errorf("event[%d].LastEvent = %t", i, event.LastEvent)
		}
	}

	started := eventPayloadAs[RunStartedPayload](t, events[0])
	if started.TaskTotal != 1 || started.IterationsTotal != 1 {
		t.Errorf("run_started totals = %d tasks, %d iterations", started.TaskTotal, started.IterationsTotal)
	}
	heartbeat := eventPayloadAs[RunProgressPayload](t, events[6])
	if heartbeat.Phase != RunPhaseExecuting || heartbeat.RunningTasks != 1 || heartbeat.ElapsedMS != 30000 {
		t.Errorf("heartbeat payload = %+v", heartbeat)
	}
	completed := eventPayloadAs[CaseCompletedPayload](t, events[7])
	if completed.CaseID != "目录/case%一" || completed.CompletedTasks != 1 || completed.Status != CaseStatusFail || completed.DurationMS != 32500 {
		t.Errorf("case_completed payload = %+v", completed)
	}
	iteration := eventPayloadAs[IterationCompletedPayload](t, events[9])
	if iteration.InvocationCompletedTasks != 1 || iteration.Failed != 1 || iteration.DurationMS != 32500 {
		t.Errorf("iteration_completed payload = %+v", iteration)
	}
	finished := eventPayloadAs[RunFinishedPayload](t, events[11])
	if finished.Status != RunStatusCompleted || finished.CompletedTasks != 1 || finished.Failed != 1 || finished.DurationMS != 32500 {
		t.Errorf("run_finished payload = %+v", finished)
	}

	clock.Advance(30 * time.Second)
	if got := len(sink.snapshot()); got != len(events) {
		t.Errorf("heartbeat continued after Finish: event count = %d, want %d", got, len(events))
	}
}

//nolint:gocyclo,cyclop // The test exercises each rejected transition before graceful error finalization.
func TestLifecycleRejectsIllegalTransitionsAndAllowsPartialErrorFinish(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	publisher, err := NewPublisher(PublisherConfig{Sink: sink})
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	lifecycle, err := NewLifecycle(publisher, singleTaskPlan(), LifecycleOptions{})
	if err != nil {
		t.Fatalf("NewLifecycle() error = %v", err)
	}
	ctx := context.Background()
	if err := lifecycle.Start(ctx, "engine", "skill"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := lifecycle.SetPhase(ctx, RunPhaseExecuting); err != nil {
		t.Fatalf("SetPhase() error = %v", err)
	}
	if err := lifecycle.IterationStarted(ctx, 1); err != nil {
		t.Fatalf("IterationStarted() error = %v", err)
	}
	if err := lifecycle.CaseStarted(ctx, "unknown"); err == nil {
		t.Fatal("unknown task start succeeded")
	}
	if err := lifecycle.CaseStarted(ctx, "task-1"); err != nil {
		t.Fatalf("CaseStarted() error = %v", err)
	}
	eventCount := len(sink.snapshot())
	if err := lifecycle.CaseStarted(ctx, "task-1"); err == nil {
		t.Fatal("duplicate task start succeeded")
	}
	if err := lifecycle.IterationCompleted(ctx, 1); err == nil {
		t.Fatal("unfinished iteration completion succeeded")
	}
	if len(sink.snapshot()) != eventCount {
		t.Fatal("illegal transitions published events")
	}
	if err := lifecycle.Finish(ctx, RunStatusCompleted); err == nil {
		t.Fatal("completed Finish() accepted an unfinished task")
	}
	if err := lifecycle.Finish(ctx, RunStatusError); err != nil {
		t.Fatalf("partial error Finish() error = %v", err)
	}
	if err := lifecycle.CaseCompleted(ctx, "task-1", CaseStatusPass, nil); err == nil {
		t.Fatal("case completion after Finish succeeded")
	}

	events := sink.snapshot()
	finished := events[len(events)-1]
	if !finished.LastEvent || finished.Type != EventRunFinished {
		t.Fatalf("final event = %+v", finished)
	}
	payload := eventPayloadAs[RunFinishedPayload](t, finished)
	if payload.Status != RunStatusError || payload.CompletedTasks != 0 {
		t.Fatalf("partial run_finished payload = %+v", payload)
	}
}

//nolint:gocyclo,cyclop // The test checks concurrent task events and every resulting progress snapshot.
func TestLifecycleMaintainsProgressInvariantsUnderConcurrency(t *testing.T) {
	t.Parallel()

	const taskCount = 24
	tasks := make([]Task, 0, taskCount)
	for i := 1; i <= taskCount; i++ {
		tasks = append(tasks, Task{
			ID: fmt.Sprintf("task-%d", i), Iteration: 1, CaseID: fmt.Sprintf("case-%d", i),
			Configuration: ConfigurationWithSkill, Index: uint64(i),
		})
	}
	sink := newRecordingSink()
	publisher, err := NewPublisher(PublisherConfig{Sink: sink})
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	lifecycle, err := NewLifecycle(publisher, Plan{Iterations: []Iteration{{Number: 1, Tasks: tasks}}}, LifecycleOptions{})
	if err != nil {
		t.Fatalf("NewLifecycle() error = %v", err)
	}
	ctx := context.Background()
	if err := lifecycle.Start(ctx, "engine", "skill"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := lifecycle.SetPhase(ctx, RunPhaseExecuting); err != nil {
		t.Fatalf("SetPhase() error = %v", err)
	}
	if err := lifecycle.IterationStarted(ctx, 1); err != nil {
		t.Fatalf("IterationStarted() error = %v", err)
	}

	var wg sync.WaitGroup
	for _, task := range tasks {
		wg.Go(func() {
			if err := lifecycle.CaseStarted(ctx, task.ID); err != nil {
				t.Errorf("CaseStarted(%s) error = %v", task.ID, err)
			}
		})
	}
	wg.Wait()
	for i, task := range tasks {
		status := CaseStatusPass
		if i%2 == 1 {
			status = CaseStatusSkip
		}
		wg.Go(func() {
			if err := lifecycle.CaseCompleted(ctx, task.ID, status, nil); err != nil {
				t.Errorf("CaseCompleted(%s) error = %v", task.ID, err)
			}
		})
	}
	wg.Wait()
	if err := lifecycle.IterationCompleted(ctx, 1); err != nil {
		t.Fatalf("IterationCompleted() error = %v", err)
	}
	if err := lifecycle.Finish(ctx, RunStatusCompleted); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}

	events := sink.snapshot()
	for i, event := range events {
		if event.SequenceNumber != uint64(i+1) {
			t.Fatalf("event[%d].SequenceNumber = %d", i, event.SequenceNumber)
		}
		progress, ok := event.Payload.(RunProgressPayload)
		if !ok {
			continue
		}
		if progress.CompletedTasks != progress.total() {
			t.Errorf("progress completed/count mismatch: %+v", progress)
		}
		if progress.CompletedTasks+progress.RunningTasks > progress.TaskTotal {
			t.Errorf("progress exceeds total: %+v", progress)
		}
	}
	finished := eventPayloadAs[RunFinishedPayload](t, events[len(events)-1])
	if finished.CompletedTasks != taskCount || finished.Passed != taskCount/2 || finished.Skipped != taskCount/2 {
		t.Errorf("run_finished counts = %+v", finished)
	}
}

func TestLifecycleSnapshotsAndValidatesPlan(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	publisher, err := NewPublisher(PublisherConfig{Sink: sink})
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	plan := singleTaskPlan()
	lifecycle, err := NewLifecycle(publisher, plan, LifecycleOptions{})
	if err != nil {
		t.Fatalf("NewLifecycle() error = %v", err)
	}
	plan.Iterations[0].Tasks[0].CaseID = "mutated"
	if err := lifecycle.Start(context.Background(), "engine", "skill"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := lifecycle.SetPhase(context.Background(), RunPhaseExecuting); err != nil {
		t.Fatalf("SetPhase() error = %v", err)
	}
	if err := lifecycle.IterationStarted(context.Background(), 1); err != nil {
		t.Fatalf("IterationStarted() error = %v", err)
	}
	if err := lifecycle.CaseStarted(context.Background(), "task-1"); err != nil {
		t.Fatalf("CaseStarted() error = %v", err)
	}
	caseStarted := eventPayloadAs[CaseStartedPayload](t, sink.snapshot()[4])
	if caseStarted.CaseID != "case/原始%1" {
		t.Fatalf("snapshotted CaseID = %q", caseStarted.CaseID)
	}

	invalidPlans := []Plan{
		{},
		{Iterations: []Iteration{{Number: 1}}},
		{Iterations: []Iteration{{Number: 1, Tasks: []Task{{ID: "task", Iteration: 2, CaseID: "case", Configuration: ConfigurationWithSkill, Index: 1}}}}},
		{Iterations: []Iteration{{Number: 1, Tasks: []Task{{ID: "task", Iteration: 1, CaseID: "case", Configuration: ConfigurationWithSkill, Index: 2}}}}},
	}
	for i, invalid := range invalidPlans {
		if _, err := NewLifecycle(publisher, invalid, LifecycleOptions{}); err == nil {
			t.Errorf("invalid plan %d was accepted", i)
		}
	}
}

func singleTaskPlan() Plan {
	return Plan{Iterations: []Iteration{{
		Number: 1,
		Tasks: []Task{{
			ID: "task-1", Iteration: 1, CaseID: "case/原始%1", Configuration: ConfigurationWithSkill, Index: 1,
		}},
	}}}
}

func eventPayloadAs[T Payload](t *testing.T, event Event) T {
	t.Helper()
	payload, ok := event.Payload.(T)
	if !ok {
		var zero T
		t.Fatalf("event %q payload type = %T, want %T", event.Type, event.Payload, zero)
	}
	return payload
}

func waitForEventCount(t *testing.T, sink *recordingSink, count int) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for len(sink.snapshot()) < count {
		select {
		case <-sink.notify:
		case <-deadline.C:
			t.Fatalf("event count = %d, want at least %d", len(sink.snapshot()), count)
		}
	}
}

type manualClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*manualTicker
}

func newManualClock(now time.Time) *manualClock {
	return &manualClock{now: now}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) NewTicker(interval time.Duration) Ticker {
	c.mu.Lock()
	defer c.mu.Unlock()
	ticker := &manualTicker{interval: interval, ticks: make(chan time.Time, 16)}
	c.tickers = append(c.tickers, ticker)
	return ticker
}

func (c *manualClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	now := c.now
	tickers := append([]*manualTicker(nil), c.tickers...)
	c.mu.Unlock()
	for _, ticker := range tickers {
		ticker.advance(duration, now)
	}
}

type manualTicker struct {
	mu       sync.Mutex
	interval time.Duration
	elapsed  time.Duration
	stopped  bool
	ticks    chan time.Time
}

func (t *manualTicker) C() <-chan time.Time { return t.ticks }

func (t *manualTicker) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
}

func (t *manualTicker) advance(duration time.Duration, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	t.elapsed += duration
	for t.elapsed >= t.interval {
		t.elapsed -= t.interval
		t.ticks <- now
	}
}
