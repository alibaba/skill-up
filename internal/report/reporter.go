// Package report emits JSON, JUnit, and HTML reports from evaluation runs.
package report

import (
	"context"
	"time"

	"github.com/alibaba/skill-up/internal/judge"
)

// Reporter writes evaluation output to a chosen format.
type Reporter interface {
	Write(ctx context.Context, in Input) error
}

// Input aggregates run results for reporting.
type Input struct {
	SkillName     string           `json:"skill_name"`
	SchemaVersion string           `json:"schema_version"`
	EngineName    string           `json:"engine_name"`
	ModelName     string           `json:"model_name"`
	StartTime     time.Time        `json:"start_time"`
	EndTime       time.Time        `json:"end_time"`
	CaseResults   []CaseResult     `json:"case_results"`
	TotalTokens   int              `json:"total_tokens"`
	Benchmark     *BenchmarkResult `json:"benchmark,omitempty"`
}

// TotalDuration calculates the total wall-clock duration from StartTime to EndTime.
// Falls back to summing individual case durations if StartTime/EndTime are not set.
func (in Input) TotalDuration() time.Duration {
	if !in.StartTime.IsZero() && !in.EndTime.IsZero() {
		return in.EndTime.Sub(in.StartTime)
	}
	var total time.Duration
	for _, cr := range in.CaseResults {
		total += time.Duration(cr.DurationMs) * time.Millisecond
	}
	return total
}

// OverallPassRate calculates the overall pass rate across all cases.
//
// In benchmark mode (benchmark.enabled: true), CaseResults holds two entries
// per case: one "with_skill" and one "without_skill" baseline. The baseline
// is a reference data point, not an independent evaluation outcome, so it
// must not be double-counted here — otherwise a baseline failure would drag
// down the overall pass rate even though every with_skill case passed. See
// PrimaryCaseResults for the de-duplication rule.
func (in Input) OverallPassRate() float64 {
	primary := in.PrimaryCaseResults()
	if len(primary) == 0 {
		return 0
	}
	passed := 0
	for _, cr := range primary {
		if cr.Status == judge.StatusPass {
			passed++
		}
	}
	return float64(passed) / float64(len(primary))
}

// PrimaryCaseResults returns one CaseResult per case ID. It gives an exact
// "with_skill" result priority, otherwise uses a non-baseline result and
// falls back to the "without_skill" baseline when that is all that is
// available. Order follows first-seen case ID order in CaseResults. Use this
// instead of iterating CaseResults directly whenever computing an aggregate
// (pass rate, counts, CI failure signal) that should reflect one outcome per
// case rather than one outcome per configuration.
func (in Input) PrimaryCaseResults() []CaseResult {
	type slot struct {
		result   CaseResult
		occupied bool
	}
	order := make([]string, 0, len(in.CaseResults))
	byID := make(map[string]slot, len(in.CaseResults))

	for _, cr := range in.CaseResults {
		s, ok := byID[cr.CaseID]
		if !ok {
			order = append(order, cr.CaseID)
		}
		if cr.Configuration == "with_skill" {
			byID[cr.CaseID] = slot{result: cr, occupied: true}
			continue
		}
		if !s.occupied || s.result.Configuration == "without_skill" {
			byID[cr.CaseID] = slot{result: cr, occupied: true}
		}
	}

	out := make([]CaseResult, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id].result)
	}
	return out
}

// CaseResult represents the result of a single case execution.
type CaseResult struct {
	CaseID        string            `json:"case_id"`
	Title         string            `json:"title"`
	Status        judge.Status      `json:"status"`
	DurationMs    int64             `json:"duration_ms"`
	Turns         int               `json:"turns"`
	InputTokens   int               `json:"input_tokens"`
	OutputTokens  int               `json:"output_tokens"`
	Error         string            `json:"error,omitempty"`
	Grading       *judge.Result     `json:"grading"`
	JudgeSkills   []judge.SkillInfo `json:"judge_skills,omitempty"`
	Configuration string            `json:"configuration,omitempty"` // "with_skill" or "without_skill"
	Prompt        string            `json:"prompt,omitempty"`        // input prompt sent to the agent
	Response      string            `json:"response,omitempty"`      // agent final message
	TurnResults   []CaseTurnResult  `json:"turn_results,omitempty"`  // per-turn outcomes; nil for single-turn
}

// CaseTurnResult holds the outcome of a single turn for reporting purposes.
type CaseTurnResult struct {
	TurnNumber int    `json:"turn_number"`
	Content    string `json:"content"`
	Response   string `json:"response"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
}

// BenchmarkResult is the top-level structure for benchmark.json.
type BenchmarkResult struct {
	RunSummary BenchmarkRunSummary `json:"run_summary"`
}

// BenchmarkRunSummary holds the stats for with_skill and optionally without_skill.
type BenchmarkRunSummary struct {
	WithSkill    BenchmarkStats  `json:"with_skill"`
	WithoutSkill *BenchmarkStats `json:"without_skill"`
	Delta        *BenchmarkDelta `json:"delta"`
}

// BenchmarkStats holds the computed statistics for a run.
type BenchmarkStats struct {
	PassRate     StatValue `json:"pass_rate"`
	TimeSeconds  StatValue `json:"time_seconds"`
	InputTokens  StatValue `json:"input_tokens"`
	OutputTokens StatValue `json:"output_tokens"`
}

// StatValue holds mean and standard deviation.
type StatValue struct {
	Mean   float64 `json:"mean"`
	StdDev float64 `json:"stddev"`
}

// BenchmarkDelta holds the difference between with_skill and without_skill.
type BenchmarkDelta struct {
	PassRate     float64 `json:"pass_rate"`
	TimeSeconds  float64 `json:"time_seconds"`
	InputTokens  float64 `json:"input_tokens"`
	OutputTokens float64 `json:"output_tokens"`
}
