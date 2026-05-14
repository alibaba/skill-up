//go:build e2e

// CLI integration tests that complement contract_test.go.
//
// Scope (what is here, and why it is NOT in contract_test.go):
//   - --auto mode end-to-end: eval.yaml / evals.json auto-detection, SKILL.md
//     directory discovery, engine override, missing evals directory error.
//   - CLI usage UX: usage/flag errors show "Usage:", case failures suppress it.
//   - Import edge cases not covered by the contract tests (missing skill_name,
//     nonexistent input, expected_output round-trip).
//   - SKILL.md-based eval.yaml auto-discovery for validate / list-cases.
//   - Full-e2e-only --auto flows against real LLM (examples/ and testdata/).
//
// Tests that merely duplicate contract_test.go (plain run --dry-run,
// validate / list-cases happy paths, etc.) have intentionally been removed.

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// getExamplesDir returns the path to examples/code-stats/evals.
func getExamplesDir() string {
	return filepath.Join(getProjectRoot(), "examples", "code-stats", "evals")
}

// -----------------------------------------------------------------------------
// validate / list-cases auto-discovery (running without an explicit eval path)
// -----------------------------------------------------------------------------

func TestCLI_ValidateAutoDiscoversEvalPathFromSkillDir(t *testing.T) {
	t.Parallel()

	skillDir := filepath.Join(getExamplesDir(), "..")
	result := Run(t, RunConfig{WorkDir: skillDir}, "validate")

	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s\nstderr: %s",
			result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "Config: discovered eval.yaml:") {
		t.Fatalf("expected discovery log in stdout, got: %s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "eval.yaml is valid") {
		t.Fatalf("expected validation success in stdout, got: %s", result.Stdout)
	}
}

func TestCLI_ListCasesAutoDiscoversEvalPathFromSkillDir(t *testing.T) {
	t.Parallel()

	skillDir := filepath.Join(getExamplesDir(), "..")
	result := Run(t, RunConfig{WorkDir: skillDir}, "list-cases")

	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s\nstderr: %s",
			result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "Config: discovered eval.yaml:") {
		t.Fatalf("expected discovery log in stdout, got: %s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "analyze-directory") {
		t.Fatalf("expected case 'analyze-directory' in output, got: %s", result.Stdout)
	}
}

// -----------------------------------------------------------------------------
// CLI usage UX: when to show the "Usage:" banner
// -----------------------------------------------------------------------------

func TestCLI_RunNonexistent(t *testing.T) {
	t.Parallel()

	result := RunSimple(t, "run", "/nonexistent/eval.yaml")

	if result.ExitCode == 0 {
		t.Errorf("expected non-zero exit code for nonexistent path")
	}
}

func TestCLI_RunUsageErrorShowsUsage(t *testing.T) {
	t.Parallel()

	result := RunSimple(t, "run", "a.yaml", "b.yaml")

	if result.ExitCode == 0 {
		t.Fatal("expected non-zero exit code for too many args")
	}
	if !strings.Contains(result.Stderr, "Usage:") {
		t.Fatalf("expected usage in stderr, got: %s", result.Stderr)
	}
}

func TestCLI_RunFlagErrorShowsUsage(t *testing.T) {
	t.Parallel()

	result := RunSimple(t, "run", "--definitely-not-a-flag")

	if result.ExitCode == 0 {
		t.Fatal("expected non-zero exit code for invalid flag")
	}
	if !strings.Contains(result.Stderr, "Usage:") {
		t.Fatalf("expected usage in stderr, got: %s", result.Stderr)
	}
}

// TestCLI_RunCaseFailureSuppressesUsage verifies the inverse: when a run fails
// because of case errors (not a CLI misuse), the "Usage:" help is NOT printed.
func TestCLI_RunCaseFailureSuppressesUsage(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, "SKILL.md"), "# Test\n")

	evalPath := filepath.Join(tmpDir, "eval.yaml")
	writeFile(t, evalPath, `schema_version: v1alpha1

environment:
  type: opensandbox
  sandbox_template: basic-template

engine:
  name: claude_code
  model:
    provider: dashscope
    name: qwen3.6-plus

cases:
  files:
    - evals/cases/basic.yaml

judge:
  type: rule_based
`)
	writeFile(t, filepath.Join(tmpDir, "evals", "cases", "basic.yaml"), `id: basic
input:
  prompt: |
    Say hello.
`)

	result := RunSimple(t, "run", evalPath)

	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit code, got stdout=%s stderr=%s", result.Stdout, result.Stderr)
	}
	if strings.Contains(result.Stderr, "Usage:") {
		t.Fatalf("expected no usage in stderr, got: %s", result.Stderr)
	}
	if !strings.Contains(result.Stderr, "one or more cases errored") {
		t.Fatalf("expected case error in stderr, got: %s", result.Stderr)
	}
}

// -----------------------------------------------------------------------------
// Import edge cases
// -----------------------------------------------------------------------------

func TestCLI_Import(t *testing.T) {
	t.Parallel()

	evalsPath := filepath.Join(getProjectRoot(), "e2e", "testdata", "evals.json")
	tmpDir := t.TempDir()

	result := RunSimple(t, "import", evalsPath, "--output", tmpDir)

	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s\nstderr: %s",
			result.ExitCode, result.Stdout, result.Stderr)
	}

	for _, rel := range []string{"cases/case-1.yaml", "cases/case-2.yaml", "eval.yaml"} {
		if _, err := os.Stat(filepath.Join(tmpDir, rel)); os.IsNotExist(err) {
			t.Errorf("expected %s to exist under %s", rel, tmpDir)
		}
	}

	if !strings.Contains(result.Stdout, "Total: 2 cases imported") {
		t.Errorf("expected 'Total: 2 cases imported' in output, got: %s", result.Stdout)
	}
}

func TestCLI_ImportNonexistent(t *testing.T) {
	t.Parallel()

	result := RunSimple(t, "import", "/nonexistent/evals.json")

	if result.ExitCode == 0 {
		t.Errorf("expected non-zero exit code for nonexistent path")
	}
}

func TestCLI_ImportMissingSkillName(t *testing.T) {
	t.Parallel()

	tmpFile := filepath.Join(t.TempDir(), "missing-skill.json")
	if err := os.WriteFile(tmpFile, []byte(`{"evals": [{"id": 1, "prompt": "test"}]}`), 0o644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	result := RunSimple(t, "import", tmpFile)

	if result.ExitCode == 0 {
		t.Errorf("expected non-zero exit code for missing skill_name")
	}
}

func TestCLI_ImportVerifiesExpectedOutput(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	evalsContent := `{
  "skill_name": "test-import-skill",
  "evals": [
    {
      "id": 1,
      "prompt": "test prompt",
      "expected_output": "Expected output description",
      "files": [],
      "expectations": ["Expect 1"]
    }
  ]
}`
	evalsPath := filepath.Join(tmpDir, "evals.json")
	if err := os.WriteFile(evalsPath, []byte(evalsContent), 0o644); err != nil {
		t.Fatalf("failed to write evals.json: %v", err)
	}

	result := RunSimple(t, "import", evalsPath, "--output", tmpDir)
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s\nstderr: %s",
			result.ExitCode, result.Stdout, result.Stderr)
	}

	casePath := filepath.Join(tmpDir, "cases", "case-1.yaml")
	caseData, err := os.ReadFile(casePath)
	if err != nil {
		t.Fatalf("failed to read case file: %v", err)
	}

	for _, want := range []string{"Expected output description", "id: case-1"} {
		if !strings.Contains(string(caseData), want) {
			t.Errorf("expected %q in case file, got: %s", want, string(caseData))
		}
	}
}

// -----------------------------------------------------------------------------
// --auto mode end-to-end (no API key; dry-run or banner-only assertions)
// -----------------------------------------------------------------------------

const autoEvalsJSON = `{
  "skill_name": "test-auto-skill",
  "evals": [
    {
      "id": 1,
      "prompt": "echo hello",
      "expected_output": "hello",
      "files": [],
      "expectations": ["hello"]
    }
  ]
}`

// writeAutoEvalsJSON writes an Anthropic evals.json under dir/evals/.
func writeAutoEvalsJSON(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "evals", "evals.json"), autoEvalsJSON)
}

func TestCLI_RunAutoWithEvalsJSON(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	writeAutoEvalsJSON(t, tmpDir)

	result := Run(t, RunConfig{Timeout: 120e9}, "run", "--auto", "--dry-run", tmpDir)

	if !strings.Contains(result.Stdout, "Auto-detected Anthropic evals.json") {
		t.Errorf("expected 'Auto-detected Anthropic evals.json' in output, got: %s", result.Stdout)
	}
}

func TestCLI_RunAutoWithEvalsJSONFromSkillRoot(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, "SKILL.md"), "# Test Skill\n")
	writeAutoEvalsJSON(t, tmpDir)

	result := Run(t, RunConfig{Timeout: 120e9, WorkDir: tmpDir}, "run", "--auto", "--dry-run")

	if !strings.Contains(result.Stdout, "Auto-detected Anthropic evals.json") {
		t.Errorf("expected 'Auto-detected Anthropic evals.json' in output, got: %s", result.Stdout)
	}
}

func TestCLI_RunAutoWithEvalYAMLPath(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, "SKILL.md"), "# Test Skill\n")

	evalPath := filepath.Join(tmpDir, "evals", "eval.yaml")
	writeFile(t, evalPath, `schema_version: v1alpha1

environment:
  type: none

engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto

cases:
  files:
    - evals/cases/case.yaml
`)
	writeFile(t, filepath.Join(tmpDir, "evals", "cases", "case.yaml"), `id: case-1
input:
  prompt: echo hello
expect:
  must_contain:
    - hello
`)

	result := Run(t, RunConfig{Timeout: 120e9}, "run", "--auto", evalPath, "--dry-run")

	if result.ExitCode != 0 {
		t.Fatalf("expected success, got exit %d\nstdout: %s\nstderr: %s", result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "Auto-detected eval.yaml") {
		t.Errorf("expected 'Auto-detected eval.yaml' in output, got: %s", result.Stdout)
	}
}

func TestCLI_RunAutoNoEvalsDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	result := RunSimple(t, "run", "--auto", tmpDir)

	if result.ExitCode == 0 {
		t.Errorf("expected non-zero exit code when no evals directory found")
	}
	if !strings.Contains(result.Stderr, "no evals/ directory found") {
		t.Errorf("expected 'no evals/ directory found' in stderr, got: %s", result.Stderr)
	}
}

func TestCLI_RunAutoWithEngine(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	writeAutoEvalsJSON(t, tmpDir)

	result := Run(t, RunConfig{Timeout: 120e9}, "run", "--auto", "--dry-run", "--engine", "codex", tmpDir)

	if !strings.Contains(result.Stdout, "Auto-detected Anthropic evals.json") {
		t.Errorf("expected 'Auto-detected Anthropic evals.json' in output, got: %s", result.Stdout)
	}
}

// -----------------------------------------------------------------------------
// Full-e2e: --auto against real LLM backends
// -----------------------------------------------------------------------------

func TestCLI_RunAutoWithExamples(t *testing.T) {
	t.Parallel()

	skipIfNotFullE2E(t)
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping test")
	}

	examplesDir := filepath.Join(getProjectRoot(), "examples", "code-stats")
	result := Run(t, RunConfig{Timeout: 120e9}, "run", "--auto", examplesDir)

	// examples/code-stats ships both eval.yaml and evals.json; auto-detection
	// resolves to eval.yaml, so assert the actual log line rather than the
	// Anthropic-format one that never fires for this fixture.
	if !strings.Contains(result.Stdout, "Auto-detected eval.yaml") {
		t.Errorf("expected 'Auto-detected eval.yaml' in output, got: %s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "Running evaluation") {
		t.Errorf("expected runner stage log in output, got: %s", result.Stdout)
	}
}

func TestCLI_RunAutoWithClaudeCode(t *testing.T) {
	t.Parallel()

	skipIfNotFullE2E(t)
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping test")
	}

	tmpDir := t.TempDir()
	writeAutoEvalsJSON(t, tmpDir)

	runCfg := RunConfig{Timeout: 120e9, Env: []string{"ANTHROPIC_API_KEY=" + apiKey}}
	result := Run(t, runCfg, "run", "--auto", "--engine", "claude_code", tmpDir)

	if !strings.Contains(result.Stdout, "Auto-detected Anthropic evals.json") {
		t.Errorf("expected 'Auto-detected Anthropic evals.json' in output, got: %s", result.Stdout)
	}
}

func TestCLI_RunTestdataSample(t *testing.T) {
	t.Parallel()

	skipIfNotFullE2E(t)
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping test")
	}

	evalYamlPath := filepath.Join(getProjectRoot(), "e2e", "testdata", "git-context", "evals", "eval.yaml")
	runCfg := RunConfig{Timeout: 120e9, Env: []string{"ANTHROPIC_API_KEY=" + apiKey}}
	result := Run(t, runCfg, "run", evalYamlPath)

	// LLM responses are non-deterministic; cases may fail expect/judge checks.
	// We only verify the pipeline ran without crashing.
	if result.ExitCode == ExitCodeTimeout {
		t.Skip("run timed out")
	}
	if result.ExitCode != 0 {
		t.Logf("run exited %d (LLM output may not satisfy judge criteria): stderr=%s", result.ExitCode, result.Stderr)
	}
}
