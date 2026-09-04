package evalevent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

const defaultHeartbeatInterval = 30 * time.Second

// Task is the lifecycle emitter's immutable view of one planned evaluation task.
type Task struct {
	ID            string
	Iteration     uint64
	CaseID        string
	Configuration Configuration
	Index         uint64
	Title         string
}

// Iteration is the lifecycle emitter's immutable view of one planned iteration.
type Iteration struct {
	Number uint64
	Tasks  []Task
}

// Plan contains all tasks announced by run_started.
type Plan struct {
	Iterations []Iteration
}

// Ticker is the minimal periodic clock contract used by Lifecycle.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// Clock supplies monotonic-capable times and tickers for lifecycle timing.
type Clock interface {
	Now() time.Time
	NewTicker(interval time.Duration) Ticker
}

// LifecycleOptions controls lifecycle timing. Zero values use production defaults.
type LifecycleOptions struct {
	Clock             Clock
	HeartbeatInterval time.Duration
}

// Lifecycle serializes task state transitions and their progress snapshots.
type Lifecycle struct {
	mu sync.Mutex

	publisher *Publisher
	clock     Clock
	interval  time.Duration

	tasks      map[string]*lifecycleTask
	iterations map[uint64]*lifecycleIteration
	taskTotal  uint64

	started       bool
	finished      bool
	phase         RunPhase
	runStartedAt  time.Time
	completed     uint64
	resultCounts  ResultCounts
	activeTaskIDs map[string]struct{}

	heartbeatStopOnce sync.Once
	heartbeatStop     chan struct{}
	heartbeatDone     chan struct{}
	heartbeatStarted  bool
}

type lifecycleTask struct {
	task      Task
	started   bool
	completed bool
	startedAt time.Time
}

type lifecycleIteration struct {
	iteration  Iteration
	started    bool
	completed  bool
	startedAt  time.Time
	result     ResultCounts
	completedN uint64
}

// ValidatePlan validates an immutable lifecycle plan without opening a Sink.
func ValidatePlan(plan Plan) error {
	tasks, iterations, taskTotal, err := snapshotPlan(plan)
	if err != nil {
		return err
	}
	if uint64(len(tasks)) != taskTotal || len(iterations) != len(plan.Iterations) {
		return errors.New("validated plan snapshot is inconsistent")
	}
	return nil
}

// NewLifecycle validates and snapshots a plan without importing runner or evaluator types.
func NewLifecycle(publisher *Publisher, plan Plan, opts LifecycleOptions) (*Lifecycle, error) {
	if publisher == nil {
		return nil, errors.New("publisher must not be nil")
	}
	tasks, iterations, taskTotal, err := snapshotPlan(plan)
	if err != nil {
		return nil, err
	}
	clock := opts.Clock
	if clock == nil {
		clock = systemClock{}
	}
	interval := opts.HeartbeatInterval
	if interval == 0 {
		interval = defaultHeartbeatInterval
	}
	if interval < 0 {
		return nil, errors.New("heartbeat interval must not be negative")
	}
	return &Lifecycle{
		publisher:     publisher,
		clock:         clock,
		interval:      interval,
		tasks:         tasks,
		iterations:    iterations,
		taskTotal:     taskTotal,
		activeTaskIDs: make(map[string]struct{}),
		heartbeatStop: make(chan struct{}),
		heartbeatDone: make(chan struct{}),
	}, nil
}

// Start publishes run_started and the initial preparing snapshot, then starts heartbeats.
func (l *Lifecycle) Start(ctx context.Context, engine, skillName string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.started {
		return errors.New("lifecycle already started")
	}
	if err := validateNonEmptyString("engine", engine); err != nil {
		return err
	}
	if err := validateNonEmptyString("skill_name", skillName); err != nil {
		return err
	}
	l.started = true
	l.phase = RunPhasePreparing
	l.runStartedAt = l.clock.Now()
	if err := l.publishLocked(ctx, RunStartedPayload{
		Engine:          engine,
		SkillName:       skillName,
		TaskTotal:       l.taskTotal,
		IterationsTotal: uint64(len(l.iterations)),
	}); err != nil {
		return err
	}
	if err := l.publishProgressLocked(ctx); err != nil {
		return err
	}
	l.startHeartbeatLocked(context.WithoutCancel(ctx))
	return nil
}

// SetPhase moves the active invocation to a later execution phase and publishes progress.
func (l *Lifecycle) SetPhase(ctx context.Context, phase RunPhase) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureActiveLocked(); err != nil {
		return err
	}
	if !phase.valid() {
		return fmt.Errorf("invalid phase %q", phase)
	}
	if phase == RunPhaseFinalizing {
		return errors.New("finalizing phase is owned by Finish")
	}
	if phaseRank(phase) <= phaseRank(l.phase) {
		return fmt.Errorf("phase transition from %q to %q is not forward", l.phase, phase)
	}
	l.phase = phase
	return l.publishProgressLocked(ctx)
}

// Heartbeat publishes a progress snapshot with refreshed elapsed time.
func (l *Lifecycle) Heartbeat(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureActiveLocked(); err != nil {
		return err
	}
	return l.publishProgressLocked(ctx)
}

// IterationStarted announces one known iteration before any of its tasks start.
func (l *Lifecycle) IterationStarted(ctx context.Context, number uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureExecutingLocked(); err != nil {
		return err
	}
	iteration, ok := l.iterations[number]
	if !ok {
		return fmt.Errorf("unknown iteration %d", number)
	}
	if iteration.started {
		return fmt.Errorf("iteration %d already started", number)
	}
	iteration.started = true
	iteration.startedAt = l.clock.Now()
	return l.publishLocked(ctx, IterationStartedPayload{Iteration: number})
}

// CaseStarted announces one known planned task and refreshes progress.
func (l *Lifecycle) CaseStarted(ctx context.Context, taskID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureExecutingLocked(); err != nil {
		return err
	}
	task, ok := l.tasks[taskID]
	if !ok {
		return fmt.Errorf("unknown task %q", taskID)
	}
	iteration := l.iterations[task.task.Iteration]
	if !iteration.started || iteration.completed {
		return fmt.Errorf("iteration %d is not active", task.task.Iteration)
	}
	if task.started {
		return fmt.Errorf("task %q already started", taskID)
	}
	task.started = true
	task.startedAt = l.clock.Now()
	l.activeTaskIDs[taskID] = struct{}{}
	if err := l.publishLocked(ctx, CaseStartedPayload{TaskFields: l.taskFields(task.task)}); err != nil {
		return err
	}
	return l.publishProgressLocked(ctx)
}

// CaseCompleted records one terminal task status and refreshes progress.
func (l *Lifecycle) CaseCompleted(ctx context.Context, taskID string, status CaseStatus, passRate *float64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureExecutingLocked(); err != nil {
		return err
	}
	if !status.valid() {
		return fmt.Errorf("invalid case status %q", status)
	}
	if err := validatePassRate(passRate); err != nil {
		return err
	}
	task, ok := l.tasks[taskID]
	if !ok {
		return fmt.Errorf("unknown task %q", taskID)
	}
	if !task.started || task.completed {
		return fmt.Errorf("task %q is not actively running", taskID)
	}
	task.completed = true
	delete(l.activeTaskIDs, taskID)
	l.completed++
	addStatus(&l.resultCounts, status)
	iteration := l.iterations[task.task.Iteration]
	iteration.completedN++
	addStatus(&iteration.result, status)
	payload := CaseCompletedPayload{
		TaskFields:     l.taskFields(task.task),
		CompletedTasks: l.completed,
		Status:         status,
		PassRate:       copyPassRate(passRate),
		DurationMS:     elapsedMS(task.startedAt, l.clock.Now()),
	}
	if err := l.publishLocked(ctx, payload); err != nil {
		return err
	}
	return l.publishProgressLocked(ctx)
}

// IterationCompleted records one iteration after all its planned tasks complete.
func (l *Lifecycle) IterationCompleted(ctx context.Context, number uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureExecutingLocked(); err != nil {
		return err
	}
	iteration, ok := l.iterations[number]
	if !ok {
		return fmt.Errorf("unknown iteration %d", number)
	}
	if !iteration.started || iteration.completed {
		return fmt.Errorf("iteration %d is not actively running", number)
	}
	if iteration.completedN != uint64(len(iteration.iteration.Tasks)) {
		return fmt.Errorf("iteration %d has unfinished tasks", number)
	}
	iteration.completed = true
	return l.publishLocked(ctx, IterationCompletedPayload{
		Iteration:                number,
		InvocationCompletedTasks: l.completed,
		ResultCounts:             iteration.result,
		DurationMS:               elapsedMS(iteration.startedAt, l.clock.Now()),
	})
}

// Finish stops heartbeats and attempts to publish final progress and run_finished.
func (l *Lifecycle) Finish(ctx context.Context, status RunStatus) error {
	l.stopHeartbeat()

	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureActiveLocked(); err != nil {
		return err
	}
	if !status.valid() {
		return fmt.Errorf("invalid run status %q", status)
	}
	if status == RunStatusCompleted && l.completed != l.taskTotal {
		return fmt.Errorf("completed run has %d of %d terminal tasks", l.completed, l.taskTotal)
	}
	if status == RunStatusCompleted {
		for number, iteration := range l.iterations {
			if !iteration.completed {
				return fmt.Errorf("completed run has unfinished iteration %d", number)
			}
		}
	}
	l.phase = RunPhaseFinalizing
	clear(l.activeTaskIDs)
	l.finished = true
	progressErr := l.publishProgressLocked(ctx)
	finishedErr := l.publishLastLocked(ctx, RunFinishedPayload{
		Status:         status,
		CompletedTasks: l.completed,
		ResultCounts:   l.resultCounts,
		DurationMS:     elapsedMS(l.runStartedAt, l.clock.Now()),
	})
	if progressErr != nil {
		return progressErr
	}
	return finishedErr
}

func (l *Lifecycle) taskFields(task Task) TaskFields {
	return TaskFields{
		TaskID:        task.ID,
		Iteration:     task.Iteration,
		CaseID:        task.CaseID,
		Configuration: task.Configuration,
		TaskIndex:     task.Index,
		TaskTotal:     l.taskTotal,
		Title:         task.Title,
	}
}

func (l *Lifecycle) progressPayloadLocked() RunProgressPayload {
	return RunProgressPayload{
		Phase:          l.phase,
		TaskTotal:      l.taskTotal,
		CompletedTasks: l.completed,
		RunningTasks:   uint64(len(l.activeTaskIDs)),
		ResultCounts:   l.resultCounts,
		ElapsedMS:      elapsedMS(l.runStartedAt, l.clock.Now()),
	}
}

func (l *Lifecycle) publishProgressLocked(ctx context.Context) error {
	return l.publishLocked(ctx, l.progressPayloadLocked())
}

func (l *Lifecycle) publishLocked(ctx context.Context, payload Payload) error {
	return l.recordPublicationError(l.publisher.Publish(ctx, payload))
}

func (l *Lifecycle) publishLastLocked(ctx context.Context, payload Payload) error {
	return l.recordPublicationError(l.publisher.PublishLast(ctx, payload))
}

func (l *Lifecycle) recordPublicationError(err error) error {
	if err != nil {
		l.requestHeartbeatStop()
	}
	return err
}

func (l *Lifecycle) ensureActiveLocked() error {
	if !l.started {
		return errors.New("lifecycle has not started")
	}
	if l.finished {
		return errors.New("lifecycle already finished")
	}
	return nil
}

func (l *Lifecycle) ensureExecutingLocked() error {
	if err := l.ensureActiveLocked(); err != nil {
		return err
	}
	if l.phase != RunPhaseExecuting {
		return fmt.Errorf("lifecycle is in %q phase, not executing", l.phase)
	}
	return nil
}

func (l *Lifecycle) startHeartbeatLocked(ctx context.Context) {
	if l.interval <= 0 {
		close(l.heartbeatDone)
		return
	}
	l.heartbeatStarted = true
	ticker := l.clock.NewTicker(l.interval)
	go l.runHeartbeat(ctx, ticker)
}

func (l *Lifecycle) runHeartbeat(ctx context.Context, ticker Ticker) {
	defer close(l.heartbeatDone)
	defer ticker.Stop()
	for {
		select {
		case <-l.heartbeatStop:
			return
		case <-ticker.C():
			if err := l.Heartbeat(ctx); err != nil {
				return
			}
		}
	}
}

func (l *Lifecycle) requestHeartbeatStop() {
	l.heartbeatStopOnce.Do(func() { close(l.heartbeatStop) })
}

func (l *Lifecycle) stopHeartbeat() {
	l.mu.Lock()
	started := l.heartbeatStarted
	l.mu.Unlock()
	l.requestHeartbeatStop()
	if started {
		<-l.heartbeatDone
	}
}

func snapshotPlan(plan Plan) (map[string]*lifecycleTask, map[uint64]*lifecycleIteration, uint64, error) {
	if len(plan.Iterations) == 0 {
		return nil, nil, 0, errors.New("plan must contain at least one iteration")
	}
	taskCount := 0
	for _, iteration := range plan.Iterations {
		taskCount += len(iteration.Tasks)
	}
	if taskCount == 0 || uint64(taskCount) > MaxSafeInteger {
		return nil, nil, 0, fmt.Errorf("plan task total must be between 1 and %d", MaxSafeInteger)
	}
	taskTotal := uint64(taskCount)
	tasks := make(map[string]*lifecycleTask, taskCount)
	iterations := make(map[uint64]*lifecycleIteration, len(plan.Iterations))
	indices := make(map[uint64]struct{}, taskCount)
	for _, iteration := range plan.Iterations {
		if err := validatePositiveInteger("iteration", iteration.Number); err != nil {
			return nil, nil, 0, err
		}
		if len(iteration.Tasks) == 0 {
			return nil, nil, 0, fmt.Errorf("iteration %d must contain at least one task", iteration.Number)
		}
		if _, exists := iterations[iteration.Number]; exists {
			return nil, nil, 0, fmt.Errorf("duplicate iteration %d", iteration.Number)
		}
		iterationCopy := Iteration{Number: iteration.Number, Tasks: append([]Task(nil), iteration.Tasks...)}
		iterations[iteration.Number] = &lifecycleIteration{iteration: iterationCopy}
		for _, task := range iteration.Tasks {
			if err := validatePlanTask(task, iteration.Number, taskTotal); err != nil {
				return nil, nil, 0, err
			}
			if _, exists := tasks[task.ID]; exists {
				return nil, nil, 0, fmt.Errorf("duplicate task ID %q", task.ID)
			}
			if _, exists := indices[task.Index]; exists {
				return nil, nil, 0, fmt.Errorf("duplicate task index %d", task.Index)
			}
			indices[task.Index] = struct{}{}
			tasks[task.ID] = &lifecycleTask{task: task}
		}
	}
	for index := uint64(1); index <= taskTotal; index++ {
		if _, ok := indices[index]; !ok {
			return nil, nil, 0, fmt.Errorf("plan is missing task index %d", index)
		}
	}
	return tasks, iterations, taskTotal, nil
}

func validatePlanTask(task Task, iteration, taskTotal uint64) error {
	if err := validateNonEmptyString("task_id", task.ID); err != nil {
		return err
	}
	if task.Iteration != iteration {
		return fmt.Errorf("task %q iteration %d does not match iteration %d", task.ID, task.Iteration, iteration)
	}
	if err := validateNonEmptyString("case_id", task.CaseID); err != nil {
		return err
	}
	if !task.Configuration.valid() {
		return fmt.Errorf("task %q has invalid configuration %q", task.ID, task.Configuration)
	}
	if task.Index == 0 || task.Index > taskTotal {
		return fmt.Errorf("task %q index must be between 1 and %d", task.ID, taskTotal)
	}
	return validateString("title", task.Title)
}

func addStatus(counts *ResultCounts, status CaseStatus) {
	switch status {
	case CaseStatusPass:
		counts.Passed++
	case CaseStatusFail:
		counts.Failed++
	case CaseStatusError:
		counts.Errored++
	case CaseStatusSkip:
		counts.Skipped++
	}
}

func validatePassRate(passRate *float64) error {
	if passRate == nil {
		return nil
	}
	if math.IsNaN(*passRate) || math.IsInf(*passRate, 0) || *passRate < 0 || *passRate > 1 {
		return errors.New("pass_rate must be finite and between 0 and 1")
	}
	return nil
}

func copyPassRate(passRate *float64) *float64 {
	if passRate == nil {
		return nil
	}
	value := *passRate
	return &value
}

func elapsedMS(start, end time.Time) uint64 {
	duration := end.Sub(start)
	if duration <= 0 {
		return 0
	}
	return uint64(duration / time.Millisecond)
}

func phaseRank(phase RunPhase) int {
	switch phase {
	case RunPhasePreparing:
		return 1
	case RunPhaseExecuting:
		return 2
	case RunPhaseFinalizing:
		return 3
	default:
		return 0
	}
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
func (systemClock) NewTicker(interval time.Duration) Ticker {
	return systemTicker{ticker: time.NewTicker(interval)}
}

type systemTicker struct {
	ticker *time.Ticker
}

func (t systemTicker) C() <-chan time.Time { return t.ticker.C }
func (t systemTicker) Stop()               { t.ticker.Stop() }
