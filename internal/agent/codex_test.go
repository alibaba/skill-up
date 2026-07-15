package agent

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/platform"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

const (
	testAssistantRole = "assistant"
	testToolCallRole  = "tool_call"
	testToolResRole   = "tool_result"
	testStatusError   = "error"
)

func TestNewCodexAgent(t *testing.T) {
	t.Parallel()

	ag := NewCodexAgent(Config{})

	if ag.Name() != "codex" {
		t.Fatalf("expected name codex, got %s", ag.Name())
	}
	if ag.Cfg.CheckCmd != "command -v codex" {
		t.Fatalf("expected codex check cmd, got %s", ag.Cfg.CheckCmd)
	}
	if ag.Cfg.SkillPath != ".codex/skills" {
		t.Fatalf("expected codex skill path, got %s", ag.Cfg.SkillPath)
	}
}

func TestCodexCheckCredentials(t *testing.T) {
	t.Parallel()

	ag := NewCodexAgent(Config{})
	if err := ag.CheckCredentials(context.Background()); err != nil {
		t.Fatalf("expected missing OPENAI_API_KEY to be informational only, got %v", err)
	}

	ag = NewCodexAgent(Config{APIKey: "openai-test-token"})
	if err := ag.CheckCredentials(context.Background()); err != nil {
		t.Fatalf("expected OPENAI_API_KEY to be accepted, got %v", err)
	}
}

func TestCodexCheckCredentials_LogsMaskedTokenAndBaseURL(t *testing.T) {
	// Not parallel: captureStdout redirects package-level log output.

	logging.SetVerbosity(1)
	defer logging.SetVerbosity(0)

	apiKey := strings.Join([]string{"openai", "codex", "test", "token", "1234"}, "-")

	ag := NewCodexAgent(Config{
		APIKey:  apiKey,
		BaseURL: "https://openai.example.com/v1",
	})
	output := captureStdout(t, func() {
		if err := ag.CheckCredentials(context.Background()); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
	if !strings.Contains(output, `level=DEBUG msg="OPENAI_API_KEY detected for codex (masked: `) {
		t.Fatalf("expected api key log, got %q", output)
	}
	if !strings.Contains(output, credential.MaskAPIKey(apiKey)) {
		t.Fatalf("expected masked key log, got %q", output)
	}
	if !strings.Contains(output, `level=DEBUG msg="OPENAI_BASE_URL detected for codex (value: https://openai.example.com/v1, source=agent_config)"`) {
		t.Fatalf("expected base url log, got %q", output)
	}
}

func TestCodexInstall_DefaultsToPinnedVersion(t *testing.T) {
	t.Parallel()

	rt := &codexTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			ExitCode: 0,
		},
	}
	ag := NewCodexAgent(Config{APIKey: "install-should-not-see-api-key", BaseURL: "https://install.example.test"})

	if err := ag.Install(context.Background(), rt); err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if !strings.Contains(rt.lastCommand, "@openai/codex@"+codexDefaultVersion) {
		t.Fatalf("install command %q does not pin codex version %s", rt.lastCommand, codexDefaultVersion)
	}
	if _, ok := rt.lastExecEnv["PATH"]; ok {
		t.Fatalf("install env should not carry PATH from agent; PATH flows via runtime baseline. got %q", rt.lastExecEnv["PATH"])
	}
	if got := rt.mergedEnv["PATH"]; got == "" {
		t.Fatalf("expected probeAndMergePATH to populate runtime baseline with PATH; mergedEnv=%+v", rt.mergedEnv)
	}
	if got := rt.lastExecEnv[credential.EnvOpenAIAPIKey]; got != "" {
		t.Fatalf("install env leaked %s = %q", credential.EnvOpenAIAPIKey, got)
	}
	if got := rt.lastExecEnv[credential.EnvOpenAIBaseURL]; got != "" {
		t.Fatalf("install env leaked %s = %q", credential.EnvOpenAIBaseURL, got)
	}
	for _, want := range []string{
		`NVM_DIR="${NVM_DIR:-$HOME/.nvm}"`,
		"nvm install '" + agentNodeDefaultVersion + "'",
		"npm install -g --include=optional '@openai/codex@" + codexDefaultVersion + "'",
	} {
		if !strings.Contains(rt.lastCommand, want) {
			t.Fatalf("install command missing %q:\n%s", want, rt.lastCommand)
		}
	}
}

func TestBuildCodexRunCmd(t *testing.T) {
	t.Parallel()

	cmd := buildCodexRunCmd("say hi", "gpt-5.4", codexProviderConfig{}, codexProcessSandbox)
	if cmd == "" {
		t.Fatal("expected non-empty command")
	}
	if want := "codex exec --json --skip-git-repo-check --sandbox workspace-write -m 'gpt-5.4' 'say hi'"; cmd != want {
		t.Fatalf("unexpected command:\nwant: %s\ngot:  %s", want, cmd)
	}
}

// TestCodexRunProviderConfig_EmptyProviderWithBaseURLOverridesBuiltin pins the
// post-fix behavior for `ModelProvider="" + BaseURL=<non-default>`: same as the
// `ModelProvider="openai"` case, route through the synthetic "skill-up-openai"
// override so codex emits a full `model_providers.skill-up-openai.{base_url,
// env_key,wire_api=chat}` block. Regression guard for the prior buggy fallback
// that emitted only `-c openai_base_url=...` (silently ignored by codex →
// upstream got hit on /responses with the bundled provider's wire_api,
// returning 400 against /chat/completions-only endpoints like idealab).
func TestCodexRunProviderConfig_EmptyProviderWithBaseURLOverridesBuiltin(t *testing.T) {
	t.Parallel()

	ag := NewCodexAgent(Config{
		ModelName: "gpt-5.2-1211-global",
		BaseURL:   "https://idealab.alibaba-inc.com/api/openai/v1",
	})

	got := ag.runProviderConfig(context.Background())
	if got.Name != codexOpenAIOverrideProvider {
		t.Fatalf("provider name = %q, want %q", got.Name, codexOpenAIOverrideProvider)
	}
	if got.Label != codexOpenAIOverrideProvider {
		t.Fatalf("provider label = %q, want %q", got.Label, codexOpenAIOverrideProvider)
	}
	if got.BaseURL != "https://idealab.alibaba-inc.com/api/openai/v1" {
		t.Fatalf("base URL = %q, want idealab endpoint", got.BaseURL)
	}
	if got.EnvKey != credential.EnvOpenAIAPIKey {
		t.Fatalf("env key = %q, want %q", got.EnvKey, credential.EnvOpenAIAPIKey)
	}
	if got.WireAPI != codexCustomWireAPI {
		t.Fatalf("wire API = %q, want %q", got.WireAPI, codexCustomWireAPI)
	}
}

// TestCodexRunProviderConfig_EmptyProviderEmptyBaseURL keeps the no-config
// case explicit: when neither provider nor base URL is set, runProviderConfig
// returns the zero value so codex falls back to its bundled defaults.
func TestCodexRunProviderConfig_EmptyProviderEmptyBaseURL(t *testing.T) {
	t.Parallel()

	ag := NewCodexAgent(Config{ModelName: "gpt-5.2-1211-global"})

	if got := ag.runProviderConfig(context.Background()); got.Name != "" || got.BaseURL != "" {
		t.Fatalf("runProviderConfig() = %+v, want empty when both ModelProvider and BaseURL are unset", got)
	}
}

func TestBuildCodexRunCmdWithCustomProvider(t *testing.T) {
	t.Parallel()

	cmd := buildCodexRunCmd("say hi", "qwen3.6-plus", codexProviderConfig{
		Name:    "dashscope",
		Label:   "dashscope",
		BaseURL: "https://example.com/compatible-mode/v1",
		EnvKey:  credential.EnvOpenAIAPIKey,
		WireAPI: "chat",
	}, codexBypassSandbox)
	want := `codex exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox -c 'model_provider="dashscope"' -c 'model_providers.dashscope.name="dashscope"' -c 'model_providers.dashscope.base_url="https://example.com/compatible-mode/v1"' -c 'model_providers.dashscope.env_key="OPENAI_API_KEY"' -c 'model_providers.dashscope.wire_api="chat"' -m 'qwen3.6-plus' 'say hi'`
	if cmd != want {
		t.Fatalf("unexpected command:\nwant: %s\ngot:  %s", want, cmd)
	}
}

func TestCodexEffectiveModelName_IgnoresNonOpenAIProvider(t *testing.T) {
	ag := NewCodexAgent(Config{
		ModelProvider: "anthropic",
		ModelName:     "claude-sonnet-4-6",
	})

	output := captureStdout(t, func() {
		if got := ag.effectiveModelName(context.Background()); got != "" {
			t.Fatalf("effectiveModelName() = %q, want empty", got)
		}
	})
	if !strings.Contains(output, `level=WARNING msg="codex custom model provider \"anthropic\" requires base_url; model override \"claude-sonnet-4-6\" is omitted and local codex model settings will be used instead"`) {
		t.Fatalf("expected warning log, got %q", output)
	}
}

func TestCodexEffectiveModelName_UsesCustomProviderModel(t *testing.T) {
	t.Parallel()

	ag := NewCodexAgent(Config{
		ModelProvider: "dashscope",
		ModelName:     "qwen3.6-plus",
		BaseURL:       "https://example.com/compatible-mode/v1",
	})

	if got := ag.effectiveModelName(context.Background()); got != "qwen3.6-plus" {
		t.Fatalf("effectiveModelName() = %q, want qwen3.6-plus", got)
	}
}

func TestCodexRunProviderConfig_CustomProviderFromConfig(t *testing.T) {
	t.Parallel()

	ag := NewCodexAgent(Config{
		ModelProvider: "dashscope",
		BaseURL:       "https://example.com/compatible-mode/v1",
	})

	got := ag.runProviderConfig(context.Background())
	if got.Name != "dashscope" {
		t.Fatalf("provider name = %q, want dashscope", got.Name)
	}
	if got.Label != "dashscope" {
		t.Fatalf("provider label = %q, want dashscope", got.Label)
	}
	if got.BaseURL != "https://example.com/compatible-mode/v1" {
		t.Fatalf("base URL = %q, want configured value", got.BaseURL)
	}
	if got.EnvKey != credential.EnvOpenAIAPIKey {
		t.Fatalf("env key = %q, want %q", got.EnvKey, credential.EnvOpenAIAPIKey)
	}
	if got.WireAPI != "chat" {
		t.Fatalf("wire API = %q, want chat", got.WireAPI)
	}
}

func TestCodexRunProviderConfig_RejectsInvalidProviderName(t *testing.T) {
	t.Parallel()

	ag := NewCodexAgent(Config{
		ModelProvider: "dash.scope",
		ModelName:     "qwen3.6-plus",
		BaseURL:       "https://example.com/compatible-mode/v1",
	})

	if got := ag.effectiveModelName(context.Background()); got != "" {
		t.Fatalf("effectiveModelName() = %q, want empty for invalid provider name", got)
	}
	if got := ag.runProviderConfig(context.Background()); got.Name != "" || got.BaseURL != "" {
		t.Fatalf("runProviderConfig() = %+v, want empty for invalid provider name", got)
	}
}

// TestBuildCodexRunCmd_OpenAIWithBaseURL_EmitsSkillUpProviderFlags asserts the
// full -c flag set produced when callers configure provider=openai together
// with a custom BaseURL. The synthesised "skill-up-openai" provider entry must
// appear verbatim in the resulting codex command; the legacy
// `-c openai_base_url=...` fallback is unreachable on this path.
func TestBuildCodexRunCmd_OpenAIWithBaseURL_EmitsSkillUpProviderFlags(t *testing.T) {
	t.Parallel()

	ag := NewCodexAgent(Config{
		ModelProvider: agentProviderOpenAI,
		ModelName:     "qwen3.6-plus",
		BaseURL:       "https://dashscope.aliyuncs.com/compatible-mode/v1",
	})

	cmd := buildCodexRunCmd("hello world", ag.effectiveModelName(context.Background()), ag.runProviderConfig(context.Background()), codexBypassSandbox)
	want := `codex exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox -c 'model_provider="skill-up-openai"' -c 'model_providers.skill-up-openai.name="skill-up-openai"' -c 'model_providers.skill-up-openai.base_url="https://dashscope.aliyuncs.com/compatible-mode/v1"' -c 'model_providers.skill-up-openai.env_key="OPENAI_API_KEY"' -c 'model_providers.skill-up-openai.wire_api="chat"' -m 'qwen3.6-plus' 'hello world'`
	if cmd != want {
		t.Fatalf("unexpected command:\nwant: %s\ngot:  %s", want, cmd)
	}
}

func TestCodexRunProviderConfig_OpenAIWithBaseURLOverridesBuiltin(t *testing.T) {
	t.Parallel()

	ag := NewCodexAgent(Config{
		ModelProvider: agentProviderOpenAI,
		ModelName:     "qwen3.6-plus",
		BaseURL:       "https://dashscope.aliyuncs.com/compatible-mode/v1",
	})

	got := ag.runProviderConfig(context.Background())
	if got.Name != codexOpenAIOverrideProvider {
		t.Fatalf("provider name = %q, want %q", got.Name, codexOpenAIOverrideProvider)
	}
	if got.BaseURL != "https://dashscope.aliyuncs.com/compatible-mode/v1" {
		t.Fatalf("base URL = %q, want DashScope endpoint", got.BaseURL)
	}
	if got.EnvKey != credential.EnvOpenAIAPIKey {
		t.Fatalf("env key = %q, want %q", got.EnvKey, credential.EnvOpenAIAPIKey)
	}
	if got.WireAPI != "chat" {
		t.Fatalf("wire API = %q, want chat", got.WireAPI)
	}
}

func TestCodexRunProviderConfig_OpenAIWithoutBaseURLEmitsNothing(t *testing.T) {
	t.Parallel()

	ag := NewCodexAgent(Config{
		ModelProvider: agentProviderOpenAI,
		ModelName:     "gpt-5.4",
	})

	got := ag.runProviderConfig(context.Background())
	if got.Name != "" || got.BaseURL != "" {
		t.Fatalf("runProviderConfig() = %+v, want empty when BaseURL is unset", got)
	}
}

func TestCodexRunTurn_FirstTurnDelegatesToRun(t *testing.T) {
	t.Parallel()

	threadEvent := `{"type":"thread.started","thread_id":"thread-abc"}`
	rt := &codexTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			Stdout:   threadEvent + "\n",
			ExitCode: 0,
		},
	}
	ag := NewCodexAgent(Config{ModelName: "o3"})

	result, err := ag.RunTurn(context.Background(), rt, ExecOptions{}, transcript.Message{
		Role:    transcript.RoleUser,
		Content: "start task",
		Turn:    1,
	}, "")
	if err != nil {
		t.Fatalf("RunTurn (first turn): %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
		return
	}
	// First turn should use "codex exec", not "codex resume"
	if containsCommand(rt.commands, "codex resume") {
		t.Fatalf("first turn should not use codex resume: %v", rt.commands)
	}
	if !containsCommand(rt.commands, "codex exec") {
		t.Fatalf("first turn should use codex exec: %v", rt.commands)
	}
	// SessionID should be extracted from thread event
	if result.SessionID != "thread-abc" {
		t.Fatalf("expected SessionID = %q, got %q", "thread-abc", result.SessionID)
	}
}

func TestCodexRunTurn_ResumeUsesCorrectCommand(t *testing.T) {
	t.Parallel()

	rt := &codexTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			Stdout:   `{"type":"turn.started"}` + "\n" + `{"type":"item.completed","item":{"type":"agent_message","text":"resumed"}}` + "\n",
			ExitCode: 0,
		},
	}
	ag := NewCodexAgent(Config{ModelName: "o3"})

	result, err := ag.RunTurn(context.Background(), rt, ExecOptions{}, transcript.Message{
		Role:    transcript.RoleUser,
		Content: "continue working",
		Turn:    2,
	}, "thread-xyz-456")
	if err != nil {
		t.Fatalf("RunTurn (resume): %v", err)
	}
	if result.SessionID != "thread-xyz-456" {
		t.Fatalf("expected SessionID = %q, got %q", "thread-xyz-456", result.SessionID)
	}
	// Should use "codex exec resume" with the session ID
	if !containsCommand(rt.commands, "codex exec resume") {
		t.Fatalf("expected codex exec resume command, got %v", rt.commands)
	}
	if !containsCommand(rt.commands, "'thread-xyz-456'") {
		t.Fatalf("expected session ID in command, got %v", rt.commands)
	}
	if !containsCommand(rt.commands, "'continue working'") {
		t.Fatalf("expected instruction in command, got %v", rt.commands)
	}
}

func TestBuildCodexResumeCmdWithLastMessage(t *testing.T) {
	t.Parallel()

	cmd := buildCodexResumeCmdWithLastMessage(
		"sess-123", "do something", "o3",
		codexProviderConfig{},
		"/tmp/last.txt",
	)
	if !strings.HasPrefix(cmd, "codex exec resume --json --skip-git-repo-check") {
		t.Fatalf("expected codex exec resume prefix, got %q", cmd)
	}
	if !strings.Contains(cmd, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("expected bypass flag, got %q", cmd)
	}
	if !strings.Contains(cmd, "'sess-123'") {
		t.Fatalf("expected session ID, got %q", cmd)
	}
	if !strings.Contains(cmd, "'do something'") {
		t.Fatalf("expected instruction, got %q", cmd)
	}
	if !strings.Contains(cmd, "-m 'o3'") {
		t.Fatalf("expected model flag, got %q", cmd)
	}
	if !strings.Contains(cmd, "--output-last-message") {
		t.Fatalf("expected --output-last-message flag, got %q", cmd)
	}
}

func TestShellQuoteEscapesSingleQuote(t *testing.T) {
	t.Parallel()

	got := shellQuote("this skill's prompt")
	want := `'this skill'\''s prompt'`
	if got != want {
		t.Fatalf("unexpected quoted string:\nwant: %s\ngot:  %s", want, got)
	}
}

func TestParseCodexOutput(t *testing.T) { //nolint:cyclop,gocyclo // exhaustive transcript-shape assertions
	t.Parallel()

	output := `
Reading additional input from stdin...
{"type":"thread.started","thread_id":"abc"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"I’m checking the workspace first."}}
{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"/bin/zsh -lc 'rg --files -uu'","aggregated_output":"","exit_code":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"/bin/zsh -lc 'rg --files -uu'","aggregated_output":"foo.txt\n","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"foo.txt is here."}}
{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":20,"cache_creation_input_tokens":3,"output_tokens":7}}
`

	parsed := parseCodexOutput(context.Background(), output)
	trans, threadID := parsed.transcript, parsed.threadID
	finalMsg, inputTokens, outputTokens := parsed.finalMsg, parsed.inputTokens, parsed.outputTokens
	if len(trans) != 4 {
		t.Fatalf("expected 4 transcript messages, got %d", len(trans))
	}
	if threadID != "abc" {
		t.Fatalf("expected thread id abc, got %q", threadID)
	}
	if trans[0].Role != testAssistantRole || trans[0].Content != "I’m checking the workspace first." {
		t.Fatalf("unexpected first message: %+v", trans[0])
	}
	if trans[1].Role != testToolCallRole || trans[1].ToolCall == nil || trans[1].ToolCall.Name != "command_execution" {
		t.Fatalf("unexpected tool call: %+v", trans[1])
	}
	if trans[2].Role != testToolResRole || trans[2].ToolResult == nil || trans[2].ToolResult.Content != "foo.txt\n" {
		t.Fatalf("unexpected tool result: %+v", trans[2])
	}
	if trans[3].Role != testAssistantRole || trans[3].Content != "foo.txt is here." {
		t.Fatalf("unexpected final assistant message: %+v", trans[3])
	}
	if finalMsg != "foo.txt is here." {
		t.Fatalf("expected final message to be parsed, got %q", finalMsg)
	}
	if inputTokens != 123 {
		t.Fatalf("expected input tokens 123, got %d", inputTokens)
	}
	if outputTokens != 7 {
		t.Fatalf("expected output tokens 7, got %d", outputTokens)
	}
	if got := codexTurns(trans); got != 1 {
		t.Fatalf("expected 1 turn, got %d", got)
	}
}

func TestParseCodexOutput_MCPToolCall(t *testing.T) {
	t.Parallel()

	output := `
{"type":"thread.started","thread_id":"abc"}
{"type":"turn.started"}
{"type":"item.started","item":{"id":"item_0","type":"mcp_tool_call","server":"agent-sandbox","tool":"create_sandbox","arguments":{"config":{"language":{"pythonVersion":"3.12"}}},"result":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_0","type":"mcp_tool_call","server":"agent-sandbox","tool":"create_sandbox","arguments":{"config":{"language":{"pythonVersion":"3.12"}}},"result":{"content":[{"type":"text","text":"sandbox created"}]},"status":"completed"}}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"done"}}
`

	parsed := parseCodexOutput(context.Background(), output)
	trans := parsed.transcript
	if len(trans) != 3 {
		t.Fatalf("expected 3 transcript messages, got %d", len(trans))
	}
	if trans[0].Role != testToolCallRole || trans[0].ToolCall == nil {
		t.Fatalf("unexpected tool call message: %+v", trans[0])
	}
	if trans[0].ToolCall.Name != "mcp__agent-sandbox__create_sandbox" {
		t.Fatalf("tool call name = %q", trans[0].ToolCall.Name)
	}
	if trans[1].Role != testToolResRole || trans[1].ToolResult == nil {
		t.Fatalf("unexpected tool result message: %+v", trans[1])
	}
	resultContent, _ := trans[1].ToolResult.Content.(string)
	if !strings.Contains(resultContent, "sandbox created") {
		t.Fatalf("unexpected tool result content: %+v", trans[1].ToolResult.Content)
	}
}

func TestParseCodexOutput_MCPToolCallErrorStatus(t *testing.T) {
	t.Parallel()

	output := `
{"type":"thread.started","thread_id":"abc"}
{"type":"turn.started"}
{"type":"item.started","item":{"id":"item_0","type":"mcp_tool_call","server":"agent-sandbox","tool":"create_sandbox","arguments":{},"result":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_0","type":"mcp_tool_call","server":"agent-sandbox","tool":"create_sandbox","arguments":{},"result":{"content":[{"type":"text","text":"sandbox failed"}]},"status":"failed"}}
`

	parsed := parseCodexOutput(context.Background(), output)
	trans := parsed.transcript
	if len(trans) != 2 {
		t.Fatalf("expected 2 transcript messages, got %d", len(trans))
	}
	if trans[1].Role != testToolResRole || trans[1].ToolResult == nil {
		t.Fatalf("unexpected tool result message: %+v", trans[1])
	}
	if trans[1].ToolResult.Status != toolStatusError {
		t.Fatalf("expected error status, got %q", trans[1].ToolResult.Status)
	}
}

func TestParseCodexOutput_ConcatenatedEvents(t *testing.T) {
	t.Parallel()

	output := `{"type":"thread.started","thread_id":"abc"}{"type":"turn.started"}{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"OK"}}{"type":"turn.completed","usage":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0}}`

	parsed := parseCodexOutput(context.Background(), output)
	if parsed.threadID != "abc" {
		t.Fatalf("expected thread id abc, got %q", parsed.threadID)
	}
	if parsed.finalMsg != "OK" {
		t.Fatalf("expected final message OK, got %q", parsed.finalMsg)
	}
	if got := parsed.transcript.FinalAssistantMessage(); got != "OK" {
		t.Fatalf("expected assistant transcript OK, got %q", got)
	}
}

func TestParseCodexOutput_WarnsOnMalformedConcatenatedEvents(t *testing.T) {
	output := `{"type":"thread.started","thread_id":"abc"}{"type":`

	logOutput := captureStdout(t, func() {
		parsed := parseCodexOutput(context.Background(), output)
		if parsed.threadID != "abc" {
			t.Fatalf("expected thread id abc before malformed event, got %q", parsed.threadID)
		}
	})
	if !strings.Contains(logOutput, "failed to decode JSON event stream line") {
		t.Fatalf("expected JSON decode warning, got %q", logOutput)
	}
}

func TestParseCodexSessionFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	content := `{"timestamp":"2026-04-20T04:05:27.815Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Run the code-stats skill to analyze the current directory."}]}}
{"timestamp":"2026-04-20T04:05:38.215Z","type":"event_msg","payload":{"type":"agent_message","message":"I'm using the code-stats skill.","phase":"commentary"}}
{"timestamp":"2026-04-20T04:05:38.216Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}","call_id":"call_1"}}
{"timestamp":"2026-04-20T04:05:38.436Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"Output:\n/private/tmp/workspace\n"}}
{"timestamp":"2026-04-20T04:05:59.400Z","type":"event_msg","payload":{"type":"agent_message","message":"# Code Statistics","phase":"final_answer"}}
{"timestamp":"2026-04-20T04:05:59.418Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":61324,"cached_input_tokens":56448,"output_tokens":1115}}}}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	parsed := parseCodexSessionFile(sessionPath)
	trans := parsed.transcript
	if len(trans) != 5 {
		t.Fatalf("expected 5 transcript messages, got %d", len(trans))
	}

	for _, tc := range []struct {
		idx     int
		role    transcript.Role
		content string
	}{
		{0, transcript.RoleUser, "Run the code-stats skill to analyze the current directory."},
		{1, testAssistantRole, "I'm using the code-stats skill."},
		{4, testAssistantRole, "# Code Statistics"},
	} {
		if trans[tc.idx].Role != tc.role {
			t.Fatalf("trans[%d]: expected role %q, got %q", tc.idx, tc.role, trans[tc.idx].Role)
		}
		if trans[tc.idx].Content != tc.content {
			t.Fatalf("trans[%d]: expected content %q, got %q", tc.idx, tc.content, trans[tc.idx].Content)
		}
	}
	if trans[0].Turn != 1 {
		t.Fatalf("trans[0]: expected Turn 1, got %d", trans[0].Turn)
	}
	if trans[2].ToolCall == nil {
		t.Fatalf("trans[2]: expected ToolCall")
	}
	if trans[2].ToolCall.ID != "call_1" {
		t.Fatalf("trans[2]: expected ToolCall.ID 'call_1', got %q", trans[2].ToolCall.ID)
	}
	if trans[3].ToolResult == nil {
		t.Fatalf("trans[3]: expected ToolResult")
	}
	if trans[3].ToolResult.CallID != "call_1" {
		t.Fatalf("trans[3]: expected ToolResult.CallID 'call_1', got %q", trans[3].ToolResult.CallID)
	}
	if parsed.finalMsg != "# Code Statistics" {
		t.Fatalf("expected final message from session, got %q", parsed.finalMsg)
	}
	if parsed.inputTokens != 117772 {
		t.Fatalf("expected input tokens 117772, got %d", parsed.inputTokens)
	}
	if parsed.outputTokens != 1115 {
		t.Fatalf("expected output tokens 1115, got %d", parsed.outputTokens)
	}
}

func TestParseCodexSessionFile_ToolErrorStatus(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session-error.jsonl")
	content := `{"timestamp":"2026-04-20T04:05:27.815Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Run the command."}]}}
{"timestamp":"2026-04-20T04:05:38.216Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"false\"}","call_id":"call_err"}}
{"timestamp":"2026-04-20T04:05:38.436Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_err","output":"Chunk ID: abc\nProcess exited with code 1\nOutput:\nboom\n"}}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	parsed := parseCodexSessionFile(sessionPath)
	trans, finalMsg := parsed.transcript, parsed.finalMsg
	inputTokens, outputTokens := parsed.inputTokens, parsed.outputTokens
	_ = finalMsg
	_ = inputTokens
	_ = outputTokens
	if len(trans) != 3 {
		t.Fatalf("expected 3 transcript messages, got %d", len(trans))
	}
	if trans[2].Role != testToolResRole || trans[2].ToolResult == nil {
		t.Fatalf("unexpected tool result: %+v", trans[2])
	}
	if trans[2].ToolResult.Status != testStatusError {
		t.Fatalf("expected tool result status error, got %q", trans[2].ToolResult.Status)
	}
}

func TestCodexToolResultStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "missing marker defaults to success",
			output: "Output:\nhello\n",
			want:   toolStatusSuccess,
		},
		{
			name:   "multiple markers uses last one",
			output: "Process exited with code 1\nnoise\nProcess exited with code 0\n",
			want:   toolStatusSuccess,
		},
		{
			name:   "multi digit exit code is error",
			output: "Chunk\nProcess exited with code 12\nOutput:\nboom\n",
			want:   toolStatusError,
		},
		{
			name:   "trailing text after exit code still parses",
			output: "Chunk\nProcess exited with code 7 after retry\n",
			want:   toolStatusError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := codexToolResultStatus(tt.output)
			if got != tt.want {
				t.Fatalf("codexToolResultStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCodexRun_PreservesArtifactsOnNonZeroExit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "rollout-thread-123.jsonl")
	sessionContent := `{"timestamp":"2026-04-20T04:05:27.815Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Inspect the repo."}]}}
{"timestamp":"2026-04-20T04:05:38.216Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"false\"}","call_id":"call_err"}}
{"timestamp":"2026-04-20T04:05:38.436Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_err","output":"Chunk ID: abc\nProcess exited with code 1\nOutput:\nboom\n"}}`
	if err := os.WriteFile(sessionPath, []byte(sessionContent), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	rt := &codexTestRuntime{
		workspace:    dir,
		sessionPath:  sessionPath,
		sessionBytes: []byte(sessionContent),
		execResult: runtime.ExecResult{
			Stdout: `{"type":"thread.started","thread_id":"thread-123"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"I hit a problem."}}
{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":2,"cache_creation_input_tokens":1,"output_tokens":3}}`,
			Stderr:   "codex failed",
			ExitCode: 1,
		},
	}

	ag := NewCodexAgent(Config{})
	sessionResult, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{
		Role:    transcript.RoleUser,
		Content: "Inspect the repo.",
		Turn:    1,
	}})
	if err == nil {
		t.Fatal("expected run error")
	}
	if sessionResult == nil {
		t.Fatal("expected session result")
		return
	}
	if sessionResult.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", sessionResult.ExitCode)
	}
	if sessionResult.Stderr != "codex failed" {
		t.Fatalf("unexpected stderr: %q", sessionResult.Stderr)
	}
	if len(sessionResult.Transcript) == 0 {
		t.Fatal("expected transcript on non-zero exit")
	}
	if sessionResult.Artifacts == nil {
		t.Fatal("expected artifacts")
	}
	if len(sessionResult.Artifacts.GeneratedFiles) != 2 {
		t.Fatalf("expected 2 generated files, got %v", sessionResult.Artifacts.GeneratedFiles)
	}
	if sessionResult.Artifacts.GeneratedFiles[0] != "stdout.json" {
		t.Fatalf("expected stdout.json artifact, got %v", sessionResult.Artifacts.GeneratedFiles)
	}
	if sessionResult.Artifacts.GeneratedFiles[1] != sessionPath {
		t.Fatalf("expected session artifact %q, got %v", sessionPath, sessionResult.Artifacts.GeneratedFiles)
	}
	if sessionResult.Transcript[len(sessionResult.Transcript)-1].Role != testToolResRole {
		t.Fatalf("expected final transcript message to be tool result, got %+v", sessionResult.Transcript[len(sessionResult.Transcript)-1])
	}
}

func TestCodexRun_MergesConfiguredEnvVars(t *testing.T) {
	t.Parallel()

	configAPIKey := strings.Join([]string{"openai", "config", "token"}, "-")
	rt := &codexTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			Stdout: `{"type":"thread.started","thread_id":"thread-123"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"done"}}
{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":1}}`,
			ExitCode: 0,
		},
	}

	ag := NewCodexAgent(Config{
		APIKey:  configAPIKey,
		BaseURL: "https://cfg-base",
		EnvVars: map[string]string{
			"CODEX_TEST_FLAG": "cfg-flag",
		},
	})
	_, err := ag.Run(context.Background(), rt, ExecOptions{
		Env: map[string]string{
			"OPENAI_BASE_URL": "https://override-base",
			"EXTRA_FLAG":      "1",
		},
	}, []transcript.Message{{
		Role:    transcript.RoleUser,
		Content: "Inspect the repo.",
		Turn:    1,
	}})
	if err != nil {
		t.Fatalf("run codex: %v", err)
	}
	if rt.lastExecEnv["CODEX_TEST_FLAG"] != "cfg-flag" {
		t.Fatalf("expected CODEX_TEST_FLAG to be merged, got %q", rt.lastExecEnv["CODEX_TEST_FLAG"])
	}
	if rt.lastExecEnv["OPENAI_API_KEY"] != configAPIKey {
		t.Fatalf("expected typed API key to be converted into env, got %q", rt.lastExecEnv["OPENAI_API_KEY"])
	}
	if rt.lastExecEnv["OPENAI_BASE_URL"] != "https://override-base" {
		t.Fatalf("expected opts env to override OPENAI_BASE_URL, got %q", rt.lastExecEnv["OPENAI_BASE_URL"])
	}
	if rt.lastExecEnv["EXTRA_FLAG"] != "1" {
		t.Fatalf("expected EXTRA_FLAG to be preserved, got %q", rt.lastExecEnv["EXTRA_FLAG"])
	}
}

func TestCodexRun_SandboxFlagHonoursKwargAndRuntime(t *testing.T) {
	t.Parallel()

	stdout := `{"type":"thread.started","thread_id":"thread-123"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"done"}}
{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":1}}`

	cases := []struct {
		name        string
		workspace   string // codexTestRuntime returns RequiresProcessSandbox() == (workspace != "opensandbox")
		kwargs      map[string]string
		wantFlag    string
		notWantFlag string
	}{
		{
			name:        "none runtime, no kwarg -> auto workspace-write",
			workspace:   t.TempDir(),
			kwargs:      nil,
			wantFlag:    "--sandbox workspace-write",
			notWantFlag: "--dangerously-bypass-approvals-and-sandbox",
		},
		{
			name:        "none runtime, bypass_sandbox=true -> forced bypass",
			workspace:   t.TempDir(),
			kwargs:      map[string]string{KwargBypassSandbox: "true"},
			wantFlag:    "--dangerously-bypass-approvals-and-sandbox",
			notWantFlag: "--sandbox workspace-write",
		},
		{
			name:        "opensandbox runtime, no kwarg -> bypass (unchanged)",
			workspace:   "opensandbox",
			kwargs:      nil,
			wantFlag:    "--dangerously-bypass-approvals-and-sandbox",
			notWantFlag: "--sandbox workspace-write",
		},
		{
			name:        "none runtime, bypass_sandbox=garbage -> auto workspace-write (ParseBool fails)",
			workspace:   t.TempDir(),
			kwargs:      map[string]string{KwargBypassSandbox: "garbage"},
			wantFlag:    "--sandbox workspace-write",
			notWantFlag: "--dangerously-bypass-approvals-and-sandbox",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := &codexTestRuntime{
				workspace:  tc.workspace,
				execResult: runtime.ExecResult{Stdout: stdout, ExitCode: 0},
			}
			ag := NewCodexAgent(Config{Kwargs: tc.kwargs})
			if _, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{
				Role:    transcript.RoleUser,
				Content: "hi",
				Turn:    1,
			}}); err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if !containsCommand(rt.commands, tc.wantFlag) {
				t.Fatalf("no command contains %q; commands=%v", tc.wantFlag, rt.commands)
			}
			if containsCommand(rt.commands, tc.notWantFlag) {
				t.Fatalf("commands unexpectedly contain %q; commands=%v", tc.notWantFlag, rt.commands)
			}
		})
	}
}

func TestCodexRun_KeepsStreamFinalMessageWhenSessionHasNoFinalMessage(t *testing.T) {
	t.Parallel()

	rt := &codexTestRuntime{
		workspace:   t.TempDir(),
		sessionPath: "/tmp/codex-session.jsonl",
		sessionBytes: []byte(`{"timestamp":"2026-04-20T04:05:27.815Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Return exactly OK."}]}}
{"timestamp":"2026-04-20T04:05:28.216Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}","call_id":"call_1"}}
	{"timestamp":"2026-04-20T04:05:28.436Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"Output:\n/workspace\n"}}
	{"timestamp":"2026-04-20T04:05:29.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":99,"cached_input_tokens":1,"output_tokens":88}}}}`),
		execResult: runtime.ExecResult{
			ExitCode: 0,
			Stdout: `{"type":"thread.started","thread_id":"thread-123"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"OK"}}
{"type":"turn.completed","usage":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0}}`,
		},
	}
	ag := NewCodexAgent(Config{})

	sessionResult, err := ag.Run(context.Background(), rt, ExecOptions{ArtifactDir: t.TempDir()}, []transcript.Message{
		{Role: transcript.RoleUser, Content: "Return exactly OK.", Turn: 1},
	})
	if err != nil {
		t.Fatalf("run codex: %v", err)
	}
	if sessionResult.FinalMessage != "OK" {
		t.Fatalf("expected stream final message to be preserved, got %q", sessionResult.FinalMessage)
	}
	if got := sessionResult.Transcript.FinalAssistantMessage(); got != "OK" {
		t.Fatalf("expected stream assistant transcript to be preserved, got %q", got)
	}
	if sessionResult.InputTokens != 0 || sessionResult.OutputTokens != 0 {
		t.Fatalf("expected stream token counts to be preserved, got %d/%d", sessionResult.InputTokens, sessionResult.OutputTokens)
	}
}

func TestCodexRun_UsesOutputLastMessageWhenJSONLHasNoFinalMessage(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	lastMessagePath := filepath.Join(workspace, ".skill-up", "codex-last-message.txt")
	rt := &codexTestRuntime{
		workspace:        workspace,
		execResult:       runtime.ExecResult{Stdout: `{"type":"thread.started","thread_id":"abc"}` + "\n", ExitCode: 0},
		lastMessagePath:  lastMessagePath,
		lastMessageBytes: []byte("from last message file\n"),
	}
	ag := NewCodexAgent(Config{ModelName: "gpt-5.4"})

	sessionResult, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{
		Role:    transcript.RoleUser,
		Content: "hello",
		Turn:    1,
	}})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !containsCommand(rt.commands, "--output-last-message "+shellQuote(lastMessagePath)) {
		t.Fatalf("run command missing output-last-message path:\n%s", rt.lastCommand)
	}
	if sessionResult.FinalMessage != "from last message file" {
		t.Fatalf("FinalMessage = %q, want last message file", sessionResult.FinalMessage)
	}
	if got := sessionResult.Transcript.FinalAssistantMessage(); got != "from last message file" {
		t.Fatalf("transcript final = %q, want last message file", got)
	}
}

func TestDownloadSessionArtifact_WithArtifactDirDoesNotRegisterGeneratedFile(t *testing.T) {
	t.Parallel()

	sessionPath := "/tmp/codex-session.jsonl"
	sessionContent := `{"type":"response_item","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`
	rt := &codexTestRuntime{
		workspace:    t.TempDir(),
		sessionPath:  sessionPath,
		sessionBytes: []byte(sessionContent),
	}
	artifactDir := t.TempDir()

	artifactPath, registeredPath, cleanup, ok := downloadSessionArtifact(context.Background(), rt, artifactDir, sessionPath)
	defer cleanup()
	if !ok {
		t.Fatal("expected downloadSessionArtifact to succeed")
	}
	if registeredPath != "" {
		t.Fatalf("expected no registered path for local artifact dir download, got %q", registeredPath)
	}
	if artifactPath != filepath.Join(artifactDir, filepath.Base(sessionPath)) {
		t.Fatalf("unexpected artifact path: %q", artifactPath)
	}
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("expected downloaded artifact: %v", err)
	}
	if string(data) != sessionContent {
		t.Fatalf("unexpected artifact content: %q", string(data))
	}
}

func TestCodexRun_PropagatesObservabilityEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=test")

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}))

	rt := &codexTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			Stdout: `{"type":"thread.started","thread_id":"thread-123"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"done"}}`,
			ExitCode: 0,
		},
	}
	ag := NewCodexAgent(Config{
		ModelProvider: agentProviderOpenAI,
		ModelName:     "gpt-test",
	})

	_, err = ag.Run(ctx, rt, ExecOptions{}, []transcript.Message{{
		Role:    transcript.RoleUser,
		Content: "Inspect the repo.",
		Turn:    1,
	}})
	if err != nil {
		t.Fatalf("run codex: %v", err)
	}

	if rt.lastExecEnv["TRACEPARENT"] == "" {
		t.Fatalf("expected TRACEPARENT in agent env, got %v", rt.lastExecEnv)
	}
	if rt.lastExecEnv["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://collector:4318" {
		t.Fatalf("expected OTEL endpoint propagation, got %q", rt.lastExecEnv["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
	resourceAttrs := rt.lastExecEnv["OTEL_RESOURCE_ATTRIBUTES"]
	for _, want := range []string{
		"deployment.environment=test",
		"skill_up.engine=codex",
		"skill_up.model=openai/gpt-test",
		"skill_up.parent_trace_id=4bf92f3577b34da6a3ce929d0e0e4736",
	} {
		if !strings.Contains(resourceAttrs, want) {
			t.Fatalf("expected resource attrs to contain %q, got %q", want, resourceAttrs)
		}
	}
}

type codexTestRuntime struct {
	workspace           string
	sessionPath         string
	sessionBytes        []byte
	lastMessagePath     string
	lastMessageBytes    []byte
	execResult          runtime.ExecResult
	commands            []string
	lastCommand         string
	lastExecEnv         map[string]string
	probeResponseStdout string
	mergedEnv           map[string]string
}

func (r *codexTestRuntime) Create(context.Context) error { return nil }
func (r *codexTestRuntime) Close() error                 { return nil }
func (r *codexTestRuntime) Start(context.Context) error  { return nil }
func (r *codexTestRuntime) Stop(context.Context) error   { return nil }
func (r *codexTestRuntime) UploadFile(context.Context, string, string) error {
	return nil
}

func (r *codexTestRuntime) UploadDir(context.Context, string, string) error {
	return nil
}

func (r *codexTestRuntime) DownloadFile(_ context.Context, sourcePath, targetPath string) error {
	if sourcePath == r.lastMessagePath {
		return os.WriteFile(targetPath, r.lastMessageBytes, 0o600)
	}
	if sourcePath != r.sessionPath {
		return fmt.Errorf("unexpected download path: %s", sourcePath)
	}
	return os.WriteFile(targetPath, r.sessionBytes, 0o600)
}
func (r *codexTestRuntime) DownloadDir(context.Context, string, string) error { return nil }
func (r *codexTestRuntime) Exec(_ context.Context, command string, opts runtime.ExecOptions) (runtime.ExecResult, error) {
	// Probe calls (agent.Install via probeAndMergePATH) get a canned
	// literal PATH and are NOT recorded as a real command. Exact-match
	// the probe constant so unrelated `printf '%s' "$HOME/..."` tests
	// aren't silently intercepted.
	if command == codexExecPathProbeCmd {
		stdout := r.probeResponseStdout
		if stdout == "" {
			stdout = "/fake/.local/bin:/fake/.nvm/current/bin:/usr/bin"
		}
		return runtime.ExecResult{Stdout: stdout}, nil
	}
	// ensureNodeRuntime emits a script whose first conditional short-circuits
	// when codex is already on PATH. Treat it as a no-op success so the
	// subsequent agent-command Exec is what tests observe via lastCommand /
	// execResult — otherwise the bootstrap call would consume the configured
	// non-zero exit codes meant for the codex invocation.
	if strings.Contains(command, "if command -v 'codex' >/dev/null 2>&1; then exit 0; fi") {
		return runtime.ExecResult{ExitCode: 0}, nil
	}
	r.commands = append(r.commands, command)
	r.lastCommand = command
	if strings.Contains(command, "SKILL_UP_CODEX_THREAD_ID") {
		if opts.Env["SKILL_UP_CODEX_THREAD_ID"] != "thread-123" {
			return runtime.ExecResult{}, fmt.Errorf("unexpected thread id: %q", opts.Env["SKILL_UP_CODEX_THREAD_ID"])
		}
		return runtime.ExecResult{Stdout: r.sessionPath}, nil
	}
	r.lastExecEnv = mapsClone(opts.Env)
	return r.execResult, nil
}

func containsCommand(commands []string, want string) bool {
	for _, cmd := range commands {
		if strings.Contains(cmd, want) {
			return true
		}
	}
	return false
}
func (r *codexTestRuntime) Workspace() string { return r.workspace }
func (r *codexTestRuntime) RequiresProcessSandbox() bool {
	return r.workspace != "opensandbox"
}

func (r *codexTestRuntime) MergeEnv(env map[string]string) {
	if r.mergedEnv == nil {
		r.mergedEnv = make(map[string]string, len(env))
	}
	maps.Copy(r.mergedEnv, env)
}

func (r *codexTestRuntime) Shell() platform.Shell {
	return platform.Shell{GOOS: platform.GOOSLinux, Family: platform.ShellPOSIX}
}
