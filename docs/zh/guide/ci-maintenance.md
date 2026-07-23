# CI 维护手册

本手册用于保持仓库设置与工作流文件一致。禁止在本文中记录 Secret 值、Token、Runner 注册 Token 或任何凭据。

## 信任边界

Pull Request 代码只在 GitHub 托管 Runner 上运行。持久化的自托管 Runner 仅用于可信的 `push`、`merge_group`、`workflow_dispatch` 和可复用工作流调用。

| 能力 | 必需标签 | 用途 |
| --- | --- | --- |
| 可信 Linux | `self-hosted`、`linux`、`x64`、`trusted` | 可信集成测试和模型测试 |
| 支持 Docker 的可信 Linux | `self-hosted`、`linux`、`x64`、`docker`、`trusted` | 可信容器 E2E 测试 |
| 不可信 PR 校验 | `ubuntu-24.04` 或 `windows-2025` | 构建、Lint、冒烟测试、文档和 CodeQL |

不要给会检出或执行 PR 代码的工作流添加 `pull_request_target`，不要让 PR Job 使用自托管 Runner。Runner 主机是持久化环境，应定期打补丁、移除无用软件和凭据、限制 Docker 权限；怀疑被入侵后必须重建。

## 稳定检查与 Merge Queue

Ruleset 必须使用下表的 Job 展示名称。`build` 等 Job ID 只是实现细节，不能当作 required check 名称。

| 工作流 | 事件 | 必需检查 | 可选检查 |
| --- | --- | --- | --- |
| CI | `push`、`pull_request`、`merge_group` | `Build & Test`、`E2E Smoke`、`Lint` | — |
| CodeQL | `push`、`pull_request`、`merge_group`、定时任务 | `Analyze (actions)`、`Analyze (go)`、`Analyze (python)` | — |
| Extended CI | `merge_group`、手动触发 | `Extended CI Summary` | 其余组件 Job 由 Summary 汇总 |
| Model E2E | 手动触发 | 不得设为必需检查 | `E2E (none runtime, live models)`、`E2E (OpenSandbox, live model)`、`E2E (Docker runtime, live model)` |
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

## Release 发布

1. 确认发布 commit 位于 `main` 且 CI 全部通过。
2. 创建受 tag Ruleset 保护、已签名的 `v*` 语义化版本 tag。
3. 等待无发布权限的构建 Job 生成 GoReleaser 文件，并上传保留一天的候选发布 Artifact。
4. 检查 tag、commit、变更日志和构建摘要后，批准 `release` Environment 部署。
5. 验证发布 Job 对同一批候选归档和校验和生成了 Attestation 并完成上传，期间没有重新构建。
6. 发布失败时修复后发布新版本，不要改写已经发布的 tag。

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
