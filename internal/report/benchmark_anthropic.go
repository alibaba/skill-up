// Package report — benchmark_anthropic.go implements the Anthropic-compatible
// benchmark.json format and computation logic.
//
// This file contains types and functions for generating benchmark outputs
// that are compatible with Anthropic's eval-viewer and skill-creator tooling.
package report

import (
	"fmt"
	"sort"
	"time"
)

// ---------------------------------------------------------------------------
// Anthropic-compatible benchmark.json format
// ---------------------------------------------------------------------------

// AnthropicBenchmark corresponds to the full Anthropic benchmark.json schema.
//
// This format includes metadata, per-run details, summary statistics, and
// optional notes — matching the demo/chinese-jokes-workspace/benchmark.json.
type AnthropicBenchmark struct {
	Metadata   BenchmarkMetadata   `json:"metadata"`
	Runs       []BenchmarkRun      `json:"runs"`
	RunSummary AnthropicRunSummary `json:"run_summary"`
	Notes      []string            `json:"notes,omitempty"`
}

// BenchmarkMetadata holds skill and execution metadata.
type BenchmarkMetadata struct {
	SkillName            string `json:"skill_name"`
	SkillPath            string `json:"skill_path"`
	Timestamp            string `json:"timestamp"`
	EvalsRun             []int  `json:"evals_run"`
	RunsPerConfiguration int    `json:"runs_per_configuration"`
}

// BenchmarkRun holds per-eval, per-configuration run details.
type BenchmarkRun struct {
	EvalID        int                    `json:"eval_id"`
	EvalName      string                 `json:"eval_name"`
	Configuration string                 `json:"configuration"`
	RunNumber     int                    `json:"run_number"`
	Result        BenchmarkRunResult     `json:"result"`
	Expectations  []AnthropicExpectation `json:"expectations"`
}

// BenchmarkRunResult holds aggregated metrics for a single run.
type BenchmarkRunResult struct {
	PassRate    float64 `json:"pass_rate"`
	Passed      int     `json:"passed"`
	Failed      int     `json:"failed"`
	Total       int     `json:"total"`
	TimeSeconds float64 `json:"time_seconds"`
	Tokens      int     `json:"tokens"`
	Errors      int     `json:"errors"`
}

// AnthropicRunSummary holds per-configuration summary statistics.
type AnthropicRunSummary struct {
	WithSkill    AnthropicStatSummary  `json:"with_skill"`
	WithoutSkill *AnthropicStatSummary `json:"without_skill"`
	Delta        *AnthropicDelta       `json:"delta"`
}

// AnthropicStatSummary holds statistics with min/max for a configuration.
type AnthropicStatSummary struct {
	PassRate    AnthropicStatValue `json:"pass_rate"`
	TimeSeconds AnthropicStatValue `json:"time_seconds"`
	Tokens      AnthropicStatValue `json:"tokens"`
}

// AnthropicStatValue holds mean, stddev, min, max for a metric.
type AnthropicStatValue struct {
	Mean   float64 `json:"mean"`
	StdDev float64 `json:"stddev"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

// AnthropicDelta holds the string-formatted delta values between configurations.
type AnthropicDelta struct {
	PassRate    string `json:"pass_rate"`
	TimeSeconds string `json:"time_seconds"`
	Tokens      string `json:"tokens"`
}

// ---------------------------------------------------------------------------
// Statistics helpers (min/max)
// ---------------------------------------------------------------------------

// sliceMin computes the minimum of a float64 slice. Returns 0 for empty slices.
func sliceMin(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	m := values[0]
	for _, v := range values[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

// sliceMax computes the maximum of a float64 slice. Returns 0 for empty slices.
func sliceMax(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	m := values[0]
	for _, v := range values[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

// ComputeAnthropicStatValue computes an AnthropicStatValue from raw values.
func ComputeAnthropicStatValue(values []float64) AnthropicStatValue {
	return AnthropicStatValue{
		Mean:   mean(values),
		StdDev: stdDev(values),
		Min:    sliceMin(values),
		Max:    sliceMax(values),
	}
}

// ---------------------------------------------------------------------------
// Anthropic benchmark computation
// ---------------------------------------------------------------------------

// ComputeAnthropicBenchmark builds the full Anthropic-compatible benchmark
// from evaluation run data. An optional timestamp can be provided; if empty,
// the current UTC time is used.
func ComputeAnthropicBenchmark(
	skillName, skillPath string,
	withSkillRuns []BenchmarkRun,
	withoutSkillRuns []BenchmarkRun,
	opts ...func(*benchmarkOptions),
) *AnthropicBenchmark {
	o := benchmarkOptions{timestamp: time.Now().UTC()}
	for _, fn := range opts {
		fn(&o)
	}
	timestamp := o.timestamp.Format(time.RFC3339)

	// Collect eval IDs from runs.
	evalIDSet := map[int]struct{}{}
	allRuns := append(append([]BenchmarkRun{}, withSkillRuns...), withoutSkillRuns...)
	for _, r := range allRuns {
		evalIDSet[r.EvalID] = struct{}{}
	}
	evalIDs := make([]int, 0, len(evalIDSet))
	for id := range evalIDSet {
		evalIDs = append(evalIDs, id)
	}
	sort.Ints(evalIDs)

	withSummary := computeAnthropicRunSummary(withSkillRuns)

	bm := &AnthropicBenchmark{
		Metadata: BenchmarkMetadata{
			SkillName:            skillName,
			SkillPath:            skillPath,
			Timestamp:            timestamp,
			EvalsRun:             evalIDs,
			RunsPerConfiguration: 1,
		},
		Runs:       allRuns,
		RunSummary: AnthropicRunSummary{WithSkill: withSummary},
	}

	if len(withoutSkillRuns) > 0 {
		withoutSummary := computeAnthropicRunSummary(withoutSkillRuns)
		bm.RunSummary.WithoutSkill = &withoutSummary

		bm.RunSummary.Delta = &AnthropicDelta{
			PassRate:    fmt.Sprintf("%+.2f", withSummary.PassRate.Mean-withoutSummary.PassRate.Mean),
			TimeSeconds: fmt.Sprintf("%+.1f", withSummary.TimeSeconds.Mean-withoutSummary.TimeSeconds.Mean),
			Tokens:      fmt.Sprintf("%+.1f", withSummary.Tokens.Mean-withoutSummary.Tokens.Mean),
		}
	}

	return bm
}

// benchmarkOptions holds optional configuration for ComputeAnthropicBenchmark.
type benchmarkOptions struct {
	timestamp time.Time
}

// WithTimestamp sets a fixed timestamp for the benchmark metadata.
func WithTimestamp(t time.Time) func(*benchmarkOptions) {
	return func(o *benchmarkOptions) {
		o.timestamp = t
	}
}

func computeAnthropicRunSummary(runs []BenchmarkRun) AnthropicStatSummary {
	return AnthropicStatSummary{
		PassRate:    ComputeAnthropicStatValue(extractRunMetric(runs, func(run BenchmarkRun) float64 { return run.Result.PassRate })),
		TimeSeconds: ComputeAnthropicStatValue(extractRunMetric(runs, func(run BenchmarkRun) float64 { return run.Result.TimeSeconds })),
		Tokens:      ComputeAnthropicStatValue(extractRunMetric(runs, func(run BenchmarkRun) float64 { return float64(run.Result.Tokens) })),
	}
}

func extractRunMetric(runs []BenchmarkRun, fn func(BenchmarkRun) float64) []float64 {
	values := make([]float64, len(runs))
	for i, r := range runs {
		values[i] = fn(r)
	}
	return values
}

// WriteAnthropicBenchmark writes an AnthropicBenchmark to the specified file.
func WriteAnthropicBenchmark(path string, bm *AnthropicBenchmark) error {
	return writeJSONFile(path, bm, "anthropic benchmark")
}
