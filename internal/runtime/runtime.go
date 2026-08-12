package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/platform"
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
	// The capacity hint deliberately avoids summing both lengths: the sum is an
	// unbounded arithmetic expression feeding an allocation, which static analysis
	// flags as a possible overflow. Keys overlap anyway, so the larger input is a
	// sound lower bound and the map grows from there if needed.
	env := make(map[string]string, max(len(persistentEnv), len(callEnv)))
	maps.Copy(env, persistentEnv)
	maps.Copy(env, callEnv)
	return env
}

// mergeIntoEnvBaseline overlays src onto *target. Single shared
// implementation for Runtime.MergeEnv across the three concrete runtimes;
// they each retain a 1-line method so they continue to satisfy the
// interface, but the behaviour itself lives here.
func mergeIntoEnvBaseline(target *map[string]string, src map[string]string) {
	if len(src) == 0 {
		return
	}
	if *target == nil {
		*target = make(map[string]string, len(src))
	}
	maps.Copy(*target, src)
}

// ExecResult holds the output and exit code of a command execution.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// AgentMetadata carries case-level metadata that only agents building a
// structured session input (e.g. the Custom Engine) consume. It is threaded
// per-Run via ExecOptions.AgentMetadata rather than baked into the agent at
// construction, because a single agent instance is reused across cases.
type AgentMetadata struct {
	CaseID   string
	Variant  string
	MaxTurns int
}

// ExecOptions configures how a command is executed.
type ExecOptions struct {
	Cwd         string
	Env         map[string]string
	TimeoutSec  int
	ArtifactDir string

	// AgentMetadata carries case-level metadata for agents that build a
	// structured session input (e.g. Custom Engine). It is nil for runs that
	// do not need it; built-in agents ignore it. Read it via AgentMeta, which
	// is nil-safe.
	AgentMetadata *AgentMetadata
}

// AgentMeta returns the case-level agent metadata, or a zero value when unset,
// so callers can read its fields without a nil check.
func (o ExecOptions) AgentMeta() AgentMetadata {
	if o.AgentMetadata == nil {
		return AgentMetadata{}
	}
	return *o.AgentMetadata
}

// Runtime defines the interface for sandbox runtimes.
//
//nolint:interfacebloat // Runtime is the shared contract for lifecycle, transfer, exec, and agent policy.
type Runtime interface {
	Create(ctx context.Context) error
	Close() error

	Start(ctx context.Context) error
	Stop(ctx context.Context) error

	// UploadFile and UploadDir copy host files into the runtime workspace.
	// Implementations MUST preserve the source permission bits — in
	// particular the executable bit — so skills that ship runnable helper
	// scripts install without a chmod workaround. This holds for every
	// runtime whose target filesystem supports Unix file modes (none,
	// opensandbox, docker).
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
	// Shell describes the target command interpreter used by Exec. The target
	// may differ from the skill-up host (for example, a Linux container on a
	// Windows host), so callers must not re-derive it from platform.Host.
	Shell() platform.Shell
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
	Source  string
	Target  string
	Include []string
	Exclude []string
}

// NewRuntime creates a Runtime based on the config type.
func NewRuntime(cfg Config) (Runtime, error) {
	var (
		rt  Runtime
		err error
	)
	switch cfg.Type {
	case "none":
		rt = &NoneRuntime{cfg: cfg}
	case "opensandbox":
		rt, err = NewOpenSandboxRuntime(cfg)
	case "docker":
		rt, err = NewDockerRuntime(cfg)
	default:
		return nil, errors.New("unknown runtime type: " + cfg.Type)
	}
	if err != nil {
		return nil, err
	}
	if err := rt.Shell().Validate(); err != nil {
		return nil, fmt.Errorf("invalid %s runtime shell: %w", cfg.Type, err)
	}
	return rt, nil
}
