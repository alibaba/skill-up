package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/observability"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// ClaudeCodeAgent implements Agent for claude-code CLI.
type ClaudeCodeAgent struct {
	CLIAgent
}

const claudeCodePackage = "@anthropic-ai/claude-code"

// claudeCodeExecPathProbeCmd resolves both $HOME/.local/bin (where `npm
// install -g` puts the claude binary via the bootstrap's npm_config_prefix)
// and $HOME/.nvm/current/bin (where the node interpreter lives — claude's
// `#!/usr/bin/env node` shebang needs to find node at exec time).
const claudeCodeExecPathProbeCmd = `printf '%s' "$HOME/.local/bin:$HOME/.nvm/current/bin:$PATH"`

// NewClaudeCodeAgent creates a new ClaudeCodeAgent.
func NewClaudeCodeAgent(cfg Config) *ClaudeCodeAgent {
	if cfg.Name == "" {
		cfg.Name = "claude-code"
	}
	cfg.CheckCmd = "command -v claude"
	cfg.SkillPath = ".claude/skills"

	return &ClaudeCodeAgent{
		CLIAgent: CLIAgent{BaseAgent: NewBaseAgent(cfg)},
	}
}

// Install installs Claude Code when it is not already available in the runtime.
//
//nolint:dupl // each agent Install shares the same probe→merge→exec lifecycle; the deltas (probe const, default install cmd) are pulled out, leaving the orchestration intentionally similar.
func (a *ClaudeCodeAgent) Install(ctx context.Context, rt Runtime) error {
	a.probeAndMergePATH(ctx, rt, claudeCodeExecPathProbeCmd)

	opts := ExecOptions{Cwd: "/"}
	opts = a.mergeExecOptionsEnv(ctx, opts, nil, nil)

	installCmd := a.Cfg.InstallCmd
	if installCmd == "" {
		installCmd = defaultClaudeCodeInstallCmd()
	}

	execResult, err := rt.Exec(ctx, installCmd, opts)
	if err != nil {
		return fmt.Errorf("install failed: %w", err)
	}
	if execResult.ExitCode != 0 {
		return fmt.Errorf("install failed: %w: %s", ErrAgentInstallFailed, execResult.Stderr)
	}
	return nil
}

// InstallMCP installs MCP servers with the Claude Code CLI.
func (a *ClaudeCodeAgent) InstallMCP(ctx context.Context, rt Runtime, mcpCfg runtime.MCPConfig) error {
	if len(mcpCfg.Servers) == 0 {
		return nil
	}
	if err := ensureNodeRuntime(ctx, rt, "claude", ExecOptions{}); err != nil {
		return err
	}
	return installMCPServers(ctx, rt, mcpCfg, buildClaudeMCPInstallCmd)
}

func buildClaudeMCPInstallCmd(server runtime.MCPServerConfig) (string, error) {
	return buildClaudeCompatibleMCPInstallCmd("claude", "claude-code", server)
}

func defaultClaudeCodeInstallCmd() string {
	lines := []string{
		"set -e",
		"if command -v claude >/dev/null 2>&1; then exit 0; fi",
	}
	lines = append(lines, nodeBootstrapLines(agentNodeDefaultVersion)...)
	lines = append(lines, "npm install -g --include=optional "+shellQuote(claudeCodePackage))
	return strings.Join(lines, "\n")
}

// CheckCredentials reports the anthropic env configuration used by claude-code.
func (a *ClaudeCodeAgent) CheckCredentials(ctx context.Context) error {
	a.logCredentialStatus(
		ctx,
		credential.EnvAnthropicAPIKey,
		credential.EnvAnthropicBaseURL,
		"ANTHROPIC_API_KEY not set, claude-code will rely on existing login state if available",
	)
	return nil
}

// Run executes the claude-code agent with the given messages via stream-json.
// It streams messages to stdin and parses stream-json output to build the transcript.
func (a *ClaudeCodeAgent) Run(ctx context.Context, rt Runtime, opts ExecOptions, messages []transcript.Message) (*SessionResult, error) {
	if err := requireBashOnWindowsHost(rt); err != nil {
		return nil, fmt.Errorf("%s: %w", a.Name(), err)
	}
	start := time.Now()

	sessionID := uuid.New().String()
	logging.DebugContextf(ctx, "Session ID: %s", sessionID)

	envVars := a.credentialEnvVars(credential.EnvAnthropicAPIKey, credential.EnvAnthropicBaseURL)
	// Some Anthropic-compatible proxies (e.g. internal anthropic-proxy
	// gateways) only validate `Authorization: Bearer <token>` and do not
	// recognise the `x-api-key` header. The Anthropic SDK switches to
	// Bearer when ANTHROPIC_AUTH_TOKEN is set, and the official Anthropic
	// API also accepts Bearer, so writing both env vars covers both
	// flavours without forcing operators to maintain two credentials.
	if a.Cfg.APIKey != "" {
		envVars[credential.EnvAnthropicAuthToken] = a.Cfg.APIKey
	}
	envVars["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	envVars["IS_SANDBOX"] = "1"
	if observability.TracingEnabled() {
		envVars["CLAUDE_CODE_ENABLE_TELEMETRY"] = "1"
		envVars["CLAUDE_CODE_ENHANCED_TELEMETRY_BETA"] = "1"
		envVars["ENABLE_ENHANCED_TELEMETRY_BETA"] = "1"
	}
	opts = a.mergeExecOptionsEnv(ctx, opts, envVars, a.buildAgentObservabilityAttrs(map[string]string{
		"claude_code.session_id": sessionID,
	}))
	ctx = observability.ContextWithConfiguredAgentSpanAttributes(ctx, opts.Env)

	instruction := BuildInstructionFromMessages(messages)
	if err := ensureNodeRuntime(ctx, rt, "claude", opts); err != nil {
		return &SessionResult{
			Engine:     a.Name(),
			ExitCode:   1,
			DurationMs: time.Since(start).Milliseconds(),
			Artifacts:  &SessionArtifacts{},
		}, err
	}
	cmd, promptDelivery, err := deliverPrompt(ctx, rt, opts, instruction, promptCommandBuilder{
		Inline: func(prompt string) string {
			return buildClaudePrintCmd(sessionID, a.effectiveModelName(ctx), prompt)
		},
		StdinFile: func(path string) string {
			return buildClaudePrintStdinCmd(sessionID, a.effectiveModelName(ctx), path)
		},
	})
	if err != nil {
		return &SessionResult{
			Engine:     a.Name(),
			ExitCode:   1,
			DurationMs: time.Since(start).Milliseconds(),
			Artifacts:  &SessionArtifacts{},
		}, err
	}

	result, err := rt.Exec(ctx, cmd, opts)
	sessionResult := a.buildSessionResult(ctx, rt, opts, instruction, start, result)
	if sessionResult != nil {
		sessionResult.PromptDelivery = promptDelivery
	}
	if providerErr := claudeProviderRunError(result, sessionResult); providerErr != nil {
		return sessionResult, providerErr
	}
	if err != nil {
		if sessionResult == nil {
			sessionResult = &SessionResult{
				Engine:     a.Name(),
				ExitCode:   1,
				DurationMs: time.Since(start).Milliseconds(),
				Stderr:     result.Stderr,
				Artifacts:  &SessionArtifacts{},
			}
		}
		return sessionResult, fmt.Errorf("claude-code run failed: %w", err)
	}

	if result.ExitCode != 0 {
		return sessionResult, fmt.Errorf("claude-code run failed (exit %d): %s", result.ExitCode, result.Stderr)
	}

	return sessionResult, nil
}

func (a *ClaudeCodeAgent) effectiveModelName(_ context.Context) string {
	return strings.TrimSpace(a.Cfg.ModelName)
}

func claudeProviderRunError(result ExecResult, sessionResult *SessionResult) error {
	if authMsg, ok := providerAuthFailureSignal(result, sessionResult); ok {
		markSessionFailed(sessionResult)
		return fmt.Errorf("claude-code authentication failed: %s", authMsg)
	}
	if rateLimitMsg, ok := providerRateLimitSignal(result, sessionResult); ok {
		markSessionFailed(sessionResult)
		return fmt.Errorf("claude-code provider rate limit: %s", rateLimitMsg)
	}
	return nil
}

func markSessionFailed(sessionResult *SessionResult) {
	if sessionResult != nil && sessionResult.ExitCode == 0 {
		sessionResult.ExitCode = 1
	}
}

func providerRateLimitSignal(result ExecResult, sessionResult *SessionResult) (string, bool) {
	for _, candidate := range []string{
		result.Stderr,
		result.Stdout,
		sessionDiagnosticText(sessionResult),
	} {
		if msg, ok := providerRateLimitText(candidate); ok {
			return msg, true
		}
	}
	return "", false
}

func providerAuthFailureSignal(result ExecResult, sessionResult *SessionResult) (string, bool) {
	for _, candidate := range []string{
		result.Stderr,
		result.Stdout,
		sessionDiagnosticText(sessionResult),
	} {
		if msg, ok := providerAuthFailureText(candidate); ok {
			return msg, true
		}
	}
	return "", false
}

func sessionDiagnosticText(sessionResult *SessionResult) string {
	if sessionResult == nil {
		return ""
	}
	if sessionResult.FinalMessage != "" {
		return sessionResult.FinalMessage
	}
	if sessionResult.Stderr != "" {
		return sessionResult.Stderr
	}
	return ""
}

func providerRateLimitText(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}

	scanText := trimmed
	const providerRateLimitScanWindow = 2048
	if len(scanText) > providerRateLimitScanWindow {
		scanText = scanText[len(scanText)-providerRateLimitScanWindow:]
	}
	lower := strings.ToLower(scanText)
	switch {
	case strings.Contains(scanText, "模型提供方限流"),
		strings.Contains(lower, "rate limit"),
		strings.Contains(lower, "too many requests"),
		strings.Contains(lower, "http 429"),
		strings.Contains(lower, "status 429"),
		strings.Contains(lower, "error 429"),
		strings.Contains(lower, "code 429"),
		strings.Contains(lower, "429 too many requests"),
		strings.Contains(lower, "request limit reached"):
		return trimmed, true
	default:
		return "", false
	}
}

func providerAuthFailureText(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}

	scanText := trimmed
	const providerAuthScanWindow = 4096
	if len(scanText) > providerAuthScanWindow {
		scanText = scanText[len(scanText)-providerAuthScanWindow:]
	}
	lower := strings.ToLower(scanText)
	// Remove spaces around colons so JSON like `"key": 403` and `"key":403` both match.
	compact := strings.ReplaceAll(lower, ": ", ":")
	compact = strings.ReplaceAll(compact, " :", ":")
	switch {
	case strings.Contains(lower, "authentication_failed"),
		strings.Contains(compact, "\"api_error_status\":403"),
		strings.Contains(compact, "\"error_status\":403"),
		strings.Contains(lower, "authentication error"):
		return trimmed, true
	default:
		return "", false
	}
}

func buildClaudePrintCmd(sessionID, model, instruction string) string {
	cmd := "claude --settings " + shellQuote(`{"disableAllHooks":true}`) + " --session-id " + sessionID + " -p --permission-mode=bypassPermissions"
	if model != "" {
		cmd += " --model " + shellQuote(model)
	}
	cmd += " " + shellQuote(instruction)
	return cmd
}

func buildClaudePrintStdinCmd(sessionID, model, promptPath string) string {
	cmd := "cat " + shellQuote(promptPath) + " | claude --settings " + shellQuote(`{"disableAllHooks":true}`) + " --session-id " + sessionID + " -p --permission-mode=bypassPermissions"
	if model != "" {
		cmd += " --model " + shellQuote(model)
	}
	return cmd
}

type claudePrintJSONResult struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	NumTurns  int    `json:"num_turns"`
	SessionID string `json:"session_id"`
}

// buildClaudePrintJSONSessionResult parses the JSON output produced by claude -p
// and builds a SessionResult. It is not yet called by Run because the current
// implementation uses the plain-text helper (buildClaudeTextSessionResult).
// This function is pre-built for the planned --output-format json mode
// (see docs/design-docs/0.1.0-design.md) and is covered by unit tests.
func buildClaudePrintJSONSessionResult(engine string, start time.Time, result ExecResult) *SessionResult {
	sessionResult := &SessionResult{
		Engine:     engine,
		ExitCode:   result.ExitCode,
		DurationMs: time.Since(start).Milliseconds(),
		Stderr:     result.Stderr,
		Artifacts: &SessionArtifacts{
			GeneratedFiles: []string{"stdout.json"},
		},
	}

	var payload claudePrintJSONResult
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		sessionResult.FinalMessage = result.Stdout
		if strings.TrimSpace(result.Stdout) != "" {
			sessionResult.Transcript = transcript.Transcript{
				{Role: transcript.RoleAssistant, Content: result.Stdout, Turn: 1},
			}
			sessionResult.Turns = 1
		}
		return sessionResult
	}

	sessionResult.FinalMessage = payload.Result
	sessionResult.Turns = payload.NumTurns
	if sessionResult.Turns == 0 && strings.TrimSpace(payload.Result) != "" {
		sessionResult.Turns = 1
	}
	if strings.TrimSpace(payload.Result) != "" {
		sessionResult.Transcript = transcript.Transcript{
			{Role: transcript.RoleAssistant, Content: payload.Result, Turn: 1},
		}
	}
	if payload.IsError && sessionResult.ExitCode == 0 {
		sessionResult.ExitCode = 1
	}
	return sessionResult
}

// buildSessionResult constructs a SessionResult by first attempting to locate
// and parse the Claude session JSONL file (for full transcript and token
// usage). If the session file is unavailable it falls back to plain-text
// stdout parsing.
func (a *ClaudeCodeAgent) buildSessionResult(ctx context.Context, rt Runtime, opts ExecOptions, instruction string, start time.Time, result ExecResult) *SessionResult {
	var trans transcript.Transcript
	var finalMsg string
	var inputTokens, outputTokens int

	cleanupCtx, cleanupCancel := sessionCleanupContext(ctx)
	defer cleanupCancel()

	generatedFiles := []string{}
	if artifactPath, err := persistSessionArtifact(cleanupCtx, rt, opts.ArtifactDir, "stdout.json", result.Stdout); err == nil {
		if opts.ArtifactDir == "" {
			generatedFiles = append(generatedFiles, artifactPath)
		}
	}

	var cleanupSession func()
	generatedFiles, cleanupSession = withDownloadedSession(
		cleanupCtx, rt, opts.ArtifactDir, findClaudeSessionFile(cleanupCtx, rt), generatedFiles,
		func(artifactPath string) {
			t, f, inTok, outTok := parseSessionFile(artifactPath)
			if len(t) > 0 {
				trans = t
				finalMsg = f
			}
			inputTokens, outputTokens = inTok, outTok
		},
	)
	defer cleanupSession()

	// Fallback: build a minimal transcript from stdout.
	if trans == nil && result.Stdout != "" {
		final := strings.TrimSpace(result.Stdout)
		if final != "" {
			trans = transcript.Transcript{
				{Role: transcript.RoleUser, Content: instruction, Turn: 1},
				{Role: transcript.RoleAssistant, Content: final, Turn: 1},
			}
			finalMsg = final
		}
	}

	return &SessionResult{
		Engine:       a.Name(),
		ExitCode:     result.ExitCode,
		DurationMs:   time.Since(start).Milliseconds(),
		Turns:        countTurns(trans),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		FinalMessage: finalMsg,
		Stderr:       result.Stderr,
		Transcript:   trans,
		Artifacts: &SessionArtifacts{
			GeneratedFiles: generatedFiles,
		},
	}
}

func buildClaudeTextSessionResult(engine, instruction string, start time.Time, result ExecResult) *SessionResult {
	sessionResult := &SessionResult{
		Engine:     engine,
		ExitCode:   result.ExitCode,
		DurationMs: time.Since(start).Milliseconds(),
		Stderr:     result.Stderr,
		Artifacts: &SessionArtifacts{
			GeneratedFiles: []string{"stdout.json"},
		},
	}

	final := strings.TrimSpace(result.Stdout)
	sessionResult.FinalMessage = final
	if final != "" {
		sessionResult.Transcript = transcript.Transcript{
			{Role: transcript.RoleUser, Content: instruction, Turn: 1},
			{Role: transcript.RoleAssistant, Content: final, Turn: 1},
		}
		sessionResult.Turns = 1
	}
	return sessionResult
}

// findClaudeSessionFile resolves the newest session JSONL under the Claude
// projects tree (~/.claude/projects/<workspace-key>/) for this workspace.
func findClaudeSessionFile(ctx context.Context, rt Runtime) string {
	return findAgentSessionJSONL(ctx, rt, agentSessionLookup{
		envVar:    "SKILL_UP_CLAUDE_WSKEY",
		rootTmpl:  "$home/.claude/projects/$SKILL_UP_CLAUDE_WSKEY",
		findExtra: "",
	})
}

// buildStreamJSON converts messages to stream-json format for input.
func (a *ClaudeCodeAgent) buildStreamJSON(messages []transcript.Message) string {
	if len(messages) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, msg := range messages {
		contentJSON, err := claudeInputContentJSON(msg.Content)
		if err != nil {
			contentJSON = []byte(`[{"type":"text","text":"?"}]`)
		}
		switch msg.Role {
		case transcript.RoleUser:
			fmt.Fprintf(&sb, `{"type":"user","message":{"role":"user","content":%s}}`+"\n", contentJSON)
		case transcript.RoleAssistant:
			fmt.Fprintf(&sb, `{"type":"assistant","message":{"role":"assistant","content":%s}}`+"\n", contentJSON)
		}
	}
	return sb.String()
}

func claudeInputContentJSON(content any) ([]byte, error) {
	switch v := content.(type) {
	case string:
		return json.Marshal([]map[string]string{{
			"type": "text",
			"text": v,
		}})
	default:
		return json.Marshal(content)
	}
}

type streamParseState struct {
	messages          []transcript.Message
	finalMsg          string
	totalInputTokens  int
	totalOutputTokens int
	currentTurn       int
}

func applyStreamEvent(state *streamParseState, event *streamEvent) {
	switch event.Type {
	case "user":
		applyStreamUserEvent(state, event)
	case "assistant": //nolint:goconst
		applyStreamAssistantEvent(state, event)
	case "tool_call":
		applyStreamToolCallEvent(state, event)
	case "tool_result":
		applyStreamToolResultEvent(state, event)
	case "result":
		applyStreamResultEvent(state, event)
	}
}

// applyStreamUserEvent advances the turn counter and appends a user message.
// A user event always advances the turn (even when the inline message payload
// is missing, to keep tool-call/tool-result turns aligned with the prompt).
func applyStreamUserEvent(state *streamParseState, event *streamEvent) {
	state.currentTurn++
	if event.Message == nil {
		return
	}
	state.messages = append(state.messages, transcript.Message{
		Role:    transcript.RoleUser,
		Content: extractTextFromContent(event.Message.Content),
		Turn:    state.currentTurn,
	})
}

// applyStreamAssistantEvent appends an assistant message and updates token
// counters from message.usage when present.
func applyStreamAssistantEvent(state *streamParseState, event *streamEvent) {
	if event.Message == nil {
		return
	}
	state.messages = append(state.messages, transcript.Message{
		Role:    transcript.RoleAssistant,
		Content: extractTextFromContent(event.Message.Content),
		Turn:    state.currentTurn,
	})
	if in := sessionUsageInputTotal(event.Message.Usage); in > 0 {
		state.totalInputTokens = in
	}
	if event.Message.Usage.OutputTokens > 0 {
		state.totalOutputTokens = event.Message.Usage.OutputTokens
	}
}

// applyStreamToolCallEvent appends a tool-call message at the current turn.
func applyStreamToolCallEvent(state *streamParseState, event *streamEvent) {
	if event.ToolCall == nil {
		return
	}
	state.messages = append(state.messages, transcript.Message{
		Role: transcript.RoleToolCall,
		ToolCall: &transcript.ToolCallInfo{
			ID:        event.ToolCall.ID,
			Name:      event.ToolCall.Name,
			Arguments: event.ToolCall.Input,
		},
		Turn: state.currentTurn,
	})
}

// applyStreamToolResultEvent appends a tool-result message at the current turn.
func applyStreamToolResultEvent(state *streamParseState, event *streamEvent) {
	if event.ToolResult == nil {
		return
	}
	state.messages = append(state.messages, transcript.Message{
		Role: transcript.RoleToolResult,
		ToolResult: &transcript.ToolResultInfo{
			CallID:  event.ToolResult.CallID,
			Status:  event.ToolResult.Status,
			Content: event.ToolResult.Content,
		},
		Turn: state.currentTurn,
	})
}

// applyStreamResultEvent records the final assistant message and the terminal
// usage roll-up emitted at the end of a stream-json session.
func applyStreamResultEvent(state *streamParseState, event *streamEvent) {
	if event.FinalMessage != "" {
		state.finalMsg = event.FinalMessage
	}
	if event.Usage == nil {
		return
	}
	if in := sessionUsageInputTotal(*event.Usage); in > 0 {
		state.totalInputTokens = in
	}
	if event.Usage.OutputTokens > 0 {
		state.totalOutputTokens = event.Usage.OutputTokens
	}
}

// parseStreamOutput parses stream-json output and extracts transcript, final message, and tokens.
func parseStreamOutput(output string) (trans transcript.Transcript, finalMsg string, inputTokens int, outputTokens int) { //nolint:revive,nonamedreturns // named returns required by revive confusing-results
	var state streamParseState
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, "", 0, 0
	}

	if strings.HasPrefix(trimmed, "[") {
		var events []streamEvent
		if err := json.Unmarshal([]byte(trimmed), &events); err == nil {
			for i := range events {
				applyStreamEvent(&state, &events[i])
			}
			return state.messages, state.finalMsg, state.totalInputTokens, state.totalOutputTokens
		}
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	const maxTokenLen = 1024 * 1024
	buf := make([]byte, maxTokenLen)
	scanner.Buffer(buf, maxTokenLen)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line == "[]" || line == "{}" {
			continue
		}

		var event streamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		applyStreamEvent(&state, &event)
	}

	return state.messages, state.finalMsg, state.totalInputTokens, state.totalOutputTokens
}

// streamEvent represents an event in Claude's stream-json output.
type streamEvent struct {
	Type         string            `json:"type"`
	Message      *streamMessage    `json:"message,omitempty"`
	ToolCall     *streamToolCall   `json:"tool_call,omitempty"`
	ToolResult   *streamToolResult `json:"tool_result,omitempty"`
	FinalMessage string            `json:"result,omitempty"`
	Usage        *streamUsage      `json:"usage,omitempty"`
}

// streamMessage represents a message in stream-json.
type streamMessage struct {
	Content any         `json:"content"`
	Role    string      `json:"role"`
	Usage   streamUsage `json:"usage"`
}

// streamUsage represents token usage in stream-json and session JSONL.
// Prompt-cached runs often report most input as cache_read_input_tokens /
// cache_creation_input_tokens with input_tokens at or near zero.
type streamUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func sessionUsageInputTotal(u streamUsage) int {
	return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}

// streamToolCall represents a tool call in stream-json.
type streamToolCall struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// streamToolResult represents a tool result in stream-json.
type streamToolResult struct {
	CallID  string `json:"call_id"`
	Status  string `json:"status"`
	Content any    `json:"content"`
}

// countTurns counts the number of assistant turns in a transcript.
func countTurns(trans transcript.Transcript) int {
	if trans == nil {
		return 0
	}
	turns := 0
	for _, msg := range trans {
		if msg.Role == transcript.RoleAssistant {
			turns++
		}
	}
	return turns
}

// mapsClone creates a shallow copy of a map.
func mapsClone(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	maps.Copy(result, m)

	return result
}

// parseSessionFile reads a session JSONL file (Claude Code or QoderCLI; same
// line schema) and returns transcript, final assistant text, and token fields
// derived from assistant message.usage lines.
//
// Session file semantics (verified against local ~/.claude/projects/*.jsonl):
// message.usage on successive "assistant" lines is a running/cumulative meter
// snapshot, not a per-line delta—e.g. the same input_tokens often repeats across
// many lines while output_tokens ratchets up within one model step; input_tokens
// jumps when the prompt grows. Sidechains/resets can drop counters mid-file.
//
// Token aggregation takes the max over assistant lines of (input_tokens +
// cache_read_input_tokens + cache_creation_input_tokens) and max of
// output_tokens (high-water over the file). After a mid-file reset, earlier
// peaks can still dominate; that matches "best single reading" without summing
// duplicate cumulative snapshots.
func parseSessionFile(sf string) (trans transcript.Transcript, finalMsg string, inputTokens int, outputTokens int) { //nolint:revive,nonamedreturns,cyclop // named returns required by revive confusing-results; per-line dispatch loop kept inline for readability.
	file, err := os.Open(sf)
	if err != nil {
		return nil, "", 0, 0
	}
	defer file.Close() //nolint:errcheck

	var messages []transcript.Message
	currentTurn := 0

	scanner := bufio.NewScanner(file)
	const maxTokenLen = 1024 * 1024
	buf := make([]byte, maxTokenLen)
	scanner.Buffer(buf, maxTokenLen)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line == "[]" || line == "{}" {
			continue
		}

		var event claudeEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		var final string
		var turnMsgs []transcript.Message
		turnMsgs, final, currentTurn = processClaudeEvent(event, currentTurn)
		if turnMsgs != nil {
			messages = append(messages, turnMsgs...)
		}
		if final != "" {
			finalMsg = final
		}
		if event.Type == "assistant" && event.Message != nil { //nolint:goconst
			inputTokens = max(inputTokens, sessionUsageInputTotal(event.Message.Usage))
			outputTokens = max(outputTokens, event.Message.Usage.OutputTokens)
		}
	}

	if finalMsg == "" && len(messages) > 0 {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == transcript.RoleAssistant && len(messages[i].Content) > 0 {
				finalMsg = messages[i].Content
				break
			}
		}
	}

	_ = scanner.Err()

	return messages, finalMsg, inputTokens, outputTokens
}

func processClaudeEvent(event claudeEvent, turn int) ([]transcript.Message, string, int) {
	switch event.Type {
	case "user":
		turn++
		if event.Message == nil {
			return nil, "", turn
		}

		messages := extractToolMessagesFromContent(event.Message.Content, turn)
		if text := extractTextFromContent(event.Message.Content); text != "" {
			messages = append([]transcript.Message{{Role: transcript.RoleUser, Content: text, Turn: turn}}, messages...)
		}
		return messages, "", turn

	case "assistant": //nolint:goconst
		return processClaudeAssistantEvent(event, turn)

	case "tool_call":
		if event.ToolCall == nil {
			return nil, "", turn
		}

		return []transcript.Message{
			{
				Role: transcript.RoleToolCall,
				ToolCall: &transcript.ToolCallInfo{
					ID:        event.ToolCall.ID,
					Name:      event.ToolCall.Name,
					Arguments: event.ToolCall.Input,
				},
				Turn: turn,
			},
		}, "", turn

	case "tool_result":
		if event.ToolResult == nil {
			return nil, "", turn
		}

		return []transcript.Message{
			{
				Role: transcript.RoleToolResult,
				ToolResult: &transcript.ToolResultInfo{
					CallID:  event.ToolResult.CallID,
					Status:  event.ToolResult.Status,
					Content: event.ToolResult.Content,
				},
				Turn: turn,
			},
		}, "", turn

	case "result":
		if event.FinalMessage != nil {
			return nil, event.FinalMessage.Content, turn
		}

		return nil, "", turn

	case "error":
		if event.IsAPIErrorMessage && event.Message != nil {
			return []transcript.Message{
				{Role: transcript.RoleError, Content: extractTextFromContent(event.Message.Content), Turn: turn},
			}, "", turn
		}
		return nil, "", turn
	}

	return nil, "", turn
}

func processClaudeAssistantEvent(event claudeEvent, turn int) ([]transcript.Message, string, int) {
	if event.Message == nil {
		return nil, "", turn
	}

	role := transcript.RoleAssistant
	if event.IsAPIErrorMessage {
		role = transcript.RoleError
	}
	messages := extractToolMessagesFromContent(event.Message.Content, turn)
	if text := extractTextFromContent(event.Message.Content); text != "" {
		messages = append(messages, transcript.Message{Role: role, Content: text, Turn: turn})
	}
	return messages, "", turn
}

// extractTextFromContent extracts text content from Claude message content block.
func extractTextFromContent(content any) string {
	if content == nil {
		return ""
	}

	switch c := content.(type) {
	case string:
		return c
	case []any:
		var textParts []string
		for _, block := range c {
			blockMap, ok := block.(map[string]any)
			if !ok {
				continue
			}
			blockType, _ := blockMap["type"].(string)
			switch blockType {
			case "text":
				if text, ok := blockMap["text"].(string); ok {
					textParts = append(textParts, text)
				}
			case "tool_use":
				if name, ok := blockMap["name"].(string); ok {
					textParts = append(textParts, "[tool: "+name+"]")
				}
			}
		}
		if len(textParts) > 0 {
			return strings.Join(textParts, "\n\n")
		}
	}

	return ""
}

func extractToolMessagesFromContent(content any, turn int) []transcript.Message {
	blocks, ok := content.([]any)
	if !ok {
		return nil
	}
	messages := make([]transcript.Message, 0)
	for _, block := range blocks {
		blockMap, ok := block.(map[string]any)
		if !ok {
			continue
		}
		blockType, _ := blockMap["type"].(string)
		switch blockType {
		case "tool_use":
			id, _ := blockMap["id"].(string)
			name, _ := blockMap["name"].(string)
			input, _ := blockMap["input"].(map[string]any)
			messages = append(messages, transcript.Message{
				Role: transcript.RoleToolCall,
				ToolCall: &transcript.ToolCallInfo{
					ID:        id,
					Name:      name,
					Arguments: input,
				},
				Turn: turn,
			})
		case "tool_result":
			callID, _ := blockMap["tool_use_id"].(string)
			status := toolStatusSuccess
			if isErr, _ := blockMap["is_error"].(bool); isErr {
				status = toolStatusError
			}
			messages = append(messages, transcript.Message{
				Role: transcript.RoleToolResult,
				ToolResult: &transcript.ToolResultInfo{
					CallID:  callID,
					Status:  status,
					Content: contentBlockToString(blockMap["content"]),
				},
				Turn: turn,
			})
		}
	}
	return messages
}

func contentBlockToString(content any) string {
	switch c := content.(type) {
	case nil:
		return ""
	case string:
		return c
	default:
		data, err := json.Marshal(c)
		if err != nil {
			return fmt.Sprint(c)
		}
		return string(data)
	}
}

// claudeEvent represents an event in a Claude session JSONL file.
type claudeEvent struct {
	Type              string            `json:"type"`
	Message           *claudeMessage    `json:"message,omitempty"`
	ToolCall          *claudeToolCall   `json:"tool_call,omitempty"`
	ToolResult        *claudeToolResult `json:"tool_result,omitempty"`
	FinalMessage      *claudeFinalMsg   `json:"final_message,omitempty"`
	IsAPIErrorMessage bool              `json:"isApiErrorMessage,omitempty"`
	APIErrorStatus    int               `json:"apiErrorStatus,omitempty"`
}

// claudeMessage represents a message in a Claude session.
type claudeMessage struct {
	Content any         `json:"content"`
	Role    string      `json:"role"`
	Usage   streamUsage `json:"usage"`
}

// claudeToolCall represents a tool call in a Claude session.
type claudeToolCall struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// claudeToolResult represents a tool result in a Claude session.
type claudeToolResult struct {
	CallID  string `json:"call_id"`
	Status  string `json:"status"`
	Content any    `json:"content"`
}

// claudeFinalMsg represents the final message in a Claude session.
type claudeFinalMsg struct {
	Content string `json:"content"`
}
