package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
)

const nodeCommand = "node"

func TestProvisioner_RealServerFromConfigRefResolvesAuthEnv(t *testing.T) {
	t.Parallel()

	skillDir := t.TempDir()
	configPath := filepath.Join(skillDir, "evals", "fixtures", "mcp", "github.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `endpoint: https://mcp.example.test/github
required_env:
  - GITHUB_TOKEN
env:
  EXTRA_TOKEN: ${EXTRA_TOKEN}
headers:
  Authorization: Bearer ${GITHUB_TOKEN}
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	provisioner := Provisioner{
		SkillDir: skillDir,
		LookupEnv: func(name string) (string, bool) {
			values := map[string]string{
				"GITHUB_TOKEN": "ghp-test",
				"EXTRA_TOKEN":  "extra-test",
			}
			value, ok := values[name]
			return value, ok
		},
	}
	resolved, env, err := provisioner.Provision(config.MCPConfig{
		Servers: []config.MCPServer{{
			Name:      "github",
			Mode:      "real",
			ConfigRef: "evals/fixtures/mcp/github.yaml",
		}},
	})
	if err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	if len(resolved.Servers) != 1 {
		t.Fatalf("servers len = %d, want 1", len(resolved.Servers))
	}
	server := resolved.Servers[0]
	if server.Endpoint != "https://mcp.example.test/github" {
		t.Fatalf("Endpoint = %q", server.Endpoint)
	}
	if server.Transport != "http" {
		t.Fatalf("Transport = %q, want http", server.Transport)
	}
	if server.ConfigRef != configPath {
		t.Fatalf("ConfigRef = %q, want %q", server.ConfigRef, configPath)
	}
	if server.Env["GITHUB_TOKEN"] != "ghp-test" || server.Env["EXTRA_TOKEN"] != "extra-test" {
		t.Fatalf("server env = %#v", server.Env)
	}
	if env["GITHUB_TOKEN"] != "ghp-test" || env["EXTRA_TOKEN"] != "extra-test" {
		t.Fatalf("run env = %#v", env)
	}
	if server.Headers["Authorization"] != "Bearer ${GITHUB_TOKEN}" {
		t.Fatalf("headers = %#v", server.Headers)
	}
	if len(server.HeaderEnv) != 0 {
		t.Fatalf("HeaderEnv = %#v, want empty for interpolated header", server.HeaderEnv)
	}
}

func TestProvisioner_RealServerResolvesEndpointEnvRefs(t *testing.T) {
	t.Parallel()

	skillDir := t.TempDir()
	configPath := filepath.Join(skillDir, "mcp.yaml")
	content := `transport: http
endpoint: https://mcp.example.com/sandbox/mcp?token=${SANDBOX_MCP_TOKEN}
headers:
  PRIVATE-TOKEN: ${CODE_PRIVATE_TOKEN}
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, env, err := (Provisioner{
		SkillDir: skillDir,
		LookupEnv: func(name string) (string, bool) {
			values := map[string]string{
				"SANDBOX_MCP_TOKEN":  "sandbox-token",
				"CODE_PRIVATE_TOKEN": "code-token",
			}
			value, ok := values[name]
			return value, ok
		},
	}).Provision(config.MCPConfig{
		Servers: []config.MCPServer{{Name: "agent-sandbox", Mode: "real", ConfigRef: "mcp.yaml"}},
	})
	if err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	server := resolved.Servers[0]
	if server.Endpoint != "https://mcp.example.com/sandbox/mcp?token=${SANDBOX_MCP_TOKEN}" {
		t.Fatalf("Endpoint = %q", server.Endpoint)
	}
	if server.Headers["PRIVATE-TOKEN"] != "${CODE_PRIVATE_TOKEN}" {
		t.Fatalf("Headers = %#v", server.Headers)
	}
	if server.Env["SANDBOX_MCP_TOKEN"] != "sandbox-token" || env["SANDBOX_MCP_TOKEN"] != "sandbox-token" {
		t.Fatalf("endpoint env not propagated: server=%#v run=%#v", server.Env, env)
	}
	if server.Env["CODE_PRIVATE_TOKEN"] != "code-token" || env["CODE_PRIVATE_TOKEN"] != "code-token" {
		t.Fatalf("header env not propagated: server=%#v run=%#v", server.Env, env)
	}
	if server.HeaderEnv["PRIVATE-TOKEN"] != "CODE_PRIVATE_TOKEN" {
		t.Fatalf("HeaderEnv = %#v", server.HeaderEnv)
	}
}

func TestProvisioner_RejectsInvalidBareEnvRef(t *testing.T) {
	t.Parallel()

	_, _, err := (Provisioner{
		LookupEnv: func(name string) (string, bool) {
			return "value", true
		},
	}).Provision(config.MCPConfig{
		Servers: []config.MCPServer{{
			Name:     "agent-sandbox",
			Mode:     "real",
			Endpoint: "$FOO-BAR",
		}},
	})
	if err == nil {
		t.Fatal("expected invalid endpoint error")
	}
	if strings.Contains(err.Error(), "referenced environment variable FOO-BAR is not set") {
		t.Fatalf("invalid env ref was treated as a valid whole env ref: %v", err)
	}
}

func TestProvisioner_RealStdioServerFromConfig(t *testing.T) {
	t.Parallel()

	resolved, env, err := (Provisioner{}).Provision(config.MCPConfig{
		Servers: []config.MCPServer{{
			Name:      "marker",
			Mode:      "real",
			Transport: "stdio",
			Command:   "node",
			Args:      []string{"marker-server.mjs", "secret-marker"},
		}},
	})
	if err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	if len(env) != 0 {
		t.Fatalf("run env = %#v, want empty", env)
	}
	server := resolved.Servers[0]
	if server.Transport != transportStdio {
		t.Fatalf("Transport = %q, want stdio", server.Transport)
	}
	if server.Command != nodeCommand {
		t.Fatalf("Command = %q, want node", server.Command)
	}
	if strings.Join(server.Args, "|") != "marker-server.mjs|secret-marker" {
		t.Fatalf("Args = %#v", server.Args)
	}
}

func TestProvisioner_RealStdioServerFromConfigRef(t *testing.T) {
	t.Parallel()

	skillDir := t.TempDir()
	configPath := filepath.Join(skillDir, "mcp.yaml")
	if err := os.WriteFile(configPath, []byte("transport: stdio\ncommand: node\nargs: [server.mjs]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, _, err := (Provisioner{SkillDir: skillDir}).Provision(config.MCPConfig{
		Servers: []config.MCPServer{{Name: "marker", Mode: "real", ConfigRef: "mcp.yaml"}},
	})
	if err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	server := resolved.Servers[0]
	if server.Transport != transportStdio || server.Command != nodeCommand || strings.Join(server.Args, "|") != "server.mjs" {
		t.Fatalf("server = %#v", server)
	}
}

func TestProvisioner_RealServerRequiresNonInteractiveAuth(t *testing.T) {
	t.Parallel()

	skillDir := t.TempDir()
	configPath := filepath.Join(skillDir, "mcp.yaml")
	if err := os.WriteFile(configPath, []byte("endpoint: https://mcp.example.test\nrequired_env: [MCP_TOKEN]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := (Provisioner{
		SkillDir:  skillDir,
		LookupEnv: func(string) (string, bool) { return "", false },
	}).Provision(config.MCPConfig{
		Servers: []config.MCPServer{{Name: "secure", Mode: "real", ConfigRef: "mcp.yaml"}},
	})
	if err == nil || !strings.Contains(err.Error(), "MCP_TOKEN") {
		t.Fatalf("expected missing MCP_TOKEN error, got %v", err)
	}
}

func TestProvisioner_MockedFilesystemUsesGeneratedStdioServer(t *testing.T) {
	t.Parallel()

	resolved, env, err := (Provisioner{}).Provision(config.MCPConfig{
		Servers: []config.MCPServer{{Name: "filesystem", Mode: "mocked"}},
	})
	if err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	if len(env) != 0 {
		t.Fatalf("run env = %#v, want empty", env)
	}
	server := resolved.Servers[0]
	if server.Mode != modeMocked || server.Transport != transportStdio || server.Command != nodeCommand {
		t.Fatalf("server = %#v", server)
	}
	if len(server.Args) != 2 || server.Args[0] != "-e" {
		t.Fatalf("Args = %#v", server.Args)
	}
	if !strings.Contains(server.Args[1], "list_directory") || !strings.Contains(server.Args[1], "write_file") {
		t.Fatalf("mock script does not expose filesystem tools")
	}
}

func TestProvisioner_MockedServerFromConfigRefUsesToolResponses(t *testing.T) {
	t.Parallel()

	skillDir := t.TempDir()
	configPath := filepath.Join(skillDir, "mcp.yaml")
	content := `tool_responses:
  create_publish_plan_simple:
    default:
      id: "plan-{{params.name | default: 'demo'}}"
      status: created
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, env, err := (Provisioner{SkillDir: skillDir}).Provision(config.MCPConfig{
		Servers: []config.MCPServer{{Name: "project-mgmt", Mode: "mocked", ConfigRef: "mcp.yaml"}},
	})
	if err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	if len(env) != 0 {
		t.Fatalf("run env = %#v, want empty", env)
	}
	server := resolved.Servers[0]
	if server.ConfigRef != configPath {
		t.Fatalf("ConfigRef = %q, want %q", server.ConfigRef, configPath)
	}
	if server.Transport != transportStdio || server.Command != nodeCommand {
		t.Fatalf("server = %#v", server)
	}
	if len(server.Args) != 2 || !strings.Contains(server.Args[1], "create_publish_plan_simple") {
		t.Fatalf("mock script missing fixture tool: %#v", server.Args)
	}
}
