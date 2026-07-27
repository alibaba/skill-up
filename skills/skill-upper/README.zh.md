# skill-upper

一个帮助你使用 `skill-up` CLI 评测并持续演进其他 Agent Skill 的 Agent Skill。

## 功能概述

`skill-upper` 引导你完成从评测到演进的闭环：

- **定位** 目标 Skill，理解其能力边界
- **搭建** `evals/eval.yaml` 和 `evals/cases/*.yaml` 脚手架，选择合适的 judge 类型
- **校验** 配置，在运行前发现 schema 错误
- **运行** 评测，调用真实 Agent Engine（Claude Code、Codex、qodercli 等）
- **诊断** 结构化报告和输出证据中的失败原因
- **演进** 修复目标 Skill 或增强 eval 覆盖，然后重新运行评测

## 使用场景

- 需要对某个 Skill 进行评测、测试或回归验证
- 需要根据评测失败修复并持续迭代某个 Skill
- 需要编写 `eval.yaml` / `case.yaml` 或选择 judge 类型
- 运行 `skill-up run/validate/list-cases/report/import/init`
- 从 Anthropic `evals.json` 迁移到 skill-up 格式
