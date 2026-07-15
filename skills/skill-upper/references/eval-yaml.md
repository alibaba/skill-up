# eval.yaml 字段参考（skill-up）

`eval.yaml` 是评测入口，声明「在什么环境、用什么 Engine、跑哪些用例、如何出报告」。内容对齐 [skill-up 用户手册 - 编写评测](https://alibaba.github.io/skill-up/zh/guide/writing-evals.html)。

## 完整字段骨架

```yaml
schema_version: v1alpha1

environment:
  type: none                      # none | opensandbox | docker

mcp:
  servers:
    - name: github
      mode: real                  # real；mocked 预留
      transport: http             # http | stdio；可按 endpoint/command 推断
      config_ref: evals/fixtures/mcp/github.yaml

skills:
  - source: local_path
    path: .

engine:
  name: claude_code               # claude_code | codex | qodercli（也兼容 qoder-cli）
  model:
    provider: anthropic
    name: claude-sonnet-4-6
    base_url: ""

cases:
  files:
    - evals/cases/a.yaml
  defaults:
    timeout_seconds: 300
    max_turns: 12
    collect_artifacts:          # 可选：用 glob 采集 workspace 产物文件
      - "**/*.json"
      - "report/**"
  parallelism: 2
  retry_policy:
    max_retries: 1
    retry_on: [timeout, error]

judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
  skills:                         # 可选：仅安装给 judge agent 的评分 Skill
    - source: local_path
      path: evals/fixtures/judge-rubric
  criteria:
    - "按 judge-rubric 中的细则判断输出是否满足要求"

benchmark:
  enabled: false

report:
  formats: [json, html]
  artifacts: [transcript]
```

`cases.parallelism` 可被 `skill-up run --parallelism N`（1–256）临时覆盖。

`collect_artifacts`（`cases.defaults` 级，或单个 `case.yaml` 内追加）用 [doublestar](https://github.com/bmatcuk/doublestar) glob（`*` 单层、`**` 跨目录）声明要采集的 workspace 文件。无论 Agent 成功/失败/超时，命中文件都会保留相对路径下载到 `<output-dir>/<case>/<config>/outputs/workspace/`。两层按并集去重合并。它与 `report.artifacts`（产物*类型*）、`agent_judge` 的 git diff（字符串）正交。

`judge.skills` 仅支持 `judge.type: agent_judge`，用于给 judge agent 安装可复用的评分 Rubric Skill。它不会安装到主运行 agent；顶层 `skills` 也不会自动安装到 judge。benchmark 下 `with_skill` / `without_skill` 都会安装 judge Skills，因为它们属于评分工具。路径相对 Skill 根目录解析，安装依赖具体 Agent adapter 的原生 Skill 支持；不要把 Skill 文件内容复制进 `criteria`。

## 运行环境

| type | 适用场景 | 说明 |
| --- | --- | --- |
| `none` | 纯文本 I/O、不强依赖沙箱 | 冷启动最快 |
| `opensandbox` | 需要远程沙箱（文件、命令执行等） | 需 `OPENSANDBOX_API_KEY`；服务地址等可放在 `environment.kwargs` 或 `OPENSANDBOX_BASE_URL` |
| `docker` | 本地容器隔离，无需远程服务 | 需本地 `docker` CLI 和 Docker daemon；镜像需提前拉取 |

### OpenSandbox 示例

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

常用 `kwargs`：`base_url`、`extensions`（JSON 字符串）、`request_timeout_seconds`、`file_transfer_parallelism` 等。鉴权密钥来自环境变量 `OPENSANDBOX_API_KEY`。

### Docker 示例

```yaml
environment:
  type: docker
  image: node:22                    # 必填，需提前 docker pull
  workspace_mount: /workspace       # 默认 /workspace
  env:
    NPM_CONFIG_REGISTRY: https://registry.npmmirror.com
  setup_steps:
    - run: npm install -g typescript
  entrypoint: ["sleep", "infinity"] # 默认 sleep infinity
```

前置条件：本地 `docker` CLI 和 Docker daemon。`network_policy: deny_all` 以 `--network=none` 创建容器；`allow_declared` 暂不支持。


## MCP

- `mode: real` 会把真实 MCP Server 装进 Agent。
- HTTP MCP 可 inline 或 `config_ref` 指向 `evals/fixtures/mcp/*.yaml`。
- stdio MCP 可配置 `command` / `args`。
- 环境变量引用：`${VAR}` 或整值 `$VAR`；`required_env` 会注入 Agent 环境。

## Engine 与模型

- `engine.model` 可选；省略时由引擎本地默认模型接管。
- `provider` / `name` 组合在 CLI 中形如 `anthropic/claude-sonnet-4-6`、`openai/gpt-4` 等。
- `qodercli` 通常无需配置 `model`。

### `engine.kwargs` —— agent 私有开关

`engine.kwargs` 是字符串键值对，每个 agent 只读取自己关心的 key，未知 key 被忽略。无人认识的 key（拼写错误，如 `bypas_sandbox`）会在 verbose 日志里打 DEBUG，`-v` 可见。CLI 等价开关：`--engine-kwarg key=value`（别名 `--ek`），可重复。优先级 `--engine-kwarg` > `engine.kwargs` > 缺省。

```yaml
engine:
  name: codex
  kwargs:
    bypass_sandbox: "true"
```

| key | agent | true 时行为 | 缺省 / false |
|---|---|---|---|
| `bypass_sandbox` | `codex` | 命令行强制 `--dangerously-bypass-approvals-and-sandbox`，覆盖根据 runtime 自动决定的 sandbox flag。用于宿主内核不支持 Landlock 的场景（典型：部分 CI 容器） | 维持现状：`none` runtime 用 `--sandbox workspace-write`，其它 runtime 已是 bypass |
| `bypass_sandbox` | `claude_code` | no-op（claude 现有命令已固定 `--permission-mode=bypassPermissions`） | no-op |
| `bypass_sandbox` | `qodercli` | no-op（qoder CLI 无对应 flag） | no-op |

## 常见错误

- `opensandbox` 但未配置鉴权或 `base_url` → 运行时失败
- `engine.model` 与网关不匹配 → 连接报错
- `cases.files` 路径不存在 → validate 失败
- 所有相对路径相对于 **Skill 根目录**（`SKILL.md` 所在目录）
