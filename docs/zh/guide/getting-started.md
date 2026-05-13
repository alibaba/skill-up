# 快速上手

skill-up 是一个面向 Agent Skill 开发者的评测工具。你可以用它来验证 Skill 在真实 Agent Engine（如 Claude Code、Codex、 Qodercli）中的功能正确性，并在本地或 CI 中持续回归。

---

## 安装

skill-up 以预编译二进制分发，无需安装任何运行时依赖。

### 使用 go install（推荐）

```bash
go install github.com/alibaba/skill-up/cmd/skill-up@latest
```

或从仓库 checkout 后本地构建：

```bash
make build
# 或
go build -o bin/skill-up ./cmd/skill-up
```

### 预编译二进制

从 [GitHub Releases](https://github.com/alibaba/skill-up/releases) 下载对应平台的压缩包，解压后将 `skill-up` 可执行文件放入 `PATH`。

### 验证安装

```bash
skill-up --version
```

---

## 核心概念

使用 skill-up 评测一个 Skill，你需要准备两样东西：

1. **eval.yaml** — 评测入口配置，声明运行环境、使用的 Engine 和模型、评估方式等全局设置
2. **case.yaml** — 单个评测用例，定义要发给 Agent 的 prompt、预期结果和评分规则

它们放在你 Skill 目录下的 `evals/` 文件夹中：

```plain
my-skill/
  SKILL.md              # 你的 Skill 定义
  evals/                # 评测目录
    eval.yaml           # 评测入口配置
    cases/              # 用例目录
      basic-test.yaml   # 一个评测用例
      edge-case.yaml    # 另一个评测用例
    fixtures/           # 测试资源（可选）
      repos/            # 仓库模板
      scripts/          # 评估脚本
```

---

## 5 分钟上手

### 第一步：创建评测配置

在你的 Skill 目录下创建 `evals/eval.yaml`：

```yaml
schema_version: v1alpha1

environment:
  type: none                    # 纯文本 Skill 无需容器隔离

skills:
  - source: local_path
    path: .                     # 当前 Skill 目录
  - source: local_path
    path: ./dependency_skill_dir     # 相对于待测Skill目录下，依赖其他skill时， 其他的SKILL.md 所在路径；

engine:
  name: claude_code             # 使用 Claude Code 作为 Agent Engine
  # model 配置为可选项。不填时，引擎将使用本地默认模型配置，不传 --model 参数。
  # 如需指定模型，可取消下方注释：
  # model:
  #   provider: anthropic
  #   name: claude-sonnet-4-6

cases:
  files:
    - evals/cases/hello-world.yaml
  defaults:
    timeout_seconds: 120
    max_turns: 5

report:
  formats: [json]
```

> **提示：** `engine.model` 中的 `provider` 和 `name` 均为可选字段。省略时，引擎会使用自身的本地默认模型设置，运行命令中不会添加 `--model` 参数。如需明确指定模型，只需在 `engine` 下添加 `model` 配置即可。

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
  must_not_contain:
    - "error"

judge:
  type: rule_based
  success:
    - output_contains:
        all: ["Hello", "World"]
```

### 第三步：校验配置

在运行前先检查配置是否正确：

```bash
skill-up validate ./evals/eval.yaml
```

正确时输出：

```plain
✓ eval.yaml is valid (loaded 1 case(s))
```

### 第四步：运行评测

```bash
skill-up run ./evals/eval.yaml
```

你将看到类似这样的输出：

```plain
Running 1 case(s) with agent claude_code
[Runner] Running 1 cases with agent claude_code
[Evaluator] Skill installed: <skill-name>
[Evaluator] Running case hello-world (with_skill): Skill 应该正确响应基本请求
[Evaluator] Case hello-world: PASS (pass_rate: 100.0%)
[INFO] Results written to ./<skill-name>-workspace/iteration-1
```

---

## 下一步

- [编写评测配置与用例](./writing-evals) — 了解 `eval.yaml` 和 `case.yaml` 的完整配置方式
- [CLI 命令参考](./cli-reference) — 查看所有可用命令和参数
- [从 Anthropic 格式迁移](./migration) — 如果你已有 Anthropic skill-creator 的 `evals.json`
