package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/agentkind"
	"github.com/alibaba/skill-up/internal/platform"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

func TestOpenCodeFactoryAndCommands(t *testing.T) {
	t.Parallel()
	ag, err := DetectAgent(agentkind.OpenCode, Config{ModelProvider: "anthropic", ModelName: "claude-test"})
	if err != nil {
		t.Fatal(err)
	}
	openCode, ok := ag.(*OpenCodeAgent)
	if !ok {
		t.Fatalf("agent type = %T", ag)
	}
	if openCode.Cfg.SkillPath != ".opencode/skills" || openCode.Cfg.CheckCmd != "command -v opencode" {
		t.Fatalf("OpenCode defaults = %+v", openCode.Cfg)
	}
	cmd := buildOpenCodeRunCmd("-fix 'this'", openCode.model(), "ses_123")
	for _, want := range []string{"opencode run --format json", "--model 'anthropic/claude-test'", "--session 'ses_123'", "-- " + shellQuote("-fix 'this'")} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command %q missing %q", cmd, want)
		}
	}
	if !strings.Contains(defaultOpenCodeInstallCmdForVersion("1.2.3"), "'opencode-ai@1.2.3'") {
		t.Fatal("pinned version missing from install command")
	}
}

func TestOpenCodeMCPConfigAndProviderAreRuntimeScoped(t *testing.T) {
	t.Parallel()
	rt := &qwenTestRuntime{workspace: t.TempDir()}
	ag := NewOpenCodeAgent(Config{ModelProvider: "acme", ModelName: "coder", BaseURL: "https://llm.example/v1", APIKey: "secret"})
	err := ag.InstallMCP(context.Background(), rt, runtime.MCPConfig{Servers: []runtime.MCPServerConfig{
		{Name: "local", Transport: "stdio", Command: "npx", Args: []string{"-y", "test"}, Env: map[string]string{"MCP_KEY": "mcp-secret"}},
		{Name: "remote", Transport: "http", Endpoint: "https://mcp.example/${MCP_KEY}", Env: map[string]string{"MCP_KEY": "remote-secret"}, Headers: map[string]string{"Authorization": "Bearer ${MCP_KEY}"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCP      map[string]map[string]any `json:"mcp"`
		Provider map[string]map[string]any `json:"provider"`
	}
	if err := json.Unmarshal([]byte(rt.mergedEnv["OPENCODE_CONFIG_CONTENT"]), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.MCP["remote"]["url"] != "https://mcp.example/{env:SKILL_UP_OPENCODE_MCP_1_MCP_KEY}" {
		t.Fatalf("remote MCP URL = %v", cfg.MCP["remote"]["url"])
	}
	if cfg.MCP["local"]["type"] != "local" || cfg.Provider["acme"]["npm"] != "@ai-sdk/openai-compatible" {
		t.Fatalf("MCP/provider config = %+v", cfg)
	}
	if strings.Contains(rt.mergedEnv["OPENCODE_CONFIG_CONTENT"], "secret") {
		t.Fatal("inline config contains a credential value")
	}
	if rt.mergedEnv["SKILL_UP_OPENCODE_API_KEY"] != "secret" || rt.mergedEnv["SKILL_UP_OPENCODE_MCP_0_MCP_KEY"] != "mcp-secret" || rt.mergedEnv["SKILL_UP_OPENCODE_MCP_1_MCP_KEY"] != "remote-secret" {
		t.Fatal("runtime credential environment is missing")
	}
}

func TestOpenCodeMCPPreservesRuntimeConfiguration(t *testing.T) {
	if goruntime.GOOS == platform.GOOSWindows {
		t.Skip("runtime configuration probe uses a POSIX shell")
	}
	initial := `{"plugin":["existing-plugin"],"permission":{"bash":"ask"},"mcp":{"existing":{"type":"local","command":["existing-server"]}},"provider":{"other":{"npm":"existing-provider"}}}`
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", WorkspaceDir: t.TempDir(), Env: map[string]string{"OPENCODE_CONFIG_CONTENT": initial}})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer rt.Close() //nolint:errcheck
	ag := NewOpenCodeAgent(Config{ModelProvider: "acme", ModelName: "coder", BaseURL: "https://llm.example/v1"})
	if err := ag.InstallMCP(context.Background(), rt, runtime.MCPConfig{Servers: []runtime.MCPServerConfig{{Name: "added", Transport: "stdio", Command: "added-server"}}}); err != nil {
		t.Fatal(err)
	}
	result, err := rt.Exec(context.Background(), `printf '%s' "$OPENCODE_CONFIG_CONTENT"`, runtime.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("read runtime config: result=%+v err=%v", result, err)
	}
	var cfg struct {
		Plugin     []string                   `json:"plugin"`
		Permission map[string]string          `json:"permission"`
		MCP        map[string]json.RawMessage `json:"mcp"`
		Provider   map[string]json.RawMessage `json:"provider"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Plugin) != 1 || cfg.Plugin[0] != "existing-plugin" || cfg.Permission["bash"] != "ask" {
		t.Fatalf("existing OpenCode settings lost: %+v", cfg)
	}
	if cfg.MCP["existing"] == nil || cfg.MCP["added"] == nil {
		t.Fatalf("MCP config = %+v", cfg.MCP)
	}
	if cfg.Provider["other"] == nil || cfg.Provider["acme"] == nil {
		t.Fatalf("provider config = %+v", cfg.Provider)
	}
}

func TestOpenCodeCustomAnthropicProvider(t *testing.T) {
	t.Parallel()
	rt := &qwenTestRuntime{workspace: t.TempDir()}
	ag := NewOpenCodeAgent(Config{ModelProvider: "acme", ModelName: "claude-test", BaseURL: "https://anthropic.example", Protocol: "anthropic", APIKey: "secret"})
	if err := ag.InstallMCP(context.Background(), rt, runtime.MCPConfig{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rt.mergedEnv["OPENCODE_CONFIG_CONTENT"], `"npm":"@ai-sdk/anthropic"`) {
		t.Fatalf("wrong custom provider package: %s", rt.mergedEnv["OPENCODE_CONFIG_CONTENT"])
	}
	if ag.model() != "acme/claude-test" {
		t.Fatalf("model = %q", ag.model())
	}
}

func TestOpenCodeRunAndResumeAcrossRuntimeKinds(t *testing.T) {
	t.Parallel()
	stream := strings.Join([]string{
		`{"type":"step_start","sessionID":"ses_123","part":{"type":"step-start"}}`,
		`{"type":"tool_use","sessionID":"ses_123","part":{"type":"tool","tool":"bash","callID":"call_1","state":{"status":"completed","input":{"command":"pwd"},"output":"/work"}}}`,
		`{"type":"text","sessionID":"ses_123","part":{"type":"text","text":"opencode finished"}}`,
		`{"type":"step_finish","sessionID":"ses_123","part":{"type":"step-finish","tokens":{"input":10,"output":3}}}`,
	}, "\n")
	for _, processSandbox := range []bool{true, false} {
		rt := &qwenTestRuntime{workspace: t.TempDir(), noProcessSandbox: !processSandbox, execResult: runtime.ExecResult{Stdout: stream}}
		ag := NewOpenCodeAgent(Config{ModelProvider: "openai", ModelName: "test"})
		result, err := ag.Run(context.Background(), rt, runtime.ExecOptions{}, []transcript.Message{{Role: transcript.RoleUser, Content: "test"}})
		if err != nil {
			t.Fatalf("Run with processSandbox=%t: %v", processSandbox, err)
		}
		if result.SessionID != "ses_123" || result.FinalMessage != "opencode finished" || result.InputTokens != 10 || len(result.Transcript.ToolCalls()) != 1 {
			t.Fatalf("unexpected session result: %+v", result)
		}
		result, err = ag.RunTurn(context.Background(), rt, runtime.ExecOptions{}, transcript.Message{Role: transcript.RoleUser, Content: "again"}, result.SessionID)
		if err != nil || !strings.Contains(rt.lastCommand, "--session 'ses_123'") || result.FinalMessage != "opencode finished" {
			t.Fatalf("resume failed: cmd=%q result=%+v err=%v", rt.lastCommand, result, err)
		}
	}
}

func TestOpenCodeEmptyOutputFails(t *testing.T) {
	t.Parallel()
	rt := &qwenTestRuntime{workspace: t.TempDir()}
	result, err := NewOpenCodeAgent(Config{}).Run(context.Background(), rt, runtime.ExecOptions{}, []transcript.Message{{Role: transcript.RoleUser, Content: "test"}})
	if err == nil || result.ExitCode == 0 {
		t.Fatalf("empty output result=%+v err=%v", result, err)
	}
}

func TestOpenCodeErrorEventFailsEvenWithAssistantText(t *testing.T) {
	t.Parallel()
	rt := &qwenTestRuntime{workspace: t.TempDir(), execResult: runtime.ExecResult{Stdout: strings.Join([]string{
		`{"type":"text","sessionID":"ses_123","part":{"text":"working"}}`,
		`{"type":"error","sessionID":"ses_123","error":{"name":"APIError","data":{"message":"provider unavailable"}}}`,
	}, "\n")}}
	result, err := NewOpenCodeAgent(Config{}).Run(context.Background(), rt, runtime.ExecOptions{}, []transcript.Message{{Role: transcript.RoleUser, Content: "test"}})
	if err == nil || !strings.Contains(err.Error(), "provider unavailable") || result.ExitCode == 0 {
		t.Fatalf("error event result=%+v err=%v", result, err)
	}
}

func TestOpenCodeLocalRuntimeCommandBoundary(t *testing.T) {
	if goruntime.GOOS == platform.GOOSWindows {
		t.Skip("fake OpenCode executable uses a POSIX shell")
	}
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := "#!/bin/sh\nif [ \"$1\" = '--version' ]; then echo 1.2.3; exit 0; fi\nprintf '%s\\n' \"$*\" > \"$FAKE_ARGS_PATH\"\necho '{\"type\":\"text\",\"sessionID\":\"ses_local\",\"part\":{\"text\":\"local ok\"}}'\n"
	launcherPath := filepath.Join(binDir, "opencode")
	if err := os.WriteFile(launcherPath, []byte(launcher), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(launcherPath, 0o700); err != nil { // #nosec G302 -- temporary test CLI must be executable
		t.Fatal(err)
	}
	argsPath := filepath.Join(root, "args")
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", WorkspaceDir: root, Env: map[string]string{
		"PATH": binDir + string(os.PathListSeparator) + os.Getenv("PATH"), "FAKE_ARGS_PATH": argsPath,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer rt.Close() //nolint:errcheck
	ag := NewOpenCodeAgent(Config{ModelProvider: "openai", ModelName: "test"})
	if observed, err := Preflight(context.Background(), rt, ag); err != nil || observed.Version != "1.2.3" {
		t.Fatalf("Preflight observation=%+v err=%v", observed, err)
	}
	result, err := ag.Run(context.Background(), rt, runtime.ExecOptions{}, []transcript.Message{{Role: transcript.RoleUser, Content: "say hello"}})
	if err != nil || result.FinalMessage != "local ok" || result.SessionID != "ses_local" {
		t.Fatalf("Run result=%+v err=%v", result, err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil || !strings.Contains(string(args), "--model openai/test") || !strings.Contains(string(args), "say hello") {
		t.Fatalf("OpenCode arguments=%q err=%v", args, err)
	}
}
