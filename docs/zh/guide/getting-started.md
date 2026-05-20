# 快速上手

skill-up 是一个面向 Agent Skill 开发者的评测工具。你可以用它来验证 Skill 在真实 Agent Engine（如 Claude Code、Codex、 Qodercli）中的功能正确性，并在本地或 CI 中持续回归。

---

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

---

## 手动安装

### 使用安装脚本

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

engine:
  name: claude_code             # 使用 Claude Code 作为 Agent Engine

cases:
  files:
    - evals/cases/hello-world.yaml
```

> **提示：** 当 `evals/eval.yaml` 位于包含 `SKILL.md` 的目录下时，skill-up 会自动安装当前 Skill。未写出的字段会使用默认值：JSON 报告、`timeout_seconds: 300`、`max_turns: 10`、`parallelism: 1`。只有需要覆盖默认行为时，才添加 `engine.model`、`skills`、`cases.defaults` 或 `report`。

完整的 `eval.yaml` 配置说明见 [编写评测配置与用例](./writing-evals)。

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
  must_not_contain:
    - "error"
```

用例 `id` 默认取文件名（这里是 `hello-world`）。只有在需要脚本评测或 Agent 评测时，才需要额外添加 `judge` 配置。

### 第三步：校验配置

这一步是可选的，但建议首次运行前执行：它只检查 `eval.yaml` 和引用的用例文件，不会启动 Agent Engine。

```bash
skill-up validate
```

正确时输出：

```plain
✓ eval.yaml is valid (loaded 1 case(s))
```

### 第四步：运行评测

```bash
skill-up run
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
