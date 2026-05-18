---
name: skill-upper
description: '使用 skill-up CLI 为 Agent Skill 搭建、运行和解读评测（evals）/ Set up and run Agent Skill evals with skill-up CLI. 当用户提到评测、eval、测试或回归某个 Skill，想添加 evals、编写 eval.yaml/case.yaml、运行 skill-up run/validate/list-cases/report/import/init，或从 Anthropic evals.json 迁移时使用。负责定位目标 Skill、生成 evals 脚手架、编写用例与 judge、处理凭据与用户配置、校验运行并解读 result/grading/benchmark 报告。英文请求必须先读取并遵循 SKILL.md；不要基于中文 SKILL.zh.md 生成英文任务。'
---

# use-skill-up-cli

帮助你使用 `skill-up` CLI 为 Agent Skill 搭建、运行、解读评测。

> English version: see [`SKILL.md`](./SKILL.md).

## 语言策略 / Language Policy

### 英文语境强制切换

如果用户当前消息主要是英文，或用户明确要求英文输出，**立即读取 `./SKILL.md`，并把 `SKILL.md` 作为本轮任务的唯一权威说明**。不要继续基于本中文 `SKILL.zh.md` 的正文、中文示例、中文模板注释或中文术语来推断英文任务；后续定位 Skill、生成 evals、编写 case、选择 judge、总结结果都按 `SKILL.md` 执行。

只有在用户使用中文，或无法判断语言时，才继续使用本 `SKILL.md` 的中文流程。

**默认使用中文与用户对话；如果用户使用英文（或其他语言）提问，则切换到对应语言回复，保持与用户输入一致。**

Default to Simplified Chinese when responding to the user. If the user writes in English (or any other language), switch to that language and stay consistent with the user's input throughout the session.

判断规则（优先级从高到低）：

1. 用户在当前消息中明确指定语言（例如 "answer in English" / "用中文回答"）→ 服从用户指定
2. 用户当前消息使用的自然语言 → 跟随用户的语言
3. 以上都无法判定 → 使用中文（默认）

无论使用哪种语言回复，本 SKILL.md 中的命令、`eval.yaml` / `case.yaml` 字段名、报告字段名等技术标识符必须保持英文原文，不要翻译。但是文档中的注释遵循对应的语言。

### 生成物语言规则

当你创建或编辑 `eval.yaml`、`case.yaml`、判分脚本、README 片段、最终回复等任何用户可见内容时，必须把“用户当前消息的语言”视为本轮输出语言：

- 用户用中文提问 → 最终回复、生成文件中的自然语言文案、YAML 注释、`title`、`description`、`input.prompt`、`expect` 关键词、`judge.criteria` 等都使用中文。
- 用户用英文提问 → 最终回复、生成文件中的自然语言文案、YAML 注释、`title`、`description`、`input.prompt`、`expect` 关键词、`judge.criteria` 等都使用英文；不要在生成的 case 文件中留下中文或 CJK 字符。
- 如果目标 Skill 自身是中文，但用户本轮用英文要求你创建 evals，应当把功能语义翻译成英文来写测试用例，而不是照抄目标 Skill 或模板里的中文文案。
- 英文语境下，`rule_based` 的 `expect.must_contain`、`judge.success.output_contains` 等确定性关键词也必须是英文关键词。例如把 `资源泄漏`、`关闭`、`异常处理` 改写为 `resource leak`、`close`、`exception handling`，不要写成 `"资源" (resources)` 这种双语括注。
- 技术标识符始终保持原样，例如 `schema_version`、`environment.type`、`engine.name`、`rule_based`、`agent_judge`、`script_path`、文件路径和命令。
- 使用 `assets/*.tmpl` 时，只把它当作结构参考。模板中的注释和占位文案必须改写成当前输出语言；如果当前输出语言是英文，必须翻译或删除所有中文注释与中文占位内容。
- 在英文语境下，完成所有文件生成后、提交最终回复前，**必须执行 CJK 自检**：逐一打开每个 `evals/cases/*.yaml` 和 `evals/eval.yaml`，搜索 CJK 字符（Unicode 范围 `\u4e00-\u9fff\u3400-\u4dbf\uf900-\ufaff\u3000-\u303f\uff00-\uffef`），包括但不限于 `title`、`description`、`input.prompt`、`expect` 关键词、`judge.criteria`、YAML 注释。一旦发现任何 CJK 字符，**立即替换为等价英文后才能结束任务**。此步骤不可跳过。

## 什么是 skill-up

`skill-up` 是一个面向 Agent Skill 开发者的评测 CLI。它把 Skill 安装到真实的 Agent Engine（Claude Code、Codex、qodercli 等）里，针对一组用例创建执行环境，执行 prompt，并根据你声明的规则 / LLM 评审 / 自定义脚本，来对待测 Skill 的使用效果进行判分，最终产出报告。

官方文档：<https://alibaba.github.io/skill-up/zh/>（用户手册）。

典型使用形态：

```
my-skill/
  SKILL.md
  evals/
    eval.yaml           # 评测入口（engine/模型/环境/用例集合）
    cases/
      <case-id>.yaml    # 单个评测用例（prompt + expect + judge）
    fixtures/           # 可选：仓库模板、补丁、脚本、MCP 配置
```

## 触发场景

遇到以下任一情况，应当使用本 skill：

- 用户要求 "跑一下 / 评测 / 验证 / 测试这个 skill"
- 用户想 "给 skill 加 evals、加测试用例、加回归用例"
- 用户要编辑 `eval.yaml` / `case.yaml`、或让你选择合适的 `judge` 类型
- 用户提到 `skill-up run/validate/list-cases/report/import/init`
- 用户想从 Anthropic `evals.json` 迁移到 skill-up
- 当前工作目录下存在 `evals/eval.yaml` 或 `evals/evals.json`，且用户想运行它

## 主流程（请严格按此顺序执行）

### 步骤 0：确保 skill-up 已安装

动手之前先验证 `skill-up` 可用：

```bash
command -v skill-up && skill-up --version
```

若输出版本号，跳到步骤 0.5 或步骤 1。若提示 `command not found`，在 **macOS / Linux** 上安装：

```bash
# 默认装最新版到 ~/.local/bin，脚本从 GitHub Releases 下载
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash

# 固定版本（安装脚本读取 SKILL_UP_VERSION，可为 vX.Y.Z 或 X.Y.Z）
export SKILL_UP_VERSION=v0.1.0
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash

# 可选：自定义安装目录（默认 $HOME/.local/bin）
export INSTALL_DIR="$HOME/bin"
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash
```

> **平台说明**：`skill-up` 目前仅支持 **macOS / Linux**，暂不支持 Windows。

安装后立刻再跑一次 `skill-up --version` 确认。若命令仍找不到，把 `~/.local/bin` 加进 `PATH`（重开终端或 `source` 对应 rc 文件）。

更多细节见 `references/install.md`。

### 步骤 0.5（可选）：用户配置与 Telemetry

若需要 OTLP 链路、`runtime_kwargs`（例如 opensandbox 默认 `base_url`）等，可初始化用户级或项目级配置：

```bash
skill-up init              # 写入 ~/.config/skill-up/config.yaml（遵循 XDG）
skill-up init --local      # 写入 $PWD/.skill-up.yaml
skill-up init --print      # 仅打印模板到 stdout
skill-up init --force      # 覆盖已存在文件
```

配置发现顺序（低 → 高）：内嵌空默认 < 用户配置 < 项目 `.skill-up.yaml` < `--config <path>`。环境变量 `SKILL_EVAL_CONFIG` 可指向用户配置文件路径（与代码常量名一致）。详见官方 README「User config」一节。

### 步骤 1：定位目标 Skill

1. 确认目标 Skill 的根目录（包含 `SKILL.md` 的那一级）。优先按这个顺序找：
   - 用户在消息里明确提到的路径
   - 当前工作目录及其父目录中最近的 `SKILL.md`
   - 用户打开的 / 最近查看的 Skill 文件
2. 读一下目标 `SKILL.md`，理解它的能力边界、触发条件、需要的工具 / 脚本 / 资产，这样你才能为它写出贴合实际的 prompt 和断言。如果目标 Skill 是中文而用户本轮用英文提问，先在脑中把能力点翻译成英文术语表，后续 prompt、断言关键词和总结都使用英文术语。
3. 确认 `evals/` 是否已存在：
   - 已存在 `evals/eval.yaml` → 跳到步骤 4（可选先按步骤 3 查缺补漏）
   - 只有 Anthropic 格式的 `evals/evals.json` → 参见 `references/migrate-anthropic.md`，两条路：`skill-up run --auto` 直接跑，或 `skill-up import` 转成原生 YAML
   - 什么都没有 → 进入步骤 2 生成脚手架

### 步骤 2：搭建 evals 脚手架（仅当还没有时）

基于目标 SKILL.md 的语义，生成最小可用骨架。模板见 `assets/`：

- 将 `assets/eval.yaml.tmpl` 复制到 `<skill-root>/evals/eval.yaml`
- 将 `assets/case.yaml.tmpl` 复制到 `<skill-root>/evals/cases/<case-id>.yaml`

复制模板后必须立刻做语言适配：按"生成物语言规则"改写所有自然语言内容。尤其在英文语境下，**禁止**把模板里的中文注释、中文占位符、中文标题或中文 judge criteria 原样写入生成的 `evals/cases/*.yaml`——所有占位文案必须翻译为英文。模板中的中文仅作为结构参考，不是要照搬的内容。

英文语境下生成 `rule_based` 用例时，要选择模型自然会用英文输出的关键词作为断言，不要为了贴近中文 Skill 原文而写中文关键词。比如代码审查用例可以断言 `resource leak`、`null pointer`、`unhandled exception`、`FileReader`，不要断言 `资源`、`关闭`、`异常`。

选型要点：

- `environment.type`：**纯文本类 Skill 用 `none`**；**需要远程沙箱（文件 / 命令执行等）用 `opensandbox`**（需环境变量 `OPENSANDBOX_API_KEY`，服务端地址等放在 `environment.kwargs` 或 `OPENSANDBOX_BASE_URL`）。当前 CLI **不支持** `docker` / `remote_sandbox` 作为环境与 skill-eval 旧文档对齐的旧类型。
- `engine.name` + `engine.model`：默认用 `claude_code`；`model` 可省略，由引擎本地默认模型接管。若用户明确使用 `codex` / `qodercli` 等，跟随用户。`qodercli` 通常无需配置 `engine.model`。
- `judge.type`：
  - `rule_based` — 有确定性关键词 / 文件 / 退出码 / 工具调用可验证时首选
  - `agent_judge` — 语义评估、需要 LLM 裁判时使用，但会额外消耗 token
  - `script` — 有结构化输出，需要自定义脚本检查时使用
  - 详见 `references/judge-types.md`
- 每个用例至少写一个能真正体现 Skill 价值的 prompt，不要用 "hello world" 糊弄；用例 ID = 文件名（不含 `.yaml`）

更细的字段含义见：

- `references/eval-yaml.md` — `eval.yaml` 完整字段
- `references/case-yaml.md` — `case.yaml` 完整字段（含多轮、context、expect、judge）

### 步骤 3：查缺补漏（已有 evals 时）

即便 `evals/` 已存在，也要快速过一遍：

- 用 `skill-up list-cases <path>` 列出所有用例，确认没漏掉
- 读一下 `eval.yaml`，检查 `engine.model` 与用户本地凭据 / 网关是否匹配
- 读一下典型用例的 `judge.type`，判断是否合理（避免 agent_judge 滥用）
- 如果用户提出加用例 / 加断言的诉求，在 `cases/` 下新建 / 编辑 yaml

### 步骤 4：校验配置

**永远**在 run 之前先跑一次 validate，成本低、能提前发现 schema 错误：

```bash
skill-up validate <skill-root>/evals/eval.yaml
```

通过时输出 `✓ eval.yaml is valid (loaded N case(s))`。如果失败，根据错误信息改 yaml，再校验，直到通过。

### 步骤 5：准备凭据

Agent Engine 需要调用模型 API，优先级从高到低：

1. 命令行 `--api-key`（一次性覆盖）
2. 环境变量 `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `QODER_PERSONAL_ACCESS_TOKEN`
3. `~/.skill-up/credentials.yaml`（持久化）

在动手 run 之前，先检查一下用户的 shell 里有没有对应的环境变量：

```bash
printenv | grep -E 'ANTHROPIC_API_KEY|OPENAI_API_KEY|QODER_PERSONAL_ACCESS_TOKEN'
```

没有就停下来，**向用户确认**凭据如何提供，**不要**擅自写入 `credentials.yaml` 或硬编码到 yaml 里。

使用 `environment.type: opensandbox` 时，通常还需要 `OPENSANDBOX_API_KEY`（见 `references/eval-yaml.md`）。

### 步骤 6：运行评测

标准调用：

```bash
skill-up run <skill-root>/evals/eval.yaml
```

常用 flag：

| 场景 | 命令 |
|---|---|
| 只跑某几个用例 | `--include-case-name "basic-*"`（可多次） |
| 排除某些用例 | `--exclude-case-name "*-flaky"` |
| 生成 HTML 报告给人看 | `--format html`（可与 `--format junit` 叠加） |
| 临时换 engine | `--engine codex --model openai/gpt-4` |
| 覆盖用例并行数 | `--parallelism 4`（1–256） |
| 直接消费 Anthropic evals.json | `--auto` |
| 连续运行 N 轮 | `--iteration 3`（产物写入 iteration-1/ 到 iteration-N/） |
| 自动在已有轮次后追加 | `--iteration 0`（默认行为：在最后一个已有 `iteration-N/` 之后再追加一轮） |
| 显示详细日志 | `-v`（debug）、`-vv`（trace） |

退出码 `0` = 全部通过；`1` = 有用例失败或执行出错，可以直接作为 CI 门禁。

**长耗时提示**：评测会真实调用大模型，通常 30s ~ 数分钟，切忌用过短的超时。如果用户在等结果，可以先用 `--include-case-name` 跑一个用例冒烟。

### 步骤 7：解读报告

默认产物目录是 `<skill-root>/<skill-name>-workspace/iteration-N/`，关键文件：

- `result.json` — 整个 run 的结构化结果（含完整的 `case_results[].grading`，包括 `status`、`turns_executed`、`assertion_results`）
- `benchmark.json` — 汇总统计（通过率、时间、token）
- `report.html`（若 `--format html`） — 人类可读报告
- `<case-id>/with_skill/grading.json` — 每个用例的评估结果（Anthropic 兼容格式，仅含 `expectations` + `summary`）
- `<case-id>/with_skill/outputs/` — Agent 真实产出的文件

总结给用户时，**务必**：

1. 先说一句总览：`N/M 通过 (X%)，耗时 Y 秒，用时最长的用例是 Z`
2. 对**失败**用例逐个列出：case-id、failing assertion 的 `text` 字段、`evidence` 字段里的关键证据。不要只说 "失败了"
3. 如果用户开启了 `benchmark.enabled: true` 做基线对比，要对比 `with_skill` vs `without_skill` 的 delta
4. 如果用户要看可视化报告，主动提示 HTML 路径，或者追加一次 `skill-up report result.json --format html`

## 命令速查

| 命令 | 用途 |
|---|---|
| `skill-up validate <eval.yaml>` | 校验配置，run 前必做 |
| `skill-up list-cases <eval.yaml>` | 列出所有用例 |
| `skill-up run [eval.yaml]` | 运行评测；省略路径时自动查找 `evals/eval.yaml` |
| `skill-up run --auto` | 直接消费 Anthropic `evals/evals.json` |
| `skill-up report <result.json> --format html` | 从已有结果重生成报告（支持 json/junit/html），不再重跑 |
| `skill-up import <evals.json>` | 一次性把 Anthropic 格式转成原生 yaml |
| `skill-up init` | 写入用户配置模板（Telemetry / runtime_kwargs 等） |
| `skill-up debug judge <input.json>` | 调试 judge 模块 |
| `skill-up debug report <input.json>` | 调试 report 模块 |

完整 flag 见 `references/cli.md`。

## 常见坑位

- **模型名写错** — `claude_code` engine 通常使用 `anthropic/claude-sonnet-4-6` 这类官方风格 ID；若用户配置了 `base_url` 或代理，可能使用内部 alias，按用户实际环境保留
- **`environment.type: opensandbox` 但未准备 `OPENSANDBOX_API_KEY`** — 沙箱创建或鉴权会失败
- **`expect.must_contain` 写了中文但模型输出是英文** — 先跑一轮看输出语言，再决定关键词；必要时在 prompt 里约束语言
- **`agent_judge` 滥用** — 能用 `rule_based` 绝不用 `agent_judge`，能 `expect` 先过滤的尽量 `expect`，省 token
- **Anthropic evals.json 字段误解** — 它的 `expectations` 是自然语言，默认映射到 `agent_judge.criteria`；要想变成确定性检查就得 `import` 后手改
- **fixture 路径** — 所有路径（包括 `cases.files` 和 fixture 路径）都是相对于 Skill 根目录（即 `SKILL.md` 所在目录）
- **iteration** — `skill-up run` 默认 `--iteration 0` 表示在已有最大 `iteration-N` 之后追加下一轮；显式 `--iteration N`（正整数）会跑满 N 轮

## 参考资料

- `references/install.md` — 安装 / 升级 / 排错
- `references/eval-yaml.md` — eval.yaml 完整字段说明（含 environment、engine、mcp、skills、benchmark、report）
- `references/case-yaml.md` — case.yaml 完整字段说明（含多轮 turns、context、expect、judge）
- `references/judge-types.md` — rule_based / agent_judge / script 三种 judge 的选型与写法
- `references/cli.md` — 全部命令和 flag
- `references/migrate-anthropic.md` — 从 Anthropic evals.json 迁移
- `assets/eval.yaml.tmpl` — eval.yaml 最小模板
- `assets/case.yaml.tmpl` — case.yaml 最小模板
