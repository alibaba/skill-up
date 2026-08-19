package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/observability"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// QoderCLIAgent implements Agent for qodercli.
type QoderCLIAgent struct {
	CLIAgent

	profile qoderCLIProfile
}

type qoderCLIProfile struct {
	edition        string
	binary         string
	credentialEnv  string
	exposeUsageEnv string
	configDir      string
	installURL     string
}

const (
	qoderEditionGlobal = "global"
	qoderEditionCN     = "cn"
)

var supportedQoderModels = []string{"lite", "efficient", "auto", "performance", "ultimate"}

// qoderExecPathProbeCmd resolves $HOME/.local/bin only — qodercli is a
// self-contained binary placed there by the official installer, not a node
// script, so the nvm path is unneeded.
const qoderExecPathProbeCmd = `printf '%s' "$HOME/.local/bin:$PATH"`

const (
	qoderExposeTokenUsageEnv     = "QODER_EXPOSE_TOKEN_USAGE"   //nolint:gosec // environment variable name, not a credential
	qoderCNExposeTokenUsageEnv   = "QODERCN_EXPOSE_TOKEN_USAGE" //nolint:gosec // environment variable name, not a credential
	qoderExposeTokenUsageEnabled = "true"
	qoderJSONOutputFlag          = " --output-format json"
)

func qoderProfileForKwargs(kwargs map[string]string) qoderCLIProfile {
	edition := strings.ToLower(strings.TrimSpace(kwargs[KwargEdition]))
	switch edition {
	case "", qoderEditionGlobal:
		return qoderCLIProfile{
			edition:        qoderEditionGlobal,
			binary:         "qodercli",
			credentialEnv:  credential.EnvQoderPersonalAccessToken,
			exposeUsageEnv: qoderExposeTokenUsageEnv,
			configDir:      ".qoder",
			installURL:     "https://qoder.com/install",
		}
	case qoderEditionCN:
		return qoderCLIProfile{
			edition:        qoderEditionCN,
			binary:         "qodercn",
			credentialEnv:  credential.EnvQoderCNPersonalAccessToken,
			exposeUsageEnv: qoderCNExposeTokenUsageEnv,
			configDir:      ".qoder-cn",
			installURL:     "https://static.qoder.com.cn/qoder-cli-cn/install.sh",
		}
	default:
		logging.Warnf("qodercli ignores unsupported edition %q and uses %q", edition, qoderEditionGlobal)
		return qoderProfileForKwargs(nil)
	}
}

// NewQoderCLIAgent creates a new QoderCLIAgent.
func NewQoderCLIAgent(cfg Config) *QoderCLIAgent {
	profile := qoderProfileForKwargs(cfg.Kwargs)
	if cfg.Name == "" {
		cfg.Name = "qodercli"
	}
	if cfg.CheckCmd == "" {
		cfg.CheckCmd = "command -v " + profile.binary
	}
	if cfg.RunCmd == "" {
		cfg.RunCmd = profile.binary + " -p \"%s\" 2>&1"
	}
	if cfg.SkillPath == "" {
		cfg.SkillPath = ".qoder/skills"
	}

	return &QoderCLIAgent{
		CLIAgent: CLIAgent{BaseAgent: NewBaseAgent(cfg)},
		profile:  profile,
	}
}

// CheckCredentials checks whether the selected qodercli edition can see its PAT
// either from the runtime env prepared by skill-up or from the current process env.
// Note: qodercli supports login-based authentication, so missing token is not a hard error.
// This method logs masked token presence or a warning when missing, but still returns nil to allow execution.
func (a *QoderCLIAgent) CheckCredentials(ctx context.Context) error {
	if token := a.Cfg.EnvVars[a.profile.credentialEnv]; token != "" {
		logging.DebugContextf(ctx, "%s detected for %s (source=runtime_env)", a.profile.credentialEnv, a.profile.binary)
		return nil
	}
	if token := os.Getenv(a.profile.credentialEnv); token != "" {
		_ = token
		logging.DebugContextf(ctx, "%s detected for %s (source=process_env)", a.profile.credentialEnv, a.profile.binary)
		return nil
	}

	logging.WarnContextf(ctx, "%s not set, %s will rely on existing login state if available", a.profile.credentialEnv, a.profile.binary)
	return nil
}

// Run executes the selected qodercli edition with the resolved model and environment overrides.
//
//nolint:dupl
func (a *QoderCLIAgent) Run(ctx context.Context, rt Runtime, opts ExecOptions, messages []transcript.Message) (*SessionResult, error) {
	if err := requireBashTargetShell(rt); err != nil {
		return nil, fmt.Errorf("%s: %w", a.Name(), err)
	}
	start := time.Now()

	instruction := BuildInstructionFromMessages(messages)
	cmd, promptDelivery, err := deliverPrompt(ctx, rt, opts, instruction, promptCommandBuilder{
		Inline: func(prompt string) string {
			return buildQoderRunCmdForBinary(a.profile.binary, prompt, a.effectiveModelName(ctx))
		},
		StdinFile: func(path string) string {
			return buildQoderRunStdinCmdForBinary(a.profile.binary, path, a.effectiveModelName(ctx))
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

	envVars := a.qoderRunEnvVars()
	opts = a.mergeExecOptionsEnv(ctx, opts, envVars, a.buildAgentObservabilityAttrs(nil))
	ctx = observability.ContextWithConfiguredAgentSpanAttributes(ctx, opts.Env)

	result, err := rt.Exec(ctx, cmd, opts)
	sessionResult := a.buildSessionResult(ctx, rt, opts, instruction, start, result)
	if sessionResult != nil {
		sessionResult.PromptDelivery = promptDelivery
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
		return sessionResult, fmt.Errorf("%s run failed: %w", a.profile.binary, err)
	}

	if result.ExitCode != 0 {
		return sessionResult, fmt.Errorf("%s run failed (exit %d): %s", a.profile.binary, result.ExitCode, result.Stderr)
	}

	return sessionResult, nil
}

func (a *QoderCLIAgent) effectiveModelName(ctx context.Context) string {
	if a.Cfg.ModelName == "" {
		return ""
	}
	if slices.Contains(supportedQoderModels, a.Cfg.ModelName) {
		return a.Cfg.ModelName
	}
	logging.WarnContextf(
		ctx,
		"%s ignores configured model %q and will use local qoder model settings instead",
		a.profile.binary,
		a.Cfg.ModelName,
	)
	return ""
}

func (a *QoderCLIAgent) qoderRunEnvVars() map[string]string {
	envVars := a.credentialEnvVars("", "")
	if _, configured := envVars[a.profile.exposeUsageEnv]; !configured {
		envVars[a.profile.exposeUsageEnv] = qoderExposeTokenUsageEnabled
	}
	return envVars
}

func buildQoderRunCmd(instruction, model string) string {
	return buildQoderRunCmdForBinary("qodercli", instruction, model)
}

func buildQoderRunCmdForBinary(binary, instruction, model string) string {
	cmd := binary + " --permission-mode=bypass_permissions" + qoderJSONOutputFlag
	if model != "" {
		cmd += " --model " + shellQuote(model)
	}
	cmd += " -p " + shellQuote(instruction)

	return cmd
}

func buildQoderRunStdinCmdForBinary(binary, promptPath, model string) string {
	cmd := "cat " + shellQuote(promptPath) + " | " + binary + " --permission-mode=bypass_permissions" + qoderJSONOutputFlag
	if model != "" {
		cmd += " --model " + shellQuote(model)
	}
	cmd += " -p -"

	return cmd
}

// buildQoderResumeCmd constructs a Global qodercli command that resumes an existing
// session identified by sessionID and sends a new user prompt.
func buildQoderResumeCmd(instruction, model, sessionID string) string {
	return buildQoderResumeCmdForBinary("qodercli", instruction, model, sessionID)
}

func buildQoderResumeCmdForBinary(binary, instruction, model, sessionID string) string {
	cmd := binary + " --permission-mode=bypass_permissions" + qoderJSONOutputFlag
	if model != "" {
		cmd += " --model " + shellQuote(model)
	}
	cmd += " -r " + shellQuote(sessionID)
	cmd += " -p " + shellQuote(instruction)
	return cmd
}

func (a *QoderCLIAgent) buildSessionResult(ctx context.Context, rt Runtime, opts ExecOptions, instruction string, start time.Time, result ExecResult) *SessionResult {
	trans, finalMsg, inputTokens, outputTokens := parseStreamOutput(result.Stdout)
	sessionID := parseQoderSessionID(result.Stdout)

	cleanupCtx, cleanupCancel := sessionCleanupContext(ctx)
	defer cleanupCancel()

	generatedFiles := []string{}
	if artifactPath, err := persistSessionArtifact(cleanupCtx, rt, opts.ArtifactDir, "stdout.json", result.Stdout); err == nil {
		if opts.ArtifactDir == "" {
			generatedFiles = append(generatedFiles, artifactPath)
		}
	}

	sessionFilePath := a.findSessionFile(cleanupCtx, rt)
	if sessionID == "" && sessionFilePath != "" {
		sessionID = extractSessionIDFromPath(sessionFilePath)
	}

	var cleanupSession func()
	generatedFiles, cleanupSession = withDownloadedSession(
		cleanupCtx, rt, opts.ArtifactDir, sessionFilePath, generatedFiles,
		func(artifactPath string) {
			t, f, inTok, outTok := parseSessionFile(artifactPath)
			if len(t) > 0 {
				trans = t
				if f != "" {
					finalMsg = f
				}
			}
			inputTokens = max(inputTokens, inTok)
			outputTokens = max(outputTokens, outTok)
		},
	)
	defer cleanupSession()

	if trans == nil {
		assistantMessage := finalMsg
		if assistantMessage == "" {
			assistantMessage = strings.TrimSpace(result.Stdout)
		}
		if assistantMessage != "" {
			trans = transcript.Transcript{
				{Role: transcript.RoleUser, Content: instruction, Turn: 1},
				{Role: transcript.RoleAssistant, Content: assistantMessage, Turn: 1},
			}
			finalMsg = assistantMessage
		}
	}
	return &SessionResult{
		Engine:       a.Name(),
		SessionID:    sessionID,
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

func parseQoderSessionID(output string) string {
	var envelope struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &envelope); err != nil {
		return ""
	}
	return envelope.SessionID
}

// RunTurn resumes an existing QoderCLI session and sends a single user
// message. If sessionID is empty, it starts a new session (first turn).
// This implements the SessionResumer interface for multi-turn evaluation.
func (a *QoderCLIAgent) RunTurn(ctx context.Context, rt Runtime, opts ExecOptions, message transcript.Message, sessionID string) (*SessionResult, error) {
	if sessionID == "" {
		// First turn — delegate to Run which creates a new session.
		return a.Run(ctx, rt, opts, []transcript.Message{message})
	}

	if err := requireBashTargetShell(rt); err != nil {
		return nil, fmt.Errorf("%s: %w", a.Name(), err)
	}
	start := time.Now()

	instruction := message.Content
	cmd := buildQoderResumeCmdForBinary(a.profile.binary, instruction, a.effectiveModelName(ctx), sessionID)

	envVars := a.qoderRunEnvVars()
	opts = a.mergeExecOptionsEnv(ctx, opts, envVars, a.buildAgentObservabilityAttrs(nil))
	ctx = observability.ContextWithConfiguredAgentSpanAttributes(ctx, opts.Env)

	result, err := rt.Exec(ctx, cmd, opts)
	sessionResult := a.buildSessionResult(ctx, rt, opts, instruction, start, result)
	if sessionResult != nil {
		sessionResult.SessionID = sessionID
	}
	if err != nil {
		if sessionResult == nil {
			sessionResult = &SessionResult{
				Engine:     a.Name(),
				SessionID:  sessionID,
				ExitCode:   1,
				DurationMs: time.Since(start).Milliseconds(),
				Stderr:     result.Stderr,
				Artifacts:  &SessionArtifacts{},
			}
		}
		return sessionResult, fmt.Errorf("%s resume failed: %w", a.profile.binary, err)
	}

	if result.ExitCode != 0 {
		return sessionResult, fmt.Errorf("%s resume failed (exit %d): %s", a.profile.binary, result.ExitCode, result.Stderr)
	}

	return sessionResult, nil
}

// findQoderSessionFile resolves the newest session JSONL that belongs to this
// workspace under the Qoder projects tree. Per runtime isolation, HOME and the
// tree are read only inside the runtime via Exec (not os.Getenv / host os.Stat).
//
// sessionDepth of 1 matters beyond performance: a Skill or Task call spawns a
// sub-agent whose transcript is written to <sessionID>/subagents/agent-*.jsonl
// inside this same tree. Those are often the newest files, and their names are
// not resumable session ids.
func findQoderSessionFile(ctx context.Context, rt Runtime) string {
	return findQoderSessionFileInConfigDir(ctx, rt, ".qoder")
}

func (a *QoderCLIAgent) findSessionFile(ctx context.Context, rt Runtime) string {
	return findQoderSessionFileInConfigDir(ctx, rt, a.profile.configDir)
}

func findQoderSessionFileInConfigDir(ctx context.Context, rt Runtime, configDir string) string {
	return findAgentSessionJSONL(ctx, rt, agentSessionLookup{
		projectsRootTmpl: "$home/" + configDir + "/projects",
		sessionDepth:     1,
		findExtra:        `! -name "*-session.json"`,
	})
}

// Install installs the selected Qoder CLI edition via its official install script.
//
//nolint:dupl // each agent Install shares the same probe→merge→exec lifecycle; the deltas (probe const, default install cmd) are pulled out, leaving the orchestration intentionally similar.
func (a *QoderCLIAgent) Install(ctx context.Context, rt Runtime) error {
	a.probeAndMergePATH(ctx, rt, qoderExecPathProbeCmd)

	opts := ExecOptions{Cwd: "/"}
	opts = a.mergeExecOptionsEnv(ctx, opts, nil, nil)

	installCmd := a.Cfg.InstallCmd
	if installCmd == "" {
		installCmd = defaultQoderCLIInstallCmdForProfile(a.profile)
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

// InstallMCP installs MCP servers with the Qoder CLI.
func (a *QoderCLIAgent) InstallMCP(ctx context.Context, rt Runtime, mcpCfg runtime.MCPConfig) error {
	return installMCPServers(ctx, rt, mcpCfg, func(server runtime.MCPServerConfig) (string, error) {
		return buildQoderMCPInstallCmdForBinary(a.profile.binary, server)
	})
}

func buildQoderMCPInstallCmd(server runtime.MCPServerConfig) (string, error) {
	return buildQoderMCPInstallCmdForBinary("qodercli", server)
}

func buildQoderMCPInstallCmdForBinary(binary string, server runtime.MCPServerConfig) (string, error) {
	return buildClaudeCompatibleMCPInstallCmd(binary, binary, server)
}

func defaultQoderCLIInstallCmd() string {
	return defaultQoderCLIInstallCmdForProfile(qoderProfileForKwargs(nil))
}

func defaultQoderCLIInstallCmdForProfile(profile qoderCLIProfile) string {
	return strings.Join([]string{
		"set -e",
		"if command -v " + profile.binary + " >/dev/null 2>&1; then exit 0; fi",
		"curl -fsSL " + profile.installURL + " | bash",
	}, "\n")
}
