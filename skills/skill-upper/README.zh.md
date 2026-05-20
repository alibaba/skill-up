# skill-upper

一个帮助你使用 `skill-up` CLI 为 Agent Skill 搭建、运行和解读评测（evals）的 Agent Skill。

## 功能概述

`skill-upper` 引导你完成评测的完整生命周期：

- **定位** 目标 Skill，理解其能力边界
- **搭建** `evals/eval.yaml` 和 `evals/cases/*.yaml` 脚手架，选择合适的 judge 类型
- **校验** 配置，在运行前发现 schema 错误
- **运行** 评测，调用真实 Agent Engine（Claude Code、Codex、qodercli 等）
- **解读** 结果——通过率、失败断言、基线对比和 HTML 报告

## 使用场景

- 需要对某个 Skill 进行评测、测试或回归验证
- 需要编写 `eval.yaml` / `case.yaml` 或选择 judge 类型
- 运行 `skill-up run/validate/list-cases/report/import/init`
- 从 Anthropic `evals.json` 迁移到 skill-up 格式

