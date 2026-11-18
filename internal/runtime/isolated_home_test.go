package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentHomeLifecycle(t *testing.T) {
	for _, mode := range []string{"", "inherited", "temporary"} {
		for _, external := range []bool{false, true} {
			for _, remove := range []bool{false, true} {
				t.Run(fmt.Sprintf("mode=%s/external=%t/delete=%t", mode, external, remove), func(t *testing.T) {
					cfg := Config{Type: "none", AgentHome: mode, Delete: remove}
					if external {
						cfg.WorkspaceDir = t.TempDir()
					}
					checkAgentHomeLifecycle(t, cfg)
				})
			}
		}
	}
}

func checkAgentHomeLifecycle(t *testing.T, cfg Config) {
	t.Helper()
	external, remove := cfg.WorkspaceDir != "", cfg.Delete
	rt := &NoneRuntime{cfg: cfg}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	runDir, workspace := rt.runDir, rt.Workspace()
	t.Cleanup(func() {
		_ = os.RemoveAll(runDir)
		if !external {
			_ = os.RemoveAll(workspace)
		}
	})
	checkAgentHomeLayout(t, rt)
	checkIsolatedHomeWorkspaceArtifact(t, rt)
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	_, workspaceErr := os.Stat(workspace)
	if runDir != "" {
		if _, err := os.Stat(runDir); os.IsNotExist(err) != remove {
			t.Fatalf("run directory after Close: %v", err)
		}
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

func checkAgentHomeLayout(t *testing.T, rt *NoneRuntime) {
	t.Helper()
	cfg := rt.cfg
	if cfg.WorkspaceDir != "" {
		if rt.Workspace() != cfg.WorkspaceDir || rt.OwnsWorkspace() {
			t.Fatalf("external workspace was replaced or claimed: %q", rt.Workspace())
		}
	} else if rt.Workspace() != filepath.Join(rt.runDir, "workspace") || !rt.OwnsWorkspace() {
		t.Fatalf("workspace %q must be owned and inside run directory %q", rt.Workspace(), rt.runDir)
	}
	if (rt.runDir != "") != (cfg.WorkspaceDir == "" || cfg.AgentHome == AgentHomeTemporary) {
		t.Fatalf("unexpected run directory %q for config %+v", rt.runDir, cfg)
	}
	if rt.runDir != "" && !strings.HasPrefix(filepath.Base(rt.runDir), "skill-up-") {
		t.Fatalf("run directory = %q, want skill-up-*", rt.runDir)
	}
	if rt.IsolatedHome() != (cfg.AgentHome == AgentHomeTemporary) {
		t.Fatalf("IsolatedHome = %t for mode %q", rt.IsolatedHome(), cfg.AgentHome)
	}
}

func checkIsolatedHomeWorkspaceArtifact(t *testing.T, rt *NoneRuntime) {
	t.Helper()
	if rt.IsolatedHome() {
		if err := os.WriteFile(filepath.Join(rt.runDir, "auth.json"), []byte("test-key"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(rt.runDir, "sessions"), 0o700); err != nil {
			t.Fatal(err)
		}
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
	rt := &NoneRuntime{cfg: Config{Type: "none", AgentHome: "temporary", Delete: true, Env: map[string]string{"HOME": "/personal", "CODEX_HOME": "/personal/codex"}}}
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
		if !strings.HasPrefix(got[key], rt.runDir) {
			t.Errorf("%s = %q, not under %q", key, got[key], rt.runDir)
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
	want := strings.Join([]string{rt.runDir, rt.runDir, filepath.Join(rt.runDir, ".claude")}, "|")
	if result.Stdout != want {
		t.Fatalf("env = %q, want %q", result.Stdout, want)
	}
}

func TestIsolatedHomesArePerRuntime(t *testing.T) {
	workspace := t.TempDir()
	var homes []string
	for range 2 {
		rt := &NoneRuntime{cfg: Config{Type: "none", WorkspaceDir: workspace, AgentHome: "temporary", Delete: true}}
		if err := rt.Create(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rt.Close() })
		homes = append(homes, rt.runDir)
	}
	if homes[0] == homes[1] {
		t.Fatal("runtimes share a home")
	}
}

func TestInheritedHomeEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	for _, mode := range []string{"", "inherited"} {
		t.Run(mode, func(t *testing.T) {
			rt := &NoneRuntime{cfg: Config{AgentHome: mode, Delete: true}}
			if !rt.Shell().IsBash() && rt.Shell().GOOS == "windows" {
				t.Skip("requires bash")
			}
			if err := rt.Create(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = rt.Close() })
			out, err := rt.Exec(t.Context(), `printf '%s' "$HOME|$CODEX_HOME|$CLAUDE_CONFIG_DIR"`, ExecOptions{})
			want := strings.Join([]string{home, filepath.Join(home, "codex"), filepath.Join(home, "claude")}, "|")
			if err != nil || out.ExitCode != 0 || out.Stdout != want {
				t.Fatalf("inherited env = %+v, %v; want %q", out, err, want)
			}
		})
	}
}

func TestRuntimeAgentHomeValidation(t *testing.T) {
	for _, tc := range []struct {
		kind, mode, wantErr string
	}{
		{kind: "none", mode: ""},
		{kind: "none", mode: "inherited"},
		{kind: "none", mode: "temporary"},
		{kind: "none", mode: "unknown", wantErr: "must be inherited or temporary"},
		{kind: "docker", mode: "temporary", wantErr: "requires the none runtime"},
		{kind: "opensandbox", mode: "temporary", wantErr: "requires the none runtime"},
	} {
		t.Run(tc.kind+"/"+tc.mode, func(t *testing.T) {
			_, err := NewRuntime(Config{Type: tc.kind, AgentHome: tc.mode})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("NewRuntime error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
