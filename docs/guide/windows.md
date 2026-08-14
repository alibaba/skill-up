# Windows Support

skill-up runs natively on Windows. This page covers what works, the current
limitations, and the recommended workflow.

---

## Supported

- **Build and unit tests** — `go build ./...` and `go test ./...` pass on
  Windows. Maintainer-dispatched Extended CI exercises a trusted self-hosted
  runner; pull-request and merge-group code remains on clean GitHub-hosted
  runners.
- **The `none` runtime** — commands run on the host through `cmd.exe`.
- **The `opensandbox` runtime** — unaffected by the host OS; it always
  executes inside a Linux sandbox.
- **The script judge** — dispatches by file extension (or shebang):

  | Script            | Interpreter on Windows                      |
  | ----------------- | ------------------------------------------- |
  | `.ps1`            | PowerShell                                  |
  | `.cmd` / `.bat`   | `cmd.exe`                                   |
  | `.sh`             | bash (Git Bash; see below)                  |

## Running `.sh` script judges on Windows

A `.sh` script judge needs a `bash` interpreter. skill-up looks for one in
this order:

1. the `SKILL_UP_BASH` environment variable (an explicit path to `bash.exe`);
2. `bash` on `PATH`;
3. well-known Git Bash install locations —
   `C:\Program Files\Git\bin\bash.exe` and
   `C:\Program Files (x86)\Git\bin\bash.exe`.

If none is found the script judge fails with a clear error. Install
[Git for Windows](https://git-scm.com/download/win) or set `SKILL_UP_BASH`
(see [Environment Variables](./user-config.md#skill_up_bash) for full details).

The WSL shim at `C:\Windows\System32\bash.exe` is intentionally rejected at
all three steps (override, PATH, well-known) because it expects Linux-format
`/mnt/c/...` paths and silently fails on the Windows-style paths skill-up
generates. Users who want to drive script judges through WSL must arrange
path translation upstream and point `SKILL_UP_BASH` at a non-WSL bash — or
simply run skill-up inside WSL itself (see "Recommended workflow" below).

## OpenSandbox runtime on Windows

The `opensandbox` runtime talks to a remote OpenSandbox server over HTTP and
never spawns a host shell. Running `skill-up.exe` on native Windows against a
remote sandbox works today: all host-side path handling already crosses the
host→sandbox boundary through `filepath.ToSlash`, and the sandbox itself is a
Linux container, so the script judge and any agent run inside it behave
exactly as they do on Linux.

OpenSandbox also offers a [**Windows guest profile**](https://github.com/alibaba/OpenSandbox/blob/main/docs/windows-sandbox.md):
the server runs `dockur/windows` (Windows in KVM/QEMU inside a Linux container)
and the API accepts `platform: {"os": "windows", "arch": "amd64"}` on create.
At the time of writing the Go SDK does not yet expose the `Platform` field, so
driving a Windows-guest sandbox from skill-up is blocked on an upstream Go SDK
update — tracked separately.

For a Windows machine that needs the full agent workflow **without** a remote
sandbox, run skill-up inside **WSL2**. WSL2 is a Linux environment, so both the
`none` and `opensandbox` runtimes — including the agent Node/nvm bootstrap —
work without limitation.

## Contributor tooling

`make` is not available on Windows by default. Use the PowerShell scripts
under `scripts/windows/` instead:

```powershell
# Install git hooks (equivalent to `make hooks`)
pwsh scripts/windows/hooks.ps1

# Install pinned lint tools into .tools/bin (equivalent to `make lint-tools`)
pwsh scripts/windows/lint-tools.ps1

# fmt-check + vet + revive + golangci-lint (equivalent to `make verify`)
pwsh scripts/windows/verify.ps1
```

Build and test use the standard Go toolchain, which is cross-platform:

```powershell
go build -o bin/skill-up.exe ./cmd/skill-up
go test -race ./...
```

## Known limitations

- **Running real agents natively** — Claude Code / Codex / Qoder CLI are
  launched through a bash-based Node/nvm bootstrap. That bootstrap does not
  run under `cmd.exe`. To run full agent evals on Windows, either install
  Node.js and the agent CLIs yourself beforehand, or use WSL2.
- **`.ps1` script judges require a Windows target** — when the runtime target
  is POSIX (for example the `opensandbox` Linux sandbox), only `.sh` scripts
  are supported.
- **`cmd.exe` expands `%VAR%` inside arguments** — when no bash is discovered
  and the `cmd /d /s /c` fallback shell runs, literal `%NAME%` substrings
  inside command arguments are still expanded by cmd. There is no reliable
  command-line escape for this. Do not interpolate untrusted strings into
  shell commands. Install Git Bash (which skill-up auto-discovers) to avoid
  the cmd fallback entirely.

## Recommended workflow

- **Authoring and running script-judge evals** — native Windows works well.
  Prefer `.ps1` script judges, or install Git for Windows for `.sh` support.
- **Running full agent evals** — use **WSL2**, so the evaluator and the agent
  CLIs share one POSIX environment and avoid path/credential friction.
