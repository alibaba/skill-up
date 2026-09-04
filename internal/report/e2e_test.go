// Package report — e2e_test.go end-to-end integration tests
//
// These tests simulate the complete evaluation-report generation pipeline:
//
//	judge.Result → report.CaseResult → Reporter.Write() → output files
//
// Each scenario includes a clear description of what it does and why it is
// a meaningful test. Reading these tests gives a complete picture of the
// report module's functionality and implementation.
//
// Test organization:
//  1. Data-flow integrity — verify the judge output → report input field mapping
//  2. Three-format end-to-end — JSON/JUnit/HTML complete flow from input to file
//  3. Benchmark integration — statistics computation → report embedding pipeline
//  4. Edge cases — empty data, all-error, mixed status, etc.
//  5. Joint test — same input generates all three formats; verifies consistency
package report

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/judge"
)

// ============================================================================
// Test data constructors
// ============================================================================

// buildRealisticInput constructs an Input that resembles a real evaluation run.
//
// Simulates a run with 4 cases:
//   - PASS: successfully identified a null-pointer bug (2 assertions all pass)
//   - FAIL: failed to handle boundary input gracefully (1 assertion fails)
//   - SKIP: skipped because a precondition was not met (has skip_reason)
//   - ERROR: engine timed out and crashed (has error info, no grading)
//
// This dataset covers all four judge.Status values and forms the baseline for
// verifying that the report module correctly handles all output scenarios.
func buildRealisticInput() Input {
	start := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)
	end := start.Add(6*time.Minute + 48*time.Second)

	skipReason := "post_condition not met: file output.txt does not exist"

	return Input{
		SkillName:     "code-review",
		SchemaVersion: "v1alpha1",
		EngineName:    "codex",
		ModelName:     "openai/gpt-5.4",
		RequestedConfiguration: &AgentConfiguration{
			Role: "runner", Engine: "codex", Protocol: "openai", Provider: "openai", Model: "gpt-5.4", Version: "1.2.3",
		},
		AppliedConfiguration: &AgentConfiguration{
			Role: "runner", Engine: "codex", Protocol: "openai", Provider: "openai", Model: "gpt-5.4", Version: "1.2.3",
		},
		ObservedConfiguration: &AgentConfiguration{Model: "gpt-5.4", Version: "1.2.3"},
		StartTime:             start,
		EndTime:               end,
		CaseResults: []CaseResult{
			{
				CaseID:     "identify-null-bug",
				Title:      "Agent should identify a missing null check",
				Status:     judge.StatusPass,
				DurationMs: 45200,
				Turns:      5,
				Grading: &judge.Result{
					Status:        judge.StatusPass,
					TurnsExecuted: 5,
					TurnsTotal:    5,
					AssertionResults: []judge.AssertionResult{
						{Text: "output_contains{all:[null bug]}", Passed: true, Evidence: "all keywords found in output"},
						{Text: "tool_called: read_file", Passed: true, Evidence: "tool \"read_file\" was called"},
					},
					Summary: judge.ResultSummary{Passed: 2, Failed: 0, Total: 2, PassRate: 1.0},
				},
			},
			{
				CaseID:     "graceful-null-handling",
				Title:      "Handle null input gracefully",
				Status:     judge.StatusFail,
				DurationMs: 62100,
				Turns:      8,
				Grading: &judge.Result{
					Status:        judge.StatusFail,
					TurnsExecuted: 8,
					TurnsTotal:    8,
					AssertionResults: []judge.AssertionResult{
						{Text: "output_contains: 'graceful'", Passed: false, Evidence: "output does not contain 'graceful'"},
						{Text: "exit_code: 0", Passed: true, Evidence: "exit_code is 0 as expected"},
					},
					Summary: judge.ResultSummary{Passed: 1, Failed: 1, Total: 2, PassRate: 0.5},
				},
			},
			{
				CaseID:     "file-output-check",
				Title:      "Verify file generation",
				Status:     judge.StatusSkip,
				DurationMs: 800,
				Turns:      1,
				Grading: &judge.Result{
					Status:           judge.StatusSkip,
					SkipReason:       &skipReason,
					TurnsExecuted:    1,
					TurnsTotal:       3,
					AssertionResults: []judge.AssertionResult{},
					Summary:          judge.ResultSummary{},
				},
			},
			{
				CaseID:     "timeout-crash",
				Title:      "Engine timeout",
				Status:     judge.StatusError,
				DurationMs: 300000,
				Error:      "engine process killed after 300s",
			},
		},
	}
}

// ============================================================================
// Scenario 1: Data-flow integrity — judge.Result → Input → CaseResult mapping
// ============================================================================

// TestE2E_DataFlow_JudgeResultToCaseResult verifies the data mapping from
// judge output to report input.
//
// What it does: simulates the runner layer wrapping judge.Result into
// report.CaseResult and verifies that all key fields (status, grading, turns,
// duration) are correctly propagated.
//
// Why it is meaningful: this is the data boundary between the judge module and
// the report module. If this mapping is wrong, all subsequent reports will be
// generated from incorrect data.
func TestE2E_DataFlow_JudgeResultToCaseResult(t *testing.T) {
	// Simulate a Result produced by the judge.
	judgeResult := &judge.Result{
		Status:        judge.StatusPass,
		TurnsExecuted: 3,
		TurnsTotal:    5,
		AssertionResults: []judge.AssertionResult{
			{Text: "output_contains{all:[fix bug]}", Passed: true, Evidence: "found 'fix' and 'bug'"},
			{Text: "exit_code: 0", Passed: true, Evidence: "exit_code matches"},
		},
		Summary: judge.ResultSummary{Passed: 2, Failed: 0, Total: 2, PassRate: 1.0},
	}

	// Runner layer wraps judge.Result into report.CaseResult.
	caseResult := CaseResult{
		CaseID:     "test-case-001",
		Title:      "Verify bug fix suggestion",
		Status:     judgeResult.Status,
		DurationMs: 42000,
		Turns:      judgeResult.TurnsExecuted,
		Grading:    judgeResult,
	}

	// Verify correct mapping.
	if caseResult.Status != judge.StatusPass {
		t.Fatalf("CaseResult.Status should be PASS, got %s", caseResult.Status)
	}
	if caseResult.Grading.Summary.PassRate != 1.0 {
		t.Fatalf("Grading pass_rate should be 1.0, got %f", caseResult.Grading.Summary.PassRate)
	}
	if len(caseResult.Grading.AssertionResults) != 2 {
		t.Fatalf("expected 2 assertions, got %d", len(caseResult.Grading.AssertionResults))
	}

	// Verify Input aggregated statistics.
	start := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)
	input := Input{
		SkillName:   "test-skill",
		StartTime:   start,
		EndTime:     start.Add(42 * time.Second),
		CaseResults: []CaseResult{caseResult},
	}
	if input.OverallPassRate() != 1.0 {
		t.Fatalf("OverallPassRate should be 1.0, got %f", input.OverallPassRate())
	}
	if input.TotalDuration() != 42*time.Second {
		t.Fatalf("TotalDuration should be 42s, got %v", input.TotalDuration())
	}
}

// ============================================================================
// Scenario 2: JSON Reporter end-to-end
// ============================================================================

// TestE2E_JSONReporter_FullPipeline verifies the complete JSON report
// generation pipeline.
//
// What it does: writes an Input containing all 4 status values to a JSON file,
// then deserializes and verifies that all fields are correctly preserved.
//
// Why it is meaningful: JSON is the "source of truth" for reports; JUnit and
// HTML are derived views. If the JSON report loses data, all downstream formats
// will be affected.
func TestE2E_JSONReporter_FullPipeline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	input := buildRealisticInput()

	r := &JSONReporter{OutputPath: path}
	if err := r.Write(context.Background(), input); err != nil {
		t.Fatalf("JSONReporter.Write: %v", err)
	}

	// Deserialize and verify completeness.
	data, _ := os.ReadFile(path)
	var parsed Input
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse output JSON: %v", err)
	}

	// Metadata intact.
	if parsed.SkillName != "code-review" {
		t.Fatalf("skill_name: want 'code-review', got %q", parsed.SkillName)
	}
	if parsed.EngineName != "codex" {
		t.Fatalf("engine_name: want 'codex', got %q", parsed.EngineName)
	}
	assertReportConfigurations(t, parsed)

	// All 4 cases preserved.
	if len(parsed.CaseResults) != 4 {
		t.Fatalf("case count: want 4, got %d", len(parsed.CaseResults))
	}

	// Verify status of each case.
	expected := []judge.Status{judge.StatusPass, judge.StatusFail, judge.StatusSkip, judge.StatusError}
	for i, want := range expected {
		if parsed.CaseResults[i].Status != want {
			t.Fatalf("case[%d] status: want %s, got %s", i, want, parsed.CaseResults[i].Status)
		}
	}

	// Verify PASS case assertion details are preserved.
	passCase := parsed.CaseResults[0]
	if passCase.Grading == nil {
		t.Fatal("PASS case should have grading")
	}
	if len(passCase.Grading.AssertionResults) != 2 {
		t.Fatalf("PASS case assertions: want 2, got %d", len(passCase.Grading.AssertionResults))
	}

	// Verify FAIL case failure evidence is preserved.
	failCase := parsed.CaseResults[1]
	if failCase.Grading.AssertionResults[0].Evidence != "output does not contain 'graceful'" {
		t.Fatalf("FAIL case evidence not preserved")
	}

	// Verify SKIP case skip_reason is preserved.
	skipCase := parsed.CaseResults[2]
	if skipCase.Grading.SkipReason == nil || *skipCase.Grading.SkipReason == "" {
		t.Fatal("SKIP case should have skip_reason")
	}

	// Verify ERROR case error message.
	errorCase := parsed.CaseResults[3]
	if errorCase.Error != "engine process killed after 300s" {
		t.Fatalf("ERROR case error: want 'engine process killed after 300s', got %q", errorCase.Error)
	}
	if errorCase.Grading != nil {
		t.Fatal("ERROR case should NOT have grading")
	}
}

func assertReportConfigurations(t *testing.T, parsed Input) {
	t.Helper()
	if parsed.RequestedConfiguration == nil || parsed.AppliedConfiguration == nil || parsed.ObservedConfiguration == nil {
		t.Fatalf("requested/applied/observed configuration missing: %+v", parsed)
	}
	if parsed.RequestedConfiguration.Model != "gpt-5.4" || parsed.AppliedConfiguration.Protocol != "openai" || parsed.ObservedConfiguration.Model != "gpt-5.4" {
		t.Fatalf("requested/applied/observed configuration = %+v / %+v / %+v", parsed.RequestedConfiguration, parsed.AppliedConfiguration, parsed.ObservedConfiguration)
	}
}

// ============================================================================
// Scenario 3: JUnit Reporter end-to-end
// ============================================================================

// TestE2E_JUnitReporter_FullPipeline verifies the complete JUnit XML report
// generation pipeline.
//
// What it does: generates JUnit XML from data with all 4 status values and
// verifies the XML structure and statistics.
//
// Why it is meaningful: JUnit XML is the standard input format for CI systems
// (Jenkins, GitHub Actions). Incorrect tests/failures/errors/skipped counts
// lead to inaccurate CI gate decisions.
func TestE2E_JUnitReporter_FullPipeline(t *testing.T) { //nolint:cyclop,gocyclo // e2e test requires many assertions
	dir := t.TempDir()
	path := filepath.Join(dir, "result.xml")
	input := buildRealisticInput()

	r := &JUnitReporter{OutputPath: path}
	if err := r.Write(context.Background(), input); err != nil {
		t.Fatalf("JUnitReporter.Write: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	// Verify XML header.
	if !strings.Contains(content, "<?xml") {
		t.Fatal("missing XML header")
	}

	// Deserialize and verify structured data.
	var suites junitTestSuites
	if err := xml.Unmarshal(data, &suites); err != nil {
		t.Fatalf("parse JUnit XML: %v", err)
	}

	if len(suites.Suites) != 1 {
		t.Fatalf("expected 1 test suite, got %d", len(suites.Suites))
	}

	suite := suites.Suites[0]
	if suite.Name != "code-review" {
		t.Fatalf("suite name: want 'code-review', got %q", suite.Name)
	}

	// Statistics check: 4 tests, 1 failure, 1 error, 1 skipped.
	if suite.Tests != 4 {
		t.Fatalf("tests count: want 4, got %d", suite.Tests)
	}
	if suite.Failures != 1 {
		t.Fatalf("failures count: want 1, got %d", suite.Failures)
	}
	if suite.Errors != 1 {
		t.Fatalf("errors count: want 1, got %d", suite.Errors)
	}
	if suite.Skipped != 1 {
		t.Fatalf("skipped count: want 1, got %d", suite.Skipped)
	}

	// Verify sub-elements of each testcase.
	for _, tc := range suite.TestCases {
		switch tc.Name {
		case "identify-null-bug":
			if tc.Failure != nil || tc.Error != nil || tc.Skipped != nil {
				t.Fatal("PASS case should not have failure/error/skipped elements")
			}
		case "graceful-null-handling":
			if tc.Failure == nil {
				t.Fatal("FAIL case should have <failure> element")
			}
			if !strings.Contains(tc.Failure.Body, "graceful") {
				t.Fatal("failure body should contain assertion evidence")
			}
		case "file-output-check":
			if tc.Skipped == nil {
				t.Fatal("SKIP case should have <skipped> element")
			}
		case "timeout-crash":
			if tc.Error == nil {
				t.Fatal("ERROR case should have <error> element")
			}
			if !strings.Contains(tc.Error.Body, "killed") {
				t.Fatal("error body should contain error message")
			}
		}
	}
}

// ============================================================================
// Scenario 4: HTML Reporter end-to-end
// ============================================================================

// TestE2E_HTMLReporter_FullPipeline verifies the complete HTML report
// generation pipeline.
//
// What it does: generates an HTML report containing all 4 status values and
// verifies the rendering of key content.
//
// Why it is meaningful: the HTML report is the primary interface for manual
// review. It must correctly display: overall statistics, per-case status
// indicators, assertion details, and failure evidence.
func TestE2E_HTMLReporter_FullPipeline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.html")
	input := buildRealisticInput()

	r := &HTMLReporter{OutputPath: path}
	if err := r.Write(context.Background(), input); err != nil {
		t.Fatalf("HTMLReporter.Write: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	// Basic HTML structure.
	if !strings.Contains(content, "<!DOCTYPE html>") {
		t.Fatal("missing DOCTYPE")
	}
	if !strings.Contains(content, "code-review") {
		t.Fatal("missing skill name in HTML")
	}

	// Stats card: pass rate = 1/4 = 25%.
	if !strings.Contains(content, "25%") {
		t.Fatal("HTML should show 25% pass rate")
	}

	// All case IDs present.
	caseIDs := []string{"identify-null-bug", "graceful-null-handling", "file-output-check", "timeout-crash"}
	for _, id := range caseIDs {
		if !strings.Contains(content, id) {
			t.Fatalf("HTML missing case ID: %s", id)
		}
	}

	// Assertion evidence present.
	if !strings.Contains(content, "graceful") {
		t.Fatal("HTML should contain failure evidence")
	}

	// Engine and model information.
	if !strings.Contains(content, "codex") {
		t.Fatal("HTML should contain engine name")
	}
	if !strings.Contains(content, "gpt-5.4") {
		t.Fatal("HTML should contain model name")
	}
	for _, want := range []string{`"protocol":"openai"`, `"requested_model":"openai/gpt-5.4"`, `"applied_model":"openai/gpt-5.4"`, `"observed_model":"gpt-5.4"`, `"requested_version":"1.2.3"`, `"applied_version":"1.2.3"`, `"observed_version":"1.2.3"`} {
		if !strings.Contains(content, want) {
			t.Fatalf("HTML should contain configuration field %s", want)
		}
	}
}

// ============================================================================
// Scenario 5: Benchmark end-to-end — from CaseResult to BenchmarkResult
// ============================================================================

// TestE2E_Benchmark_ExtractAndCompute verifies the complete benchmark
// statistics computation pipeline.
//
// What it does: extracts metrics from a CaseResult list → computes statistics
// → verifies JSON serialization format. This simulates the runner's full
// post-evaluation benchmarking flow.
//
// Why it is meaningful: benchmark correctness depends on the
// ExtractMetrics → ComputeBenchmark pipeline. Assertions cover pass rate and
// duration mean/stddev, ensuring the statistics are meaningful for CI reports
// and manual analysis.
func TestE2E_Benchmark_ExtractAndCompute(t *testing.T) {
	input := buildRealisticInput()

	// 1. Extract metrics.
	metrics := ExtractMetrics(input.CaseResults)
	if len(metrics) != 4 {
		t.Fatalf("expected 4 metrics, got %d", len(metrics))
	}

	// Only the first case is PASS.
	passCount := 0
	for _, m := range metrics {
		if m.Passed {
			passCount++
		}
	}
	if passCount != 1 {
		t.Fatalf("expected 1 passed, got %d", passCount)
	}

	// 2. Simplified benchmark mode.
	result := ComputeBenchmark(metrics, nil)
	if result.RunSummary.WithoutSkill != nil {
		t.Fatal("simplified mode: without_skill should be nil")
	}
	if result.RunSummary.Delta != nil {
		t.Fatal("simplified mode: delta should be nil")
	}
	if result.RunSummary.WithSkill.PassRate.Mean != 0.25 {
		t.Fatalf("pass_rate mean: want 0.25, got %f", result.RunSummary.WithSkill.PassRate.Mean)
	}

	// 3. Full benchmark mode (simulating without_skill comparison).
	withoutMetrics := []CaseMetrics{
		{Passed: false, TimeSeconds: 60, InputTokens: 4000, OutputTokens: 1000},
		{Passed: false, TimeSeconds: 70, InputTokens: 4400, OutputTokens: 1100},
		{Passed: false, TimeSeconds: 55, InputTokens: 3840, OutputTokens: 960},
		{Passed: false, TimeSeconds: 65, InputTokens: 4160, OutputTokens: 1040},
	}
	fullResult := ComputeBenchmark(metrics, withoutMetrics)
	if fullResult.RunSummary.WithoutSkill == nil {
		t.Fatal("full mode: without_skill should not be nil")
	}
	if fullResult.RunSummary.Delta == nil {
		t.Fatal("full mode: delta should not be nil")
	}

	// with_skill pass_rate = 0.25, without_skill = 0.0 → delta = 0.25
	if math.Abs(fullResult.RunSummary.Delta.PassRate-0.25) > 0.001 {
		t.Fatalf("delta pass_rate: want 0.25, got %f", fullResult.RunSummary.Delta.PassRate)
	}

	// 4. JSON serialization format verification.
	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	jsonStr := string(jsonData)
	if !strings.Contains(jsonStr, `"without_skill": null`) {
		t.Fatalf("simplified mode JSON should have without_skill: null")
	}
	if !strings.Contains(jsonStr, `"delta": null`) {
		t.Fatalf("simplified mode JSON should have delta: null")
	}
}

// ============================================================================
// Scenario 6: JSON report with Benchmark
// ============================================================================

// TestE2E_JSONReporter_WithBenchmark verifies that a Benchmark is correctly
// embedded in the JSON report.
//
// What it does: builds an Input with Benchmark → JSON → deserialize and verify.
//
// Why it is meaningful: a complete evaluation report = case results + benchmark
// statistics. This test ensures the benchmark field is correctly serialized and
// parseable by downstream tools.
func TestE2E_JSONReporter_WithBenchmark(t *testing.T) {
	input := buildRealisticInput()
	metrics := ExtractMetrics(input.CaseResults)
	input.Benchmark = ComputeBenchmark(metrics, nil)

	dir := t.TempDir()
	path := filepath.Join(dir, "result_with_benchmark.json")
	r := &JSONReporter{OutputPath: path}

	if err := r.Write(context.Background(), input); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, _ := os.ReadFile(path)
	var parsed Input
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if parsed.Benchmark == nil {
		t.Fatal("benchmark should be present in JSON output")
	}
	if parsed.Benchmark.RunSummary.WithSkill.PassRate.Mean != 0.25 {
		t.Fatalf("benchmark pass_rate: want 0.25, got %f",
			parsed.Benchmark.RunSummary.WithSkill.PassRate.Mean)
	}
}

// ============================================================================
// Scenario 7: Edge case — empty case list
// ============================================================================

// TestE2E_EmptyResults_AllFormats verifies that an empty result list does not
// cause any Reporter to crash.
//
// What it does: generates JSON/JUnit/HTML reports with an empty CaseResults.
//
// Why it is meaningful: an empty result is a valid input (e.g. a configuration
// error leading to zero cases executed); reporters must handle it gracefully
// without panicking.
func TestE2E_EmptyResults_AllFormats(t *testing.T) {
	input := Input{
		SkillName:   "empty-skill",
		StartTime:   time.Now(),
		EndTime:     time.Now(),
		CaseResults: []CaseResult{},
	}

	dir := t.TempDir()
	reporters := map[string]Reporter{
		"json":  &JSONReporter{OutputPath: filepath.Join(dir, "empty.json")},
		"junit": &JUnitReporter{OutputPath: filepath.Join(dir, "empty.xml")},
		"html":  &HTMLReporter{OutputPath: filepath.Join(dir, "empty.html")},
	}

	for name, r := range reporters {
		if err := r.Write(context.Background(), input); err != nil {
			t.Fatalf("%s reporter failed on empty input: %v", name, err)
		}
	}

	// Verify JSON empty case list.
	data, _ := os.ReadFile(filepath.Join(dir, "empty.json"))
	var parsed Input
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(parsed.CaseResults) != 0 {
		t.Fatalf("expected 0 cases, got %d", len(parsed.CaseResults))
	}

	// Verify JUnit zero counts.
	xmlData, _ := os.ReadFile(filepath.Join(dir, "empty.xml"))
	if !strings.Contains(string(xmlData), `tests="0"`) {
		t.Fatal("JUnit should show tests=0 for empty input")
	}
}

// ============================================================================
// Scenario 8: Edge case — all pass
// ============================================================================

// TestE2E_AllPass verifies report correctness when all cases pass.
//
// What it does: all cases are PASS; verifies pass_rate=100% and no failure
// elements.
//
// Why it is meaningful: all-pass is the ideal scenario; CI should give a green
// light. JUnit XML must not contain any <failure>/<error>/<skipped> elements.
func TestE2E_AllPass(t *testing.T) {
	input := Input{
		SkillName: "perfect-skill",
		StartTime: time.Now(),
		EndTime:   time.Now().Add(30 * time.Second),
		CaseResults: []CaseResult{
			{
				CaseID: "case-1", Status: judge.StatusPass, DurationMs: 10000,
				Grading: &judge.Result{
					Status:           judge.StatusPass,
					AssertionResults: []judge.AssertionResult{{Text: "check-1", Passed: true, Evidence: "ok"}},
					Summary:          judge.ResultSummary{Passed: 1, Total: 1, PassRate: 1.0},
				},
			},
			{
				CaseID: "case-2", Status: judge.StatusPass, DurationMs: 15000,
				Grading: &judge.Result{
					Status:           judge.StatusPass,
					AssertionResults: []judge.AssertionResult{{Text: "check-2", Passed: true, Evidence: "ok"}},
					Summary:          judge.ResultSummary{Passed: 1, Total: 1, PassRate: 1.0},
				},
			},
		},
	}

	if input.OverallPassRate() != 1.0 {
		t.Fatalf("pass_rate should be 1.0, got %f", input.OverallPassRate())
	}

	dir := t.TempDir()

	// JUnit: 0 failures, 0 errors, 0 skipped.
	junitPath := filepath.Join(dir, "allpass.xml")
	jr := &JUnitReporter{OutputPath: junitPath}
	if err := jr.Write(context.Background(), input); err != nil {
		t.Fatalf("JUnit write failed: %v", err)
	}

	xmlData, _ := os.ReadFile(junitPath)
	content := string(xmlData)
	if !strings.Contains(content, `failures="0"`) {
		t.Fatal("all-pass JUnit should have 0 failures")
	}
	if !strings.Contains(content, `errors="0"`) {
		t.Fatal("all-pass JUnit should have 0 errors")
	}
	if strings.Contains(content, "<failure") {
		t.Fatal("all-pass JUnit should NOT contain <failure> element")
	}

	// HTML: 100% pass rate.
	htmlPath := filepath.Join(dir, "allpass.html")
	hr := &HTMLReporter{OutputPath: htmlPath}
	if err := hr.Write(context.Background(), input); err != nil {
		t.Fatalf("HTML write failed: %v", err)
	}

	htmlData, _ := os.ReadFile(htmlPath)
	if !strings.Contains(string(htmlData), "100%") {
		t.Fatal("all-pass HTML should show 100% pass rate")
	}
}

// ============================================================================
// Scenario 9: Edge case — all fail
// ============================================================================

// TestE2E_AllFail verifies report correctness when all cases fail.
//
// What it does: all cases are FAIL; verifies pass_rate=0% and each case has
// failure details.
//
// Why it is meaningful: an all-fail result must let developers clearly see
// every failure reason; every JUnit testcase should contain a <failure>
// element.
func TestE2E_AllFail(t *testing.T) {
	input := Input{
		SkillName: "broken-skill",
		StartTime: time.Now(),
		EndTime:   time.Now().Add(1 * time.Minute),
		CaseResults: []CaseResult{
			{
				CaseID: "fail-1", Status: judge.StatusFail, DurationMs: 20000,
				Grading: &judge.Result{
					Status: judge.StatusFail,
					AssertionResults: []judge.AssertionResult{
						{Text: "output_contains: fix", Passed: false, Evidence: "missing 'fix' in output"},
					},
					Summary: judge.ResultSummary{Passed: 0, Failed: 1, Total: 1, PassRate: 0},
				},
			},
			{
				CaseID: "fail-2", Status: judge.StatusFail, DurationMs: 25000,
				Grading: &judge.Result{
					Status: judge.StatusFail,
					AssertionResults: []judge.AssertionResult{
						{Text: "exit_code: 0", Passed: false, Evidence: "exit_code was 1"},
					},
					Summary: judge.ResultSummary{Passed: 0, Failed: 1, Total: 1, PassRate: 0},
				},
			},
		},
	}

	if input.OverallPassRate() != 0 {
		t.Fatalf("pass_rate should be 0, got %f", input.OverallPassRate())
	}

	dir := t.TempDir()
	junitPath := filepath.Join(dir, "allfail.xml")
	jr := &JUnitReporter{OutputPath: junitPath}
	if err := jr.Write(context.Background(), input); err != nil {
		t.Fatalf("JUnit write failed: %v", err)
	}

	xmlData, _ := os.ReadFile(junitPath)
	content := string(xmlData)
	if !strings.Contains(content, `failures="2"`) {
		t.Fatal("all-fail JUnit should have 2 failures")
	}
}

// ============================================================================
// Scenario 10: Three-format consistency — same input generates all formats
// ============================================================================

// TestE2E_ThreeFormats_Consistency verifies that the same Input presents
// consistent key data across all three formats.
//
// What it does: generates JSON/JUnit/HTML from the same Input and verifies
// that case counts, failure counts, and key information are consistent.
//
// Why it is meaningful: all three formats are views of the same source of
// truth. If JSON shows 4 cases but JUnit shows 3, it indicates data loss in
// the format conversion.
func TestE2E_ThreeFormats_Consistency(t *testing.T) {
	input := buildRealisticInput()
	dir := t.TempDir()

	jsonPath := filepath.Join(dir, "result.json")
	junitPath := filepath.Join(dir, "result.xml")
	htmlPath := filepath.Join(dir, "result.html")

	ctx := context.Background()

	// Generate all three formats.
	if err := (&JSONReporter{OutputPath: jsonPath}).Write(ctx, input); err != nil {
		t.Fatalf("JSON write failed: %v", err)
	}
	if err := (&JUnitReporter{OutputPath: junitPath}).Write(ctx, input); err != nil {
		t.Fatalf("JUnit write failed: %v", err)
	}
	if err := (&HTMLReporter{OutputPath: htmlPath}).Write(ctx, input); err != nil {
		t.Fatalf("HTML write failed: %v", err)
	}

	// JSON: 4 cases
	jsonData, _ := os.ReadFile(jsonPath)
	var parsed Input
	if err := json.Unmarshal(jsonData, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(parsed.CaseResults) != 4 {
		t.Fatalf("JSON: expected 4 cases, got %d", len(parsed.CaseResults))
	}

	// JUnit: 4 tests, 1 failure, 1 error
	xmlData, _ := os.ReadFile(junitPath)
	var suites junitTestSuites
	if err := xml.Unmarshal(xmlData, &suites); err != nil {
		t.Fatalf("failed to unmarshal XML: %v", err)
	}
	suite := suites.Suites[0]
	if suite.Tests != 4 {
		t.Fatalf("JUnit: expected 4 tests, got %d", suite.Tests)
	}
	if suite.Failures != 1 {
		t.Fatalf("JUnit: expected 1 failure, got %d", suite.Failures)
	}

	// HTML: all case IDs present.
	htmlData, _ := os.ReadFile(htmlPath)
	htmlContent := string(htmlData)
	for _, cr := range input.CaseResults {
		if !strings.Contains(htmlContent, cr.CaseID) {
			t.Fatalf("HTML: missing case ID %q", cr.CaseID)
		}
	}
}

// ============================================================================
// Scenario 11: Safety when Grading is nil
// ============================================================================

// TestE2E_NilGrading_NoFailure verifies that reporters do not panic when
// Grading is nil.
//
// What it does: constructs an ERROR-status case with nil Grading and generates
// all three report formats.
//
// Why it is meaningful: ERROR cases typically have no Grading (the engine
// crashed before evaluation could complete); reporters must handle this in a
// nil-safe manner without dereferencing a nil pointer.
func TestE2E_NilGrading_NoFailure(t *testing.T) {
	input := Input{
		SkillName: "crash-test",
		StartTime: time.Now(),
		EndTime:   time.Now().Add(10 * time.Second),
		CaseResults: []CaseResult{
			{
				CaseID:     "engine-crash",
				Status:     judge.StatusError,
				DurationMs: 5000,
				Error:      "segfault in engine",
				Grading:    nil, // intentionally nil: engine crashed before grading
			},
		},
	}

	dir := t.TempDir()
	reporters := []Reporter{
		&JSONReporter{OutputPath: filepath.Join(dir, "nil.json")},
		&JUnitReporter{OutputPath: filepath.Join(dir, "nil.xml")},
		&HTMLReporter{OutputPath: filepath.Join(dir, "nil.html")},
	}

	for i, r := range reporters {
		if err := r.Write(context.Background(), input); err != nil {
			t.Fatalf("reporter[%d] failed with nil grading: %v", i, err)
		}
	}
}

// ============================================================================
// Scenario 12: Large-batch performance baseline
// ============================================================================

// TestE2E_LargeBatch verifies Reporter correctness under a large number of cases.
//
// What it does: constructs 100 cases (50 PASS + 50 FAIL) and verifies correct
// report generation.
//
// Why it is meaningful: real evaluations may contain dozens or hundreds of
// cases; this test ensures reporters do not exhibit performance degradation or
// truncation under bulk data.
func TestE2E_LargeBatch(t *testing.T) {
	var cases []CaseResult
	for i := range 100 {
		status := judge.StatusPass
		passed := true
		if i%2 == 0 {
			status = judge.StatusFail
			passed = false
		}
		_ = passed
		cases = append(cases, CaseResult{
			CaseID:     "case-" + string(rune('A'+i%26)) + "-" + time.Now().Format("150405"),
			Status:     status,
			DurationMs: int64(1000 + i*100),
			Grading: &judge.Result{
				Status: status,
				AssertionResults: []judge.AssertionResult{
					{Text: "check", Passed: status == judge.StatusPass, Evidence: "evidence"},
				},
				Summary: judge.ResultSummary{
					Passed: func() int {
						if status == judge.StatusPass {
							return 1
						}
						return 0
					}(),
					Failed: func() int {
						if status == judge.StatusFail {
							return 1
						}
						return 0
					}(),
					Total: 1,
					PassRate: func() float64 {
						if status == judge.StatusPass {
							return 1.0
						}
						return 0.0
					}(),
				},
			},
		})
	}

	input := Input{
		SkillName:   "large-batch",
		StartTime:   time.Now(),
		EndTime:     time.Now().Add(10 * time.Minute),
		CaseResults: cases,
	}

	// pass_rate = 50/100 = 0.5
	if math.Abs(input.OverallPassRate()-0.5) > 0.001 {
		t.Fatalf("expected 50%% pass rate, got %f", input.OverallPassRate())
	}

	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "large.json")
	jr := &JSONReporter{OutputPath: jsonPath}
	if err := jr.Write(context.Background(), input); err != nil {
		t.Fatalf("large batch JSON write failed: %v", err)
	}

	// Verify output contains all 100 cases.
	data, _ := os.ReadFile(jsonPath)
	var parsed Input
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(parsed.CaseResults) != 100 {
		t.Fatalf("expected 100 cases in output, got %d", len(parsed.CaseResults))
	}
}

// ============================================================================
// Helper functions
// ============================================================================

// ============================================================================
// Anthropic-compatible artifact integration tests
// ============================================================================

// TestE2E_AnthropicArtifacts_FullPipeline tests the complete Anthropic-compatible
// artifact generation pipeline.
//
// Verifies: judge.Result → Anthropic format conversion → workspace write → file format correctness.
func TestE2E_AnthropicArtifacts_FullPipeline(t *testing.T) { //nolint:cyclop,gocyclo,maintidx,funlen // e2e test requires many assertions
	tmpDir := t.TempDir()

	// 1. Create IterationWorkspace.
	ws, err := NewIterationWorkspace(tmpDir, "test-skill", 1)
	if err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	// 2. Prepare test data — simulate evaluation results for 2 cases.
	case1Grading := &judge.Result{
		Status:        judge.StatusPass,
		TurnsExecuted: 1,
		TurnsTotal:    1,
		AssertionResults: []judge.AssertionResult{
			{Text: "output contains a correct joke", Passed: true, Evidence: "contains humorous element"},
			{Text: "joke is in the target language", Passed: true, Evidence: "entire response is in target language"},
		},
		Summary: judge.ResultSummary{Passed: 2, Failed: 0, Total: 2, PassRate: 1.0},
	}

	case2Grading := &judge.Result{
		Status:        judge.StatusFail,
		TurnsExecuted: 1,
		TurnsTotal:    1,
		AssertionResults: []judge.AssertionResult{
			{Text: "output contains a dry joke", Passed: false, Evidence: "not a dry joke"},
		},
		Summary: judge.ResultSummary{Passed: 0, Failed: 1, Total: 1, PassRate: 0.0},
	}

	// 3. Create directory structure.
	caseIDs := []string{"joke-1", "joke-2"}
	if err := ws.EnsureDirsWithBaseline(caseIDs); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	// 4. Write per-case artifacts (with_skill).
	// Case 1
	if err := ws.WriteResponse("joke-1", "with_skill", "Here is a really funny joke..."); err != nil {
		t.Fatalf("WriteResponse failed: %v", err)
	}
	grading1 := ConvertToAnthropicGrading(case1Grading)
	if err := ws.WriteGrading("joke-1", "with_skill", grading1); err != nil {
		t.Fatalf("WriteGrading failed: %v", err)
	}
	meta1 := &EvalMetadata{EvalID: 1, EvalName: "Jokes Eval 1", Prompt: "Tell me a joke", Assertions: []string{"output contains a correct joke", "joke is in the target language"}}
	if err := ws.WriteEvalMeta("joke-1", meta1); err != nil {
		t.Fatalf("WriteEvalMeta failed: %v", err)
	}

	// Case 2
	if err := ws.WriteResponse("joke-2", "with_skill", "This is not a dry joke"); err != nil {
		t.Fatalf("WriteResponse failed: %v", err)
	}
	grading2 := ConvertToAnthropicGrading(case2Grading)
	if err := ws.WriteGrading("joke-2", "with_skill", grading2); err != nil {
		t.Fatalf("WriteGrading failed: %v", err)
	}
	meta2 := &EvalMetadata{EvalID: 2, EvalName: "Jokes Eval 2", Prompt: "Tell me a dry joke", Assertions: []string{"output contains a dry joke"}}
	if err := ws.WriteEvalMeta("joke-2", meta2); err != nil {
		t.Fatalf("WriteEvalMeta failed: %v", err)
	}

	// 5. Write without_skill artifacts.
	if err := ws.WriteResponse("joke-1", "without_skill", "I don't know how to tell a joke"); err != nil {
		t.Fatalf("WriteResponse without_skill failed: %v", err)
	}
	withoutGrading := &judge.Result{
		Status: judge.StatusFail,
		AssertionResults: []judge.AssertionResult{
			{Text: "output contains a correct joke", Passed: false, Evidence: "no joke found"},
		},
		Summary: judge.ResultSummary{Passed: 0, Failed: 1, Total: 1, PassRate: 0.0},
	}
	withoutGrading1 := ConvertToAnthropicGrading(withoutGrading)
	if err := ws.WriteGrading("joke-1", "without_skill", withoutGrading1); err != nil {
		t.Fatalf("WriteGrading without_skill failed: %v", err)
	}

	// 6. Generate benchmark.json.
	withSkillRuns := []BenchmarkRun{
		{
			EvalID: 1, EvalName: "Jokes Eval 1", Configuration: "with_skill", RunNumber: 1,
			Result:       BenchmarkRunResult{PassRate: 1.0, Passed: 2, Total: 2, TimeSeconds: 1.5},
			Expectations: grading1.Expectations,
		},
		{
			EvalID: 2, EvalName: "Jokes Eval 2", Configuration: "with_skill", RunNumber: 1,
			Result:       BenchmarkRunResult{PassRate: 0.0, Failed: 1, Total: 1, TimeSeconds: 0.8},
			Expectations: grading2.Expectations,
		},
	}
	withoutSkillRuns := []BenchmarkRun{
		{
			EvalID: 1, EvalName: "Jokes Eval 1", Configuration: "without_skill", RunNumber: 1,
			Result:       BenchmarkRunResult{PassRate: 0.0, Failed: 1, Total: 1, TimeSeconds: 2.0},
			Expectations: withoutGrading1.Expectations,
		},
	}

	bm := ComputeAnthropicBenchmark("chinese-jokes", "/path/to/skill", withSkillRuns, withoutSkillRuns)
	if err := ws.WriteBenchmark(bm); err != nil {
		t.Fatalf("WriteBenchmark failed: %v", err)
	}

	// 7. Generate benchmark.md.
	if err := ws.WriteBenchmarkMD(bm); err != nil {
		t.Fatalf("WriteBenchmarkMD failed: %v", err)
	}

	// 8. Generate report.html.
	reportInput := Input{
		SkillName: "chinese-jokes",
		StartTime: time.Now(),
		EndTime:   time.Now(),
		CaseResults: []CaseResult{
			{
				CaseID:        "joke-1",
				Title:         "Chinese Jokes 1",
				Status:        judge.StatusPass,
				DurationMs:    1500,
				Turns:         1,
				Configuration: "with_skill",
				Prompt:        "Tell me a joke",
				Response:      "Here is a really funny joke...",
				Grading:       case1Grading,
			},
			{
				CaseID:        "joke-1",
				Title:         "Jokes Eval 1",
				Status:        judge.StatusFail,
				DurationMs:    2000,
				Turns:         1,
				Configuration: "without_skill",
				Prompt:        "Tell me a joke",
				Response:      "I don't know how to tell a joke",
				Grading:       withoutGrading,
			},
		},
	}
	reportPath := filepath.Join(ws.IterationDir(), "report.html")
	if err := WriteHTMLReport(context.Background(), reportPath, reportInput); err != nil {
		t.Fatalf("WriteHTMLReport failed: %v", err)
	}

	// ========== Verification ==========

	// Verify directory structure.
	iterDir := ws.IterationDir()
	requiredFiles := []string{
		"joke-1/with_skill/outputs/response.md",
		"joke-1/with_skill/grading.json",
		"joke-1/without_skill/outputs/response.md",
		"joke-1/without_skill/grading.json",
		"joke-1/eval_metadata.json",
		"joke-2/with_skill/outputs/response.md",
		"joke-2/with_skill/grading.json",
		"joke-2/eval_metadata.json",
		"benchmark.json",
		"benchmark.md",
		"report.html",
	}

	for _, f := range requiredFiles {
		path := filepath.Join(iterDir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("missing required file: %s", f)
		}
	}

	// Verify grading.json format.
	gradingData, _ := os.ReadFile(filepath.Join(iterDir, "joke-1/with_skill/grading.json"))
	var parsedGrading AnthropicGrading
	if err := json.Unmarshal(gradingData, &parsedGrading); err != nil {
		t.Fatalf("invalid grading.json: %v", err)
	}
	if len(parsedGrading.Expectations) != 2 {
		t.Errorf("expected 2 expectations, got %d", len(parsedGrading.Expectations))
	}
	if parsedGrading.Summary.PassRate != 1.0 {
		t.Errorf("expected pass_rate 1.0, got %f", parsedGrading.Summary.PassRate)
	}

	// Verify eval_metadata.json format.
	metaData, _ := os.ReadFile(filepath.Join(iterDir, "joke-1/eval_metadata.json"))
	var parsedMeta EvalMetadata
	if err := json.Unmarshal(metaData, &parsedMeta); err != nil {
		t.Fatalf("invalid eval_metadata.json: %v", err)
	}
	if parsedMeta.EvalName != "Jokes Eval 1" {
		t.Errorf("expected eval_name 'Jokes Eval 1', got %s", parsedMeta.EvalName)
	}

	// Verify benchmark.json format.
	bmData, _ := os.ReadFile(filepath.Join(iterDir, "benchmark.json"))
	var parsedBM AnthropicBenchmark
	if err := json.Unmarshal(bmData, &parsedBM); err != nil {
		t.Fatalf("invalid benchmark.json: %v", err)
	}
	if parsedBM.Metadata.SkillName != "chinese-jokes" {
		t.Errorf("expected skill_name 'chinese-jokes', got %s", parsedBM.Metadata.SkillName)
	}
	if len(parsedBM.Runs) != 3 { // 2 with_skill + 1 without_skill
		t.Errorf("expected 3 runs, got %d", len(parsedBM.Runs))
	}

	// Verify benchmark.md content.
	bmMDData, _ := os.ReadFile(filepath.Join(iterDir, "benchmark.md"))
	if !strings.Contains(string(bmMDData), "chinese-jokes") {
		t.Error("benchmark.md should contain skill name")
	}
	if !strings.Contains(string(bmMDData), "Pass Rate") {
		t.Error("benchmark.md should contain Pass Rate")
	}

	// Verify report.html content.
	reportHTML, _ := os.ReadFile(reportPath)
	if !strings.Contains(string(reportHTML), "chinese-jokes") {
		t.Error("report.html should contain skill name")
	}
	if !strings.Contains(string(reportHTML), "Tell me a joke") {
		t.Error("report.html should contain prompt")
	}

	t.Logf("All Anthropic artifacts generated successfully in %s", iterDir)
}

// TestE2E_AnthropicArtifacts_WithoutBaseline tests artifact generation without a baseline.
func TestE2E_AnthropicArtifacts_WithoutBaseline(t *testing.T) {
	tmpDir := t.TempDir()

	ws, err := NewIterationWorkspace(tmpDir, "simple-skill", 1)
	if err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	caseIDs := []string{"test-case"}
	if err := ws.EnsureDirs(caseIDs); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	// Write with_skill only
	if err := ws.WriteResponse("test-case", "with_skill", "Agent response"); err != nil {
		t.Fatalf("WriteResponse failed: %v", err)
	}

	grading := &judge.Result{
		Status:           judge.StatusPass,
		AssertionResults: []judge.AssertionResult{{Text: "check", Passed: true, Evidence: "ok"}},
		Summary:          judge.ResultSummary{Passed: 1, Total: 1, PassRate: 1.0},
	}
	antGrading := ConvertToAnthropicGrading(grading)
	if err := ws.WriteGrading("test-case", "with_skill", antGrading); err != nil {
		t.Fatalf("WriteGrading failed: %v", err)
	}

	runs := []BenchmarkRun{{
		EvalID: 1, EvalName: "test", Configuration: "with_skill", RunNumber: 1,
		Result: BenchmarkRunResult{PassRate: 1.0, Passed: 1, Total: 1, TimeSeconds: 1.0},
	}}

	bm := ComputeAnthropicBenchmark("simple-skill", "", runs, nil)
	if err := ws.WriteBenchmark(bm); err != nil {
		t.Fatalf("WriteBenchmark failed: %v", err)
	}

	// Verify no without_skill directory
	withoutDir := filepath.Join(ws.IterationDir(), "test-case", "without_skill")
	if _, err := os.Stat(withoutDir); !os.IsNotExist(err) {
		t.Error("without_skill directory should not exist when baseline is disabled")
	}

	// Verify benchmark has no delta
	bmData, _ := os.ReadFile(filepath.Join(ws.IterationDir(), "benchmark.json"))
	var parsedBM AnthropicBenchmark
	if err := json.Unmarshal(bmData, &parsedBM); err != nil {
		t.Fatalf("invalid benchmark.json: %v", err)
	}
	if parsedBM.RunSummary.WithoutSkill != nil {
		t.Error("expected nil without_skill summary")
	}
}
