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
				if home == workspace || filepath.Dir(home) != filepath.Dir(workspace) {
					t.Fatalf("home %q must be beside workspace %q", home, workspace)
				}
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
			})
		}
	}
}

func TestIsolatedHomeEnvironmentCannotBeOverridden(t *testing.T) {
	rt := &NoneRuntime{cfg: Config{Type: "none", IsolateHome: true, Delete: true, Env: map[string]string{"HOME": "/personal", "CODEX_HOME": "/personal/codex"}}}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	rt.MergeEnv(map[string]string{"CLAUDE_CONFIG_DIR": "/personal/claude"})
	got := envMapFromList(rt.isolatedEnvironment(mergeEnv(rt.cfg.Env, map[string]string{"home": "/override", "XDG_CONFIG_HOME": "/override", "BASH_ENV": "/personal/startup", "OPENAI_API_KEY": "test-key"})))
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
	want := strings.Join([]string{rt.isolatedHome, filepath.Join(rt.isolatedHome, ".codex"), filepath.Join(rt.isolatedHome, ".claude")}, "|")
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
