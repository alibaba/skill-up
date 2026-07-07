//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMCP_SandboxRealAgents(t *testing.T) {
	skipIfSandboxMCPUnavailable(t)

	evalPath := filepath.Join(getProjectRoot(), "e2e", "testdata", "sandbox-mcp", "evals", "eval.yaml")
	for _, tc := range []struct {
		name   string
		engine string
		model  string
	}{
		{name: "claude_code", engine: "claude_code", model: "dashscope/qwen3.6-plus"},
		{name: "qodercli", engine: "qodercli", model: "qoder/auto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.engine == "claude_code" {
				skipIfClaudeUnavailable(t)
				skipClaudeCodeIfIncompatibleEndpoint(t)
			}
			if tc.engine == "qodercli" {
				skipIfQoderCLIUnavailable(t)
			}

			runEvalWithRetries(t, mcpEvalRun{
				evalPath:   evalPath,
				engine:     tc.engine,
				model:      tc.model,
				outputRoot: e2eOutputDir(t),
				outputName: "sandbox-mcp-" + tc.name,
			})
		})
	}
}

func TestMCP_StdioMarkerAgents(t *testing.T) {
	skipIfNotFullE2E(t)

	evalPath := filepath.Join(getProjectRoot(), "e2e", "testdata", "mcp-stdio-marker", "evals", "eval.yaml")
	for _, tc := range []struct {
		name   string
		engine string
		model  string
	}{
		{name: "claude_code", engine: "claude_code", model: "dashscope/qwen3.6-plus"},
		{name: "qodercli", engine: "qodercli", model: "qoder/auto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.engine == "claude_code" {
				skipIfClaudeUnavailable(t)
				skipClaudeCodeIfIncompatibleEndpoint(t)
			}
			if tc.engine == "qodercli" {
				skipIfQoderCLIUnavailable(t)
			}

			runEvalWithRetries(t, mcpEvalRun{
				evalPath:   evalPath,
				engine:     tc.engine,
				model:      tc.model,
				outputRoot: e2eOutputDir(t),
				outputName: "mcp-stdio-marker-" + tc.name,
				attempts:   2,
				timeout:    240 * time.Second,
			})
		})
	}
}

func TestMCP_MockedMarkerAgents(t *testing.T) {
	skipIfNotFullE2E(t)

	evalPath := filepath.Join(getProjectRoot(), "e2e", "testdata", "mcp-mocked-marker", "evals", "eval.yaml")
	for _, tc := range []struct {
		name   string
		engine string
		model  string
	}{
		{name: "claude_code", engine: "claude_code", model: "anthropic/qwen3.6-plus"},
		{name: "qodercli", engine: "qodercli", model: "qoder/auto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.engine == "claude_code" {
				skipIfClaudeUnavailable(t)
				skipClaudeCodeIfIncompatibleEndpoint(t)
			}
			if tc.engine == "qodercli" {
				skipIfQoderCLIUnavailable(t)
			}

			runEvalWithRetries(t, mcpEvalRun{
				evalPath:   evalPath,
				engine:     tc.engine,
				model:      tc.model,
				outputRoot: e2eOutputDir(t),
				outputName: "mcp-mocked-marker-" + tc.name,
				attempts:   2,
				timeout:    240 * time.Second,
			})
		})
	}
}

// TestMCP_CaseMockOverrides drives two cases that share the same mocked MCP
// server name (project-mgmt) and tool (get_project) but override the fixture
// per case (SUP-0003). The eval-level default fixture returns UNKNOWN, while
// the open/closed cases override it to OPEN/CLOSED. This verifies case-level
// mocked overrides route the correct fixture end-to-end.
func TestMCP_CaseMockOverrides(t *testing.T) {
	skipIfNotFullE2E(t)

	evalPath := filepath.Join(getProjectRoot(), "e2e", "testdata", "mcp-case-overrides", "evals", "eval.yaml")
	for _, tc := range []struct {
		name   string
		engine string
		model  string
	}{
		{name: "claude_code", engine: "claude_code", model: "anthropic/qwen3.6-plus"},
		{name: "qodercli", engine: "qodercli", model: "qoder/auto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.engine == "claude_code" {
				skipIfClaudeUnavailable(t)
				skipClaudeCodeIfIncompatibleEndpoint(t)
			}
			if tc.engine == "qodercli" {
				skipIfQoderCLIUnavailable(t)
			}

			runEvalWithRetries(t, mcpEvalRun{
				evalPath:   evalPath,
				engine:     tc.engine,
				model:      tc.model,
				outputRoot: e2eOutputDir(t),
				outputName: "mcp-case-overrides-" + tc.name,
				attempts:   2,
				timeout:    240 * time.Second,
			})
		})
	}
}

func skipIfSandboxMCPUnavailable(t *testing.T) {
	t.Helper()
	var missing []string
	for _, name := range []string{"OPENSANDBOX_API_KEY", "SANDBOX_MCP_TOKEN"} {
		if os.Getenv(name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return
	}
	msg := strings.Join(missing, ", ") + " not set"
	if os.Getenv("IS_SANDBOX") == "1" {
		t.Fatal(msg)
	}
	t.Skip(msg)
}

// skipClaudeCodeIfIncompatibleEndpoint skips when the configured
// ANTHROPIC_BASE_URL is known to be incompatible with claude-code's protocol
// (for example, OpenAI-compatible endpoints that don't speak the Anthropic
// API surface). Operators set SKILL_UP_E2E_CLAUDE_CODE_INCOMPATIBLE_HOST to
// the substring of ANTHROPIC_BASE_URL that should trigger the skip; the test
// is opt-in by default so external users with a real Anthropic API key always
// run the case.
func skipClaudeCodeIfIncompatibleEndpoint(t *testing.T) {
	t.Helper()
	substr := os.Getenv("SKILL_UP_E2E_CLAUDE_CODE_INCOMPATIBLE_HOST")
	if substr == "" {
		return
	}
	if !strings.Contains(os.Getenv("ANTHROPIC_BASE_URL"), substr) {
		return
	}
	t.Skipf("ANTHROPIC_BASE_URL contains %q; claude-code cannot drive this endpoint with the test's model", substr)
}

func e2eOutputDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("SKILL_UP_E2E_OUTPUT_DIR"); dir != "" {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(getProjectRoot(), dir)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create e2e output dir: %v", err)
		}
		return dir
	}
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create e2e output dir: %v", err)
	}
	return dir
}

type mcpEvalRun struct {
	evalPath   string
	engine     string
	model      string
	outputRoot string
	outputName string
	attempts   int
	timeout    time.Duration
}

func (r mcpEvalRun) outputDir() string {
	return filepath.Join(r.outputRoot, r.outputName)
}

func runEvalWithRetries(t *testing.T, run mcpEvalRun) {
	t.Helper()
	attempts := run.attempts
	if attempts == 0 {
		attempts = 3
	}
	timeout := run.timeout
	if timeout == 0 {
		timeout = 420 * time.Second
	}

	var last RunResult
	for attempt := 1; attempt <= attempts; attempt++ {
		t.Logf("running MCP eval %s with %s (attempt %d/%d)", run.outputName, run.engine, attempt, attempts)
		result := Run(t, RunConfig{Timeout: timeout}, "run", run.evalPath, "--engine", run.engine, "--model", run.model, "--output-dir", run.outputDir())
		writeAttemptLog(t, run.outputRoot, run.outputName, attempt, result)
		if result.ExitCode == 0 {
			return
		}
		last = result
	}

	t.Fatalf("MCP eval %s failed after %d attempts: exit=%d\nstdout:\n%s\nstderr:\n%s",
		run.outputName, attempts, last.ExitCode, last.Stdout, last.Stderr)
}

func writeAttemptLog(t *testing.T, outputRoot, outputName string, attempt int, result RunResult) {
	t.Helper()
	path := filepath.Join(outputRoot, fmt.Sprintf("%s-attempt-%d.log", outputName, attempt))
	var b strings.Builder
	fmt.Fprintf(&b, "exit_code: %d\n", result.ExitCode)
	b.WriteString("===== stdout =====\n")
	b.WriteString(result.Stdout)
	if !strings.HasSuffix(result.Stdout, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("===== stderr =====\n")
	b.WriteString(result.Stderr)
	if !strings.HasSuffix(result.Stderr, "\n") {
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write attempt log: %v", err)
	}
}
