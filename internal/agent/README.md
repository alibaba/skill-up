# Agent

The Agent module is responsible for installing and running Agent Engines (qoder-cli, claude-code, etc.).

## Core Concepts

### Output File Naming

| Agent | Runtime output file | Description |
|-------|---------------------|-------------|
| Claude Code | `outputs/stdout.json` | Standard output, JSONL format |
| QoderCLI | `outputs/stdout.txt` | Standard output, plain text format |

When the agent runs, `tee` writes stdout directly into the runtime's `outputs/` directory.

### Session Files and Parsing (Claude Code and QoderCLI)

The session-trace JSONL files written by **Claude Code** and **QoderCLI** share the **same line structure**: a top-level `type` (`user` / `assistant` / `tool_*` / `result`, etc.), a nested `message` (whose `content` may be an array of blocks; `usage` lives under `message`). Extra fields in Qoder lines such as `uuid`, `sessionId`, `cwd` are ignored during deserialization.

Both are parsed by **`parseSessionFile`** in `internal/agent/claude_code.go` (`claudeEvent`); after `qodercli.go` downloads the session file, it calls the same parser directly — there is **no** separate Qoder JSON parsing branch.

#### Claude Code

- **Session ID**: each `Run` generates a UUID and passes it to `claude --session-id <uuid> ...`; standard logs print `[INFO] Session ID: ...`.
- **On-disk trace file**: written by Claude Code under the user's home directory, typically at `~/.claude/projects/.../<session-id>.jsonl`.
- **Lookup** (`findClaudeSessionFile`): runs a script in the runtime that scans `~/.claude/projects/<workspaceKey>/*.jsonl`, picks the **most recently modified** path by mtime, and returns its **absolute path**; `workspaceKey` is passed via the `SKILL_UP_CLAUDE_WSKEY` environment variable. If nothing is found, an empty string is returned and the caller falls back to plain-text stdout parsing.
- **JSONL line semantics (session file)**: each JSON line maps to a `claudeEvent`: `user` / `assistant` (with nested `message`, optional `usage`), `tool_call`, `tool_result`, `result` (with `final_message`). `parseSessionFile` produces the `Transcript` and the final message text in a single scan.
- **Meaning of `message.usage` (tokens) on disk**: in real sessions, the `input_tokens` / `output_tokens` (and common `cache_read_input_tokens` etc.) on `assistant` lines are usually **cumulative meters** at the time of that line, not per-line increments. A common pattern in a single streamed turn is **the same `input_tokens` repeated across multiple lines** with **`output_tokens` growing line by line**; `input_tokens` jumps again as the context grows. Branches/side-chains can cause the meters to **reset midway**. Therefore, **do not** simply add up `usage` values from each `assistant` line as if they were disjoint increments.
- **Tokens returned by `parseSessionFile`**: for each `assistant` line's `usage`, it composes the "input side" reading as **`input_tokens` + `cache_read_input_tokens` + `cache_creation_input_tokens`**, then takes the **max** across the whole file; `output_tokens` is also taken as **max** (high-water mark). This avoids summing repeated cumulative meters; with prompt caching, the input often goes into the cache fields rather than `input_tokens`.
- **Artifacts**: Codex JSONL is captured outside the evaluated workspace while the agent runs, then persisted to the prepared output directory (or `.skill-up/stdout.json` after the run when no output directory is provided). Session paths are appended as runtime artifacts when found so the eval side can archive them.
- **Tokens at run time**: `buildSessionResult` first tries to extract the full transcript and token usage (high-water `max` aggregation) from the **session JSONL file** (`parseSessionFile`); if the session file is unavailable, it falls back to building a minimal transcript from plain-text stdout (no token data in this case).

#### QoderCLI

- **Session-to-workspace mapping**: take `rt.Workspace()`, run it through `filepath.EvalSymlinks`, replace every `/` in the path with `-` to get `workspaceKey`, which corresponds to Qoder's project directory at `$HOME/.qoder/projects/<workspaceKey>/`.
- **Environment isolation**: the skill-up process is isolated from the runtime (see `internal/runtime/README.md`), so we **must not** use `os.Getenv("HOME")` or `os.Stat` in Go to scan Qoder directories. `findQoderSessionFile` runs a script **inside the runtime** via `rt.Exec`: it uses `printenv HOME` to obtain `$HOME` in that environment, and passes `workspaceKey` through **`ExecOptions.Env`** (`SKILL_UP_QODER_WSKEY`) instead of concatenating it into the shell string.
- **On-disk trace file**: `*.jsonl` files in the directory above, **excluding** any whose name matches `*-session.json`.
- **Lookup** (`findQoderSessionFile`): inside the runtime, `find` writes candidate paths into a temp file that is then read line by line (avoiding both the issue of `read` failing when no file matches under `set -e`, and the lack of process substitution in minimal environments); `stat` is used to get mtime (GNU `stat -c %Y` / BSD `stat -f %m`), and the **most recently modified** path is selected; an empty string is returned when nothing is found.
- **JSONL line semantics (session file)**: same as Claude Code; uses **`parseSessionFile`**.
- **Current state of `message.usage` (tokens) in Qoder on-disk files**: sampling `~/.qoder/projects/**/*.jsonl` shows that although `assistant` lines do contain a `message.usage` structure, **`input_tokens` / `output_tokens` / `cache_read_input_tokens` / `cache_creation_input_tokens` are commonly all 0** (i.e. the client does not write real usage into the local session). As a result, **tokens parsed out of the session for `SessionResult` are usually 0 on the Qoder side**; this is independent of the parsing implementation and depends on whether Qoder later starts writing nonzero `usage` into the session. Qoder's **`stdout.txt` is plain text**, unlike Claude's stream-json, so tokens cannot be merged in from stdout either.
- **Concurrency limit**: QoderCLI currently uses a fixed `/tmp/qodercli-natives-<version>-<platform>` native directory at runtime. When several `qodercli` processes start concurrently within the same runtime environment, they may race to extract/rename the natives directory; if a root-owned remnant of that directory is left in the image, this directly triggers `EPERM`. `skill-up` does **not** silently add a lock at the agent layer, because doing so would implicitly invalidate user-configured per-case parallelism for qodercli. To run qodercli evaluations stably, set `cases.parallelism` to `1` in the eval config, or fix the image / upstream qodercli's native-directory isolation first.
- **Artifacts**: `GeneratedFiles` includes the workspace `stdout.txt`; when a session is found, its **absolute path** is appended. If no session is parsed, a minimal `Transcript` can still be constructed from the plain-text `stdout.txt`.

## Architecture

```
internal/agent/
├── agent.go           # Core interface definitions: Agent, SessionResult, BaseAgent
├── factory.go         # DetectAgent / DetectAgentWithInitParams factory functions
├── claude_code.go     # ClaudeCodeAgent implementation
├── qodercli.go        # QoderCLIAgent implementation
├── codex.go           # CodexAgent implementation (OpenAI Codex CLI)
├── qwen_code.go       # QwenCodeAgent implementation (Qwen Code CLI, OpenAI-compatible)
├── cli.go             # CLIAgent base implementation (for generic CLI tools)
├── mcp.go             # MCP server installation helpers
├── node_install.go    # Node.js / npm install helpers
├── session_lookup.go  # Session file lookup utilities
├── skill.go           # Skill installation helpers
├── tool_status.go     # Tool call status parsing helpers
├── errors.go          # Agent error types
└── *_test.go          # Unit tests
```

### Node.js / npm mirrors

`node_install.go` bootstraps Node.js via `nvm` inside the runtime. It defaults to the **official** endpoints and honors standard environment variables, so users can override them without touching the code:

| Variable | Default | Purpose |
|----------|---------|---------|
| `NVM_NODEJS_ORG_MIRROR` | `https://nodejs.org/dist` | Node.js tarball mirror used by `nvm install` |
| `npm_config_registry` / `NPM_CONFIG_REGISTRY` | `https://registry.npmjs.org` | npm package registry |
| `NVM_SOURCE` | `https://github.com/nvm-sh/nvm.git` | Git source used by `nvm` itself |
| `NVM_DIR` | `$HOME/.nvm` | Installation directory; `$NVM_DIR/nvm.sh` is sourced when present |

For mainland China users with limited access to the official endpoints, export the mirrored values before running `skill-up`, e.g.:

```bash
export NVM_SOURCE=https://gitee.com/mirrors/nvm.git
export NVM_NODEJS_ORG_MIRROR=https://mirrors.aliyun.com/nodejs-release
export NPM_CONFIG_REGISTRY=https://registry.npmmirror.com
```

The bootstrap is invoked from `ensureNodeRuntime`, which runs as its **own** `rt.Exec` call before the agent invocation (or MCP install). The script's first line short-circuits with `exit 0` when the CLI binary (`claude`, `codex`, `qwen`) is already on `PATH`, so the happy-path cost is one `command -v` check. Splitting the bootstrap into a separate Exec keeps the agent run's stdout/stderr clean of nvm/curl noise and makes bootstrap failures distinguishable in errors (`node bootstrap failed: ...`) and traces.

## Agent Interface

```go
type Agent interface {
    Name() string
    Install(ctx context.Context, rt Runtime) error
    InstallMCP(ctx context.Context, rt Runtime, mcpCfg runtime.MCPConfig) error
    InstallSkill(ctx context.Context, rt Runtime, skillCfg runtime.SkillConfig) error
    Run(ctx context.Context, rt Runtime, opts ExecOptions, messages []transcript.Message) (*SessionResult, error)
    Check(ctx context.Context, rt Runtime) error
    CheckCredentials(ctx context.Context) error
}
```

## SessionResult Structure

```go
type SessionResult struct {
    Engine       string
    Model        string
    ExitCode     int
    DurationMs   int64
    Turns        int
    InputTokens  int
    OutputTokens int
    FinalMessage string
    Stderr       string
    Transcript   transcript.Transcript
    Artifacts    *SessionArtifacts
}
```

### SessionArtifacts

`SessionArtifacts` stores additional information such as workspace diffs, generated files, and logs (reserved fields):

```go
type SessionArtifacts struct {
    WorkspaceDiff  string   `json:"workspace_diff,omitempty"`
    GeneratedFiles []string `json:"generated_files,omitempty"`
    Logs           string   `json:"logs,omitempty"`
}
```

#### Codex

- **Session output**: `outputs/stdout.json` (JSONL stream from `codex --json`).
- **Supported transports**: MCP servers with `stdio` and `http` transports; HTTP MCP supports header-based authentication.
- **Token aggregation**: same high-water mark strategy as Claude Code — max of input (including cached) and max of output tokens across all events.
- **Model override**: passes `--model` flag; warns if a custom provider requires `base_url` configuration.
- **Event parsing**: handles `thread.started`, `turn.started/completed`, `item.started/completed` event types.

## MCP Installation

Agents support installing MCP servers via `InstallMCP(ctx, rt, mcpCfg)`; the server configuration is obtained from the `runtime.MCPConfig` passed in by the evaluator.
