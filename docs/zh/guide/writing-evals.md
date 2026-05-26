# 编写评测配置与用例

本文档介绍如何为你的 Skill 编写完整的评测配置。你将学会如何定义运行环境、编写评测用例、以及配置评估策略。

---

## 目录结构

评测文件放在 Skill 目录下的 `evals/` 文件夹中：

```plain
my-skill/
  SKILL.md                        # Skill 定义文档
  evals/
    eval.yaml                     # 评测入口配置（必须）
    cases/                        # 用例目录
      basic-success.yaml          # 每个文件是一个用例
      edge-case-null.yaml
      regression-001.yaml
    fixtures/                     # 测试资源（可选）
      repos/                      # 仓库模板
        sample-project/
      diffs/                      # 补丁文件
        null-check.patch
      scripts/                    # 评估脚本
        check-output.sh
      mcp/                        # MCP 工具配置
        github.json
```

> **命名约定**：用例文件名即为用例 ID（去掉 `.yaml` 后缀）。例如 `basic-success.yaml` 的用例 ID 是 `basic-success`。

---

## eval.yaml — 评测入口配置

`eval.yaml` 是评测的全局配置，定义了「在什么环境中、用什么 Engine、怎么评估」。

### 最小配置

```yaml
schema_version: v1alpha1

environment:
  type: none

engine:
  name: claude_code
  model:
    provider: anthropic
    name: claude-sonnet-4-6

cases:
  files:
    - evals/cases/my-test.yaml
```

### 完整配置详解

```yaml
# ========== 1. 版本声明 ==========
schema_version: v1alpha1          # 固定值，不可省略

# ========== 2. 运行环境 ==========
environment:
  type: none                      # 支持 none / opensandbox / docker

# ========== 3. MCP 工具 ==========
mcp:
  servers:
    - name: github                # MCP Server 名称
      mode: real                  # real / mocked
      transport: http             # http / stdio；未指定时按 endpoint/command 推断
      config_ref: evals/fixtures/mcp/github.yaml  # 配置文件路径

# ========== 4. Skill 安装 ==========
skills:
  - source: local_path            # local_path（本地目录）
    path: .                       # Skill 所在路径

# ========== 5. Agent Engine ==========
engine:
  name: claude_code               # claude_code / codex / qodercli（也兼容 qoder-cli）
  model:
    provider: anthropic           # 模型供应商
    name: claude-sonnet-4-6       # 模型名称
    base_url: ""                  # 自定义 API 端点（可选）

# ========== 6. 用例配置 ==========
cases:
  files:                          # 用例文件列表（相对于 Skill 根目录）
    - evals/cases/basic-success.yaml
    - evals/cases/edge-case.yaml
  defaults:                       # 用例默认值
    timeout_seconds: 300          # 超时时间（秒），默认 300
    max_turns: 12                 # 最大交互轮数，默认 12
  parallelism: 2                  # 用例并行数，默认 1
  retry_policy:                   # 重试策略
    max_retries: 1
    retry_on: [timeout, error]

# ========== 7. 基线对比（可选） ==========
benchmark:
  enabled: false                  # 启用后会运行 with_skill 和 without_skill 两组对比

# ========== 8. 报告输出 ==========
report:
  formats: [json, html]           # json / junit / html
  artifacts: [transcript]         # 报告中包含的产物
```

`cases.parallelism` 是配置文件中的默认用例并行数；临时运行时可以用 `skill-up run --parallelism N` 覆盖它，不需要修改 `eval.yaml`。命令行覆盖值必须在 1 到 256 之间。

### 自定义 Engine（Custom Engine）

当 `engine.name` 不是内置引擎（`claude_code` / `codex` / `qodercli`）时，必须再写一个 `engine.custom` 段来告诉 skill-up 怎么调用你的 Agent。当前只实现了 `transport: local`；`transport: http` 已设计但尚未实现，validation 会直接报 "not yet implemented"。

```yaml
engine:
  name: my-agent
  model:
    provider: anthropic
    name: claude-sonnet-4-6
  custom:
    transport: local              # local（已实现）| http（规划中）
    response_format: session_result  # session_result（默认）| text
    timeout_seconds: 300
    env:                          # 凭据 / 敏感参数 —— 不要在 command/args 里引用这些
      MY_AGENT_TOKEN: ${MY_AGENT_TOKEN}
    kwargs:                       # 非敏感开关，模板里以 ${kwargs.<key>} 暴露
      profile: production
    local:
      command: /opt/my-agent/bin/run
      args:
        - --input
        - ${input_file}           # skill-up 写入的 SessionInput JSON 路径
        - --output
        - ${output_file}          # 你的 Agent 应写入 SessionResult JSON 的路径
      cwd: ${workspace}           # 可选；被限制在 runtime workspace 内
      input_file: inputs/messages.json     # 可选覆盖（相对 workspace）
      output_file: outputs/session-result.json
```

`command` / `args` / `cwd` / `env` / `input_file` / `output_file` 中可用的模板变量：
`${workspace}`、`${input_file}`、`${output_file}`、`${model}`、`${model_provider}`、`${model_name}`、`${case_id}`、`${variant}`、`${max_turns}`、`${timeout_seconds}`、`${kwargs.<key>}`，以及环境变量形式 `${VAR}` / `${VAR:-default}` / `${VAR?error message}`。

凭据收敛规则（配置加载期强校验）：

- `${api_key}` 以及任何看起来像凭据的 kwarg key（`token` / `secret` / `api_key` / `apiKey` / `bearerToken` 等）都不允许出现在 `command` / `args` / `cwd` / `input_file` / `output_file` 中，必须经由 `engine.custom.env` 注入到子进程环境变量里。
- `${SOMEVAR:-...}` 默认值如果匹配常见凭据特征（`sk-...`、`sk-ant-...`、`ghp_...`、`AIza...`、`AKIA...`、JWT 等），同样会在命令行场景被拒。

SessionInput / SessionResult 的完整 JSON 契约见 `docs/design/custom-engine.md`。

### MCP 配置说明

MCP 支持 `mode: real` 和 `mode: mocked`。`real` 用于把真实 MCP Server 安装到 `claude_code`、`qodercli` 或 `codex` 等 Agent；`mocked` 会由 `internal/mcp` 生成本地 stdio Mock Server，并按普通 MCP 配置安装到 Agent。

HTTP MCP 可以直接在 `eval.yaml` 中声明，也可以用 `config_ref` 引用 `evals/fixtures/mcp/*.yaml`：

```yaml
mcp:
  servers:
    - name: agent-sandbox
      mode: real
      transport: http
      config_ref: evals/fixtures/mcp/agent-sandbox.yaml
```

`config_ref` 文件支持以下字段：

```yaml
transport: http
endpoint: https://mcp.example.com/mcp?token=${MCP_TOKEN}
required_env:
  - MCP_TOKEN
headers:
  PRIVATE-TOKEN: ${PRIVATE_TOKEN}
```

stdio MCP 可以配置 `command` 和 `args`：

```yaml
mcp:
  servers:
    - name: marker
      mode: real
      transport: stdio
      command: /usr/bin/python3
      args: [evals/fixtures/mcp/marker_server.py]
```

mocked MCP 可以直接使用内置的 `filesystem` Mock Server：

```yaml
mcp:
  servers:
    - name: filesystem
      mode: mocked
```

也可以通过 `config_ref` 定义工具响应：

```yaml
mcp:
  servers:
    - name: project-mgmt
      mode: mocked
      config_ref: evals/fixtures/mcp/project-mgmt.yaml
```

```yaml
tool_responses:
  create_publish_plan_simple:
    default:
      id: 999
      name: "{{params.name}}"
      status: ONGOING
```

环境变量引用支持 `${VAR}` 和完整值 `$VAR` 两种形式，变量名必须匹配 `[A-Za-z_][A-Za-z0-9_]*`。`required_env` 会把变量注入 Agent 运行环境；`headers` 中完整的环境变量引用会额外记录变量名，供 Agent 安装 MCP 时选择合适的传递方式。

### 运行环境选择指南

| 环境类型      | 适用场景                       | 示例 Skill                       |
| :-----------: | :----------------------------: | :------------------------------: |
| `none`        | 纯文本 I/O，无文件系统依赖      | 命令路由、知识问答、文本生成     |
| `opensandbox` | 需要远程沙箱服务                | 代码审查、项目生成、脚本执行     |
| `docker`      | 本地容器隔离，无需远程服务       | 自定义工具链、可复现 CI、离线环境 |

> **建议**：如果你的 Skill 不涉及文件操作，使用 `none` 可以省去沙箱准备时间。

#### OpenSandbox 配置

使用 `environment.type: opensandbox` 时，沙箱鉴权从进程环境变量 `OPENSANDBOX_API_KEY` 读取。服务地址、扩展参数等非敏感配置建议放在 `environment.kwargs` 中；Agent 自动安装后的可执行文件路径由对应 Agent 实现处理，通常不需要在评测配置中手动补 `PATH`。

```yaml
environment:
  type: opensandbox
  image: registry.example.com/your-org/sandbox-base:latest
  workspace_mount: /workspace
  ready_timeout_seconds: 300
  kwargs:
    base_url: https://agent-sandbox.example.com
    extensions: '{"profile":"ci"}'
    request_timeout_seconds: "900"
    file_transfer_parallelism: "8"
```

常用配置项：

| 字段 | 说明 |
| --- | --- |
| `image` | 沙箱镜像；未配置时使用 OpenSandbox runtime 默认镜像 |
| `workspace_mount` | runtime 工作区路径，默认 `/workspace` |
| `env` | 注入沙箱命令执行环境的变量；如确需扩展 PATH，可使用 `PATH: $CUSTOM_BIN:$PATH`，runtime 会在沙箱内展开 |
| `setup_steps` | 沙箱创建后在 runtime 工作区内执行的初始化命令 |
| `kwargs.base_url` | OpenSandbox 服务地址，也可用 `OPENSANDBOX_BASE_URL` 设置 |
| `kwargs.extensions` | JSON 字符串形式的 OpenSandbox 扩展配置 |
| `kwargs.request_timeout_seconds` | OpenSandbox SDK 请求超时时间 |
| `kwargs.file_transfer_parallelism` | 目录下载并发度 |

#### Docker 配置

使用 `environment.type: docker` 时，Agent 在本地 Docker 容器中运行，提供容器级别的隔离（文件系统、进程、网络），无需任何远程服务。

**前置条件：** 需要 `docker` CLI 在 PATH 中，且 Docker daemon 正在运行。runtime 不会自动拉取镜像，请提前执行 `docker pull <image>`。

```yaml
environment:
  type: docker
  image: node:22                    # 必填 — 需提前拉取到本地
  workspace_mount: /workspace       # 默认：/workspace
  env:
    NPM_CONFIG_REGISTRY: https://registry.npmmirror.com
  setup_steps:
    - run: npm install -g typescript
  entrypoint: ["sleep", "infinity"] # 覆盖容器 entrypoint（默认：sleep infinity）
```

常用配置项：

| 字段 | 说明 |
| --- | --- |
| `image` | **必填。** Docker 镜像名称，需在本地可用（使用 `docker pull` 预拉取） |
| `workspace_mount` | 容器内工作区路径，默认 `/workspace`，必须为绝对路径 |
| `env` | 注入容器命令执行环境的变量 |
| `setup_steps` | 容器启动后执行的初始化命令 |
| `entrypoint` | 覆盖容器的 ENTRYPOINT，默认 `["sleep", "infinity"]` |
| `network_policy` | `deny_all` 以 `--network=none` 创建容器（无网络访问）；`allow_declared` 暂不支持，需要 FQDN 级别出口过滤请使用 `opensandbox` |

> **建议：** Docker runtime 适合需要自定义系统包、特定语言运行时、或离线/隔离环境的评测。如需远程托管沙箱，请使用 `opensandbox`。

---

## case.yaml — 评测用例

每个 `.yaml` 文件定义一个评测用例，包含「发什么 prompt」和「怎么验证结果」。

### 单轮对话用例

大多数场景使用单轮 prompt 即可：

```yaml
id: find-null-bug
title: 应该识别出空指针 bug
description: 验证 Skill 能在代码审查中发现 null 解引用问题

input:
  prompt: |
    Review the current diff and report findings.

context:
  repo_fixture: evals/fixtures/repos/null-check-bug    # 加载仓库模板
  git:
    init: true
    checkout: main
    apply_diff: evals/fixtures/diffs/null-check.patch   # 应用补丁

constraints:
  timeout_seconds: 180
  max_turns: 8

expect:                           # 基本门槛检查
  must_contain:
    - "null"
    - "bug"
  must_not_contain:
    - "LGTM"
  exit_code: 0

judge:                            # 质量评估
  type: rule_based
  success:
    - output_contains:
        all: ["null", "bug"]
    - exit_code: 0
```

---

## 用例上下文 — context

`context` 用于为用例准备初始环境：

### 加载仓库模板

```yaml
context:
  repo_fixture: evals/fixtures/repos/my-project    # 将目录内容复制到工作区
```

### Git 操作

```yaml
context:
  repo_fixture: evals/fixtures/repos/my-project
  git:
    init: true                               # 初始化 git 仓库
    checkout: feature-branch                 # 切换分支
    apply_diff: evals/fixtures/diffs/my.patch      # 应用补丁文件
    remotes:                                 # 配置 git remote
      - name: origin
        url: https://github.com/user/repo
```

### 内联文件

```yaml
context:
  files:
    "src/main.py": |
      def hello():
          print("Hello World")
    "config.json": |
      {"debug": true}
```

---

## 评估策略

skill-up 的评估分为两层：**expect**（门槛检查）和 **judge**（质量评估）。

### expect — 快速门槛检查

`expect` 是零成本的本地检查。如果 `expect` 不通过，`judge` 会被跳过。

```yaml
expect:
  must_contain:                 # 输出必须包含这些关键词（全部匹配）
    - "review"
    - "bug"
  must_not_contain:             # 输出不能包含这些关键词
    - "LGTM"
    - "error"
  exit_code: 0                  # 期望退出码
  files_exist:                  # 期望这些文件存在
    - "review.md"
    - "output.json"
  files_not_exist:              # 期望这些文件不存在
    - "temp.log"
```

### judge: rule_based — 确定性规则评估

基于声明式规则判定用例是否通过，结果完全确定、可重复：

```yaml
judge:
  type: rule_based
  success:                                    # 所有条件必须满足
    - output_contains:
        all: ["bug", "null"]                  # 输出必须同时包含
        any: ["建议修复", "推荐更改"]          # 输出至少包含一个
        not: ["LGTM"]                         # 输出不能包含
    - exit_code: 0                            # 退出码必须为 0
    - tool_called:                            # Agent 必须调用了某个工具
        name: "github::create_pull_request"
        args:                                 # 工具参数部分匹配
          title: "Fix null check"
  failure:                                    # 任一条件匹配则立即失败
    - output_contains:
        any: ["无需修改", "代码正确"]
```

> **评估逻辑**：`failure` 优先于 `success`。任何一条 `failure` 规则匹配即立即失败；否则所有 `success` 规则必须全部满足才算通过。

### judge: script — 自定义脚本评估

用你自己的脚本（任何语言）来评估结果：

```yaml
judge:
  type: script
  script_path: evals/fixtures/scripts/check-quality.sh
  timeout_seconds: 30
```

脚本规则：

- **退出码 0** 表示通过，**非 0** 表示失败
- 工作目录为用例的工作区根目录
- 可用环境变量：`$EVAL_FINAL_MESSAGE`（最终消息）、`$EVAL_EXIT_CODE`（退出码）
- `$EVAL_TRANSCRIPT_PATH` 仅在运行时生成了可用 transcript 路径时才会设置；若未设置，脚本中会收到空值
- 脚本的标准输出会作为评估理由记录到报告中

### judge: agent_judge — LLM 评审

让 LLM 按你定义的标准来评分，适合需要语义理解的场景：

```yaml
judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6        # 评审使用的模型
  criteria:                                  # 评估标准（自然语言描述）
    - "输出中识别了真实存在的 bug，并给出了准确位置"
    - "没有将正确代码误报为 bug"
    - "建议具有可操作性，不是泛泛而谈"
  pass_threshold: 0.7                        # 通过率阈值，默认 0.7
  timeout_seconds: 60                        # 可选：限制单次 judge 调用时长（0 = 不加 judge 级 deadline，仍受 case timeout 约束）
```

> **成本提示**：`agent_judge` 会消耗额外的 token。建议对关键断言先用 `expect` 或 `rule_based` 做确定性检查，只对需要语义理解的部分使用 `agent_judge`。

---

## 基线对比

启用 `benchmark.enabled: true` 后，每个用例会运行两次：

1. **with_skill** — 安装 Skill 后执行（实验组）
2. **without_skill** — 不安装 Skill 执行（基线组）

对比结果会体现 Skill 带来的增量价值（通过率提升、时间和 token 消耗差异）。

```yaml
benchmark:
  enabled: true
```

> **注意**：基线对比会使评测时间和 token 消耗翻倍，默认关闭。

---

## 凭据配置

评测需要调用 Agent Engine 和模型 API，你需要提供凭据。按优先级从高到低：

### 1. 命令行参数（临时覆盖）

```bash
skill-up run ./evals/eval.yaml --api-key sk-xxx
```

### 2. 环境变量（推荐）

```bash
export ANTHROPIC_API_KEY=sk-ant-xxx
export OPENAI_API_KEY=sk-xxx
skill-up run ./evals/eval.yaml
```

环境变量按 `<PROVIDER>_<FIELD>` 格式自动解析，支持的字段有 `API_KEY`、`BASE_URL`、`MODEL`：

| Provider  | API Key             | Base URL             | Model            |
| :-------: | :-----------------: | :------------------: | :--------------: |
| anthropic | `ANTHROPIC_API_KEY` | `ANTHROPIC_BASE_URL` | `ANTHROPIC_MODEL` |
| openai    | `OPENAI_API_KEY`    | `OPENAI_BASE_URL`    | `OPENAI_MODEL`   |
| 其他      | `<PROVIDER>_API_KEY`| `<PROVIDER>_BASE_URL`| `<PROVIDER>_MODEL`|

你也可以在项目根目录创建 `.env` 文件，skill-up 启动时会自动加载其中的变量。

### 3. 配置文件（持久化）

创建 `~/.skill-up/credentials.yaml`：

```yaml
providers:
  anthropic:
    api_key: sk-ant-xxx
  openai:
    api_key: sk-xxx
    base_url: https://api.openai.com/v1    # 可选，支持代理
```

### qodercli 凭据说明

qodercli 的认证凭证与模型层凭证（如 `ANTHROPIC_API_KEY`）**完全独立**，两者不能混用。

| 层级               | 环境变量                        | 用途                                  |
| :----------------: | :-----------------------------: | :-----------------------------------: |
| qodercli 服务认证   | `QODER_PERSONAL_ACCESS_TOKEN`   | 向 qodercli 服务验证身份               |
| 模型层认证          | `ANTHROPIC_API_KEY` 等          | 由 qodercli 内部管理，用户无需关心      |

配置方式：

```bash
# 方式 1：直接设置环境变量
export QODER_PERSONAL_ACCESS_TOKEN=your_token_here

# 方式 2：写入项目根目录 .env 文件
echo 'QODER_PERSONAL_ACCESS_TOKEN=your_token_here' >> .env
```

> **提示**：`QODER_PERSONAL_ACCESS_TOKEN` 不是必须的。如果未设置，qodercli 会自动使用本地登录态（`~/.qoder/`），与手动运行 `qodercli` 的行为一致。你可以通过 `qodercli /login` 完成本地登录。
>
> **注意**：`--api-key` 参数和 `eval.yaml` 中的 provider API key **不会**被用作 qodercli 的认证 token。qodercli 仅从环境变量 `QODER_PERSONAL_ACCESS_TOKEN` 或本地登录态获取认证信息。

qodercli 的模型参数也有特殊限制：

- `model` 仅支持 qodercli 自己识别的值：`lite`、`efficient`、`auto`、`performance`、`ultimate`
- `base_url` 对 qodercli 不生效

---

## 实战示例

### 示例 A：纯文本路由 Skill

无文件依赖的轻量场景，验证 Skill 是否正确路由命令：

```yaml
# eval.yaml
schema_version: v1alpha1
environment:
  type: none
engine:
  name: claude_code
  model:
    provider: anthropic
    name: claude-sonnet-4-6
cases:
  files:
    - evals/cases/route-to-summary.yaml
  parallelism: 4                  # 无状态，可以完全并行
judge:
  type: rule_based
```

```yaml
# cases/route-to-summary.yaml
id: route-to-summary
title: 资源概况查询应路由到 app summary

input:
  prompt: |
    查一下 my-app 的资源概况，有多少机器

expect:
  must_contain:
    - "app summary"
  must_not_contain:
    - "app get"

judge:
  type: rule_based
  success:
    - output_contains:
        all: ["app summary", "--name"]
```

### 示例 B：MCP 工具调用 Skill

验证 Skill 是否正确调用了指定的 MCP 工具：

```yaml
# cases/create-plan.yaml
id: create-plan
title: 应正确调用创建发布计划工具

input:
  prompt: |
    帮我创建一个叫「Q1发布」的发布计划，计划 2026-04-03 发布

judge:
  type: rule_based
  success:
    - tool_called:
        name: "project-mgmt::create_publish_plan_simple"
        args:
          name: "Q1发布"
          planReleaseDate: "2026-04-03"
```

---

## 常见问题

### Q: eval.yaml 中的路径怎么填？

所有路径（包括 `cases.files` 和 fixture 路径）都是相对于 Skill 根目录（即 SKILL.md 所在目录）的。例如 `evals/fixtures/repos/my-project` 指的是 `<skill-root>/evals/fixtures/repos/my-project`。

### Q: 什么时候用 expect，什么时候用 judge？

`expect` 用于快速的、零成本的门槛检查（文件是否存在、关键词是否出现）。`judge` 用于更复杂的质量评估。两者可以同时使用——`expect` 不通过时 `judge` 会被跳过，节省时间和 token。
