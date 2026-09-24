package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/agentkind"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/observability"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// OpenCodeAgent runs OpenCode inside the selected runtime.
type OpenCodeAgent struct{ CLIAgent }

const openCodePackage = "opencode-ai"

// NewOpenCodeAgent creates an OpenCode CLI adapter.
func NewOpenCodeAgent(cfg Config) *OpenCodeAgent {
	if cfg.Name == "" {
		cfg.Name = agentkind.OpenCode
	}
	if cfg.CheckCmd == "" {
		cfg.CheckCmd = "command -v opencode"
	}
	if cfg.VersionCmd == "" {
		cfg.VersionCmd = "opencode --version"
	}
	if cfg.SkillPath == "" {
		cfg.SkillPath = ".opencode/skills"
	}
	return &OpenCodeAgent{CLIAgent: CLIAgent{BaseAgent: NewBaseAgent(cfg)}}
}

// Install installs OpenCode in an isolated runtime when necessary.
func (a *OpenCodeAgent) Install(ctx context.Context, rt Runtime) error {
	a.probeAndMergePATH(ctx, rt, `printf '%s' "$HOME/.local/bin:$HOME/.nvm/current/bin:$HOME/.opencode/bin:$PATH"`)
	cmd := a.Cfg.InstallCmd
	if cmd == "" {
		cmd = defaultOpenCodeInstallCmdForVersion(a.Cfg.Version)
	}
	result, err := rt.Exec(ctx, cmd, a.mergeExecOptionsEnv(ctx, ExecOptions{Cwd: "/"}, nil, nil))
	if err != nil {
		return fmt.Errorf("install failed: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("install failed: %w: %s", ErrAgentInstallFailed, result.Stderr)
	}
	return nil
}

func defaultOpenCodeInstallCmdForVersion(version string) string {
	lines := []string{"set -e"}
	if guard := installedVersionGuard("opencode", version); guard != "" {
		lines = append(lines, guard)
	} else {
		lines = append(lines, "if command -v opencode >/dev/null 2>&1; then exit 0; fi")
	}
	lines = append(lines, nodeBootstrapLines(agentNodeDefaultVersion)...)
	lines = append(lines, "npm install -g "+shellQuote(versionedPackage(openCodePackage, version)))
	return strings.Join(lines, "\n")
}

// CheckCredentials leaves local OpenCode login available when no key is supplied.
func (a *OpenCodeAgent) CheckCredentials(ctx context.Context) error {
	if a.Cfg.ModelProvider == agentProviderAnthropic || a.Cfg.Protocol == string(credential.ProtocolAnthropic) {
		a.logCredentialStatus(ctx, credential.EnvAnthropicAPIKey, credential.EnvAnthropicBaseURL,
			"ANTHROPIC_API_KEY not set, opencode will rely on existing login state if available")
	} else {
		a.logCredentialStatus(ctx, credential.EnvOpenAIAPIKey, credential.EnvOpenAIBaseURL,
			"OPENAI_API_KEY not set, opencode will rely on existing login state if available")
	}
	return nil
}

// InstallMCP passes a per-runtime MCP configuration to OpenCode without writing
// to the caller's project configuration. OpenCode merges this inline config.
func (a *OpenCodeAgent) InstallMCP(_ context.Context, rt Runtime, mcpCfg runtime.MCPConfig) error {
	servers := make(map[string]any, len(mcpCfg.Servers))
	env := make(map[string]string)
	for i, server := range mcpCfg.Servers {
		entry, serverEnv, err := openCodeMCPServer(server, i)
		if err != nil {
			return err
		}
		servers[server.Name] = entry
		maps.Copy(env, serverEnv)
	}
	configValue := make(map[string]any)
	if existing := os.Getenv("OPENCODE_CONFIG_CONTENT"); existing != "" {
		if err := json.Unmarshal([]byte(existing), &configValue); err != nil {
			return fmt.Errorf("parse OPENCODE_CONFIG_CONTENT: %w", err)
		}
		if configValue == nil {
			configValue = make(map[string]any)
		}
	}
	if len(servers) > 0 {
		if existing, ok := configValue["mcp"].(map[string]any); ok {
			maps.Copy(existing, servers)
			configValue["mcp"] = existing
		} else {
			configValue["mcp"] = servers
		}
	}
	if a.Cfg.BaseURL != "" {
		provider, entry, providerEnv := a.providerConfig()
		providers, _ := configValue["provider"].(map[string]any)
		if providers == nil {
			providers = make(map[string]any)
		}
		providers[provider] = entry
		configValue["provider"] = providers
		maps.Copy(env, providerEnv)
	}
	if len(configValue) == 0 {
		return nil
	}
	config, err := json.Marshal(configValue)
	if err != nil {
		return fmt.Errorf("encode opencode MCP config: %w", err)
	}
	env["OPENCODE_CONFIG_CONTENT"] = string(config)
	rt.MergeEnv(env)
	return nil
}

func openCodeMCPServer(server runtime.MCPServerConfig, index int) (map[string]any, map[string]string, error) {
	if server.Name == "" {
		return nil, nil, errors.New("opencode MCP server name is required")
	}
	env := make(map[string]string, len(server.Env))
	refs := make(map[string]string, len(server.Env))
	for key, value := range server.Env {
		if !shellEnvNamePattern.MatchString(key) {
			return nil, nil, fmt.Errorf("mcp server %q env %q is invalid", server.Name, key)
		}
		ref := "SKILL_UP_OPENCODE_MCP_" + strconv.Itoa(index) + "_" + key
		refs[key] = ref
		env[ref] = value
	}
	switch server.Transport {
	case mcpTransportStdio:
		if server.Command == "" {
			return nil, nil, fmt.Errorf("mcp server %q stdio transport requires command", server.Name)
		}
		serverEnv := make(map[string]string, len(refs))
		for key, ref := range refs {
			serverEnv[key] = "{env:" + ref + "}"
		}
		return map[string]any{"type": "local", "command": append([]string{server.Command}, server.Args...), "environment": serverEnv}, env, nil
	case "", mcpTransportHTTP:
		if server.Endpoint == "" {
			return nil, nil, fmt.Errorf("mcp server %q http transport requires endpoint", server.Name)
		}
		headers := maps.Clone(server.Headers)
		for key, value := range headers {
			headers[key] = openCodeEnvRefs(value, refs)
		}
		for key, value := range server.HeaderEnv {
			if !shellEnvNamePattern.MatchString(value) {
				return nil, nil, fmt.Errorf("mcp server %q header environment %q is invalid", server.Name, value)
			}
			ref := refs[value]
			if ref == "" {
				ref = value
			}
			headers[key] = "{env:" + ref + "}"
		}
		return map[string]any{"type": "remote", "url": openCodeEnvRefs(server.Endpoint, refs), "headers": headers}, env, nil
	default:
		return nil, nil, fmt.Errorf("mcp server %q transport %q is not supported by opencode", server.Name, server.Transport)
	}
}

func (a *OpenCodeAgent) providerConfig() (string, map[string]any, map[string]string) {
	provider := a.Cfg.ModelProvider
	if provider == "" {
		provider = agentProviderOpenAI
	}
	opts := map[string]any{"baseURL": a.Cfg.BaseURL}
	entry := map[string]any{"options": opts}
	env := make(map[string]string)
	if provider == agentProviderOpenAI || provider == agentProviderAnthropic {
		return provider, entry, env
	}
	if a.Cfg.Protocol == string(credential.ProtocolAnthropic) {
		entry["npm"] = "@ai-sdk/anthropic"
	} else {
		entry["npm"] = "@ai-sdk/openai-compatible"
	}
	if a.Cfg.ModelName != "" {
		entry["models"] = map[string]any{a.Cfg.ModelName: map[string]any{}}
	}
	if a.Cfg.APIKey != "" {
		opts["apiKey"] = "{env:SKILL_UP_OPENCODE_API_KEY}"
		env["SKILL_UP_OPENCODE_API_KEY"] = a.Cfg.APIKey
	}
	return provider, entry, env
}

func openCodeEnvRefs(value string, refs map[string]string) string {
	return shellEnvRefPattern.ReplaceAllStringFunc(value, func(match string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}")
		if ref := refs[name]; ref != "" {
			name = ref
		}
		return "{env:" + name + "}"
	})
}

// Run executes a batch prompt in OpenCode.
func (a *OpenCodeAgent) Run(ctx context.Context, rt Runtime, opts ExecOptions, messages []transcript.Message) (*SessionResult, error) {
	return a.execute(ctx, rt, opts, BuildInstructionFromMessages(messages), "")
}

// RunTurn resumes the exact OpenCode session returned by the previous turn.
func (a *OpenCodeAgent) RunTurn(ctx context.Context, rt Runtime, opts ExecOptions, message transcript.Message, sessionID string) (*SessionResult, error) {
	return a.execute(ctx, rt, opts, message.Content, sessionID)
}

// RequiresSessionIDForNextTurn prevents a missing ID from resuming an unrelated session.
func (*OpenCodeAgent) RequiresSessionIDForNextTurn() bool { return true }

// ReturnsIncrementalSessionResults reports that run --session emits this turn only.
func (*OpenCodeAgent) ReturnsIncrementalSessionResults() bool { return true }

func (a *OpenCodeAgent) execute(ctx context.Context, rt Runtime, opts ExecOptions, instruction, sessionID string) (finalResult *SessionResult, finalErr error) {
	defer func() { a.annotateSessionResult(finalResult) }()
	if err := requireBashTargetShell(rt); err != nil {
		return nil, fmt.Errorf("%s: %w", a.Name(), err)
	}
	start := time.Now()
	env := a.credentialEnvVars("", "")
	if a.Cfg.APIKey != "" {
		if a.Cfg.ModelProvider == agentProviderAnthropic || a.Cfg.Protocol == string(credential.ProtocolAnthropic) {
			env[credential.EnvAnthropicAPIKey] = a.Cfg.APIKey
		} else {
			env[credential.EnvOpenAIAPIKey] = a.Cfg.APIKey
		}
	}
	opts = a.mergeExecOptionsEnv(ctx, opts, env, a.buildAgentObservabilityAttrs(nil))
	ctx = observability.ContextWithConfiguredAgentSpanAttributes(ctx, opts.Env)
	if err := ensureNodeRuntime(ctx, rt, "opencode", opts); err != nil {
		return &SessionResult{Engine: a.Name(), ExitCode: 1, Artifacts: &SessionArtifacts{}}, err
	}
	cmd, delivery, err := deliverPrompt(ctx, rt, opts, instruction, promptCommandBuilder{
		Inline: func(prompt string) string { return buildOpenCodeRunCmd(prompt, a.model(), sessionID) },
		StdinFile: func(path string) string {
			return buildOpenCodeRunCmd("", a.model(), sessionID) + " -- \"$(cat " + shellQuote(path) + ")\""
		},
	})
	if err != nil {
		return &SessionResult{Engine: a.Name(), ExitCode: 1, Artifacts: &SessionArtifacts{}}, err
	}
	result, execErr := rt.Exec(ctx, cmd, opts)
	parsed, eventError := parseOpenCodeEvents(result.Stdout, instruction)
	parsed.Engine = a.Name()
	parsed.ExitCode = result.ExitCode
	parsed.DurationMs = time.Since(start).Milliseconds()
	parsed.Stderr = result.Stderr
	parsed.PromptDelivery = delivery
	parsed.Artifacts = &SessionArtifacts{}
	cleanupCtx, cleanupCancel := sessionCleanupContext(ctx)
	defer cleanupCancel()
	if artifact, err := persistSessionArtifact(cleanupCtx, rt, opts.ArtifactDir, "stdout.jsonl", result.Stdout); err == nil {
		if opts.ArtifactDir == "" {
			parsed.Artifacts.GeneratedFiles = append(parsed.Artifacts.GeneratedFiles, artifact)
		}
	}
	if execErr != nil {
		if parsed.ExitCode == 0 {
			parsed.ExitCode = 1
		}
		return parsed, fmt.Errorf("opencode run failed: %w", execErr)
	}
	if result.ExitCode != 0 {
		return parsed, fmt.Errorf("opencode run failed (exit %d): %s", result.ExitCode, result.Stderr)
	}
	if eventError != "" {
		parsed.ExitCode = 1
		return parsed, fmt.Errorf("opencode run failed: %s", eventError)
	}
	if parsed.FinalMessage == "" {
		parsed.ExitCode = 1
		return parsed, errors.New("opencode run returned no assistant text")
	}
	return parsed, nil
}

func (a *OpenCodeAgent) model() string {
	model := strings.TrimSpace(a.Cfg.ModelName)
	if model == "" || strings.Contains(model, "/") {
		return model
	}
	if provider := strings.TrimSpace(a.Cfg.ModelProvider); provider != "" {
		return provider + "/" + model
	}
	if a.Cfg.BaseURL != "" {
		return agentProviderOpenAI + "/" + model
	}
	return model
}

func buildOpenCodeRunCmd(instruction, model, sessionID string) string {
	cmd := "opencode run --format json --dangerously-skip-permissions"
	if model != "" {
		cmd += " --model " + shellQuote(model)
	}
	if sessionID != "" {
		cmd += " --session " + shellQuote(sessionID)
	}
	if instruction != "" {
		cmd += " -- " + shellQuote(instruction)
	}
	return cmd
}

type openCodeEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Error     struct {
		Name string `json:"name"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	} `json:"error"`
	Part struct {
		Type   string `json:"type"`
		Text   string `json:"text"`
		Tool   string `json:"tool"`
		CallID string `json:"callID"`
		State  struct {
			Status string         `json:"status"`
			Input  map[string]any `json:"input"`
			Output string         `json:"output"`
			Error  string         `json:"error"`
		} `json:"state"`
		Tokens struct {
			Input  int `json:"input"`
			Output int `json:"output"`
		} `json:"tokens"`
	} `json:"part"`
}

func parseOpenCodeEvents(output, instruction string) (*SessionResult, string) {
	res := &SessionResult{Transcript: transcript.Transcript{{Role: transcript.RoleUser, Content: instruction, Turn: 1}}}
	var textParts []string
	var eventError string
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var event openCodeEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event.SessionID != "" {
			res.SessionID = event.SessionID
		}
		switch event.Type {
		case toolStatusError:
			eventError = event.Error.Data.Message
			if eventError == "" {
				eventError = event.Error.Name
			}
			res.Transcript = append(res.Transcript, transcript.Message{Role: transcript.RoleError, Content: eventError, Turn: 1})
		case customResponseText:
			if event.Part.Text != "" {
				textParts = append(textParts, event.Part.Text)
				res.Transcript = append(res.Transcript, transcript.Message{Role: transcript.RoleAssistant, Content: event.Part.Text, Turn: 1})
			}
		case "tool_use":
			res.Transcript = append(res.Transcript, transcript.Message{
				Role: transcript.RoleToolCall, Turn: 1,
				ToolCall: &transcript.ToolCallInfo{ID: event.Part.CallID, Name: event.Part.Tool, Arguments: event.Part.State.Input},
			})
			if event.Part.State.Status == "completed" || event.Part.State.Status == toolStatusError {
				status := toolStatusSuccess
				content := event.Part.State.Output
				if event.Part.State.Status == toolStatusError {
					status = toolStatusError
					content = event.Part.State.Error
				}
				res.Transcript = append(res.Transcript, transcript.Message{
					Role: transcript.RoleToolResult, Turn: 1,
					ToolResult: &transcript.ToolResultInfo{CallID: event.Part.CallID, Status: status, Content: content},
				})
			}
		case "step_finish":
			res.InputTokens += event.Part.Tokens.Input
			res.OutputTokens += event.Part.Tokens.Output
		}
	}
	res.FinalMessage = strings.Join(textParts, "\n")
	if res.FinalMessage != "" {
		res.Turns = 1
	}
	return res, eventError
}

var (
	_ StrictSessionResumer      = (*OpenCodeAgent)(nil)
	_ IncrementalSessionResumer = (*OpenCodeAgent)(nil)
)
