package agent

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/platform"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

const testHello = "hello"

func TestClaudeCodeCheckCredentials_LogsMaskedTokenAndBaseURL(t *testing.T) {
	// Not parallel: captureStdout redirects package-level log output.

	logging.SetVerbosity(1)
	defer logging.SetVerbosity(0)

	apiKey := strings.Join([]string{"anthropic", "test", "token", "1234"}, "-")

	ag := NewClaudeCodeAgent(Config{
		APIKey:        apiKey,
		BaseURL:       "https://anthropic.example.com",
		ModelProvider: "anthropic",
	})
	output := captureStdout(t, func() {
		if err := ag.CheckCredentials(context.Background()); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
	if !strings.Contains(output, `level=DEBUG msg="ANTHROPIC_API_KEY detected for claude-code (masked: `) {
		t.Fatalf("expected api key log, got %q", output)
	}
	if !strings.Contains(output, credential.MaskAPIKey(apiKey)) {
		t.Fatalf("expected masked key log, got %q", output)
	}
	if !strings.Contains(output, `level=DEBUG msg="ANTHROPIC_BASE_URL detected for claude-code (value: https://anthropic.example.com, source=agent_config)"`) {
		t.Fatalf("expected base url log, got %q", output)
	}
}

func TestClaudeCodeInstall_DefaultCommand(t *testing.T) {
	t.Parallel()

	cmd := defaultClaudeCodeInstallCmd()
	for _, want := range []string{
		`NVM_DIR="${NVM_DIR:-$HOME/.nvm}"`,
		`agent_npm_prefix="${npm_config_prefix:-$HOME/.local}"`,
		`export npm_config_prefix="$agent_npm_prefix"`,
		"nvm install '" + agentNodeDefaultVersion + "'",
		"npm install -g --include=optional '" + claudeCodePackage + "'",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("install command missing %q:\n%s", want, cmd)
		}
	}
}

func TestClaudeCodeInstall_ConfiguredVersion(t *testing.T) {
	t.Parallel()

	cmd := defaultClaudeCodeInstallCmdForVersion("2.3.4")
	for _, want := range []string{"claude --version", "'@anthropic-ai/claude-code@2.3.4'"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("install command missing %q:\n%s", want, cmd)
		}
	}
}

func TestClaudeCodeInstall_UsesDefaultCommand(t *testing.T) {
	t.Parallel()

	rt := &claudeCodeTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			ExitCode: 0,
		},
	}
	ag := NewClaudeCodeAgent(Config{APIKey: "install-should-not-see-api-key", BaseURL: "https://install.example.test"})

	if err := ag.Install(context.Background(), rt); err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if !strings.Contains(rt.lastCommand, "npm install -g --include=optional '"+claudeCodePackage+"'") {
		t.Fatalf("install command does not install claude-code:\n%s", rt.lastCommand)
	}
	if _, ok := rt.lastExecEnv["PATH"]; ok {
		t.Fatalf("install env should not carry PATH from agent; PATH flows via runtime baseline. got %q", rt.lastExecEnv["PATH"])
	}
	if got := rt.mergedEnv["PATH"]; got == "" {
		t.Fatalf("expected probeAndMergePATH to populate runtime baseline with PATH; mergedEnv=%+v", rt.mergedEnv)
	}
	if got := rt.lastExecEnv[credential.EnvAnthropicAPIKey]; got != "" {
		t.Fatalf("install env leaked %s = %q", credential.EnvAnthropicAPIKey, got)
	}
	if got := rt.lastExecEnv[credential.EnvAnthropicBaseURL]; got != "" {
		t.Fatalf("install env leaked %s = %q", credential.EnvAnthropicBaseURL, got)
	}
}

func TestBuildClaudeRunCmd_WithModel(t *testing.T) {
	t.Parallel()

	cmd := buildClaudePrintCmd("session-123", "claude-sonnet-4-6", testHello)
	if !strings.Contains(cmd, "--session-id session-123") {
		t.Fatalf("expected session id in command, got %q", cmd)
	}
	if !strings.Contains(cmd, `claude --settings '{"disableAllHooks":true}' --session-id session-123`) {
		t.Fatalf("expected disableAllHooks settings override in command, got %q", cmd)
	}
	if !strings.Contains(cmd, " -p --permission-mode=bypassPermissions") {
		t.Fatalf("expected -p print mode in command, got %q", cmd)
	}
	if !strings.Contains(cmd, "--model 'claude-sonnet-4-6'") {
		t.Fatalf("expected --model flag in command, got %q", cmd)
	}
	if strings.Contains(cmd, "ANTHROPIC_MODEL") {
		t.Fatalf("did not expect ANTHROPIC_MODEL env injection in command, got %q", cmd)
	}
}

func TestBuildClaudeResumeCmd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		sessionID   string
		model       string
		instruction string
		wantParts   []string
		wantAbsent  []string
	}{
		{
			name:        "with model",
			sessionID:   "abc-123",
			model:       "claude-sonnet-4-6",
			instruction: "continue",
			wantParts:   []string{"--resume abc-123", "-p", "--model 'claude-sonnet-4-6'", "'continue'"},
			wantAbsent:  []string{"--session-id"},
		},
		{
			name:        "without model",
			sessionID:   "def-456",
			model:       "",
			instruction: "next step",
			wantParts:   []string{"--resume def-456", "-p", "'next step'"},
			wantAbsent:  []string{"--model", "--session-id"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := buildClaudeResumeCmd(tt.sessionID, tt.model, tt.instruction)
			for _, part := range tt.wantParts {
				if !strings.Contains(cmd, part) {
					t.Fatalf("expected command to contain %q, got %q", part, cmd)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(cmd, absent) {
					t.Fatalf("expected command to NOT contain %q, got %q", absent, cmd)
				}
			}
		})
	}
}

func TestClaudeCodeRun_PopulatesSessionID(t *testing.T) {
	t.Parallel()

	rt := &claudeCodeTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			Stdout:   "OK\n",
			ExitCode: 0,
		},
	}
	ag := NewClaudeCodeAgent(Config{})

	result, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{
		Role:    transcript.RoleUser,
		Content: "Reply OK.",
		Turn:    1,
	}})
	if err != nil {
		t.Fatalf("run claude-code: %v", err)
	}
	if result.SessionID == "" {
		t.Fatal("expected SessionID to be populated after Run")
	}
	// Verify the session ID is a valid UUID-like format (contains hyphens)
	if !strings.Contains(result.SessionID, "-") {
		t.Fatalf("expected SessionID to look like a UUID, got %q", result.SessionID)
	}
}

func TestClaudeCodeRunTurn_FirstTurnDelegatesToRun(t *testing.T) {
	t.Parallel()

	rt := &claudeCodeTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			Stdout:   "OK\n",
			ExitCode: 0,
		},
	}
	ag := NewClaudeCodeAgent(Config{})

	result, err := ag.RunTurn(context.Background(), rt, ExecOptions{}, transcript.Message{
		Role:    transcript.RoleUser,
		Content: "start conversation",
		Turn:    1,
	}, "")
	if err != nil {
		t.Fatalf("RunTurn (first turn): %v", err)
	}
	if result.SessionID == "" {
		t.Fatal("expected SessionID after first turn")
	}
	// First turn uses --session-id (not --resume)
	if strings.Contains(rt.agentCommand, "--resume") {
		t.Fatalf("first turn should use --session-id, not --resume: %q", rt.agentCommand)
	}
}

func TestClaudeCodeRunTurn_ResumeUsesCorrectFlag(t *testing.T) {
	t.Parallel()

	rt := &claudeCodeTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			Stdout:   "Resumed answer\n",
			ExitCode: 0,
		},
	}
	ag := NewClaudeCodeAgent(Config{ModelName: "claude-sonnet-4-6"})

	result, err := ag.RunTurn(context.Background(), rt, ExecOptions{}, transcript.Message{
		Role:    transcript.RoleUser,
		Content: "follow up",
		Turn:    2,
	}, "existing-session-id")
	if err != nil {
		t.Fatalf("RunTurn (resume): %v", err)
	}
	if result.SessionID != "existing-session-id" {
		t.Fatalf("expected SessionID = %q, got %q", "existing-session-id", result.SessionID)
	}
	if !strings.Contains(rt.agentCommand, "--resume existing-session-id") {
		t.Fatalf("expected --resume flag, got %q", rt.agentCommand)
	}
	if strings.Contains(rt.agentCommand, "--session-id") {
		t.Fatalf("resume should not use --session-id: %q", rt.agentCommand)
	}
	if !strings.Contains(rt.agentCommand, "--model 'claude-sonnet-4-6'") {
		t.Fatalf("expected model flag in resume command: %q", rt.agentCommand)
	}
	if !strings.Contains(rt.agentCommand, "'follow up'") {
		t.Fatalf("expected instruction in resume command: %q", rt.agentCommand)
	}
}

func TestBuildStreamJSON_EncodesStringAsTextBlocks(t *testing.T) {
	t.Parallel()

	ag := NewClaudeCodeAgent(Config{})
	got := ag.buildStreamJSON([]transcript.Message{
		{Role: transcript.RoleUser, Content: testHello, Turn: 1},
	})

	want := "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":[{\"text\":\"hello\",\"type\":\"text\"}]}}\n"
	if got != want {
		t.Fatalf("unexpected stream json:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestBuildClaudePrintJSONSessionResult(t *testing.T) {
	t.Parallel()

	start := time.Now()
	result := buildClaudePrintJSONSessionResult("claude_code", start, ExecResult{
		Stdout: `{"type":"result","subtype":"success","is_error":false,"result":"hello","num_turns":2,"session_id":"abc"}`,
	})

	if result.FinalMessage != testHello {
		t.Fatalf("expected final message hello, got %q", result.FinalMessage)
	}
	if result.Turns != 2 {
		t.Fatalf("expected turns 2, got %d", result.Turns)
	}
	if len(result.Transcript) != 1 || result.Transcript[0].Content != testHello {
		t.Fatalf("unexpected transcript: %+v", result.Transcript)
	}
}

func TestBuildClaudeTextSessionResult(t *testing.T) {
	t.Parallel()

	start := time.Now()
	result := buildClaudeTextSessionResult("claude_code", "prompt", start, ExecResult{
		Stdout: testHello,
	})

	if result.FinalMessage != testHello {
		t.Fatalf("expected final message hello, got %q", result.FinalMessage)
	}
	if result.Turns != 1 {
		t.Fatalf("expected turns 1, got %d", result.Turns)
	}
	if len(result.Transcript) != 2 || result.Transcript[1].Content != testHello {
		t.Fatalf("unexpected transcript: %+v", result.Transcript)
	}
}

func TestClaudeCodeAppliedModelName_PassesThroughExplicitModel(t *testing.T) {
	t.Parallel()

	ag := NewClaudeCodeAgent(Config{ModelName: "claude-sonnet-4-20250514"})
	if got := ag.appliedModelName(context.Background()); got != "claude-sonnet-4-20250514" {
		t.Fatalf("appliedModelName() = %q, want claude-sonnet-4-20250514", got)
	}
}

func TestClaudeCodeRun_WritesBothAnthropicAuthEnvVars(t *testing.T) {
	t.Parallel()

	rt := &claudeCodeTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			Stdout:   "OK\n",
			ExitCode: 0,
		},
	}
	ag := NewClaudeCodeAgent(Config{
		APIKey:    "sk-test-token",
		BaseURL:   "https://anthropic-proxy.example.com",
		ModelName: "claude-sonnet-4-6",
	})

	if _, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{
		Role:    transcript.RoleUser,
		Content: "Reply OK.",
		Turn:    1,
	}}); err != nil {
		t.Fatalf("run claude-code: %v", err)
	}

	// Both vars must be present so internal anthropic-proxy gateways that
	// only validate Authorization: Bearer (e.g. ducky /v1/anthropic-proxy)
	// authenticate, while keeping x-api-key compatibility for the official
	// Anthropic endpoint and any proxy that prefers it.
	if got := rt.lastExecEnv[credential.EnvAnthropicAPIKey]; got != "sk-test-token" {
		t.Fatalf("%s = %q, want sk-test-token", credential.EnvAnthropicAPIKey, got)
	}
	if got := rt.lastExecEnv[credential.EnvAnthropicAuthToken]; got != "sk-test-token" {
		t.Fatalf("%s = %q, want sk-test-token", credential.EnvAnthropicAuthToken, got)
	}
}

func TestClaudeCodeRun_EnablesTelemetryAndPropagatesTraceContext(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://collector:4318/v1/traces")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "http/protobuf")
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

	rt := &claudeCodeTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			Stdout:   "OK\n",
			ExitCode: 0,
		},
	}
	ag := NewClaudeCodeAgent(Config{
		ModelProvider: "anthropic",
		ModelName:     "claude-sonnet-4-6",
	})

	_, err = ag.Run(ctx, rt, ExecOptions{}, []transcript.Message{{
		Role:    transcript.RoleUser,
		Content: "Reply OK.",
		Turn:    1,
	}})
	if err != nil {
		t.Fatalf("run claude-code: %v", err)
	}

	if rt.lastExecEnv["TRACEPARENT"] == "" {
		t.Fatalf("expected TRACEPARENT in claude-code env, got %v", rt.lastExecEnv)
	}
	if rt.lastExecEnv["CLAUDE_CODE_ENABLE_TELEMETRY"] != "1" {
		t.Fatalf("expected Claude telemetry to be enabled, got %q", rt.lastExecEnv["CLAUDE_CODE_ENABLE_TELEMETRY"])
	}
	if rt.lastExecEnv["CLAUDE_CODE_ENHANCED_TELEMETRY_BETA"] != "1" {
		t.Fatalf("expected Claude enhanced telemetry beta to be enabled, got %q", rt.lastExecEnv["CLAUDE_CODE_ENHANCED_TELEMETRY_BETA"])
	}
	if rt.lastExecEnv["ENABLE_ENHANCED_TELEMETRY_BETA"] != "1" {
		t.Fatalf("expected enhanced telemetry beta to be enabled, got %q", rt.lastExecEnv["ENABLE_ENHANCED_TELEMETRY_BETA"])
	}
	resourceAttrs := rt.lastExecEnv["OTEL_RESOURCE_ATTRIBUTES"]
	for _, want := range []string{
		"deployment.environment=test",
		"skill_up.engine=claude-code",
		"skill_up.model=anthropic/claude-sonnet-4-6",
		"skill_up.parent_trace_id=4bf92f3577b34da6a3ce929d0e0e4736",
		"claude_code.session_id=",
	} {
		if !strings.Contains(resourceAttrs, want) {
			t.Fatalf("expected resource attrs to contain %q, got %q", want, resourceAttrs)
		}
	}
}

func TestParseStreamOutput_JSONEventArray(t *testing.T) {
	t.Parallel()

	output := `[{"type":"system","subtype":"init"},{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Authentication error"}],"usage":{"input_tokens":0,"output_tokens":0}}},{"type":"result","subtype":"success","is_error":true,"api_error_status":403,"result":"Authentication error","usage":{"input_tokens":0,"output_tokens":0}}]`
	trans, finalMsg, inputTokens, outputTokens := parseStreamOutput(output)

	if len(trans) != 1 {
		t.Fatalf("expected 1 transcript message, got %d", len(trans))
	}
	if trans[0].Content != "Authentication error" {
		t.Fatalf("unexpected assistant content: %q", trans[0].Content)
	}
	if finalMsg != "Authentication error" {
		t.Fatalf("unexpected final message: %q", finalMsg)
	}
	if inputTokens != 0 || outputTokens != 0 {
		t.Fatalf("unexpected tokens: %d/%d", inputTokens, outputTokens)
	}
}

func TestParseStreamOutput_NDJSONWithTurnsAndTools(t *testing.T) {
	t.Parallel()

	output := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Prompt"}]}}`,
		`{"type":"tool_call","tool_call":{"id":"tool-1","name":"read_file","input":{"path":"README.md"}}}`,
		`{"type":"tool_result","tool_result":{"call_id":"tool-1","status":"success","content":"contents"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Answer"}],"usage":{"input_tokens":3,"cache_read_input_tokens":2,"output_tokens":5}}}`,
		`{"type":"result","result":"Final answer","usage":{"input_tokens":4,"cache_creation_input_tokens":1,"output_tokens":6}}`,
		`not-json`,
		`{}`,
	}, "\n")

	trans, finalMsg, inputTokens, outputTokens := parseStreamOutput(output)
	assertClaudeStreamParseResult(t, trans, finalMsg, inputTokens, outputTokens)
}

func assertClaudeStreamParseResult(t *testing.T, trans transcript.Transcript, finalMsg string, inputTokens, outputTokens int) {
	t.Helper()
	if finalMsg != "Final answer" {
		t.Fatalf("finalMsg = %q, want Final answer", finalMsg)
	}
	if inputTokens != 5 || outputTokens != 6 {
		t.Fatalf("tokens = %d/%d, want 5/6", inputTokens, outputTokens)
	}
	if len(trans) != 4 {
		t.Fatalf("transcript length = %d, want 4: %#v", len(trans), trans)
	}
	assertClaudeStreamUserMessage(t, trans[0])
	assertClaudeStreamToolCall(t, trans[1])
	assertClaudeStreamToolResult(t, trans[2])
	assertClaudeStreamAssistantMessage(t, trans[3])
}

func assertClaudeStreamUserMessage(t *testing.T, msg transcript.Message) {
	t.Helper()
	if msg.Role != transcript.RoleUser || msg.Content != "Prompt" || msg.Turn != 1 {
		t.Fatalf("user message = %#v", msg)
	}
}

func assertClaudeStreamToolCall(t *testing.T, msg transcript.Message) {
	t.Helper()
	if msg.ToolCall == nil || msg.ToolCall.Name != "read_file" || msg.Turn != 1 {
		t.Fatalf("tool call = %#v", msg)
	}
}

func assertClaudeStreamToolResult(t *testing.T, msg transcript.Message) {
	t.Helper()
	if msg.ToolResult == nil || msg.ToolResult.CallID != "tool-1" || msg.Turn != 1 {
		t.Fatalf("tool result = %#v", msg)
	}
}

func assertClaudeStreamAssistantMessage(t *testing.T, msg transcript.Message) {
	t.Helper()
	if msg.Role != transcript.RoleAssistant || msg.Content != "Answer" {
		t.Fatalf("assistant message = %#v", msg)
	}
}

func TestStreamEventNilPayloadsStillMaintainTurnState(t *testing.T) {
	t.Parallel()

	var state streamParseState
	applyStreamUserEvent(&state, &streamEvent{})
	if state.currentTurn != 1 || len(state.messages) != 0 {
		t.Fatalf("nil user event state = %+v, want turn advanced without message", state)
	}
	applyStreamToolCallEvent(&state, &streamEvent{})
	applyStreamToolResultEvent(&state, &streamEvent{})
	applyStreamAssistantEvent(&state, &streamEvent{})
	applyStreamResultEvent(&state, &streamEvent{Usage: &streamUsage{OutputTokens: 2}})
	if len(state.messages) != 0 {
		t.Fatalf("nil payload events appended messages: %#v", state.messages)
	}
	if state.totalOutputTokens != 2 {
		t.Fatalf("result usage output tokens = %d, want 2", state.totalOutputTokens)
	}
}

func TestClaudeInputContentJSONAndContentBlockToString(t *testing.T) {
	t.Parallel()

	data, err := claudeInputContentJSON("hello")
	if err != nil {
		t.Fatalf("claudeInputContentJSON string error: %v", err)
	}
	if !strings.Contains(string(data), `"text":"hello"`) {
		t.Fatalf("string content JSON = %s", data)
	}
	data, err = claudeInputContentJSON([]map[string]string{{"type": "text", "text": "custom"}})
	if err != nil {
		t.Fatalf("claudeInputContentJSON custom error: %v", err)
	}
	if !strings.Contains(string(data), `"custom"`) {
		t.Fatalf("custom content JSON = %s", data)
	}

	if got := contentBlockToString(nil); got != "" {
		t.Fatalf("contentBlockToString(nil) = %q, want empty", got)
	}
	if got := contentBlockToString("plain"); got != "plain" {
		t.Fatalf("contentBlockToString(string) = %q, want plain", got)
	}
	if got := contentBlockToString(map[string]string{"b": "2", "a": "1"}); got != `{"a":"1","b":"2"}` {
		t.Fatalf("contentBlockToString(map) = %q", got)
	}
	if got := contentBlockToString(func() {}); got == "" {
		t.Fatalf("contentBlockToString(unmarshalable) = %q, want fmt fallback", got)
	}
}

func TestProviderAuthFailureSignal(t *testing.T) {
	t.Parallel()

	msg, ok := providerAuthFailureSignal(ExecResult{
		Stdout: `[{"type":"result","subtype":"success","is_error":true,"api_error_status":403,"result":"Authentication error","error":"authentication_failed"}]`,
	}, &SessionResult{})
	if !ok {
		t.Fatal("expected auth failure signal")
	}
	if !strings.Contains(msg, "authentication_failed") {
		t.Fatalf("unexpected auth failure message: %q", msg)
	}
}

func TestParseSessionFile_UserMessage(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "550e8400-e29b-41d4-a716-446655440000.jsonl")

	content := `{"type":"user","message":{"content":"Hello","role":"user"}}
{"type":"assistant","message":{"content":"Hi there!","role":"assistant"}}
{"type":"result","final_message":{"content":"Done"}}
`
	if err := os.WriteFile(sessionFile, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write session file: %v", err)
	}

	trans, finalMsg, _, _ := parseSessionFile(sessionFile)

	if len(trans) != 2 {
		t.Errorf("expected 2 messages, got %d", len(trans))
	}
	if trans[0].Role != transcript.RoleUser {
		t.Errorf("first message should be user, got %v", trans[0].Role)
	}
	if trans[0].Content != "Hello" {
		t.Errorf("first message content should be 'Hello', got %q", trans[0].Content)
	}
	if trans[1].Role != transcript.RoleAssistant {
		t.Errorf("second message should be assistant, got %v", trans[1].Role)
	}
	if trans[1].Content != "Hi there!" {
		t.Errorf("second message content should be 'Hi there!', got %q", trans[1].Content)
	}
	if finalMsg != "Done" {
		t.Errorf("final message should be 'Done', got %q", finalMsg)
	}
}

func TestParseSessionFile_ToolCalls(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "550e8400-e29b-41d4-a716-446655440001.jsonl")

	content := `{"type":"user","message":{"content":"Read the file","role":"user"}}
{"type":"assistant","message":{"content":"[tool: Read]","role":"assistant"}}
{"type":"tool_call","tool_call":{"id":"call_123","name":"Read","input":{"path":"/test.txt"}}}
{"type":"tool_result","tool_result":{"call_id":"call_123","status":"success","content":"file contents"}}
{"type":"assistant","message":{"content":"The file contains: file contents","role":"assistant"}}
{"type":"result","final_message":{"content":"I read the file"}}
`
	if err := os.WriteFile(sessionFile, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write session file: %v", err)
	}

	trans, finalMsg, _, _ := parseSessionFile(sessionFile)

	if len(trans) != 5 {
		t.Errorf("expected 5 messages, got %d", len(trans))
	}

	if trans[2].Role != transcript.RoleToolCall {
		t.Errorf("3rd message should be tool_call, got %v", trans[2].Role)
	}
	if trans[2].ToolCall == nil {
		t.Error("tool call info should not be nil")
	}
	if trans[2].ToolCall.Name != "Read" {
		t.Errorf("tool name should be 'Read', got %q", trans[2].ToolCall.Name)
	}

	if trans[3].Role != transcript.RoleToolResult {
		t.Errorf("4th message should be tool_result, got %v", trans[3].Role)
	}

	if finalMsg != "I read the file" {
		t.Errorf("final message should be 'I read the file', got %q", finalMsg)
	}
}

// TestParseSessionFile_ToolUseOnlyAssistantEventKeepsFinalTextAnswer uses the
// shape a real qodercli session takes when the model answers by delegating to a
// Skill: the first assistant event carries nothing but a tool_use block, the
// tool result and the injected Skill body arrive as "user" events, and the
// actual answer is the last assistant text. qodercli writes no "result" event,
// so the final message has to come from the transcript.
//
// A tool_use block is already represented by its own tool_call message, so it
// must not additionally masquerade as assistant prose — otherwise a placeholder
// like "[tool: Skill]" can be selected as the answer under evaluation.
func TestParseSessionFile_ToolUseOnlyAssistantEventKeepsFinalTextAnswer(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl")

	content := `{"type":"user","message":{"content":"How is data tiering defined?","role":"user"}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Skill","input":{"skill":"kb"}}],"role":"assistant"}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"Launching skill: kb"}],"role":"user"}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Tier 1.1 applies. See alidocs.dingtalk.com/i/p/tiering"}],"role":"assistant"}}
`
	if err := os.WriteFile(sessionFile, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write session file: %v", err)
	}

	trans, finalMsg, _, _ := parseSessionFile(sessionFile)

	const wantAnswer = "Tier 1.1 applies. See alidocs.dingtalk.com/i/p/tiering"
	if finalMsg != wantAnswer {
		t.Errorf("final message = %q, want %q", finalMsg, wantAnswer)
	}
	if got := trans.FinalAssistantMessage(); got != wantAnswer {
		t.Errorf("FinalAssistantMessage() = %q, want %q", got, wantAnswer)
	}
	for i, msg := range trans {
		if msg.Role == transcript.RoleAssistant && strings.Contains(msg.Content, "[tool:") {
			t.Errorf("message %d: assistant text must not be a tool placeholder, got %q", i, msg.Content)
		}
	}

	calls := trans.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected the Skill tool call to stay visible, got %d tool calls", len(calls))
	}
	if calls[0].ToolCall.Name != "Skill" {
		t.Errorf("tool call name = %q, want Skill", calls[0].ToolCall.Name)
	}
}

func TestParseSessionFile_EmptyFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "empty.jsonl")
	if err := os.WriteFile(sessionFile, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	trans, finalMsg, inputTokens, outputTokens := parseSessionFile(sessionFile)
	if trans != nil {
		t.Errorf("expected nil transcript for empty file, got %v", trans)
	}
	if finalMsg != "" {
		t.Errorf("expected empty finalMsg, got %q", finalMsg)
	}
	if inputTokens != 0 || outputTokens != 0 {
		t.Errorf("expected zero tokens, got %d/%d", inputTokens, outputTokens)
	}
}

func TestParseSessionFile_MaxUsageIncludesCacheInputTokens(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "session-cache-usage.jsonl")

	content := `{"type":"user","message":{"content":"Hi","role":"user"}}
{"type":"assistant","message":{"content":"A","role":"assistant","usage":{"input_tokens":0,"output_tokens":3,"cache_read_input_tokens":900,"cache_creation_input_tokens":10}}}
`
	if err := os.WriteFile(sessionFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, inTok, outTok := parseSessionFile(sessionFile)
	if inTok != 910 {
		t.Errorf("expected composite input max 910 (0+900+10), got %d", inTok)
	}
	if outTok != 3 {
		t.Errorf("expected output tokens 3, got %d", outTok)
	}
}

func TestParseSessionFile_MaxUsageFromAssistantMessages(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "session-with-usage.jsonl")

	content := `{"type":"user","message":{"content":"Hi","role":"user"}}
{"type":"assistant","message":{"content":"A","role":"assistant","usage":{"input_tokens":3,"output_tokens":4}}}
{"type":"assistant","message":{"content":"B","role":"assistant","usage":{"input_tokens":10,"output_tokens":20}}}
`
	if err := os.WriteFile(sessionFile, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write session file: %v", err)
	}

	_, _, inTok, outTok := parseSessionFile(sessionFile)
	if inTok != 10 {
		t.Errorf("expected max input tokens 10, got %d", inTok)
	}
	if outTok != 20 {
		t.Errorf("expected max output tokens 20, got %d", outTok)
	}
}

func TestExtractTextFromContent(t *testing.T) {
	t.Parallel()

	if got := extractTextFromContent(nil); got != "" {
		t.Errorf("expected empty string for nil, got %q", got)
	}

	if got := extractTextFromContent("plain text"); got != "plain text" {
		t.Errorf("expected 'plain text', got %q", got)
	}

	content := []any{
		map[string]any{"type": "text", "text": "Hello"},
		map[string]any{"type": "tool_use", "name": "Read"},
	}
	if got := extractTextFromContent(content); got != "Hello\n\n[tool: Read]" {
		t.Errorf("expected 'Hello\\n\\n[tool: Read]', got %q", got)
	}
}

func TestApplyStreamEvent(t *testing.T) {
	t.Run("user message increments turn", func(t *testing.T) {
		state := &streamParseState{}
		event := &streamEvent{
			Type:    "assistant",
			Message: &streamMessage{Content: "Hi", Role: "assistant", Usage: streamUsage{InputTokens: 10, OutputTokens: 5}},
		}
		applyStreamEvent(state, event)
		if state.totalInputTokens != 10 {
			t.Errorf("expected input tokens 10, got %d", state.totalInputTokens)
		}
		if state.totalOutputTokens != 5 {
			t.Errorf("expected output tokens 5, got %d", state.totalOutputTokens)
		}
		if state.finalMsg != "" {
			t.Errorf("expected empty finalMsg, got %q", state.finalMsg)
		}
	})

	t.Run("result sets final message", func(t *testing.T) {
		state := &streamParseState{}
		event := &streamEvent{
			Type:         "result",
			FinalMessage: "Final answer",
			Usage:        &streamUsage{InputTokens: 100, OutputTokens: 50},
		}
		applyStreamEvent(state, event)
		if state.finalMsg != "Final answer" {
			t.Errorf("expected 'Final answer', got %q", state.finalMsg)
		}
		if state.totalInputTokens != 100 {
			t.Errorf("expected input tokens 100, got %d", state.totalInputTokens)
		}
	})

	t.Run("tool_call and tool_result", func(t *testing.T) {
		state := &streamParseState{}
		applyStreamEvent(state, &streamEvent{
			Type:     "tool_call",
			ToolCall: &streamToolCall{ID: "call_1", Name: "Bash", Input: map[string]any{"command": "ls"}},
		})
		applyStreamEvent(state, &streamEvent{
			Type:       "tool_result",
			ToolResult: &streamToolResult{CallID: "call_1", Status: "success", Content: "file.txt"},
		})
		if len(state.messages) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(state.messages))
		}
		if state.messages[0].Role != transcript.RoleToolCall {
			t.Errorf("first message role: want tool_call, got %v", state.messages[0].Role)
		}
		if state.messages[1].Role != transcript.RoleToolResult {
			t.Errorf("second message role: want tool_result, got %v", state.messages[1].Role)
		}
	})

	t.Run("ignores nil message", func(t *testing.T) {
		state := &streamParseState{}
		applyStreamEvent(state, &streamEvent{Type: "assistant", Message: nil})
		if len(state.messages) != 0 {
			t.Errorf("expected no messages, got %v", state.messages)
		}
	})
}

func TestProviderRateLimitText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		text  string
		match bool
	}{
		{name: "english rate limit", text: "API Error: 429 rate limit exceeded", match: true},
		{name: "chinese rate limit", text: "模型提供方限流，请稍后重试", match: true},
		{name: "too many requests", text: "Too many requests from provider", match: true},
		{name: "http 429", text: "HTTP 429 from provider", match: true},
		{name: "status 429", text: "request failed with status 429", match: true},
		{name: "port 4290", text: "connected to localhost:4290 successfully", match: false},
		{name: "ticket 429", text: "see ticket #429 for details", match: false},
		{name: "long output scans tail only", text: strings.Repeat("x", 3000) + " HTTP 429 from provider", match: true},
		{name: "normal text", text: "hello world", match: false},
		{name: "empty", text: "", match: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, ok := providerRateLimitText(tt.text)
			if ok != tt.match {
				t.Fatalf("providerRateLimitText(%q) match=%v, want %v", tt.text, ok, tt.match)
			}
		})
	}
}

func TestProviderRateLimitSignal_PrefersSessionFinalMessage(t *testing.T) {
	t.Parallel()

	msg, ok := providerRateLimitSignal(ExecResult{}, &SessionResult{
		FinalMessage: "API Error: 400 rate limit",
	})
	if !ok {
		t.Fatal("expected provider rate limit to be detected from session final message")
	}
	if !strings.Contains(msg, "rate limit") {
		t.Fatalf("expected rate limit message, got %q", msg)
	}
}

type claudeCodeTestRuntime struct {
	workspace           string
	execResult          runtime.ExecResult
	lastCommand         string
	agentCommand        string // first non-probe, non-intercepted command
	lastExecEnv         map[string]string
	execCount           int
	probeResponseStdout string            // canned stdout for PATH probe; defaults to a fake bin
	mergedEnv           map[string]string // accumulates entries from MergeEnv calls
}

func (r *claudeCodeTestRuntime) Create(context.Context) error { return nil }
func (r *claudeCodeTestRuntime) Close() error                 { return nil }
func (r *claudeCodeTestRuntime) Start(context.Context) error  { return nil }
func (r *claudeCodeTestRuntime) Stop(context.Context) error   { return nil }
func (r *claudeCodeTestRuntime) UploadFile(context.Context, string, string) error {
	return nil
}

func (r *claudeCodeTestRuntime) UploadDir(context.Context, string, string) error {
	return nil
}

func (r *claudeCodeTestRuntime) DownloadFile(context.Context, string, string) error {
	return nil
}

func (r *claudeCodeTestRuntime) DownloadDir(context.Context, string, string) error {
	return nil
}

func (r *claudeCodeTestRuntime) Exec(_ context.Context, command string, opts runtime.ExecOptions) (runtime.ExecResult, error) {
	// Probe calls (issued by agent.Install via probeAndMergePATH) get a
	// canned literal PATH and are NOT recorded as the agent's own
	// command. Match the exact probe constant rather than a prefix so a
	// future test that legitimately runs `printf '%s' "$HOME/..."` for
	// some other purpose isn't silently swallowed.
	if command == claudeCodeExecPathProbeCmd {
		stdout := r.probeResponseStdout
		if stdout == "" {
			stdout = "/fake/.local/bin:/fake/.nvm/current/bin:/usr/bin"
		}
		return runtime.ExecResult{Stdout: stdout}, nil
	}
	// ensureNodeRuntime emits a script whose first conditional short-circuits
	// when claude is already on PATH. Treat it as a no-op success so the
	// subsequent agent-command Exec is what tests observe via lastCommand.
	if strings.Contains(command, "if command -v 'claude' >/dev/null 2>&1; then exit 0; fi") {
		return runtime.ExecResult{ExitCode: 0}, nil
	}
	// Session file lookup scripts start with printenv HOME; treat them as
	// background operations that do not overwrite the agent command.
	if strings.HasPrefix(command, "home=$(printenv HOME)") {
		return runtime.ExecResult{}, errors.New("no session file")
	}
	r.lastCommand = command
	r.execCount++
	if r.execCount == 1 {
		r.agentCommand = command
		r.lastExecEnv = mapsClone(opts.Env)
		return r.execResult, nil
	}
	return runtime.ExecResult{}, errors.New("no session file")
}

func (r *claudeCodeTestRuntime) Workspace() string { return r.workspace }
func (r *claudeCodeTestRuntime) RequiresProcessSandbox() bool {
	return true
}

func (r *claudeCodeTestRuntime) MergeEnv(env map[string]string) {
	if r.mergedEnv == nil {
		r.mergedEnv = make(map[string]string, len(env))
	}
	maps.Copy(r.mergedEnv, env)
}

func (r *claudeCodeTestRuntime) Shell() platform.Shell {
	return platform.Shell{GOOS: platform.GOOSLinux, Family: platform.ShellPOSIX}
}
