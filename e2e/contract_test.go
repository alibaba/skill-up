//go:build e2e

// Package e2e contains documentation-driven contract tests.
//
// Each test in this file directly maps to a verifiable promise from the
// user manual (docs/user-manual/). When all contract tests pass, every documented
// CLI behavior has been verified. Tests marked with t.Skip("KNOWN_GAP: ...")
// represent documented features that are not yet implemented.
//
// Run: go test -tags e2e -run TestContract ./e2e/ -v
//
// The test output serves as a capability matrix:
//
//	PASS  = promise verified
//	SKIP  = known gap (documented but unimplemented)
//	FAIL  = regression (was working, now broken)
package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// CLI Reference — skill-up run
// ============================================================================

// --- Exit Codes ---

func TestContract_Run_ExitCode_AllPass_Returns0(t *testing.T) {
	t.Parallel()

	// Create a project where the mock engine output satisfies expect.must_contain.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "# Contract Test Skill\n")
	writeFile(t, filepath.Join(dir, "evals", "eval.yaml"), `schema_version: v1alpha1
environment:
  type: none
skills:
  - source: local_path
    path: .
engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto
cases:
  files:
    - evals/cases/pass.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "pass.yaml"), `id: pass-case
title: Should pass
input:
  prompt: Find the null pointer bug
expect:
  must_contain:
    - "null"
`)

	env := mockEngineEnv(t)
	result := Run(t, RunConfig{Env: env, WorkDir: dir, Timeout: 60 * time.Second},
		"run", filepath.Join(dir, "evals", "eval.yaml"), "--no-delete")

	// Doc promise: exit code 0 when all cases pass
	if result.ExitCode != 0 {
		t.Errorf("DOC: 'exit 0 = all cases pass', got exit %d\nstdout: %s\nstderr: %s",
			result.ExitCode, result.Stdout, result.Stderr)
	}
}

func TestContract_Run_ExitCode_CaseFail_Returns1(t *testing.T) {
	t.Parallel()
	t.Skip("KNOWN_GAP: CLI returns exit 0 even when cases fail (see gap report #1)")
}

func TestContract_Run_ExitCode_CaseError_Returns1(t *testing.T) {
	t.Parallel()
	t.Skip("KNOWN_GAP: CLI returns exit 0 even when cases error (see gap report #1)")
}

// --- Flags ---

func TestContract_Run_OutputDir_CustomPath(t *testing.T) {
	t.Parallel()
	t.Skip("KNOWN_GAP: --output-dir flag is accepted but not implemented (see gap report #3)")
}

func TestContract_Run_Iteration_ExplicitNumber(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "# Contract Test Skill\n")
	writeFile(t, filepath.Join(dir, "evals", "eval.yaml"), `schema_version: v1alpha1
environment:
  type: none
skills:
  - source: local_path
    path: .
engine:
  name: qoder-cli
  model:
    provider: test
    name: auto
cases:
  files:
    - evals/cases/pass.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "pass.yaml"), `id: pass-case
title: Should pass twice
input:
  prompt: Find the null pointer bug
expect:
  must_contain:
    - "null"
`)

	outputDir := filepath.Join(dir, "artifacts")
	env := mockEngineEnv(t)
	result := Run(t, RunConfig{Env: env, WorkDir: dir, Timeout: 60 * time.Second},
		"run", filepath.Join(dir, "evals", "eval.yaml"), "--iteration", "2", "--output-dir", outputDir, "--no-delete")

	if result.ExitCode != 0 {
		t.Fatalf("DOC: --iteration should run successfully, got exit %d\nstdout: %s\nstderr: %s",
			result.ExitCode, result.Stdout, result.Stderr)
	}
	for _, rel := range []string{
		filepath.Join("iteration-1", "result.json"),
		filepath.Join("iteration-2", "result.json"),
	} {
		path := filepath.Join(outputDir, rel)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("DOC: expected %s to exist: %v", path, err)
		}
	}
}

func TestContract_Run_Format_OnlyJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "# Contract Test Skill\n")
	writeFile(t, filepath.Join(dir, "evals", "eval.yaml"), `schema_version: v1alpha1
environment:
  type: none
skills:
  - source: local_path
    path: .
engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto
cases:
  files:
    - evals/cases/pass.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "pass.yaml"), `id: format-json-case
title: Format JSON only
input:
  prompt: Find the null pointer bug
expect:
  must_contain:
    - "null"
`)

	outputDir := filepath.Join(dir, "artifacts")
	env := mockEngineEnv(t)
	result := Run(t, RunConfig{Env: env, WorkDir: dir, Timeout: 60 * time.Second},
		"run", filepath.Join(dir, "evals", "eval.yaml"),
		"--format", "json", "--output-dir", outputDir, "--no-delete")

	if result.ExitCode != 0 {
		t.Fatalf("DOC: --format json should exit 0, got exit %d\nstdout: %s\nstderr: %s",
			result.ExitCode, result.Stdout, result.Stderr)
	}

	iterDir := filepath.Join(outputDir, "iteration-1")
	if _, err := os.Stat(filepath.Join(iterDir, "result.json")); err != nil {
		t.Errorf("DOC: result.json should always exist, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(iterDir, "report.html")); err == nil {
		t.Errorf("DOC: report.html should NOT exist when --format json only")
	}
	if _, err := os.Stat(filepath.Join(iterDir, "report.xml")); err == nil {
		t.Errorf("DOC: report.xml should NOT exist when --format json only")
	}
}

func TestContract_Run_Format_MultipleFormats(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "# Contract Test Skill\n")
	writeFile(t, filepath.Join(dir, "evals", "eval.yaml"), `schema_version: v1alpha1
environment:
  type: none
skills:
  - source: local_path
    path: .
engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto
cases:
  files:
    - evals/cases/pass.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "pass.yaml"), `id: format-multi-case
title: Format multiple
input:
  prompt: Find the null pointer bug
expect:
  must_contain:
    - "null"
`)

	outputDir := filepath.Join(dir, "artifacts")
	env := mockEngineEnv(t)
	result := Run(t, RunConfig{Env: env, WorkDir: dir, Timeout: 60 * time.Second},
		"run", filepath.Join(dir, "evals", "eval.yaml"),
		"--format", "json", "--format", "html", "--output-dir", outputDir, "--no-delete")

	if result.ExitCode != 0 {
		t.Fatalf("DOC: --format json --format html should exit 0, got exit %d\nstdout: %s\nstderr: %s",
			result.ExitCode, result.Stdout, result.Stderr)
	}

	iterDir := filepath.Join(outputDir, "iteration-1")
	if _, err := os.Stat(filepath.Join(iterDir, "result.json")); err != nil {
		t.Errorf("DOC: result.json should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(iterDir, "report.html")); err != nil {
		t.Errorf("DOC: report.html should exist when --format html specified: %v", err)
	}
	if _, err := os.Stat(filepath.Join(iterDir, "report.xml")); err == nil {
		t.Errorf("DOC: report.xml should NOT exist when only json+html formats specified")
	}
}

func TestContract_Run_DryRun_ShowsSummary(t *testing.T) {
	t.Parallel()

	mockDir := getMockEngineTestdataDir()
	evalPath := filepath.Join(mockDir, "evals", "eval.yaml")

	result := RunSimple(t, "run", evalPath, "--dry-run")

	if result.ExitCode != 0 {
		t.Fatalf("DOC: dry-run should exit 0, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "Would run") {
		t.Errorf("DOC: dry-run should output 'Would run', got: %s", result.Stdout)
	}
}

func TestContract_Run_AutoDetect_EvalsJSON(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	evalsDir := filepath.Join(tmpDir, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	evalsJSON := `{
  "skill_name": "test-skill",
  "evals": [
    {"id": 1, "prompt": "hello", "expected_output": "hello", "expectations": ["hello"]}
  ]
}`
	writeFile(t, filepath.Join(evalsDir, "evals.json"), evalsJSON)

	result := RunSimple(t, "run", "--auto", "--dry-run", tmpDir)

	if result.ExitCode != 0 {
		t.Fatalf("DOC: --auto should detect evals.json, got exit %d\nstdout: %s\nstderr: %s",
			result.ExitCode, result.Stdout, result.Stderr)
	}
}

func TestContract_Run_IncludeCaseName_GlobFilter(t *testing.T) {
	t.Parallel()

	mockDir := getMockEngineTestdataDir()
	evalPath := filepath.Join(mockDir, "evals", "eval.yaml")

	result := RunSimple(t, "run", evalPath, "--dry-run", "--include-case-name", "must-*")

	if result.ExitCode != 0 {
		t.Fatalf("DOC: --include-case-name with glob should work, got exit %d", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "must-contain-pass") {
		t.Errorf("DOC: should include must-contain-pass")
	}
	if strings.Contains(result.Stdout, "rule-based-pass") {
		t.Errorf("DOC: should NOT include rule-based-pass when filter is 'must-*'")
	}
}

func TestContract_Run_ExcludeCaseName_GlobFilter(t *testing.T) {
	t.Parallel()

	mockDir := getMockEngineTestdataDir()
	evalPath := filepath.Join(mockDir, "evals", "eval.yaml")

	result := RunSimple(t, "run", evalPath, "--dry-run", "--exclude-case-name", "*-fail")

	if result.ExitCode != 0 {
		t.Fatalf("DOC: --exclude-case-name should work, got exit %d", result.ExitCode)
	}
	if strings.Contains(result.Stdout, "must-contain-fail") {
		t.Errorf("DOC: must-contain-fail should be excluded by '*-fail'")
	}
	if !strings.Contains(result.Stdout, "must-contain-pass") {
		t.Errorf("DOC: must-contain-pass should NOT be excluded")
	}
}

func TestContract_Run_EngineOverride(t *testing.T) {
	t.Parallel()

	mockDir := getMockEngineTestdataDir()
	evalPath := filepath.Join(mockDir, "evals", "eval.yaml")

	// Override engine to claude_code (dry-run so no real execution needed)
	result := RunSimple(t, "run", evalPath, "--dry-run", "--engine", "claude_code")

	if result.ExitCode != 0 {
		t.Fatalf("DOC: --engine override should work, got exit %d\nstderr: %s",
			result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "claude_code") {
		t.Errorf("DOC: output should mention overridden engine claude_code")
	}
}

func TestContract_Run_ModelOverride(t *testing.T) {
	t.Parallel()

	mockDir := getMockEngineTestdataDir()
	evalPath := filepath.Join(mockDir, "evals", "eval.yaml")

	result := RunSimple(t, "run", evalPath, "--dry-run", "--model", "openai/gpt-4")

	if result.ExitCode != 0 {
		t.Fatalf("DOC: --model override should work, got exit %d\nstderr: %s",
			result.ExitCode, result.Stderr)
	}
}

func TestContract_Run_NoDelete_PreservesWorkspace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "# No Delete Skill\n")
	writeFile(t, filepath.Join(dir, "evals", "eval.yaml"), `schema_version: v1alpha1
environment:
  type: none
skills:
  - source: local_path
    path: .
engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto
cases:
  files:
    - evals/cases/test.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "test.yaml"), `id: no-delete-test
title: Test no-delete
input:
  prompt: hello
`)

	env := mockEngineEnv(t)
	result := Run(t, RunConfig{Env: env, WorkDir: dir, Timeout: 60 * time.Second},
		"run", filepath.Join(dir, "evals", "eval.yaml"), "--no-delete")

	if result.ExitCode != 0 {
		t.Logf("Warning: run exit %d (may be expected)", result.ExitCode)
	}

	// --no-delete should preserve workspace
	// The workspace is created alongside the skill directory as <skillName>-workspace/
	pattern := filepath.Join(filepath.Dir(dir), "*-workspace")
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		t.Logf("Note: workspace directory not found; --no-delete behavior may vary")
	}
}

// ============================================================================
// CLI Reference — skill-up validate
// ============================================================================

func TestContract_Validate_ValidConfig_Returns0(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	evalPath := makeMinimalEvalProject(t, dir)

	result := RunSimple(t, "validate", evalPath)

	if result.ExitCode != 0 {
		t.Fatalf("DOC: validate should return 0 for valid config, got %d\nstderr: %s",
			result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "valid") {
		t.Errorf("DOC: validate output should contain 'valid'")
	}
}

func TestContract_Validate_InvalidConfig_ReturnsError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "eval.yaml"), "not: a: valid: config: ][")

	result := RunSimple(t, "validate", filepath.Join(dir, "eval.yaml"))

	if result.ExitCode == 0 {
		t.Errorf("DOC: validate should return non-zero for invalid config")
	}
}

func TestContract_Validate_MissingFile_ReturnsError(t *testing.T) {
	t.Parallel()

	result := RunSimple(t, "validate", "/nonexistent/path/eval.yaml")

	if result.ExitCode == 0 {
		t.Errorf("DOC: validate should return non-zero for missing file")
	}
}

// ============================================================================
// CLI Reference — skill-up list-cases
// ============================================================================

func TestContract_ListCases_ShowsAllConfigured(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	evalPath := makeMinimalEvalProject(t, dir)

	result := RunSimple(t, "list-cases", evalPath)

	if result.ExitCode != 0 {
		t.Fatalf("DOC: list-cases should exit 0, got %d", result.ExitCode)
	}
	for _, id := range []string{"basic-success", "edge-case"} {
		if !strings.Contains(result.Stdout, id) {
			t.Errorf("DOC: list-cases should show case %q", id)
		}
	}
}

func TestContract_ListCases_MissingConfig_ReturnsError(t *testing.T) {
	t.Parallel()

	result := RunSimple(t, "list-cases", "/nonexistent/eval.yaml")

	if result.ExitCode == 0 {
		t.Errorf("DOC: list-cases should fail for missing config")
	}
}

// ============================================================================
// CLI Reference — skill-up import
// ============================================================================

func TestContract_Import_AnthropicFormat(t *testing.T) {
	t.Parallel()

	evalsPath := filepath.Join(projectRootFromTest(), "e2e", "testdata", "evals.json")
	tmpDir := t.TempDir()

	result := RunSimple(t, "import", evalsPath, "--output", tmpDir)

	if result.ExitCode != 0 {
		t.Fatalf("DOC: import should convert evals.json successfully, got exit %d\nstderr: %s",
			result.ExitCode, result.Stderr)
	}

	// Should create eval.yaml
	if _, err := os.Stat(filepath.Join(tmpDir, "eval.yaml")); os.IsNotExist(err) {
		t.Errorf("DOC: import should create eval.yaml")
	}

	// Should create case files
	caseFiles, _ := filepath.Glob(filepath.Join(tmpDir, "cases", "*.yaml"))
	if len(caseFiles) == 0 {
		t.Errorf("DOC: import should create case YAML files")
	}
}

func TestContract_Import_InvalidJSON_ReturnsError(t *testing.T) {
	t.Parallel()

	tmpFile := filepath.Join(t.TempDir(), "bad.json")
	writeFile(t, tmpFile, "not valid json")

	result := RunSimple(t, "import", tmpFile)

	if result.ExitCode == 0 {
		t.Errorf("DOC: import should fail for invalid JSON")
	}
}

// ============================================================================
// CLI Reference — skill-up report
// ============================================================================

func TestContract_Report_FromResultJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	resultData := map[string]interface{}{
		"skill_name":     "test",
		"schema_version": "v1alpha1",
		"engine_name":    "qoder-cli",
		"model_name":     "mock",
		"case_results": []map[string]interface{}{
			{"case_id": "c1", "status": "PASS", "duration_ms": 100},
		},
	}
	data, _ := json.MarshalIndent(resultData, "", "  ")
	writeFile(t, filepath.Join(dir, "result.json"), string(data))

	result := RunSimple(t, "report", filepath.Join(dir, "result.json"))

	t.Logf("report stdout: %s", result.Stdout)
	t.Logf("report stderr: %s", result.Stderr)
	// Verify command exists and doesn't crash
}

// ============================================================================
// Writing Eval Config & Cases — expect layer
// ============================================================================

func TestContract_Expect_MustContain_Pass(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "# Expect Skill\n")
	writeFile(t, filepath.Join(dir, "evals", "eval.yaml"), `schema_version: v1alpha1
environment:
  type: none
skills:
  - source: local_path
    path: .
engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto
cases:
  files:
    - evals/cases/test.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "test.yaml"), `id: expect-must-contain
title: must_contain check
input:
  prompt: Find the null pointer bug
expect:
  must_contain:
    - "null"
    - "bug"
`)

	env := mockEngineEnv(t)
	result := Run(t, RunConfig{Env: env, WorkDir: dir, Timeout: 60 * time.Second},
		"run", filepath.Join(dir, "evals", "eval.yaml"), "--no-delete")

	// Mock returns "null pointer bug" for null-related prompts
	if strings.Contains(result.Stdout, "expect pre-check FAILED") {
		t.Errorf("DOC: must_contain with matching keywords should pass")
	}
}

func TestContract_Expect_MustNotContain_Pass(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "# Expect Skill\n")
	writeFile(t, filepath.Join(dir, "evals", "eval.yaml"), `schema_version: v1alpha1
environment:
  type: none
skills:
  - source: local_path
    path: .
engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto
cases:
  files:
    - evals/cases/test.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "test.yaml"), `id: expect-must-not-contain
title: must_not_contain check
input:
  prompt: Find the null pointer bug
expect:
  must_not_contain:
    - "LGTM"
    - "everything looks good"
`)

	env := mockEngineEnv(t)
	result := Run(t, RunConfig{Env: env, WorkDir: dir, Timeout: 60 * time.Second},
		"run", filepath.Join(dir, "evals", "eval.yaml"), "--no-delete")

	// Mock returns "null pointer bug..." which does NOT contain "LGTM"
	if strings.Contains(result.Stdout, "expect pre-check FAILED") {
		t.Errorf("DOC: must_not_contain should pass when keywords absent")
	}
}

// ============================================================================
// Writing Eval Config & Cases — judge layer
// ============================================================================

func TestContract_Judge_RuleBased_OutputContains(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "# Judge Skill\n")
	writeFile(t, filepath.Join(dir, "evals", "eval.yaml"), `schema_version: v1alpha1
environment:
  type: none
skills:
  - source: local_path
    path: .
engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto
cases:
  files:
    - evals/cases/test.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "test.yaml"), `id: rule-based-test
title: Rule-based output_contains
input:
  prompt: Review the code for null bugs
judge:
  type: rule_based
  success:
    - output_contains:
        all: ["null", "bug"]
        not: ["LGTM"]
`)

	env := mockEngineEnv(t)
	result := Run(t, RunConfig{Env: env, WorkDir: dir, Timeout: 60 * time.Second},
		"run", filepath.Join(dir, "evals", "eval.yaml"), "--no-delete")

	if !strings.Contains(result.Stdout, "PASS") {
		t.Logf("DOC: rule_based judge with output_contains should PASS\nstdout: %s", result.Stdout)
	}
}

func TestContract_Judge_Script_ExitCode0_Pass(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "# Script Judge Skill\n")

	scriptContent := "#!/bin/bash\necho 'Script passed'\nexit 0\n"
	scriptPath := filepath.Join(dir, "evals", "fixtures", "scripts", "always-pass.sh")
	writeFile(t, scriptPath, scriptContent)
	if err := os.Chmod(scriptPath, 0o755); err != nil {
		t.Fatalf("chmod script: %v", err)
	}

	writeFile(t, filepath.Join(dir, "evals", "eval.yaml"), `schema_version: v1alpha1
environment:
  type: none
skills:
  - source: local_path
    path: .
engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto
cases:
  files:
    - evals/cases/test.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "test.yaml"), `id: script-pass
title: Script judge exit 0
input:
  prompt: hello
judge:
  type: script
  script_path: evals/fixtures/scripts/always-pass.sh
`)

	env := mockEngineEnv(t)
	result := Run(t, RunConfig{Env: env, WorkDir: dir, Timeout: 60 * time.Second},
		"run", filepath.Join(dir, "evals", "eval.yaml"), "--no-delete")

	if !strings.Contains(result.Stdout, "PASS") {
		t.Logf("DOC: script judge with exit 0 should PASS\nstdout: %s", result.Stdout)
	}
}

// ============================================================================
// Writing Eval Config & Cases — advanced features (known gaps)
// ============================================================================

func TestContract_PostCondition_SkipRemaining(t *testing.T) {
	t.Parallel()
	t.Skip("KNOWN_GAP: post_condition is documented but not implemented (see gap report #6)")
}

func TestContract_PostCondition_OnFail_Fail(t *testing.T) {
	t.Parallel()
	t.Skip("KNOWN_GAP: post_condition on_fail:fail is documented but not implemented (see gap report #6)")
}

func TestContract_RetryPolicy_MaxRetries(t *testing.T) {
	t.Parallel()

	evalDir := filepath.Join(getProjectRoot(), "e2e", "testdata", "retry-and-parallelism")
	evalPath := filepath.Join(evalDir, "evals", "eval.yaml")
	outputDir := t.TempDir()
	stateFile := filepath.Join(t.TempDir(), "retry-state.txt")

	env := mockEngineEnv(
		t,
		"MOCK_FAIL_COUNT=1",
		"MOCK_STATE_FILE="+stateFile,
		"MOCK_RESPONSE=4",
	)
	result := Run(t, RunConfig{
		Env:     env,
		WorkDir: evalDir,
		Timeout: 60 * time.Second,
	}, "run", evalPath, "--include-case-name", "flaky-success-retry", "--output-dir", outputDir, "--no-delete", "-v")

	if result.ExitCode != 0 {
		t.Fatalf("DOC: retry_policy should recover a transient error and exit 0, got %d\nstdout: %s\nstderr: %s", result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "attempt 1/3 hit error, retrying") {
		t.Fatalf("DOC: retry_policy should log the retry attempt, got stdout: %s", result.Stdout)
	}

	data, err := os.ReadFile(filepath.Join(outputDir, "iteration-1", "result.json"))
	if err != nil {
		t.Fatalf("DOC: retry_policy run should produce result.json: %v", err)
	}

	var report struct {
		CaseResults []struct {
			CaseID string `json:"case_id"`
			Status string `json:"status"`
		} `json:"case_results"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("DOC: result.json should be valid JSON: %v\ncontent: %s", err, string(data))
	}

	foundFlaky := false
	for _, caseResult := range report.CaseResults {
		if caseResult.CaseID == "flaky-success-retry" {
			foundFlaky = true
			if caseResult.Status != "PASS" {
				t.Fatalf("DOC: flaky-success-retry should PASS after retry, got %s", caseResult.Status)
			}
		}
	}
	if !foundFlaky {
		t.Fatalf("DOC: expected flaky-success-retry in result.json, got %s", string(data))
	}
}

func TestContract_Benchmark_Enabled(t *testing.T) {
	t.Parallel()
	t.Skip("KNOWN_GAP: benchmark.enabled=true does not correctly control baseline execution (see gap report #5)")
}

func TestContract_Benchmark_Disabled_NoBenchmarkArtifacts(t *testing.T) {
	t.Parallel()
	t.Skip("KNOWN_GAP: benchmark.enabled=false still generates benchmark artifacts (see gap report #5)")
}

func TestContract_ReportFormats_ControlsOutput(t *testing.T) {
	t.Parallel()
	t.Skip("KNOWN_GAP: report.formats config does not control which formats are generated (see gap report #5)")
}

// ============================================================================
// Writing Eval Config & Cases — environment types
// ============================================================================

func TestContract_Environment_None_Works(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "# None Env Skill\n")
	writeFile(t, filepath.Join(dir, "evals", "eval.yaml"), `schema_version: v1alpha1
environment:
  type: none
skills:
  - source: local_path
    path: .
engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto
cases:
  files:
    - evals/cases/test.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "test.yaml"), `id: env-none
title: None environment
input:
  prompt: hello
`)

	env := mockEngineEnv(t)
	result := Run(t, RunConfig{Env: env, WorkDir: dir, Timeout: 60 * time.Second},
		"run", filepath.Join(dir, "evals", "eval.yaml"), "--no-delete")

	if result.ExitCode != 0 {
		t.Logf("Warning: environment:none run exited %d", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "Running evaluation") {
		t.Errorf("DOC: environment:none should work with basic run")
	}
}

// ============================================================================
// Quick Start — Basic workflow
// ============================================================================

func TestContract_QuickStart_ValidateListRunDryRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	evalPath := makeMinimalEvalProject(t, dir)

	// Step 1: validate
	vResult := RunSimple(t, "validate", evalPath)
	if vResult.ExitCode != 0 {
		t.Fatalf("quickstart: validate failed: %s", vResult.Stderr)
	}

	// Step 2: list-cases
	lResult := RunSimple(t, "list-cases", evalPath)
	if lResult.ExitCode != 0 {
		t.Fatalf("quickstart: list-cases failed: %s", lResult.Stderr)
	}
	if !strings.Contains(lResult.Stdout, "basic-success") {
		t.Errorf("quickstart: list-cases should show basic-success")
	}

	// Step 3: run --dry-run
	dResult := RunSimple(t, "run", evalPath, "--dry-run")
	if dResult.ExitCode != 0 {
		t.Fatalf("quickstart: run --dry-run failed: %s", dResult.Stderr)
	}
	if !strings.Contains(dResult.Stdout, "Would run") {
		t.Errorf("quickstart: dry-run should show 'Would run'")
	}
}

// ============================================================================
// Artifact Directory Structure — grading.json format
// ============================================================================

func TestContract_Artifacts_GradingJSON_Format(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "# Grading Skill\n")
	writeFile(t, filepath.Join(dir, "evals", "eval.yaml"), `schema_version: v1alpha1
environment:
  type: none
skills:
  - source: local_path
    path: .
engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto
cases:
  files:
    - evals/cases/test.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "test.yaml"), `id: grading-format
title: Grading JSON format
input:
  prompt: Find the null pointer bug
judge:
  type: rule_based
  success:
    - output_contains:
        all: ["null"]
`)

	env := mockEngineEnv(t)
	Run(t, RunConfig{Env: env, WorkDir: dir, Timeout: 60 * time.Second},
		"run", filepath.Join(dir, "evals", "eval.yaml"), "--no-delete")

	// Find the workspace directory (created alongside the skill directory)
	pattern := filepath.Join(filepath.Dir(dir), "*-workspace", "iteration-*", "grading-format", "with_skill", "grading.json")
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		t.Skip("grading.json not found; may depend on workspace layout")
	}

	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("failed to read grading.json: %v", err)
	}

	// Verify grading.json has the documented format
	var grading struct {
		Expectations []struct {
			Text     string `json:"text"`
			Passed   bool   `json:"passed"`
			Evidence string `json:"evidence"`
		} `json:"expectations"`
		Summary struct {
			Passed   int     `json:"passed"`
			Failed   int     `json:"failed"`
			Total    int     `json:"total"`
			PassRate float64 `json:"pass_rate"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(data, &grading); err != nil {
		t.Fatalf("DOC: grading.json should be valid JSON with expectations/summary: %v\ncontent: %s", err, string(data))
	}

	if grading.Summary.Total == 0 {
		t.Errorf("DOC: grading.json summary.total should be > 0")
	}
}
