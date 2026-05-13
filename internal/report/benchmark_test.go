package report

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/judge"
)

// ---------------------------------------------------------------------------
// Mean — table-driven
// ---------------------------------------------------------------------------

func TestMean(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		values []float64
		want   float64
	}{
		{"empty", nil, 0},
		{"single", []float64{5.0}, 5.0},
		{"multiple", []float64{10.0, 20.0, 30.0}, 20.0},
		{"with_decimals", []float64{1.5, 2.5}, 2.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := mean(tt.values)
			if got != tt.want {
				t.Fatalf("mean(%v) = %f, want %f", tt.values, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// StdDev — table-driven
// ---------------------------------------------------------------------------

func TestStdDev(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		values []float64
		want   float64
		tol    float64 // tolerance for float comparison
	}{
		{"empty", nil, 0, 0},
		{"single", []float64{5.0}, 0, 0},
		{"identical", []float64{3.0, 3.0, 3.0}, 0, 0},
		{"simple", []float64{2.0, 4.0, 4.0, 4.0, 5.0, 5.0, 7.0, 9.0}, 2.0, 0.01},
		{"two_values", []float64{0, 10}, 5.0, 0.01},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := stdDev(tt.values)
			if math.Abs(got-tt.want) > tt.tol {
				t.Fatalf("stdDev(%v) = %f, want %f (±%f)", tt.values, got, tt.want, tt.tol)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// PassRate — table-driven
// ---------------------------------------------------------------------------

func TestPassRate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		passed []bool
		want   float64
	}{
		{"empty", nil, 0},
		{"all_pass", []bool{true, true, true}, 1.0},
		{"all_fail", []bool{false, false}, 0},
		{"mixed", []bool{true, false, true, false, true}, 0.6},
		{"single_pass", []bool{true}, 1.0},
		{"single_fail", []bool{false}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := passRate(tt.passed)
			if math.Abs(got-tt.want) > 0.001 {
				t.Fatalf("passRate(%v) = %f, want %f", tt.passed, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ComputeBenchmarkStats
// ---------------------------------------------------------------------------

func TestComputeBenchmarkStats_Empty(t *testing.T) {
	t.Parallel()
	stats := ComputeBenchmarkStats(nil)
	if stats.PassRate.Mean != 0 || stats.TimeSeconds.Mean != 0 || stats.InputTokens.Mean != 0 || stats.OutputTokens.Mean != 0 {
		t.Fatalf("expected all zeros for empty metrics, got %+v", stats)
	}
}

func TestComputeBenchmarkStats_SingleCase(t *testing.T) {
	t.Parallel()
	metrics := []CaseMetrics{
		{Passed: true, TimeSeconds: 45.0, InputTokens: 3000, OutputTokens: 800},
	}
	stats := ComputeBenchmarkStats(metrics)
	if stats.PassRate.Mean != 1.0 {
		t.Fatalf("expected pass_rate 1.0, got %f", stats.PassRate.Mean)
	}
	if stats.TimeSeconds.Mean != 45.0 {
		t.Fatalf("expected time 45.0, got %f", stats.TimeSeconds.Mean)
	}
	if stats.InputTokens.Mean != 3000 {
		t.Fatalf("expected input_tokens 3000, got %f", stats.InputTokens.Mean)
	}
	if stats.OutputTokens.Mean != 800 {
		t.Fatalf("expected output_tokens 800, got %f", stats.OutputTokens.Mean)
	}
	if stats.PassRate.StdDev != 0 || stats.TimeSeconds.StdDev != 0 || stats.InputTokens.StdDev != 0 || stats.OutputTokens.StdDev != 0 {
		t.Fatalf("expected 0 stddev for single sample, got %+v", stats)
	}
}

func TestComputeBenchmarkStats_MultipleCases(t *testing.T) {
	t.Parallel()
	metrics := []CaseMetrics{
		{Passed: true, TimeSeconds: 40.0, InputTokens: 2400, OutputTokens: 600},
		{Passed: true, TimeSeconds: 50.0, InputTokens: 3200, OutputTokens: 800},
		{Passed: false, TimeSeconds: 60.0, InputTokens: 4000, OutputTokens: 1000},
	}
	stats := ComputeBenchmarkStats(metrics)

	if math.Abs(stats.PassRate.Mean-2.0/3.0) > 0.001 {
		t.Fatalf("expected pass_rate ~0.667, got %f", stats.PassRate.Mean)
	}
	if stats.TimeSeconds.Mean != 50.0 {
		t.Fatalf("expected time mean 50.0, got %f", stats.TimeSeconds.Mean)
	}
	if stats.InputTokens.Mean != 3200.0 {
		t.Fatalf("expected input_tokens mean 3200, got %f", stats.InputTokens.Mean)
	}
	if stats.OutputTokens.Mean != 800.0 {
		t.Fatalf("expected output_tokens mean 800, got %f", stats.OutputTokens.Mean)
	}
	if stats.TimeSeconds.StdDev <= 0 {
		t.Fatal("expected positive time stddev")
	}
}

// ---------------------------------------------------------------------------
// ComputeBenchmark — simplified mode (without_skill = nil)
// ---------------------------------------------------------------------------

func TestComputeBenchmark_SimplifiedMode(t *testing.T) {
	t.Parallel()
	metrics := []CaseMetrics{
		{Passed: true, TimeSeconds: 45.0, InputTokens: 3000, OutputTokens: 800},
		{Passed: true, TimeSeconds: 38.7, InputTokens: 2500, OutputTokens: 700},
		{Passed: false, TimeSeconds: 62.1, InputTokens: 3600, OutputTokens: 900},
	}
	result := ComputeBenchmark(metrics, nil)

	if result.RunSummary.WithoutSkill != nil {
		t.Fatal("simplified mode should have without_skill = nil")
	}
	if result.RunSummary.Delta != nil {
		t.Fatal("simplified mode should have delta = nil")
	}
	if result.RunSummary.WithSkill.PassRate.Mean == 0 {
		t.Fatal("with_skill should have non-zero pass_rate")
	}
}

func TestComputeBenchmark_SimplifiedMode_JSONFormat(t *testing.T) {
	t.Parallel()
	metrics := []CaseMetrics{
		{Passed: true, TimeSeconds: 45.0, InputTokens: 3000, OutputTokens: 800},
	}
	result := ComputeBenchmark(metrics, nil)

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	content := string(data)

	// Verify null fields match design doc format.
	if !strings.Contains(content, `"without_skill": null`) {
		t.Fatalf("expected without_skill: null in json, got:\n%s", content)
	}
	if !strings.Contains(content, `"delta": null`) {
		t.Fatalf("expected delta: null in json, got:\n%s", content)
	}
}

// ---------------------------------------------------------------------------
// ComputeBenchmark — full mode (with delta)
// ---------------------------------------------------------------------------

func TestComputeBenchmark_FullMode(t *testing.T) {
	t.Parallel()
	withMetrics := []CaseMetrics{
		{Passed: true, TimeSeconds: 45.0, InputTokens: 3000, OutputTokens: 800},
		{Passed: true, TimeSeconds: 38.7, InputTokens: 2500, OutputTokens: 700},
		{Passed: true, TimeSeconds: 55.3, InputTokens: 3400, OutputTokens: 800},
		{Passed: true, TimeSeconds: 89.4, InputTokens: 3200, OutputTokens: 800},
		{Passed: false, TimeSeconds: 62.1, InputTokens: 3600, OutputTokens: 900},
	}
	withoutMetrics := []CaseMetrics{
		{Passed: false, TimeSeconds: 58.0, InputTokens: 4400, OutputTokens: 1100},
		{Passed: false, TimeSeconds: 52.0, InputTokens: 4160, OutputTokens: 1040},
		{Passed: true, TimeSeconds: 65.0, InputTokens: 4640, OutputTokens: 1160},
		{Passed: false, TimeSeconds: 70.0, InputTokens: 4800, OutputTokens: 1200},
		{Passed: false, TimeSeconds: 48.0, InputTokens: 3840, OutputTokens: 960},
	}
	result := ComputeBenchmark(withMetrics, withoutMetrics)

	if result.RunSummary.WithoutSkill == nil {
		t.Fatal("full mode should have without_skill")
	}
	if result.RunSummary.Delta == nil {
		t.Fatal("full mode should have delta")
	}

	// with_skill pass_rate: 4/5 = 0.8
	if math.Abs(result.RunSummary.WithSkill.PassRate.Mean-0.8) > 0.001 {
		t.Fatalf("expected with_skill pass_rate 0.8, got %f", result.RunSummary.WithSkill.PassRate.Mean)
	}
	// without_skill pass_rate: 1/5 = 0.2
	if math.Abs(result.RunSummary.WithoutSkill.PassRate.Mean-0.2) > 0.001 {
		t.Fatalf("expected without_skill pass_rate 0.2, got %f", result.RunSummary.WithoutSkill.PassRate.Mean)
	}
	// delta pass_rate: 0.8 - 0.2 = 0.6
	if math.Abs(result.RunSummary.Delta.PassRate-0.6) > 0.001 {
		t.Fatalf("expected delta pass_rate 0.6, got %f", result.RunSummary.Delta.PassRate)
	}
	// delta time should be negative (with_skill is faster).
	// with mean: (45+38.7+55.3+89.4+62.1)/5 = 58.1, without mean: (58+52+65+70+48)/5 = 58.6
	// delta: 58.1 - 58.6 = -0.5
	if result.RunSummary.Delta.TimeSeconds >= 0 {
		t.Fatalf("expected negative time delta (skill saves time), got %f", result.RunSummary.Delta.TimeSeconds)
	}
	// delta input_tokens should be negative (with_skill uses fewer tokens).
	if result.RunSummary.Delta.InputTokens >= 0 {
		t.Fatalf("expected negative input_tokens delta, got %f", result.RunSummary.Delta.InputTokens)
	}
	if result.RunSummary.Delta.OutputTokens >= 0 {
		t.Fatalf("expected negative output_tokens delta, got %f", result.RunSummary.Delta.OutputTokens)
	}
}

// ---------------------------------------------------------------------------
// ExtractMetrics
// ---------------------------------------------------------------------------

func TestExtractMetrics(t *testing.T) {
	t.Parallel()
	results := []CaseResult{
		{Status: judge.StatusPass, DurationMs: 45200},
		{Status: judge.StatusFail, DurationMs: 62100},
		{Status: judge.StatusSkip, DurationMs: 1000},
	}
	metrics := ExtractMetrics(results)

	if len(metrics) != 3 {
		t.Fatalf("expected 3 metrics, got %d", len(metrics))
	}
	if !metrics[0].Passed {
		t.Fatal("first metric should be passed")
	}
	if metrics[1].Passed {
		t.Fatal("second metric should not be passed")
	}
	if metrics[2].Passed {
		t.Fatal("third metric (skip) should not be passed")
	}
	if math.Abs(metrics[0].TimeSeconds-45.2) > 0.001 {
		t.Fatalf("expected 45.2s, got %f", metrics[0].TimeSeconds)
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestComputeBenchmark_AllPass(t *testing.T) {
	t.Parallel()
	metrics := []CaseMetrics{
		{Passed: true, TimeSeconds: 10, InputTokens: 80, OutputTokens: 20},
		{Passed: true, TimeSeconds: 20, InputTokens: 160, OutputTokens: 40},
	}
	result := ComputeBenchmark(metrics, nil)
	if result.RunSummary.WithSkill.PassRate.Mean != 1.0 {
		t.Fatalf("expected pass_rate 1.0, got %f", result.RunSummary.WithSkill.PassRate.Mean)
	}
}

func TestComputeBenchmark_AllFail(t *testing.T) {
	t.Parallel()
	metrics := []CaseMetrics{
		{Passed: false, TimeSeconds: 10, InputTokens: 80, OutputTokens: 20},
		{Passed: false, TimeSeconds: 20, InputTokens: 160, OutputTokens: 40},
	}
	result := ComputeBenchmark(metrics, nil)
	if result.RunSummary.WithSkill.PassRate.Mean != 0 {
		t.Fatalf("expected pass_rate 0, got %f", result.RunSummary.WithSkill.PassRate.Mean)
	}
}

// ---------------------------------------------------------------------------
// Anthropic benchmark format tests
// ---------------------------------------------------------------------------

func TestMinMax(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		values  []float64
		wantMin float64
		wantMax float64
	}{
		{"empty", nil, 0, 0},
		{"single", []float64{5.0}, 5.0, 5.0},
		{"multiple", []float64{3.0, 1.0, 4.0, 1.5, 9.0}, 1.0, 9.0},
		{"identical", []float64{2.0, 2.0, 2.0}, 2.0, 2.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := sliceMin(tt.values); got != tt.wantMin {
				t.Errorf("sliceMin(%v) = %f, want %f", tt.values, got, tt.wantMin)
			}
			if got := sliceMax(tt.values); got != tt.wantMax {
				t.Errorf("sliceMax(%v) = %f, want %f", tt.values, got, tt.wantMax)
			}
		})
	}
}

func TestComputeAnthropicStatValue(t *testing.T) {
	t.Parallel()
	values := []float64{0.4, 0.6, 0.4}
	sv := ComputeAnthropicStatValue(values)

	if math.Abs(sv.Mean-0.4667) > 0.01 {
		t.Errorf("expected mean ~0.47, got %f", sv.Mean)
	}
	if sv.Min != 0.4 {
		t.Errorf("expected min 0.4, got %f", sv.Min)
	}
	if sv.Max != 0.6 {
		t.Errorf("expected max 0.6, got %f", sv.Max)
	}
	if sv.StdDev <= 0 {
		t.Error("expected positive stddev")
	}
}

func TestComputeAnthropicBenchmark_WithSkillOnly(t *testing.T) {
	t.Parallel()
	runs := []BenchmarkRun{
		{
			EvalID: 1, EvalName: "test-1", Configuration: "with_skill", RunNumber: 1,
			Result:       BenchmarkRunResult{PassRate: 1.0, Passed: 3, Failed: 0, Total: 3},
			Expectations: []AnthropicExpectation{{Text: "test", Passed: true, Evidence: "ok"}},
		},
	}

	bm := ComputeAnthropicBenchmark("test-skill", "/path/to/skill", runs, nil)

	if bm.Metadata.SkillName != "test-skill" {
		t.Errorf("expected skill_name 'test-skill', got %s", bm.Metadata.SkillName)
	}
	if bm.RunSummary.WithoutSkill != nil {
		t.Error("expected without_skill to be nil")
	}
	if bm.RunSummary.Delta != nil {
		t.Error("expected delta to be nil")
	}
	if bm.RunSummary.WithSkill.PassRate.Mean != 1.0 {
		t.Errorf("expected pass_rate mean 1.0, got %f", bm.RunSummary.WithSkill.PassRate.Mean)
	}
}

func TestComputeAnthropicBenchmark_FullMode(t *testing.T) {
	t.Parallel()
	withRuns := []BenchmarkRun{
		{
			EvalID: 1, EvalName: "a", Configuration: "with_skill", RunNumber: 1,
			Result: BenchmarkRunResult{PassRate: 1.0, Passed: 5, Total: 5, TimeSeconds: 40, Tokens: 3000},
		},
		{
			EvalID: 2, EvalName: "b", Configuration: "with_skill", RunNumber: 1,
			Result: BenchmarkRunResult{PassRate: 1.0, Passed: 5, Total: 5, TimeSeconds: 50, Tokens: 3200},
		},
	}
	withoutRuns := []BenchmarkRun{
		{
			EvalID: 1, EvalName: "a", Configuration: "without_skill", RunNumber: 1,
			Result: BenchmarkRunResult{PassRate: 0.4, Passed: 2, Failed: 3, Total: 5, TimeSeconds: 55, Tokens: 4200},
		},
		{
			EvalID: 2, EvalName: "b", Configuration: "without_skill", RunNumber: 1,
			Result: BenchmarkRunResult{PassRate: 0.6, Passed: 3, Failed: 2, Total: 5, TimeSeconds: 65, Tokens: 4600},
		},
	}

	bm := ComputeAnthropicBenchmark("test-skill", "/path", withRuns, withoutRuns)

	if bm.RunSummary.WithoutSkill == nil {
		t.Fatal("expected without_skill")
	}
	if bm.RunSummary.Delta == nil {
		t.Fatal("expected delta")
	}
	if len(bm.Runs) != 4 {
		t.Errorf("expected 4 runs, got %d", len(bm.Runs))
	}
	if bm.RunSummary.WithSkill.TimeSeconds.Mean != 45 {
		t.Errorf("expected with_skill time mean 45, got %f", bm.RunSummary.WithSkill.TimeSeconds.Mean)
	}
	if bm.RunSummary.WithSkill.Tokens.Mean != 3100 {
		t.Errorf("expected with_skill tokens mean 3100, got %f", bm.RunSummary.WithSkill.Tokens.Mean)
	}
	if bm.RunSummary.Delta.TimeSeconds != "-15.0" {
		t.Errorf("expected time delta -15.0, got %q", bm.RunSummary.Delta.TimeSeconds)
	}
	if bm.RunSummary.Delta.Tokens != "-1300.0" {
		t.Errorf("expected token delta -1300.0, got %q", bm.RunSummary.Delta.Tokens)
	}
}

func TestComputeAnthropicBenchmark_JSONFormat(t *testing.T) {
	t.Parallel()
	// Verify JSON output contains expected Anthropic fields.
	withRuns := []BenchmarkRun{
		{
			EvalID: 1, EvalName: "test", Configuration: "with_skill", RunNumber: 1,
			Result:       BenchmarkRunResult{PassRate: 1.0, Passed: 5, Total: 5},
			Expectations: []AnthropicExpectation{{Text: "t", Passed: true, Evidence: "e"}},
		},
	}

	bm := ComputeAnthropicBenchmark("chinese-jokes", "/path", withRuns, nil)
	data, err := json.MarshalIndent(bm, "", "  ")
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	for _, key := range []string{"metadata", "runs", "run_summary"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("missing top-level field %q", key)
		}
	}

	meta, ok := parsed["metadata"].(map[string]any)
	if !ok {
		t.Fatal("metadata is not map[string]any")
	}
	if meta["skill_name"] != "chinese-jokes" {
		t.Errorf("expected skill_name 'chinese-jokes', got %v", meta["skill_name"])
	}
}

func TestWriteAnthropicBenchmark(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "benchmark.json")

	bm := &AnthropicBenchmark{
		Metadata: BenchmarkMetadata{
			SkillName: "test",
			Timestamp: "2026-04-08T15:20:00Z",
			EvalsRun:  []int{1},
		},
		Runs: []BenchmarkRun{},
		RunSummary: AnthropicRunSummary{
			WithSkill: AnthropicStatSummary{
				PassRate: AnthropicStatValue{Mean: 1.0},
			},
		},
	}

	if err := WriteAnthropicBenchmark(path, bm); err != nil {
		t.Fatalf("WriteAnthropicBenchmark error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file error: %v", err)
	}

	var loaded AnthropicBenchmark
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if loaded.Metadata.SkillName != "test" {
		t.Errorf("expected skill_name 'test', got %s", loaded.Metadata.SkillName)
	}
}
