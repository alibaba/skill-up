package runtime

import (
	"context"
	"errors"
	"io"
	"maps"
	"os"
	"strings"
	"time"
)

const (
	// ClaudeDirMode is the default directory mode for Claude files.
	ClaudeDirMode = 0o755
	// ClaudeFileMode is the default file mode for Claude files.
	ClaudeFileMode = 0o600
)

// mergeEnv returns the host env (os.Environ) with persistentEnv and callEnv
// overlaid, in that order (callEnv wins). Values are forwarded LITERALLY: no
// $VAR / ${VAR} expansion. This matches the docker and opensandbox runtimes,
// which also pass env literally — callers that need shell expansion should
// either resolve the value first or prepend `export X=...` to the command.
func mergeEnv(persistentEnv, callEnv map[string]string) []string {
	envMap := envMapFromList(os.Environ())
	maps.Copy(envMap, persistentEnv)
	maps.Copy(envMap, callEnv)

	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}
	return env
}

func envMapFromList(env []string) map[string]string {
	envMap := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			envMap[key] = value
		}
	}
	return envMap
}

func mergeEnvMaps(persistentEnv, callEnv map[string]string) map[string]string {
	if len(persistentEnv) == 0 && len(callEnv) == 0 {
		return nil
	}
	env := make(map[string]string, len(persistentEnv)+len(callEnv))
	maps.Copy(env, persistentEnv)
	maps.Copy(env, callEnv)
	return env
}

// ExecResult holds the output and exit code of a command execution.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// ExecOptions configures how a command is executed.
type ExecOptions struct {
	Cwd         string
	Env         map[string]string
	TimeoutSec  int
	ArtifactDir string
}

// Runtime defines the interface for sandbox runtimes.
//
//nolint:interfacebloat // Runtime is the shared contract for lifecycle, transfer, exec, and agent policy.
type Runtime interface {
	Create(ctx context.Context) error
	Close() error

	Start(ctx context.Context) error
	Stop(ctx context.Context) error

	UploadFile(ctx context.Context, sourcePath, targetPath string) error
	UploadDir(ctx context.Context, sourceDir, targetDir string) error
	DownloadFile(ctx context.Context, sourcePath, targetPath string) error
	// DownloadDir copies sourceDir from the runtime workspace to targetDir on the host.
	// For NoneRuntime, sourceDir may be relative to Workspace or an absolute host path.
	DownloadDir(ctx context.Context, sourceDir, targetDir string) error

	Exec(ctx context.Context, command string, opts ExecOptions) (ExecResult, error)
	// MergeEnv layers entries onto the runtime's persistent env baseline
	// (Config.Env). Subsequent Exec calls see these vars unless overridden
	// by opts.Env. Used by orchestrators (e.g. the evaluator) to seed
	// runtime-resolved values — for example the agent's PATH expanded
	// against the target shell — without each Exec caller needing to
	// know about them. Idempotent; later calls overwrite same-key values.
	MergeEnv(env map[string]string)
	Workspace() string
	// RequiresProcessSandbox reports whether agents should enable their own process sandbox.
	RequiresProcessSandbox() bool
}

// FileReadSeeker combines io.ReadSeeker for file access.
type FileReadSeeker interface {
	io.ReadSeeker
}

// SetupStep represents a single setup command to run.
type SetupStep struct {
	Run string
}

// Config holds all runtime configuration options.
type Config struct {
	Type           string
	Image          string
	WorkspaceMount string
	Env            map[string]string
	SetupSteps     []SetupStep

	SandboxTemplate string

	UseServerProxy bool
	ReadyTimeout   time.Duration
	SandboxTimeout time.Duration
	Entrypoint     []string
	Metadata       map[string]string
	Kwargs         map[string]string

	NetworkPolicy string   // deny_all, allow_declared
	AllowedEgress []string // FQDN/wildcard egress allowlist for allow_declared

	SkillPath string

	Delete bool
}

// MCPServerConfig describes a single MCP server available to a runtime.
type MCPServerConfig struct {
	Name      string
	Mode      string
	Transport string
	Command   string
	Args      []string
	Endpoint  string
	ConfigRef string
	Env       map[string]string
	Headers   map[string]string
	HeaderEnv map[string]string
}

// MCPConfig contains the MCP servers to install in a runtime.
type MCPConfig struct {
	Servers []MCPServerConfig
}

// SkillConfig identifies a skill source and its target install location.
type SkillConfig struct {
	Source string
	Target string
}

// NewRuntime creates a Runtime based on the config type.
func NewRuntime(cfg Config) (Runtime, error) {
	switch cfg.Type {
	case "none":
		return &NoneRuntime{cfg: cfg}, nil
	case "opensandbox":
		return NewOpenSandboxRuntime(cfg)
	case "docker":
		return NewDockerRuntime(cfg)
	default:
		return nil, errors.New("unknown runtime type: " + cfg.Type)
	}
}
