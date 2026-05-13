// Package report — benchmark.go implements benchmark.json generation logic.
//
// Benchmark is the final aggregation step of the evaluation pipeline.
// It computes statistics (mean, stddev) for pass_rate, time, and tokens,
// and optionally calculates delta between with_skill and without_skill runs.
//
// Two modes (design doc):
//   - Simplified mode (default): only with_skill, without_skill=null, delta=null
//   - Full mode (benchmark.enabled=true): with_skill + without_skill + delta
package report

import (
	"math"

	"github.com/alibaba/skill-up/internal/judge"
)

// CaseMetrics holds the raw metrics for a single case execution.
type CaseMetrics struct {
	Passed       bool    // whether the case passed
	TimeSeconds  float64 // execution time in seconds
	InputTokens  float64 // input tokens consumed
	OutputTokens float64 // output tokens consumed
}

// ---------------------------------------------------------------------------
// Statistics functions (pure computation, no I/O)
// ---------------------------------------------------------------------------

// mean computes the arithmetic mean of a float64 slice.
// Returns 0 for an empty slice.
func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// stdDev computes the population standard deviation of a float64 slice.
// Returns 0 for slices with fewer than 2 elements.
func stdDev(values []float64) float64 {
	n := len(values)
	if n < 2 {
		return 0
	}
	avg := mean(values)
	var sumSq float64
	for _, v := range values {
		d := v - avg
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(n))
}

// passRate computes the fraction of true values in a bool slice.
// Returns 0 for an empty slice.
func passRate(passed []bool) float64 {
	if len(passed) == 0 {
		return 0
	}
	count := 0
	for _, p := range passed {
		if p {
			count++
		}
	}
	return float64(count) / float64(len(passed))
}

// ---------------------------------------------------------------------------
// Benchmark computation
// ---------------------------------------------------------------------------

// ComputeBenchmarkStats computes BenchmarkStats from a slice of CaseMetrics.
func ComputeBenchmarkStats(metrics []CaseMetrics) BenchmarkStats {
	if len(metrics) == 0 {
		return BenchmarkStats{}
	}

	passed := make([]bool, len(metrics))
	times := make([]float64, len(metrics))
	inputTokens := make([]float64, len(metrics))
	outputTokens := make([]float64, len(metrics))

	for i, m := range metrics {
		passed[i] = m.Passed
		times[i] = m.TimeSeconds
		inputTokens[i] = m.InputTokens
		outputTokens[i] = m.OutputTokens
	}

	passFloats := make([]float64, len(passed))
	for i, p := range passed {
		if p {
			passFloats[i] = 1
		}
	}

	return BenchmarkStats{
		PassRate:     StatValue{Mean: passRate(passed), StdDev: stdDev(passFloats)},
		TimeSeconds:  StatValue{Mean: mean(times), StdDev: stdDev(times)},
		InputTokens:  StatValue{Mean: mean(inputTokens), StdDev: stdDev(inputTokens)},
		OutputTokens: StatValue{Mean: mean(outputTokens), StdDev: stdDev(outputTokens)},
	}
}

// ComputeBenchmarkDelta computes the delta between with_skill and without_skill stats.
func ComputeBenchmarkDelta(withSkill, withoutSkill BenchmarkStats) BenchmarkDelta {
	return BenchmarkDelta{
		PassRate:     withSkill.PassRate.Mean - withoutSkill.PassRate.Mean,
		TimeSeconds:  withSkill.TimeSeconds.Mean - withoutSkill.TimeSeconds.Mean,
		InputTokens:  withSkill.InputTokens.Mean - withoutSkill.InputTokens.Mean,
		OutputTokens: withSkill.OutputTokens.Mean - withoutSkill.OutputTokens.Mean,
	}
}

// ComputeBenchmark builds the complete BenchmarkResult.
//
//   - Simplified mode: pass withoutSkillMetrics as nil.
//   - Full mode: pass both withSkillMetrics and withoutSkillMetrics.
func ComputeBenchmark(withSkillMetrics []CaseMetrics, withoutSkillMetrics []CaseMetrics) *BenchmarkResult {
	withStats := ComputeBenchmarkStats(withSkillMetrics)

	result := &BenchmarkResult{
		RunSummary: BenchmarkRunSummary{
			WithSkill:    withStats,
			WithoutSkill: nil,
			Delta:        nil,
		},
	}

	if withoutSkillMetrics != nil {
		withoutStats := ComputeBenchmarkStats(withoutSkillMetrics)
		delta := ComputeBenchmarkDelta(withStats, withoutStats)
		result.RunSummary.WithoutSkill = &withoutStats
		result.RunSummary.Delta = &delta
	}

	return result
}

// ---------------------------------------------------------------------------
// Helper: extract CaseMetrics from CaseResults
// ---------------------------------------------------------------------------

// ExtractMetrics converts CaseResults into CaseMetrics for benchmark computation.
func ExtractMetrics(results []CaseResult) []CaseMetrics {
	metrics := make([]CaseMetrics, len(results))
	for i, r := range results {
		metrics[i] = CaseMetrics{
			Passed:       r.Status == judge.StatusPass,
			TimeSeconds:  float64(r.DurationMs) / 1000.0,
			InputTokens:  float64(r.InputTokens),
			OutputTokens: float64(r.OutputTokens),
		}
	}
	return metrics
}
