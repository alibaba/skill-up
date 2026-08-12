// Package compare compares two offline skill-up report results.
package compare

import (
	"time"

	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
)

// Options controls compare output and gate evaluation.
type Options struct {
	FailOnRegression        bool
	MaxRegressions          *int
	MaxTokenIncreasePercent *float64
}

// Result is the stable comparison result used by text and JSON output.
type Result struct {
	Metadata MetadataDiff    `json:"metadata"`
	Run      RunComparison   `json:"run"`
	Cases    CaseTransitions `json:"cases"`
	Gates    GateResult      `json:"gates"`
}

// MetadataDiff records old and new run metadata.
type MetadataDiff struct {
	SkillName     FieldDiff[string]    `json:"skill_name"`
	SchemaVersion FieldDiff[string]    `json:"schema_version"`
	EngineName    FieldDiff[string]    `json:"engine_name"`
	ModelName     FieldDiff[string]    `json:"model_name"`
	StartTime     FieldDiff[time.Time] `json:"start_time"`
	EndTime       FieldDiff[time.Time] `json:"end_time"`
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
	CaseCount    int     `json:"case_count"`
	PassCount    int     `json:"pass_count"`
	PassRate     float64 `json:"pass_rate"`
	TotalTokens  int     `json:"total_tokens"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	DurationMs   int64   `json:"duration_ms"`
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
	CaseID    string       `json:"case_id"`
	OldTitle  string       `json:"old_title,omitempty"`
	NewTitle  string       `json:"new_title,omitempty"`
	OldStatus judge.Status `json:"old_status,omitempty"`
	NewStatus judge.Status `json:"new_status,omitempty"`
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
	result := Result{
		Metadata: metadataDiff(oldInput, newInput),
		Run: RunComparison{
			Old:   oldMetrics,
			New:   newMetrics,
			Delta: diffRunMetrics(oldMetrics, newMetrics),
		},
		Cases: compareCases(oldInput.PrimaryCaseResults(), newInput.PrimaryCaseResults()),
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
		SkillName:     fieldDiff(oldInput.SkillName, newInput.SkillName),
		SchemaVersion: fieldDiff(oldInput.SchemaVersion, newInput.SchemaVersion),
		EngineName:    fieldDiff(oldInput.EngineName, newInput.EngineName),
		ModelName:     fieldDiff(oldInput.ModelName, newInput.ModelName),
		StartTime:     fieldDiff(oldInput.StartTime, newInput.StartTime),
		EndTime:       fieldDiff(oldInput.EndTime, newInput.EndTime),
	}
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
				CaseID: oldCase.CaseID, OldTitle: oldCase.Title, OldStatus: oldCase.Status,
			})
			continue
		}

		transition := CaseTransition{
			CaseID: oldCase.CaseID, OldTitle: oldCase.Title, NewTitle: newCase.Title,
			OldStatus: oldCase.Status, NewStatus: newCase.Status,
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
				CaseID: newCase.CaseID, NewTitle: newCase.Title, NewStatus: newCase.Status,
			})
		}
	}
	return transitions
}
