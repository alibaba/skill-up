// Package compare compares two offline skill-up report results.
package compare

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
)

// Options controls compare output and gate evaluation.
type Options struct {
	FailOnRegression        bool
	MaxRegressions          *int
	MaxTokenIncreasePercent *float64
	OldMissingMetrics       []string
	NewMissingMetrics       []string
}

// Result is the stable comparison result used by text and JSON output.
type Result struct {
	Baseline  Source          `json:"baseline"`
	Candidate Source          `json:"candidate"`
	Coverage  Coverage        `json:"coverage"`
	Metadata  MetadataDiff    `json:"metadata"`
	Run       RunComparison   `json:"run"`
	Cases     CaseTransitions `json:"cases"`
	Gates     GateResult      `json:"gates"`
}

// Source identifies an original result artifact independently of its configuration.
type Source struct {
	ID    int    `json:"id,omitempty"`
	Label string `json:"label"`
	Path  string `json:"path"`
}

// Coverage describes the shared case set, without treating added or removed cases as pairs.
type Coverage struct {
	Shared  int `json:"shared"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// MetadataDiff records old and new run metadata.
type MetadataDiff struct {
	RequestedConfiguration FieldDiff[string]    `json:"requested_configuration"`
	AppliedConfiguration   FieldDiff[string]    `json:"applied_configuration"`
	ObservedConfiguration  FieldDiff[string]    `json:"observed_configuration"`
	SkillName              FieldDiff[string]    `json:"skill_name"`
	SchemaVersion          FieldDiff[string]    `json:"schema_version"`
	EngineName             FieldDiff[string]    `json:"engine_name"`
	ModelName              FieldDiff[string]    `json:"model_name"`
	StartTime              FieldDiff[time.Time] `json:"start_time"`
	EndTime                FieldDiff[time.Time] `json:"end_time"`
}

// FieldDiff stores old/new values and whether they differ.
type FieldDiff[T comparable] struct {
	Old     T    `json:"old"`
	New     T    `json:"new"`
	Changed bool `json:"changed"`
}

// RunComparison stores old/new/delta aggregate metrics.
type RunComparison struct {
	Old   RunMetrics `json:"old"`
	New   RunMetrics `json:"new"`
	Delta RunMetrics `json:"delta"`
}

// RunMetrics stores aggregate metrics for one result set or a delta.
type RunMetrics struct {
	Missing      []string `json:"-"`
	CaseCount    int      `json:"case_count"`
	PassCount    int      `json:"pass_count"`
	PassRate     float64  `json:"pass_rate"`
	TotalTokens  int      `json:"total_tokens"`
	InputTokens  int      `json:"input_tokens"`
	OutputTokens int      `json:"output_tokens"`
	DurationMs   int64    `json:"duration_ms"`
}

// MarshalJSON preserves unknown measurements as null rather than measured zero.
func (m RunMetrics) MarshalJSON() ([]byte, error) {
	type metrics RunMetrics
	data, err := json.Marshal(metrics(m))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for _, name := range m.Missing {
		fields[name] = json.RawMessage("null")
	}
	return json.Marshal(fields)
}

// CaseTransitions groups case status transitions.
type CaseTransitions struct {
	Fixed     []CaseTransition `json:"fixed"`
	Regressed []CaseTransition `json:"regressed"`
	Changed   []CaseTransition `json:"changed"`
	Unchanged []CaseTransition `json:"unchanged"`
	Added     []CaseTransition `json:"added"`
	Removed   []CaseTransition `json:"removed"`
}

// CaseTransition describes one case movement between old and new results.
type CaseTransition struct {
	PromptChanged    *bool        `json:"prompt_changed"`
	OldConfiguration string       `json:"old_configuration,omitempty"`
	NewConfiguration string       `json:"new_configuration,omitempty"`
	CaseID           string       `json:"case_id"`
	OldTitle         string       `json:"old_title,omitempty"`
	NewTitle         string       `json:"new_title,omitempty"`
	OldStatus        judge.Status `json:"old_status,omitempty"`
	NewStatus        judge.Status `json:"new_status,omitempty"`
}

// GateResult records whether CI gates passed and why they failed.
type GateResult struct {
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures"`
}

// Compare compares two offline report inputs.
func Compare(oldInput, newInput report.Input, options Options) Result {
	oldMetrics := collectRunMetrics(oldInput)
	newMetrics := collectRunMetrics(newInput)
	oldMetrics.Missing = options.OldMissingMetrics
	newMetrics.Missing = options.NewMissingMetrics
	result := Result{
		Baseline:  Source{Label: "Baseline"},
		Candidate: Source{Label: "Candidate"},
		Metadata:  metadataDiff(oldInput, newInput),
		Run: RunComparison{
			Old:   oldMetrics,
			New:   newMetrics,
			Delta: diffRunMetrics(oldMetrics, newMetrics),
		},
		Cases: compareCases(oldInput.PrimaryCaseResults(), newInput.PrimaryCaseResults()),
	}
	result.Coverage = Coverage{
		Shared: oldMetrics.CaseCount - len(result.Cases.Removed),
		Added:  len(result.Cases.Added), Removed: len(result.Cases.Removed),
	}
	result.Gates = EvaluateGates(result, options)
	return result
}

func collectRunMetrics(input report.Input) RunMetrics {
	primary := input.PrimaryCaseResults()
	metrics := RunMetrics{
		CaseCount:   len(primary),
		TotalTokens: input.TotalTokens,
		DurationMs:  input.TotalDuration().Milliseconds(),
	}
	for _, cr := range primary {
		if cr.Status == judge.StatusPass {
			metrics.PassCount++
		}
		metrics.InputTokens += cr.InputTokens
		metrics.OutputTokens += cr.OutputTokens
	}
	if metrics.CaseCount > 0 {
		metrics.PassRate = float64(metrics.PassCount) / float64(metrics.CaseCount)
	}
	return metrics
}

func diffRunMetrics(oldMetrics, newMetrics RunMetrics) RunMetrics {
	return RunMetrics{
		Missing:      slices.Concat(oldMetrics.Missing, newMetrics.Missing),
		CaseCount:    newMetrics.CaseCount - oldMetrics.CaseCount,
		PassCount:    newMetrics.PassCount - oldMetrics.PassCount,
		PassRate:     newMetrics.PassRate - oldMetrics.PassRate,
		TotalTokens:  newMetrics.TotalTokens - oldMetrics.TotalTokens,
		InputTokens:  newMetrics.InputTokens - oldMetrics.InputTokens,
		OutputTokens: newMetrics.OutputTokens - oldMetrics.OutputTokens,
		DurationMs:   newMetrics.DurationMs - oldMetrics.DurationMs,
	}
}

func metadataDiff(oldInput, newInput report.Input) MetadataDiff {
	return MetadataDiff{
		RequestedConfiguration: fieldDiff(configurationText(oldInput.RequestedConfiguration), configurationText(newInput.RequestedConfiguration)),
		AppliedConfiguration:   fieldDiff(configurationText(oldInput.AppliedConfiguration), configurationText(newInput.AppliedConfiguration)),
		ObservedConfiguration:  fieldDiff(configurationText(oldInput.ObservedConfiguration), configurationText(newInput.ObservedConfiguration)),
		SkillName:              fieldDiff(oldInput.SkillName, newInput.SkillName),
		SchemaVersion:          fieldDiff(oldInput.SchemaVersion, newInput.SchemaVersion),
		EngineName:             fieldDiff(oldInput.EngineName, newInput.EngineName),
		ModelName:              fieldDiff(oldInput.ModelName, newInput.ModelName),
		StartTime:              fieldDiff(oldInput.StartTime, newInput.StartTime),
		EndTime:                fieldDiff(oldInput.EndTime, newInput.EndTime),
	}
}

func configurationText(configuration *report.AgentConfiguration) string {
	if configuration == nil {
		return ""
	}
	data, err := json.Marshal(configuration)
	if err != nil {
		return ""
	}
	return string(data)
}

func fieldDiff[T comparable](oldValue, newValue T) FieldDiff[T] {
	return FieldDiff[T]{Old: oldValue, New: newValue, Changed: oldValue != newValue}
}

func compareCases(oldCases, newCases []report.CaseResult) CaseTransitions {
	newByID := make(map[string]report.CaseResult, len(newCases))
	for _, newCase := range newCases {
		newByID[newCase.CaseID] = newCase
	}

	transitions := CaseTransitions{
		Fixed:     make([]CaseTransition, 0),
		Regressed: make([]CaseTransition, 0),
		Changed:   make([]CaseTransition, 0),
		Unchanged: make([]CaseTransition, 0),
		Added:     make([]CaseTransition, 0),
		Removed:   make([]CaseTransition, 0),
	}
	for _, oldCase := range oldCases {
		newCase, exists := newByID[oldCase.CaseID]
		if !exists {
			transitions.Removed = append(transitions.Removed, CaseTransition{
				OldConfiguration: oldCase.Configuration,
				CaseID:           oldCase.CaseID, OldTitle: oldCase.Title, OldStatus: oldCase.Status,
			})
			continue
		}

		transition := CaseTransition{
			OldConfiguration: oldCase.Configuration, NewConfiguration: newCase.Configuration,
			CaseID: oldCase.CaseID, OldTitle: oldCase.Title, NewTitle: newCase.Title,
			OldStatus: oldCase.Status, NewStatus: newCase.Status,
		}
		if oldCase.Prompt != "" && newCase.Prompt != "" {
			changed := oldCase.Prompt != newCase.Prompt
			transition.PromptChanged = &changed
		}
		switch {
		case oldCase.Status != judge.StatusPass && newCase.Status == judge.StatusPass:
			transitions.Fixed = append(transitions.Fixed, transition)
		case oldCase.Status == judge.StatusPass && newCase.Status != judge.StatusPass:
			transitions.Regressed = append(transitions.Regressed, transition)
		case oldCase.Status == newCase.Status:
			transitions.Unchanged = append(transitions.Unchanged, transition)
		default:
			transitions.Changed = append(transitions.Changed, transition)
		}
	}

	oldByID := make(map[string]struct{}, len(oldCases))
	for _, oldCase := range oldCases {
		oldByID[oldCase.CaseID] = struct{}{}
	}
	for _, newCase := range newCases {
		if _, exists := oldByID[newCase.CaseID]; !exists {
			transitions.Added = append(transitions.Added, CaseTransition{
				NewConfiguration: newCase.Configuration,
				CaseID:           newCase.CaseID, NewTitle: newCase.Title, NewStatus: newCase.Status,
			})
		}
	}
	return transitions
}
