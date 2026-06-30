package agent

import (
	"context"
	"fmt"
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
		installCmd = defaultQwenCodeInstallCmd()
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
	lines := []string{
		"set -e",
		"if command -v qwen >/dev/null 2>&1; then exit 0; fi",
	}
	lines = append(lines, nodeBootstrapLines(agentNodeDefaultVersion)...)
	lines = append(lines, "npm install -g "+shellQuote(qwenCodePackage))
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

// Run executes qwen non-interactively (`qwen --yolo -p <instruction>`) and
// converts stdout into a transcript.
func (a *QwenCodeAgent) Run(ctx context.Context, rt Runtime, opts ExecOptions, messages []transcript.Message) (*SessionResult, error) {
	if err := requireBashOnWindowsHost(rt); err != nil {
		return nil, fmt.Errorf("%s: %w", a.Name(), err)
	}
	start := time.Now()

	instruction := BuildInstructionFromMessages(messages)
	model := a.effectiveModelName(ctx)

	envVars := a.credentialEnvVars(credential.EnvOpenAIAPIKey, credential.EnvOpenAIBaseURL)
	// Qwen Code resolves the active model from OPENAI_MODEL when --model is not
	// supplied; mirroring the flag into the env keeps env-only configurations
	// working and matches the OpenAI-compatible provider contract.
	if model != "" {
		envVars[credential.EnvOpenAIModel] = model
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

	cmd := buildQwenCodeRunCmd(instruction, model)
	result, err := rt.Exec(ctx, cmd, opts)
	sessionResult := a.buildSessionResult(ctx, rt, opts, instruction, start, result)
	if err != nil {
		return sessionResult, fmt.Errorf("qwen_code run failed: %w", err)
	}
	if result.ExitCode != 0 {
		return sessionResult, fmt.Errorf("qwen_code run failed (exit %d): %s", result.ExitCode, result.Stderr)
	}

	return sessionResult, nil
}

func (a *QwenCodeAgent) effectiveModelName(_ context.Context) string {
	return strings.TrimSpace(a.Cfg.ModelName)
}

func buildQwenCodeRunCmd(instruction, model string) string {
	// --yolo auto-approves every action so the run is fully non-interactive;
	// -p runs the prompt in headless mode and prints the final answer to stdout.
	cmd := "qwen --yolo"
	if model != "" {
		cmd += " -m " + shellQuote(model)
	}
	cmd += " -p " + shellQuote(instruction)
	return cmd
}

func (a *QwenCodeAgent) buildSessionResult(ctx context.Context, rt Runtime, opts ExecOptions, instruction string, start time.Time, result ExecResult) *SessionResult {
	cleanupCtx, cleanupCancel := sessionCleanupContext(ctx)
	defer cleanupCancel()

	generatedFiles := []string{}
	if artifactPath, err := persistSessionArtifact(cleanupCtx, rt, opts.ArtifactDir, "stdout.txt", result.Stdout); err == nil {
		if opts.ArtifactDir == "" {
			generatedFiles = append(generatedFiles, artifactPath)
		}
	}

	var trans transcript.Transcript
	var finalMsg string
	if final := strings.TrimSpace(result.Stdout); final != "" {
		trans = transcript.Transcript{
			{Role: transcript.RoleUser, Content: instruction, Turn: 1},
			{Role: transcript.RoleAssistant, Content: final, Turn: 1},
		}
		finalMsg = final
	}

	return &SessionResult{
		Engine:       a.Name(),
		ExitCode:     result.ExitCode,
		DurationMs:   time.Since(start).Milliseconds(),
		Turns:        countTurns(trans),
		FinalMessage: finalMsg,
		Stderr:       result.Stderr,
		Transcript:   trans,
		Artifacts: &SessionArtifacts{
			GeneratedFiles: generatedFiles,
		},
	}
}
