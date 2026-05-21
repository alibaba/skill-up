// Package agent provides agent installation and execution for skill-up.
package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/observability"
	"github.com/alibaba/skill-up/internal/platform"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/internal/shellquote"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// ErrAgentNotFound is returned when the agent executable is not found.
var ErrAgentNotFound = errors.New("agent not found in PATH")

// ErrAgentRequiresBash is returned when an agent CLI is invoked on a Windows
// host where Git Bash (or another bash discoverable by platform.DiscoverBash)
// is not available. Agent commands are POSIX-quoted and assume a bash
// interpreter; running them through the cmd.exe fallback would let metachars
// (`& | " %VAR%`) in the instruction reach the shell unprotected. See
// docs/guide/windows.md for the documented limitation.
var ErrAgentRequiresBash = errors.New("agent CLI execution on Windows requires bash; install Git for Windows or set SKILL_UP_BASH")

// requireBashOnWindowsHost rejects agent execution when the runtime's target
// is Windows but the host shell would be cmd.exe. We only enforce this for
// runtimes whose target matches the host (NoneRuntime today); sandboxed
// runtimes target a non-Windows guest and never go through platform.Host().
func requireBashOnWindowsHost(rt Runtime) error {
	if rt.TargetGOOS() != "windows" {
		return nil
	}
	if platform.Host().IsBash {
		return nil
	}
	return ErrAgentRequiresBash
}

// ErrAgentInstallFailed is returned when agent installation fails.
var ErrAgentInstallFailed = errors.New("agent installation failed")

// SessionResult holds the result of an agent session execution.
type SessionResult struct {
	Engine       string                `json:"engine,omitempty"`
	Model        string                `json:"model,omitempty"`
	ExitCode     int                   `json:"exit_code"`
	DurationMs   int64                 `json:"duration_ms"`
	Turns        int                   `json:"turns"`
	InputTokens  int                   `json:"input_tokens,omitempty"`
	OutputTokens int                   `json:"output_tokens,omitempty"`
	FinalMessage string                `json:"final_message,omitempty"`
	Stderr       string                `json:"stderr,omitempty"`
	Transcript   transcript.Transcript `json:"transcript,omitempty"`
	Artifacts    *SessionArtifacts     `json:"artifacts,omitempty"`
}

// SessionArtifacts holds artifacts produced during an agent session.
type SessionArtifacts struct {
	WorkspaceDiff  string         `json:"workspace_diff,omitempty"`
	GeneratedFiles []string       `json:"generated_files,omitempty"` // Runtime file paths, e.g. ["outputs/stdout.json", "outputs/transcript.jsonl"]
	Files          []ArtifactFile `json:"files,omitempty"`           // Structured artifact declarations (Custom Engine).
	Logs           string         `json:"logs,omitempty"`
}

// ArtifactFile is a structured artifact declaration returned by an agent.
// Exactly one of Path, URL, Content, ContentBase64 should be set.
type ArtifactFile struct {
	Name          string `json:"name"`
	Path          string `json:"path,omitempty"`
	URL           string `json:"url,omitempty"`
	Content       string `json:"content,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
	ContentType   string `json:"content_type,omitempty"`
}

// Config configures the agent.
type Config struct {
	Name            string
	Version         string
	Entry           string
	InstallCmd      string
	RunCmd          string // Command template for running agent; use %s for instruction placeholder
	VersionCmd      string
	CheckCmd        string
	InstallMCPCmd   string // Template for installing MCP server, supports {{.Name}}, {{.Endpoint}}, {{.ConfigRef}}, {{.Workspace}}, {{.Env}}, {{.Headers}}
	InstallSkillCmd string // Template for installing skill, supports {{.Source}}, {{.Target}}, {{.Workspace}}
	EnvVars         map[string]string
	// SkillPath is the directory where the agent reads skills.
	// If not set, agent uses default location (~/.claude/skills/).
	SkillPath     string
	ModelName     string
	ModelProvider string
	APIKey        string
	BaseURL       string
	// Kwargs carries agent-specific key/value options forwarded from
	// EngineConfig.Kwargs. Each agent reads only the keys it understands;
	// unknown keys are ignored. See agent kwargs helpers in kwargs.go.
	Kwargs map[string]string
	// Custom carries the custom engine configuration when Name does not match
	// a built-in agent. It is nil for built-in agents.
	Custom *config.CustomEngineConfig
}

// Runtime is an alias for runtime.Runtime for agent package convenience.
type Runtime = runtime.Runtime

// ExecOptions is an alias for runtime.ExecOptions.
type ExecOptions = runtime.ExecOptions

// ExecResult is an alias for runtime.ExecResult.
type ExecResult = runtime.ExecResult

// Agent interface for agent installation and execution.
type Agent interface {
	// Name returns the agent name.
	Name() string
	// Install installs the agent in the environment.
	Install(ctx context.Context, rt Runtime) error
	// InstallMCP installs MCP servers.
	InstallMCP(ctx context.Context, rt Runtime, mcpCfg runtime.MCPConfig) error
	// InstallSkill installs a skill to the agent's skill directory.
	InstallSkill(ctx context.Context, rt Runtime, skillCfg runtime.SkillConfig) error
	// Run runs the agent with the given messages and returns output.
	Run(ctx context.Context, rt Runtime, opts ExecOptions, messages []transcript.Message) (*SessionResult, error)
	// Check checks if the agent is available.
	Check(ctx context.Context, rt Runtime) error
	// CheckCredentials checks if the required credentials are set.
	CheckCredentials(ctx context.Context) error
}

// BaseAgent provides common functionality for agents.
// Embedded by specific agent implementations.
type BaseAgent struct {
	Cfg     Config
	Version string
}

// ExitCodeSignalKilled is the ExitCode value in SessionResult when the agent
// process was terminated by a signal (SIGKILL, SIGSEGV, etc.).
// This matches Go's os.ProcessState.ExitCode() behavior on Unix, which returns
// -1 when the process did not exit normally.
const ExitCodeSignalKilled = -1

const (
	agentProviderOpenAI    = "openai"
	agentProviderAnthropic = "anthropic"
)

// NewBaseAgent creates a new BaseAgent with the given config.
// It preserves the resolved config passed from the caller.
func NewBaseAgent(cfg Config) BaseAgent {
	if cfg.EnvVars == nil {
		cfg.EnvVars = make(map[string]string)
	}

	return BaseAgent{
		Cfg:     cfg,
		Version: cfg.Version,
	}
}

// Name returns the agent name.
func (a *BaseAgent) Name() string {
	return a.Cfg.Name
}

// CheckCredentials checks if the required credentials are set.
func (a *BaseAgent) CheckCredentials(ctx context.Context) error {
	switch a.Cfg.ModelProvider {
	case agentProviderOpenAI:
		a.logCredentialStatus(
			ctx,
			credential.EnvOpenAIAPIKey,
			credential.EnvOpenAIBaseURL,
			"OPENAI_API_KEY not set, agent will rely on existing login state if available",
		)
	case agentProviderAnthropic:
		a.logCredentialStatus(
			ctx,
			credential.EnvAnthropicAPIKey,
			credential.EnvAnthropicBaseURL,
			"ANTHROPIC_API_KEY not set, agent will rely on existing login state if available",
		)
	}

	return nil
}

func (a *BaseAgent) logCredentialStatus(ctx context.Context, apiKeyEnv, baseURLEnv, missingMessage string) {
	if a.Cfg.APIKey != "" {
		logging.DebugContextf(ctx, "%s detected for %s (masked: %s, source=agent_config)", apiKeyEnv, a.Name(), credential.MaskAPIKey(a.Cfg.APIKey))
		if baseURLEnv != "" && a.Cfg.BaseURL != "" {
			logging.DebugContextf(ctx, "%s detected for %s (value: %s, source=agent_config)", baseURLEnv, a.Name(), a.Cfg.BaseURL)
		}
		return
	}
	// Fall back to the current provider's process env for observability only.
	// This does not scan unrelated providers.
	if apiKey := os.Getenv(apiKeyEnv); apiKey != "" {
		logging.DebugContextf(ctx, "%s detected for %s (source=process_env)", apiKeyEnv, a.Name())
		if baseURLEnv != "" && os.Getenv(baseURLEnv) != "" {
			logging.DebugContextf(ctx, "%s detected for %s (source=process_env)", baseURLEnv, a.Name())
		}
		return
	}
	logging.WarnContextf(ctx, "%s", missingMessage)
}

func (a *BaseAgent) credentialEnvVars(apiKeyEnv, baseURLEnv string) map[string]string {
	envVars := mapsClone(a.Cfg.EnvVars)
	if a.Cfg.APIKey != "" && apiKeyEnv != "" {
		envVars[apiKeyEnv] = a.Cfg.APIKey
	}
	if a.Cfg.BaseURL != "" && baseURLEnv != "" {
		envVars[baseURLEnv] = a.Cfg.BaseURL
	}

	return envVars
}

// installSkillDefault is the fallback skill installer used when an agent does
// not configure its own InstallSkillCmd template. It installs the skill source
// under a.Cfg.SkillPath (or the caller-supplied target). Defined on BaseAgent
// so both CLIAgent and CustomAgent share it via embedding, without making
// CustomAgent inherit the rest of CLIAgent.
func (a *BaseAgent) installSkillDefault(ctx context.Context, rt Runtime, skillCfg runtime.SkillConfig) error {
	target := skillCfg.Target
	if target == "" && a.Cfg.SkillPath != "" {
		target = filepath.Join(a.Cfg.SkillPath, filepath.Base(skillCfg.Source))
	}
	return installSkill(ctx, rt, skillCfg.Source, target)
}

func persistRuntimeArtifact(ctx context.Context, rt Runtime, targetPath, content string) error {
	f, err := os.CreateTemp("", "skill-up-artifact-*")
	if err != nil {
		return fmt.Errorf("create temp artifact file: %w", err)
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath) //nolint:errcheck

	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp artifact file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp artifact file: %w", err)
	}
	if err := rt.UploadFile(ctx, tmpPath, targetPath); err != nil {
		return fmt.Errorf("upload %s: %w", targetPath, err)
	}
	return nil
}

func persistSessionArtifact(ctx context.Context, rt Runtime, artifactDir, targetPath, content string) (string, error) {
	if artifactDir != "" {
		return writeLocalArtifact(artifactDir, filepath.Base(targetPath), content)
	}
	if err := persistRuntimeArtifact(ctx, rt, targetPath, content); err != nil {
		return "", err
	}
	return targetPath, nil
}

func writeLocalArtifact(artifactDir, fileName, content string) (string, error) {
	path := filepath.Join(artifactDir, filepath.Base(fileName))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create artifact directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("write artifact file %s: %w", path, err)
	}
	if _, err := io.WriteString(f, content); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write artifact file %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close artifact file %s: %w", path, err)
	}
	return path, nil
}

func downloadSessionArtifact(ctx context.Context, rt Runtime, artifactDir, sessionFilePath string) (artifactPath, registeredPath string, cleanup func(), ok bool) {
	cleanup = func() {}
	if artifactDir == "" {
		artifactPath = filepath.Join(os.TempDir(), "session-"+filepath.Base(sessionFilePath))
		if err := rt.DownloadFile(ctx, sessionFilePath, artifactPath); err != nil {
			return "", "", cleanup, false
		}
		cleanup = func() { _ = os.Remove(artifactPath) }
		return artifactPath, sessionFilePath, cleanup, true
	}
	artifactPath = filepath.Join(artifactDir, filepath.Base(sessionFilePath))
	if err := rt.DownloadFile(ctx, sessionFilePath, artifactPath); err != nil {
		return "", "", cleanup, false
	}
	return artifactPath, "", cleanup, true
}

func (a *BaseAgent) mergeExecOptionsEnv(ctx context.Context, opts ExecOptions, envVars map[string]string, attrs map[string]string) ExecOptions {
	merged := map[string]string{}
	maps.Copy(merged, envVars)
	maps.Copy(merged, opts.Env)
	maps.Copy(merged, observability.AgentEnv(ctx, merged, attrs))
	opts.Env = merged
	return opts
}

// probeAndMergePATH runs probeCmd against rt and merges the literal result
// into rt's env baseline under key "PATH". Each agent's Install calls this
// with its own probeCmd (e.g. claudeCodeExecPathProbeCmd) so the resolved
// PATH covers the directories where that agent's binaries actually live.
//
// On probe failure the runtime's default PATH stands and a warning is
// logged; the install command still runs (its own bootstrap, if any, may
// still succeed).
func (a *BaseAgent) probeAndMergePATH(ctx context.Context, rt Runtime, probeCmd string) {
	res, err := rt.Exec(ctx, probeCmd, ExecOptions{})
	if err != nil || res.ExitCode != 0 {
		logging.WarnContextf(ctx, "agent %s: PATH probe failed (err=%v exit=%d); using runtime default PATH", a.Name(), err, res.ExitCode)
		return
	}
	path := strings.TrimSpace(res.Stdout)
	if path == "" {
		return
	}
	rt.MergeEnv(map[string]string{"PATH": path})
}

func (a *BaseAgent) buildAgentObservabilityAttrs(extra map[string]string) map[string]string {
	attrs := mapsClone(extra)
	if attrs == nil {
		attrs = make(map[string]string)
	}
	if a.Name() != "" {
		attrs["skill_up.engine"] = a.Name()
	}
	if a.Cfg.ModelName != "" {
		attrs["skill_up.model"] = formatAgentModel(a.Cfg.ModelProvider, a.Cfg.ModelName)
	}
	return attrs
}

func formatAgentModel(provider, model string) string {
	if provider == "" {
		return model
	}
	if model == "" {
		return provider
	}
	return provider + "/" + model
}

// shellQuote quotes a string for safe POSIX shell usage. Agent commands are
// always composed for and executed by bash (via the Node/nvm bootstrap), so
// POSIX quoting is correct even when skill-up itself runs on a Windows host.
// It delegates to internal/shellquote so the project keeps a single quoting
// implementation.
func shellQuote(s string) string {
	return shellquote.QuotePOSIX(s)
}

// BuildInstructionFromMessages converts messages to a single instruction string.
// This is a fallback for agents that don't support structured input.
func BuildInstructionFromMessages(messages []transcript.Message) string {
	if len(messages) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, msg := range messages {
		if msg.Role == transcript.RoleUser {
			sb.WriteString(msg.Content)
			sb.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(sb.String())
}
