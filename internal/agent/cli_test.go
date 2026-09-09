package agent

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/platform"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// skipIfNoPOSIXShell skips tests that rely on a POSIX shell at the host
// (RunCmd/InstallSkillCmd/InstallMCPCmd templates baked with bash builtins,
// `&&` pipelines, `$VAR` expansion, etc.). On Windows skill-up's none runtime
// uses cmd.exe, which can't interpret those constructs — agent execution on
// native Windows is intentionally out of scope; users go through WSL2.
func skipIfNoPOSIXShell(t *testing.T) {
	t.Helper()
	if goruntime.GOOS == platform.GOOSWindows {
		t.Skip("POSIX-shell agent template; native Windows agent execution is unsupported (use WSL2)")
	}
}

func TestCLIAgent_Run(t *testing.T) {
	skipIfNoPOSIXShell(t)
	t.Parallel()

	// Use NoneRuntime as the test runtime
	rt := &runtime.NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	// Create a CLIAgent with a known RunCmd
	ag := &CLIAgent{
		BaseAgent: BaseAgent{
			Cfg: Config{
				Name:   "test-agent",
				RunCmd: "echo hello %s",
			},
		},
	}

	result, err := ag.Run(context.Background(), rt, ExecOptions{Cwd: t.TempDir()}, []transcript.Message{{Role: transcript.RoleUser, Content: "world"}})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	// RunCmd = "echo hello %s", instruction = "world" → "echo hello world"
	if result.FinalMessage == "" {
		t.Error("expected non-empty output")
	}
}

func TestCLIAgent_RunExitError(t *testing.T) {
	skipIfNoPOSIXShell(t)
	t.Parallel()

	rt := &runtime.NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	ag := &CLIAgent{
		BaseAgent: BaseAgent{
			Cfg: Config{
				Name:   "test-agent",
				RunCmd: "exit %d",
			},
		},
	}

	_, err := ag.Run(context.Background(), rt, ExecOptions{Cwd: t.TempDir()}, []transcript.Message{{Role: transcript.RoleUser, Content: "1"}})
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
}

func TestCLIAgent_Check(t *testing.T) {
	t.Parallel()

	rt := &runtime.NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	ag := &CLIAgent{
		BaseAgent: BaseAgent{
			Cfg: Config{
				Name:     "test-agent",
				CheckCmd: "echo ok",
			},
		},
	}

	if err := ag.Check(context.Background(), rt); err != nil {
		t.Fatalf("Check failed: %v", err)
	}
}

func TestCLIAgent_CheckNotFound(t *testing.T) {
	t.Parallel()

	rt := &runtime.NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	ag := &CLIAgent{
		BaseAgent: BaseAgent{
			Cfg: Config{
				Name:     "test-agent",
				CheckCmd: "exit 1",
			},
		},
	}

	err := ag.Check(context.Background(), rt)
	if err == nil {
		t.Fatal("expected error when check command fails")
	}
}

func TestCLIAgent_CheckNoCmd(t *testing.T) {
	t.Parallel()

	rt := &runtime.NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	ag := &CLIAgent{
		BaseAgent: BaseAgent{
			Cfg: Config{
				Name:     "test-agent",
				CheckCmd: "",
			},
		},
	}

	err := ag.Check(context.Background(), rt)
	if err == nil {
		t.Fatal("expected error when CheckCmd is empty")
	}
}

func TestCLIAgent_InspectRuntime(t *testing.T) {
	t.Parallel()

	rt := &runtime.NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	ag := &CLIAgent{BaseAgent: BaseAgent{Cfg: Config{
		Name:       "test-agent",
		Version:    "v1.2.3",
		VersionCmd: "printf 'test-agent 1.2.3 (build 4)'",
	}}}
	observation, err := ag.InspectRuntime(context.Background(), rt)
	if err != nil {
		t.Fatalf("InspectRuntime failed: %v", err)
	}
	if observation.Version != "1.2.3" {
		t.Fatalf("Version = %q, want 1.2.3", observation.Version)
	}
}

func TestNormalizeVersionOutput(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"plain":              "tool 1.2.3",
		"v prefix":           "tool v1.2.3",
		"prerelease":         "tool 1.2.3-beta.1",
		"build metadata":     "tool 1.2.3+build.4",
		"prerelease build":   "tool 1.2.3-beta.1+build.4",
		"four-part version":  "tool 1.2.3.4",
		"invalid prerelease": "tool 1.2.3-01",
	}
	wants := map[string]string{
		"plain":            "1.2.3",
		"v prefix":         "1.2.3",
		"prerelease":       "1.2.3-beta.1",
		"build metadata":   "1.2.3+build.4",
		"prerelease build": "1.2.3-beta.1+build.4",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeVersionOutput(input); got != wants[name] {
				t.Fatalf("normalizeVersionOutput(%q) = %q, want %q", input, got, wants[name])
			}
		})
	}
}

func TestCLIAgent_InspectRuntimeRejectsInvalidVersionObservation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		version    string
		versionCmd string
		wantError  string
	}{
		{name: "mismatch", version: "1.2.4", versionCmd: "printf 'test-agent 1.2.3'", wantError: "version mismatch"},
		{name: "missing", version: "1.2.3", versionCmd: "printf 'unknown'", wantError: "returned no version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rt := &runtime.NoneRuntime{}
			if err := rt.Create(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rt.Close() }()

			ag := &CLIAgent{BaseAgent: BaseAgent{Cfg: Config{
				Name:       "test-agent",
				Version:    tt.version,
				VersionCmd: tt.versionCmd,
			}}}
			_, err := ag.InspectRuntime(context.Background(), rt)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("InspectRuntime error = %v, want %s", err, tt.wantError)
			}
		})
	}
}

func TestCLIAgent_InspectRuntimeLeavesUnparseableVersionUnknownWithoutConstraint(t *testing.T) {
	t.Parallel()

	rt := &runtime.NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	ag := &CLIAgent{BaseAgent: BaseAgent{Cfg: Config{
		Name:       "test-agent",
		VersionCmd: "printf 'development build'",
	}}}
	observation, err := ag.InspectRuntime(context.Background(), rt)
	if err != nil {
		t.Fatalf("InspectRuntime failed: %v", err)
	}
	if observation.Version != "" {
		t.Fatalf("Version = %q, want unknown", observation.Version)
	}
}

func TestCLIAgent_InstallSkillDefault(t *testing.T) {
	t.Parallel()

	rt := &runtime.NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	// Create a skill directory with files
	skillDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Skill"), 0o600); err != nil {
		t.Fatal(err)
	}

	ag := &CLIAgent{
		BaseAgent: BaseAgent{
			Cfg: Config{
				Name:      "test-agent",
				SkillPath: ".claude/skills",
			},
		},
	}

	err := ag.InstallSkill(context.Background(), rt, runtime.SkillConfig{Source: skillDir})
	if err != nil {
		t.Fatalf("InstallSkill failed: %v", err)
	}

	// Verify skill was uploaded to workspace
	skillFile := filepath.Join(rt.Workspace(), ".claude", "skills", filepath.Base(skillDir), "SKILL.md")
	if _, err := os.ReadFile(skillFile); err != nil {
		t.Errorf("skill file should exist at %s: %v", skillFile, err)
	}
}

func TestCLIAgent_InstallSkillWithCmd(t *testing.T) {
	skipIfNoPOSIXShell(t)
	t.Parallel()

	rt := &runtime.NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	ag := &CLIAgent{
		BaseAgent: BaseAgent{
			Cfg: Config{
				Name: "test-agent",
				// Template that writes to a known location for verification
				InstallSkillCmd: "mkdir -p {{.Workspace}}/installed && echo installed > {{.Workspace}}/installed/{{.Source}}",
				SkillPath:       ".claude/skills",
			},
		},
	}

	err := ag.InstallSkill(context.Background(), rt, runtime.SkillConfig{Source: "my-skill"})
	if err != nil {
		t.Fatalf("InstallSkill failed: %v", err)
	}

	// Verify the install command was executed
	marker := filepath.Join(rt.Workspace(), "installed", "my-skill")
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Errorf("install marker should exist: %v", err)
	}
	if string(data) != "installed\n" {
		t.Errorf("marker content: got %q, want %q", string(data), "installed\n")
	}
}

func TestCLIAgent_InstallSkillWithCmdRejectsFilters(t *testing.T) {
	t.Parallel()

	ag := &CLIAgent{BaseAgent: BaseAgent{Cfg: Config{
		Name:            "test-agent",
		InstallSkillCmd: "echo install",
	}}}
	err := ag.InstallSkill(context.Background(), nil, runtime.SkillConfig{
		Source:  "my-skill",
		Exclude: []string{".qoder/repowiki/**"},
	})
	if err == nil || !strings.Contains(err.Error(), "not supported with a custom InstallSkillCmd") {
		t.Fatalf("InstallSkill error = %v, want unsupported filters", err)
	}
}

func TestCLIAgent_InstallMCPEmpty(t *testing.T) {
	t.Parallel()

	rt := &runtime.NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	ag := &CLIAgent{
		BaseAgent: BaseAgent{
			Cfg: Config{
				Name:          "test-agent",
				InstallMCPCmd: "",
			},
		},
	}

	// Empty InstallMCPCmd should be a no-op
	err := ag.InstallMCP(context.Background(), rt, runtime.MCPConfig{})
	if err != nil {
		t.Fatalf("InstallMCP with empty cmd should be no-op: %v", err)
	}
}

func TestCLIAgent_InstallMCPConfiguredServersRequireInstallCommand(t *testing.T) {
	t.Parallel()

	rt := &runtime.NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	ag := &CLIAgent{
		BaseAgent: BaseAgent{
			Cfg: Config{
				Name:          "test-agent",
				InstallMCPCmd: "",
			},
		},
	}

	err := ag.InstallMCP(context.Background(), rt, runtime.MCPConfig{
		Servers: []runtime.MCPServerConfig{{Name: "github", Mode: "real", Endpoint: "https://mcp.example.test"}},
	})
	if err == nil {
		t.Fatal("expected missing InstallMCPCmd error")
	}
}

func TestCLIAgent_InstallMCPUsesResolvedEndpointConfigRefAndEnv(t *testing.T) {
	skipIfNoPOSIXShell(t)
	t.Parallel()

	rt := &runtime.NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	ag := &CLIAgent{
		BaseAgent: BaseAgent{
			Cfg: Config{
				Name:          "test-agent",
				InstallMCPCmd: `mkdir -p installed && printf "%s|%s|%s|%s" "{{.Name}}" "{{.Endpoint}}" "{{.ConfigRef}}" "$MCP_TOKEN" > installed/mcp`,
			},
		},
	}

	err := ag.InstallMCP(context.Background(), rt, runtime.MCPConfig{
		Servers: []runtime.MCPServerConfig{{
			Name:      "github",
			Mode:      "real",
			Endpoint:  "https://mcp.example.test/github",
			ConfigRef: "/tmp/github-mcp.yaml",
			Env:       map[string]string{"MCP_TOKEN": "secret-token"},
		}},
	})
	if err != nil {
		t.Fatalf("InstallMCP failed: %v", err)
	}
	marker := filepath.Join(rt.Workspace(), "installed", "mcp")
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("install marker should exist: %v", err)
	}
	want := "github|https://mcp.example.test/github|/tmp/github-mcp.yaml|secret-token"
	if string(data) != want {
		t.Fatalf("marker content: got %q, want %q", string(data), want)
	}
}

func TestCheckCommandForOS(t *testing.T) {
	tests := []struct {
		name, in, goos, want string
	}{
		{"posix unchanged", "command -v codex", "linux", "command -v codex"},
		{"darwin unchanged", "command -v claude", "darwin", "command -v claude"},
		{"windows translates", "command -v codex", platform.GOOSWindows, "where codex"},
		{"windows redirects /dev/null", "command -v codex >/dev/null 2>&1", platform.GOOSWindows, "where codex >nul 2>&1"},
		{"windows stderr /dev/null", "command -v claude 2>/dev/null", platform.GOOSWindows, "where claude 2>nul"},
		{"windows non-command form unchanged", "codex --version", platform.GOOSWindows, "codex --version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkCommandForOS(tt.in, tt.goos); got != tt.want {
				t.Fatalf("checkCommandForOS(%q, %q) = %q, want %q", tt.in, tt.goos, got, tt.want)
			}
		})
	}
}
