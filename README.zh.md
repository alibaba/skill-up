<div align="center">
  <p align="center">
    <img src="assets/logo.png" alt="skill-up logo" width="150" />
  </p>

  <h1>skill-up</h1>

  <p align="center">
    <b>评测 Agent Skill、Agent 与 Workspace，基于证据演进 Skill。</b>
  </p>

  <p align="center">
    <a href="https://github.com/alibaba/skill-up/actions">
      <img src="https://github.com/alibaba/skill-up/actions/workflows/ci.yml/badge.svg" alt="CI" />
    </a>
    <a href="https://deepwiki.com/alibaba/skill-up">
      <img src="assets/deepwiki-badge.svg" alt="Ask DeepWiki" />
    </a>
    <a href="./.github/badges/coverage.json">
      <img src="https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/alibaba/skill-up/badges/.github/badges/coverage.json" alt="Coverage" />
    </a>
    <a href="https://go.dev/">
      <img src="https://img.shields.io/badge/go-%3E%3D1.26-blue" alt="Go Version" />
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
    <a href="plugins/dsh-skill-up/README.md">
      <img src="https://img.shields.io/badge/DeepSeek%20Harness-plugin-4D6BFE" alt="DeepSeek Harness Plugin" />
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

**skill-up** 是评测 Agent Skill、Agent 及其 Workspace 的 CLI。它用声明式用例驱动 Agent Engine，检查响应和 Workspace 变更，并在本地或 CI 生成报告。

- **Skill 评测**：验证 Skill 在不同用例中的行为，并可对比安装与不安装 Skill 的结果。
- **Agent 评测**：不安装 Skill，直接评估内置或自定义 Agent 的表现。
- **Workspace 评测**：使用用例 fixture 或已有本地目录，检查 Agent 对文件和代码仓库的处理结果。
- **Skill 演进**：**skill-upper** 通过对话分析失败、修复或补充评测用例，并重新运行 skill-up。

![skill-upper 驱动的 Skill 评测与演进流程](docs/public/skill-up-overview.png)

## 特性

- **skill-upper 从评测到演进的闭环**：通过自然对话创建评测、诊断失败、自动修复或补充用例并重新运行 skill-up，让 eval 评测集持续演进。
- **声明式评测配置**：通过 YAML（`eval.yaml` + `cases/*.yaml`）定义评测环境、引擎、模型和用例。
- **运行环境准备**：为每个用例准备本地、Docker 或 OpenSandbox 环境，在支持的 Agent Engine 中安装配置的真实或 Mock MCP Server，并将 `context.repo_fixture` 和 `context.files` 上传到 workspace。
- **可选 Skill 与 Workspace 用例**：无需安装 Skill 即可评测 Agent；可按用例准备代码仓库 fixture，或用 `--workspace` 评测已有本地目录。
- **多引擎支持**：内置支持 Qoder CLI、Claude Code、Codex；亦可通过 `engine.custom` 接入用户自定义 Agent（本地命令或 HTTP 服务，详见 [docs/design/custom-engine.md](docs/design/custom-engine.md)）。
- **DeepSeek Harness 插件**：通过内置的 `skill-upper` Skill、显式启用的持久化观察采集、审批门禁回归用例、隔离运行与状态对比，验证有证据的 Skill 改进。详见 [`plugins/dsh-skill-up`](plugins/dsh-skill-up/README.md)。
- **多轮对话与用户模拟**：支持固定轮次、固定与模拟回复混合编排，以及由独立模型驱动并自主结束的用户对话。
- **按角色指定引擎与模型**：被测 Agent、Agent Judge 和用户模拟器可分别配置，便于独立评判自定义 Agent。
- **灵活评分**：支持 `rule_based`（规则匹配）、`script`（脚本评分）、`agent_judge`（Agent 评分）三种评估策略。
- **结构化报告**：输出 Anthropic 兼容的 `grading.json`、`benchmark.json`、`benchmark.md`，以及 `result.json`、JUnit XML 和 HTML 报告。
- **Anthropic 兼容**：通过 `skill-up import` 导入 `evals.json`，或使用 `--auto` 自动识别。
- **CI 就绪**：专为本地开发和持续集成流水线设计。

## 为什么需要 skill-up

[Harness engineering](https://openai.com/index/harness-engineering/) 将 Agent 的运行环境、工具、约束和反馈循环都视为需要设计与改进的系统部分；[行为评测](https://developers.googleblog.com/the-anatomy-of-harness-engineering-how-to-evaluate-iterate-and-guard-ai-coding-agents/) 则让预期动作可观察，并帮助发现回归。`skill-up` 提供其中的评测与反馈环节：用可复现的提示词和 Workspace 运行 Agent，检查响应、工具调用和文件变更，并生成结构化报告。

评测 Skill 时，官方的 [Agent Skills 评测指南](https://agentskills.io/skill-creation/evaluating-skills) 还强调安装与不安装 Skill 的对比、评分汇总和持续迭代。`skill-up` 用同一套 CLI 支持这些流程：

- 用声明式的 `eval.yaml` + `cases/*.yaml` 取代临时拼出来的运行目录。
- 补齐持续改进闭环：skill-upper 可以解读失败报告、修复或新增 eval 用例，并通过对话驱动下一轮 skill-up 运行。
- 自动完成 Workspace 准备、按需安装 Skill、调用 Agent Engine、评分和生成报告。
- 支持多个引擎（`claude_code`、`codex`、`qodercli`、`qwen_code`），不绑定单一客户端。
- 兼容 Anthropic 风格的 `evals.json`，同时提供更丰富的 judge、适合 CI 的命令和结构化报告。

## 快速上手：使用 skill-upper 演进 Skill

演进 Skill 时，可使用仓库内置的 **skill-upper** Agent Skill。它可以让
AI Agent 通过对话创建评测、运行 skill-up、理解失败原因、修复 Skill 或
eval、补充回归用例，并持续完成下一轮迭代。

参与仓库开发时，只修改 `skills/skill-upper/` 这一份唯一源码。插件 bundle
生成到被忽略的构建目录中，不跟踪副本，也不使用软链；本地打包前运行
`make bundle-plugins`。
`make package-plugins VERSION=<version>` 会生成自包含的 Codex 和 DSH
安装包。GitHub tag release 会同时附带两个安装包及其校验和；本仓库不会将
任一插件发布到外部 marketplace 或 npm。

### 第一步：安装 skill-upper

```bash
# Codex，全局安装
npx skills add https://github.com/alibaba/skill-up/tree/main/skills/skill-upper -g -a codex -y

# Claude Code，全局安装
npx skills add https://github.com/alibaba/skill-up/tree/main/skills/skill-upper -g -a claude-code -y
```

通常不需要提前安装 skill-up。skill-upper 运行时会检查 CLI；如果缺失，
它会引导 Agent 完成安装。

### 第二步：创建并运行第一组评测

在 Codex、Claude Code 或其他兼容 Agent 中打开包含 `SKILL.md` 的 Skill
项目，然后直接对话：

```markdown
使用 skill-upper 评测这个 Skill。
阅读 SKILL.md，识别最重要的能力，创建真实的 eval 用例并选择合适的
Judge，校验配置后运行 skill-up。最后总结结果和影响最大的失败项。
```

skill-upper 会生成声明式评测集并替你驱动 CLI：

```text
my-skill/
  SKILL.md
  evals/
    eval.yaml
    cases/
      <case-id>.yaml
my-skill-workspace/
  iteration-1/
    result.json
```

### 第三步：修复、回归并持续迭代

在同一段对话中继续：

```markdown
使用 skill-upper 根据最新评测结果改进这个 Skill。
遵循它的演进流程，对照原版与候选版，补充回归覆盖，并总结证据支持
保留哪些改动。
```

对照与接受改动的规则统一定义在
[skill-upper 的演进流程](skills/skill-upper/SKILL.md#step-8-evolve-the-skill-when-requested)中。
这段提示词只负责调用该流程；候选版没有体现出有证据支持的改进时，
保留原版也是有效结果。

### 更喜欢手工配置？

你仍然可以直接安装 CLI，并手写 `eval.yaml` 与用例文件：

```bash
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash
```

详细步骤请查看官网的
[快速开始](https://alibaba.github.io/skill-up/zh/guide/getting-started)、
[编写评测](https://alibaba.github.io/skill-up/zh/guide/writing-evals)、
[CLI 命令参考](https://alibaba.github.io/skill-up/zh/guide/cli-reference)和
[用户配置](https://alibaba.github.io/skill-up/zh/guide/user-config)。
Windows 的安装方式与已知限制请参阅
[Windows 指南](https://alibaba.github.io/skill-up/zh/guide/windows)。

## 不依赖 Skill 评测 Agent 或 Workspace

Agent 评测集不需要 `SKILL.md`，但应显式设置 `skills: []`：省略 `skills` 时，
如果 skill-up 在配置文件的上层目录找到 `SKILL.md`，就会自动安装该 Skill。
将 `eval.yaml` 和用例放在 `evals/` 下，并显式传入配置路径。用例和 fixture
路径相对于找到的 Skill 根目录解析；找不到时则相对于 `evals/` 所在目录。

```yaml
# evals/eval.yaml
schema_version: v1alpha1
environment:
  type: none
skills: []
engine:
  name: codex
cases:
  files:
    - evals/cases/agent-response.yaml
```

```yaml
# evals/cases/agent-response.yaml
input:
  prompt: Reply with READY.
expect:
  must_contain: [READY]
```

```bash
skill-up validate ./evals/eval.yaml
skill-up run ./evals/eval.yaml
```

评测代码仓库任务时，可在用例中用 `context.repo_fixture` 准备独立的
Workspace，再通过 `expect` 或 Judge 检查结果。评测已准备好的本地目录时，
先编写适用于该目录的用例，再运行
`skill-up run ./evals/eval.yaml --workspace /path/to/project --parallelism 1`。
skill-up 会保留这个目录，但 Agent 和用例准备步骤可能修改其中内容。此参数仅
支持 `environment.type: none`、串行用例且关闭 benchmark。详见
[编写评测](docs/zh/guide/writing-evals.md)和
[CLI 命令参考](docs/zh/guide/cli-reference.md#skill-up-run)。

## 多轮对话与角色配置

模拟用户根据 Agent 的实际回答生成后续输入，无需预先写死整段问答。例如，可以用
一场“二十问猜物品”评测 Agent 的提问与推理能力：

- **被测 Agent**：用自然语言提出是非问题，并猜测物品。
- **模拟用户**：掌握隐藏答案，根据对话历史回答问题。
- **独立 Agent Judge**：读取完整对话，按明确标准逐项评分。

以下 **case YAML** 片段只给出开场规则，后续回复由模拟用户动态生成：

```yaml
id: umbrella
title: 通过模拟用户的多轮回复猜出隐藏物品
user_simulator:
  scenario: |
    你是二十问的出题人，隐藏答案是一把普通折叠雨伞。
    按常识只回答“是”“否”或“不确定”，不要泄露答案或主动提供线索。
    玩家猜中，或完成最后一次允许的猜测后，立即结束对话。
input:
  prompt: |
    我想好了一个日常物品。请用是非问题猜出它，我只回答是、否或不确定。
    每轮只问一个问题，至少提问两次再猜；提问和错误猜测共限 20 次，
    之后还有一次最终猜测。请用自然语言交流。
constraints:
  max_turns: 22
```

隐藏答案只放在模拟用户的场景中，不放进被测 Agent 的开场消息。例如，Agent
询问物品是否用于遮雨，与询问它是否为电子产品，会引出不同的后续问题，模拟用户
会根据实际问题回答。22 轮运行上限为最多 20 次提问或错误猜测、一次最终猜测及
一句正常收尾留出空间；游戏规则仍由 Judge 检查，运行上限本身不负责执行这些规则。

也可以使用其他对话编排方式：

| 方式 | 用例配置 | 行为 |
| --- | --- | --- |
| 自主模拟（上例） | `input.prompt` + `user_simulator.scenario`，不配置 `input.turns` | 模拟器生成后续用户消息，并决定何时停止 |
| 混合对话 | `input.turns` 中混用 `content` 和 `respond`，并配置 `user_simulator.scenario` | 模拟器根据真实对话历史和当前轮指令生成指定轮次的用户回复 |
| 固定对话 | `input.turns[].content` | 按顺序发送预先写好的用户消息 |

真实的逐轮交互需要引擎支持会话续接。目前 `claude_code`、`codex`、`qodercli`
和 `opencode` 支持；custom engine 需设置 `engine.custom.conversation_mode: stateful`
并通过 `session_id` 续接会话。普通固定多轮用例在不支持续接的引擎上退化为批量提示；
用户模拟用例要求支持续接。自主对话的开场算第 1 轮，`max_turns` 限制总轮数；
模拟器决定停止并不代表通过，最终仍由配置的 Judge 判定。

### 分别配置被测 Agent、Judge 和模拟器

| 角色 | `eval.yaml` 配置 | 独立性 |
| --- | --- | --- |
| 被测 Agent | `engine.name`、`engine.model`、可选 `engine.custom` | 执行任务，可使用内置引擎或自有本地命令／HTTP 服务 |
| Agent Judge | `judge.engine`，或 `judges[]` 中各成员的 `engine` | 可选择独立引擎、模型、provider 和端点；规则与脚本 Judge 不需要模型 |
| 模拟用户 | `user_simulator.provider`、`protocol`、`model` | 使用独立模型连接，根据场景和历史生成用户回复；不选择 Agent CLI 引擎 |

以下是 **eval.yaml** 的角色配置片段。模型名为占位符，需替换为各 provider
实际支持的 ID，并配置用例文件和环境。这里的评分标准对应上面的游戏，
`pass_threshold: 1.0` 要求所有评分项均通过：

```yaml
engine:
  name: codex
  model:
    provider: openai
    name: your-agent-model-id

judge:
  type: agent_judge
  engine:
    name: claude_code
    model:
      provider: anthropic
      name: your-judge-model-id
  criteria:
    - "玩家至少提问两次再猜，并正确猜出雨伞"
    - "玩家每轮只问一个问题，遵守 20 次提问或错误猜测加一次最终猜测的限制"
    - "模拟用户的回答符合普通折叠雨伞的常识，且未在猜中前泄露答案"
  pass_threshold: 1.0
  context:
    transcript: file_ref

user_simulator:
  provider: simulation
  protocol: openai
  model: your-simulator-model-id
  timeout_seconds: 60
```

`judge.engine` 不继承被测 Agent 的模型、CLI 覆盖参数或连接配置；分别准备所需的
Engine 和凭据。它与旧式 `judge.model` 互斥，后者复用被测 Agent 的引擎。
示例中的模拟器使用 `SIMULATION_API_KEY` 和 `SIMULATION_BASE_URL`，也可通过
凭据文件配置同名 provider；`protocol` 支持 `openai`（Chat Completions）和
`anthropic`（Messages）。独立 Judge 仍使用用例的 runtime，没有 `judge.environment`。
驱动 skill-upper 做诊断和修改的宿主 Agent／模型由宿主应用配置，不属于这些评测字段。

完整字段、逐轮断言和会话支持矩阵见[编写评测](https://alibaba.github.io/skill-up/zh/guide/writing-evals)。
可运行配置及已验证范围见[用户模拟示例](examples/user-simulator/README.md)，
自定义引擎接入见[Custom Engine 设计](docs/design/custom-engine.md)。

## CLI 命令概览

| 命令                                 | 说明                                       |
| ------------------------------------ | ------------------------------------------ |
| `skill-up run [path]`                | 运行评测用例并生成报告                     |
| `skill-up validate [path]`           | 校验 `eval.yaml` 和用例文件                |
| `skill-up list-cases [path]`         | 列出配置引用的所有用例                     |
| `skill-up compare <result> <result>...` | 离线对比多个评测结果，支持 HTML、Markdown、JSON 和文本 |
| `skill-up report <result.json>`      | 从已有结果生成报告                         |
| `skill-up import <evals.json>`       | 将 Anthropic `evals.json` 导入为 YAML 用例 |
| `skill-up debug judge <input.json>`  | 使用 JSON 输入调试 judge 模块              |
| `skill-up debug report <input.json>` | 使用 JSON 输入调试 report 模块             |

## GitHub Action

在 CI 上对你的 Agent Skill 跑评测,每个 PR 自动触发——并在一步内**跨引擎**
(`claude_code` / `codex` / `qodercli` / `qwen_code`)校验同一个 skill。本仓库根目录提供了
action([`action.yml`](action.yml)):

```yaml
# .github/workflows/skill-eval.yml
name: Skill Eval
on:
  pull_request:
    paths: ['skills/**', 'evals/**', '**/SKILL.md']
jobs:
  eval:
    runs-on: ubuntu-latest          # Docker 容器 action —— 仅 Linux
    steps:
      - uses: actions/checkout@v4
      - uses: alibaba/skill-up@main  # 见下方「版本引用」
        with:
          engine: claude_code        # 或 codex / qodercli / qwen_code;留空则由 eval.yaml 自行声明
          api-key: ${{ secrets.ANTHROPIC_API_KEY }}
          base-url: https://api.anthropic.com   # 你的模型端点
          skill-target: evals/eval.yaml
```

调用方前提:**Linux** runner(这是 Docker 容器 action),以及把模型凭据存为仓库
secret。runner 镜像是 public 的,无需额外 registry 鉴权。

主要入参:`engine`、`model`、`provider`、`api-key`、`base-url`、`skill-target`、
`parallelism`。action 预先把 skill-up 和三个引擎 CLI 烤进 runner 镜像,跑一次
就是「拉镜像、评测」。完整入参/产出见 [`action.yml`](action.yml)。

### 版本引用

`uses:` 可以指向任何**包含 `action.yml`** 的 git ref。生产建议 pin 一个**含本
action 的 release tag**(从引入 action 的那个 release 起)或 commit SHA;`@main`
则始终跟随最新。**早于** action 引入的 release tag 里没有 `action.yml`,不能用作 ref。

## 许可证

Apache License 2.0 — 详见 [LICENSE](LICENSE)。
