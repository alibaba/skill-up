# Windows 支持

skill-up 原生支持 Windows。本页说明哪些功能可用、当前的限制，以及推荐的工作流。

---

## 已支持

- **构建与单元测试** —— `go build ./...` 和 `go test ./...` 在 Windows 上通过。
  CI 在 Linux 之外额外运行 `windows-latest` runner。
- **`none` runtime** —— 命令通过 `cmd.exe` 在宿主机上执行。
- **`opensandbox` runtime** —— 不受宿主机 OS 影响，始终在 Linux 沙箱内执行。
- **script judge** —— 按文件扩展名（或 shebang）分派解释器：

  | 脚本              | Windows 上的解释器                          |
  | ----------------- | ------------------------------------------- |
  | `.ps1`            | PowerShell                                  |
  | `.cmd` / `.bat`   | `cmd.exe`                                   |
  | `.sh`             | bash（Git Bash / WSL），见下文              |

## 在 Windows 上运行 `.sh` script judge

`.sh` script judge 需要一个 `bash` 解释器。skill-up 按以下顺序查找：

1. `SKILL_UP_BASH` 环境变量（指向 `bash.exe` 的明确路径）；
2. `PATH` 上的 `bash`；
3. 知名安装位置 —— `C:\Program Files\Git\bin\bash.exe` 以及 WSL 的 `bash.exe`。

若都找不到，script judge 会以明确的错误失败。请安装
[Git for Windows](https://git-scm.com/download/win) 或设置 `SKILL_UP_BASH`。

## 贡献者工具

Windows 默认没有 `make`。请改用 `scripts/windows/` 下的 PowerShell 脚本：

```powershell
# 安装 git hooks（等价于 `make hooks`）
pwsh scripts/windows/hooks.ps1

# 将固定版本的 lint 工具装入 .tools/bin（等价于 `make lint-tools`）
pwsh scripts/windows/lint-tools.ps1

# fmt-check + vet + revive + golangci-lint（等价于 `make verify`）
pwsh scripts/windows/verify.ps1
```

构建和测试使用标准的 Go 工具链，本身就是跨平台的：

```powershell
go build -o bin/skill-up.exe ./cmd/skill-up
go test -race ./...
```

## 已知限制

- **原生运行真实 agent** —— Claude Code / Codex / Qoder CLI 通过基于 bash 的
  Node/nvm 引导脚本启动，该脚本无法在 `cmd.exe` 下运行。要在 Windows 上运行
  完整的 agent 评测，请预先自行安装 Node.js 和对应的 agent CLI，或使用 WSL2。
- **`.ps1` script judge 需要 Windows 目标** —— 当 runtime 目标是 POSIX
  （例如 `opensandbox` 的 Linux 沙箱）时，仅支持 `.sh` 脚本。

## 推荐工作流

- **编写并运行 script-judge 评测** —— 原生 Windows 即可。优先使用 `.ps1`
  script judge，或安装 Git for Windows 以支持 `.sh`。
- **运行完整的 agent 评测** —— 使用 **WSL2**，让评测器与 agent CLI 共享同一个
  POSIX 环境，避免路径与凭据的摩擦。
