<div align="center">
  <p align="center">
    <img src="assets/logo.png" alt="skill-up logo" width="150" />
  </p>

  <h1>skill-up</h1>

  <p align="center">
    <a href="https://github.com/alibaba/skill-up/actions">
      <img src="https://github.com/alibaba/skill-up/actions/workflows/ci.yml/badge.svg" alt="CI" />
    </a>
    <a href="https://deepwiki.com/alibaba/skill-up">
      <img src="https://deepwiki.com/badge.svg" alt="Ask DeepWiki" />
    </a>
    <a href="./.github/badges/coverage.json">
      <img src="https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/alibaba/skill-up/main/.github/badges/coverage.json" alt="Coverage" />
    </a>
    <a href="https://go.dev/">
      <img src="https://img.shields.io/badge/go-%3E%3D1.25-blue" alt="Go Version" />
    </a>
    <a href="LICENSE">
      <img src="https://img.shields.io/badge/license-Apache%202.0-green" alt="License" />
    </a>
    <a href="https://goreportcard.com/report/github.com/alibaba/skill-up">
      <img src="https://goreportcard.com/badge/github.com/alibaba/skill-up" alt="Go Report Card" />
    </a>
    <a href="https://github.com/alibaba/skill-up/releases">
      <img src="https://img.shields.io/github/v/release/alibaba/skill-up" alt="Release" />
    </a>
  </p>

  <p align="center">
    <a href="./README.md">English</a> | <b>中文</b>
  </p>

  <p align="center">
    📖 <a href="https://alibaba.github.io/skill-up/zh/">用户手册</a> · <a href="https://alibaba.github.io/skill-up/">User Manual</a>
  </p>

  <hr />
</div>

## 简介

**skill-up** 是面向 Agent Skill 开发者的 CLI 评测框架。在 Skill 包内通过 `evals/eval.yaml` 与 `evals/cases/*.yaml` 声明评测环境、依赖、用例与评估方式，在本地或 CI 中运行评测并生成结构化报告。

> [!WARNING]
> 本项目仍处于 **早期演进阶段**：代码尚未完全稳定，部分 CLI 命令、配置字段以及公共 API 在后续版本中仍有可能调整。请在生产环境使用前关注 [CHANGELOG](CHANGELOG.md) 并做好兼容性验证。

## 特性

- **声明式评测配置**：通过 YAML（`eval.yaml` + `cases/*.yaml`）定义评测环境、引擎、模型和用例。
- **多引擎支持**：支持 Qoder CLI、Claude Code、Codex 等 Agent 引擎。
- **灵活评分**：支持 `rule_based`（规则匹配）、`script`（脚本评分）、`agent_judge`（Agent 评分）三种评估策略。
- **结构化报告**：输出 Anthropic 兼容的 `grading.json`、`benchmark.json`、`benchmark.md`，以及 `result.json`、JUnit XML 和 HTML 报告。
- **Anthropic 兼容**：通过 `skill-up import` 导入 `evals.json`，或使用 `--auto` 自动识别。
- **CI 就绪**：专为本地开发和持续集成流水线设计。

## 为什么需要 skill-up

官方的 [Agent Skills 评测指南](https://agentskills.io/skill-creation/evaluating-skills) 说明了正确的评测循环：编写真实用例，分别运行 with/without Skill，评分输出，汇总结果，然后持续迭代。`skill-up` 的价值是把这套流程产品化成一个可复用的 CLI：

- 用声明式的 `eval.yaml` + `cases/*.yaml` 取代临时拼出来的运行目录。
- 自动完成 workspace 准备、Skill 安装、Agent Engine 调用、评分和报告生成。
- 支持多个引擎（`claude_code`、`codex`、`qodercli`），不绑定单一客户端。
- 兼容 Anthropic 风格的 `evals.json`，同时提供更丰富的 judge、适合 CI 的命令和结构化报告。

## 推荐使用方式：AI 辅助配合 skill-upper

推荐使用仓库内置的 **skill-upper** Agent Skill。它会引导 AI Agent
为目标 Skill 生成评测配置、校验、运行并解释结果，避免一开始就手写所有
YAML。

### 1. 安装 `skill-upper` Agent Skill

推荐使用 `skills` CLI 安装：

```bash
# Codex，全局安装
npx skills add https://github.com/alibaba/skill-up/tree/main/skills/skill-upper -g -a codex -y

# Claude Code，全局安装
npx skills add https://github.com/alibaba/skill-up/tree/main/skills/skill-upper -g -a claude-code -y
```

安装这个 Skill 前不需要先安装 `skill-up`。`skill-upper` 在运行时会检查
`skill-up` 命令是否可用；如果缺失，它会引导 Agent 完成安装。

### 2. 添加与运行评测

在 AI Agent 中打开目标 Skill 项目。目标项目至少应包含：

```text
my-skill/
  SKILL.md
```

然后直接给 Agent 一个明确任务：

```text
使用 skill-upper 给这个 Skill 添加评测。
添加这个评测用例：
- 输入：写一个 hello world 的程序。
- 评测：是否包含 hello 和 world 打印。

然后运行 skill-up 完成校验和评测。
```

Agent 应该会生成类似结构：

```text
my-skill/
  SKILL.md
  evals/
    eval.yaml
    cases/
      basic.yaml
my-skill-workspace/
  iteration-1/
    result.json
```

当 `evals/eval.yaml` 位于包含 `SKILL.md` 的目录下时，`skill-up` 会在运行时
自动安装这个本地 Skill，通常不需要在 `eval.yaml` 里手动写 Skill 路径。

## 安装

使用安装脚本：

```bash
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash
```

安装脚本会从 [GitHub Releases](https://github.com/alibaba/skill-up/releases) 下载当前平台对应的二进制文件。

如需从仓库 checkout 后本地构建，需要安装 [Go](https://go.dev/dl/) 1.25 或更高版本：

```bash
make build
# 或
go build -o bin/skill-up ./cmd/skill-up
```

## 快速上手

### 第一步：创建评测配置

在 Skill 目录下创建 `evals/eval.yaml`：

```yaml
schema_version: v1alpha1

environment:
  type: none

engine:
  name: claude_code

cases:
  files:
    - evals/cases/hello-world.yaml
```

当 `evals/eval.yaml` 位于包含 `SKILL.md` 的目录下时，skill-up 会自动安装当前 Skill。未写出的字段会使用默认值：JSON 报告、`timeout_seconds: 300`、`max_turns: 10`、`parallelism: 1`。

完整的 `eval.yaml` 配置说明见 [编写评测配置与用例](docs/zh/guide/writing-evals.md)。

### 第二步：编写 Eval Case

创建 `evals/cases/hello-world.yaml`：

```yaml
input:
  prompt: |
    请帮我生成一个 Hello World 程序

expect:
  must_contain:
    - "Hello"
    - "World"
```

用例 `id` 默认取文件名（这里是 `hello-world`）。只有在需要脚本评测或 Agent 评测时，才需要额外添加 `judge` 配置。

### 第三步：校验配置

```bash
skill-up validate
```

这一步是可选的，但建议首次运行前执行：它只检查 `eval.yaml` 和引用的用例文件，不会启动 Agent Engine。

### 第四步：运行评测

```bash
skill-up run
```

评测结果将写入 `<skill-name>-workspace/iteration-1/` 目录。

### 从 Anthropic 格式导入

```bash
skill-up import ./evals/evals.json --output ./evals
```

## CLI 命令概览

| 命令                                 | 说明                                       |
| ------------------------------------ | ------------------------------------------ |
| `skill-up run [path]`                | 运行评测用例并生成报告                     |
| `skill-up validate [path]`           | 校验 `eval.yaml` 和用例文件                |
| `skill-up list-cases [path]`         | 列出配置引用的所有用例                     |
| `skill-up report <result.json>`      | 从已有结果生成报告                         |
| `skill-up import <evals.json>`       | 将 Anthropic `evals.json` 导入为 YAML 用例 |
| `skill-up debug judge <input.json>`  | 使用 JSON 输入调试 judge 模块              |
| `skill-up debug report <input.json>` | 使用 JSON 输入调试 report 模块             |

## 许可证

Apache License 2.0 — 详见 [LICENSE](LICENSE)。
