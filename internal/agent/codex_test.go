package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/platform"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

func TestCodexArtifactTransferContextDetachesAndExceedsCleanupTimeout(t *testing.T) {
	t.Parallel()

	parent, parentCancel := context.WithCancel(context.Background())
	parentCancel()
	transferCtx, transferCancel := codexArtifactTransferContext(parent, codexDefaultOutputBytes)
	defer transferCancel()

	if err := transferCtx.Err(); err != nil {
		t.Fatalf("transfer context inherited parent cancellation: %v", err)
	}
	deadline, ok := transferCtx.Deadline()
	if !ok {
		t.Fatal("transfer context has no deadline")
	}
	if remaining := time.Until(deadline); remaining <= sessionCleanupTimeout {
		t.Fatalf("transfer timeout %s must exceed cleanup timeout %s", remaining, sessionCleanupTimeout)
	}
}

func TestPrepareCodexRunArtifactsUsesTargetPathSemantics(t *testing.T) {
	t.Parallel()

	// Simulate a Windows host path paired with a Linux remote runtime. The
	// generated path must follow the runtime shell, not host filepath rules.
	rt := &codexTestRuntime{workspace: `C:\host\workspace`}
	dir, lastMessagePath, stdoutPath, err := prepareCodexRunArtifacts(context.Background(), rt)
	if err != nil {
		t.Fatalf("prepareCodexRunArtifacts: %v", err)
	}
	if !strings.HasPrefix(dir, "/tmp/"+codexRunArtifactPrefix) {
		t.Fatalf("artifact dir = %q, want Linux runtime temp path", dir)
	}
	if lastMessagePath != path.Join(dir, "last-message.txt") || stdoutPath != path.Join(dir, codexStdoutArtifactPath) {
		t.Fatalf("artifact paths = %q/%q, want children of %q", lastMessagePath, stdoutPath, dir)
	}
}

const (
	testAssistantRole = "assistant"
	testToolCallRole  = "tool_call"
	testToolResRole   = "tool_result"
	testStatusError   = "error"
	testCommandCallID = "exec-2"
	testJudgeJSON     = `{"results":[]}`
	testThreadID      = "abc"
)

func TestNewCodexAgent(t *testing.T) { //nolint:dupl // mirrors constructor defaults asserted for Qwen Code
	t.Parallel()

	ag := NewCodexAgent(Config{})

	if ag.Name() != "codex" {
		t.Fatalf("expected name codex, got %s", ag.Name())
	}
	if ag.Cfg.CheckCmd != "command -v codex" {
		t.Fatalf("expected codex check cmd, got %s", ag.Cfg.CheckCmd)
	}
	if ag.Cfg.VersionCmd != "codex --version" {
		t.Fatalf("expected codex version cmd, got %s", ag.Cfg.VersionCmd)
	}
	if ag.Cfg.SkillPath != ".codex/skills" {
		t.Fatalf("expected codex skill path, got %s", ag.Cfg.SkillPath)
	}
}

func TestCodexInstall_ConfiguredVersion(t *testing.T) {
	t.Parallel()

	cmd := defaultCodexInstallCmdForVersion("1.2.3")
	if !strings.Contains(cmd, "'@openai/codex@1.2.3'") {
		t.Fatalf("install command does not use configured version:\n%s", cmd)
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

func TestCodexCheckCredentials_LogsTokenPresenceAndBaseURL(t *testing.T) {
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
	if !strings.Contains(output, `level=DEBUG msg="OPENAI_API_KEY detected for codex (source=agent_config)"`) {
		t.Fatalf("expected api key log, got %q", output)
	}
	if strings.Contains(output, apiKey) || strings.Contains(output, "****") {
		t.Fatalf("key material leaked into log: %q", output)
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

func TestCodexAppliedModelName_IgnoresNonOpenAIProvider(t *testing.T) {
	t.Parallel()

	ag := NewCodexAgent(Config{
		ModelProvider: "anthropic",
		ModelName:     "claude-sonnet-4-6",
	})

	if got := ag.appliedModelName(context.Background()); got != "" {
		t.Fatalf("appliedModelName() = %q, want empty", got)
	}
}

func TestCodexAppliedModelName_UsesCustomProviderModel(t *testing.T) {
	t.Parallel()

	ag := NewCodexAgent(Config{
		ModelProvider: "dashscope",
		ModelName:     "qwen3.6-plus",
		BaseURL:       "https://example.com/compatible-mode/v1",
	})

	if got := ag.appliedModelName(context.Background()); got != "qwen3.6-plus" {
		t.Fatalf("appliedModelName() = %q, want qwen3.6-plus", got)
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

	if got := ag.appliedModelName(context.Background()); got != "" {
		t.Fatalf("appliedModelName() = %q, want empty for invalid provider name", got)
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

	cmd := buildCodexRunCmd("hello world", ag.appliedModelName(context.Background()), ag.runProviderConfig(context.Background()), codexBypassSandbox)
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
		"",
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
	if threadID != testThreadID {
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

func TestParseCodexOutput_MCPToolCallFailureKeepsErrorMessage(t *testing.T) {
	t.Parallel()

	output := `
{"type":"thread.started","thread_id":"abc"}
{"type":"turn.started"}
{"type":"item.started","item":{"id":"item_0","type":"mcp_tool_call","server":"shop","tool":"get_order","arguments":{"order_id":42},"result":null,"error":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_0","type":"mcp_tool_call","server":"shop","tool":"get_order","arguments":{"order_id":42},"result":null,"error":{"message":"MCP tool call requires approval"},"status":"failed"}}
`

	trans := parseCodexOutput(context.Background(), output).transcript
	if len(trans) != 2 {
		t.Fatalf("expected 2 transcript messages, got %d", len(trans))
	}
	result := trans[1].ToolResult
	if result == nil || result.Status != toolStatusError {
		t.Fatalf("expected failed tool result, got %+v", trans[1])
	}
	if result.Content != "MCP tool call requires approval" {
		t.Fatalf("tool result content = %q, want the error message", result.Content)
	}
}

func TestParseCodexOutput_ConcatenatedEvents(t *testing.T) {
	t.Parallel()

	output := `{"type":"thread.started","thread_id":"abc"}{"type":"turn.started"}{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"OK"}}{"type":"turn.completed","usage":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0}}`

	parsed := parseCodexOutput(context.Background(), output)
	if parsed.threadID != testThreadID {
		t.Fatalf("expected thread id abc, got %q", parsed.threadID)
	}
	if parsed.finalMsg != "OK" {
		t.Fatalf("expected final message OK, got %q", parsed.finalMsg)
	}
	if got := parsed.transcript.FinalAssistantMessage(); got != "OK" {
		t.Fatalf("expected assistant transcript OK, got %q", got)
	}
}

func TestParseCodexOutput_LargeJSONLRecordDoesNotTruncateFinalMessage(t *testing.T) {
	t.Parallel()

	largeOutput := strings.Repeat("x", 1024*1024)
	output := strings.Join([]string{
		`{"type":"thread.started","thread_id":"abc"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"id":"progress","type":"agent_message","text":"still checking"}}`,
		fmt.Sprintf(`{"type":"item.completed","item":{"id":"command","type":"command_execution","command":"rg .","aggregated_output":%q,"exit_code":0,"status":"completed"}}`, largeOutput),
		`{"type":"item.completed","item":{"id":"final","type":"agent_message","text":"{\"results\":[]}"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}`,
	}, "\n")

	parsed := parseCodexOutput(context.Background(), output)
	if parsed.finalMsg != testJudgeJSON {
		t.Fatalf("final message = %q, want Judge JSON", parsed.finalMsg)
	}
	if got := parsed.transcript.FinalAssistantMessage(); got != testJudgeJSON {
		t.Fatalf("transcript final = %q, want Judge JSON", got)
	}
}

func TestParseCodexOutput_JSONLRecordLimit(t *testing.T) {
	t.Parallel()

	line := `{"type":"thread.started","thread_id":"abc"}`
	for _, tc := range []struct {
		name    string
		limit   int
		wantErr bool
	}{
		{name: "record at limit", limit: len(line)},
		{name: "record over limit", limit: len(line) - 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parsed := parseCodexOutputWithLimit(context.Background(), strings.NewReader(line), tc.limit)
			if tc.wantErr {
				if parsed.err == nil || !strings.Contains(parsed.err.Error(), "record 1 exceeds maximum size") {
					t.Fatalf("error = %v, want explicit record limit error", parsed.err)
				}
				return
			}
			if parsed.err != nil {
				t.Fatalf("parse record at limit: %v", parsed.err)
			}
			if parsed.threadID != testThreadID {
				t.Fatalf("thread id = %q, want abc", parsed.threadID)
			}
		})
	}
}

func TestParseCodexOutput_WarnsOnMalformedConcatenatedEvents(t *testing.T) {
	output := `{"type":"thread.started","thread_id":"abc"}{"type":`

	logOutput := captureStdout(t, func() {
		parsed := parseCodexOutput(context.Background(), output)
		if parsed.threadID != testThreadID {
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

func TestParseCodexSessionFile_MCPToolCallItem(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session-mcp.jsonl")
	content := `{"timestamp":"2026-11-01T07:11:40.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"What is the status of order 42?"}]}}
{"timestamp":"2026-11-01T07:11:45.000Z","type":"response_item","payload":{"type":"custom_tool_call","status":"completed","call_id":"call_1","name":"exec","input":"text(await tools.mcp__shop__get_order({order_id:42}));"}}
{"timestamp":"2026-11-01T07:11:45.100Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"exec-1","server":"shop","tool":"get_order","arguments":{"order_id":42},"status":"completed","result":{"content":[{"type":"text","text":"{\"status\":\"SHIPPED-7F3Q\"}"}]}}}}
{"timestamp":"2026-11-01T07:11:45.200Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_1","output":[{"type":"input_text","text":"Script completed"}]}}
{"timestamp":"2026-11-01T07:11:45.300Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"exec-2","command":["/bin/zsh","-lc","cat SKILL.md"],"exit_code":0,"aggregated_output":"Call get_order.\n","status":"completed"}}}
{"timestamp":"2026-11-01T07:11:45.400Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"msg-1","content":[{"type":"Text","text":"skipped"}]}}}
{"timestamp":"2026-11-01T07:11:46.000Z","type":"event_msg","payload":{"type":"agent_message","message":"Order 42 is SHIPPED-7F3Q.","phase":"final_answer"}}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	parsed := parseCodexSessionFile(sessionPath)
	if parsed.err != nil {
		t.Fatalf("parse session file: %v", parsed.err)
	}
	trans := parsed.transcript
	if len(trans) != 6 {
		t.Fatalf("expected 6 transcript messages, got %d: %+v", len(trans), trans)
	}
	assertCodexShopGetOrderPair(t, trans[1], trans[2], toolStatusSuccess, `{"content":[{"text":"{\"status\":\"SHIPPED-7F3Q\"}","type":"text"}]}`)
	cmdCall, cmdResult := trans[3].ToolCall, trans[4].ToolResult
	if cmdCall == nil || cmdCall.Name != codexCommandToolName || cmdCall.ID != testCommandCallID || cmdCall.Arguments["command"] != "/bin/zsh -lc 'cat SKILL.md'" {
		t.Fatalf("trans[3]: expected command_execution exec-2, got %+v", trans[3])
	}
	if cmdResult == nil || cmdResult.CallID != testCommandCallID || cmdResult.Status != toolStatusSuccess || cmdResult.Content != "Call get_order.\n" {
		t.Fatalf("trans[4]: expected successful exec-2 result, got %+v", trans[4])
	}
	if parsed.finalMsg != "Order 42 is SHIPPED-7F3Q." {
		t.Fatalf("final message = %q", parsed.finalMsg)
	}
}

func TestParseCodexSessionFile_NormalizesDirectFunctionCalls(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session-direct-call.jsonl")
	content := `{"timestamp":"2026-11-01T07:00:00.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"List files."}]}}
{"timestamp":"2026-11-01T07:00:01.000Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls\"}","call_id":"call_9"}}
{"timestamp":"2026-11-01T07:00:01.500Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"call_9","command":["/bin/zsh","-lc","ls"],"exit_code":1,"aggregated_output":"boom","status":"failed"}}}
{"timestamp":"2026-11-01T07:00:02.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_9","output":"Process exited with code 1\nOutput:\nboom\n"}}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	trans := parseCodexSessionFile(sessionPath).transcript
	if len(trans) != 3 {
		t.Fatalf("expected the direct call once (3 messages), got %d: %+v", len(trans), trans)
	}
	call := trans[1].ToolCall
	if call == nil || call.ID != "call_9" || call.Name != codexCommandToolName || call.Arguments["command"] != "/bin/zsh -lc ls" {
		t.Fatalf("trans[1]: expected the direct call in the stream's command_execution form, got %+v", trans[1])
	}
	if trans[2].ToolResult == nil || trans[2].ToolResult.CallID != "call_9" || trans[2].ToolResult.Status != testStatusError ||
		trans[2].ToolResult.Content != "Process exited with code 1\nOutput:\nboom\n" {
		t.Fatalf("trans[2]: expected the function_call_output content with error status, got %+v", trans[2])
	}
}

// A direct call whose item failed without an exit code stays failed even when
// its function_call_output carries no exit-code marker, in either line order.
func TestParseCodexSessionFile_DirectCallItemFailureWins(t *testing.T) {
	t.Parallel()

	call := `{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"./run.sh\"}","call_id":"call_5"}}`
	item := `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"call_5","command":["/bin/zsh","-lc","./run.sh"],"exit_code":null,"aggregated_output":"","status":"failed"}}}`
	output := `{"type":"response_item","payload":{"type":"function_call_output","call_id":"call_5","output":"exec failed: sandbox denied"}}`
	for name, lines := range map[string][]string{
		"item before output": {call, item, output},
		"output before item": {call, output, item},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(sessionPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
				t.Fatalf("write session file: %v", err)
			}
			trans := parseCodexSessionFile(sessionPath).transcript
			if len(trans) != 2 || trans[1].ToolResult == nil {
				t.Fatalf("expected one call and one result, got %+v", trans)
			}
			if got := trans[1].ToolResult; got.Status != toolStatusError || got.Content != "exec failed: sandbox denied" {
				t.Fatalf("result = %+v, want error status with the function_call_output content", got)
			}
		})
	}
}

func TestCodexCommandFailedBeforeStartIsAnError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session-not-started.jsonl")
	content := `{"timestamp":"2026-11-01T07:00:00.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Run it."}]}}
{"timestamp":"2026-11-01T07:00:01.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"exec-7","command":["/bin/zsh","-lc","./run.sh"],"exit_code":null,"aggregated_output":"","status":"failed"}}}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	trans := parseCodexSessionFile(sessionPath).transcript
	if len(trans) != 3 || trans[2].ToolResult == nil || trans[2].ToolResult.Status != toolStatusError {
		t.Fatalf("session: expected a failed command result, got %+v", trans)
	}

	output := `{"type":"thread.started","thread_id":"abc"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"/bin/zsh -lc ./run.sh","aggregated_output":"","exit_code":null,"status":"failed"}}`
	streamTrans := parseCodexOutput(context.Background(), output).transcript
	if len(streamTrans) != 2 || streamTrans[1].ToolResult == nil || streamTrans[1].ToolResult.Status != toolStatusError {
		t.Fatalf("stream: expected a failed command result, got %+v", streamTrans)
	}
}

func TestParseCodexSessionFile_MCPToolCallItemFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session-mcp-failed.jsonl")
	content := `{"timestamp":"2026-11-01T07:09:27.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"What is the status of order 42?"}]}}
{"timestamp":"2026-11-01T07:09:30.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"exec-1","server":"shop","tool":"get_order","arguments":{"order_id":42},"status":"failed","error":{"message":"MCP tool call requires approval"}}}}
{"timestamp":"2026-11-01T07:09:31.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"exec-2","command":["/bin/zsh","-lc","cat missing.txt"],"exit_code":1,"aggregated_output":"cat: missing.txt: No such file or directory\n","status":"failed"}}}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	trans := parseCodexSessionFile(sessionPath).transcript
	if len(trans) != 5 {
		t.Fatalf("expected 5 transcript messages, got %d: %+v", len(trans), trans)
	}
	assertCodexShopGetOrderPair(t, trans[1], trans[2], toolStatusError, "MCP tool call requires approval")
	if trans[3].ToolCall == nil || trans[3].ToolCall.ID != testCommandCallID || trans[3].ToolCall.Arguments["command"] != "/bin/zsh -lc 'cat missing.txt'" {
		t.Fatalf("trans[3]: expected command_execution exec-2, got %+v", trans[3])
	}
	result := trans[4].ToolResult
	if result == nil || result.CallID != testCommandCallID || result.Status != toolStatusError || result.Content != "cat: missing.txt: No such file or directory\n" {
		t.Fatalf("trans[4]: expected failed exec-2 result with its output, got %+v", trans[4])
	}
}

func assertCodexShopGetOrderPair(t *testing.T, callMsg, resultMsg transcript.Message, wantStatus, wantContent string) {
	t.Helper()

	call := callMsg.ToolCall
	if callMsg.Role != testToolCallRole || call == nil || callMsg.Turn != 1 {
		t.Fatalf("expected turn 1 tool call, got %+v", callMsg)
	}
	if call.Name != "mcp__shop__get_order" || call.ID != "exec-1" {
		t.Fatalf("tool call = %q (id %q), want mcp__shop__get_order (id exec-1)", call.Name, call.ID)
	}
	if got, ok := call.Arguments["order_id"].(float64); !ok || got != 42 {
		t.Fatalf("tool call arguments = %+v, want order_id 42", call.Arguments)
	}
	result := resultMsg.ToolResult
	if resultMsg.Role != testToolResRole || result == nil {
		t.Fatalf("expected tool result, got %+v", resultMsg)
	}
	if result.CallID != "exec-1" || result.Status != wantStatus || result.Content != wantContent {
		t.Fatalf("tool result = %+v, want call exec-1, status %q, content %q", result, wantStatus, wantContent)
	}
}

func TestParseCodexSessionFile_LargeJSONLRecordDoesNotTruncateFinalMessage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "large-session.jsonl")
	content := strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"agent_message","message":"still checking","phase":"commentary"}}`,
		fmt.Sprintf(`{"type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":%q}}`, strings.Repeat("x", 1024*1024)),
		`{"type":"event_msg","payload":{"type":"agent_message","message":"{\"results\":[]}","phase":"final_answer"}}`,
	}, "\n")
	if err := os.WriteFile(sessionPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	parsed := parseCodexSessionFile(sessionPath)
	if parsed.err != nil {
		t.Fatalf("parse large session file: %v", parsed.err)
	}
	if parsed.finalMsg != testJudgeJSON {
		t.Fatalf("final message = %q, want Judge JSON", parsed.finalMsg)
	}
}

func TestParseCodexSessionFile_JSONLRecordLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "bounded-session.jsonl")
	line := `{"type":"event_msg","payload":{"type":"agent_message","message":"done","phase":"final_answer"}}`
	if err := os.WriteFile(sessionPath, []byte(line), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	parsed := parseCodexSessionFileWithLimit(sessionPath, len(line)-1)
	if parsed.err == nil || !strings.Contains(parsed.err.Error(), "Codex session JSONL record 1 exceeds maximum size") {
		t.Fatalf("error = %v, want explicit session record limit error", parsed.err)
	}
}

func TestParseCodexSessionFile_ReturnsReadError(t *testing.T) {
	t.Parallel()

	parsed := parseCodexSessionFile(t.TempDir())
	if parsed.err == nil {
		t.Fatal("expected directory read error")
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
	stdoutPath := filepath.Join(dir, ".skill-up", codexStdoutArtifactPath)
	if sessionResult.Artifacts.GeneratedFiles[0] != stdoutPath {
		t.Fatalf("expected stdout artifact %q, got %v", stdoutPath, sessionResult.Artifacts.GeneratedFiles)
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

func TestCodexSessionLookupScript_HonoursCodexHome(t *testing.T) {
	t.Parallel()
	if goruntime.GOOS == platform.GOOSWindows {
		t.Skip("POSIX shell script")
	}

	const threadID = "01a195f3-1c06-7a31-95c2-eed72a7ac24e"
	writeSession := func(t *testing.T, codexHome string) string {
		t.Helper()
		dir := filepath.Join(codexHome, "sessions", "2026", "11", "01")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir sessions: %v", err)
		}
		sessionFile := filepath.Join(dir, "rollout-2026-11-01T12-41-40-"+threadID+".jsonl")
		if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("write session: %v", err)
		}
		return sessionFile
	}
	run := func(t *testing.T, env ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "sh", "-c", codexSessionLookupScript)
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "SKILL_UP_CODEX_THREAD_ID=" + threadID}, env...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("lookup script failed: %v", err)
		}
		return strings.TrimSpace(string(out))
	}

	home := t.TempDir()
	codexHome := t.TempDir()
	inHome := writeSession(t, filepath.Join(home, ".codex"))
	inCodexHome := writeSession(t, codexHome)

	if got := run(t, "HOME="+home); got != inHome {
		t.Fatalf("without CODEX_HOME: got %q, want %q", got, inHome)
	}
	if got := run(t, "HOME="+home, "CODEX_HOME="+codexHome); got != inCodexHome {
		t.Fatalf("with CODEX_HOME: got %q, want %q", got, inCodexHome)
	}
	if got := run(t, "HOME="+home, "CODEX_HOME="+t.TempDir()); got != "" {
		t.Fatalf("CODEX_HOME without sessions: got %q, want no match", got)
	}
}

func codexApprovalFlag(name string) string {
	return `-c 'mcp_servers.` + name + `.default_tools_approval_mode="approve"'`
}

func codexRunCommand(t *testing.T, rt *codexTestRuntime) string {
	t.Helper()
	for i := len(rt.commands) - 1; i >= 0; i-- {
		if strings.Contains(rt.commands[i], "codex exec") {
			return rt.commands[i]
		}
	}
	t.Fatalf("no codex exec command recorded: %v", rt.commands)
	return ""
}

// Cases share one CodexAgent and run concurrently, so each run must approve
// only the servers installed for its own runtime.
func TestCodexRun_ApprovesOnlyThisCasesMCPServers(t *testing.T) {
	t.Parallel()

	stdout := `{"type":"thread.started","thread_id":"thread-123"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"OK"}}`
	stdio := func(name string) runtime.MCPServerConfig {
		return runtime.MCPServerConfig{Name: name, Transport: "stdio", Command: "node", Args: []string{name + ".js"}}
	}
	cases := []struct {
		name    string
		servers []runtime.MCPServerConfig
		want    []string
		notWant []string
	}{
		{name: "shop only", servers: []runtime.MCPServerConfig{stdio("shop")}, want: []string{"shop"}, notWant: []string{"docs-v2_x"}},
		{name: "docs and a dotted name", servers: []runtime.MCPServerConfig{stdio("docs-v2_x"), stdio("bad.name")}, want: []string{"docs-v2_x"}, notWant: []string{"shop", "bad.name"}},
		{name: "no servers", notWant: []string{"shop", "docs-v2_x"}},
	}

	ag := NewCodexAgent(Config{})
	runtimes := make([]*codexTestRuntime, len(cases))
	var wg sync.WaitGroup
	for i, tc := range cases {
		runtimes[i] = &codexTestRuntime{workspace: t.TempDir(), execResult: runtime.ExecResult{Stdout: stdout, ExitCode: 0}}
		wg.Add(1)
		go func(rt *codexTestRuntime, servers []runtime.MCPServerConfig) {
			defer wg.Done()
			if err := ag.InstallMCP(context.Background(), rt, runtime.MCPConfig{Servers: servers}); err != nil {
				t.Errorf("InstallMCP failed: %v", err)
				return
			}
			if _, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{Role: transcript.RoleUser, Content: "hi", Turn: 1}}); err != nil {
				t.Errorf("Run failed: %v", err)
			}
		}(runtimes[i], tc.servers)
	}
	wg.Wait()

	for i, tc := range cases {
		runCmd := codexRunCommand(t, runtimes[i])
		if !strings.Contains(runCmd, codexProcessSandbox) || strings.Contains(runCmd, codexBypassSandbox) {
			t.Errorf("%s: approval must keep the workspace-write sandbox: %s", tc.name, runCmd)
		}
		for _, name := range tc.want {
			if !strings.Contains(runCmd, codexApprovalFlag(name)) {
				t.Errorf("%s: missing approval for %q: %s", tc.name, name, runCmd)
			}
		}
		for _, name := range tc.notWant {
			if strings.Contains(runCmd, "mcp_servers."+name+".") {
				t.Errorf("%s: unexpected approval for %q: %s", tc.name, name, runCmd)
			}
		}
	}
}

// A reused workspace runs its cases one after another; each install replaces
// the approved set instead of adding to it.
func TestCodexRun_ReinstallReplacesApprovedMCPServers(t *testing.T) {
	t.Parallel()

	rt := &codexTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{Stdout: `{"type":"thread.started","thread_id":"thread-123"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"OK"}}`, ExitCode: 0},
	}
	ag := NewCodexAgent(Config{})
	for _, step := range []struct {
		install []string
		want    []string
		notWant []string
	}{
		{install: []string{"shop"}, want: []string{"shop"}},
		{install: []string{"docs"}, want: []string{"docs"}, notWant: []string{"shop"}},
		{notWant: []string{"shop", "docs"}},
	} {
		servers := make([]runtime.MCPServerConfig, 0, len(step.install))
		for _, name := range step.install {
			servers = append(servers, runtime.MCPServerConfig{Name: name, Transport: "stdio", Command: "node", Args: []string{name + ".js"}})
		}
		if err := ag.InstallMCP(context.Background(), rt, runtime.MCPConfig{Servers: servers}); err != nil {
			t.Fatalf("InstallMCP(%v) failed: %v", step.install, err)
		}
		if _, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{Role: transcript.RoleUser, Content: "hi", Turn: 1}}); err != nil {
			t.Fatalf("Run after installing %v failed: %v", step.install, err)
		}
		runCmd := codexRunCommand(t, rt)
		for _, name := range step.want {
			if !strings.Contains(runCmd, codexApprovalFlag(name)) {
				t.Fatalf("after installing %v: missing approval for %q: %s", step.install, name, runCmd)
			}
		}
		for _, name := range step.notWant {
			if strings.Contains(runCmd, "mcp_servers."+name+".") {
				t.Fatalf("after installing %v: stale approval for %q: %s", step.install, name, runCmd)
			}
		}
	}
}

func mockedCodexServer(name string) runtime.MCPServerConfig {
	// The script mirrors a generated mock: quotes, a newline and a backslash.
	script := "const fixtures = {\"tools\":{\"get\":{\"response\":\"it's ok\"}}};\nconsole.log(\"a\\\\b\");"
	return runtime.MCPServerConfig{Name: name, Mode: "mocked", Transport: "stdio", Command: "node", Args: []string{"-e", script}}
}

// assertCodexMockDefined checks that cmd defines the mocked server with its
// script written to a file in the run's artifact directory, not inline.
func assertCodexMockDefined(t *testing.T, rt *codexTestRuntime, cmd string, server runtime.MCPServerConfig, index int) {
	t.Helper()
	scriptPath := targetPathJoin(rt.Shell().GOOS, rt.runArtifactDir, fmt.Sprintf("mcp-server-%d.cjs", index))
	wantArgs, err := json.Marshal([]string{scriptPath})
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{
		"-c " + shellQuote("mcp_servers."+server.Name+`.command="node"`),
		"-c " + shellQuote("mcp_servers."+server.Name+".args="+string(wantArgs)),
	} {
		if !strings.Contains(cmd, flag) {
			t.Fatalf("command missing %s: %s", flag, cmd)
		}
	}
	if strings.Contains(cmd, "const fixtures") {
		t.Fatalf("the mock script must not be inlined: %s", cmd)
	}
	rt.mu.Lock()
	uploaded := rt.uploads[scriptPath]
	rt.mu.Unlock()
	if uploaded != server.Args[1] {
		t.Fatalf("script at %s = %q, want the generated script", scriptPath, uploaded)
	}
}

// Mocked servers are defined on the codex exec command line, so nothing is
// written to the user's Codex config; other servers keep codex mcp add.
func TestCodexInstallMCP_DefinesMockedServersOnCommandLine(t *testing.T) {
	t.Parallel()

	stdout := `{"type":"thread.started","thread_id":"thread-123"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"OK"}}`
	mocked := mockedCodexServer("shop")
	installed := runtime.MCPServerConfig{Name: "docs", Mode: "real", Transport: "stdio", Command: "docs-server"}
	for _, tc := range []struct {
		name      string
		workspace string
		kwargs    map[string]string
		approval  bool
	}{
		{name: "workspace-write sandbox", workspace: t.TempDir(), approval: true},
		{name: "bypass_sandbox", workspace: t.TempDir(), kwargs: map[string]string{KwargBypassSandbox: "true"}},
		{name: "opensandbox", workspace: "opensandbox"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := &codexTestRuntime{workspace: tc.workspace, execResult: runtime.ExecResult{Stdout: stdout, ExitCode: 0}}
			ag := NewCodexAgent(Config{Kwargs: tc.kwargs})
			if err := ag.InstallMCP(context.Background(), rt, runtime.MCPConfig{Servers: []runtime.MCPServerConfig{mocked, installed}}); err != nil {
				t.Fatalf("InstallMCP failed: %v", err)
			}
			if containsCommand(rt.commands, "codex mcp add 'shop'") || containsCommand(rt.commands, "codex mcp remove 'shop'") {
				t.Fatalf("mocked server must not be written to the Codex config: %v", rt.commands)
			}
			if !containsCommand(rt.commands, "codex mcp add 'docs'") {
				t.Fatalf("real server should still be installed: %v", rt.commands)
			}
			if _, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{Role: transcript.RoleUser, Content: "hi", Turn: 1}}); err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			runCmd := codexRunCommand(t, rt)
			assertCodexMockDefined(t, rt, runCmd, mocked, 0)
			if strings.Contains(runCmd, "mcp_servers.docs.command") {
				t.Fatalf("installed server must not be redefined on the command line: %s", runCmd)
			}
			if got := strings.Contains(runCmd, codexApprovalFlag("shop")); got != tc.approval {
				t.Fatalf("approval flag present = %t, want %t: %s", got, tc.approval, runCmd)
			}
		})
	}
}

// A resumed turn is a new codex invocation, so it needs the definitions again.
func TestCodexRunTurn_ResumeDefinesMockedServers(t *testing.T) {
	t.Parallel()

	rt := &codexTestRuntime{workspace: t.TempDir(), execResult: runtime.ExecResult{Stdout: `{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"OK"}}`, ExitCode: 0}}
	ag := NewCodexAgent(Config{})
	mocked := mockedCodexServer("shop")
	if err := ag.InstallMCP(context.Background(), rt, runtime.MCPConfig{Servers: []runtime.MCPServerConfig{mocked}}); err != nil {
		t.Fatalf("InstallMCP failed: %v", err)
	}
	if _, err := ag.RunTurn(context.Background(), rt, ExecOptions{}, transcript.Message{Role: transcript.RoleUser, Content: "next", Turn: 2}, "sess-1"); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	resumeCmd := codexRunCommand(t, rt)
	if !strings.Contains(resumeCmd, "codex exec resume") {
		t.Fatalf("expected a resume command, got %s", resumeCmd)
	}
	assertCodexMockDefined(t, rt, resumeCmd, mocked, 0)
}

// Names that differ only in case are distinct Codex servers, so their scripts
// must not share a file on a case-insensitive disk.
func TestCodexRun_CaseVariantMockNamesGetSeparateScripts(t *testing.T) {
	t.Parallel()

	rt := &codexTestRuntime{workspace: t.TempDir(), execResult: runtime.ExecResult{Stdout: `{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"OK"}}`, ExitCode: 0}}
	upper, lower := mockedCodexServer("Shop"), mockedCodexServer("shop")
	upper.Args = []string{"-e", "console.log('upper');"}
	ag := NewCodexAgent(Config{})
	if err := ag.InstallMCP(context.Background(), rt, runtime.MCPConfig{Servers: []runtime.MCPServerConfig{upper, lower}}); err != nil {
		t.Fatalf("InstallMCP failed: %v", err)
	}
	if _, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{Role: transcript.RoleUser, Content: "hi", Turn: 1}}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	runCmd := codexRunCommand(t, rt)
	assertCodexMockDefined(t, rt, runCmd, upper, 0)
	assertCodexMockDefined(t, rt, runCmd, lower, 1)
}

func TestCodexInstallMCP_RejectsNameAlreadyInCodexConfig(t *testing.T) {
	t.Parallel()

	rt := &codexTestRuntime{workspace: t.TempDir(), existingMCP: []string{"docs", "shop"}}
	ag := NewCodexAgent(Config{})
	err := ag.InstallMCP(context.Background(), rt, runtime.MCPConfig{Servers: []runtime.MCPServerConfig{mockedCodexServer("shop")}})
	if err == nil || !strings.Contains(err.Error(), `codex already has an MCP server named "shop"`) || !strings.Contains(err.Error(), "codex mcp remove shop") {
		t.Fatalf("InstallMCP error = %v, want the name conflict", err)
	}
	if containsCommand(rt.commands, "codex mcp remove") || containsCommand(rt.commands, "codex mcp add") {
		t.Fatalf("the user's server must be left alone: %v", rt.commands)
	}
}

func TestCodexInstallMCP_FailsWhenServerListIsUnreadable(t *testing.T) {
	t.Parallel()

	rt := &codexTestRuntime{workspace: t.TempDir(), listMCPFails: true}
	ag := NewCodexAgent(Config{})
	err := ag.InstallMCP(context.Background(), rt, runtime.MCPConfig{Servers: []runtime.MCPServerConfig{mockedCodexServer("shop")}})
	if err == nil || !strings.Contains(err.Error(), "list the MCP servers in the Codex config: exit 1: Error: failed to load config") {
		t.Fatalf("InstallMCP error = %v, want the listing failure", err)
	}
}

// A mocked server that cannot be defined on the command line is rejected; it
// is never installed into the user's Codex config.
func TestCodexInstallMCP_RejectsMockedServerItCannotDefine(t *testing.T) {
	t.Parallel()

	withEnv := mockedCodexServer("shop")
	withEnv.Env = map[string]string{"TOKEN": "x"}
	for _, tc := range []struct {
		name   string
		server runtime.MCPServerConfig
		want   string
	}{
		{"dotted name", mockedCodexServer("shop.tools"), "the name must match"},
		{"environment values", withEnv, "only a generated stdio command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := &codexTestRuntime{workspace: t.TempDir()}
			err := NewCodexAgent(Config{}).InstallMCP(context.Background(), rt, runtime.MCPConfig{Servers: []runtime.MCPServerConfig{tc.server}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("InstallMCP error = %v, want containing %q", err, tc.want)
			}
			if containsCommand(rt.commands, "codex mcp add") || containsCommand(rt.commands, "codex mcp remove") {
				t.Fatalf("a mocked server must never reach the Codex config: %v", rt.commands)
			}
		})
	}
}

func TestCodexRun_NoMCPApprovalWhenApprovalsAreBypassed(t *testing.T) {
	t.Parallel()

	stdout := `{"type":"thread.started","thread_id":"thread-123"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"OK"}}`
	mcpCfg := runtime.MCPConfig{Servers: []runtime.MCPServerConfig{{Name: "shop", Transport: "stdio", Command: "node", Args: []string{"shop.js"}}}}
	for _, tc := range []struct {
		name      string
		workspace string
		kwargs    map[string]string
	}{
		{name: "bypass_sandbox", workspace: t.TempDir(), kwargs: map[string]string{KwargBypassSandbox: "true"}},
		{name: "opensandbox", workspace: "opensandbox"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := &codexTestRuntime{workspace: tc.workspace, execResult: runtime.ExecResult{Stdout: stdout, ExitCode: 0}}
			ag := NewCodexAgent(Config{Kwargs: tc.kwargs})
			if err := ag.InstallMCP(context.Background(), rt, mcpCfg); err != nil {
				t.Fatalf("InstallMCP failed: %v", err)
			}
			if _, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{Role: transcript.RoleUser, Content: "hi", Turn: 1}}); err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if runCmd := codexRunCommand(t, rt); strings.Contains(runCmd, "default_tools_approval_mode") {
				t.Fatalf("approval override added although approvals are bypassed: %s", runCmd)
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

	artifactDir := t.TempDir()
	sessionResult, err := ag.Run(context.Background(), rt, ExecOptions{ArtifactDir: artifactDir}, []transcript.Message{
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
	wantStdoutPath := filepath.Join(artifactDir, codexStdoutArtifactPath)
	if len(sessionResult.Artifacts.GeneratedFiles) == 0 || sessionResult.Artifacts.GeneratedFiles[0] != wantStdoutPath {
		t.Fatalf("generated files = %v, want local stdout artifact %q", sessionResult.Artifacts.GeneratedFiles, wantStdoutPath)
	}
}

// A Codex CLI that calls MCP tools from its code tool records them in the
// session file only as item_completed events. The session transcript is longer
// than the stream one here, so it wins and must still carry the MCP call.
func TestCodexRun_SessionTranscriptKeepsMCPToolCalls(t *testing.T) {
	t.Parallel()

	rt := &codexTestRuntime{
		workspace:   t.TempDir(),
		sessionPath: "/tmp/codex-session.jsonl",
		sessionBytes: []byte(`{"timestamp":"2026-11-01T07:11:40.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"What is the status of order 42?"}]}}
{"timestamp":"2026-11-01T07:11:41.000Z","type":"event_msg","payload":{"type":"agent_message","message":"Reading the skill.","phase":"commentary"}}
{"timestamp":"2026-11-01T07:11:42.000Z","type":"response_item","payload":{"type":"custom_tool_call","status":"completed","call_id":"call_1","name":"exec","input":"text(await tools.exec_command({cmd:\"cat SKILL.md\"}));"}}
{"timestamp":"2026-11-01T07:11:42.400Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"exec-2","command":["/bin/zsh","-lc","cat SKILL.md"],"exit_code":0,"aggregated_output":"Call get_order.\n","status":"completed"}}}
{"timestamp":"2026-11-01T07:11:42.500Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_1","output":[{"type":"input_text","text":"Script completed"}]}}
{"timestamp":"2026-11-01T07:11:45.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"exec-1","server":"shop","tool":"get_order","arguments":{"order_id":42},"status":"completed","result":{"content":[{"type":"text","text":"SHIPPED-7F3Q"}]}}}}
{"timestamp":"2026-11-01T07:11:46.000Z","type":"event_msg","payload":{"type":"agent_message","message":"Order 42 is SHIPPED-7F3Q.","phase":"final_answer"}}`),
		execResult: runtime.ExecResult{
			ExitCode: 0,
			Stdout: `{"type":"thread.started","thread_id":"thread-123"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"/bin/zsh -lc 'cat SKILL.md'","aggregated_output":"Call get_order.\n","exit_code":0,"status":"completed"}}
{"type":"item.started","item":{"id":"item_1","type":"mcp_tool_call","server":"shop","tool":"get_order","arguments":{"order_id":42},"result":null,"error":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_1","type":"mcp_tool_call","server":"shop","tool":"get_order","arguments":{"order_id":42},"result":{"content":[{"type":"text","text":"SHIPPED-7F3Q"}]},"error":null,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"Order 42 is SHIPPED-7F3Q."}}
{"type":"turn.completed","usage":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0}}`,
		},
	}
	ag := NewCodexAgent(Config{})

	sessionResult, err := ag.Run(context.Background(), rt, ExecOptions{ArtifactDir: t.TempDir()}, []transcript.Message{
		{Role: transcript.RoleUser, Content: "What is the status of order 42?", Turn: 1},
	})
	if err != nil {
		t.Fatalf("run codex: %v", err)
	}
	if got := sessionResult.Transcript.FinalAssistantMessage(); got != "Order 42 is SHIPPED-7F3Q." {
		t.Fatalf("final assistant message = %q", got)
	}
	var calls []string
	for _, msg := range sessionResult.Transcript {
		if msg.ToolCall != nil {
			calls = append(calls, msg.ToolCall.ID+" "+msg.ToolCall.Name)
		}
	}
	want := []string{"exec-2 " + codexCommandToolName, "exec-1 mcp__shop__get_order"}
	if !slices.Equal(calls, want) {
		t.Fatalf("tool calls = %v, want the session calls %v", calls, want)
	}
}

func TestCodexRun_UsesOutputLastMessageWhenJSONLHasNoFinalMessage(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	rt := &codexTestRuntime{
		workspace:        workspace,
		execResult:       runtime.ExecResult{Stdout: `{"type":"thread.started","thread_id":"abc"}` + "\n", ExitCode: 0},
		lastMessageBytes: []byte("from last message file\n"),
	}
	ag := NewCodexAgent(Config{ModelName: "gpt-5.4"})
	customCwd := t.TempDir()

	sessionResult, err := ag.Run(context.Background(), rt, ExecOptions{Cwd: customCwd}, []transcript.Message{{
		Role:    transcript.RoleUser,
		Content: "hello",
		Turn:    1,
	}})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	lastMessagePath := filepath.Join(rt.runArtifactDir, "last-message.txt")
	if !containsCommand(rt.commands, "--output-last-message "+shellQuote(lastMessagePath)) {
		t.Fatalf("run command missing output-last-message path:\n%s", rt.lastCommand)
	}
	if sessionResult.FinalMessage != "from last message file" {
		t.Fatalf("FinalMessage = %q, want last message file", sessionResult.FinalMessage)
	}
	if got := sessionResult.Transcript.FinalAssistantMessage(); got != "from last message file" {
		t.Fatalf("transcript final = %q, want last message file", got)
	}
	stdoutPath := filepath.Join(rt.runArtifactDir, codexStdoutArtifactPath)
	if !containsCommand(rt.commands, ">"+shellQuote(stdoutPath)) {
		t.Fatalf("run command does not use the isolated JSONL path with cwd %q:\n%s", customCwd, rt.lastCommand)
	}
	if strings.HasPrefix(stdoutPath, workspace+string(filepath.Separator)) {
		t.Fatalf("stdout path %q must stay outside evaluated workspace %q", stdoutPath, workspace)
	}
}

func TestCodexRun_OutputLastMessageOverridesProgressMessage(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	rt := &codexTestRuntime{
		workspace: workspace,
		execResult: runtime.ExecResult{
			Stdout:   `{"type":"item.completed","item":{"id":"progress","type":"agent_message","text":"still checking"}}` + "\n",
			ExitCode: 0,
		},
		lastMessageBytes: []byte("{\"results\":[]}\n"),
	}
	ag := NewCodexAgent(Config{ModelName: "gpt-5.4"})

	sessionResult, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{
		Role:    transcript.RoleUser,
		Content: "grade this",
		Turn:    1,
	}})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if sessionResult.FinalMessage != testJudgeJSON {
		t.Fatalf("FinalMessage = %q, want last-message artifact", sessionResult.FinalMessage)
	}
	if got := sessionResult.Transcript.FinalAssistantMessage(); got != testJudgeJSON {
		t.Fatalf("transcript final = %q, want last-message artifact", got)
	}
	stdoutPath := filepath.Join(rt.runArtifactDir, codexStdoutArtifactPath)
	if !containsCommand(rt.commands, ">"+shellQuote(stdoutPath)) {
		t.Fatalf("run command does not redirect JSONL stdout to an artifact:\n%s", rt.lastCommand)
	}
}

func TestCodexRun_RejectsOversizedJSONLOutputBeforeDownload(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	rt := &codexTestRuntime{
		workspace:        workspace,
		execResult:       runtime.ExecResult{Stdout: strings.Repeat("x", 65), ExitCode: 0},
		lastMessageBytes: []byte("final answer\n"),
	}
	ag := NewCodexAgent(Config{Kwargs: map[string]string{KwargMaxJSONLOutputBytes: "64"}})

	sessionResult, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{
		Role: transcript.RoleUser, Content: "grade this", Turn: 1,
	}})
	if err == nil || !strings.Contains(err.Error(), "Codex stdout JSONL output is 65 bytes, exceeding maximum size of 64 bytes") {
		t.Fatalf("error = %v, want explicit total output limit error", err)
	}
	if sessionResult == nil || sessionResult.FinalMessage != "final answer" {
		t.Fatalf("last-message recovery = %+v, want final answer alongside parse error", sessionResult)
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

func TestDownloadSessionArtifact_WithoutArtifactDirUsesUniqueTempPaths(t *testing.T) {
	t.Parallel()

	sessionPath := "/tmp/codex-last-message.txt"
	rt := &codexTestRuntime{
		lastMessagePath:  sessionPath,
		lastMessageBytes: []byte("final answer"),
	}

	firstPath, firstRegistered, firstCleanup, firstOK := downloadSessionArtifact(context.Background(), rt, "", sessionPath)
	if !firstOK {
		t.Fatal("first downloadSessionArtifact call failed")
	}
	defer firstCleanup()
	secondPath, secondRegistered, secondCleanup, secondOK := downloadSessionArtifact(context.Background(), rt, "", sessionPath)
	if !secondOK {
		t.Fatal("second downloadSessionArtifact call failed")
	}
	defer secondCleanup()

	if firstPath == secondPath {
		t.Fatalf("concurrent downloads share temp path %q", firstPath)
	}
	if firstRegistered != sessionPath || secondRegistered != sessionPath {
		t.Fatalf("registered paths = %q/%q, want %q", firstRegistered, secondRegistered, sessionPath)
	}
	firstCleanup()
	if _, err := os.Stat(firstPath); !os.IsNotExist(err) {
		t.Fatalf("first cleanup left temp artifact at %q: %v", firstPath, err)
	}
	if data, err := os.ReadFile(secondPath); err != nil || string(data) != "final answer" {
		t.Fatalf("first cleanup affected second artifact: data=%q err=%v", data, err)
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
	existingMCP         []string // server names `codex mcp list --json` reports as configured
	listMCPFails        bool
	uploads             map[string]string // target path -> uploaded content
	mu                  sync.Mutex
	lastCommand         string
	lastExecEnv         map[string]string
	runArtifactDir      string
	probeResponseStdout string
	mergedEnv           map[string]string
}

func (r *codexTestRuntime) Create(context.Context) error { return nil }
func (r *codexTestRuntime) Close() error                 { return nil }
func (r *codexTestRuntime) Start(context.Context) error  { return nil }
func (r *codexTestRuntime) Stop(context.Context) error   { return nil }
func (r *codexTestRuntime) UploadFile(_ context.Context, sourcePath, targetPath string) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.uploads == nil {
		r.uploads = make(map[string]string)
	}
	r.uploads[targetPath] = string(data)
	return nil
}

func (r *codexTestRuntime) UploadDir(context.Context, string, string) error {
	return nil
}

func (r *codexTestRuntime) DownloadFile(_ context.Context, sourcePath, targetPath string) error {
	if sourcePath == r.lastMessagePath || filepath.Base(sourcePath) == "last-message.txt" && len(r.lastMessageBytes) > 0 {
		return os.WriteFile(targetPath, r.lastMessageBytes, 0o600)
	}
	if filepath.Base(sourcePath) == codexStdoutArtifactPath {
		return os.WriteFile(targetPath, []byte(r.execResult.Stdout), 0o600)
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
	if dir, ok := strings.CutPrefix(command, "mkdir -p "); ok {
		r.runArtifactDir = strings.Trim(dir, "'")
		return runtime.ExecResult{ExitCode: 0}, nil
	}
	if strings.HasPrefix(command, "rm -rf -- ") {
		return runtime.ExecResult{ExitCode: 0}, nil
	}
	if strings.HasPrefix(command, "wc -c <") {
		size := len(r.execResult.Stdout)
		if r.sessionPath != "" && strings.Contains(command, shellQuote(r.sessionPath)) {
			size = len(r.sessionBytes)
		}
		return runtime.ExecResult{Stdout: strconv.Itoa(size), ExitCode: 0}, nil
	}
	r.commands = append(r.commands, command)
	r.lastCommand = command
	if command == "codex mcp list --json" {
		if r.listMCPFails {
			return runtime.ExecResult{ExitCode: 1, Stderr: "Error: failed to load config"}, nil
		}
		listed := make([]codexConfiguredMCPServer, 0, len(r.existingMCP))
		for _, name := range r.existingMCP {
			listed = append(listed, codexConfiguredMCPServer{Name: name})
		}
		out, err := json.Marshal(listed)
		if err != nil {
			return runtime.ExecResult{}, err
		}
		return runtime.ExecResult{Stdout: string(out)}, nil
	}
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
