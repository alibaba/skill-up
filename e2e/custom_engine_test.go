//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// getCustomEngineTestdataDir returns the path to e2e/testdata/custom-engine.
func getCustomEngineTestdataDir() string {
	_, testFile, _, _ := runtime.Caller(0)
	projectRoot := filepath.Dir(filepath.Dir(testFile))
	return filepath.Join(projectRoot, "e2e", "testdata", "custom-engine")
}

// skipIfNoPOSIXShell skips the test on platforms where the agent.sh fixture
// cannot run. agent.sh uses bash with `set -euo pipefail` and is not portable
// to Windows; the test infrastructure for cross-platform fixtures is tracked
// in issue #54.
func skipIfNoPOSIXShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("agent.sh fixture is POSIX-only; see issue #54")
	}
}

// customEngineEnv returns an env slice that points the custom engine's
// ${CUSTOM_AGENT_BIN} placeholder at the fixture's agent.sh, while
// preserving PATH and HOME so the test still resolves system tools.
func customEngineEnv(agentBin string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"CUSTOM_AGENT_BIN=" + agentBin,
	}
}

// TestPipeline_CustomEngine_LocalTransport runs the entire evaluation pipeline
// against a user-defined Custom Engine (transport: local). It verifies that:
//  1. The eval loads and the custom engine block is env-resolved and validated.
//  2. The framework writes the SessionInput JSON to ${input_file}.
//  3. The agent's stdout is parsed back into a SessionResult.
//  4. final_message reaches the report and an expect.must_contain rule passes.
func TestPipeline_CustomEngine_LocalTransport(t *testing.T) {
	skipIfNoPOSIXShell(t)
	t.Parallel()

	dir := getCustomEngineTestdataDir()
	evalPath := filepath.Join(dir, "evals", "eval.yaml")
	agentBin := filepath.Join(dir, "agent.sh")

	outputDir := t.TempDir()
	result := Run(t, RunConfig{
		Env:     customEngineEnv(agentBin),
		WorkDir: dir,
		Timeout: 60 * time.Second,
	}, "run", evalPath, "--no-delete", "--output-dir", outputDir)

	if result.ExitCode != 0 {
		t.Fatalf("custom engine run failed: exit=%d\nstdout:\n%s\nstderr:\n%s",
			result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "Running evaluation") {
		t.Errorf("expected runner stage log in output, got:\n%s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "PASS") {
		t.Errorf("expected at least one PASS case, got:\n%s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "1 passed") {
		t.Errorf("expected the summary line to report 1 passed, got:\n%s", result.Stdout)
	}
}

// TestPipeline_CustomEngine_Validate runs `skill-up validate` against the
// custom engine eval.yaml. This exercises the post-load
// config.ResolveCustomEngineConfig path that validate.go invokes, including
// env-variable resolution of ${CUSTOM_AGENT_BIN}.
func TestPipeline_CustomEngine_Validate(t *testing.T) {
	skipIfNoPOSIXShell(t)
	t.Parallel()

	dir := getCustomEngineTestdataDir()
	evalPath := filepath.Join(dir, "evals", "eval.yaml")
	agentBin := filepath.Join(dir, "agent.sh")

	result := Run(t, RunConfig{
		Env:     customEngineEnv(agentBin),
		WorkDir: dir,
		Timeout: 30 * time.Second,
	}, "validate", evalPath)

	if result.ExitCode != 0 {
		t.Fatalf("validate failed: exit=%d\nstdout:\n%s\nstderr:\n%s",
			result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "is valid") {
		t.Errorf("expected validation success message, got:\n%s", result.Stdout)
	}
}

// TestPipeline_CustomEngine_RejectsMissingEnvVar verifies the load-time env
// resolution: when ${CUSTOM_AGENT_BIN} is unset, `run` must fail with a
// configuration error before exec'ing anything.
func TestPipeline_CustomEngine_RejectsMissingEnvVar(t *testing.T) {
	skipIfNoPOSIXShell(t)
	t.Parallel()

	dir := getCustomEngineTestdataDir()
	evalPath := filepath.Join(dir, "evals", "eval.yaml")

	// Note: CUSTOM_AGENT_BIN intentionally unset.
	result := Run(t, RunConfig{
		Env: []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
		},
		WorkDir: dir,
		Timeout: 30 * time.Second,
	}, "run", evalPath, "--no-delete", "--output-dir", t.TempDir())

	if result.ExitCode == 0 {
		t.Fatalf("expected run to fail with missing env var, got exit 0\nstdout:\n%s", result.Stdout)
	}
	combined := result.Stdout + result.Stderr
	if !strings.Contains(combined, "CUSTOM_AGENT_BIN") {
		t.Errorf("expected the error to name the missing env var, got:\n%s", combined)
	}
}
