package cli

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	comparepkg "github.com/alibaba/skill-up/internal/compare"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
)

// newCompareCmd creates an isolated cobra command wired to runCompare for testing.
func newCompareCmd() *cobra.Command {
	cmd := &cobra.Command{RunE: runCompare}
	configureCompareFlags(cmd)
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
		Runs        json.RawMessage   `json:"runs"`
		Comparisons []json.RawMessage `json:"comparisons"`
		Gates       json.RawMessage   `json:"gates"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal compare output: %v", err)
	}
	if result.Runs == nil || len(result.Comparisons) != 1 || result.Gates == nil {
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

func TestRunCompareShareableFilesSurviveGateFailure(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"html", "markdown"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			oldPath := writeCompareResultJSON(t, dir, "old.json", makeReportInput(judge.StatusPass))
			newPath := writeCompareResultJSON(t, dir, "new.json", makeReportInput(judge.StatusFail))
			original, err := os.ReadFile(oldPath)
			if err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(dir, "comparison."+format)
			cmd := newCompareCmd()
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			for flag, value := range map[string]string{"format": format, "output": output, "fail-on-regression": "true", "baseline-label": "Before", "candidate-label": "After"} {
				if err := cmd.Flags().Set(flag, value); err != nil {
					t.Fatal(err)
				}
			}
			if err := runCompare(cmd, []string{oldPath, newPath}); err == nil || !strings.Contains(err.Error(), "comparison gates failed") {
				t.Fatalf("expected gate failure: %v", err)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"Before", "After", "Regressed (1)", "1 case(s) regressed"} {
				if !strings.Contains(string(data), want) {
					t.Errorf("%s missing %q", format, want)
				}
			}
			if stdout.Len() != 0 {
				t.Fatalf("file output leaked into stdout: %s", stdout.String())
			}
			retained, err := os.ReadFile(oldPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original, retained) {
				t.Fatal("source artifact modified")
			}
		})
	}
}

func TestRunCompareProtectsSourceArtifacts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldPath := writeCompareResultJSON(t, dir, "old.json", makeReportInput(judge.StatusPass))
	newPath := writeCompareResultJSON(t, dir, "new.json", makeReportInput(judge.StatusPass))
	linked := filepath.Join(dir, "linked.json")
	if err := os.Link(oldPath, linked); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{oldPath, newPath, linked} {
		cmd := newCompareCmd()
		if err := cmd.Flags().Set("output", output); err != nil {
			t.Fatal(err)
		}
		if err := runCompare(cmd, []string{oldPath, newPath}); err == nil || !strings.Contains(err.Error(), "must not overwrite") {
			t.Fatalf("output %q: %v", output, err)
		}
	}
}

func TestRootCommandIncludesCompare(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"compare"})
	if err != nil {
		t.Fatal(err)
	}
	if command != compareCmd {
		t.Fatal("compare command not registered")
	}
}

func TestRunCompareMultipleInputsAndLabels(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	paths := []string{
		writeCompareResultJSON(t, dir, "baseline.json", makeReportInput(judge.StatusPass)),
		writeCompareResultJSON(t, dir, "same.json", makeReportInput(judge.StatusPass)),
		writeCompareResultJSON(t, dir, "regressed.json", makeReportInput(judge.StatusFail)),
	}
	for _, format := range []string{"json", "text", "html", "markdown"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			cmd := newCompareCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			for flag, value := range map[string]string{"format": format, "fail-on-regression": "true"} {
				if err := cmd.Flags().Set(flag, value); err != nil {
					t.Fatal(err)
				}
			}
			for _, label := range []string{"Same Skill", "Revised Skill"} {
				if err := cmd.Flags().Set("candidate-label", label); err != nil {
					t.Fatal(err)
				}
			}
			if err := runCompare(cmd, paths); err == nil || !strings.Contains(err.Error(), "Revised Skill") {
				t.Fatalf("later candidate gate not enforced: %v", err)
			}
			for _, want := range []string{"Same Skill", "Revised Skill", "regressed.json"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("%s output lost %q", format, want)
				}
			}
			if format == "json" {
				assertMultipleCompareJSON(t, out.Bytes())
			}
		})
	}
}

func TestRunCompareValidatesEveryCandidateBeforeWriting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	valid := writeCompareResultJSON(t, dir, "valid.json", makeReportInput(judge.StatusPass))
	missing := filepath.Join(dir, "missing-third.json")
	cmd := newCompareCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runCompare(cmd, []string{valid, valid, missing}); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("later invalid input ignored: %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("partial report emitted for invalid inputs")
	}
	if err := cmd.Flags().Set("candidate-label", "only-one-label"); err != nil {
		t.Fatal(err)
	}
	if err := runCompare(cmd, []string{valid, valid, valid}); err == nil || !strings.Contains(err.Error(), "2 candidates") {
		t.Fatalf("ambiguous label count accepted: %v", err)
	}
}

func TestRunCompareProtectsLaterCandidate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base := writeCompareResultJSON(t, dir, "baseline.json", makeReportInput(judge.StatusPass))
	later := writeCompareResultJSON(t, dir, "later.json", makeReportInput(judge.StatusFail))
	cmd := newCompareCmd()
	if err := cmd.Flags().Set("output", later); err != nil {
		t.Fatal(err)
	}
	if err := runCompare(cmd, []string{base, base, later}); err == nil || !strings.Contains(err.Error(), "must not overwrite") {
		t.Fatalf("later candidate can be overwritten: %v", err)
	}
}

func assertMultipleCompareJSON(t *testing.T, data []byte) {
	t.Helper()
	var set comparepkg.Set
	if err := json.Unmarshal(data, &set); err != nil {
		t.Fatal(err)
	}
	if len(set.Runs) != 3 || len(set.Comparisons) != 2 || !set.Comparisons[0].Gates.Passed || set.Comparisons[1].Gates.Passed {
		t.Fatalf("wrong per-candidate gates: %+v", set)
	}
}

func TestAutomaticCompareLabels(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }
	tests := []struct {
		name  string
		paths []string
		want  []string
	}{
		{"distinct directories", []string{path("alpha", "result.json"), path("beta", "result.json"), path("gamma", "result.json")}, []string{"alpha", "beta", "gamma"}},
		{"same directory", []string{path("alpha", "a.json"), path("alpha", "b.json")}, []string{"1", "2"}},
		{"one duplicate switches all labels", []string{path("alpha", "result.json"), path("beta", "a.json"), path("beta", "b.json")}, []string{"1", "2", "3"}},
		{"same directory basename", []string{path("a", "run", "result.json"), path("b", "run", "result.json")}, []string{"1", "2"}},
		{"bare filenames", []string{"old.json", "new.json", "other.json"}, []string{"1", "2", "3"}},
		{"clean relative paths", []string{filepath.Join("alpha", "nested", "..", "result.json"), filepath.Join("beta", "result.json")}, []string{"alpha", "beta"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := automaticCompareLabels(tc.paths); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("labels = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAutomaticLabelsPropagateToReportsAndRunIdentity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	var paths []string
	for _, name := range []string{"alpha", "beta", "gamma"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, writeCompareResultJSON(t, dir, "result.json", makeReportInput(judge.StatusPass)))
	}
	for _, format := range []string{"json", "html", "markdown", "text"} {
		cmd := newCompareCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := cmd.Flags().Set("format", format); err != nil {
			t.Fatal(err)
		}
		if err := runCompare(cmd, paths); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "Candidate 1") || strings.Contains(out.String(), "Baseline") {
			t.Errorf("%s still uses role-based display labels", format)
		}
		if format == jsonFormat {
			assertDirectoryRunIdentity(t, out.Bytes())
		}
	}
}

func assertDirectoryRunIdentity(t *testing.T, data []byte) {
	t.Helper()
	var set comparepkg.Set
	if err := json.Unmarshal(data, &set); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"alpha", "beta", "gamma"} {
		if source := set.Runs[i].Source; source.ID != i+1 || source.Label != name {
			t.Fatalf("wrong run identity: %+v", source)
		}
	}
	if set.Baseline != set.Runs[0].Source || set.Comparisons[1].Candidate != set.Runs[2].Source {
		t.Fatal("reference or comparison lost run identity")
	}
}
