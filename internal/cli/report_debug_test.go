package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
)

// newDebugReportCmd creates an isolated cobra command wired to runReportDebug for testing.
func newDebugReportCmd() *cobra.Command {
	cmd := &cobra.Command{RunE: runReportDebug}
	cmd.Flags().String("format", "json", "")
	return cmd
}

// writeReportInputJSON serialises a report.Input to a temp file and returns the path.
func writeReportInputJSON(t *testing.T, dir string, input report.Input) string {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal report input: %v", err)
	}
	path := filepath.Join(dir, "input.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write input.json: %v", err)
	}
	return path
}

// ---------------------------------------------------------------------------
// runReportDebug
// ---------------------------------------------------------------------------

// runReportDebugFormatCase exercises the "set format flag → run → assert
// output file exists" flow shared by the default/junit/html cases. Extracted
// to silence dupl on near-identical bodies.
//
// runReportDebug writes to the current directory, and t.Chdir is incompatible
// with t.Parallel, so callers must not mark themselves parallel.
func runReportDebugFormatCase(t *testing.T, format string, status judge.Status, expectFile string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)

	inputPath := writeReportInputJSON(t, dir, makeReportInput(status))

	cmd := newDebugReportCmd()
	cmd.SetErr(&bytes.Buffer{})
	if format != "" {
		if err := cmd.Flags().Set("format", format); err != nil {
			t.Fatalf("set flag: %v", err)
		}
	}

	if err := runReportDebug(cmd, []string{inputPath}); err != nil {
		t.Fatalf("runReportDebug error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, expectFile)); err != nil {
		t.Errorf("%s not created: %v", expectFile, err)
	}
}

func TestRunReportDebug_DefaultFormat(t *testing.T) {
	runReportDebugFormatCase(t, "", judge.StatusPass, "report.json")
}

func TestRunReportDebug_JUnitFormat(t *testing.T) {
	runReportDebugFormatCase(t, "junit", judge.StatusFail, "report.xml")
}

func TestRunReportDebug_HTMLFormat(t *testing.T) {
	runReportDebugFormatCase(t, "html", judge.StatusPass, "report.html")
}

func TestRunReportDebug_MarkdownFormat(t *testing.T) {
	runReportDebugFormatCase(t, "markdown", judge.StatusFail, "report.md")
}

func TestRunReportDebug_MissingFile(t *testing.T) {
	t.Parallel()
	cmd := newDebugReportCmd()
	cmd.SetErr(&bytes.Buffer{})

	err := runReportDebug(cmd, []string{"/nonexistent/input.json"})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestRunReportDebug_InvalidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	badPath := filepath.Join(dir, "input.json")
	if err := os.WriteFile(badPath, []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newDebugReportCmd()
	cmd.SetErr(&bytes.Buffer{})

	err := runReportDebug(cmd, []string{badPath})
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestRunReportDebug_UnsupportedFormat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	inputPath := writeReportInputJSON(t, dir, makeReportInput(judge.StatusPass))

	cmd := newDebugReportCmd()
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Flags().Set("format", "csv"); err != nil {
		t.Fatalf("set flag: %v", err)
	}

	err := runReportDebug(cmd, []string{inputPath})
	if err == nil {
		t.Fatal("expected error for unsupported format, got nil")
	}
}

func TestRunReportDebug_StderrSummary(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	inputPath := writeReportInputJSON(t, dir, makeReportInput(judge.StatusPass))

	cmd := newDebugReportCmd()
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)

	if err := runReportDebug(cmd, []string{inputPath}); err != nil {
		t.Fatalf("runReportDebug error: %v", err)
	}

	stderr := errBuf.String()
	if len(stderr) == 0 {
		t.Error("expected non-empty stderr summary")
	}
}
