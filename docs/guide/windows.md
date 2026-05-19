# Windows Support

skill-up runs natively on Windows. This page covers what works, the current
limitations, and the recommended workflow.

---

## Supported

- **Build and unit tests** — `go build ./...` and `go test ./...` pass on
  Windows. CI exercises a `windows-latest` runner alongside Linux.
- **The `none` runtime** — commands run on the host through `cmd.exe`.
- **The `opensandbox` runtime** — unaffected by the host OS; it always
  executes inside a Linux sandbox.
- **The script judge** — dispatches by file extension (or shebang):

  | Script            | Interpreter on Windows                      |
  | ----------------- | ------------------------------------------- |
  | `.ps1`            | PowerShell                                  |
  | `.cmd` / `.bat`   | `cmd.exe`                                   |
  | `.sh`             | bash (Git Bash / WSL), see below            |

## Running `.sh` script judges on Windows

A `.sh` script judge needs a `bash` interpreter. skill-up looks for one in
this order:

1. the `SKILL_UP_BASH` environment variable (an explicit path to `bash.exe`);
2. `bash` on `PATH`;
3. well-known locations — `C:\Program Files\Git\bin\bash.exe` and the WSL
   `bash.exe`.

If none is found the script judge fails with a clear error. Install
[Git for Windows](https://git-scm.com/download/win) or set `SKILL_UP_BASH`.

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

## Recommended workflow

- **Authoring and running script-judge evals** — native Windows works well.
  Prefer `.ps1` script judges, or install Git for Windows for `.sh` support.
- **Running full agent evals** — use **WSL2**, so the evaluator and the agent
  CLIs share one POSIX environment and avoid path/credential friction.
