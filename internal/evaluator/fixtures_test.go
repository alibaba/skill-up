package evaluator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

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

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
}

func gitContextCase(git *config.GitContext) *config.CaseConfig {
	return &config.CaseConfig{Context: config.Context{Git: git}}
}

func TestGitCheckoutUploader_CreatesBranchWhenMissing(t *testing.T) {
	requireGit(t)

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
	requireGit(t)

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
	requireGit(t)

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

func TestGitCheckoutUploader_ErrorPathDoesNotEvaluateBranch(t *testing.T) {
	requireGit(t)

	rt := &mockRuntime{workspace: t.TempDir()}
	// A branch name carrying a command substitution but no whitespace/control
	// chars, so it passes validateGitBranch and reaches the shell script.
	caseCfg := gitContextCase(&config.GitContext{Init: true, Checkout: "$(>pwned)"})

	if err := (&gitInitUploader{}).Upload(context.Background(), rt, caseCfg, "", ""); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if res, err := rt.Exec(context.Background(), "git commit -q --allow-empty -m init",
		runtime.ExecOptions{Cwd: rt.Workspace()}); err != nil || res.ExitCode != 0 {
		t.Fatalf("seed commit: err=%v exit=%d stderr=%s", err, res.ExitCode, res.Stderr)
	}

	if err := (&gitCheckoutUploader{}).Upload(context.Background(), rt, caseCfg, "", ""); err == nil {
		t.Fatal("expected error for invalid branch, got nil")
	}
	if _, err := os.Stat(filepath.Join(rt.Workspace(), "pwned")); !os.IsNotExist(err) {
		t.Fatalf("branch name was evaluated by the shell: stat err=%v", err)
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

type applyDiffTestRuntime struct {
	runtime.Runtime

	upload func(context.Context, string, string) error
	exec   func(context.Context, string, runtime.ExecOptions) (runtime.ExecResult, error)
}

func (r *applyDiffTestRuntime) UploadFile(ctx context.Context, source, target string) error {
	return r.upload(ctx, source, target)
}

func (r *applyDiffTestRuntime) Exec(ctx context.Context, command string, opts runtime.ExecOptions) (runtime.ExecResult, error) {
	if r.exec != nil {
		return r.exec(ctx, command, opts)
	}
	return r.Runtime.Exec(ctx, command, opts)
}

func newApplyDiffRuntime(t *testing.T) runtime.Runtime {
	t.Helper()
	requireGit(t)
	workspace := filepath.Join(t.TempDir(), "workspace with ' quote")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", WorkspaceDir: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if err := (&gitInitUploader{}).Upload(t.Context(), rt, gitContextCase(&config.GitContext{Init: true}), "", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "value.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return rt
}

func writeApplyDiff(t *testing.T, dir, name, value string) {
	t.Helper()
	patch := fmt.Sprintf("diff --git a/value.txt b/value.txt\n--- a/value.txt\n+++ b/value.txt\n@@ -1 +1 @@\n-before\n+%s\n", value)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(patch), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestApplyDiffUploader_ConcurrentSameBasename(t *testing.T) {
	t.Parallel()
	name := "same-" + uuid.NewString() + ".patch"
	t.Cleanup(func() { _ = os.Remove(filepath.Join(os.TempDir(), name)) })
	var uploaded sync.WaitGroup
	uploaded.Add(2)
	results := make(chan error, 2)
	runtimes := []runtime.Runtime{newApplyDiffRuntime(t), newApplyDiffRuntime(t)}
	for i, base := range runtimes {
		skillDir := t.TempDir()
		writeApplyDiff(t, skillDir, name, fmt.Sprintf("case-%d", i))
		rt := &applyDiffTestRuntime{Runtime: base}
		rt.upload = func(ctx context.Context, source, target string) error {
			err := base.UploadFile(ctx, source, target)
			uploaded.Done()
			uploaded.Wait() // Both uploads finish before either git apply reads its patch.
			return err
		}
		go func() {
			results <- (&applyDiffUploader{}).Upload(t.Context(), rt, gitContextCase(&config.GitContext{ApplyDiff: name}), skillDir, skillDir)
		}()
	}
	for range runtimes {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	for i, rt := range runtimes {
		got, err := os.ReadFile(filepath.Join(rt.Workspace(), "value.txt"))
		if err != nil || string(got) != fmt.Sprintf("case-%d\n", i) {
			t.Errorf("case %d: content = %q, err = %v", i, got, err)
		}
	}
}

func TestApplyDiffUploader_CleansUp(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"success", "invalid patch", "exec error", "cancelled", "partial upload"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			testApplyDiffCleanup(t, mode)
		})
	}
}

func testApplyDiffCleanup(t *testing.T, mode string) {
	t.Helper()
	base := newApplyDiffRuntime(t)
	skillDir := t.TempDir()
	name := "patch ' ;echo pwned-" + uuid.NewString() + ".diff"
	writeApplyDiff(t, skillDir, name, "after")
	if mode == "invalid patch" {
		if err := os.WriteFile(filepath.Join(skillDir, name), []byte("not a patch\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sentinel := errors.New("transfer or execution failed")
	var targetPath string
	rt := &applyDiffTestRuntime{Runtime: base}
	rt.upload = func(ctx context.Context, source, target string) error {
		targetPath = target
		if !filepath.IsAbs(targetPath) {
			targetPath = filepath.Join(base.Workspace(), targetPath)
		}
		t.Cleanup(func() { _ = os.Remove(targetPath) })
		if err := base.UploadFile(ctx, source, target); err != nil {
			return err
		}
		if mode == "partial upload" {
			return sentinel
		}
		return nil
	}
	rt.exec = func(ctx context.Context, command string, opts runtime.ExecOptions) (runtime.ExecResult, error) {
		if strings.HasPrefix(command, "git apply ") {
			switch mode {
			case "exec error":
				return runtime.ExecResult{}, sentinel
			case "cancelled":
				cancel()
				return runtime.ExecResult{}, ctx.Err()
			}
		}
		return base.Exec(ctx, command, opts)
	}
	err := (&applyDiffUploader{}).Upload(ctx, rt, gitContextCase(&config.GitContext{ApplyDiff: name}), skillDir, skillDir)
	if (err != nil) != (mode != "success") {
		t.Fatalf("Upload error = %v for %s", err, mode)
	}
	wantErr := map[string]error{"exec error": sentinel, "partial upload": sentinel, "cancelled": context.Canceled}[mode]
	if wantErr != nil && !errors.Is(err, wantErr) {
		t.Errorf("original error lost: got %v, want %v", err, wantErr)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Errorf("uploaded patch remains: %s (stat error %v)", targetPath, err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, name)); err != nil {
		t.Errorf("source patch was removed: %v", err)
	}
}
