package cli

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
)

// newCompareCmd creates an isolated cobra command wired to runCompare for testing.
func newCompareCmd() *cobra.Command {
	cmd := &cobra.Command{RunE: runCompare}
	cmd.Flags().String("format", "text", "")
	cmd.Flags().Bool("fail-on-regression", false, "")
	cmd.Flags().Float64("max-token-increase-percent", 0, "")
	return cmd
}

func TestRunCompare_DefaultTextFormat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldPath := writeCompareResultJSON(t, dir, "old.json", makeReportInput(judge.StatusFail))
	newPath := writeCompareResultJSON(t, dir, "new.json", makeReportInput(judge.StatusPass))

	cmd := newCompareCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runCompare(cmd, []string{oldPath, newPath}); err != nil {
		t.Fatalf("runCompare error: %v", err)
	}

	for _, section := range []string{"Run summary", "Metadata differences", "Case transitions", "Gates: passed"} {
		if !strings.Contains(out.String(), section) {
			t.Errorf("text output should contain %q, got: %s", section, out.String())
		}
	}
}

func TestRunCompare_AllowsEmptyModelName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldInput := makeReportInput(judge.StatusFail)
	oldInput.ModelName = ""
	newInput := makeReportInput(judge.StatusPass)
	newInput.ModelName = ""
	oldPath := writeCompareResultJSON(t, dir, "old.json", oldInput)
	newPath := writeCompareResultJSON(t, dir, "new.json", newInput)

	cmd := newCompareCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runCompare(cmd, []string{oldPath, newPath}); err != nil {
		t.Fatalf("runCompare error: %v", err)
	}
	if !strings.Contains(out.String(), "Run summary") {
		t.Errorf("text output should contain run summary, got: %s", out.String())
	}
}

func TestRunCompare_JSONFormat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldPath := writeCompareResultJSON(t, dir, "old.json", makeReportInput(judge.StatusFail))
	newPath := writeCompareResultJSON(t, dir, "new.json", makeReportInput(judge.StatusPass))

	cmd := newCompareCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Flags().Set("format", "json"); err != nil {
		t.Fatalf("set format: %v", err)
	}

	if err := runCompare(cmd, []string{oldPath, newPath}); err != nil {
		t.Fatalf("runCompare error: %v", err)
	}

	var result struct {
		Run   json.RawMessage `json:"run"`
		Cases json.RawMessage `json:"cases"`
		Gates json.RawMessage `json:"gates"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal compare output: %v", err)
	}
	if result.Run == nil || result.Cases == nil || result.Gates == nil {
		t.Fatalf("JSON output missing stable result fields: %s", out.String())
	}
}

func TestRunCompare_RegressionGateWritesTextBeforeReturningError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldPath := writeCompareResultJSON(t, dir, "old.json", makeReportInput(judge.StatusPass))
	newPath := writeCompareResultJSON(t, dir, "new.json", makeReportInput(judge.StatusFail))

	cmd := newCompareCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Flags().Set("fail-on-regression", "true"); err != nil {
		t.Fatalf("set fail-on-regression flag: %v", err)
	}

	err := runCompare(cmd, []string{oldPath, newPath})
	if err == nil || !strings.Contains(err.Error(), "comparison gates failed") {
		t.Fatalf("runCompare error = %v, want gate failure", err)
	}
	for _, want := range []string{"Run summary", "Gates: failed", "1 case(s) regressed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("text output should contain %q before returning error, got: %s", want, out.String())
		}
	}
}

func TestRunCompare_TokenGateWritesJSONBeforeReturningError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldInput := makeReportInput(judge.StatusPass)
	oldInput.TotalTokens = 100
	newInput := makeReportInput(judge.StatusPass)
	newInput.TotalTokens = 150
	oldPath := writeCompareResultJSON(t, dir, "old.json", oldInput)
	newPath := writeCompareResultJSON(t, dir, "new.json", newInput)

	cmd := newCompareCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Flags().Set("format", "json"); err != nil {
		t.Fatalf("set format flag: %v", err)
	}
	if err := cmd.Flags().Set("max-token-increase-percent", "20"); err != nil {
		t.Fatalf("set max-token-increase-percent flag: %v", err)
	}

	err := runCompare(cmd, []string{oldPath, newPath})
	if err == nil || !strings.Contains(err.Error(), "comparison gates failed") {
		t.Fatalf("runCompare error = %v, want gate failure", err)
	}

	var result struct {
		Gates struct {
			Passed   bool     `json:"passed"`
			Failures []string `json:"failures"`
		} `json:"gates"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal JSON output written before error: %v\noutput: %s", err, out.String())
	}
	if result.Gates.Passed || len(result.Gates.Failures) != 1 || !strings.Contains(result.Gates.Failures[0], "50.00%") {
		t.Fatalf("JSON gates = %#v, want failed token increase gate", result.Gates)
	}
}

func TestRunCompare_InvalidInputsIncludeRoleAndPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	validPath := writeCompareResultJSON(t, dir, "valid.json", makeReportInput(judge.StatusPass))
	invalidPath := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(invalidPath, []byte("not-json"), 0o600); err != nil {
		t.Fatalf("write invalid input: %v", err)
	}

	tests := []struct {
		name string
		args []string
		role string
		path string
	}{
		{name: "old", args: []string{invalidPath, validPath}, role: "old", path: invalidPath},
		{name: "new", args: []string{validPath, invalidPath}, role: "new", path: invalidPath},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := newCompareCmd()
			cmd.SetOut(&bytes.Buffer{})

			err := runCompare(cmd, tc.args)
			if err == nil {
				t.Fatal("expected invalid JSON error, got nil")
			}
			if !strings.Contains(err.Error(), tc.role) || !strings.Contains(err.Error(), tc.path) {
				t.Errorf("error should identify %s input %q, got: %v", tc.role, tc.path, err)
			}
		})
	}
}

func TestRunCompare_RejectsStructurallyInvalidInputs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	validPath := writeCompareResultJSON(t, dir, "valid.json", makeReportInput(judge.StatusPass))

	tests := []struct {
		name      string
		input     report.Input
		wantError string
	}{
		{name: "empty result", input: report.Input{}, wantError: "missing required result metadata"},
		{
			name: "blank case ID",
			input: func() report.Input {
				input := makeReportInput(judge.StatusPass)
				input.CaseResults = []report.CaseResult{{CaseID: " ", Status: judge.StatusPass}}
				return input
			}(),
			wantError: "case result has an empty case ID",
		},
		{
			name: "invalid status",
			input: func() report.Input {
				input := makeReportInput(judge.StatusPass)
				input.CaseResults = []report.CaseResult{{CaseID: "case-1", Status: "UNKNOWN"}}
				return input
			}(),
			wantError: "case \"case-1\" has invalid status \"UNKNOWN\"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			invalidPath := writeCompareResultJSON(t, dir, tc.name+".json", tc.input)
			cmd := newCompareCmd()
			cmd.SetOut(&bytes.Buffer{})

			err := runCompare(cmd, []string{invalidPath, validPath})
			if err == nil {
				t.Fatal("expected structural validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantError) {
				t.Errorf("error = %v, want to contain %q", err, tc.wantError)
			}
			if !strings.Contains(err.Error(), "old") || !strings.Contains(err.Error(), invalidPath) {
				t.Errorf("error should identify old input %q, got: %v", invalidPath, err)
			}
		})
	}
}

func TestRunCompare_RejectsInvalidMaxTokenIncreasePercent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldPath := writeCompareResultJSON(t, dir, "old.json", makeReportInput(judge.StatusPass))
	newPath := writeCompareResultJSON(t, dir, "new.json", makeReportInput(judge.StatusPass))

	for _, value := range []float64{-1, math.NaN(), math.Inf(1)} {
		t.Run("invalid value", func(t *testing.T) {
			t.Parallel()
			cmd := newCompareCmd()
			cmd.SetOut(&bytes.Buffer{})
			if err := cmd.Flags().Set("max-token-increase-percent", strconv.FormatFloat(value, 'g', -1, 64)); err != nil {
				t.Fatalf("set max-token-increase-percent: %v", err)
			}

			err := runCompare(cmd, []string{oldPath, newPath})
			if err == nil || !strings.Contains(err.Error(), "max-token-increase-percent") {
				t.Fatalf("runCompare error = %v, want invalid token limit error", err)
			}
		})
	}
}

func TestRunCompare_UnsupportedFormat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldPath := writeCompareResultJSON(t, dir, "old.json", makeReportInput(judge.StatusPass))
	newPath := writeCompareResultJSON(t, dir, "new.json", makeReportInput(judge.StatusPass))

	cmd := newCompareCmd()
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Flags().Set("format", "csv"); err != nil {
		t.Fatalf("set format: %v", err)
	}

	err := runCompare(cmd, []string{oldPath, newPath})
	if err == nil {
		t.Fatal("expected unsupported format error, got nil")
	}
	if !strings.Contains(err.Error(), "csv") {
		t.Errorf("error should name unsupported format, got: %v", err)
	}
}

func writeCompareResultJSON(t *testing.T, dir, name string, input report.Input) string {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal report input: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write report input: %v", err)
	}
	return path
}
