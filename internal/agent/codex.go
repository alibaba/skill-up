package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/observability"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// CodexAgent implements Agent for codex CLI.
type CodexAgent struct {
	CLIAgent
}

const (
	codexEngineName     = "codex"
	codexDefaultVersion = "0.80.0"
	codexProcessSandbox = "--sandbox workspace-write"
	codexBypassSandbox  = "--dangerously-bypass-approvals-and-sandbox"
	codexCustomWireAPI  = "chat"
	// codexOpenAIOverrideProvider is the provider key emitted when callers
	// configure provider=openai with a custom BaseURL. The literal "openai"
	// name can't be reused because codex ships a built-in provider config
	// under that key and won't reliably merge our overrides onto it. The
	// "skill-up-" prefix makes the synthesised entry obvious when it shows
	// up in codex command lines or logs (e.g. `-c model_provider="skill-up-openai"`).
	codexOpenAIOverrideProvider = "skill-up-openai"
	codexUserRole               = "user"
	codexOutputText             = "output_text"
	codexInputText              = "input_text"
	codexAgentMsg               = "agent_message"
	codexFnCall                 = "function_call"
	codexFnCallOut              = "function_call_output"
	codexTokenCount             = "token_count"
	codexStatusSuccess          = "success"
	codexStatusError            = "error"
)

// codexExecPathProbeCmd resolves $HOME/.local/bin (the codex binary, installed
// via `npm install -g` under the bootstrap's npm_config_prefix) and
// $HOME/.nvm/current/bin (node, needed by codex's #!/usr/bin/env node shebang).
const codexExecPathProbeCmd = `printf '%s' "$HOME/.local/bin:$HOME/.nvm/current/bin:$PATH"`

// NewCodexAgent creates a new CodexAgent.
func NewCodexAgent(cfg Config) *CodexAgent {
	if cfg.Name == "" {
		cfg.Name = codexEngineName
	}
	if cfg.CheckCmd == "" {
		cfg.CheckCmd = "command -v codex"
	}
	if cfg.SkillPath == "" {
		cfg.SkillPath = ".codex/skills"
	}

	return &CodexAgent{
		CLIAgent: CLIAgent{BaseAgent: NewBaseAgent(cfg)},
	}
}

// Install installs Codex CLI when it is not already available in the runtime.
//
//nolint:dupl // each agent Install shares the same probe→merge→exec lifecycle; the deltas (probe const, default install cmd) are pulled out, leaving the orchestration intentionally similar.
func (a *CodexAgent) Install(ctx context.Context, rt Runtime) error {
	a.probeAndMergePATH(ctx, rt, codexExecPathProbeCmd)

	opts := ExecOptions{Cwd: "/"}
	opts = a.mergeExecOptionsEnv(ctx, opts, nil, nil)

	installCmd := a.Cfg.InstallCmd
	if installCmd == "" {
		installCmd = defaultCodexInstallCmd()
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

// InstallMCP installs MCP servers with the Codex CLI.
func (a *CodexAgent) InstallMCP(ctx context.Context, rt Runtime, mcpCfg runtime.MCPConfig) error {
	return installMCPServers(ctx, rt, mcpCfg, buildCodexMCPInstallCmd)
}

func buildCodexMCPInstallCmd(server runtime.MCPServerConfig) (string, error) {
	var cmd strings.Builder
	cmd.WriteString("codex mcp remove ")
	cmd.WriteString(shellQuote(server.Name))
	cmd.WriteString(" >/dev/null 2>&1 || true\n")
	cmd.WriteString("codex mcp add ")
	cmd.WriteString(shellQuote(server.Name))
	switch server.Transport {
	case mcpTransportStdio:
		if server.Command == "" {
			return "", fmt.Errorf("mcp server %q stdio transport requires command", server.Name)
		}
		for _, key := range sortedMapKeys(server.Env) {
			envArg, err := shellEnvAssignment(key)
			if err != nil {
				return "", fmt.Errorf("mcp server %q env %q is invalid: %w", server.Name, key, err)
			}
			cmd.WriteString(" --env ")
			cmd.WriteString(envArg)
		}
		cmd.WriteString(" -- ")
		cmd.WriteString(shellQuote(server.Command))
		for _, arg := range server.Args {
			cmd.WriteByte(' ')
			cmd.WriteString(shellQuote(arg))
		}
	case "", mcpTransportHTTP:
		if server.Endpoint == "" {
			return "", fmt.Errorf("mcp server %q http transport requires endpoint", server.Name)
		}
		if len(server.Headers) > 0 {
			return buildCodexMCPRemoteInstallCmd(server)
		}
		endpoint, err := shellExpandedValue(server.Endpoint)
		if err != nil {
			return "", fmt.Errorf("mcp server %q endpoint is invalid: %w", server.Name, err)
		}
		cmd.WriteString(" --url ")
		cmd.WriteString(endpoint)
	default:
		return "", fmt.Errorf("mcp server %q transport %q is not supported by codex", server.Name, server.Transport)
	}
	return nodeRuntimeCommandWithGuard("codex", cmd.String()), nil
}

func buildCodexMCPRemoteInstallCmd(server runtime.MCPServerConfig) (string, error) {
	bridgeScript, err := buildCodexMCPRemoteBridgeScript(server)
	if err != nil {
		return "", err
	}
	var cmd strings.Builder
	cmd.WriteString("codex mcp remove ")
	cmd.WriteString(shellQuote(server.Name))
	cmd.WriteString(" >/dev/null 2>&1 || true\n")
	cmd.WriteString("codex mcp add ")
	cmd.WriteString(shellQuote(server.Name))
	for _, envName := range sortedUniqueMapValues(server.HeaderEnv) {
		if !shellEnvNamePattern.MatchString(envName) {
			return "", fmt.Errorf("mcp server %q header environment variable %q is invalid", server.Name, envName)
		}
		if _, ok := server.Env[envName]; ok {
			envArg, err := shellEnvAssignment(envName)
			if err != nil {
				return "", fmt.Errorf("mcp server %q header environment variable %q is invalid: %w", server.Name, envName, err)
			}
			cmd.WriteString(" --env ")
			cmd.WriteString(envArg)
		}
	}
	cmd.WriteString(" -- 'sh' '-c' ")
	cmd.WriteString(shellQuote(bridgeScript))
	cmd.WriteString(" 'mcp-remote' ")
	endpoint, err := shellExpandedValue(server.Endpoint)
	if err != nil {
		return "", fmt.Errorf("mcp server %q endpoint is invalid: %w", server.Name, err)
	}
	cmd.WriteString(endpoint)
	return nodeRuntimeCommandWithGuard("codex", cmd.String()), nil
}

func buildCodexMCPRemoteBridgeScript(server runtime.MCPServerConfig) (string, error) {
	var script strings.Builder
	script.WriteString("exec npx mcp-remote \"$1\"")
	for _, key := range sortedMapKeys(server.Headers) {
		if envName := server.HeaderEnv[key]; envName != "" {
			if !shellEnvNamePattern.MatchString(envName) {
				return "", fmt.Errorf("mcp server %q header %q environment variable %q is invalid", server.Name, key, envName)
			}
			script.WriteString(" --header ")
			header, err := shellExpandedValue(key + ":" + server.Headers[key])
			if err != nil {
				return "", fmt.Errorf("mcp server %q header %q is invalid: %w", server.Name, key, err)
			}
			script.WriteString(header)
			continue
		}
		header, err := shellExpandedValue(key + ":" + server.Headers[key])
		if err != nil {
			return "", fmt.Errorf("mcp server %q header %q is invalid: %w", server.Name, key, err)
		}
		script.WriteString(" --header ")
		script.WriteString(header)
	}
	script.WriteString(" 2>/dev/null")
	return script.String(), nil
}

func defaultCodexInstallCmd() string {
	lines := []string{
		"set -e",
		"if command -v codex >/dev/null 2>&1 && codex --version 2>/dev/null | grep -q " + shellQuote(codexDefaultVersion) + "; then exit 0; fi",
	}
	lines = append(lines, nodeBootstrapLines(agentNodeDefaultVersion)...)
	lines = append(lines, "npm install -g --include=optional "+shellQuote("@openai/codex@"+codexDefaultVersion))
	return strings.Join(lines, "\n")
}

// CheckCredentials checks if OPENAI_API_KEY is set.
// Codex can also rely on local login state, so missing env var is informational only.
func (a *CodexAgent) CheckCredentials(ctx context.Context) error {
	a.logCredentialStatus(
		ctx,
		credential.EnvOpenAIAPIKey,
		credential.EnvOpenAIBaseURL,
		"OPENAI_API_KEY not set, codex will rely on existing login state if available",
	)
	return nil
}

// Run executes codex in non-interactive JSON mode and converts events into a transcript.
//
//nolint:dupl
func (a *CodexAgent) Run(ctx context.Context, rt Runtime, opts ExecOptions, messages []transcript.Message) (*SessionResult, error) {
	start := time.Now()

	instruction := BuildInstructionFromMessages(messages)
	// bypass_sandbox kwarg overrides the runtime-derived choice; useful when
	// the host kernel does not support codex's Landlock-based linux-sandbox
	// (e.g. CI containers that block Landlock syscalls).
	sandboxFlag := codexBypassSandbox
	if rt.RequiresProcessSandbox() && !EngineKwargBool(a.Cfg.Kwargs, KwargBypassSandbox) {
		sandboxFlag = codexProcessSandbox
	}
	lastMessagePath := filepath.Join(rt.Workspace(), ".skill-up", "codex-last-message.txt")
	cmd := "mkdir -p " + shellQuote(filepath.Dir(lastMessagePath)) + "\n" +
		nodeRuntimeCommandWithGuard("codex", buildCodexRunCmdWithLastMessage(instruction, a.effectiveModelName(ctx), a.runProviderConfig(ctx), sandboxFlag, lastMessagePath))

	envVars := a.credentialEnvVars(credential.EnvOpenAIAPIKey, credential.EnvOpenAIBaseURL)
	opts = a.mergeExecOptionsEnv(ctx, opts, envVars, a.buildAgentObservabilityAttrs(nil))
	ctx = observability.ContextWithConfiguredAgentSpanAttributes(ctx, opts.Env)

	result, err := rt.Exec(ctx, cmd, opts)
	sessionResult := a.buildSessionResult(ctx, rt, opts, instruction, start, result, lastMessagePath)
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
		return sessionResult, fmt.Errorf("codex run failed: %w", err)
	}

	if result.ExitCode != 0 {
		return sessionResult, fmt.Errorf("codex run failed (exit %d): %s", result.ExitCode, result.Stderr)
	}

	return sessionResult, nil
}

func (a *CodexAgent) effectiveModelName(ctx context.Context) string {
	if a.Cfg.ModelProvider == "" || a.Cfg.ModelProvider == agentProviderOpenAI {
		return a.Cfg.ModelName
	}
	reason := codexCustomProviderUnavailableReason(a.Cfg.ModelProvider, a.Cfg.BaseURL)
	if reason == "" {
		return a.Cfg.ModelName
	}
	logging.WarnContextf(
		ctx,
		"codex custom model provider %q %s; model override %q is omitted and local codex model settings will be used instead",
		a.Cfg.ModelProvider,
		reason,
		a.Cfg.ModelName,
	)
	return ""
}

type codexProviderConfig struct {
	Name    string
	Label   string
	BaseURL string
	EnvKey  string
	WireAPI string
}

func (a *CodexAgent) runProviderConfig(ctx context.Context) codexProviderConfig {
	// Empty provider + non-empty BaseURL is the same situation as
	// provider="openai" + non-empty BaseURL: the caller pointed codex at a
	// non-default endpoint without naming a distinct provider. Both cases
	// must synthesise the "skill-up-openai" override so codex emits a full
	// `model_providers.skill-up-openai.{base_url,env_key,wire_api}` config —
	// the legacy single `-c openai_base_url=...` fallback below is silently
	// ignored by codex (it keeps using its bundled api.openai.com endpoint
	// with wire_api="responses"), which makes any /chat/completions-only
	// upstream (idealab, dashscope OpenAI-compat, …) return 400 from
	// `codex_api::endpoint::responses`. wire_api="chat" forces codex onto
	// /chat/completions instead.
	//
	// We can't reuse the literal "openai" name because codex ships a
	// built-in provider config under that key and merging the override
	// onto it is unreliable across codex versions. A unique key forces
	// codex to construct a brand-new provider definition from the -c
	// flags alone.
	if a.Cfg.ModelProvider == "" || a.Cfg.ModelProvider == agentProviderOpenAI {
		if a.Cfg.BaseURL == "" {
			return codexProviderConfig{}
		}
		return codexProviderConfig{
			Name:    codexOpenAIOverrideProvider,
			Label:   codexOpenAIOverrideProvider,
			BaseURL: a.Cfg.BaseURL,
			EnvKey:  credential.EnvOpenAIAPIKey,
			WireAPI: codexCustomWireAPI,
		}
	}
	if reason := codexCustomProviderUnavailableReason(a.Cfg.ModelProvider, a.Cfg.BaseURL); reason != "" {
		logging.DebugContextf(ctx, "codex provider config omitted for provider %q: %s", a.Cfg.ModelProvider, reason)
		return codexProviderConfig{}
	}
	return codexProviderConfig{
		Name:    a.Cfg.ModelProvider,
		Label:   a.Cfg.ModelProvider,
		BaseURL: a.Cfg.BaseURL,
		EnvKey:  credential.EnvOpenAIAPIKey,
		WireAPI: codexCustomWireAPI,
	}
}

func (a *CodexAgent) buildSessionResult(
	ctx context.Context,
	rt Runtime,
	opts ExecOptions,
	instruction string,
	start time.Time,
	result ExecResult,
	lastMessagePath string,
) *SessionResult {
	cleanupCtx, cleanupCancel := sessionCleanupContext(ctx)
	defer cleanupCancel()

	generatedFiles := []string{}
	if result.Stdout != "" {
		if artifactPath, err := persistSessionArtifact(cleanupCtx, rt, opts.ArtifactDir, "stdout.json", result.Stdout); err == nil {
			if opts.ArtifactDir == "" {
				generatedFiles = append(generatedFiles, artifactPath)
			}
		}
	}
	streamParsed := parseCodexOutput(ctx, result.Stdout)
	trans, finalMsg := streamParsed.transcript, streamParsed.finalMsg
	inputTokens, outputTokens := streamParsed.inputTokens, streamParsed.outputTokens

	var cleanupSession func()
	generatedFiles, cleanupSession = withDownloadedSession(
		cleanupCtx, rt, opts.ArtifactDir, findCodexSessionPath(cleanupCtx, rt, streamParsed.threadID), generatedFiles,
		func(artifactPath string) {
			sessionParsed := parseCodexSessionFile(artifactPath)
			if len(sessionParsed.transcript) > len(streamParsed.transcript) &&
				(sessionParsed.finalMsg != "" || result.ExitCode != 0 || finalMsg == "") {
				trans, finalMsg = sessionParsed.transcript, sessionParsed.finalMsg
				inputTokens = sessionParsed.inputTokens
				outputTokens = sessionParsed.outputTokens
			}
		},
	)
	defer cleanupSession()

	if trans == nil && instruction != "" {
		trans = transcript.Transcript{
			{Role: transcript.RoleUser, Content: instruction, Turn: 1},
		}
	}
	if finalMsg == "" {
		finalMsg = trans.FinalAssistantMessage()
	}
	var lastMsgCleanup func()
	finalMsg, trans, generatedFiles, lastMsgCleanup = resolveCodexLastMessage(cleanupCtx, rt, opts.ArtifactDir, lastMessagePath, finalMsg, trans, generatedFiles)
	defer lastMsgCleanup()

	return &SessionResult{
		Engine:       a.Name(),
		ExitCode:     result.ExitCode,
		DurationMs:   time.Since(start).Milliseconds(),
		Turns:        codexTurns(trans),
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

func resolveCodexLastMessage(ctx context.Context, rt Runtime, artifactDir, lastMessagePath, finalMsg string, trans transcript.Transcript, generatedFiles []string) (string, transcript.Transcript, []string, func()) {
	if finalMsg != "" || lastMessagePath == "" {
		return finalMsg, trans, generatedFiles, func() {}
	}
	artifactPath, registeredPath, cleanup, ok := downloadSessionArtifact(ctx, rt, artifactDir, lastMessagePath)
	if !ok {
		return finalMsg, trans, generatedFiles, func() {}
	}
	if registeredPath != "" {
		generatedFiles = append(generatedFiles, registeredPath)
	}
	if data, err := os.ReadFile(artifactPath); err == nil {
		finalMsg = strings.TrimSpace(string(data))
		if finalMsg != "" {
			trans = append(trans, transcript.Message{
				Role:    transcript.RoleAssistant,
				Content: finalMsg,
				Turn:    max(codexTurns(trans), 1),
			})
		}
	}
	return finalMsg, trans, generatedFiles, cleanup
}

func buildCodexRunCmd(instruction, model string, provider codexProviderConfig, sandboxFlag string) string {
	return buildCodexRunCmdWithLastMessage(instruction, model, provider, sandboxFlag, "")
}

func buildCodexRunCmdWithLastMessage(instruction, model string, provider codexProviderConfig, sandboxFlag, lastMessagePath string) string {
	cmd := "codex exec --json --skip-git-repo-check"
	if sandboxFlag != "" {
		cmd += " " + sandboxFlag
	}
	cmd += codexProviderFlags(provider)
	if model != "" {
		cmd += " -m " + shellQuote(model)
	}
	if lastMessagePath != "" {
		cmd += " --output-last-message " + shellQuote(lastMessagePath)
	}
	cmd += " " + shellQuote(instruction)

	return cmd
}

func codexProviderFlags(provider codexProviderConfig) string {
	if provider.Name == "" {
		return ""
	}
	flags := " -c " + shellQuote("model_provider="+strconv.Quote(provider.Name))
	if provider.Label != "" {
		flags += " -c " + shellQuote("model_providers."+provider.Name+".name="+strconv.Quote(provider.Label))
	}
	if provider.BaseURL != "" {
		flags += " -c " + shellQuote("model_providers."+provider.Name+".base_url="+strconv.Quote(provider.BaseURL))
	}
	if provider.EnvKey != "" {
		flags += " -c " + shellQuote("model_providers."+provider.Name+".env_key="+strconv.Quote(provider.EnvKey))
	}
	if provider.WireAPI != "" {
		flags += " -c " + shellQuote("model_providers."+provider.Name+".wire_api="+strconv.Quote(provider.WireAPI))
	}
	return flags
}

func codexCustomProviderUnavailableReason(provider, baseURL string) string {
	if provider == "" || provider == agentProviderOpenAI {
		return ""
	}
	if baseURL == "" {
		return "requires base_url"
	}
	if !isCodexProviderName(provider) {
		return "must contain only letters, digits, underscores, or hyphens"
	}
	return ""
}

func isCodexProviderName(provider string) bool {
	for _, r := range provider {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return provider != ""
}

type codexEvent struct {
	Type     string      `json:"type"`
	ThreadID string      `json:"thread_id,omitempty"`
	Item     *codexItem  `json:"item,omitempty"`
	Usage    *codexUsage `json:"usage,omitempty"`
}

type codexItem struct {
	ID               string         `json:"id"`
	Type             string         `json:"type"`
	Text             string         `json:"text,omitempty"`
	Command          string         `json:"command,omitempty"`
	Server           string         `json:"server,omitempty"`
	Tool             string         `json:"tool,omitempty"`
	Arguments        map[string]any `json:"arguments,omitempty"`
	Result           any            `json:"result,omitempty"`
	AggregatedOutput string         `json:"aggregated_output,omitempty"`
	ExitCode         *int           `json:"exit_code,omitempty"`
	Status           string         `json:"status,omitempty"`
}

type codexUsage struct {
	InputTokens         int `json:"input_tokens"`
	CachedInputTokens   int `json:"cached_input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens"`
}

type codexCommandState struct {
	command string
	turn    int
}

type codexParseState struct {
	currentTurn       int
	threadID          string
	finalMsg          string
	messages          transcript.Transcript
	inputTokens       int
	outputTokens      int
	commandExecutions map[string]codexCommandState
	mcpToolCalls      map[string]int
}

type codexOutputParseResult struct {
	transcript   transcript.Transcript
	threadID     string
	finalMsg     string
	inputTokens  int
	outputTokens int
}

func parseCodexOutput(ctx context.Context, output string) codexOutputParseResult {
	state := codexParseState{
		commandExecutions: make(map[string]codexCommandState),
		mcpToolCalls:      make(map[string]int),
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	const maxTokenLen = 1024 * 1024
	buf := make([]byte, maxTokenLen)
	scanner.Buffer(buf, maxTokenLen)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}

		applyCodexEventsFromLine(ctx, &state, line)
	}

	return codexOutputParseResult{
		transcript:   state.messages,
		threadID:     state.threadID,
		finalMsg:     state.finalMsg,
		inputTokens:  state.inputTokens,
		outputTokens: state.outputTokens,
	}
}

func applyCodexEventsFromLine(ctx context.Context, state *codexParseState, line string) {
	decoder := json.NewDecoder(strings.NewReader(line))
	for {
		var event codexEvent
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			logging.WarnContextf(ctx, "CodexAgent: failed to decode JSON event stream line; remaining events on the line are skipped: %v", err)
			return
		}
		applyCodexEvent(state, event)
	}
}

func applyCodexEvent(state *codexParseState, event codexEvent) {
	switch event.Type {
	case "thread.started":
		state.threadID = event.ThreadID
	case "turn.started":
		state.currentTurn++
	case "turn.completed":
		if event.Usage == nil {
			return
		}
		state.inputTokens = max(state.inputTokens, event.Usage.InputTokens+event.Usage.CachedInputTokens+event.Usage.CacheCreationTokens)
		state.outputTokens = max(state.outputTokens, event.Usage.OutputTokens)
	case "item.started":
		applyCodexItemStarted(state, event)
	case "item.completed":
		applyCodexItemCompleted(state, event)
	}
}

func applyCodexItemStarted(state *codexParseState, event codexEvent) {
	if event.Item == nil {
		return
	}
	turn := max(state.currentTurn, 1)
	switch event.Item.Type {
	case "command_execution":
		state.commandExecutions[event.Item.ID] = codexCommandState{
			command: event.Item.Command,
			turn:    turn,
		}
		state.messages = append(state.messages, transcript.Message{
			Role: transcript.RoleToolCall,
			ToolCall: &transcript.ToolCallInfo{
				ID:   event.Item.ID,
				Name: "command_execution",
				Arguments: map[string]any{
					"command": event.Item.Command,
				},
			},
			Turn: turn,
		})
	case "mcp_tool_call":
		appendCodexMCPToolCall(state, event.Item, turn)
	}
}

func applyCodexItemCompleted(state *codexParseState, event codexEvent) {
	if event.Item == nil {
		return
	}
	turn := max(state.currentTurn, 1)
	switch event.Item.Type {
	case "agent_message":
		state.finalMsg = event.Item.Text
		state.messages = append(state.messages, transcript.Message{
			Role:    transcript.RoleAssistant,
			Content: event.Item.Text,
			Turn:    turn,
		})
	case "command_execution":
		cmdState, ok := state.commandExecutions[event.Item.ID]
		if ok {
			turn = cmdState.turn
		} else {
			state.messages = append(state.messages, transcript.Message{
				Role: transcript.RoleToolCall,
				ToolCall: &transcript.ToolCallInfo{
					ID:   event.Item.ID,
					Name: "command_execution",
					Arguments: map[string]any{
						"command": event.Item.Command,
					},
				},
				Turn: turn,
			})
		}

		status := toolStatusSuccess
		if event.Item.ExitCode != nil && *event.Item.ExitCode != 0 {
			status = toolStatusError
		}

		state.messages = append(state.messages, transcript.Message{
			Role: transcript.RoleToolResult,
			ToolResult: &transcript.ToolResultInfo{
				CallID:  event.Item.ID,
				Status:  status,
				Content: event.Item.AggregatedOutput,
			},
			Turn: turn,
		})
	case "mcp_tool_call":
		if callTurn, ok := state.mcpToolCalls[event.Item.ID]; ok {
			turn = callTurn
		} else {
			appendCodexMCPToolCall(state, event.Item, turn)
		}
		state.messages = append(state.messages, transcript.Message{
			Role: transcript.RoleToolResult,
			ToolResult: &transcript.ToolResultInfo{
				CallID:  event.Item.ID,
				Status:  codexMCPToolResultStatus(event.Item.Status),
				Content: codexMCPToolResultContent(event.Item.Result),
			},
			Turn: turn,
		})
	}
}

func appendCodexMCPToolCall(state *codexParseState, item *codexItem, turn int) {
	if item.ID != "" {
		state.mcpToolCalls[item.ID] = turn
	}
	state.messages = append(state.messages, transcript.Message{
		Role: transcript.RoleToolCall,
		ToolCall: &transcript.ToolCallInfo{
			ID:        item.ID,
			Name:      codexMCPToolName(item.Server, item.Tool),
			Arguments: item.Arguments,
		},
		Turn: turn,
	})
}

func codexMCPToolName(server, tool string) string {
	if server == "" {
		return tool
	}
	return "mcp__" + server + "__" + tool
}

func codexMCPToolResultContent(result any) string {
	if result == nil {
		return ""
	}
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprint(result)
	}
	return string(data)
}

func findCodexSessionPath(ctx context.Context, rt Runtime, threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}

	script := `home=$(printenv HOME); [ -n "$home" ] || exit 0
root="$home/.codex/sessions"; [ -d "$root" ] || exit 0
find "$root" -type f -name "*$SKILL_UP_CODEX_THREAD_ID*.jsonl" 2>/dev/null | head -1`
	result, err := rt.Exec(ctx, script, ExecOptions{
		Env: map[string]string{"SKILL_UP_CODEX_THREAD_ID": threadID},
	})
	if err != nil || result.ExitCode != 0 {
		return ""
	}

	sessionPath := strings.TrimSpace(result.Stdout)
	if sessionPath == "" || !strings.HasSuffix(sessionPath, ".jsonl") {
		return ""
	}

	return sessionPath
}

type codexSessionEvent struct {
	Type      string               `json:"type"`
	Payload   *codexSessionPayload `json:"payload,omitempty"`
	Timestamp string               `json:"timestamp,omitempty"`
}

type codexSessionPayload struct {
	Type      string                 `json:"type,omitempty"`
	Role      string                 `json:"role,omitempty"`
	Content   []codexSessionContent  `json:"content,omitempty"`
	Message   string                 `json:"message,omitempty"`
	Phase     string                 `json:"phase,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Arguments string                 `json:"arguments,omitempty"`
	CallID    string                 `json:"call_id,omitempty"`
	Output    string                 `json:"output,omitempty"`
	Info      *codexSessionTokenInfo `json:"info,omitempty"`
}

type codexSessionContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type codexSessionTokenInfo struct {
	TotalTokenUsage *codexUsage `json:"total_token_usage,omitempty"`
}

type codexSessionParseResult struct {
	transcript   transcript.Transcript
	finalMsg     string
	inputTokens  int
	outputTokens int
}

func parseCodexSessionFile(sessionFile string) codexSessionParseResult {
	file, err := os.Open(sessionFile)
	if err != nil {
		return codexSessionParseResult{}
	}
	defer file.Close() //nolint:errcheck

	var messages transcript.Transcript
	var finalMsg string
	var inputTokens, outputTokens int
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

		var event codexSessionEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Payload == nil {
			continue
		}

		switch event.Type {
		case "response_item":
			turnMsgs, final, turn := parseCodexSessionResponseItem(*event.Payload, currentTurn)
			if turnMsgs != nil {
				messages = append(messages, turnMsgs...)
			}
			if final != "" {
				finalMsg = final
			}
			currentTurn = turn
		case "event_msg":
			msgs, final, in, out := applyCodexSessionEventMsg(*event.Payload, currentTurn)
			if msgs != nil {
				messages = append(messages, msgs...)
			}
			if final != "" {
				finalMsg = final
			}
			inputTokens = max(inputTokens, in)
			outputTokens = max(outputTokens, out)
		}
	}

	if finalMsg == "" {
		finalMsg = messages.FinalAssistantMessage()
	}

	return codexSessionParseResult{
		transcript:   messages,
		finalMsg:     finalMsg,
		inputTokens:  inputTokens,
		outputTokens: outputTokens,
	}
}

func parseCodexSessionResponseItem(
	payload codexSessionPayload,
	currentTurn int,
) ([]transcript.Message, string, int) {
	switch payload.Type {
	case "message":
		content := codexSessionContentText(payload.Content)
		if content == "" {
			return nil, "", currentTurn
		}

		role := transcript.RoleAssistant
		if payload.Role == codexUserRole {
			role = transcript.RoleUser
			currentTurn++
		}
		if currentTurn == 0 {
			currentTurn = 1
		}

		msg := transcript.Message{
			Role:    role,
			Content: content,
			Turn:    currentTurn,
		}
		if role == transcript.RoleAssistant {
			return []transcript.Message{msg}, content, currentTurn
		}
		return []transcript.Message{msg}, "", currentTurn
	case codexFnCall:
		return []transcript.Message{{
			Role: transcript.RoleToolCall,
			ToolCall: &transcript.ToolCallInfo{
				ID:   payload.CallID,
				Name: payload.Name,
				Arguments: map[string]any{
					"arguments": payload.Arguments,
				},
			},
			Turn: currentTurn,
		}}, "", currentTurn
	case codexFnCallOut:
		status := codexToolResultStatus(payload.Output)
		return []transcript.Message{{
			Role: transcript.RoleToolResult,
			ToolResult: &transcript.ToolResultInfo{
				CallID:  payload.CallID,
				Status:  status,
				Content: payload.Output,
			},
			Turn: currentTurn,
		}}, "", currentTurn
	default:
		return nil, "", currentTurn
	}
}

// applyCodexSessionEventMsg projects a session-file "event_msg" payload into
// transcript messages and token deltas. Extracted from parseCodexSessionFile to
// keep the per-line dispatch loop within gocyclo budget.
func applyCodexSessionEventMsg(payload codexSessionPayload, currentTurn int) (msgs []transcript.Message, finalMsg string, inputTokens, outputTokens int) { //nolint:revive,nonamedreturns // named returns required by revive confusing-results
	switch payload.Type {
	case codexAgentMsg:
		msgs = []transcript.Message{{
			Role:    transcript.RoleAssistant,
			Content: payload.Message,
			Turn:    currentTurn,
		}}
		finalMsg = payload.Message
	case codexTokenCount:
		if payload.Info != nil && payload.Info.TotalTokenUsage != nil {
			inputTokens = codexUsageInputTotal(*payload.Info.TotalTokenUsage)
			outputTokens = payload.Info.TotalTokenUsage.OutputTokens
		}
	}
	return msgs, finalMsg, inputTokens, outputTokens
}

func codexUsageInputTotal(u codexUsage) int {
	return u.InputTokens + u.CachedInputTokens + u.CacheCreationTokens
}

func codexToolResultStatus(output string) string {
	const marker = "Process exited with code "

	idx := strings.LastIndex(output, marker)
	if idx < 0 {
		return toolStatusSuccess
	}

	codeText := strings.TrimSpace(output[idx+len(marker):])
	if codeText == "" {
		return toolStatusSuccess
	}

	for i, ch := range codeText {
		if ch < '0' || ch > '9' {
			codeText = codeText[:i]
			break
		}
	}
	if codeText == "" || codeText == "0" {
		return toolStatusSuccess
	}

	return toolStatusError
}

func codexMCPToolResultStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "completed", "success", "succeeded":
		return toolStatusSuccess
	default:
		return toolStatusError
	}
}

func codexSessionContentText(content []codexSessionContent) string {
	if len(content) == 0 {
		return ""
	}

	parts := make([]string, 0, len(content))
	for _, item := range content {
		if (item.Type == codexInputText || item.Type == codexOutputText) && item.Text != "" {
			parts = append(parts, item.Text)
		}
	}

	return strings.Join(parts, "\n\n")
}

func codexTurns(trans transcript.Transcript) int {
	maxTurn := 0
	for _, msg := range trans {
		if msg.Turn > maxTurn {
			maxTurn = msg.Turn
		}
	}

	return maxTurn
}
