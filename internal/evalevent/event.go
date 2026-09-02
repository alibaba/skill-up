// Package evalevent defines skill-up's internal evaluation event protocol.
package evalevent

import (
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"
)

// Type identifies an event payload on the wire.
type Type string

const (
	// ProtocolVersion is the current event envelope and ordered-log version.
	ProtocolVersion uint64 = 1
	// MaxSafeInteger is the largest integer that can be represented exactly by JavaScript.
	MaxSafeInteger uint64 = 9007199254740991
	// EventVersionV1 is the initial version of every core payload.
	EventVersionV1 uint64 = 1
)

const (
	// EventRunStarted identifies the core invocation-start event.
	EventRunStarted Type = "run_started"
	// EventRunProgress identifies an invocation progress snapshot.
	EventRunProgress Type = "run_progress"
	// EventIterationStarted identifies an iteration-start event.
	EventIterationStarted Type = "iteration_started"
	// EventCaseStarted identifies a planned task-start event.
	EventCaseStarted Type = "case_started"
	// EventCaseCompleted identifies a terminal task event.
	EventCaseCompleted Type = "case_completed"
	// EventIterationCompleted identifies an iteration-completion event.
	EventIterationCompleted Type = "iteration_completed"
	// EventRunFinished identifies the invocation lifecycle-ending event.
	EventRunFinished Type = "run_finished"
)

// Configuration identifies how a case task is executed.
type Configuration string

const (
	// ConfigurationWithSkill executes a case with the evaluated skill installed.
	ConfigurationWithSkill Configuration = "with_skill"
	// ConfigurationWithoutSkill executes a case without the evaluated skill.
	ConfigurationWithoutSkill Configuration = "without_skill"
)

// CaseStatus is a terminal case outcome.
type CaseStatus string

const (
	// CaseStatusPass records a passing task outcome.
	CaseStatusPass CaseStatus = "PASS"
	// CaseStatusFail records a failing task outcome.
	CaseStatusFail CaseStatus = "FAIL"
	// CaseStatusError records a task execution error.
	CaseStatusError CaseStatus = "ERROR"
	// CaseStatusSkip records a skipped task outcome.
	CaseStatusSkip CaseStatus = "SKIP"
)

// RunStatus is a terminal invocation lifecycle state.
type RunStatus string

const (
	// RunStatusCompleted means every planned task reached a terminal case status.
	RunStatusCompleted RunStatus = "COMPLETED"
	// RunStatusError means an invocation-level failure prevented normal completion.
	RunStatusError RunStatus = "ERROR"
	// RunStatusCancelled means the invocation stopped after cancellation.
	RunStatusCancelled RunStatus = "CANCELLED"
)

// RunPhase is the current invocation-level execution phase.
type RunPhase string

const (
	// RunPhasePreparing is the initial event-log startup phase.
	RunPhasePreparing RunPhase = "preparing"
	// RunPhaseExecuting is the active task-execution phase.
	RunPhaseExecuting RunPhase = "executing"
	// RunPhaseFinalizing is the invocation finalization phase.
	RunPhaseFinalizing RunPhase = "finalizing"
)

// Payload is a sealed, typed v1 event payload.
type Payload interface {
	eventType() Type
	eventVersion() uint64
	snapshot() Payload
	validate() error
}

// Event is one fully enveloped evaluation event.
type Event struct {
	ProtocolVersion uint64            `json:"protocol_version"`
	EventVersion    uint64            `json:"event_version"`
	SequenceNumber  uint64            `json:"sequence_number"`
	InvocationID    string            `json:"invocation_id"`
	Time            time.Time         `json:"time"`
	Type            Type              `json:"event"`
	LastEvent       bool              `json:"last_event,omitempty"`
	Attributes      map[string]string `json:"attributes,omitempty"`
	Payload         Payload           `json:"payload"`
}

// RunStartedPayload announces the immutable invocation task plan.
type RunStartedPayload struct {
	Engine          string `json:"engine"`
	SkillName       string `json:"skill_name"`
	TaskTotal       uint64 `json:"task_total"`
	IterationsTotal uint64 `json:"iterations_total"`
}

func (RunStartedPayload) eventType() Type      { return EventRunStarted }
func (RunStartedPayload) eventVersion() uint64 { return EventVersionV1 }
func (p RunStartedPayload) snapshot() Payload  { return p }
func (p RunStartedPayload) validate() error {
	if err := validateNonEmptyString("engine", p.Engine); err != nil {
		return err
	}
	if err := validateNonEmptyString("skill_name", p.SkillName); err != nil {
		return err
	}
	if err := validatePositiveInteger("task_total", p.TaskTotal); err != nil {
		return err
	}
	return validatePositiveInteger("iterations_total", p.IterationsTotal)
}

// ResultCounts contains terminal task counts grouped by outcome.
type ResultCounts struct {
	Passed  uint64 `json:"passed"`
	Failed  uint64 `json:"failed"`
	Errored uint64 `json:"errored"`
	Skipped uint64 `json:"skipped"`
}

func (c ResultCounts) validate() error {
	for name, value := range map[string]uint64{
		"passed": c.Passed, "failed": c.Failed, "errored": c.Errored, "skipped": c.Skipped,
	} {
		if err := validateSafeInteger(name, value); err != nil {
			return err
		}
	}
	if c.Passed > MaxSafeInteger-c.Failed || c.Passed+c.Failed > MaxSafeInteger-c.Errored ||
		c.Passed+c.Failed+c.Errored > MaxSafeInteger-c.Skipped {
		return fmt.Errorf("result count sum exceeds %d", MaxSafeInteger)
	}
	return nil
}

func (c ResultCounts) total() uint64 {
	return c.Passed + c.Failed + c.Errored + c.Skipped
}

// RunProgressPayload is a replaceable invocation-level progress snapshot.
type RunProgressPayload struct {
	ResultCounts

	Phase          RunPhase `json:"phase"`
	TaskTotal      uint64   `json:"task_total"`
	CompletedTasks uint64   `json:"completed_tasks"`
	RunningTasks   uint64   `json:"running_tasks"`
	ElapsedMS      uint64   `json:"elapsed_ms"`
}

func (RunProgressPayload) eventType() Type      { return EventRunProgress }
func (RunProgressPayload) eventVersion() uint64 { return EventVersionV1 }
func (p RunProgressPayload) snapshot() Payload  { return p }
func (p RunProgressPayload) validate() error {
	if !p.Phase.valid() {
		return fmt.Errorf("invalid phase %q", p.Phase)
	}
	if err := validatePositiveInteger("task_total", p.TaskTotal); err != nil {
		return err
	}
	if err := validateSafeInteger("completed_tasks", p.CompletedTasks); err != nil {
		return err
	}
	if err := validateSafeInteger("running_tasks", p.RunningTasks); err != nil {
		return err
	}
	if err := validateSafeInteger("elapsed_ms", p.ElapsedMS); err != nil {
		return err
	}
	if err := p.ResultCounts.validate(); err != nil {
		return err
	}
	if p.CompletedTasks != p.total() {
		return errors.New("completed_tasks must equal the result count sum")
	}
	if p.CompletedTasks > p.TaskTotal || p.RunningTasks > p.TaskTotal-p.CompletedTasks {
		return errors.New("completed_tasks + running_tasks exceeds task_total")
	}
	return nil
}

// IterationStartedPayload announces an evaluation iteration.
type IterationStartedPayload struct {
	Iteration uint64 `json:"iteration"`
}

func (IterationStartedPayload) eventType() Type      { return EventIterationStarted }
func (IterationStartedPayload) eventVersion() uint64 { return EventVersionV1 }
func (p IterationStartedPayload) snapshot() Payload  { return p }
func (p IterationStartedPayload) validate() error {
	return validatePositiveInteger("iteration", p.Iteration)
}

// TaskFields identifies one planned case/configuration execution.
type TaskFields struct {
	TaskID        string        `json:"task_id"`
	Iteration     uint64        `json:"iteration"`
	CaseID        string        `json:"case_id"`
	Configuration Configuration `json:"configuration"`
	TaskIndex     uint64        `json:"task_index"`
	TaskTotal     uint64        `json:"task_total"`
	Title         string        `json:"title"`
}

func (f TaskFields) validate() error {
	if err := validateNonEmptyString("task_id", f.TaskID); err != nil {
		return err
	}
	if err := validatePositiveInteger("iteration", f.Iteration); err != nil {
		return err
	}
	if err := validateNonEmptyString("case_id", f.CaseID); err != nil {
		return err
	}
	if !f.Configuration.valid() {
		return fmt.Errorf("invalid configuration %q", f.Configuration)
	}
	if err := validatePositiveInteger("task_total", f.TaskTotal); err != nil {
		return err
	}
	if err := validatePositiveInteger("task_index", f.TaskIndex); err != nil {
		return err
	}
	if f.TaskIndex > f.TaskTotal {
		return errors.New("task_index exceeds task_total")
	}
	return validateString("title", f.Title)
}

// CaseStartedPayload announces a task execution start.
type CaseStartedPayload struct {
	TaskFields
}

func (CaseStartedPayload) eventType() Type      { return EventCaseStarted }
func (CaseStartedPayload) eventVersion() uint64 { return EventVersionV1 }
func (p CaseStartedPayload) snapshot() Payload  { return p }
func (p CaseStartedPayload) validate() error    { return p.TaskFields.validate() }

// CaseCompletedPayload records one terminal task outcome.
type CaseCompletedPayload struct {
	TaskFields

	CompletedTasks uint64     `json:"completed_tasks"`
	Status         CaseStatus `json:"status"`
	PassRate       *float64   `json:"pass_rate,omitempty"`
	DurationMS     uint64     `json:"duration_ms"`
}

func (CaseCompletedPayload) eventType() Type      { return EventCaseCompleted }
func (CaseCompletedPayload) eventVersion() uint64 { return EventVersionV1 }
func (p CaseCompletedPayload) snapshot() Payload {
	p.PassRate = copyPassRate(p.PassRate)
	return p
}

func (p CaseCompletedPayload) validate() error {
	if err := p.TaskFields.validate(); err != nil {
		return err
	}
	if err := validateSafeInteger("completed_tasks", p.CompletedTasks); err != nil {
		return err
	}
	if p.CompletedTasks > p.TaskTotal {
		return errors.New("completed_tasks exceeds task_total")
	}
	if !p.Status.valid() {
		return fmt.Errorf("invalid case status %q", p.Status)
	}
	if p.PassRate != nil && (math.IsNaN(*p.PassRate) || math.IsInf(*p.PassRate, 0) || *p.PassRate < 0 || *p.PassRate > 1) {
		return errors.New("pass_rate must be finite and between 0 and 1")
	}
	return validateSafeInteger("duration_ms", p.DurationMS)
}

// IterationCompletedPayload records terminal counts for one iteration.
type IterationCompletedPayload struct {
	ResultCounts

	Iteration                uint64 `json:"iteration"`
	InvocationCompletedTasks uint64 `json:"invocation_completed_tasks"`
	DurationMS               uint64 `json:"duration_ms"`
}

func (IterationCompletedPayload) eventType() Type      { return EventIterationCompleted }
func (IterationCompletedPayload) eventVersion() uint64 { return EventVersionV1 }
func (p IterationCompletedPayload) snapshot() Payload  { return p }
func (p IterationCompletedPayload) validate() error {
	if err := validatePositiveInteger("iteration", p.Iteration); err != nil {
		return err
	}
	if err := validateSafeInteger("invocation_completed_tasks", p.InvocationCompletedTasks); err != nil {
		return err
	}
	if err := p.ResultCounts.validate(); err != nil {
		return err
	}
	return validateSafeInteger("duration_ms", p.DurationMS)
}

// RunFinishedPayload records the terminal invocation lifecycle state.
type RunFinishedPayload struct {
	ResultCounts

	Status         RunStatus `json:"status"`
	CompletedTasks uint64    `json:"completed_tasks"`
	DurationMS     uint64    `json:"duration_ms"`
}

func (RunFinishedPayload) eventType() Type      { return EventRunFinished }
func (RunFinishedPayload) eventVersion() uint64 { return EventVersionV1 }
func (p RunFinishedPayload) snapshot() Payload  { return p }
func (p RunFinishedPayload) validate() error {
	if !p.Status.valid() {
		return fmt.Errorf("invalid run status %q", p.Status)
	}
	if err := validateSafeInteger("completed_tasks", p.CompletedTasks); err != nil {
		return err
	}
	if err := p.ResultCounts.validate(); err != nil {
		return err
	}
	if p.CompletedTasks != p.total() {
		return errors.New("completed_tasks must equal the result count sum")
	}
	return validateSafeInteger("duration_ms", p.DurationMS)
}

func (c Configuration) valid() bool {
	return c == ConfigurationWithSkill || c == ConfigurationWithoutSkill
}

func (s CaseStatus) valid() bool {
	return s == CaseStatusPass || s == CaseStatusFail || s == CaseStatusError || s == CaseStatusSkip
}

func (s RunStatus) valid() bool {
	return s == RunStatusCompleted || s == RunStatusError || s == RunStatusCancelled
}

func (p RunPhase) valid() bool {
	return p == RunPhasePreparing || p == RunPhaseExecuting || p == RunPhaseFinalizing
}

func validatePositiveInteger(name string, value uint64) error {
	if value == 0 {
		return fmt.Errorf("%s must be at least 1", name)
	}
	return validateSafeInteger(name, value)
}

func validateSafeInteger(name string, value uint64) error {
	if value > MaxSafeInteger {
		return fmt.Errorf("%s exceeds %d", name, MaxSafeInteger)
	}
	return nil
}

func validateNonEmptyString(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	return validateString(name, value)
}

func validateString(name, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	return nil
}
