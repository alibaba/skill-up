package agent

import (
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/runtime"
)

func TestBuildCodexMCPInstallCmd_Stdio(t *testing.T) {
	t.Parallel()

	cmd, err := buildCodexMCPInstallCmd(runtime.MCPServerConfig{
		Name:      "marker",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"/tmp/marker server.mjs", "value's"},
		Env:       map[string]string{"MCP_TOKEN": "secret"},
	})
	if err != nil {
		t.Fatalf("buildCodexMCPInstallCmd failed: %v", err)
	}
	for _, want := range []string{
		"codex mcp remove 'marker'",
		"codex mcp add 'marker'",
		"--env 'MCP_TOKEN='\"$MCP_TOKEN\"",
		"-- 'node' '/tmp/marker server.mjs' 'value'\\''s'",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command missing %q:\n%s", want, cmd)
		}
	}
}

func TestBuildClaudeMCPInstallCmd_Stdio(t *testing.T) {
	t.Parallel()

	cmd, err := buildClaudeMCPInstallCmd(runtime.MCPServerConfig{
		Name:      "marker",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"/tmp/marker-server.mjs"},
		Env:       map[string]string{"MCP_TOKEN": "secret"},
	})
	if err != nil {
		t.Fatalf("buildClaudeMCPInstallCmd failed: %v", err)
	}
	for _, want := range []string{
		"claude mcp remove --scope project 'marker'",
		"claude mcp add --scope project 'marker' -e 'MCP_TOKEN='\"$MCP_TOKEN\" -- 'node' '/tmp/marker-server.mjs'",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command missing %q:\n%s", want, cmd)
		}
	}
}

func TestBuildQoderMCPInstallCmd_Stdio(t *testing.T) {
	t.Parallel()

	cmd, err := buildQoderMCPInstallCmd(runtime.MCPServerConfig{
		Name:      "marker",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"/tmp/marker-server.mjs"},
		Env:       map[string]string{"MCP_TOKEN": "secret"},
	})
	if err != nil {
		t.Fatalf("buildQoderMCPInstallCmd failed: %v", err)
	}
	for _, want := range []string{
		"qodercli mcp remove --scope project 'marker'",
		"qodercli mcp add --scope project 'marker' -e 'MCP_TOKEN='\"$MCP_TOKEN\" -- 'node' '/tmp/marker-server.mjs'",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command missing %q:\n%s", want, cmd)
		}
	}
}

func TestBuildMCPInstallCmd_HTTPHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		buildFn func(runtime.MCPServerConfig) (string, error)
		cli     string
	}{
		{name: "claude", buildFn: buildClaudeMCPInstallCmd, cli: "claude"},
		{name: "qoder", buildFn: buildQoderMCPInstallCmd, cli: "qodercli"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd, err := tt.buildFn(runtime.MCPServerConfig{
				Name:      "agent-sandbox",
				Transport: "http",
				Endpoint:  "https://mcp.example.test/agent-sandbox?token=${MCP_TOKEN}",
				Env:       map[string]string{"MCP_TOKEN": "endpoint-secret", "PRIVATE_TOKEN": "code-token"},
				Headers:   map[string]string{"PRIVATE-TOKEN": "${PRIVATE_TOKEN}"},
			})
			if err != nil {
				t.Fatalf("buildMCPInstallCmd failed: %v", err)
			}
			for _, want := range []string{
				tt.cli + " mcp add --scope project --transport http 'agent-sandbox' 'https://mcp.example.test/agent-sandbox?token='\"${MCP_TOKEN}\"",
				"--header 'PRIVATE-TOKEN:'\"${PRIVATE_TOKEN}\"",
			} {
				if !strings.Contains(cmd, want) {
					t.Fatalf("command missing %q:\n%s", want, cmd)
				}
			}
			if strings.Contains(cmd, "endpoint-secret") || strings.Contains(cmd, "code-token") {
				t.Fatalf("command should not embed resolved secrets:\n%s", cmd)
			}
		})
	}
}

func TestBuildCodexMCPInstallCmd_HTTPHeaders(t *testing.T) {
	t.Parallel()

	privateTokenHeader := "PRIVATE" + "-TOKEN"
	privateTokenEnv := "MY_APP_PRIVATE" + "_TOKEN"
	cmd, err := buildCodexMCPInstallCmd(runtime.MCPServerConfig{
		Name:      "agent-sandbox",
		Transport: "http",
		Endpoint:  "https://mcp.example.test/agent-sandbox?token=${MY_SANDBOX_MCP_TOKEN}",
		Env:       map[string]string{privateTokenEnv: "code-token", "MY_SANDBOX_MCP_TOKEN": "endpoint-secret"},
		Headers:   map[string]string{privateTokenHeader: "${" + privateTokenEnv + "}"},
		HeaderEnv: map[string]string{privateTokenHeader: privateTokenEnv},
	})
	if err != nil {
		t.Fatalf("buildCodexMCPInstallCmd failed: %v", err)
	}
	for _, want := range []string{
		"codex mcp remove 'agent-sandbox'",
		"codex mcp add 'agent-sandbox'",
		"--env 'MY_APP_PRIVATE_TOKEN='\"$MY_APP_PRIVATE_TOKEN\"",
		"exec npx mcp-remote \"$1\" --header",
		"PRIVATE-TOKEN:",
		"${MY_APP_PRIVATE_TOKEN}",
		"2>/dev/null",
		"'mcp-remote' 'https://mcp.example.test/agent-sandbox?token='\"${MY_SANDBOX_MCP_TOKEN}\"",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command missing %q:\n%s", want, cmd)
		}
	}
	if strings.Contains(cmd, "code-token") {
		t.Fatalf("command should not embed resolved header secret:\n%s", cmd)
	}
	if strings.Contains(cmd, "endpoint-secret") {
		t.Fatalf("command should not embed resolved endpoint secret:\n%s", cmd)
	}
}

func TestBuildCodexMCPInstallCmd_RejectsInvalidHeaderEnvName(t *testing.T) {
	t.Parallel()

	_, err := buildCodexMCPInstallCmd(runtime.MCPServerConfig{
		Name:      "agent-sandbox",
		Transport: "http",
		Endpoint:  "https://mcp.example.test/agent-sandbox",
		Env:       map[string]string{"BAD-NAME": "code-token"},
		Headers:   map[string]string{"PRIVATE-TOKEN": "code-token"},
		HeaderEnv: map[string]string{"PRIVATE-TOKEN": "BAD-NAME"},
	})
	if err == nil {
		t.Fatal("expected invalid header environment variable error")
	}
	if !strings.Contains(err.Error(), "BAD-NAME") {
		t.Fatalf("expected invalid env name in error, got %v", err)
	}
}
