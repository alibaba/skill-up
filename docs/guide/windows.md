# Windows Support

skill-up runs natively on Windows. This page covers what works, the current
limitations, and the recommended workflow.

---

## Supported

- **Build and unit tests** — `go build ./...` and `go test ./...` pass on
  Windows. CI exercises a `windows-latest` runner alongside Linux.
- **The `none` runtime** — commands run on the host through `cmd.exe`.
- **The `opensandbox` runtime** — unaffected by the host OS; it can target a
  Linux sandbox or an explicitly configured Windows guest.
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

## OpenSandbox Windows guests

The `opensandbox` runtime talks to a remote OpenSandbox server over HTTP, so the
skill-up host and guest OS are independent. Configure a Windows guest with
`environment.platform`; when omitted, existing Linux behavior is unchanged.

```yaml
environment:
  type: opensandbox
  image: dockurr/windows:latest
  platform:
    os: windows
    arch: amd64
  resources:
    memory: 16Gi
  workspace_mount: C:/workspace
  ready_timeout_seconds: 1800
```

Windows defaults are `C:\workspace`, 4 CPU, 8 GiB memory, and 64 GiB disk.
Users may override any resource field without repeating the others. Both Linux
and Windows use the OpenSandbox directory API first and fall back to their
native shell only when the API cannot create or verify a writable directory.
Uploads, downloads, command working directories, script judges, and artifact
collection all use guest OS path semantics even when skill-up runs on another
OS.

The OpenSandbox server needs KVM, TUN, sufficient storage, and a Windows-capable
profile; see the upstream
[Windows Sandbox guide](https://github.com/opensandbox-group/OpenSandbox/blob/main/docs/guides/windows-sandbox.md).
Cold boot can take many minutes, so use a long `ready_timeout_seconds` and
persistent server-side storage where appropriate.

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
- **Built-in Agent CLI bootstrap in a Windows OpenSandbox guest** — the runtime
  lifecycle is supported, but automatic installation of built-in Agent CLIs is
  not. Preinstall the CLI in the guest image or configure a Custom Engine.
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
