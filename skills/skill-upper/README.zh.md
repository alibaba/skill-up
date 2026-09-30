# skill-upper

一个帮助你使用 `skill-up` CLI 评测并持续演进其他 Agent Skill 的 Agent Skill。

## 功能概述

`skill-upper` 引导你完成从评测到演进的闭环：

- **定位** 目标 Skill，理解其能力边界
- **搭建** `evals/eval.yaml` 和 `evals/cases/*.yaml` 脚手架，选择合适的 judge 类型
- **模拟** 依赖 Agent 回复的用户轮次，支持固定轮次混排或自主对话，并单独配置模拟模型
- **校验** 配置，在运行前发现 schema 错误
- **运行** 评测，调用真实 Agent Engine（Claude Code、Codex、qodercli 等）
- **诊断** 结构化报告和输出证据中的失败原因
- **采集** 通过 Codex 或 DSH observer 集成记录明确归因的 Skill 使用、证据和反馈
- **审核** 本地采集的 Skill 观察记录，将已批准的反馈转成回归用例
- **演进** 修复目标 Skill 或增强 eval 覆盖，然后重新运行评测

观察采集与审核支持 Codex 和已显式启用 observer 的 DSH。Claude Code、
qodercli、Qwen Code 等其他 Agent Engine 仍可用于普通 `skill-up` 评测，
但暂不支持观察工作流。

## 使用场景

- 需要对某个 Skill 进行评测、测试或回归验证
- 需要根据评测失败修复并持续迭代某个 Skill
- 需要采集明确归因的 Skill 使用或反馈
- 需要审核观察记录，或将已批准的真实反馈转成回归用例
- 需要编写 `eval.yaml` / `case.yaml` 或选择 judge 类型
- 运行 `skill-up run/validate/list-cases/report/import/init`
- 从 Anthropic `evals.json` 迁移到 skill-up 格式

## 用户模拟器配置生成回归

`evals/eval-dashscope.yaml` 使用本地 OpenCode 和 DashScope `qwen3.8-max`
执行 `scaffold-with-user-simulator`，Agent 和 judge 均显式配置该模型。
该用例检查模拟模型独立配置、固定与模拟轮次混排、自主对话及轮次上限。
实测 OpenCode 1.14.24 下文件存在检查与五项 judge 标准全部通过，生成的
YAML 也经过独立的 `skill-up validate` 校验。本次未运行原 Claude 引擎的七用例套件。

通过密钥管理器提供 `DASHSCOPE_API_KEY` 并安装 OpenCode，在仓库根目录运行：

```bash
make build
PATH="$PWD/bin:$PATH" ./bin/skill-up run ./skills/skill-upper/evals/eval-dashscope.yaml
```

`TestSkillUpper_UserSimulator_DashScope` 将该配置纳入手动触发的完整模型 E2E。
该用例只生成配置并校验，其中 `test-model` 是占位符；真实模拟回复链路由
[用户模拟器示例](../../examples/user-simulator/README.md)单独覆盖。
