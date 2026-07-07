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

func TestProvisioner_SameNameMockedServersDifferentFixtures(t *testing.T) {
	t.Parallel()

	skillDir := t.TempDir()
	openPath := filepath.Join(skillDir, "project-open.yaml")
	closedPath := filepath.Join(skillDir, "project-closed.yaml")
	if err := os.WriteFile(openPath, []byte("tool_responses:\n  get_project:\n    default:\n      status: OPEN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(closedPath, []byte("tool_responses:\n  get_project:\n    default:\n      status: CLOSED\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	provisioner := Provisioner{SkillDir: skillDir}
	openCfg, _, err := provisioner.Provision(config.MCPConfig{
		Servers: []config.MCPServer{{Name: "project-mgmt", Mode: "mocked", ConfigRef: "project-open.yaml"}},
	})
	if err != nil {
		t.Fatalf("Provision(open) failed: %v", err)
	}
	closedCfg, _, err := provisioner.Provision(config.MCPConfig{
		Servers: []config.MCPServer{{Name: "project-mgmt", Mode: "mocked", ConfigRef: "project-closed.yaml"}},
	})
	if err != nil {
		t.Fatalf("Provision(closed) failed: %v", err)
	}

	openScript := openCfg.Servers[0].Args[1]
	closedScript := closedCfg.Servers[0].Args[1]

	// Case-level config_ref resolves relative to SkillDir.
	if openCfg.Servers[0].ConfigRef != openPath {
		t.Errorf("open ConfigRef = %q, want %q", openCfg.Servers[0].ConfigRef, openPath)
	}
	// The same server name must yield distinct embedded fixture JSON.
	if openScript == closedScript {
		t.Fatal("expected different generated scripts for different fixtures")
	}
	if !strings.Contains(openScript, "OPEN") || !strings.Contains(closedScript, "CLOSED") {
		t.Errorf("scripts do not embed their respective fixture status values")
	}
}

func TestProvisionerValidationAndTransportErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		server  config.MCPServer
		wantErr string
	}{
		{
			name:    "missing mode",
			server:  config.MCPServer{Name: "missing"},
			wantErr: "mode is required",
		},
		{
			name:    "unknown mode",
			server:  config.MCPServer{Name: "bad", Mode: "proxy"},
			wantErr: "mode must be one of",
		},
		{
			name:    "http missing endpoint",
			server:  config.MCPServer{Name: "http", Mode: "real", Transport: "http"},
			wantErr: "requires endpoint",
		},
		{
			name:    "stdio missing command",
			server:  config.MCPServer{Name: "stdio", Mode: "real", Transport: "stdio"},
			wantErr: "requires command",
		},
		{
			name:    "unknown transport",
			server:  config.MCPServer{Name: "bad-transport", Mode: "real", Transport: "sse", Endpoint: "https://example.test"},
			wantErr: "transport must be one of",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := (Provisioner{}).Provision(config.MCPConfig{Servers: []config.MCPServer{tt.server}})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Provision error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestProvisionerServerOverridesConfigRefFields(t *testing.T) {
	t.Parallel()

	skillDir := t.TempDir()
	configPath := filepath.Join(skillDir, "mcp.yaml")
	if err := os.WriteFile(configPath, []byte("transport: stdio\ncommand: node\nargs: [from-file]\nendpoint: https://file.example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, _, err := (Provisioner{SkillDir: skillDir}).Provision(config.MCPConfig{
		Servers: []config.MCPServer{{
			Name:      "override",
			Mode:      "real",
			ConfigRef: "mcp.yaml",
			Transport: "streamable-http",
			Endpoint:  "https://server.example.test",
			Command:   "ignored-because-http",
			Args:      []string{"from-server"},
		}},
	})
	if err != nil {
		t.Fatalf("Provision returned error: %v", err)
	}
	server := resolved.Servers[0]
	if server.Transport != transportHTTP {
		t.Fatalf("Transport = %q, want http", server.Transport)
	}
	if server.Endpoint != "https://server.example.test" {
		t.Fatalf("Endpoint = %q, want server override", server.Endpoint)
	}
	if server.Command != "ignored-because-http" {
		t.Fatalf("Command = %q, want explicit command preserved", server.Command)
	}
	if len(server.Args) != 1 || server.Args[0] != "from-server" {
		t.Fatalf("Args = %#v, want server override", server.Args)
	}
}

func TestResolveEnvReferences(t *testing.T) {
	t.Parallel()

	lookup := func(name string) (string, bool) {
		values := map[string]string{
			"TOKEN": "secret",
			"HOST":  "example.test",
		}
		value, ok := values[name]
		return value, ok
	}

	value, err := resolveEnvValue("https://${HOST}/mcp?token=${TOKEN}", lookup)
	if err != nil {
		t.Fatalf("resolveEnvValue returned error: %v", err)
	}
	if value != "https://example.test/mcp?token=secret" {
		t.Fatalf("resolved value = %q", value)
	}

	refs, err := resolveReferencedEnv("Bearer ${TOKEN}", lookup)
	if err != nil {
		t.Fatalf("resolveReferencedEnv returned error: %v", err)
	}
	if refs["TOKEN"] != "secret" {
		t.Fatalf("refs = %#v, want TOKEN", refs)
	}

	if _, err := resolveEnvValue("$TOKEN-suffix", lookup); err == nil || !strings.Contains(err.Error(), "invalid environment variable reference") {
		t.Fatalf("resolveEnvValue invalid ref error = %v", err)
	}
	if _, err := resolveReferencedEnv("${MISSING}", lookup); err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("resolveReferencedEnv missing error = %v", err)
	}
	if name, ok := wholeEnvRef("$TOKEN"); !ok || name != "TOKEN" {
		t.Fatalf("wholeEnvRef($TOKEN) = %q/%v, want TOKEN true", name, ok)
	}
	if _, ok := wholeEnvRef("$TOKEN-suffix"); ok {
		t.Fatal("wholeEnvRef should reject invalid bare refs")
	}
}
