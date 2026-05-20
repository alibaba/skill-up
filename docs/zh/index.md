---
layout: home

hero:
  name: skill-up
  text: 面向 Agent Skill 开发者的评测框架
  tagline: 用 YAML 声明评测环境、依赖、用例与评分策略，本地或 CI 中一键运行。
  image:
    src: /logo.png
    alt: skill-up
  actions:
    - theme: brand
      text: 快速开始
      link: /zh/guide/getting-started
    - theme: alt
      text: 编写评测配置与用例
      link: /zh/guide/writing-evals
    - theme: alt
      text: 在 GitHub 上查看
      link: https://github.com/alibaba/skill-up

features:
  - title: AI 辅助配合 skill-upper
    details: 使用 skill-upper Agent Skill，通过自然对话与 AI Agent（如 Cursor、Claude Code、Qoder 等）创建和运行评测，无需记忆 CLI 语法。
    link: /zh/guide/getting-started#推荐使用方式-ai-辅助配合-skill-upper
    linkText: 了解更多
  - title: 声明式评测配置
    details: 通过 YAML（eval.yaml + cases/*.yaml）定义评测环境、引擎、模型与用例。
    link: /zh/guide/writing-evals
    linkText: 配置参考
  - title: 多引擎支持
    details: 支持 Qoder CLI、Claude Code、Codex 等多种 Agent Engine。
  - title: 灵活的评判策略
    details: 内置 rule_based、script、agent_judge 三类评判策略。
    link: /zh/guide/writing-evals#评估策略
    linkText: 评估策略
  - title: 结构化报告
    details: 输出 Anthropic 兼容的 grading.json、benchmark.json，以及 result.json、JUnit XML 与 HTML 报告。
    link: /zh/guide/cli-reference#产物目录结构
    linkText: 产物结构
  - title: 兼容 Anthropic 格式
    details: 通过 skill-up import 导入 evals.json，或使用 --auto 自动识别。
    link: /zh/guide/migration
    linkText: 迁移指南
  - title: 面向 CI
    details: 同时面向本地开发与持续集成流水线设计。
    link: /zh/guide/cli-reference#退出码
    linkText: 退出码说明
---
