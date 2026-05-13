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
func (in Input) OverallPassRate() float64 {
	if len(in.CaseResults) == 0 {
		return 0
	}
	passed := 0
	for _, cr := range in.CaseResults {
		if cr.Status == judge.StatusPass {
			passed++
		}
	}
	return float64(passed) / float64(len(in.CaseResults))
}

// CaseResult represents the result of a single case execution.
type CaseResult struct {
	CaseID        string        `json:"case_id"`
	Title         string        `json:"title"`
	Status        judge.Status  `json:"status"`
	DurationMs    int64         `json:"duration_ms"`
	Turns         int           `json:"turns"`
	InputTokens   int           `json:"input_tokens"`
	OutputTokens  int           `json:"output_tokens"`
	Error         string        `json:"error,omitempty"`
	Grading       *judge.Result `json:"grading"`
	Configuration string        `json:"configuration,omitempty"` // "with_skill" or "without_skill"
	Prompt        string        `json:"prompt,omitempty"`        // input prompt sent to the agent
	Response      string        `json:"response,omitempty"`      // agent final message
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
