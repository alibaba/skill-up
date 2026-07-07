package report

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/judge"
)

// sampleInput returns a Input fixture used across all reporter tests.
func sampleInput() Input {
	start := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)
	end := start.Add(4*time.Minute + 5*time.Second)

	return Input{
		SkillName:     "code-review",
		SchemaVersion: "v1alpha1",
		EngineName:    "codex",
		ModelName:     "openai/gpt-5.4",
		StartTime:     start,
		EndTime:       end,
		CaseResults: []CaseResult{
			{
				CaseID:     "basic-success",
				Title:      "Agent should identify a missing null check",
				Status:     judge.StatusPass,
				DurationMs: 45200,
				Turns:      5,
				JudgeSkills: []judge.SkillInfo{
					{Source: "local_path", Path: "evals/fixtures/judge-skill", Target: "~/.claude/skills/judge-skill", Name: "judge-skill"},
				},
				Grading: &judge.Result{
					Status:        judge.StatusPass,
					TurnsExecuted: 5,
					TurnsTotal:    5,
					AssertionResults: []judge.AssertionResult{
						{Text: "output_contains", Passed: true, Evidence: "output contains 'null' and 'bug'"},
						{Text: "exit_code: 0", Passed: true, Evidence: "exit_code is 0 as expected"},
					},
					Summary: judge.ResultSummary{Passed: 2, Failed: 0, Total: 2, PassRate: 1.0},
				},
			},
			{
				CaseID:     "edge-case-null",
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
					},
					Summary: judge.ResultSummary{Passed: 0, Failed: 1, Total: 1, PassRate: 0.0},
				},
			},
			{
				CaseID:     "skipped-case",
				Title:      "Skipped due to post_condition",
				Status:     judge.StatusSkip,
				DurationMs: 1000,
				Turns:      1,
				Grading:    judge.NewSkipResult("post_condition not met", 1, 3),
			},
			{
				CaseID:     "error-case",
				Title:      "Engine timeout",
				Status:     judge.StatusError,
				DurationMs: 300000,
				Error:      "engine process killed after 300s",
			},
		},
	}
}

// ---------------------------------------------------------------------------
// JSONReporter
// ---------------------------------------------------------------------------

func TestJSONReporter_Write(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	r := &JSONReporter{OutputPath: path}

	err := r.Write(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("JSONReporter.Write failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read json file: %v", err)
	}

	// Verify it's valid JSON.
	var parsed Input
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	// Verify key fields.
	if parsed.SkillName != "code-review" {
		t.Fatalf("expected skill_name 'code-review', got %q", parsed.SkillName)
	}
	if len(parsed.CaseResults) != 4 {
		t.Fatalf("expected 4 case results, got %d", len(parsed.CaseResults))
	}
	if parsed.CaseResults[0].Status != judge.StatusPass {
		t.Fatalf("expected first case PASS, got %s", parsed.CaseResults[0].Status)
	}
	if parsed.CaseResults[1].Status != judge.StatusFail {
		t.Fatalf("expected second case FAIL, got %s", parsed.CaseResults[1].Status)
	}
	if len(parsed.CaseResults[0].JudgeSkills) != 1 || parsed.CaseResults[0].JudgeSkills[0].Path != "evals/fixtures/judge-skill" {
		t.Fatalf("judge_skills not preserved in JSON: %#v", parsed.CaseResults[0].JudgeSkills)
	}
}

func TestJSONReporter_ContainsAssertions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	r := &JSONReporter{OutputPath: path}
	_ = r.Write(context.Background(), sampleInput())

	data, _ := os.ReadFile(path)
	content := string(data)

	// Verify assertion details are present.
	if !strings.Contains(content, "output_contains") {
		t.Fatal("json should contain assertion text")
	}
	if !strings.Contains(content, "graceful") {
		t.Fatal("json should contain failure evidence")
	}
}

// ---------------------------------------------------------------------------
// JUnitReporter
// ---------------------------------------------------------------------------

func TestJUnitReporter_Write(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.xml")
	r := &JUnitReporter{OutputPath: path}

	err := r.Write(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("JUnitReporter.Write failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read junit file: %v", err)
	}
	content := string(data)

	// Verify XML structure.
	if !strings.Contains(content, "<?xml") {
		t.Fatal("missing xml header")
	}
	if !strings.Contains(content, "<testsuites>") {
		t.Fatal("missing testsuites element")
	}
	if !strings.Contains(content, `name="code-review"`) {
		t.Fatal("missing suite name")
	}
	if !strings.Contains(content, `tests="4"`) {
		t.Fatal("expected 4 tests")
	}
	if !strings.Contains(content, `failures="1"`) {
		t.Fatal("expected 1 failure")
	}
	if !strings.Contains(content, `errors="1"`) {
		t.Fatal("expected 1 error")
	}
	if !strings.Contains(content, `name="judge.skills.count" value="1"`) {
		t.Fatal("junit should include judge skill count property")
	}
	if !strings.Contains(content, `name="judge.skills.0.path" value="evals/fixtures/judge-skill"`) {
		t.Fatal("junit should include judge skill path property")
	}
}

func TestJUnitReporter_FailureDetails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.xml")
	r := &JUnitReporter{OutputPath: path}
	_ = r.Write(context.Background(), sampleInput())

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "graceful") {
		t.Fatal("junit should contain failure evidence")
	}
	if !strings.Contains(content, "<failure") {
		t.Fatal("junit should contain failure element")
	}
	if !strings.Contains(content, "<error") {
		t.Fatal("junit should contain error element")
	}
	if !strings.Contains(content, "<skipped") {
		t.Fatal("junit should contain skipped element")
	}
}

// ---------------------------------------------------------------------------
// HTMLReporter
// ---------------------------------------------------------------------------

func TestHTMLReporter_Write(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.html")
	r := &HTMLReporter{OutputPath: path}

	err := r.Write(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("HTMLReporter.Write failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read html file: %v", err)
	}
	content := string(data)

	// Verify HTML structure.
	if !strings.Contains(content, "<!DOCTYPE html>") {
		t.Fatal("missing doctype")
	}
	if !strings.Contains(content, "code-review") {
		t.Fatal("missing skill name")
	}
	if !strings.Contains(content, "basic-success") {
		t.Fatal("missing case ID")
	}
	if !strings.Contains(content, "edge-case-null") {
		t.Fatal("missing failed case ID")
	}
	if !strings.Contains(content, "evals/fixtures/judge-skill") {
		t.Fatal("missing judge skill path in embedded report data")
	}
}

func TestHTMLReporter_ContainsAssertionDetails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.html")
	r := &HTMLReporter{OutputPath: path}
	_ = r.Write(context.Background(), sampleInput())

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "graceful") {
		t.Fatal("html should contain failure evidence")
	}
	if !strings.Contains(content, "25%") {
		t.Fatal("html should contain pass rate (1 of 4 = 25%)")
	}
}

func TestHTMLReporter_SynthesizedTurnFailurePassRateScript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.html")
	r := &HTMLReporter{OutputPath: path}

	input := Input{
		SkillName: "multi-turn",
		CaseResults: []CaseResult{{
			CaseID: "turn-failure",
			Status: judge.StatusFail,
			Turns:  2,
			TurnResults: []CaseTurnResult{
				{TurnNumber: 1, Status: "completed", Response: "ok"},
				{TurnNumber: 2, Status: "failed", Reason: "missing token"},
			},
		}},
	}
	if err := r.Write(context.Background(), input); err != nil {
		t.Fatalf("HTMLReporter.Write failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read html file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Math.round((passed.length / total) * 100)") {
		t.Fatal("HTML should compute synthesized turn-failure pass rate from passed/total")
	}
	if !strings.Contains(content, "total === 0 ? 0") {
		t.Fatal("HTML should guard synthesized turn-failure pass rate when total is zero")
	}
}

// ---------------------------------------------------------------------------
// Input helpers
// ---------------------------------------------------------------------------

func TestInput_OverallPassRate(t *testing.T) {
	in := sampleInput()
	rate := in.OverallPassRate()
	// 1 pass out of 4 total = 0.25
	if rate != 0.25 {
		t.Fatalf("expected 0.25, got %f", rate)
	}
}

func TestInput_OverallPassRate_Empty(t *testing.T) {
	in := Input{}
	if in.OverallPassRate() != 0 {
		t.Fatal("empty input should return 0")
	}
}

func TestInput_TotalDuration(t *testing.T) {
	in := sampleInput()
	d := in.TotalDuration()
	if d != 4*time.Minute+5*time.Second {
		t.Fatalf("expected 4m5s, got %v", d)
	}
}
