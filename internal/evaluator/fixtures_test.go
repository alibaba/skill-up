package evaluator

import (
	"context"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/runtime"
)

func currentBranch(t *testing.T, rt runtime.Runtime) string {
	t.Helper()
	res, err := rt.Exec(context.Background(), "git branch --show-current", runtime.ExecOptions{Cwd: rt.Workspace()})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("git branch --show-current failed: err=%v exit=%d stderr=%s", err, res.ExitCode, res.Stderr)
	}
	return strings.TrimSpace(res.Stdout)
}

func gitContextCase(git *config.GitContext) *config.CaseConfig {
	return &config.CaseConfig{Context: config.Context{Git: git}}
}

func TestGitCheckoutUploader_CreatesBranchWhenMissing(t *testing.T) {
	rt := &mockRuntime{workspace: t.TempDir()}
	caseCfg := gitContextCase(&config.GitContext{Init: true, Checkout: "feature-branch"})

	if err := (&gitInitUploader{}).Upload(context.Background(), rt, caseCfg, "", ""); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := (&gitCheckoutUploader{}).Upload(context.Background(), rt, caseCfg, "", ""); err != nil {
		t.Fatalf("git checkout: %v", err)
	}

	if got := currentBranch(t, rt); got != "feature-branch" {
		t.Fatalf("expected branch feature-branch, got %q", got)
	}
}

func TestGitCheckoutUploader_SwitchesToExistingBranch(t *testing.T) {
	rt := &mockRuntime{workspace: t.TempDir()}
	caseCfg := gitContextCase(&config.GitContext{Init: true, Checkout: "main"})

	if err := (&gitInitUploader{}).Upload(context.Background(), rt, caseCfg, "", ""); err != nil {
		t.Fatalf("git init: %v", err)
	}
	// Create a commit and a second branch so "main" is a real, distinct ref.
	setup := "set -eu\ngit checkout -b main -q\ngit commit -q --allow-empty -m init\ngit checkout -b other -q\n"
	if res, err := rt.Exec(context.Background(), setup, runtime.ExecOptions{Cwd: rt.Workspace()}); err != nil || res.ExitCode != 0 {
		t.Fatalf("setup branches: err=%v exit=%d stderr=%s", err, res.ExitCode, res.Stderr)
	}

	if err := (&gitCheckoutUploader{}).Upload(context.Background(), rt, caseCfg, "", ""); err != nil {
		t.Fatalf("git checkout: %v", err)
	}

	if got := currentBranch(t, rt); got != "main" {
		t.Fatalf("expected branch main, got %q", got)
	}
}

func TestGitCheckoutUploader_FailsForMissingBranchInCommittedRepo(t *testing.T) {
	rt := &mockRuntime{workspace: t.TempDir()}
	caseCfg := gitContextCase(&config.GitContext{Init: true, Checkout: "typo-branch"})

	if err := (&gitInitUploader{}).Upload(context.Background(), rt, caseCfg, "", ""); err != nil {
		t.Fatalf("git init: %v", err)
	}
	// Give the repo a commit so HEAD is born; a missing branch is now a typo,
	// not a fresh repo, and must fail instead of being silently created.
	if res, err := rt.Exec(context.Background(), "git commit -q --allow-empty -m init",
		runtime.ExecOptions{Cwd: rt.Workspace()}); err != nil || res.ExitCode != 0 {
		t.Fatalf("seed commit: err=%v exit=%d stderr=%s", err, res.ExitCode, res.Stderr)
	}

	if err := (&gitCheckoutUploader{}).Upload(context.Background(), rt, caseCfg, "", ""); err == nil {
		t.Fatal("expected error for missing branch in committed repo, got nil")
	}
}

func TestGitCheckoutUploader_NoopWhenUnset(t *testing.T) {
	rt := &mockRuntime{workspace: t.TempDir()}

	for _, caseCfg := range []*config.CaseConfig{
		gitContextCase(nil),
		gitContextCase(&config.GitContext{Init: true}),
	} {
		if err := (&gitCheckoutUploader{}).Upload(context.Background(), rt, caseCfg, "", ""); err != nil {
			t.Fatalf("expected no-op, got error: %v", err)
		}
	}
}

func TestGitCheckoutUploader_RejectsUnsafeBranch(t *testing.T) {
	rt := &mockRuntime{workspace: t.TempDir()}

	for _, branch := range []string{"-x", "bad name", "bad\nname"} {
		caseCfg := gitContextCase(&config.GitContext{Init: true, Checkout: branch})
		if err := (&gitCheckoutUploader{}).Upload(context.Background(), rt, caseCfg, "", ""); err == nil {
			t.Fatalf("expected error for branch %q, got nil", branch)
		}
	}
}
