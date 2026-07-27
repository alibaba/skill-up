# Windows 支持

skill-up 原生支持 Windows。本页说明哪些功能可用、当前的限制，以及推荐的工作流。

---

## 已支持

- **构建与单元测试** —— `go build ./...` 和 `go test ./...` 在 Windows 上通过。
  CI 在 Linux 之外额外运行 `windows-latest` runner。
- **`none` runtime** —— 命令通过 `cmd.exe` 在宿主机上执行。
- **`opensandbox` runtime** —— 不受宿主机 OS 影响，可选择 Linux sandbox，
  也可显式配置 Windows guest。
- **script judge** —— 按文件扩展名（或 shebang）分派解释器：

  | 脚本              | Windows 上的解释器                          |
  | ----------------- | ------------------------------------------- |
  | `.ps1`            | PowerShell                                  |
  | `.cmd` / `.bat`   | `cmd.exe`                                   |
  | `.sh`             | bash（Git Bash，见下文）                    |

## 在 Windows 上运行 `.sh` script judge

`.sh` script judge 需要一个 `bash` 解释器。skill-up 按以下顺序查找：

1. `SKILL_UP_BASH` 环境变量（指向 `bash.exe` 的明确路径）；
2. `PATH` 上的 `bash`；
3. 知名 Git Bash 安装位置 —— `C:\Program Files\Git\bin\bash.exe` 与
   `C:\Program Files (x86)\Git\bin\bash.exe`。

若都找不到，script judge 会以明确的错误失败。请安装
[Git for Windows](https://git-scm.com/download/win) 或设置 `SKILL_UP_BASH`
（完整说明见[环境变量](./user-config.md#skill_up_bash)）。

`C:\Windows\System32\bash.exe`（WSL shim）会在三个步骤里都被主动忽略 ——
即使通过 `SKILL_UP_BASH` 显式指向或它在 PATH 上排在前面，因为它期望 Linux
风格的 `/mnt/c/...` 路径，而 skill-up 传入的是 Windows 路径，会静默失败。
需要走 WSL 的用户请自行处理路径翻译并把 `SKILL_UP_BASH` 指向非 WSL 的 bash，
或者直接在 WSL 内运行 skill-up（见下文「推荐工作流」）。

## OpenSandbox Windows guest

`opensandbox` runtime 通过 HTTP 与远程 OpenSandbox 服务通信，因此 skill-up
宿主机与 guest OS 相互独立。通过 `environment.platform` 选择 Windows；省略时
保持原有 Linux 行为。

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

Windows 默认使用 `C:\workspace`、4 CPU、8 GiB 内存和 64 GiB 磁盘；用户可只覆盖
需要调整的资源字段。Linux 与 Windows 都优先调用 OpenSandbox 目录 API，只有
API 无法创建或验证可写目录时才回退到各自的原生 shell。上传、下载、命令工作
目录、script judge 和 artifact 收集均按 guest OS 的路径规则处理，即使 skill-up
运行在另一种 OS 上也一致。

OpenSandbox 服务端需要 KVM、TUN、足够的存储空间以及支持 Windows 的 profile；
详见上游 [Windows Sandbox 指南](https://github.com/opensandbox-group/OpenSandbox/blob/main/docs/guides/windows-sandbox.md)。
Windows 冷启动可能耗时数分钟，应配置较长的 `ready_timeout_seconds`，必要时在
服务端启用持久存储。

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
- **Windows OpenSandbox guest 中的内置 Agent CLI 引导** —— runtime 生命周期
  已支持，但尚不会自动安装内置 Agent CLI。请将 CLI 预装进 guest 镜像，或配置
  Custom Engine。
- **`.ps1` script judge 需要 Windows 目标** —— 当 runtime 目标是 POSIX
  （例如 `opensandbox` 的 Linux 沙箱）时，仅支持 `.sh` 脚本。
- **`cmd.exe` 会展开参数里的 `%VAR%`** —— 当宿主未发现 bash、回退到
  `cmd /d /s /c` 时，参数中的 `%NAME%` 子串仍会被 cmd 展开，命令行层面没有
  可靠的转义办法。不要把不可信字符串拼接到 shell 命令中。安装 Git Bash
  （skill-up 会自动发现）可完全避开此回退路径。

## 推荐工作流

- **编写并运行 script-judge 评测** —— 原生 Windows 即可。优先使用 `.ps1`
  script judge，或安装 Git for Windows 以支持 `.sh`。
- **运行完整的 agent 评测** —— 使用 **WSL2**，让评测器与 agent CLI 共享同一个
  POSIX 环境，避免路径与凭据的摩擦。
