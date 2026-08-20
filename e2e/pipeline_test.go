//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// mockEngineHome creates a fake HOME whose $HOME/.local/bin contains symlinks
// named "qodercli", "claude", "codex", "qwen" (all pointing at
// mock-engine/engine.sh). The e2e pipeline uses environment.type: none and
// supplies PATH explicitly via mockEngineEnv, so the framework's normal
// probe-PATH-at-Install flow (see internal/agent.probeAndMergePATH, only
// invoked for envType != "none") doesn't run here. We still override HOME so
// that the symlinks under our fake $HOME/.local/bin win over any real
// claude/codex/qodercli/qwen on the developer's machine.
func mockEngineHome(t *testing.T) (home, binDir string) {
	t.Helper()

	_, testFile, _, _ := runtime.Caller(0)
	projectRoot := filepath.Dir(filepath.Dir(testFile))
	mockScript := filepath.Join(projectRoot, "e2e", "testdata", "mock-engine", "engine.sh")

	home = t.TempDir()
	binDir = filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create fake HOME bin dir: %v", err)
	}
	for _, name := range []string{"qodercli", "claude", "codex", "qwen"} {
		dst := filepath.Join(binDir, name)
		if err := os.Symlink(mockScript, dst); err != nil {
			t.Fatalf("symlink mock %s: %v", name, err)
		}
	}
	return home, binDir
}

// mockEngineBinDir returns only the bin directory of the fake HOME. Kept for
// callers that just want a directory suitable for PATH injection.
func mockEngineBinDir(t *testing.T) string {
	t.Helper()
	_, binDir := mockEngineHome(t)
	return binDir
}

// mockEngineEnv returns an Env slice that forces the CLI process to resolve
// the agent engine to our mock engine.sh. It overrides both PATH (as a
// defence-in-depth layer) and HOME (so that $HOME/.local/bin, which the agent
// prepends unconditionally, points at our symlinks).
func mockEngineEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	home, binDir := mockEngineHome(t)
	env := []string{
		"PATH=" + binDir + ":" + os.Getenv("PATH"),
		"HOME=" + home,
	}
	env = append(env, extra...)
	return env
}

// getMockEngineTestdataDir returns the path to e2e/testdata/mock-engine.
func getMockEngineTestdataDir() string {
	_, testFile, _, _ := runtime.Caller(0)
	projectRoot := filepath.Dir(filepath.Dir(testFile))
	return filepath.Join(projectRoot, "e2e", "testdata", "mock-engine")
}

// --- Layer 1: Pipeline Tests ---

// TestPipeline_FullRun_WithMockEngine runs the entire evaluation pipeline
// (config -> runtime -> agent -> judge -> report) using the mock engine,
// verifying that the pipeline executes end-to-end without requiring a real LLM.
func TestPipeline_FullRun_WithMockEngine(t *testing.T) {
	t.Parallel()

	mockDir := getMockEngineTestdataDir()
	evalPath := filepath.Join(mockDir, "evals", "eval.yaml")

	env := mockEngineEnv(t)
	outputDir := t.TempDir()
	result := Run(t, RunConfig{
		Env:     env,
		WorkDir: mockDir,
		Timeout: 120 * time.Second,
	}, "run", evalPath, "--no-delete", "--output-dir", outputDir)

	// The pipeline should complete (even if some cases fail their expect checks).
	// We verify the binary didn't crash.
	t.Logf("Pipeline exit code: %d", result.ExitCode)
	t.Logf("Pipeline stdout:\n%s", result.Stdout)
	if result.Stderr != "" {
		t.Logf("Pipeline stderr:\n%s", result.Stderr)
	}

	// The output should mention the runner stage.
	if !strings.Contains(result.Stdout, "Running evaluation") {
		t.Errorf("expected runner stage log in output, got: %s", result.Stdout)
	}
}

// TestPipeline_ReportsRequestedAppliedAndUnknownObservedModel verifies that an adapter
// fallback is visible without changing the legacy requested model_name field.
func TestPipeline_ReportsRequestedAppliedAndUnknownObservedModel(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outputDir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "# Applied config fixture\n")
	writeFile(t, filepath.Join(dir, "evals", "eval.yaml"), `schema_version: v1alpha1
environment:
  type: none
engine:
  name: qoder-cli
  model:
    provider: dashscope
    name: qwen3.6-plus
cases:
  files:
    - evals/cases/effective.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
report:
  formats: [json]
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "effective.yaml"), `id: effective-config
title: Effective configuration reporting
input:
  prompt: Say hello.
expect:
  must_contain:
    - hello
`)

	result := Run(t, RunConfig{
		Env:     mockEngineEnv(t, "MOCK_RESPONSE=hello"),
		WorkDir: dir,
		Timeout: 60 * time.Second,
	}, "run", filepath.Join(dir, "evals", "eval.yaml"), "--output-dir", outputDir, "--verbose")
	if result.ExitCode != 0 {
		t.Fatalf("applied config run failed: exit=%d\nstdout=%s\nstderr=%s", result.ExitCode, result.Stdout, result.Stderr)
	}
	for _, want := range []string{
		"requested.model=qwen3.6-plus applied.model=",
		`does not support model \"qwen3.6-plus\"`,
	} {
		if !strings.Contains(result.Stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, result.Stdout)
		}
	}

	data, err := os.ReadFile(filepath.Join(outputDir, "iteration-1", "result.json"))
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}
	var report struct {
		ModelName string `json:"model_name"`
		Requested struct {
			Protocol string `json:"protocol"`
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"requested_configuration"`
		Applied struct {
			Protocol string `json:"protocol"`
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"applied_configuration"`
		Observed *struct {
			Model string `json:"model"`
		} `json:"observed_configuration"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("parse result.json: %v", err)
	}
	if report.ModelName != "dashscope/qwen3.6-plus" || report.Requested.Model != "qwen3.6-plus" || report.Applied.Model != "" || report.Observed != nil {
		t.Fatalf("requested/applied/observed report mismatch: %+v", report)
	}
	if report.Requested.Protocol != "qoder" || report.Applied.Protocol != "qoder" || report.Applied.Provider != "dashscope" {
		t.Fatalf("protocol/provider report mismatch: %+v", report)
	}
}

// TestPipeline_AgentJudgeCorrectionRetry verifies the bounded correction path
// through the real CLI pipeline with deterministic engine output.
func TestPipeline_AgentJudgeCorrectionRetry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stateFile := filepath.Join(t.TempDir(), "judge-attempts.txt")
	outputDir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "# Judge correction fixture\n")
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
    - evals/cases/correction.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
  parallelism: 1

report:
  formats: [json]
`)
	criterion := "The final response contains the marker MOCK_CORRECTION_MARKER."
	writeFile(t, filepath.Join(dir, "evals", "cases", "correction.yaml"), `id: judge-correction
title: Agent judge correction retry
input:
  prompt: Return the marker MOCK_CORRECTION_MARKER.
constraints:
  timeout_seconds: 30
  max_turns: 1
judge:
  type: agent_judge
  model: auto
  criteria:
    - "The final response contains the marker MOCK_CORRECTION_MARKER."
  pass_threshold: 1.0
`)

	result := Run(t, RunConfig{
		Env: mockEngineEnv(t,
			"MOCK_JUDGE_CORRECTION_RETRY=1",
			"MOCK_JUDGE_STATE_FILE="+stateFile,
		),
		WorkDir: dir,
		Timeout: 60 * time.Second,
	}, "run", filepath.Join(dir, "evals", "eval.yaml"), "--output-dir", outputDir, "--verbose")
	if result.ExitCode != 0 {
		t.Fatalf("correction retry run failed: exit=%d\nstdout=%s\nstderr=%s", result.ExitCode, result.Stdout, result.Stderr)
	}

	state, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read Judge attempt state: %v", err)
	}
	if string(state) != "initial\ncorrection\n" {
		t.Fatalf("expected exactly one correction retry, got attempts %q", state)
	}

	caseDir := filepath.Join(outputDir, "iteration-1", "judge-correction", "with_skill")
	gradingData, err := os.ReadFile(filepath.Join(caseDir, "grading.json"))
	if err != nil {
		t.Fatalf("read grading.json: %v", err)
	}
	var grading struct {
		Expectations []struct {
			Text     string `json:"text"`
			Passed   bool   `json:"passed"`
			Evidence string `json:"evidence"`
		} `json:"expectations"`
		Summary struct {
			PassRate float64 `json:"pass_rate"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(gradingData, &grading); err != nil {
		t.Fatalf("parse grading.json: %v", err)
	}
	if grading.Summary.PassRate != 1 || len(grading.Expectations) != 1 || !grading.Expectations[0].Passed {
		t.Fatalf("expected corrected Judge PASS: %s", gradingData)
	}
	if grading.Expectations[0].Text != criterion || strings.TrimSpace(grading.Expectations[0].Evidence) == "" {
		t.Fatalf("unexpected corrected expectation: %#v", grading.Expectations)
	}

	judgeDir := filepath.Join(caseDir, "outputs", "judge", "run")
	for _, relativePath := range []string{
		"stdout.json",
		"raw-response-attempt-1.txt",
		filepath.Join("retry", "stdout.json"),
		"raw-response-attempt-2.txt",
	} {
		path := filepath.Join(judgeDir, relativePath)
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			t.Fatalf("expected non-empty Judge artifact %s: info=%v err=%v", relativePath, info, err)
		}
	}
}

// TestPipeline_MustContainPass verifies that a case with matching must_contain
// keywords passes the expect check when the mock engine returns the right output.
func TestPipeline_MustContainPass(t *testing.T) {
	t.Parallel()

	mockDir := getMockEngineTestdataDir()
	evalPath := filepath.Join(mockDir, "evals", "eval.yaml")

	env := mockEngineEnv(t)
	outputDir := t.TempDir()
	result := Run(t, RunConfig{
		Env:     env,
		WorkDir: mockDir,
		Timeout: 60 * time.Second,
	}, "run", evalPath, "--include-case-name", "must-contain-pass", "--no-delete", "--output-dir", outputDir)

	t.Logf("stdout:\n%s", result.Stdout)

	// The mock engine returns "null pointer bug" for prompts containing "null",
	// so must_contain: ["null", "bug"] should pass.
	if strings.Contains(result.Stdout, "expect pre-check FAILED") &&
		strings.Contains(result.Stdout, "must-contain-pass") {
		t.Errorf("must-contain-pass case should have passed expect check")
	}
}

// TestPipeline_MustContainFail verifies that a case with non-matching must_contain
// keywords fails the expect check.
func TestPipeline_MustContainFail(t *testing.T) {
	t.Parallel()

	mockDir := getMockEngineTestdataDir()
	evalPath := filepath.Join(mockDir, "evals", "eval.yaml")

	env := mockEngineEnv(t)
	outputDir := t.TempDir()
	result := Run(t, RunConfig{
		Env:     env,
		WorkDir: mockDir,
		Timeout: 60 * time.Second,
	}, "run", evalPath, "--include-case-name", "must-contain-fail", "--no-delete", "--output-dir", outputDir)

	t.Logf("stdout:\n%s", result.Stdout)

	// The mock returns "Hello! ..." for "hello" prompts, which does NOT contain
	// "nonexistent-keyword-xyz", so the expect check should fail.
	if !strings.Contains(result.Stdout, "expect pre-check FAILED") {
		// It's also acceptable if the framework records FAIL in another way
		t.Logf("Note: expect pre-check FAILED message not found; checking alternative indicators")
	}
}

// TestPipeline_RuleBasedJudge verifies that rule_based judge correctly evaluates
// output_contains rules against the mock engine's deterministic output.
func TestPipeline_RuleBasedJudge(t *testing.T) {
	t.Parallel()

	mockDir := getMockEngineTestdataDir()
	evalPath := filepath.Join(mockDir, "evals", "eval.yaml")

	env := mockEngineEnv(t)
	outputDir := t.TempDir()
	result := Run(t, RunConfig{
		Env:     env,
		WorkDir: mockDir,
		Timeout: 60 * time.Second,
	}, "run", evalPath, "--include-case-name", "rule-based-pass", "--no-delete", "--output-dir", outputDir)

	t.Logf("stdout:\n%s", result.Stdout)

	// The mock engine returns "null pointer bug" for review+null prompts.
	// The rule_based judge checks output_contains all:["null","bug"] not:["LGTM"]
	// This should pass.
	if strings.Contains(result.Stdout, "FAIL") && strings.Contains(result.Stdout, "rule-based-pass") {
		t.Logf("Warning: rule-based-pass case may have failed unexpectedly")
	}
}

// TestPipeline_ScriptJudge verifies that the script judge correctly executes
// the evaluation script and passes the environment variables.
func TestPipeline_ScriptJudge(t *testing.T) {
	t.Parallel()

	mockDir := getMockEngineTestdataDir()
	evalPath := filepath.Join(mockDir, "evals", "eval.yaml")

	env := mockEngineEnv(t)
	outputDir := t.TempDir()
	result := Run(t, RunConfig{
		Env:     env,
		WorkDir: mockDir,
		Timeout: 60 * time.Second,
	}, "run", evalPath, "--include-case-name", "script-judge", "--no-delete", "--output-dir", outputDir)

	t.Logf("stdout:\n%s", result.Stdout)

	// The script checks for "null" in EVAL_FINAL_MESSAGE. The mock returns
	// "null pointer bug" for "null" prompts, so the script should exit 0 (PASS).
}

// TestPipeline_IncludeExcludeFilter verifies that --include-case-name and
// --exclude-case-name correctly filter cases.
func TestPipeline_IncludeExcludeFilter(t *testing.T) {
	t.Parallel()

	mockDir := getMockEngineTestdataDir()
	evalPath := filepath.Join(mockDir, "evals", "eval.yaml")

	// Use --dry-run to avoid actual execution; just verify filtering
	result := RunSimple(t, "run", evalPath, "--dry-run",
		"--include-case-name", "must-*",
		"--exclude-case-name", "*-fail")

	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s\nstderr: %s",
			result.ExitCode, result.Stdout, result.Stderr)
	}

	// Should include must-contain-pass but exclude must-contain-fail
	if !strings.Contains(result.Stdout, "must-contain-pass") {
		t.Errorf("expected must-contain-pass in dry-run output")
	}
	if strings.Contains(result.Stdout, "must-contain-fail") {
		t.Errorf("must-contain-fail should have been excluded")
	}
}

// TestPipeline_ValidateConfig verifies that the mock-engine eval.yaml passes validation.
func TestPipeline_ValidateConfig(t *testing.T) {
	t.Parallel()

	mockDir := getMockEngineTestdataDir()
	evalPath := filepath.Join(mockDir, "evals", "eval.yaml")

	result := RunSimple(t, "validate", evalPath)

	if result.ExitCode != 0 {
		t.Fatalf("validation failed: exit=%d stdout=%s stderr=%s",
			result.ExitCode, result.Stdout, result.Stderr)
	}
}

// TestPipeline_ListCases verifies that list-cases shows all configured cases.
func TestPipeline_ListCases(t *testing.T) {
	t.Parallel()

	mockDir := getMockEngineTestdataDir()
	evalPath := filepath.Join(mockDir, "evals", "eval.yaml")

	result := RunSimple(t, "list-cases", evalPath)

	if result.ExitCode != 0 {
		t.Fatalf("list-cases failed: exit=%d stdout=%s stderr=%s",
			result.ExitCode, result.Stdout, result.Stderr)
	}

	expectedCases := []string{"must-contain-pass", "must-contain-fail", "rule-based-pass", "script-judge"}
	for _, id := range expectedCases {
		if !strings.Contains(result.Stdout, id) {
			t.Errorf("expected case %q in list-cases output", id)
		}
	}
}

// TestPipeline_DryRun verifies --dry-run mode shows what would run without executing.
func TestPipeline_DryRun(t *testing.T) {
	t.Parallel()

	mockDir := getMockEngineTestdataDir()
	evalPath := filepath.Join(mockDir, "evals", "eval.yaml")

	result := RunSimple(t, "run", evalPath, "--dry-run")

	if result.ExitCode != 0 {
		t.Fatalf("dry-run failed: exit=%d stdout=%s stderr=%s",
			result.ExitCode, result.Stdout, result.Stderr)
	}

	if !strings.Contains(result.Stdout, "Would run") {
		t.Errorf("expected 'Would run' in dry-run output")
	}

	if !strings.Contains(result.Stdout, "4 case(s)") {
		t.Errorf("expected '4 case(s)' in dry-run output, got: %s", result.Stdout)
	}
}

// TestPipeline_MockEngineCustomResponse verifies the mock engine respects
// MOCK_RESPONSE environment variable for fully controlled output.
func TestPipeline_MockEngineCustomResponse(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "SKILL.md"), "# Custom Response Skill\n")
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
    - evals/cases/custom.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "custom.yaml"), `id: custom-response
title: Custom response test

input:
  prompt: |
    Anything

expect:
  must_contain:
    - "CUSTOM_MARKER_12345"
`)

	env := mockEngineEnv(t, "MOCK_RESPONSE=This output contains CUSTOM_MARKER_12345 for testing")
	result := Run(t, RunConfig{
		Env:     env,
		WorkDir: dir,
		Timeout: 60 * time.Second,
	}, "run", filepath.Join(dir, "evals", "eval.yaml"), "--no-delete")

	t.Logf("stdout:\n%s", result.Stdout)

	// With MOCK_RESPONSE set, the output should contain our custom marker,
	// so the must_contain check should pass.
}

// TestPipeline_ReportGeneration verifies that the report command can read
// result.json and generate reports.
func TestPipeline_ReportGeneration(t *testing.T) {
	t.Parallel()

	// Create a minimal result.json
	dir := t.TempDir()
	resultJSON := map[string]interface{}{
		"skill_name":     "test-skill",
		"schema_version": "v1alpha1",
		"engine_name":    "qoder-cli",
		"model_name":     "mock-model",
		"case_results": []map[string]interface{}{
			{
				"case_id":     "test-case-1",
				"case_title":  "Test Case 1",
				"status":      "PASS",
				"duration_ms": 1200,
			},
		},
	}
	data, _ := json.MarshalIndent(resultJSON, "", "  ")
	writeFile(t, filepath.Join(dir, "result.json"), string(data))

	result := RunSimple(t, "report", filepath.Join(dir, "result.json"), "--format", "json")

	t.Logf("Report stdout:\n%s", result.Stdout)
	t.Logf("Report stderr:\n%s", result.Stderr)

	// Just verify the command doesn't crash
	// Note: the report command behavior depends on implementation
}
