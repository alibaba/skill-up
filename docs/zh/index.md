---
layout: home

hero:
  name: skill-up
  text: 评测并持续演进 Agent Skill
  tagline: 度量 Skill 质量，再把失败转化为自动修复和下一轮迭代。
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
  - title: skill-upper 从评测到演进
    details: 通过自然对话创建评测，让 skill-upper 诊断失败、修复或补充用例，并持续重新运行 skill-up，推动评测集不断演进。
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

## 工作原理

skill-up 将 Agent Skill 的**评测**与**演进**合为一个闭环：通过声明式 YAML 评测、隔离的多引擎运行、灵活的 Judge 和结构化报告，让质量可度量；再由 skill-upper 把失败转化为改进，自动修复或补充 eval 用例，并与你持续重跑和迭代。

同一套流程既可在本地运行，也可接入 CI；同时兼容 Anthropic `evals.json` 导入，并输出 JSON、JUnit 和 HTML 报告。

![skill-up 通过自动修复和迭代 eval 来评测并演进 Agent Skill](/skill-up-overview.png)
