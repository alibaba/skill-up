package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

func TestCLIAgent_Run(t *testing.T) {
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
		{"windows translates", "command -v codex", "windows", "where codex"},
		{"windows non-command form unchanged", "codex --version", "windows", "codex --version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkCommandForOS(tt.in, tt.goos); got != tt.want {
				t.Fatalf("checkCommandForOS(%q, %q) = %q, want %q", tt.in, tt.goos, got, tt.want)
			}
		})
	}
}
