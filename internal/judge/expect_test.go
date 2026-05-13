package judge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
)

func TestCheckExpect_NilExpect(t *testing.T) {
	result := CheckExpect(nil, Input{FinalMessage: "anything"})
	if !result.Passed {
		t.Fatal("nil expect should always pass")
	}
	if len(result.Failures) != 0 {
		t.Fatalf("expected 0 failures, got %d", len(result.Failures))
	}
}

// ---------------------------------------------------------------------------
// must_contain
// ---------------------------------------------------------------------------

func TestCheckExpect_MustContain_Pass(t *testing.T) {
	expect := &config.Expect{MustContain: []string{"null", "bug"}}
	in := Input{FinalMessage: "found null pointer bug at line 42"}
	r := CheckExpect(expect, in)
	if !r.Passed {
		t.Fatalf("expected pass, got failures: %+v", r.Failures)
	}
}

func TestCheckExpect_MustContain_Fail(t *testing.T) {
	expect := &config.Expect{MustContain: []string{"null", "missing_keyword"}}
	in := Input{FinalMessage: "found null pointer bug"}
	r := CheckExpect(expect, in)
	if r.Passed {
		t.Fatal("expected fail")
	}
	if len(r.Failures) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(r.Failures))
	}
	if r.Failures[0].Rule != "must_contain" {
		t.Fatalf("expected rule must_contain, got %s", r.Failures[0].Rule)
	}
}

func TestCheckExpect_MustContain_Empty(t *testing.T) {
	expect := &config.Expect{MustContain: []string{}}
	r := CheckExpect(expect, Input{FinalMessage: ""})
	if !r.Passed {
		t.Fatal("empty must_contain should pass")
	}
}

// ---------------------------------------------------------------------------
// must_not_contain
// ---------------------------------------------------------------------------

func TestCheckExpect_MustNotContain_Pass(t *testing.T) {
	expect := &config.Expect{MustNotContain: []string{"LGTM"}}
	in := Input{FinalMessage: "found bug at line 42"}
	r := CheckExpect(expect, in)
	if !r.Passed {
		t.Fatalf("expected pass, got failures: %+v", r.Failures)
	}
}

func TestCheckExpect_MustNotContain_Fail(t *testing.T) {
	expect := &config.Expect{MustNotContain: []string{"LGTM", "looks good"}}
	in := Input{FinalMessage: "LGTM, looks good to me"}
	r := CheckExpect(expect, in)
	if r.Passed {
		t.Fatal("expected fail")
	}
	if len(r.Failures) != 2 {
		t.Fatalf("expected 2 failures, got %d", len(r.Failures))
	}
}

// ---------------------------------------------------------------------------
// exit_code
// ---------------------------------------------------------------------------

func TestCheckExpect_ExitCode_Pass(t *testing.T) {
	// Expect exit_code=1, actual=1 → pass.
	expect := &config.Expect{ExitCode: intPtr(1)}
	r := CheckExpect(expect, Input{ExitCode: 1})
	if !r.Passed {
		t.Fatalf("expected pass, got failures: %+v", r.Failures)
	}
}

func TestCheckExpect_ExitCode_Fail(t *testing.T) {
	// Expect exit_code=1, actual=2 → fail.
	expect := &config.Expect{ExitCode: intPtr(1)}
	r := CheckExpect(expect, Input{ExitCode: 2})
	if r.Passed {
		t.Fatal("expected fail")
	}
	if r.Failures[0].Rule != "exit_code" {
		t.Fatalf("expected rule exit_code, got %s", r.Failures[0].Rule)
	}
}

func TestCheckExpect_ExitCode_Nil_NoCheck(t *testing.T) {
	// ExitCode == nil means "not configured" — any actual exit code passes.
	expect := &config.Expect{ExitCode: nil}
	r := CheckExpect(expect, Input{ExitCode: 99})
	if !r.Passed {
		t.Fatal("nil exit_code should pass regardless of actual value")
	}
}

func TestCheckExpect_ExitCode_Zero_Configured(t *testing.T) {
	// ExitCode == intPtr(0) means "configured as 0" — actual must be 0.
	expect := &config.Expect{ExitCode: intPtr(0)}

	// actual=0 → pass
	r := CheckExpect(expect, Input{ExitCode: 0})
	if !r.Passed {
		t.Fatal("exit_code 0 configured and actual 0 should pass")
	}

	// actual=1 → fail
	r2 := CheckExpect(expect, Input{ExitCode: 1})
	if r2.Passed {
		t.Fatal("exit_code 0 configured but actual 1 should fail")
	}
}

// ---------------------------------------------------------------------------
// files_exist
// ---------------------------------------------------------------------------

func TestCheckExpect_FilesExist_Pass(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte("content"), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	expect := &config.Expect{FilesExist: []string{"review.md"}}
	r := CheckExpect(expect, Input{WorkspacePath: dir})
	if !r.Passed {
		t.Fatalf("expected pass, got failures: %+v", r.Failures)
	}
}

func TestCheckExpect_FilesExist_Fail(t *testing.T) {
	dir := t.TempDir()

	expect := &config.Expect{FilesExist: []string{"missing.txt"}}
	r := CheckExpect(expect, Input{WorkspacePath: dir})
	if r.Passed {
		t.Fatal("expected fail")
	}
	if r.Failures[0].Rule != "files_exist" {
		t.Fatalf("expected rule files_exist, got %s", r.Failures[0].Rule)
	}
}

// ---------------------------------------------------------------------------
// files_not_exist
// ---------------------------------------------------------------------------

func TestCheckExpect_FilesNotExist_Pass(t *testing.T) {
	dir := t.TempDir()

	expect := &config.Expect{FilesNotExist: []string{"temp.log"}}
	r := CheckExpect(expect, Input{WorkspacePath: dir})
	if !r.Passed {
		t.Fatalf("expected pass, got failures: %+v", r.Failures)
	}
}

func TestCheckExpect_FilesNotExist_Fail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "temp.log"), []byte("log"), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	expect := &config.Expect{FilesNotExist: []string{"temp.log"}}
	r := CheckExpect(expect, Input{WorkspacePath: dir})
	if r.Passed {
		t.Fatal("expected fail")
	}
	if r.Failures[0].Rule != "files_not_exist" {
		t.Fatalf("expected rule files_not_exist, got %s", r.Failures[0].Rule)
	}
}

// ---------------------------------------------------------------------------
// golden_file
// ---------------------------------------------------------------------------

func TestCheckExpect_GoldenFile_Pass(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "sample-skill")
	if err := os.MkdirAll(filepath.Join(skillDir, "evals", "fixtures", "golden"), 0o755); err != nil {
		t.Fatalf("failed to create golden dir: %v", err)
	}
	golden := "expected output line"
	if err := os.WriteFile(filepath.Join(skillDir, "evals", "fixtures", "golden", "expected.txt"), []byte(golden), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	expect := &config.Expect{GoldenFile: "evals/fixtures/golden/expected.txt"}
	r := CheckExpect(expect, Input{FinalMessage: golden, WorkspacePath: root, SkillDir: skillDir})
	if !r.Passed {
		t.Fatalf("expected pass, got failures: %+v", r.Failures)
	}
}

func TestCheckExpect_GoldenFile_Fail_Mismatch(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "sample-skill")
	if err := os.MkdirAll(filepath.Join(skillDir, "evals", "fixtures", "golden"), 0o755); err != nil {
		t.Fatalf("failed to create golden dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "evals", "fixtures", "golden", "expected.txt"), []byte("expected"), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	expect := &config.Expect{GoldenFile: "evals/fixtures/golden/expected.txt"}
	r := CheckExpect(expect, Input{FinalMessage: "actual different", WorkspacePath: root, SkillDir: skillDir})
	if r.Passed {
		t.Fatal("expected fail")
	}
	if r.Failures[0].Rule != "golden_file" {
		t.Fatalf("expected rule golden_file, got %s", r.Failures[0].Rule)
	}
}

func TestCheckExpect_GoldenFile_Fail_Missing(t *testing.T) {
	dir := t.TempDir()

	expect := &config.Expect{GoldenFile: "evals/fixtures/golden/nonexistent.txt"}
	r := CheckExpect(expect, Input{FinalMessage: "anything", WorkspacePath: dir, SkillDir: dir})
	if r.Passed {
		t.Fatal("expected fail when golden file is missing")
	}
}

func TestCheckExpect_GoldenFile_Empty(t *testing.T) {
	// Empty golden_file path should be a no-op.
	expect := &config.Expect{GoldenFile: ""}
	r := CheckExpect(expect, Input{FinalMessage: "anything"})
	if !r.Passed {
		t.Fatal("empty golden_file should pass")
	}
}

// ---------------------------------------------------------------------------
// file_contains
// ---------------------------------------------------------------------------

func TestCheckExpect_FileContains_Pass(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main\nfunc main() {}"), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	expect := &config.Expect{
		FileContains: []config.FileContainsCheck{
			{Path: "app.go", Content: "func main()"},
		},
	}
	r := CheckExpect(expect, Input{WorkspacePath: dir})
	if !r.Passed {
		t.Fatalf("expected pass, got failures: %+v", r.Failures)
	}
}

func TestCheckExpect_FileContains_Fail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main"), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	expect := &config.Expect{
		FileContains: []config.FileContainsCheck{
			{Path: "app.go", Content: "func main()"},
		},
	}
	r := CheckExpect(expect, Input{WorkspacePath: dir})
	if r.Passed {
		t.Fatal("expected fail when file does not contain text")
	}
	if len(r.Failures) != 1 || r.Failures[0].Rule != "file_contains" {
		t.Fatalf("unexpected failures: %+v", r.Failures)
	}
}

func TestCheckExpect_FileContains_FileMissing(t *testing.T) {
	dir := t.TempDir()
	expect := &config.Expect{
		FileContains: []config.FileContainsCheck{
			{Path: "nonexistent.go", Content: "anything"},
		},
	}
	r := CheckExpect(expect, Input{WorkspacePath: dir})
	if r.Passed {
		t.Fatal("expected fail when file does not exist")
	}
}

func TestCheckExpect_FileContains_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	expect := &config.Expect{
		FileContains: []config.FileContainsCheck{
			{Path: "../../etc/passwd", Content: "root"},
		},
	}
	r := CheckExpect(expect, Input{WorkspacePath: dir})
	if r.Passed {
		t.Fatal("expected fail for path traversal")
	}
	if len(r.Failures) != 1 || r.Failures[0].Rule != "file_contains" {
		t.Fatalf("unexpected failures: %+v", r.Failures)
	}
}

// ---------------------------------------------------------------------------
// Combined rules
// ---------------------------------------------------------------------------

func TestCheckExpect_CombinedRules_AllPass(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "output.txt"), []byte("content"), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	expect := &config.Expect{
		MustContain:    []string{"null", "bug"},
		MustNotContain: []string{"LGTM"},
		FilesExist:     []string{"output.txt"},
		FilesNotExist:  []string{"temp.log"},
	}
	in := Input{
		FinalMessage:  "found null pointer bug",
		ExitCode:      0,
		WorkspacePath: dir,
	}
	r := CheckExpect(expect, in)
	if !r.Passed {
		t.Fatalf("expected all pass, got failures: %+v", r.Failures)
	}
	// Verify ConfiguredRules are populated.
	if len(r.ConfiguredRules) != 4 {
		t.Fatalf("expected 4 configured rules, got %d: %v", len(r.ConfiguredRules), r.ConfiguredRules)
	}
	// Verify ToAssertionResults emits passing entries.
	assertions := r.ToAssertionResults()
	if len(assertions) != 4 {
		t.Fatalf("expected 4 passing assertions, got %d: %+v", len(assertions), assertions)
	}
	for _, a := range assertions {
		if !a.Passed {
			t.Fatalf("expected all assertions to pass, got: %+v", a)
		}
	}
}

func TestCheckExpect_CombinedRules_MultipleFailures(t *testing.T) {
	dir := t.TempDir()

	expect := &config.Expect{
		MustContain:    []string{"missing_kw"},
		MustNotContain: []string{"LGTM"},
		ExitCode:       intPtr(2), // expect 2, actual is 1 → triggers exit_code failure
		FilesExist:     []string{"missing.txt"},
	}
	in := Input{
		FinalMessage:  "LGTM",
		ExitCode:      1,
		WorkspacePath: dir,
	}
	r := CheckExpect(expect, in)
	if r.Passed {
		t.Fatal("expected fail")
	}
	// Expect 4 failures: must_contain, must_not_contain, exit_code, files_exist
	if len(r.Failures) != 4 {
		t.Fatalf("expected 4 failures, got %d: %+v", len(r.Failures), r.Failures)
	}
}

// ---------------------------------------------------------------------------
// ToAssertionResults
// ---------------------------------------------------------------------------

func TestExpectResult_ToAssertionResults_Passed(t *testing.T) {
	r := &ExpectResult{Passed: true, ConfiguredRules: []string{"must_contain", "exit_code"}}
	results := r.ToAssertionResults()
	if len(results) != 2 {
		t.Fatalf("expected 2 passing assertion results, got %d", len(results))
	}
	for _, ar := range results {
		if !ar.Passed {
			t.Fatalf("expected all assertions to pass, got failed: %+v", ar)
		}
	}
	if results[0].Text != "expect.must_contain" {
		t.Fatalf("expected text 'expect.must_contain', got %q", results[0].Text)
	}
	if results[1].Text != "expect.exit_code" {
		t.Fatalf("expected text 'expect.exit_code', got %q", results[1].Text)
	}
}

func TestExpectResult_ToAssertionResults_Passed_NoRules(t *testing.T) {
	// No configured rules (e.g. nil expect) → no assertions.
	r := &ExpectResult{Passed: true}
	results := r.ToAssertionResults()
	if len(results) != 0 {
		t.Fatalf("expected 0 assertion results for passed expect with no rules, got %d", len(results))
	}
}

func TestExpectResult_ToAssertionResults_Failed(t *testing.T) {
	r := &ExpectResult{
		Passed: false,
		Failures: []ExpectFailure{
			{Rule: "must_contain", Detail: "missing keyword"},
			{Rule: "exit_code", Detail: "expected 0 got 1"},
		},
		ConfiguredRules: []string{"must_contain", "exit_code", "files_exist"},
	}
	results := r.ToAssertionResults()
	// 2 failures + 1 passing (files_exist) = 3 total
	if len(results) != 3 {
		t.Fatalf("expected 3 assertion results, got %d: %+v", len(results), results)
	}
	// First two are failures.
	if results[0].Passed {
		t.Fatal("assertion result[0] should be failed")
	}
	if results[0].Text != "expect.must_contain" {
		t.Fatalf("expected text 'expect.must_contain', got %q", results[0].Text)
	}
	if results[1].Passed {
		t.Fatal("assertion result[1] should be failed")
	}
	// Third is the passing rule.
	if !results[2].Passed {
		t.Fatal("assertion result[2] should be passed")
	}
	if results[2].Text != "expect.files_exist" {
		t.Fatalf("expected text 'expect.files_exist', got %q", results[2].Text)
	}
}

func TestExpectResult_ToAssertionResults_MultipleFailuresSameRule(t *testing.T) {
	// Two must_contain keywords both missing → two ExpectFailure entries for the
	// same rule. The map used to overwrite the first with the second; now both
	// details must appear in the single assertion's Evidence.
	r := &ExpectResult{
		Passed: false,
		Failures: []ExpectFailure{
			{Rule: "must_contain", Detail: `output does not contain "foo"`},
			{Rule: "must_contain", Detail: `output does not contain "bar"`},
		},
		ConfiguredRules: []string{"must_contain"},
	}
	results := r.ToAssertionResults()
	if len(results) != 1 {
		t.Fatalf("expected 1 assertion result (one per rule), got %d", len(results))
	}
	if results[0].Passed {
		t.Fatal("assertion should be failed")
	}
	if !strings.Contains(results[0].Evidence, "foo") {
		t.Errorf("evidence missing first keyword detail: %q", results[0].Evidence)
	}
	if !strings.Contains(results[0].Evidence, "bar") {
		t.Errorf("evidence missing second keyword detail: %q", results[0].Evidence)
	}
}
