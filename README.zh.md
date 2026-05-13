<div align="center">
  <p align="center">
    <img src="assets/logo.png" alt="skill-up logo" width="150" />
  </p>

  <h1>skill-up</h1>

  <p align="center">
    <a href="https://github.com/alibaba/skill-up/actions">
      <img src="https://github.com/alibaba/skill-up/actions/workflows/ci.yml/badge.svg" alt="CI" />
    </a>
    <a href="https://codecov.io/gh/alibaba/skill-up">
      <img src="https://codecov.io/gh/alibaba/skill-up/branch/main/graph/badge.svg" alt="Coverage" />
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
> 本仓库核心业务逻辑已经实现，但整体仍处于 **早期演进阶段**：代码尚未完全稳定，部分 CLI 命令、配置字段以及公共 API 在后续版本中仍有可能调整。请在生产环境使用前关注 [CHANGELOG](CHANGELOG.md) 并做好兼容性验证。

## 特性

- **声明式评测配置**：通过 YAML（`eval.yaml` + `cases/*.yaml`）定义评测环境、引擎、模型和用例。
- **多引擎支持**：支持 Qoder CLI、Claude Code、Codex 等 Agent 引擎。
- **灵活评分**：支持 `rule_based`（规则匹配）、`script`（脚本评分）、`agent_judge`（Agent 评分）三种评估策略。
- **结构化报告**：输出 Anthropic 兼容的 `grading.json`、`benchmark.json`、`benchmark.md`，以及 `result.json`、JUnit XML 和 HTML 报告。
- **Anthropic 兼容**：通过 `skill-up import` 导入 `evals.json`，或使用 `--auto` 自动识别。
- **CI 就绪**：专为本地开发和持续集成流水线设计。

## 环境要求

- [Go](https://go.dev/dl/) 1.25 或更高版本 — 构建和运行 CLI 所需。

## 安装

**源码安装：**

```bash
go install github.com/alibaba/skill-up/cmd/skill-up@latest
```

**预编译二进制：**
从 [GitHub Releases](https://github.com/alibaba/skill-up/releases) 下载。

**本地构建：**

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

skills:
  - source: local_path
    path: .

engine:
  name: claude_code

cases:
  files:
    - evals/cases/hello-world.yaml
  defaults:
    timeout_seconds: 120
    max_turns: 5

report:
  formats: [json]
```

### 第二步：编写评测用例

创建 `evals/cases/hello-world.yaml`：

```yaml
id: hello-world
title: Skill 应该正确响应基本请求

input:
  prompt: |
    请帮我生成一个 Hello World 程序

expect:
  must_contain:
    - "Hello"
    - "World"

judge:
  type: rule_based
  success:
    - output_contains:
        all: ["Hello", "World"]
```

### 第三步：校验配置

```bash
skill-up validate ./evals/eval.yaml
```

### 第四步：运行评测

```bash
skill-up run ./evals/eval.yaml
```

评测结果将写入 `<skill-name>-workspace/iteration-1/` 目录。

### 从 Anthropic 格式导入

```bash
skill-up import ./evals/evals.json --output ./evals
```

## CLI 命令概览

| 命令 | 说明 |
|---------|-------------|
| `skill-up run [path]` | 运行评测用例并生成报告 |
| `skill-up validate [path]` | 校验 `eval.yaml` 和用例文件 |
| `skill-up list-cases [path]` | 列出配置引用的所有用例 |
| `skill-up report <result.json>` | 从已有结果生成报告 |
| `skill-up import <evals.json>` | 将 Anthropic `evals.json` 导入为 YAML 用例 |
| `skill-up debug judge <input.json>` | 使用 JSON 输入调试 judge 模块 |
| `skill-up debug report <input.json>` | 使用 JSON 输入调试 report 模块 |

## 项目结构

```text
skill-up/
├── cmd/skill-up/          # CLI 入口
├── internal/              # 私有实现
│   ├── cli/               # Cobra 命令
│   ├── config/            # YAML 配置加载与校验
│   ├── credential/        # API Key 与凭证解析
│   ├── runtime/           # 工作区运行时（none / opensandbox）
│   ├── agent/             # Agent 引擎适配层
│   ├── judge/             # 评估评分器
│   ├── report/            # 报告生成器（JSON / JUnit / HTML）
│   └── runner/            # 端到端编排
├── pkg/transcript/        # 公共 transcript 解析 API
├── docs/                  # VitePress 文档站点
│   ├── .vitepress/        # VitePress 配置
│   ├── guide/             # 英文用户指南
│   ├── zh/                # 中文用户指南
│   └── public/            # 静态资源（logo 等）
├── e2e/                   # 端到端测试
├── examples/              # 示例 fixture 与脚本
├── Makefile               # 构建与质量目标
├── go.mod / go.sum        # Go 模块依赖
└── README.md              # 英文说明文档
```

## 许可证

Apache License 2.0 — 详见 [LICENSE](LICENSE)。
