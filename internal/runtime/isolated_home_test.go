package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolatedHomeLifecycle(t *testing.T) {
	for _, external := range []bool{false, true} {
		for _, remove := range []bool{false, true} {
			t.Run(fmt.Sprintf("external=%t/delete=%t", external, remove), func(t *testing.T) {
				cfg := Config{Type: "none", IsolateHome: true, Delete: remove}
				if external {
					cfg.WorkspaceDir = t.TempDir()
				}
				checkIsolatedHomeLifecycle(t, cfg)
			})
		}
	}
}

func checkIsolatedHomeLifecycle(t *testing.T, cfg Config) {
	t.Helper()
	external, remove := cfg.WorkspaceDir != "", cfg.Delete
	rt := &NoneRuntime{cfg: cfg}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	home, workspace := rt.isolatedHome, rt.Workspace()
	t.Cleanup(func() {
		_ = os.RemoveAll(home)
		if !external {
			_ = os.RemoveAll(workspace)
		}
	})
	if external {
		if workspace != cfg.WorkspaceDir || rt.OwnsWorkspace() {
			t.Fatalf("external workspace was replaced or claimed: %q", workspace)
		}
	} else if workspace != filepath.Join(home, "workspace") || !rt.OwnsWorkspace() {
		t.Fatalf("workspace %q must be owned and inside home %q", workspace, home)
	}
	checkIsolatedHomeWorkspaceArtifact(t, rt)
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	_, homeErr := os.Stat(home)
	_, workspaceErr := os.Stat(workspace)
	if os.IsNotExist(homeErr) != remove {
		t.Fatalf("home after Close: %v", homeErr)
	}
	if os.IsNotExist(workspaceErr) != (remove && !external) {
		t.Fatalf("workspace after Close: %v", workspaceErr)
	}
	if external {
		if data, err := os.ReadFile(filepath.Join(workspace, "result.txt")); err != nil || string(data) != "answer" {
			t.Fatalf("external workspace contents changed: %q, %v", data, err)
		}
	}
}

func checkIsolatedHomeWorkspaceArtifact(t *testing.T, rt *NoneRuntime) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(rt.isolatedHome, "auth.json"), []byte("test-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rt.isolatedHome, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rt.Workspace(), "result.txt"), []byte("answer"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifactDir := t.TempDir()
	if err := rt.DownloadDir(t.Context(), ".", artifactDir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(artifactDir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "result.txt" {
		t.Fatalf("workspace artifact contains agent state: %v, %v", entries, err)
	}
}

func TestIsolatedHomeEnvironmentCannotBeOverridden(t *testing.T) {
	rt := &NoneRuntime{cfg: Config{Type: "none", IsolateHome: true, Delete: true, Env: map[string]string{"HOME": "/personal", "CODEX_HOME": "/personal/codex"}}}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	rt.MergeEnv(map[string]string{"CLAUDE_CONFIG_DIR": "/personal/claude"})
	env := mergeEnv(os.Environ(), rt.cfg.Env, map[string]string{"home": "/override", "XDG_CONFIG_HOME": "/override", "BASH_ENV": "/personal/startup", "OPENAI_API_KEY": "test-key"}, rt.Shell().GOOS)
	got := make(map[string]string)
	for _, entry := range rt.isolatedEnvironment(env) {
		key, value, _ := strings.Cut(entry, "=")
		got[key] = value
	}
	for _, key := range []string{"HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		if !strings.HasPrefix(got[key], rt.isolatedHome) {
			t.Errorf("%s = %q, not under %q", key, got[key], rt.isolatedHome)
		}
	}
	if _, ok := got["home"]; ok {
		t.Error("lowercase home survived")
	}
	if got["BASH_ENV"] != "" || got["OPENAI_API_KEY"] != "test-key" {
		t.Fatalf("startup and credential handling incorrect")
	}

	if !rt.Shell().IsBash() && rt.Shell().GOOS == "windows" {
		return
	}
	result, err := rt.Exec(context.Background(), `printf '%s' "$HOME|$CODEX_HOME|$CLAUDE_CONFIG_DIR"`, ExecOptions{Env: map[string]string{"HOME": "/override"}})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("exec: %+v, %v", result, err)
	}
	want := strings.Join([]string{rt.isolatedHome, rt.isolatedHome, filepath.Join(rt.isolatedHome, ".claude")}, "|")
	if result.Stdout != want {
		t.Fatalf("env = %q, want %q", result.Stdout, want)
	}
}

func TestIsolatedHomesArePerRuntime(t *testing.T) {
	workspace := t.TempDir()
	var homes []string
	for range 2 {
		rt := &NoneRuntime{cfg: Config{Type: "none", WorkspaceDir: workspace, IsolateHome: true, Delete: true}}
		if err := rt.Create(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rt.Close() })
		homes = append(homes, rt.isolatedHome)
	}
	if homes[0] == homes[1] {
		t.Fatal("runtimes share a home")
	}
}
