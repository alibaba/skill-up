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

## Windows 上的 OpenSandbox runtime

`opensandbox` runtime 通过 HTTP 与远程 OpenSandbox 服务器通信，不会启动任何
宿主机 shell。在原生 Windows 上运行 `skill-up.exe` 连接远程 sandbox 当前即可
工作：所有宿主机侧的路径处理都已通过 `filepath.ToSlash` 跨越「宿主机→sandbox」
边界，而 sandbox 本身是 Linux 容器，因此其中的 script judge 和 agent 行为与在
Linux 上完全一致。

OpenSandbox **不**提供 Windows 容器 sandbox —— 其 SDK 和执行 API（bash 会话、
POSIX UID/GID）仅支持 Linux。因此「Windows sandbox」指的是*由 Windows 宿主机
驱动一个 Linux sandbox*，这一场景是支持的。

如果某台 Windows 机器需要在**没有**远程 sandbox 的情况下使用完整的 agent
工作流，请在 **WSL2** 中运行 skill-up。WSL2 是 Linux 环境，因此 `none` 与
`opensandbox` 两种 runtime —— 包括 agent 的 Node/nvm 引导 —— 都能无限制工作。

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
