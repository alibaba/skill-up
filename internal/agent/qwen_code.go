package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/observability"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// QwenCodeAgent implements Agent for the Qwen Code CLI (@qwen-code/qwen-code).
//
// Qwen Code is an open-source terminal coding agent optimized for Qwen models
// (https://github.com/QwenLM/qwen-code). It is a Gemini CLI fork and talks to
// any OpenAI-compatible endpoint via the OPENAI_API_KEY / OPENAI_BASE_URL /
// OPENAI_MODEL environment variables, so its credential plumbing mirrors codex.
type QwenCodeAgent struct {
	CLIAgent
}

const (
	qwenCodeEngineName = "qwen_code"
	qwenCodePackage    = "@qwen-code/qwen-code"
	// qwenCodeBinary is the executable name installed by the npm package.
	qwenCodeBinary = "qwen"
	// Session-line type discriminators in qwen's chat JSONL.
	qwenTypeUser      = "user"
	qwenTypeAssistant = "assistant"
)

// qwenCodeExecPathProbeCmd resolves both $HOME/.local/bin (where `npm install
// -g` puts the qwen binary via the bootstrap's npm_config_prefix) and
// $HOME/.nvm/current/bin (where the node interpreter lives — qwen's
// `#!/usr/bin/env node` shebang needs to find node at exec time).
const qwenCodeExecPathProbeCmd = `printf '%s' "$HOME/.local/bin:$HOME/.nvm/current/bin:$PATH"`

// NewQwenCodeAgent creates a new QwenCodeAgent.
func NewQwenCodeAgent(cfg Config) *QwenCodeAgent {
	if cfg.Name == "" {
		cfg.Name = qwenCodeEngineName
	}
	if cfg.CheckCmd == "" {
		cfg.CheckCmd = "command -v qwen"
	}
	if cfg.VersionCmd == "" {
		cfg.VersionCmd = "qwen --version"
	}
	if cfg.SkillPath == "" {
		cfg.SkillPath = ".qwen/skills"
	}

	return &QwenCodeAgent{
		CLIAgent: CLIAgent{BaseAgent: NewBaseAgent(cfg)},
	}
}

// Install installs Qwen Code when it is not already available in the runtime.
//
//nolint:dupl // each agent Install shares the same probe→merge→exec lifecycle; the deltas (probe const, default install cmd) are pulled out, leaving the orchestration intentionally similar.
func (a *QwenCodeAgent) Install(ctx context.Context, rt Runtime) error {
	a.probeAndMergePATH(ctx, rt, qwenCodeExecPathProbeCmd)

	opts := ExecOptions{Cwd: "/"}
	opts = a.mergeExecOptionsEnv(ctx, opts, nil, nil)

	installCmd := a.Cfg.InstallCmd
	if installCmd == "" {
		installCmd = defaultQwenCodeInstallCmdForVersion(a.Cfg.Version)
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

// InstallMCP installs MCP servers with the Qwen Code CLI. Qwen Code inherits
// Gemini CLI's `qwen mcp add` command, which shares the claude-compatible
// surface (`--scope`, `-e`, `--transport http`, `--header`).
func (a *QwenCodeAgent) InstallMCP(ctx context.Context, rt Runtime, mcpCfg runtime.MCPConfig) error {
	if len(mcpCfg.Servers) == 0 {
		return nil
	}
	if err := ensureNodeRuntime(ctx, rt, qwenCodeBinary, ExecOptions{}); err != nil {
		return err
	}
	return installMCPServers(ctx, rt, mcpCfg, buildQwenCodeMCPInstallCmd)
}

func buildQwenCodeMCPInstallCmd(server runtime.MCPServerConfig) (string, error) {
	return buildClaudeCompatibleMCPInstallCmd(qwenCodeBinary, qwenCodeEngineName, server)
}

func defaultQwenCodeInstallCmd() string {
	return defaultQwenCodeInstallCmdForVersion("")
}

func defaultQwenCodeInstallCmdForVersion(version string) string {
	lines := []string{
		"set -e",
	}
	if guard := installedVersionGuard(qwenCodeBinary, version); guard != "" {
		lines = append(lines, guard)
	} else {
		lines = append(lines, "if command -v qwen >/dev/null 2>&1; then exit 0; fi")
	}
	lines = append(lines, nodeBootstrapLines(agentNodeDefaultVersion)...)
	lines = append(lines, "npm install -g "+shellQuote(versionedPackage(qwenCodePackage, version)))
	return strings.Join(lines, "\n")
}

// CheckCredentials reports the OpenAI-compatible env configuration used by
// Qwen Code. A missing key is informational only — Qwen Code can also rely on
// local login state under ~/.qwen/ (e.g. Qwen OAuth).
func (a *QwenCodeAgent) CheckCredentials(ctx context.Context) error {
	a.logCredentialStatus(
		ctx,
		credential.EnvOpenAIAPIKey,
		credential.EnvOpenAIBaseURL,
		"OPENAI_API_KEY not set, qwen_code will rely on existing login state if available",
	)
	return nil
}

// Run executes qwen non-interactively (instruction piped to `qwen --yolo`) and
// builds a transcript from the session file (falling back to stdout).
func (a *QwenCodeAgent) Run(ctx context.Context, rt Runtime, opts ExecOptions, messages []transcript.Message) (finalResult *SessionResult, finalErr error) {
	defer func() { a.annotateSessionResult(finalResult) }()
	if err := requireBashTargetShell(rt); err != nil {
		return nil, fmt.Errorf("%s: %w", a.Name(), err)
	}
	start := time.Now()

	instruction := BuildInstructionFromMessages(messages)
	model := a.appliedModelName(ctx)

	envVars := a.credentialEnvVars(credential.EnvOpenAIAPIKey, credential.EnvOpenAIBaseURL)
	// Qwen Code resolves the active model from OPENAI_MODEL when --model is not
	// supplied; mirroring the flag into the env keeps env-only configurations
	// working and matches the OpenAI-compatible provider contract.
	if model != "" {
		envVars[credential.EnvOpenAIModel] = model
	}
	// --yolo auto-approves tool calls but does NOT sandbox: like claude_code
	// (--permission-mode=bypassPermissions) and qodercli, qwen_code relies on
	// the runtime for isolation rather than imposing its own sandbox (qwen's
	// `-s` needs docker/podman on Linux and is unreliable elsewhere). So the
	// "running headless without a sandbox" notice is silenced only when the
	// runtime already isolates execution (docker/opensandbox). On the none
	// runtime (host execution) it is left to surface — it is the user's signal
	// that tools run at host privilege; for untrusted skills, use a sandboxed
	// runtime. See docs/guide/writing-evals.md (engine kwargs / qwen_code).
	if !rt.RequiresProcessSandbox() {
		envVars["QWEN_CODE_SUPPRESS_YOLO_WARNING"] = "1"
	}
	opts = a.mergeExecOptionsEnv(ctx, opts, envVars, a.buildAgentObservabilityAttrs(nil))
	ctx = observability.ContextWithConfiguredAgentSpanAttributes(ctx, opts.Env)

	if err := ensureNodeRuntime(ctx, rt, qwenCodeBinary, opts); err != nil {
		return &SessionResult{
			Engine:     a.Name(),
			ExitCode:   1,
			DurationMs: time.Since(start).Milliseconds(),
			Artifacts:  &SessionArtifacts{},
		}, err
	}

	cmd, promptDelivery, err := deliverPrompt(ctx, rt, opts, instruction, promptCommandBuilder{
		Inline: func(prompt string) string {
			return buildQwenCodeRunCmd(prompt, model)
		},
		StdinFile: func(path string) string {
			return buildQwenCodeRunStdinCmd(path, model)
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
	if err != nil {
		return sessionResult, fmt.Errorf("qwen_code run failed: %w", err)
	}
	if result.ExitCode != 0 {
		return sessionResult, fmt.Errorf("qwen_code run failed (exit %d): %s", result.ExitCode, result.Stderr)
	}

	return sessionResult, nil
}

func (a *QwenCodeAgent) appliedModelName(_ context.Context) string {
	return strings.TrimSpace(a.Cfg.ModelName)
}

func buildQwenCodeRunCmd(instruction, model string) string {
	// Feed the instruction on stdin rather than as an argument. qwen reads a
	// piped prompt in non-interactive mode and prints the final answer to
	// stdout; doing it this way (a) avoids the deprecated -p/--prompt flag,
	// which upstream warns "will be removed in a future version", and (b) is
	// immune to an instruction that begins with "-" being mis-parsed as a flag
	// (the positional-prompt form is not). --yolo auto-approves every tool
	// action so the run never blocks on a confirmation prompt.
	cmd := "printf '%s' " + shellQuote(instruction) + " | qwen --yolo"
	if model != "" {
		cmd += " -m " + shellQuote(model)
	}
	return cmd
}

func buildQwenCodeRunStdinCmd(promptPath, model string) string {
	cmd := "cat " + shellQuote(promptPath) + " | qwen --yolo"
	if model != "" {
		cmd += " -m " + shellQuote(model)
	}
	return cmd
}

// buildSessionResult prefers qwen's own session JSONL (full transcript, tool
// calls and usageMetadata token counts) when it can be located and downloaded,
// and falls back to a minimal stdout-derived transcript otherwise.
func (a *QwenCodeAgent) buildSessionResult(ctx context.Context, rt Runtime, opts ExecOptions, instruction string, start time.Time, result ExecResult) *SessionResult {
	var trans transcript.Transcript
	var finalMsg string
	var inputTokens, outputTokens int

	cleanupCtx, cleanupCancel := sessionCleanupContext(ctx)
	defer cleanupCancel()

	generatedFiles := []string{}
	if artifactPath, err := persistSessionArtifact(cleanupCtx, rt, opts.ArtifactDir, "stdout.txt", result.Stdout); err == nil {
		if opts.ArtifactDir == "" {
			generatedFiles = append(generatedFiles, artifactPath)
		}
	}

	var cleanupSession func()
	generatedFiles, cleanupSession = withDownloadedSession(
		cleanupCtx, rt, opts.ArtifactDir, findQwenCodeSessionFile(cleanupCtx, rt), generatedFiles,
		func(artifactPath string) {
			t, f, inTok, outTok := parseQwenSessionFile(artifactPath)
			if len(t) > 0 {
				trans = t
				finalMsg = f
			}
			inputTokens, outputTokens = inTok, outTok
		},
	)
	defer cleanupSession()

	// Fallback: build a minimal transcript from stdout when no session file was
	// found (e.g. qwen session logging disabled, or a sandboxed run whose file
	// could not be downloaded).
	if trans == nil {
		if final := strings.TrimSpace(result.Stdout); final != "" {
			trans = transcript.Transcript{
				{Role: transcript.RoleUser, Content: instruction, Turn: 1},
				{Role: transcript.RoleAssistant, Content: final, Turn: 1},
			}
			finalMsg = final
		}
	}
	if finalMsg == "" {
		finalMsg = trans.FinalAssistantMessage()
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

// findQwenCodeSessionFile resolves the newest chat JSONL under qwen's projects
// tree (~/.qwen/projects/<workspace-key>/chats/) for this workspace, using the
// shared project-tree lookup (HOME + tree are read only inside the runtime).
func findQwenCodeSessionFile(ctx context.Context, rt Runtime) string {
	return findAgentSessionJSONL(ctx, rt, agentSessionLookup{
		projectsRootTmpl: "$home/.qwen/projects",
		// qwen keeps chats one level below the workspace directory:
		// <workspace-key>/chats/<session>.jsonl.
		// qwen chat events carry no working directory, so the directory match is
		// the only signal available for this format.
		sessionDepth: 2,
	})
}

// qwenSessionEvent is one line of qwen's chat JSONL. qwen is a Gemini CLI fork,
// so messages use the Gemini shape: role ∈ {user, model} and a `parts` array
// whose entries carry text, functionCall or functionResponse.
type qwenSessionEvent struct {
	Type          string              `json:"type"` // user | assistant | system
	Model         string              `json:"model,omitempty"`
	Message       *qwenSessionMessage `json:"message,omitempty"`
	UsageMetadata *qwenUsageMetadata  `json:"usageMetadata,omitempty"`
}

type qwenSessionMessage struct {
	Role  string            `json:"role"`
	Parts []qwenSessionPart `json:"parts"`
}

type qwenSessionPart struct {
	Text             string                `json:"text,omitempty"`
	Thought          bool                  `json:"thought,omitempty"`
	FunctionCall     *qwenFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *qwenFunctionResponse `json:"functionResponse,omitempty"`
}

type qwenFunctionCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type qwenFunctionResponse struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Response any    `json:"response,omitempty"`
}

// qwenUsageMetadata mirrors Gemini's usageMetadata. totalTokenCount ==
// promptTokenCount + candidatesTokenCount (+ thoughtsTokenCount); cachedContent
// is a subset of prompt, so it is NOT added again to avoid double counting.
type qwenUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
}

// parseQwenSessionFile reads qwen's chat JSONL and returns the transcript, the
// final assistant message, and high-water token counts. Token semantics follow
// the other agents: max input (promptTokenCount) and max output
// (candidatesTokenCount + thoughtsTokenCount) across assistant lines.
func parseQwenSessionFile(sf string) (trans transcript.Transcript, finalMsg string, inputTokens, outputTokens int) { //nolint:nonamedreturns // named returns document the four positional results
	file, err := os.Open(sf)
	if err != nil {
		return nil, "", 0, 0
	}
	defer file.Close() //nolint:errcheck

	var messages []transcript.Message
	turn := 0

	scanner := bufio.NewScanner(file)
	const maxTokenLen = 1024 * 1024
	scanner.Buffer(make([]byte, maxTokenLen), maxTokenLen)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line == "[]" || line == "{}" {
			continue
		}
		var ev qwenSessionEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == qwenTypeUser {
			turn++
		}
		if ev.Message == nil {
			continue
		}
		if final := appendQwenMessageParts(&messages, ev, max(turn, 1)); final != "" {
			finalMsg = final
		}
		if ev.Type == qwenTypeAssistant && ev.UsageMetadata != nil {
			inputTokens = max(inputTokens, ev.UsageMetadata.PromptTokenCount)
			outputTokens = max(outputTokens, ev.UsageMetadata.CandidatesTokenCount+ev.UsageMetadata.ThoughtsTokenCount)
		}
	}

	if finalMsg == "" {
		finalMsg = transcript.Transcript(messages).FinalAssistantMessage()
	}
	return messages, finalMsg, inputTokens, outputTokens
}

// appendQwenMessageParts projects one session line's parts into transcript
// messages (text → user/assistant, functionCall → tool_call, functionResponse
// → tool_result) and returns the assistant text when the line is an assistant
// turn, so the caller can track the final message.
func appendQwenMessageParts(messages *[]transcript.Message, ev qwenSessionEvent, turn int) string {
	role := transcript.RoleAssistant
	if ev.Type == qwenTypeUser {
		role = transcript.RoleUser
	}

	var textParts []string
	for _, p := range ev.Message.Parts {
		switch {
		case p.Thought:
			// Qwen records hidden reasoning as ordinary text parts annotated with
			// thought=true. Keep those parts out of the user-visible transcript and
			// FinalMessage while preserving tool calls and the final answer.
			continue
		case p.FunctionCall != nil:
			*messages = append(*messages, transcript.Message{
				Role: transcript.RoleToolCall,
				ToolCall: &transcript.ToolCallInfo{
					ID:        p.FunctionCall.ID,
					Name:      p.FunctionCall.Name,
					Arguments: p.FunctionCall.Args,
				},
				Turn: turn,
			})
		case p.FunctionResponse != nil:
			*messages = append(*messages, transcript.Message{
				Role: transcript.RoleToolResult,
				ToolResult: &transcript.ToolResultInfo{
					CallID:  p.FunctionResponse.ID,
					Status:  toolStatusSuccess,
					Content: contentBlockToString(p.FunctionResponse.Response),
				},
				Turn: turn,
			})
		case p.Text != "":
			textParts = append(textParts, p.Text)
		}
	}

	if len(textParts) == 0 {
		return ""
	}
	content := strings.Join(textParts, "\n\n")
	*messages = append(*messages, transcript.Message{Role: role, Content: content, Turn: turn})
	if role == transcript.RoleAssistant {
		return content
	}
	return ""
}
