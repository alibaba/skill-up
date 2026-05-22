# Runtime

The Runtime module is responsible for creating and managing the evaluation execution environment. The skill-up program runs on the host machine, while the agent under evaluation may run in a different environment (remote sandbox, etc.). Therefore, **skill-up cannot directly access files or environment variables inside the runtime** — all interaction must go through the Runtime interface.

## Core Principle

> **Cross-environment isolation**: the skill-up program is isolated from the runtime environment and cannot directly read/write files or environment variables inside the runtime. All file access must go through the `UploadFile` / `DownloadFile` interfaces, and all environment-variable access must go through the `Exec` interface.

## Directory Layout

### Inside the Runtime

```
{workspace}/                    # Runtime workspace root
├── outputs/                    # Agent execution artifacts
│   ├── stdout.json             # Claude Code standard output (JSONL format)
│   ├── stdout.txt              # QoderCLI standard output (plain text format)
│   └── transcript.jsonl        # Claude session trace file (if any)
└── ...                         # Agent working files

{skill-up}/                     # skill-up host environment
└── {skill-name}-workspace/     # Evaluation results directory
    └── iteration-{n}/
        └── {case-id}/
            └── {config}/
                └── outputs/    # Artifacts downloaded from the runtime
                    ├── agent/run/    # Agent execution artifacts
                    │   ├── stdout.json
                    │   └── transcript.jsonl
                    └── judge/        # Workspace downloaded after judge execution
```

### Artifact Download Stages

| Stage | Source | Destination |
|------|--------|-------------|
| After agent execution | `runtime:outputs/` | `outputs/agent/run/` |
| After judge execution | `runtime:{workspace}/` | `outputs/judge/` |

## Runtime Interface

```go
type Runtime interface {
    Create(ctx context.Context) error    // Initialize the workspace
    Close() error                        // Clean up resources

    Start(ctx context.Context) error     // Start the environment
    Stop(ctx context.Context) error      // Stop the environment

    // File transfer
    UploadFile(ctx context.Context, sourcePath, targetPath string) error
    UploadDir(ctx context.Context, sourceDir, targetDir string) error
    DownloadFile(ctx context.Context, sourcePath, targetPath string) error
    DownloadDir(ctx context.Context, sourceDir, targetDir string) error

    // Command execution
    Exec(ctx context.Context, command string, opts ExecOptions) (ExecResult, error)

    Workspace() string  // Return the workspace path (only valid for the "none" type)
    // RequiresProcessSandbox reports whether agents should enable their own process sandbox.
    RequiresProcessSandbox() bool
}
```

## File-Transfer Semantics

| Method | Direction | Description |
|------|------|------|
| `UploadFile` / `DownloadFile` | Single file | `sourcePath` is the source, `targetPath` is the destination |
| `UploadDir` / `DownloadDir` | Directory | Recursively transfers an entire directory tree |

- `Upload`: skill-up host environment → runtime environment
- `Download`: runtime environment → skill-up host environment

## Command Execution

```go
type ExecOptions struct {
    Cwd         string            // Working directory (relative to workspace or absolute)
    Env         map[string]string // Environment variables (only for this execution)
    TimeoutSec  int               // Timeout in seconds
    ArtifactDir string            // Host directory for the agent to write/download run artifacts
}

type ExecResult struct {
    Stdout   string  // Standard output
    Stderr   string  // Standard error
    ExitCode int     // Exit code
}
```

## Implementations

### NoneRuntime (Local Mode)

Uses the host environment directly with a temporary directory as the workspace:

- `Create`: creates `os.MkdirTemp("", "skill-up-*")`
- `Close`: deletes the temp directory (can be retained via `Config.Delete=false`)
- `Exec`: runs bash commands directly on the host
- `Workspace()`: returns the temp directory path
- `Upload` / `Download`: reads/writes the local file system

**Security assumption**: NoneRuntime executes commands directly on the host; `pathInWorkspaceOrAbs` allows access to any absolute path. Callers must ensure the paths they pass in are trusted and must not pass in untrusted user-controlled paths.

Suitable for local debugging and quick verification.

### OpenSandboxRuntime (Remote Sandbox Mode)

Connects to a remote sandbox environment:

- All file operations go over the network
- All commands execute in the remote environment
- `Workspace()` returns a remote path; there is no local access

Configuration includes `UseServerProxy`, `ReadyTimeout`, `SandboxTimeout`, `Kwargs`, etc. OpenSandbox authentication is handled inside the runtime implementation, which reads `OPENSANDBOX_API_KEY`; non-sensitive runtime parameters are passed through `Kwargs`, e.g. `base_url` and a JSON-string-encoded `extensions`.

### DockerRuntime (Local Container Mode)

Runs the eval inside a local Docker container — container-level isolation (filesystem, process, network) without any remote service dependency.

- `Create`: `docker create --name skill-up-<rand> --workdir <workspace> [--network none] [--env K=V]... <image> sleep infinity`. The `sleep infinity` entrypoint keeps the container alive so subsequent `exec` calls can attach; override via `environment.entrypoint`.
- `Start` / `Stop`: `docker start` / `docker stop --time 5`; both idempotent.
- `Close`: `docker rm -f` when `Config.Delete=true`; otherwise just `docker stop` so the user can inspect the container.
- `UploadFile` / `UploadDir` / `DownloadFile` / `DownloadDir`: implemented via `docker cp`. `UploadDir` and `DownloadDir` use the `src/.` trailing-dot form so the *contents* of `src` are copied into the destination (matching the existing `UploadDir`/`DownloadDir` contracts).
- `Exec`: `docker exec --workdir <cwd> [--env K=V]... <container> bash -c <command>`. `Cwd` may be relative (joined under `Workspace()`) or absolute. Caller-supplied env layers on top of `cfg.Env`, then on top of the container's own env (so `PATH`, `HOME`, etc. in the image remain intact).
- `RequiresProcessSandbox()`: returns `false` — the container already isolates the agent's processes.
- Workspace defaults to `/workspace` and must be an absolute path.

**Requirements**: a working `docker` CLI on PATH and a usable Docker daemon. The runtime does not pull images on its own — pre-pull or use an image already available locally.

**Network policy**:

| Policy | Behavior |
|-------|----------|
| (unset) | Default bridge network (full egress). |
| `deny_all` | `docker create --network=none`; no network access at all. |
| `allow_declared` | **Not supported yet** — rejected at validation and at `NewDockerRuntime`. FQDN-level egress filtering requires an egress proxy or in-container iptables sidecar that is out of scope for the initial implementation. Use `opensandbox` if you need this. |

**Security note**: `Workspace()` returns the in-container path. Unlike `NoneRuntime`, the host cannot access it directly — all data movement must go through Upload/Download. Absolute `targetPath` values in Upload* refer to absolute *container* paths, not host paths.

## Configuration

```go
type Config struct {
    Type           string            // "none" | "opensandbox" | "docker"
    Image          string            // Sandbox image (for opensandbox mode)
    WorkspaceMount string            // Workspace mount path
    Env            map[string]string // Environment variables
    SetupSteps     []SetupStep       // Initialization commands

    SandboxTemplate string           // Sandbox template name (opensandbox-specific)

    // OpenSandbox-specific
    UseServerProxy bool
    ReadyTimeout   time.Duration
    SandboxTimeout time.Duration
    Entrypoint     []string
    Metadata       map[string]string
    Kwargs         map[string]string

    // Common
    SkillPath string  // Skill files path
    Delete    bool    // Whether to delete the workspace
}
```

## MCP Configuration

The runtime may provide MCP server configuration:

```go
type MCPServerConfig struct {
    Name      string            // MCP Server name
    Mode      string            // real / mocked
    Transport string            // http / stdio
    Command   string            // stdio MCP launch command
    Args      []string          // stdio MCP launch arguments
    Endpoint  string            // HTTP MCP endpoint
    ConfigRef string            // Path reference to the original configuration
    Env       map[string]string // Env vars required to install and run the MCP server
    Headers   map[string]string // HTTP MCP headers
    HeaderEnv map[string]string // Mapping from header name to env-var name to avoid duplicate parsing
}

type MCPConfig struct {
    Servers []MCPServerConfig
}
```

These fields are populated by `internal/mcp.Provisioner` after parsing the eval configuration and the `config_ref` file, and are then passed into the runtime so that the agent's `InstallMCP` can install MCP servers using each CLI's configuration mechanism.

## File Permissions

| Constant | Value | Purpose |
|------|-----|------|
| `ClaudeDirMode` | `0o755` | Default permission for Claude file directories |
| `ClaudeFileMode` | `0o600` | Default permission for Claude files |
| `noneDirMode` | `0o755` | NoneRuntime directory permission |
| `noneFileMode` | `0o600` | NoneRuntime file permission |
