//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var binaryPath string

// ExitCodeTimeout is the exit code used when a command times out.
// This is not a standard process exit code (0-255) but a sentinel value
// to distinguish timeout from other failures.
const ExitCodeTimeout = -1

type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type RunConfig struct {
	Env       []string
	ConfigDir string
	WorkDir   string
	Timeout   time.Duration
	Stdin     string
}

func Run(t *testing.T, cfg RunConfig, args ...string) RunResult {
	t.Helper()

	configDir := cfg.ConfigDir
	if configDir == "" {
		configDir = t.TempDir()
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, args...)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}

	env := append(os.Environ(),
		"SKILL_UP_CONFIG_DIR="+configDir,
		"NO_COLOR=1",
	)
	env = append(env, cfg.Env...)
	cmd.Env = env

	if cfg.Stdin != "" {
		cmd.Stdin = strings.NewReader(cfg.Stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() != nil {
			t.Logf("command timed out: %v", args)
			exitCode = ExitCodeTimeout
		} else {
			t.Fatalf("running %v: %v", args, err)
		}
	}

	return RunResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}
}

func RunSimple(t *testing.T, args ...string) RunResult {
	t.Helper()
	return Run(t, RunConfig{}, args...)
}

func getProjectRoot() string {
	_, testFile, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(testFile))
}

// skipIfClaudeUnavailable skips the test if the claude CLI is not installed or
// its native binary is missing (e.g. npm postinstall did not run).
func skipIfClaudeUnavailable(t *testing.T) {
	t.Helper()
	path, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not found in PATH, skipping")
	}
	out, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		t.Skipf("claude not functional (native binary may be missing): %s", strings.TrimSpace(string(out)))
	}
}

// skipIfNotFullE2E skips the test when not running in full e2e mode.
// Tests that call real LLM APIs are slow (100-300s each) and should only
// run in full mode to keep the default CI pipeline fast.
func skipIfNotFullE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("SKILL_UP_FULL_E2E") == "" {
		t.Skip("skipping slow LLM-dependent test in quick e2e mode (set SKILL_UP_FULL_E2E=1 to enable)")
	}
}

// projectRootFromTest returns the repository root (one level above e2e/).
// Kept alongside getProjectRoot for readability at call sites.
func projectRootFromTest() string {
	return getProjectRoot()
}

// writeFile creates path and all its parent directories, writing content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create dir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// makeMinimalEvalProject writes a minimal but valid eval project (SKILL.md +
// evals/eval.yaml + two case files) into dir and returns the eval.yaml path.
// Shared across contract and acceptance-style tests.
func makeMinimalEvalProject(t *testing.T, dir string) string {
	t.Helper()

	writeFile(t, filepath.Join(dir, "SKILL.md"), "# Test Skill\n")
	writeFile(t, filepath.Join(dir, "evals", "eval.yaml"), `schema_version: v1alpha1

environment:
  type: none

skills:
  - source: local_path
    path: .

engine:
  name: claude_code
  model:
    provider: dashscope
    name: qwen3.6-plus

cases:
  files:
    - evals/cases/basic-success.yaml
    - evals/cases/edge-case.yaml

judge:
  type: rule_based
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "basic-success.yaml"), `id: basic-success
title: Basic success case

input:
  prompt: |
    Say hello.
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "edge-case.yaml"), `id: edge-case
title: Edge case

input:
  prompt: |
    Say goodbye.
`)

	return filepath.Join(dir, "evals", "eval.yaml")
}
