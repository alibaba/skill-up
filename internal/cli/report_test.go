package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
)

// makeReportInput returns a minimal report.Input suitable for testing.
func makeReportInput(status judge.Status) report.Input {
	return report.Input{
		SkillName:     "test-skill",
		SchemaVersion: "v1alpha1",
		EngineName:    "test-engine",
		ModelName:     "test-model",
		StartTime:     time.Now().Add(-time.Second),
		EndTime:       time.Now(),
		CaseResults: []report.CaseResult{
			{CaseID: "case-1", Title: "Case One", Status: status, DurationMs: 500},
			{CaseID: "case-2", Title: "Case Two", Status: judge.StatusFail, DurationMs: 300},
		},
	}
}

func writeResultJSON(t *testing.T, dir string, input report.Input) string {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal result.json: %v", err)
	}
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write result.json: %v", err)
	}
	return path
}

// newReportCmd creates an isolated cobra command wired to runReport for testing.
func newReportCmd() *cobra.Command {
	cmd := &cobra.Command{RunE: runReport}
	cmd.Flags().StringArray("format", nil, "")
	cmd.Flags().String("output-dir", "", "")
	return cmd
}

// ---------------------------------------------------------------------------
// buildReporter
// ---------------------------------------------------------------------------

func TestBuildReporter_JSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r, path, err := buildReporter("json", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := r.(*report.JSONReporter); !ok {
		t.Errorf("expected *report.JSONReporter, got %T", r)
	}
	if want := filepath.Join(dir, "report.json"); path != want {
		t.Errorf("path: want %s, got %s", want, path)
	}
}

func TestBuildReporter_JUnit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r, path, err := buildReporter("junit", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := r.(*report.JUnitReporter); !ok {
		t.Errorf("expected *report.JUnitReporter, got %T", r)
	}
	if want := filepath.Join(dir, "report.xml"); path != want {
		t.Errorf("path: want %s, got %s", want, path)
	}
}

func TestBuildReporter_HTML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r, path, err := buildReporter("html", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := r.(*report.HTMLReporter); !ok {
		t.Errorf("expected *report.HTMLReporter, got %T", r)
	}
	if want := filepath.Join(dir, "report.html"); path != want {
		t.Errorf("path: want %s, got %s", want, path)
	}
}

func TestBuildReporter_Markdown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r, path, err := buildReporter("markdown", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := r.(*report.MarkdownReporter); !ok {
		t.Errorf("expected *report.MarkdownReporter, got %T", r)
	}
	if want := filepath.Join(dir, "report.md"); path != want {
		t.Errorf("path: want %s, got %s", want, path)
	}
}

func TestBuildReporter_Unsupported(t *testing.T) {
	t.Parallel()
	_, _, err := buildReporter("csv", t.TempDir())
	if err == nil {
		t.Fatal("expected error for unsupported format, got nil")
	}
	if !strings.Contains(err.Error(), "csv") {
		t.Errorf("error should mention the unsupported format, got: %v", err)
	}
	if !strings.Contains(err.Error(), "markdown") {
		t.Errorf("error should mention markdown as a supported format, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// countByStatus
// ---------------------------------------------------------------------------

func TestCountByStatus(t *testing.T) {
	t.Parallel()

	input := &report.Input{
		CaseResults: []report.CaseResult{
			{Status: judge.StatusPass},
			{Status: judge.StatusPass},
			{Status: judge.StatusFail},
			{Status: judge.StatusError},
		},
	}

	tests := []struct {
		status judge.Status
		want   int
	}{
		{judge.StatusPass, 2},
		{judge.StatusFail, 1},
		{judge.StatusError, 1},
		{judge.StatusSkip, 0},
	}

	for _, tc := range tests {
		got := countByStatus(input, tc.status)
		if got != tc.want {
			t.Errorf("countByStatus(%s): want %d, got %d", tc.status, tc.want, got)
		}
	}
}

func TestCountByStatus_Empty(t *testing.T) {
	t.Parallel()
	input := &report.Input{}
	if got := countByStatus(input, judge.StatusPass); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// runReport
// ---------------------------------------------------------------------------

func TestRunReport_DefaultFormat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resultPath := writeResultJSON(t, dir, makeReportInput(judge.StatusPass))

	cmd := newReportCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runReport(cmd, []string{resultPath}); err != nil {
		t.Fatalf("runReport error: %v", err)
	}

	// Default format is json → report.json should exist.
	if _, err := os.Stat(filepath.Join(dir, "report.json")); err != nil {
		t.Errorf("report.json not created: %v", err)
	}
	if !strings.Contains(out.String(), "report.json") {
		t.Errorf("stdout should mention report.json, got: %s", out.String())
	}
}

func TestRunReport_MultipleFormats(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resultPath := writeResultJSON(t, dir, makeReportInput(judge.StatusPass))

	cmd := newReportCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Flags().Set("format", "json"); err != nil {
		t.Fatalf("set flag: %v", err)
	}
	if err := cmd.Flags().Set("format", "junit"); err != nil {
		t.Fatalf("set flag: %v", err)
	}

	if err := runReport(cmd, []string{resultPath}); err != nil {
		t.Fatalf("runReport error: %v", err)
	}

	for _, f := range []string{"report.json", "report.xml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s not created: %v", f, err)
		}
	}
}

func TestRunReport_MarkdownFormat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resultPath := writeResultJSON(t, dir, makeReportInput(judge.StatusPass))

	cmd := newReportCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Flags().Set("format", "markdown"); err != nil {
		t.Fatalf("set flag: %v", err)
	}

	if err := runReport(cmd, []string{resultPath}); err != nil {
		t.Fatalf("runReport error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "report.md")); err != nil {
		t.Errorf("report.md not created: %v", err)
	}
	if !strings.Contains(out.String(), "report.md") {
		t.Errorf("stdout should mention report.md, got: %s", out.String())
	}
}

func TestRunReport_CustomOutputDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outDir := t.TempDir()
	resultPath := writeResultJSON(t, dir, makeReportInput(judge.StatusPass))

	cmd := newReportCmd()
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Flags().Set("output-dir", outDir); err != nil {
		t.Fatalf("set flag: %v", err)
	}

	if err := runReport(cmd, []string{resultPath}); err != nil {
		t.Fatalf("runReport error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "report.json")); err != nil {
		t.Errorf("report.json not created in output-dir: %v", err)
	}
}

func TestRunReport_OutputDirAutoCreated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resultPath := writeResultJSON(t, dir, makeReportInput(judge.StatusPass))

	// Use a non-existent nested directory as output-dir.
	outDir := filepath.Join(t.TempDir(), "nested", "reports")

	cmd := newReportCmd()
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Flags().Set("output-dir", outDir); err != nil {
		t.Fatalf("set flag: %v", err)
	}

	if err := runReport(cmd, []string{resultPath}); err != nil {
		t.Fatalf("runReport error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "report.json")); err != nil {
		t.Errorf("report.json not created in auto-created output-dir: %v", err)
	}
}

func TestRunReport_MissingFile(t *testing.T) {
	t.Parallel()
	cmd := newReportCmd()
	cmd.SetOut(&bytes.Buffer{})

	err := runReport(cmd, []string{"/nonexistent/result.json"})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestRunReport_InvalidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	badPath := filepath.Join(dir, "result.json")
	if err := os.WriteFile(badPath, []byte("not-json{"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newReportCmd()
	cmd.SetOut(&bytes.Buffer{})

	err := runReport(cmd, []string{badPath})
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestRunReport_UnsupportedFormat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resultPath := writeResultJSON(t, dir, makeReportInput(judge.StatusPass))

	cmd := newReportCmd()
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Flags().Set("format", "pdf"); err != nil {
		t.Fatalf("set flag: %v", err)
	}

	err := runReport(cmd, []string{resultPath})
	if err == nil {
		t.Fatal("expected error for unsupported format, got nil")
	}
}

func TestRunReport_SummaryOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	input := makeReportInput(judge.StatusPass) // case-1=PASS, case-2=FAIL
	resultPath := writeResultJSON(t, dir, input)

	cmd := newReportCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runReport(cmd, []string{resultPath}); err != nil {
		t.Fatalf("runReport error: %v", err)
	}

	stdout := out.String()
	if !strings.Contains(stdout, "Pass rate") {
		t.Errorf("stdout should contain 'Pass rate', got: %s", stdout)
	}
}
