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
| Extended CI | `merge_group`、手动触发 | `E2E (none runtime, Windows)`、`E2E (docker runtime)`、`GoReleaser Check` | `E2E (none runtime)`、`E2E (opensandbox runtime)`、`E2E (docker runtime, full LLM)` |
| Docs | 文档相关的 `push` 和 `pull_request` | 不要设为全局必需；路径过滤会使非文档 PR 没有该检查 | `Build` |

模型检查依赖外部服务、凭据、额度和非确定性输出，因此保持可选。只有在可靠性经过量化，并确认 Merge Queue 能访问所需环境和 Secret 后，才可升级为必需检查。

所有提供必需检查的工作流都必须监听 `merge_group`，否则 Merge Queue 会永久等待一个不会创建的检查。重命名 Job 后，应先让新检查成功运行一次，再修改 Ruleset。

## Secrets 与 Environments

仓库管理员维护下列清单。负责人和轮换日期应记录在组织的密钥管理系统中，本文不记录 Secret 值。

| 名称 | 范围 | 使用方 | 轮换时机 |
| --- | --- | --- | --- |
| `DASHSCOPE_API_KEY` | 仓库 Actions Secret | Extended 模型 E2E、自评测 | 按供应商策略、维护者离职或疑似泄露时 |
| `QODER_ACCESS_TOKEN` | 仓库 Actions Secret | Qoder E2E | 按供应商策略、维护者离职或疑似泄露时 |

`release` Environment 控制 GitHub Release，`release-image` Environment 控制 GHCR 发布。两者都应配置审核者；团队人数允许时开启禁止自审，并把部署来源限制到预期 tag 或分支。Secret 只传给确实需要它的 Job。

## Runner 镜像发布

1. 审查 `action/` 下的变更，重点检查 Dockerfile 和下载的二进制文件。
2. 手动触发 **Runner Image**，批准 `release-image` 部署并等待构建与扫描完成。
3. 从工作流摘要复制已发布的不可变 `sha256:` digest。
4. 通过 Pull Request 把 `action.yml` 中的 Runner 镜像引用更新为该 digest。
5. 合并前运行复合 Action 冒烟测试和 Extended CI。

禁止静默移动 Action 使用的镜像标签；digest 变更必须可审查、可回滚。

## Release 发布

1. 确认发布 commit 位于 `main` 且 CI 全部通过。
2. 创建受 tag Ruleset 保护、已签名的 `v*` 语义化版本 tag。
3. 核对 tag 和变更日志后，批准 `release` Environment 部署。
4. 验证 GoReleaser 生成的 GitHub Release 资产与校验和。
5. 发布失败时修复后发布新版本，不要改写已经发布的 tag。

## 产物与事件响应

E2E 产物可能包含提示词、模型回复、文件路径和生成的工作区。保留期应尽可能短，禁止上传凭据；向维护者范围外分享前必须检查并脱敏。

疑似 Secret 泄露时，应禁用相关工作流、在供应商侧轮换凭据、更新 GitHub Secret、审查审计日志与工作流日志并使相关产物失效。疑似 Runner 被入侵时，应从 GitHub 移除 Runner、轮换主机可访问的凭据、使用干净镜像重建，并用新 Token 重新注册。

Runner 标签、Job 名称、必需检查、Environment 或 Secret 发生变化时，都应同步复审本手册。
