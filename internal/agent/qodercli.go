package agent

import (
	"context"
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
}

var supportedQoderModels = []string{"lite", "efficient", "auto", "performance", "ultimate"}

// qoderExecPathProbeCmd resolves $HOME/.local/bin only — qodercli is a
// self-contained binary placed there by the official installer, not a node
// script, so the nvm path is unneeded.
const qoderExecPathProbeCmd = `printf '%s' "$HOME/.local/bin:$PATH"`

// NewQoderCLIAgent creates a new QoderCLIAgent.
func NewQoderCLIAgent(cfg Config) *QoderCLIAgent {
	if cfg.Name == "" {
		cfg.Name = "qodercli"
	}
	if cfg.CheckCmd == "" {
		cfg.CheckCmd = "command -v qodercli"
	}
	if cfg.RunCmd == "" {
		cfg.RunCmd = "qodercli -p \"%s\" 2>&1"
	}
	if cfg.SkillPath == "" {
		cfg.SkillPath = ".qoder/skills"
	}

	return &QoderCLIAgent{
		CLIAgent: CLIAgent{BaseAgent: NewBaseAgent(cfg)},
	}
}

// CheckCredentials checks whether qodercli can see QODER_PERSONAL_ACCESS_TOKEN
// either from the runtime env prepared by skill-up or from the current process env.
// Note: qodercli supports login-based authentication, so missing token is not a hard error.
// This method logs masked token presence or a warning when missing, but still returns nil to allow execution.
func (a *QoderCLIAgent) CheckCredentials(ctx context.Context) error {
	if token := a.Cfg.EnvVars[credential.EnvQoderPersonalAccessToken]; token != "" {
		logging.DebugContextf(ctx, "QODER_PERSONAL_ACCESS_TOKEN detected for qodercli (source=runtime_env)")
		return nil
	}
	if token := os.Getenv(credential.EnvQoderPersonalAccessToken); token != "" {
		_ = token
		logging.DebugContextf(ctx, "QODER_PERSONAL_ACCESS_TOKEN detected for qodercli (source=process_env)")
		return nil
	}

	logging.WarnContextf(ctx, "QODER_PERSONAL_ACCESS_TOKEN not set, qodercli will rely on existing login state if available")
	return nil
}

// Run executes qodercli with the resolved model and environment overrides.
//
//nolint:dupl
func (a *QoderCLIAgent) Run(ctx context.Context, rt Runtime, opts ExecOptions, messages []transcript.Message) (*SessionResult, error) {
	if err := requireBashOnWindowsHost(rt); err != nil {
		return nil, fmt.Errorf("%s: %w", a.Name(), err)
	}
	start := time.Now()

	instruction := BuildInstructionFromMessages(messages)
	cmd := buildQoderRunCmd(instruction, a.effectiveModelName(ctx))

	envVars := a.credentialEnvVars("", "")
	opts = a.mergeExecOptionsEnv(ctx, opts, envVars, a.buildAgentObservabilityAttrs(nil))
	ctx = observability.ContextWithConfiguredAgentSpanAttributes(ctx, opts.Env)

	result, err := rt.Exec(ctx, cmd, opts)
	sessionResult := a.buildSessionResult(ctx, rt, opts, instruction, start, result)
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
		return sessionResult, fmt.Errorf("qodercli run failed: %w", err)
	}

	if result.ExitCode != 0 {
		return sessionResult, fmt.Errorf("qodercli run failed (exit %d): %s", result.ExitCode, result.Stderr)
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
		"qodercli ignores configured model %q and will use local qoder model settings instead",
		a.Cfg.ModelName,
	)
	return ""
}

func buildQoderRunCmd(instruction, model string) string {
	cmd := "qodercli --permission-mode=bypass_permissions"
	if model != "" {
		cmd += " --model " + shellQuote(model)
	}
	cmd += " -p " + shellQuote(instruction)

	return cmd
}

func (a *QoderCLIAgent) buildSessionResult(ctx context.Context, rt Runtime, opts ExecOptions, instruction string, start time.Time, result ExecResult) *SessionResult {
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
		cleanupCtx, rt, opts.ArtifactDir, findQoderSessionFile(cleanupCtx, rt), generatedFiles,
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

	if trans == nil && result.Stdout != "" {
		trans = transcript.Transcript{
			{Role: transcript.RoleUser, Content: instruction, Turn: 1},
			{Role: transcript.RoleAssistant, Content: result.Stdout, Turn: 2},
		}
		finalMsg = result.Stdout
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

// findQoderSessionFile resolves the newest matching session JSONL under the Qoder
// projects tree for this workspace. Per runtime isolation, HOME and the tree
// are read only inside the runtime via Exec (not os.Getenv / host os.Stat).
func findQoderSessionFile(ctx context.Context, rt Runtime) string {
	return findAgentSessionJSONL(ctx, rt, agentSessionLookup{
		envVar:    "SKILL_UP_QODER_WSKEY",
		rootTmpl:  "$home/.qoder/projects/$SKILL_UP_QODER_WSKEY",
		findExtra: `! -name "*-session.json"`,
	})
}

// Install installs qoder CLI via official install script.
//
//nolint:dupl // each agent Install shares the same probe→merge→exec lifecycle; the deltas (probe const, default install cmd) are pulled out, leaving the orchestration intentionally similar.
func (a *QoderCLIAgent) Install(ctx context.Context, rt Runtime) error {
	a.probeAndMergePATH(ctx, rt, qoderExecPathProbeCmd)

	opts := ExecOptions{Cwd: "/"}
	opts = a.mergeExecOptionsEnv(ctx, opts, nil, nil)

	installCmd := a.Cfg.InstallCmd
	if installCmd == "" {
		installCmd = defaultQoderCLIInstallCmd()
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
	return installMCPServers(ctx, rt, mcpCfg, buildQoderMCPInstallCmd)
}

func buildQoderMCPInstallCmd(server runtime.MCPServerConfig) (string, error) {
	return buildClaudeCompatibleMCPInstallCmd("qodercli", "qodercli", server)
}

func defaultQoderCLIInstallCmd() string {
	return strings.Join([]string{
		"set -e",
		"if command -v qodercli >/dev/null 2>&1; then exit 0; fi",
		"curl -fsSL https://qoder.com/install | bash",
	}, "\n")
}
