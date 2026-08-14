# CI 维护手册

本手册用于保持仓库设置与工作流文件一致。禁止在本文中记录 Secret 值、Token、Runner 注册 Token 或任何凭据。

## 信任边界

Pull Request 代码只在 GitHub 托管 Runner 上运行。持久化的自托管 Runner 仅用于可信的 `push`、`merge_group`、`workflow_dispatch` 和可复用工作流调用。

| 能力 | 必需标签 | 用途 |
| --- | --- | --- |
| 可信 Linux | `self-hosted`、`linux`、`x64`、`trusted` | 可信集成测试和模型测试 |
| 支持 Docker 的可信 Linux | `self-hosted`、`linux`、`x64`、`docker`、`trusted` | 可信容器 E2E 测试 |
| 可信 Windows | `self-hosted`、`Windows`、`X64`、`trusted` | 手动触发的 Windows E2E 与模型测试 |
| 不可信 PR 与 Merge Group 校验 | `ubuntu-24.04` 或 `windows-2025` | 构建、Lint、冒烟测试、文档、CodeQL 与 Merge Group Windows E2E |

不要给会检出或执行 PR 代码的工作流添加 `pull_request_target`，不要让 PR Job 使用自托管 Runner。Runner 主机是持久化环境，应定期打补丁、移除无用软件和凭据、限制 Docker 权限；怀疑被入侵后必须重建。

可信 Windows Runner 必须安装 Git for Windows，并将
`C:\Program Files\Git\cmd` 与 `C:\Program Files\Git\bin` 都加入机器级
`PATH`；修改 `PATH` 后需重启 Runner 服务。工作流通过固定版本的 setup
action 安装所需 Go 与 Node.js。Runner 服务账号、工作目录、工具缓存和 agent
HOME 都会持久化，不能把它当作每次全新的 GitHub 托管环境。
Windows 还必须开启开发者模式。对于 `NETWORK SERVICE` 等非管理员 Runner
服务账号，还需授予 `SeCreateSymbolicLinkPrivilege`，并在修改本地安全策略后
重启 Runner 服务，使新登录令牌带上该权限。workflow 会在安装 Go 工具链之前
实际创建符号链接来验证这项能力。

Merge Group 代码不得与后续会接收模型凭据的持久 Windows Runner 共用环境。
因此 Extended CI 的 `merge_group` 使用 `windows-2025`，只有维护者
`workflow_dispatch` 才会选中可信自托管标签。

## 稳定检查与 Merge Queue

Ruleset 必须使用下表的 Job 展示名称。`build` 等 Job ID 只是实现细节，不能当作 required check 名称。

| 工作流 | 事件 | 必需检查 | 可选检查 |
| --- | --- | --- | --- |
| CI | `push`、`pull_request`、`merge_group` | `Build & Test`、`E2E Smoke`、`Lint` | — |
| CodeQL | `push`、`pull_request`、`merge_group`、定时任务 | `Analyze (actions)`、`Analyze (go)`、`Analyze (python)` | — |
| Extended CI | `merge_group`、手动触发 | `Extended CI Summary` | 其余组件 Job 由 Summary 汇总 |
| Model E2E | 手动触发 | 不得设为必需检查 | `E2E (none runtime, live models)`、`E2E (OpenSandbox, live model)`、`E2E (none runtime, Windows, live Claude)`、`E2E (Docker runtime, live model)` |
| Docs | 文档相关的 `push` 和 `pull_request` | 不要设为全局必需；路径过滤会使非文档 PR 没有该检查 | `Build` |
| Workflow Security | 工作流相关的 `push`、`pull_request`、`merge_group` | 初始基线完成处置前保持可选 | `Zizmor` |

模型检查依赖外部服务、凭据、额度和非确定性输出，因此 `Model E2E` 仅允许手动触发且保持可选；触发时可以运行全部套件或只选择一个 runtime。只有在可靠性经过量化，并确认 Merge Queue 能访问所需环境和 Secret 后，才可升级为必需检查。

所有提供必需检查的工作流都必须监听 `merge_group`，否则 Merge Queue 会永久等待一个不会创建的检查。重命名 Job 后，应先让新检查成功运行一次，再修改 Ruleset。

所有工作流必须在顶层设置 `permissions: {}`，并由各 Job 单独申请权限。所有 `actions/checkout` 步骤必须设置 `persist-credentials: false`。仓库 Actions 策略应强制完整 commit SHA，并只允许当前工作流实际使用的 Action 仓库。

Release 工作流还需要 `actions/download-artifact` 和 `actions/attest`。如果仓库使用受限 Action 白名单，必须在第一次 tag 发布前允许这两个官方仓库；工作流中的引用仍必须固定到完整 commit SHA。

Zizmor 在 Model E2E 中报告的三个 `adhoc-packages` 属于已接受的低风险例外：这些固定版本的全局 CLI 正是测试对象，不是应用依赖。任何 High 或 Medium 级别的 Zizmor 发现都必须在合并前修复或完成显式评审。

## Secrets 与 Environments

仓库管理员维护下列清单。负责人和轮换日期应记录在组织的密钥管理系统中，本文不记录 Secret 值。

| 名称 | 范围 | 使用方 | 轮换时机 |
| --- | --- | --- | --- |
| `DASHSCOPE_API_KEY` | 仓库 Actions Secret | Model E2E、自评测 | 按供应商策略、维护者离职或疑似泄露时 |
| `QODER_ACCESS_TOKEN` | 仓库 Actions Secret | Qoder E2E | 按供应商策略、维护者离职或疑似泄露时 |

`release` Environment 控制“发布已构建 GoReleaser 制品”的 Job，`release-image` Environment 控制“把已构建镜像 digest 提升为用户可见 GHCR tag”的 Job。编译发生在审批前且只有源码只读权限；审批不会触发重新构建。两者都应配置审核者；团队人数允许时开启禁止自审，并把部署来源限制到预期 tag 或分支。Secret 只传给确实需要它的 Job。

## Runner 镜像发布

1. 审查 `action/` 下的变更，重点检查 Dockerfile 和下载的二进制文件。
2. 手动触发 **Runner Image**，检查构建 Job 输出的不可变 digest。
3. 批准 `release-image` 部署；发布 Job 只把该 digest 提升为目标 tag，不会重新构建。
4. 从工作流摘要复制已发布的不可变 `sha256:` digest。
5. 通过 Pull Request 把 `action.yml` 中的 Runner 镜像引用更新为该 digest。
6. 合并前运行复合 Action 冒烟测试和 Extended CI。

构建 Job 会推送 `build-<run-id>-<attempt>` 形式的流水线暂存 tag，使受保护发布 Job 能够提升同一个 Registry 对象；应按 Package 保留策略定期清理不再引用的暂存版本。禁止静默移动 Action 使用的镜像标签；digest 变更必须可审查、可回滚。可复用工作流的调用方必须消费 digest 输出，不得依赖可变 tag。

## 人工同步 skill-up 版本

官方 Action 镜像不会跟踪 `latest`。CLI Release 和 Action 镜像刷新是两个独立操作；只发布 CLI Release 不会改变现有 Action 镜像。

镜像构建从 `action/Dockerfile` 的 `ARG SKILL_UP_VERSION` 读取 CLI 版本；手动触发 **Runner Image** 时填写的 `tag` 只控制 GHCR tag，不会选择 CLI 版本。

发布新的 CLI Release（例如 `v0.8.0`）后，按照以下流程操作：

1. 确认 GitHub Release 已存在，并包含预期归档和校验和文件。镜像安装器会下载这些 Release 资产，因此资产存在之前不能构建该版本镜像。
2. 创建版本同步 Pull Request，把以下三个默认值统一更新为不带 `v` 前缀的版本：
   - `action/Dockerfile`：`ARG SKILL_UP_VERSION=0.8.0`
   - `action.yml`：`skill-up-version` fallback 默认值
   - `action/main.py`：`--skill-up-version` fallback 默认值
3. 如果 `install.sh` 内容发生变化，还要同步更新 `action/Dockerfile` 和 `action/main.py` 中固定的安装脚本 commit 与 SHA-256；安装脚本内容没有变化时不要无意义更新。
4. 运行 CI、CodeQL、Workflow Security、Dockerfile 评审和相关确定性 E2E，合并版本同步 PR 到 `main`。
5. 从 `main` 手动触发 **Runner Image**：
   - `tag` 填写目标镜像 tag，通常与 CLI Release 一致，例如 `v0.8.0`；
   - 除非维护者明确需要便利 tag，否则保持 `publish_latest` 关闭；
   - 两个输入都不会改变 `SKILL_UP_VERSION`。
6. 检查构建日志，必须确认 `skill-up --version` 精确输出 `0.8.0`；不一致时拒绝 `release-image` 部署。
7. 检查不可变构建 digest，批准 `release-image` Environment；确认发布 Job 提升的是同一 digest，期间没有重新构建。
8. 复制已发布 digest，创建第二个 Pull Request，更新 `action.yml` 中的 `image: docker://...@sha256:...` 引用及其版本注释。
9. 使用该 digest 运行复合 Action 冒烟测试、**Extended CI** 和 **Skill Upper Self-Eval**；预期引擎全部完成，或经过评审的 waiver 有明确记录后，才能合并。
10. 确认合并后的 `action.yml`、GHCR digest、镜像 label 和 `skill-up --version` 指向同一个版本。

官方镜像已经包含 skill-up，因此 `action/main.py` 会跳过安装，`skill-up-version` Action input 不能覆盖官方镜像内版本。该 input 只用于没有预装 skill-up 的自定义镜像。不得指导用户通过它选择更新的官方 CLI 版本。

仓库当前不会在镜像 digest PR 合并后自动发布独立、不可变的 Action Release tag。在该机制建立前：

- `@main` 跟踪最新合并的 Action 镜像；
- digest 更新后的 commit SHA 是稳定、不可变的 Action 引用；
- 禁止移动已经发布的 CLI tag 来包含后续 digest；
- CLI Release tag 可能包含创建 tag 时已有的 Action 镜像，不保证镜像 CLI 版本与 tag 名称一致。

### Runner 镜像发布检查清单

- [ ] CLI GitHub Release 和校验和文件已经存在。
- [ ] 三处 skill-up 版本默认值保持一致。
- [ ] 安装脚本 commit 与 checksum pin 对应预期 `install.sh` 内容。
- [ ] 镜像构建日志中的 `skill-up --version` 符合预期。
- [ ] Agent CLI 版本与新版 skill-up 兼容。
- [ ] SBOM 和 provenance 生成成功。
- [ ] Self-Eval 消费不可变 digest，而不是 tag。
- [ ] `action.yml` 已更新为验证通过的 digest。
- [ ] 生产 `action.yml` 未引用 `latest`。
- [ ] 镜像版本、GHCR digest、Action 注释和维护记录一致。

### 回滚

禁止重新构建或移动旧 digest。需要回滚时，通过 Pull Request 恢复 `action.yml` 上一个已知正常的 digest，重新运行 Action 冒烟测试并按正常 Ruleset 合并。如果 CLI Release 本身有问题，应发布新的 patch 版本并重新执行同步流程，不得改写已发布 CLI tag。

## Release 发布

1. 确认发布 commit 位于 `main` 且 CI 全部通过。
2. 创建受 tag Ruleset 保护、已签名的 `v*` 语义化版本 tag。
3. 等待无发布权限的构建 Job 生成 GoReleaser 文件，并上传保留一天的候选发布 Artifact。
4. 检查 tag、commit、变更日志和构建摘要后，批准 `release` Environment 部署。
5. 验证发布 Job 对同一批候选归档和校验和生成了 Attestation 并完成上传，期间没有重新构建。
6. 发布失败时修复后发布新版本，不要改写已经发布的 tag。

CLI Release 成功后，继续执行“人工同步 skill-up 版本”。只有 Runner Image digest 更新完成测试并合并后，才能认为该 CLI 版本已经可通过官方 GitHub Action 使用。

## 缓存策略

- Go Module 缓存只用于确定性校验 Job。Pull Request 缓存应视为不可信的性能提示：Job 仍需验证并编译全部输入，且不能获得 Secret 或写 Token。
- GoReleaser 快照和正式发布构建禁用 Actions 缓存，避免发布结果消费其他 Job 可写入的缓存条目。
- Runner 镜像构建不使用 GitHub Actions Cache 后端；Registry 写入开始后，不能让缓存服务故障导致发布失败。
- 缓存是可丢弃数据，不得承载报告、发布文件、凭据或后续 Job 必需的状态。跨 Job 传递必须使用 Artifact，并明确设置保留期。
- 修改缓存 key、依赖管理器、锁文件或信任边界时，必须先复审缓存恢复来源与写权限。

## 产物与事件响应

E2E 产物可能包含提示词、模型回复、文件路径和生成的工作区。保留期应尽可能短，禁止上传凭据；向维护者范围外分享前必须检查并脱敏。

疑似 Secret 泄露时，应禁用相关工作流、在供应商侧轮换凭据、更新 GitHub Secret、审查审计日志与工作流日志并使相关产物失效。疑似 Runner 被入侵时，应从 GitHub 移除 Runner、轮换主机可访问的凭据、使用干净镜像重建，并用新 Token 重新注册。

Runner 标签、Job 名称、必需检查、Environment 或 Secret 发生变化时，都应同步复审本手册。
