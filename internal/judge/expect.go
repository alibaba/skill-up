// Package judge — expect.go implements the Expect pre-check layer.
//
// Expect is a zero-cost, deterministic gate that runs BEFORE any judge.
// If any expect check fails, the case is immediately marked FAIL and the
// (potentially expensive) judge execution is skipped entirely.
//
// Design doc reference: "expect check → on failure mark FAIL immediately and skip judge → on pass continue to judge".
package judge

import (
	"fmt"
	"os"
	"strings"

	"github.com/alibaba/skill-up/internal/config"
)

// ExpectResult is the structured outcome of an expect pre-check.
type ExpectResult struct {
	// Passed is true when ALL expect checks succeeded.
	Passed bool

	// Failures lists every check that did not pass.
	// Empty when Passed == true.
	Failures []ExpectFailure

	// ConfiguredRules lists the names of all rules that were evaluated
	// (e.g. "must_contain", "exit_code"). Used by ToAssertionResults to
	// emit passing assertions for report completeness.
	ConfiguredRules []string
}

// ExpectFailure records a single failed expect check.
type ExpectFailure struct {
	// Rule identifies the check type (e.g. "must_contain", "files_exist").
	Rule string

	// Detail provides human-readable evidence of the failure.
	Detail string
}

// CheckExpect evaluates all expect rules defined in the case configuration
// against the judge Input. It is a pure function with no side-effects beyond
// filesystem reads (for file-related checks).
//
// Returns an ExpectResult indicating pass/fail and detailed failure evidence.
// If expect is nil (no expect block configured), returns a passing result.
func CheckExpect(expect *config.Expect, in Input) *ExpectResult {
	if expect == nil {
		return &ExpectResult{Passed: true}
	}

	var failures []ExpectFailure
	var configuredRules []string

	if len(expect.MustContain) > 0 {
		configuredRules = append(configuredRules, "must_contain")
		failures = append(failures, checkMustContain(expect.MustContain, in.FinalMessage)...)
	}
	if len(expect.MustNotContain) > 0 {
		configuredRules = append(configuredRules, "must_not_contain")
		failures = append(failures, checkMustNotContain(expect.MustNotContain, in.FinalMessage)...)
	}
	if expect.ExitCode != nil {
		configuredRules = append(configuredRules, "exit_code")
		failures = append(failures, checkExitCode(expect.ExitCode, in.ExitCode)...)
	}
	if len(expect.FilesExist) > 0 {
		configuredRules = append(configuredRules, "files_exist")
		failures = append(failures, checkFilesExist(expect.FilesExist, in.WorkspacePath)...)
	}
	if len(expect.FilesNotExist) > 0 {
		configuredRules = append(configuredRules, "files_not_exist")
		failures = append(failures, checkFilesNotExist(expect.FilesNotExist, in.WorkspacePath)...)
	}
	if expect.GoldenFile != "" {
		configuredRules = append(configuredRules, "golden_file")
		failures = append(failures, checkGoldenFile(expect.GoldenFile, in.FinalMessage, in.SkillDir, in.WorkspacePath)...)
	}
	if len(expect.FileContains) > 0 {
		configuredRules = append(configuredRules, "file_contains")
		failures = append(failures, checkFileContains(expect.FileContains, in.WorkspacePath)...)
	}

	return &ExpectResult{
		Passed:          len(failures) == 0,
		Failures:        failures,
		ConfiguredRules: configuredRules,
	}
}

// ToAssertionResults converts the ExpectResult into AssertionResult entries
// suitable for inclusion in grading.json. It emits both passing and failing
// assertions so that the report always shows what expect checks were evaluated.
// Assertions are returned in ConfiguredRules order (the order rules were evaluated).
func (r *ExpectResult) ToAssertionResults() []AssertionResult {
	// Collect all failure details per rule; a single rule (e.g. must_contain)
	// can produce multiple failures when several keywords are missing.
	failDetails := make(map[string][]string, len(r.Failures))
	for _, f := range r.Failures {
		failDetails[f.Rule] = append(failDetails[f.Rule], f.Detail)
	}
	// Emit one AssertionResult per configured rule, in evaluation order.
	results := make([]AssertionResult, 0, len(r.ConfiguredRules))
	for _, rule := range r.ConfiguredRules {
		if details, failed := failDetails[rule]; failed {
			results = append(results, AssertionResult{
				Text:     "expect." + rule,
				Passed:   false,
				Evidence: strings.Join(details, "; "),
			})
		} else {
			results = append(results, AssertionResult{
				Text:     "expect." + rule,
				Passed:   true,
				Evidence: "all checks passed",
			})
		}
	}
	return results
}

// ---------------------------------------------------------------------------
// Individual check implementations
// ---------------------------------------------------------------------------

// checkMustContain verifies that finalMessage contains ALL required keywords.
func checkMustContain(keywords []string, finalMessage string) []ExpectFailure {
	var failures []ExpectFailure
	for _, kw := range keywords {
		if !strings.Contains(finalMessage, kw) {
			failures = append(failures, ExpectFailure{
				Rule:   "must_contain",
				Detail: fmt.Sprintf("output does not contain %q", kw),
			})
		}
	}
	return failures
}

// checkMustNotContain verifies that finalMessage does NOT contain any forbidden keywords.
func checkMustNotContain(keywords []string, finalMessage string) []ExpectFailure {
	var failures []ExpectFailure
	for _, kw := range keywords {
		if strings.Contains(finalMessage, kw) {
			failures = append(failures, ExpectFailure{
				Rule:   "must_not_contain",
				Detail: fmt.Sprintf("output contains forbidden keyword %q", kw),
			})
		}
	}
	return failures
}

// checkExitCode verifies that the actual exit code matches the expected value.
// A nil expected value is treated as "not configured" (no check).
func checkExitCode(expected *int, actual int) []ExpectFailure {
	if expected == nil {
		return nil
	}
	if actual != *expected {
		return []ExpectFailure{{
			Rule:   "exit_code",
			Detail: fmt.Sprintf("expected exit_code %d, got %d", *expected, actual),
		}}
	}
	return nil
}

// checkFilesExist verifies that all listed files exist in the workspace.
func checkFilesExist(files []string, workspace string) []ExpectFailure {
	var failures []ExpectFailure
	for _, f := range files {
		exists, err := fileExistsInWorkspace(workspace, f)
		if err != nil {
			failures = append(failures, ExpectFailure{
				Rule:   "files_exist",
				Detail: fmt.Sprintf("invalid path %q: %v", f, err),
			})
			continue
		}
		if !exists {
			failures = append(failures, ExpectFailure{
				Rule:   "files_exist",
				Detail: fmt.Sprintf("expected file %q does not exist", f),
			})
		}
	}
	return failures
}

// checkFilesNotExist verifies that none of the listed files exist in the workspace.
func checkFilesNotExist(files []string, workspace string) []ExpectFailure {
	var failures []ExpectFailure
	for _, f := range files {
		exists, err := fileExistsInWorkspace(workspace, f)
		if err != nil {
			failures = append(failures, ExpectFailure{
				Rule:   "files_not_exist",
				Detail: fmt.Sprintf("invalid path %q: %v", f, err),
			})
			continue
		}
		if exists {
			failures = append(failures, ExpectFailure{
				Rule:   "files_not_exist",
				Detail: fmt.Sprintf("file %q should not exist but does", f),
			})
		}
	}
	return failures
}

// checkGoldenFile compares the final message against a golden file.
// The golden file path is resolved from the skill root when available.
func checkGoldenFile(goldenPath string, finalMessage string, skillDir string, workspace string) []ExpectFailure {
	if goldenPath == "" {
		return nil
	}
	baseDir := skillDir
	if baseDir == "" {
		baseDir = workspace
	}
	abs, err := safePath(baseDir, goldenPath)
	if err != nil {
		return []ExpectFailure{{
			Rule:   "golden_file",
			Detail: fmt.Sprintf("invalid golden file path %q: %v", goldenPath, err),
		}}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return []ExpectFailure{{
			Rule:   "golden_file",
			Detail: fmt.Sprintf("cannot read golden file %q: %v", goldenPath, err),
		}}
	}
	expected := strings.TrimSpace(string(data))
	actual := strings.TrimSpace(finalMessage)
	if expected != actual {
		// Provide a short diff hint (first divergence point).
		hint := diffHint(expected, actual)
		return []ExpectFailure{{
			Rule:   "golden_file",
			Detail: fmt.Sprintf("output does not match golden file %q: %s", goldenPath, hint),
		}}
	}
	return nil
}

// checkFileContains verifies that each specified file contains the expected text.
func checkFileContains(checks []config.FileContainsCheck, workspace string) []ExpectFailure {
	var failures []ExpectFailure
	for _, fc := range checks {
		abs, err := safePath(workspace, fc.Path)
		if err != nil {
			failures = append(failures, ExpectFailure{
				Rule:   "file_contains",
				Detail: fmt.Sprintf("invalid path %q: %v", fc.Path, err),
			})
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			failures = append(failures, ExpectFailure{
				Rule:   "file_contains",
				Detail: fmt.Sprintf("cannot read file %q: %v", fc.Path, err),
			})
			continue
		}
		if !strings.Contains(string(data), fc.Content) {
			failures = append(failures, ExpectFailure{
				Rule:   "file_contains",
				Detail: fmt.Sprintf("file %q does not contain %q", fc.Path, fc.Content),
			})
		}
	}
	return failures
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// diffHint returns a short textual hint about where expected and actual diverge.
func diffHint(expected, actual string) string {
	maxLen := min(len(expected), len(actual))
	for i := range maxLen {
		if expected[i] != actual[i] {
			start := max(i-20, 0)
			end := min(i+20, maxLen)
			return fmt.Sprintf("first diff at offset %d: expected ...%q... got ...%q...",
				i, expected[start:end], actual[start:end])
		}
	}
	if len(expected) != len(actual) {
		return fmt.Sprintf("length mismatch: expected %d chars, got %d chars", len(expected), len(actual))
	}
	return "unknown diff"
}
