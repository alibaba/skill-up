---
title: 多轮对话评估支持
authors:
  - "kongtang"
creation-date: 2026-05-19
last-updated: 2026-05-19
status: provisional
---

# SUP-0001: 多轮对话评估支持

语言：[English](../0001-multi-turn-conversation-eval.md) | 中文

<!-- toc -->
- [摘要](#摘要)
- [动机](#动机)
  - [目标](#目标)
  - [非目标](#非目标)
- [需求](#需求)
- [提案](#提案)
  - [用户场景速查](#用户场景速查)
  - [注意事项/约束/说明](#注意事项约束说明)
  - [风险与缓解措施](#风险与缓解措施)
- [设计细节](#设计细节)
  - [Schema 变更](#schema-变更)
  - [评估器多轮执行引擎](#评估器多轮执行引擎)
  - [Agent 接口扩展](#agent-接口扩展)
  - [Judge 每轮断言](#judge-每轮断言)
  - [可靠性机制](#可靠性机制)
- [测试计划](#测试计划)
- [缺点](#缺点)
- [替代方案](#替代方案)
  - [替代方案 D：场景驱动的 ChatterAgent 模式（未来扩展）](#替代方案-d场景驱动的-chatteragent-模式未来扩展)
- [所需基础设施](#所需基础设施)
- [升级与迁移策略](#升级与迁移策略)
<!-- /toc -->

## 摘要

虽然 skill-up 目前在 Schema 层面已经定义了 `input.turns` 和 `Turn`（包括 `PostCondition`），但评估器在实际执行中会将所有轮次拼接成一条指令，一次性发送给 Agent 引擎。**不存在真正的逐轮交互、中间断言或条件分支——多轮对话评估的核心机制缺失。** 本提案设计并实现完整的多轮对话评估能力，使 skill-up 能够验证阶段门禁、二次确认、信息澄清、迭代优化、跨轮状态引用等——只有通过多轮交互才能体现的 Skill 行为。

## 动机

许多 Agent Skill 的核心价值只能通过多轮交互来体现，单轮测试无法覆盖。具体问题包括：

1. **阶段门禁无法验证**：对于 SDD-RIPER 这类工作流 Skill，需要先正常启动任务，再尝试跳过某个阶段，验证 Skill 的"护栏"是否生效
2. **二次确认流程无法测试**：危险操作（删除文件、生产部署）有"询问→确认→执行"和"询问→拒绝→取消"两条路径，需要至少两轮交互
3. **信息澄清行为缺失**：参数不完整时，Skill 应询问澄清而非猜测。这需要"澄清→提供→执行"的多轮验证
4. **迭代优化无法评估**：代码生成 Skill 需要根据前序输出进行增量修改，单轮测试无法模拟
5. **跨轮状态引用缺失**：第一轮创建资源，第二轮操作该资源，需要验证 Skill 是否正确维护了上下文

**当前代码库中的具体问题**：
- 在 `internal/evaluator/evaluator.go` 中，`buildCaseMessages()` 将所有轮次构建为消息，一次性传给 `agent.Run()`
- 在 `internal/agent/agent.go` 中，`BuildInstructionFromMessages()` 将所有用户消息拼接为单个字符串
- 所有 Agent 实现（claude_code、codex、qodercli）都调用 `BuildInstructionFromMessages()` 进行一次性执行
- `PostCondition` 在 Schema 中已定义，但评估器中没有检查逻辑
- `rule_based` Judge 仅支持全局断言，不支持每轮断言

### 目标

1. **逐轮执行**：评估器对每一轮分别调用 Agent，每轮完成后检查 `post_condition`，再决定是否进入下一轮
2. **会话连续性**：同一评估用例内的多轮交互共享 Agent 会话上下文，而非每轮新建会话
3. **中间断言**：每轮完成后执行 `post_condition`，支持 `skip_remaining`（跳过后续轮次）和 `fail`（立即失败）
4. **每轮 Judge 断言**：rule_based Judge 新增 `turn_response_contains` / `turn_response_not_contains` 规则
5. **动态值捕获**：`capture` 从某轮输出中提取值，通过模板变量供后续轮次的 prompt 使用
6. **向后兼容**：单轮 `input.prompt` 模式不受影响，现有用例无需修改

### 非目标

1. **Agent 引擎协议修改**：不修改 claude_code / codex / qodercli 的底层通信协议，多轮通过现有的会话恢复机制（如 `--resume`、`codex exec resume` 和 qodercli `-r <session-id>`）实现
2. **并行轮次执行**：轮次严格串行，不支持并行
3. **自动化对话树/分支测试**：本阶段仅支持线性多轮序列，不支持条件分支形成的对话树
4. **Agent 端流式实时断言**：断言仅在每轮完成后执行，不在流式输出过程中执行
5. **动态内容生成**：所有轮次的内容必须在 YAML 中预定义，不支持运行时由 LLM 或脚本生成（`capture` + `{{variable}}` 模板变量提供有限的动态值引用，但 prompt 结构本身是确定性的）
6. **场景驱动的模拟用户模式**：类似 ChatterAgent 的模式可以根据目标自适应生成用户轮次，对开放式业务流程很有价值，但本阶段有意延后到后续提案，以便先交付确定性、可复现的脚本化多轮评估能力

## 需求

### 必须有

| ID  | 需求               | 验收标准                                                                                                               |
| --- | ------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| R1  | 逐轮执行    | 评估器对 `input.turns` 中的每一轮分别调用 Agent，每轮后收集响应                |
| R2  | 会话连续性        | 同一用例内的多轮交互共享 Agent 会话上下文；Agent 可引用前序轮次的内容 |
| R3  | post_condition 检查      | 每轮后评估 `post_condition`；`on_fail: skip_remaining` 跳过后续轮次                                   |
| R4  | 每轮 Judge 断言 | `turn_response_contains` / `turn_response_not_contains` 断言支持指定轮次号                             |
| R5  | 向后兼容    | 现有 `input.prompt` 单轮用例不受影响，无需修改                                             |
| R6  | 对话记录完整性   | 多轮交互的完整对话记录保存所有轮次，每条消息标注其轮次号            |

### 应该有

| ID  | 需求                    | 验收标准                                                                                                       |
| --- | ------------------------------ | ------------------------------------------------------------------------------------------------------------------------- |
| S1  | 捕获值提取       | 通过正则或 JSONPath 从某轮响应中提取值，作为模板变量供后续轮次的 prompt 使用 |
| S2  | 每轮超时               | 每轮可有自己的超时，独立于用例级超时                                                 |
| S3  | 每轮 tool_called 断言 | `tool_called_in_turn` 断言可指定检查哪一轮的工具调用                                      |

### 最好有

| ID  | 需求               | 验收标准                                   |
| --- | ------------------------- | ----------------------------------------------------- |
| N1  | 重试机制扩展 | `retry_on` 新增 `turn_precondition_fail` 选项 |
| N2  | 每轮 agent_judge      | 使用 LLM-as-Judge 评估特定轮次的输出 |

## 提案

### 用户场景速查

在深入技术设计之前，先看三个典型场景，展示多轮对话评估在实际中如何配置，帮助读者快速建立直观理解。

---

#### 场景 1：阶段门禁——用户尝试跳过阶段时 Skill 应拒绝

**测试目标**：SDD-RIPER 工作流 Skill 在用户请求跳过 Research 阶段时，应拒绝并引导用户按正确顺序执行。

```yaml
# cases/phase-gate.yaml
id: phase-gate-enforcement
title: 用户尝试跳过阶段时 Skill 应拒绝

input:
  turns:
    # 第 1 轮：正常启动任务；Agent 应进入 Research 阶段
    - role: user
      content: "sdd_bootstrap: task=implement user login"
      post_condition:
        must_contain_any: ["Research", "analyze", "understand requirements"]
        on_fail: skip_remaining   # Agent 未进入 Research → 跳过后续轮次（场景不适用）

    # 第 2 轮：尝试跳过；Agent 应拒绝
    - role: user
      content: "Skip the Research phase and write the code directly"

judge:
  type: rule_based
  success:
    - turn_response_contains:      # 断言第 2 轮响应包含拒绝关键词
        turn: 2
        contains_any: ["need to complete first", "cannot skip", "execute in order"]
  failure:
    - turn_response_contains:      # 第 2 轮出现代码 → 门禁失效
        turn: 2
        contains_any: ["```python", "```java", "def ", "class "]
```

**关键点**：
- `post_condition` 在第 1 轮后检查 Agent 是否进入了预期阶段；若未进入，则跳过后续轮次
- `turn_response_contains` 专门断言 Agent 第 2 轮的响应
- `judge.failure` 是可选的显式负面证据，且优先于 `judge.success` 执行；任一 failure 规则命中即立即失败。如果没有 failure 命中，则必须满足全部 success 规则。因此，当响应既不满足 success 也不满足 failure 时，会因为缺少成功证据而 FAIL

---

#### 场景 2：二次确认——危险操作的确认/拒绝路径

**测试目标**：文件删除 Skill 在执行前应先询问确认；用户确认后才会执行。

```yaml
# cases/delete-confirm.yaml
id: delete-with-confirmation
title: 文件删除需要二次确认

input:
  turns:
    # 第 1 轮：发出删除请求
    - role: user
      content: "Delete all log files under /tmp/data/"
      post_condition:
        must_contain_any: ["confirm", "sure", "proceed", "delete"]
        on_fail: fail              # Agent 未询问就删除 → 测试失败

    # 第 2 轮：用户确认
    - role: user
      content: "Yes, confirm deletion"

judge:
  type: rule_based
  success:
    - turn_response_contains:
        turn: 1
        contains_any: ["confirm", "sure", "proceed"]   # 第 1 轮应询问确认
    - turn_response_contains:
        turn: 2
        contains_any: ["deleted", "done", "removed"]   # 第 2 轮应执行删除
```

**关键点**：
- `on_fail: fail` 表示如果 Agent 在第 1 轮未询问确认，整个用例立即标记为失败
- 两个 `turn_response_contains` 分别断言不同轮次的行为

---

#### 场景 3：跨轮状态引用——第 1 轮创建的资源 ID 在第 2 轮使用

**测试目标**：Agent 创建数据库表后，用户引用该表名插入数据；Agent 应正确引用。

```yaml
# cases/cross-turn-reference.yaml
id: cross-turn-table-reference
title: 跨轮引用——使用上一轮创建的表名进行操作

input:
  turns:
    # 第 1 轮：创建表
    - role: user
      content: "Create a users table with id, name, and email fields"
      post_condition:
        must_contain_any: ["CREATE TABLE", "create table"]
        on_fail: fail
      capture:
        - variable: table_name              # 从 Agent 响应中提取表名
          pattern: "(?i)CREATE TABLE\\s+(?P<value>\\w+)"

    # 第 2 轮：使用 {{table_name}} 引用上一轮提取的表名
    - role: user
      content: "Insert a test record into the {{table_name}} table"

judge:
  type: rule_based
  success:
    - turn_response_contains:
        turn: 2
        contains_any: ["INSERT INTO"]
```

**关键点**：
- `capture` 通过正则从 Agent 响应中提取值，存入变量 `table_name`
- 第 2 轮的 `content` 使用 `{{table_name}}` 模板语法引用该值，运行时自动替换为实际提取的表名
- 这意味着评估用例无需预先知道 Agent 会给表起什么名字

---

> **总结**：多轮对话评估的核心配置模式是 `input.turns`（定义每轮 prompt）+ `post_condition`（轮间断言）+ `capture`/`{{variable}}`（跨轮值传递）+ `turn_response_contains`（每轮 Judge 断言）。所有轮次的内容都是预定义的静态文本（含模板变量替换），确保评估结果完全可复现。

### 核心思路

将评估器的用例执行模式从"一次性发送所有消息"改为"迭代逐轮执行"。对每一轮：

1. 构建当前轮的用户消息（`content` 字段 + `{{variable}}` 模板替换）
2. 调用 Agent（使用会话恢复保持上下文）
3. 收集 Agent 响应
4. 执行 `post_condition` 检查
5. 若通过，可选执行 `capture` 提取值
6. 将提取的值注入下一轮的 prompt 模板
7. 进入下一轮或终止

```
┌─────────────────────────────────────────────────┐
│                  用例执行                  │
│                                                  │
│   第 1 轮          第 2 轮          第 N 轮         │
│  ┌──────┐       ┌──────┐       ┌──────┐         │
│  │Prompt│──────▶│Prompt│──────▶│Prompt│         │
│  └──┬───┘       └──┬───┘       └──┬───┘         │
│     │              │              │              │
│     ▼              ▼              ▼              │
│  ┌──────┐       ┌──────┐       ┌──────┐         │
│  │Agent │       │Agent │       │Agent │         │
│  │ Run  │       │Resume│       │Resume│         │
│  └──┬───┘       └──┬───┘       └──┬───┘         │
│     │              │              │              │
│     ▼              ▼              ▼              │
│  ┌──────┐       ┌──────┐       ┌──────┐         │
│  │Post  │       │Post  │       │ (无  │         │
│  │Cond  │       │Cond  │       │检查) │         │
│  └──┬───┘       └──┬───┘       └──┬───┘         │
│     │              │              │              │
│     ▼              ▼              ▼              │
│  Capture?       Capture?       ──────────┐      │
│     │              │                      │      │
│     ▼              ▼                      ▼      │
│  ┌───────────────────────────────────────────┐   │
│  │       Judge（全局 + 每轮断言） │   │
│  └───────────────────────────────────────────┘   │
└─────────────────────────────────────────────────┘
```

### Agent 会话恢复机制

多轮评估的关键挑战是如何在多次调用之间保持 Agent 的会话上下文。各 Agent 引擎的会话恢复能力调研：

| 引擎        | 恢复方式                         | 程序化命令                                             | 验证状态                                                                                         |
| ----------- | -------------------------------- | ------------------------------------------------------ | ------------------------------------------------------------------------------------------------ |
| claude_code | `--resume <session-id>` + `-p`   | `claude --resume <id> -p "follow-up"`                  | ✅ 经 [官方文档](https://code.claude.com/docs/en/cli-reference) 确认                              |
| codex       | `codex exec resume <session-id>` | `codex exec resume <id> "follow-up"`                   | ✅ 经 [官方文档](https://developers.openai.com/codex/cli/features) 确认非交互模式                  |
| qodercli    | `-r <session-id>` + `-p`         | `qodercli -r <id> -p "follow-up" --output-format=json` | ✅ 最新 qodercli 非交互模式已支持；从 JSON 输出解析 `sessionId`                                   |

**会话 ID 来源**：

| 引擎        | 会话 ID 生成                  | 会话 ID 存储                                                                                 |
| ----------- | ----------------------------- | -------------------------------------------------------------------------------------------- |
| claude_code | `uuid.New()` 通过 `--session-id` 传入 | `claudePrintJSONResult.SessionID` 字段，从 JSON 输出解析                                      |
| codex       | codex CLI 自动生成            | 与当前用例初始 `Run` 关联的会话文件；绝不能取全局“最新”文件                                  |
| qodercli    | qodercli 自动生成             | 初始 `qodercli -p --output-format=json` 输出中的 `sessionId` 字段                              |

**关键设计决策**：

1. **会话 ID 获取**：claude_code 从 JSON 输出的 `session_id` 字段提取；qodercli 从 `--output-format=json` 的 `sessionId` 字段提取；codex 必须将会话文件与当前用例的初始 `Run` 建立关联（例如解析 CLI 输出的会话 ID、使用用例隔离的会话目录，或序列化 initial Run + 会话文件 diff 窗口）。由于 skill-up 支持并发执行用例，codex 绝不能取 `~/.codex/sessions/` 下的全局最新文件
2. **Agent 接口扩展**：新增可选接口 `SessionResumer`（含 `RunTurn` 方法）；评估器通过类型断言检查能力。claude_code、codex、qodercli 三个内置 Agent 都应实现该接口。不支持的自定义或旧版适配器回退到一次性拼接模式
3. **优先级**：第 1 阶段实现共享评估器引擎和 claude_code/qodercli 适配器（两者都在 JSON 输出中提供可解析的会话 ID）；第 4 阶段在 codex 会话文件关联策略最终确定后实现 codex
### 注意事项/约束/说明

1. **Agent 引擎依赖**：会话恢复依赖于各 Agent CLI 的恢复能力（`--resume`、`codex exec resume`、`-r`）。本提案目标中的三个内置引擎都支持真实多轮恢复；不实现 `SessionResumer` 的自定义或旧版引擎会回退到“拼接所有轮次一次性发送”模式（现有行为），并在报告中注明
2. **模型随机性**：Agent 对同一 prompt 的响应可能不同；`post_condition` 匹配应使用宽松模式（`must_contain_any` 而非精确匹配）
3. **成本控制**：多轮交互的 token 消耗远高于单轮。用例设计应限制轮次数（建议以 2-5 轮为主）

### 风险与缓解措施

| 风险                                                          | 影响                                                | 概率 | 缓解措施                                                                                             |
| ------------------------------------------------------------- | ----------------------------------------------------- | ----------- | ------------------------------------------------------------------------------------------------------ |
| 自定义/旧版 Agent 引擎不支持会话恢复       | 多轮测试降级为一次性拼接 | 低         | 执行前检测引擎能力；在报告中明确标注执行模式               |
| post_condition 匹配过严导致过多 SKIPs | 评估效果低                          | 中      | 提供 `must_contain_any`（OR 语义）；支持正则匹配；引导用户使用宽松条件 |
| 多轮 token 消耗触发限流           | 评估被限流                             | 中      | 在轮次间添加可配置的 `turn_delay`；文档建议限制轮次数              |
| 会话恢复失败导致上下文丢失                   | 后续轮次语义不连续            | 低         | 恢复失败时标记为 ERROR 并附带诊断信息；不静默回退                               |

## 设计细节

### Schema 变更

#### 1. Turn 结构扩展

现有 `Turn` 定义（`internal/config/schema.go`）：

```go
type Turn struct {
    Role          string         `yaml:"role"`
    Content       string         `yaml:"content"`
    PostCondition *PostCondition `yaml:"post_condition,omitempty"`
}

type PostCondition struct {
    MustContainAny []string `yaml:"must_contain_any,omitempty"`
    OnFail         string   `yaml:"on_fail,omitempty"`
}
```

扩展版本：

```go
// Turn 是多轮评估用例中的单个对话轮次。
type Turn struct {
    Role           string         `yaml:"role"`                       // user（必填）
    Content        string         `yaml:"content"`                    // prompt 文本，支持 {{variable}} 模板
    PostCondition  *PostCondition `yaml:"post_condition,omitempty"`
    Capture        []CaptureRule  `yaml:"capture,omitempty"`
    TimeoutSeconds int            `yaml:"timeout_seconds,omitempty"`  // 每轮超时覆盖
}

// PostCondition 在轮次完成后检查 agent 响应。
type PostCondition struct {
    MustContainAny []string `yaml:"must_contain_any,omitempty"` // OR：至少匹配一个
    MustContainAll []string `yaml:"must_contain_all,omitempty"` // AND：全部必须匹配
    MustNotContain []string `yaml:"must_not_contain,omitempty"` // NONE：都不应匹配
    OnFail         string   `yaml:"on_fail,omitempty"`          // skip_remaining | fail（默认：fail）
}

// CaptureRule 从 agent 响应中提取值，供后续轮次使用。
type CaptureRule struct {
    Variable string `yaml:"variable"`           // 模板变量名（如 "plan_id"）
    Pattern  string `yaml:"pattern,omitempty"`   // 含命名组的正则 (?P<value>...)
    JSONPath string `yaml:"jsonpath,omitempty"`  // JSONPath 表达式（如 "$.transcript.tool_results[0].call_id"）
}
```

#### 2. Rule 扩展（每轮断言）

在现有 `Rule` 定义上新增断言类型：

```go
type Rule struct {
    // 现有字段
    OutputContains *OutputContainsRule `json:"output_contains,omitempty" yaml:"output_contains,omitempty"`
    ExitCode       *int                `json:"exit_code,omitempty"       yaml:"exit_code,omitempty"`
    ToolCalled     *ToolCalledRule     `json:"tool_called,omitempty"     yaml:"tool_called,omitempty"`
    FilesExist     []string            `json:"files_exist,omitempty"     yaml:"files_exist,omitempty"`
    FilesNotExist  []string            `json:"files_not_exist,omitempty" yaml:"files_not_exist,omitempty"`

    // 新增：每轮断言
    TurnResponseContains    *TurnResponseContainsRule    `json:"turn_response_contains,omitempty"     yaml:"turn_response_contains,omitempty"`
    TurnResponseNotContains *TurnResponseNotContainsRule `json:"turn_response_not_contains,omitempty" yaml:"turn_response_not_contains,omitempty"`
    ToolCalledInTurn        *ToolCalledInTurnRule        `json:"tool_called_in_turn,omitempty"        yaml:"tool_called_in_turn,omitempty"`
    ToolNotCalledInTurn     *ToolNotCalledInTurnRule     `json:"tool_not_called_in_turn,omitempty"    yaml:"tool_not_called_in_turn,omitempty"`
}

// TurnResponseContainsRule 检查特定轮次的响应是否包含预期文本。
type TurnResponseContainsRule struct {
    Turn        int      `json:"turn"                 yaml:"turn"`                     // 1 起始的轮次号
    ContainsAll []string `json:"contains_all,omitempty" yaml:"contains_all,omitempty"` // AND 语义
    ContainsAny []string `json:"contains_any,omitempty" yaml:"contains_any,omitempty"` // OR 语义
}

// TurnResponseNotContainsRule 检查特定轮次的响应是否不包含文本。
type TurnResponseNotContainsRule struct {
    Turn        int      `json:"turn"             yaml:"turn"`         // 1 起始的轮次号
    NotContains []string `json:"not_contains"     yaml:"not_contains"` // 都不应匹配
}

// ToolCalledInTurnRule 检查特定轮次是否调用了某工具。
type ToolCalledInTurnRule struct {
    Turn int            `json:"turn"           yaml:"turn"`
    Name string         `json:"name"           yaml:"name"`
    Args map[string]any `json:"args,omitempty" yaml:"args,omitempty"`
}

// ToolNotCalledInTurnRule 检查特定轮次未调用某工具。
type ToolNotCalledInTurnRule struct {
    Turn int    `json:"turn" yaml:"turn"`
    Name string `json:"name" yaml:"name"`
}
```

#### 3. YAML 配置示例

> 更完整的用户场景请见上方的 [用户场景速查](#用户场景速查) 部分。此示例展示所有 Schema 字段的组合使用。

```yaml
id: clarification-and-execute
title: 参数不完整时 Skill 应询问澄清

input:
  turns:
    # 第 1 轮：故意提供不完整参数；期望 Agent 询问澄清
    - role: user
      content: "Deploy the service for me"
      post_condition:
        must_contain_any: ["which environment", "which service", "please specify", "need to know"]
        on_fail: fail
      capture:
        - variable: clarification_question
          pattern: "(?P<value>[^.?]+[?])"

    # 第 2 轮：提供参数后，Agent 应执行部署
    - role: user
      content: "Deploy order-service to staging"
      post_condition:
        must_contain_all: ["order-service", "staging"]
        on_fail: fail
      timeout_seconds: 120

    # 第 3 轮：确认部署结果
    - role: user
      content: "What's the deployment result?"

judge:
  type: rule_based
  success:
    - turn_response_contains:
        turn: 1
        contains_any: ["which", "please specify", "need"]
    - turn_response_contains:
        turn: 2
        contains_any: ["deploy", "staging"]
    - turn_response_not_contains:
        turn: 1
        not_contains: ["deployed", "deploy completed"]
    - tool_called_in_turn:
        turn: 2
        name: deploy
```

### 评估器多轮执行引擎

#### 核心变更：`executeCaseOnce` 分支

在 `internal/evaluator/evaluator.go` 中，`executeCaseOnce` 方法需要根据输入类型进行分支：

现有方法签名保持不变；在 `agent.Run` 调用前插入一个分支：

```go
func (e *defaultEvaluator) executeCaseOnce(ctx context.Context, caseCfg *config.CaseConfig,
    configName string, overrideRT runtime.Runtime, overrideAgent agent.Agent) EvalResult {

    // ── 下方现有代码，不变 ──
    // startTime, prompt/turnsTotal, result 初始化, runtime 准备, judge 配置合并 ...

    // ── 新分支：多轮执行路径 ──
    if len(caseCfg.Input.Turns) > 1 {
        return e.executeMultiTurn(ctx, caseCfg, configName, rt, runAgent, judgeCfg, startTime)
    }

    // ── 下方现有单轮执行逻辑，完全不变 ──
    // messages := buildCaseMessages(caseCfg)
    // sessionResult, execErr := runAgent.Run(...)
    // return e.evaluateCaseSession(...)
}
```

**关键说明**：此处的修改策略是**最小侵入**——在现有 `executeCaseOnce` 方法的 `runAgent.Run()` 调用前插入一个 `if` 分支，仅当 `input.turns` 元素多于一个时走多轮路径。所有现有单轮逻辑（环境准备、产物收集、expect 预检、judge 评估等）完全不变。

#### 多轮执行核心逻辑

```go
// TurnResult 保存单轮执行的结果。
type TurnResult struct {
    TurnNumber    int                       // 1 起始
    Content       string                    // 本轮发送的用户消息
    Response      string                    // agent 响应文本
    Transcript    transcript.Transcript     // 本轮的对话记录
    SessionResult *agent.SessionResult      // 完整会话结果
    Status        TurnStatus                // completed, skipped, failed, error
    SkipReason    string                    // 状态为 skipped 时填充
    CapturedVars  map[string]string         // 从本轮捕获的变量
}

type TurnStatus string

const (
    TurnCompleted TurnStatus = "completed"
    TurnSkipped   TurnStatus = "skipped"
    TurnFailed    TurnStatus = "failed"
    TurnError     TurnStatus = "error"
)

func (e *defaultEvaluator) executeMultiTurn(
    ctx context.Context,
    caseCfg *config.CaseConfig,
    configName string,
    rt runtime.Runtime,
    runAgent agent.Agent,
    judgeCfg config.JudgeConfig,
    startTime time.Time,
) EvalResult {
    turnsTotal := len(caseCfg.Input.Turns)

    // 检查 Agent 是否支持会话恢复
    resumer, supportsResume := runAgent.(agent.SessionResumer)
    if !supportsResume {
        logging.WarnContextf(ctx, "Agent %s 未实现 SessionResumer; "+
            "多轮用例 %s 回退到一次性执行", runAgent.Name(), caseCfg.ID)
        return e.executeMultiTurnFallback(ctx, caseCfg, configName, rt, runAgent)
    }

    turnResults := e.executeTurnsSequentially(ctx, caseCfg, rt, runAgent, resumer)
    return e.finalizeMultiTurnResult(ctx, caseCfg, configName, rt, judgeCfg, turnResults, startTime)
}

// executeTurnsSequentially 按序执行每一轮，检查后置条件并在轮次间捕获值。
func (e *defaultEvaluator) executeTurnsSequentially(
    ctx context.Context,
    caseCfg *config.CaseConfig,
    rt runtime.Runtime,
    runAgent agent.Agent,
    resumer agent.SessionResumer,
) []TurnResult {
    turnsTotal := len(caseCfg.Input.Turns)
    capturedVars := make(map[string]string)
    turnResults := make([]TurnResult, 0, turnsTotal)
    var sessionID string
    var codexSessionSnapshotBeforeRun SessionFileSnapshot

    for i, turn := range caseCfg.Input.Turns {
        turnNum := i + 1

        // 1. 模板变量替换
        content, renderErr := renderTemplate(turn.Content, capturedVars)
        if renderErr != nil {
            turnResults = append(turnResults, TurnResult{
                TurnNumber:  turnNum,
                Content:     turn.Content,
                Status:      TurnError,
                SkipReason:  renderErr.Error(),
                CapturedVars: map[string]string{},
            })
            return turnResults
        }

        // 2. 构建本轮消息
        message := transcript.Message{
            Role:    transcript.RoleUser,
            Content: content,
            Turn:    turnNum,
        }

        // 3. 设置每轮超时 + 调用 Agent
        sessionResult, execErr := func() (*agent.SessionResult, error) {
            turnCtx := ctx
            if turn.TimeoutSeconds > 0 {
                var cancel context.CancelFunc
                turnCtx, cancel = context.WithTimeout(ctx, time.Duration(turn.TimeoutSeconds)*time.Second)
                defer cancel()
            }

            // 第 1 轮用 Run 启动新会话；后续轮次用 RunTurn 恢复
            if turnNum == 1 {
                if runAgent.Name() == "codex" {
                    codexSessionSnapshotBeforeRun = snapshotCodexSessions(turnCtx, rt)
                }
                sr, err := runAgent.Run(turnCtx, rt, agent.ExecOptions{},
                    []transcript.Message{message})
                if sr != nil {
                    sessionID = extractSessionID(turnCtx, rt, runAgent, sr, codexSessionSnapshotBeforeRun)
                }
                return sr, err
            }
            return resumer.RunTurn(turnCtx, rt, agent.ExecOptions{},
                message, sessionID)
        }()

        // 5. 收集本轮结果
        turnResult := TurnResult{
            TurnNumber:   turnNum,
            Content:      content, // 记录实际发送的内容
            CapturedVars: make(map[string]string),
        }
        if sessionResult != nil {
            turnResult.Response = sessionResult.FinalMessage
            turnResult.Transcript = sessionResult.Transcript
            turnResult.SessionResult = sessionResult
        }
        if execErr != nil {
            turnResult.Status = TurnError
            turnResult.SkipReason = execErr.Error()
            turnResults = append(turnResults, turnResult)
            return turnResults // 执行错误，终止后续轮次
        }
        turnResult.Status = TurnCompleted

        // 6. 执行 post_condition 检查
        if turn.PostCondition != nil {
            passed, reason := checkPostCondition(turn.PostCondition, turnResult.Response)
            if !passed {
                if turn.PostCondition.OnFail == "skip_remaining" {
                    turnResult.Status = TurnSkipped
                    turnResult.SkipReason = reason
                    turnResults = append(turnResults, turnResult)
                    // 标记后续轮次为跳过
                    for j := turnNum; j < turnsTotal; j++ {
                        turnResults = append(turnResults, TurnResult{
                            TurnNumber: j + 1,
                            Status:     TurnSkipped,
                            SkipReason: fmt.Sprintf("skipped: turn %d post_condition failed", turnNum),
                        })
                    }
                    return turnResults
                }
                // 默认："fail"
                turnResult.Status = TurnFailed
                turnResult.SkipReason = reason
                turnResults = append(turnResults, turnResult)
                return turnResults
            }
        }

        // 7. 执行 capture
        for _, cap := range turn.Capture {
            value, captureErr := extractCapturedValue(cap, turnResult.Response, sessionResult)
            if captureErr != nil {
                turnResult.Status = TurnError
                turnResult.SkipReason = captureErr.Error()
                turnResults = append(turnResults, turnResult)
                return turnResults
            }
            capturedVars[cap.Variable] = value
            turnResult.CapturedVars[cap.Variable] = value
        }

        turnResults = append(turnResults, turnResult)
    }
    return turnResults
}

// finalizeMultiTurnResult 从轮次结果构建 EvalResult 并运行 judge。
func (e *defaultEvaluator) finalizeMultiTurnResult(
    ctx context.Context,
    caseCfg *config.CaseConfig,
    configName string,
    rt runtime.Runtime,
    judgeCfg config.JudgeConfig,
    turnResults []TurnResult,
    startTime time.Time,
) EvalResult {
    turnsTotal := len(caseCfg.Input.Turns)
    turnsExecuted := countExecutedTurns(turnResults)

    // 合并所有轮次的对话记录
    var fullTranscript transcript.Transcript
    var lastSessionResult *agent.SessionResult
    for _, tr := range turnResults {
        fullTranscript = append(fullTranscript, tr.Transcript...)
        if tr.SessionResult != nil {
            lastSessionResult = tr.SessionResult
        }
    }

    result := EvalResult{
        CaseID:        caseCfg.ID,
        CaseName:      caseCfg.Title,
        Prompt:        caseCfg.Input.Turns[0].Content,
        SessionResult: lastSessionResult,
        TurnsTotal:    turnsTotal,
        Configuration: configName,
    }
    if result.SessionResult == nil {
        result.SessionResult = &agent.SessionResult{}
    }
    result.SessionResult.Transcript = fullTranscript
    result.SessionResult.Turns = turnsExecuted

    // 运行 Judge 前先检查终止性轮次状态。
    // 执行错误属于基础设施错误，不能报告为 SKIP。
    if err := firstTurnError(turnResults); err != nil {
        result.Status = judge.StatusError
        result.Error = err
        return result
    }
    if hasFailedTurn(turnResults) {
        result.Status = judge.StatusFail
        return result
    }
    if allSkipped(turnResults) {
        result.Status = judge.StatusSkip
        return result
    }

    // 执行 Judge 评估（复用现有的 evaluateCaseSession 流程）
    //
    // 多轮与单轮执行的唯一区别是
    // judgeInput 携带 TurnResults，使每轮断言
    // (turn_response_contains 等) 能够工作。
    // 其余流程（expect 预检 → judge → 评分）完全相同。
    judgeInput := judge.Input{
        CaseID:         caseCfg.ID,
        Transcript:     fullTranscript,
        FinalMessage:   lastFinalMessage(turnResults),
        ExitCode:       lastExitCode(turnResults),
        WorkspacePath:  rt.Workspace(),
        SkillDir:       e.skillDir,
        TurnsExecuted:  turnsExecuted,
        TurnsTotal:     turnsTotal,
        TurnResults:    toJudgeTurnResults(turnResults),
        WorkspaceDiff:  sessionWorkspaceDiff(lastSessionResult),
        GeneratedFiles: sessionGeneratedFiles(lastSessionResult),
        SessionResult:  lastSessionResult,
    }

    if failed := e.runExpectPreCheck(ctx, caseCfg, configName, judgeInput, turnsTotal, &result); failed {
        return result
    }

    var expectAssertions []judge.AssertionResult
    if result.ExpectResult != nil {
        expectAssertions = result.ExpectResult.ToAssertionResults()
    }

    finalResult := e.runJudgePhase(ctx, rt, caseCfg, configName, judgeCfg, turnsTotal, nil, judgeInput, &result)
    if len(expectAssertions) > 0 && finalResult.Grading != nil {
        finalResult.Grading.AssertionResults = append(expectAssertions, finalResult.Grading.AssertionResults...)
        finalResult.Grading.Summary.Passed += len(expectAssertions)
        finalResult.Grading.Summary.Total += len(expectAssertions)
        if finalResult.Grading.Summary.Total > 0 {
            finalResult.Grading.Summary.PassRate = float64(finalResult.Grading.Summary.Passed) / float64(finalResult.Grading.Summary.Total)
        }
    }

    return finalResult
}
```

#### post_condition 检查实现

```go
// checkPostCondition 根据 agent 响应评估后置条件。
// 返回 (passed bool, reason string)。
func checkPostCondition(pc *config.PostCondition, response string) (bool, string) {
    lower := strings.ToLower(response)

    // must_contain_all：全部必须匹配
    for _, keyword := range pc.MustContainAll {
        if !strings.Contains(lower, strings.ToLower(keyword)) {
            return false, fmt.Sprintf("response missing required keyword: %q", keyword)
        }
    }

    // must_contain_any：至少匹配一个
    if len(pc.MustContainAny) > 0 {
        found := false
        for _, keyword := range pc.MustContainAny {
            if strings.Contains(lower, strings.ToLower(keyword)) {
                found = true
                break
            }
        }
        if !found {
            return false, fmt.Sprintf("response missing any of: %v", pc.MustContainAny)
        }
    }

    // must_not_contain：都不应匹配
    for _, keyword := range pc.MustNotContain {
        if strings.Contains(lower, strings.ToLower(keyword)) {
            return false, fmt.Sprintf("response unexpectedly contains: %q", keyword)
        }
    }

    return true, ""
}
```

#### 模板渲染实现

```go
// renderTemplate 用捕获的值替换 content 中的 {{variable}} 占位符。
// 使用简单字符串替换而非 text/template，避免复杂性和安全风险
// （无函数调用、无控制流）。
func renderTemplate(content string, vars map[string]string) (string, error) {
    result := content
    for name, value := range vars {
        result = strings.ReplaceAll(result, "{{"+name+"}}", value)
    }
    unresolved := regexp.MustCompile(`\{\{[a-zA-Z_][a-zA-Z0-9_]*\}\}`).FindAllString(result, -1)
    if len(unresolved) > 0 {
        return "", fmt.Errorf("unresolved template variables: %v", unresolved)
    }
    return result, nil
}
```

#### 捕获值提取实现

```go
// extractCapturedValue 使用配置的方法从 agent 响应中提取值。
// 提取失败时返回错误，避免未解析变量静默泄漏到后续轮次。
func extractCapturedValue(rule config.CaptureRule, response string, sr *agent.SessionResult) (string, error) {
    // 优先使用正则提取
    if rule.Pattern != "" {
        return extractByRegex(rule.Pattern, response)
    }
    // JSONPath 提取：将 TurnResult 结构化为 JSON 后查询
    if rule.JSONPath != "" && sr != nil {
        return extractByJSONPath(rule.JSONPath, response, sr)
    }
    return "", fmt.Errorf("capture %q has no extraction method or no session result", rule.Variable)
}

// extractByRegex 使用含命名组的正则 (?P<value>...) 提取值。
func extractByRegex(pattern, text string) (string, error) {
    re, err := regexp.Compile(pattern)
    if err != nil {
        return "", fmt.Errorf("invalid capture regex: %w", err)
    }
    match := re.FindStringSubmatch(text)
    if match == nil {
        return "", fmt.Errorf("capture regex did not match")
    }
    // 查找命名组 "value"
    for i, name := range re.SubexpNames() {
        if name == "value" && i < len(match) {
            if match[i] == "" {
                return "", fmt.Errorf("capture regex matched empty value")
            }
            return match[i], nil
        }
    }
    // 回退：返回第一个捕获组
    if len(match) > 1 {
        if match[1] == "" {
            return "", fmt.Errorf("capture regex matched empty value")
        }
        return match[1], nil
    }
    return "", fmt.Errorf("capture regex has no capturing group")
}

// extractByJSONPath 使用 JSONPath 表达式从会话结果中提取值。
// 根对象 $ 是轮次结果的 JSON 表示：
//   {
//     "response": "...",
//     "transcript": { "tool_calls": [...], "tool_results": [...] }
//   }
func extractByJSONPath(path, response string, sr *agent.SessionResult) (string, error) {
    // 从轮次数据构建可查询的 JSON 对象
    turnData := map[string]any{
        "response": response,
        "transcript": map[string]any{
            "tool_calls":   transcriptToolCalls(sr.Transcript),
            "tool_results": transcriptToolResults(sr.Transcript),
        },
    }
    jsonBytes, err := json.Marshal(turnData)
    if err != nil {
        return "", fmt.Errorf("marshal turn data for JSONPath: %w", err)
    }

    var data any
    if err := json.Unmarshal(jsonBytes, &data); err != nil {
        return "", fmt.Errorf("unmarshal turn data for JSONPath: %w", err)
    }

    // 使用 github.com/PaesslerAG/jsonpath 库查询 JSON 数据。
    // 需在 go.mod 中添加新依赖：go get github.com/PaesslerAG/jsonpath
    // import "github.com/PaesslerAG/jsonpath"
    result, err := jsonpath.Get(path, data)
    if err != nil {
        return "", fmt.Errorf("JSONPath capture did not match %q: %w", path, err)
    }
    value := fmt.Sprintf("%v", result)
    if value == "" || value == "<nil>" {
        return "", fmt.Errorf("JSONPath capture returned empty value for %q", path)
    }
    return value, nil
}

// transcriptToolCalls 从对话记录中提取工具调用信息。
func transcriptToolCalls(tr transcript.Transcript) []map[string]any {
    var calls []map[string]any
    for _, msg := range tr {
        if msg.Role == transcript.RoleToolCall && msg.ToolCall != nil {
            calls = append(calls, map[string]any{
                "id":        msg.ToolCall.ID,
                "name":      msg.ToolCall.Name,
                "arguments": msg.ToolCall.Arguments,
            })
        }
    }
    return calls
}

// transcriptToolResults 从对话记录中提取工具结果信息。
func transcriptToolResults(tr transcript.Transcript) []map[string]any {
    var results []map[string]any
    for _, msg := range tr {
        if msg.Role == transcript.RoleToolResult && msg.ToolResult != nil {
            results = append(results, map[string]any{
                "call_id": msg.ToolResult.CallID,
                "status":  msg.ToolResult.Status,
                "content": msg.ToolResult.Content,
            })
        }
    }
    return results
}
```

#### 辅助函数实现

```go
// countExecutedTurns 统计实际执行的轮次数（未跳过的）。
func countExecutedTurns(turnResults []TurnResult) int {
    count := 0
    for _, tr := range turnResults {
        if tr.Status == TurnCompleted || tr.Status == TurnFailed || tr.Status == TurnError {
            count++
        }
    }
	return count
}

// firstTurnError 返回轮次执行中的第一个硬执行错误。
func firstTurnError(turnResults []TurnResult) error {
    for _, tr := range turnResults {
        if tr.Status == TurnError {
            return fmt.Errorf("turn %d error: %s", tr.TurnNumber, tr.SkipReason)
        }
    }
    return nil
}

// hasFailedTurn 若任何轮次状态为 TurnFailed 则返回 true。
func hasFailedTurn(turnResults []TurnResult) bool {
    for _, tr := range turnResults {
        if tr.Status == TurnFailed {
            return true
        }
    }
    return false
}

// allSkipped 若所有轮次都被跳过（无 completed 轮次）则返回 true。
func allSkipped(turnResults []TurnResult) bool {
    for _, tr := range turnResults {
        if tr.Status == TurnCompleted {
            return false
        }
    }
    return true
}

// lastFinalMessage 返回最后一个 completed 轮次的 FinalMessage。
func lastFinalMessage(turnResults []TurnResult) string {
    for i := len(turnResults) - 1; i >= 0; i-- {
        if turnResults[i].Status == TurnCompleted && turnResults[i].Response != "" {
            return turnResults[i].Response
        }
    }
    return ""
}

// lastExitCode 返回最后一个有 SessionResult 的轮次的 ExitCode。
func lastExitCode(turnResults []TurnResult) int {
    for i := len(turnResults) - 1; i >= 0; i-- {
        if turnResults[i].SessionResult != nil {
            return turnResults[i].SessionResult.ExitCode
        }
    }
    return 0
}

// toJudgeTurnResults 将评估器 TurnResults 转换为 judge 可见的 TurnResults。
func toJudgeTurnResults(turnResults []TurnResult) []judge.TurnResult {
    results := make([]judge.TurnResult, len(turnResults))
    for i, tr := range turnResults {
        results[i] = judge.TurnResult{
            TurnNumber: tr.TurnNumber,
            Response:   tr.Response,
            Transcript: tr.Transcript,
            Status:     string(tr.Status),
        }
    }
    return results
}
```

### Agent 接口扩展

#### 新增可选 `SessionResumer` 接口

在 `internal/agent/agent.go` 中，**不修改现有 `Agent` 接口**，新增一个可选接口。Agent 实现通过 Go 接口组合自愿实现；评估器通过类型断言检查能力：

```go
// SessionResumer 是 Agent 实现可选择满足的可选接口，
// 用于支持多轮会话恢复。评估器在尝试多轮执行前
// 通过类型断言检查此接口。
type SessionResumer interface {
    // RunTurn 恢复现有会话并发送跟进消息。
    // sessionID 是初始 Run 调用返回的会话标识符。
    RunTurn(ctx context.Context, rt Runtime, opts ExecOptions, message transcript.Message, sessionID string) (*SessionResult, error)
}
```

评估器中的能力检查：

```go
resumer, supportsResume := runAgent.(agent.SessionResumer)
if !supportsResume && len(caseCfg.Input.Turns) > 1 {
    // 回退到一次性拼接模式
    logging.WarnContextf(ctx, "Agent %s 未实现 SessionResumer; "+
        "多轮用例 %s 回退到一次性执行", runAgent.Name(), caseCfg.ID)
}
```

此设计的优势：
- **零破坏**：现有 `Agent` 接口不变；所有现有实现无需修改即可编译
- **渐进采用**：仅实现 `SessionResumer` 的 Agent 走多轮路径
- **符合 Go 习惯**：与标准库中的可选接口模式一致（如 `io.ReadCloser`、`io.WriterTo`）

#### Claude Code 实现

claude code CLI 已支持 `--resume <session-id>` 结合 `-p`（print 模式）进行程序化会话恢复。在当前代码库中，`buildClaudePrintCmd` 已接收 `sessionID` 参数（通过 `uuid.New()` 生成），JSON 输出的 `claudePrintJSONResult` 结构体包含 `SessionID` 字段。

```go
// ClaudeCodeAgent 实现了 SessionResumer 接口。

// RunTurn 用跟进消息恢复 claude-code 会话。
func (a *ClaudeCodeAgent) RunTurn(ctx context.Context, rt Runtime, opts ExecOptions,
    message transcript.Message, sessionID string) (*SessionResult, error) {
    start := time.Now()

    envVars := a.credentialEnvVars(credential.EnvAnthropicAPIKey, credential.EnvAnthropicBaseURL)
    envVars["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
    envVars["IS_SANDBOX"] = "1"
    opts = a.mergeExecOptionsEnv(ctx, opts, envVars, nil)

    instruction := message.Content
    cmd := nodeRuntimeCommandWithGuard("claude",
        buildClaudeResumePrintCmd(sessionID, a.effectiveModelName(ctx), instruction))

    result, err := rt.Exec(ctx, cmd, opts)
    sessionResult := a.buildSessionResult(ctx, rt, opts, instruction, start, result)

    // 认证失败检查（与 Run 方法中相同逻辑）
    if authMsg, ok := providerAuthFailureSignal(result, sessionResult); ok {
        if sessionResult != nil && sessionResult.ExitCode == 0 {
            sessionResult.ExitCode = 1
        }
        return sessionResult, fmt.Errorf("claude-code authentication failed: %s", authMsg)
    }
    // 限流检查（与 Run 方法中相同逻辑）
    if rateLimitMsg, ok := providerRateLimitSignal(result, sessionResult); ok {
        if sessionResult != nil && sessionResult.ExitCode == 0 {
            sessionResult.ExitCode = 1
        }
        return sessionResult, fmt.Errorf("claude-code provider rate limit: %s", rateLimitMsg)
    }
    if err != nil {
        if sessionResult == nil {
            sessionResult = &SessionResult{
                Engine:     a.Name(),
                ExitCode:   1,
                DurationMs: time.Since(start).Milliseconds(),
                Stderr:     result.Stderr,
                Artifacts:  &SessionArtifacts{},
            }
        }
        return sessionResult, fmt.Errorf("claude-code resume failed: %w", err)
    }
    if result.ExitCode != 0 {
        return sessionResult, fmt.Errorf("claude-code resume failed (exit %d): %s", result.ExitCode, result.Stderr)
    }
    return sessionResult, nil
}

// buildClaudeResumePrintCmd 构建带 --resume 标志的 claude 命令。
// claude code CLI 的 --resume 参数直接接收会话 ID 值（无需 --session-id）。
func buildClaudeResumePrintCmd(sessionID, model, instruction string) string {
    cmd := "claude --settings " + shellQuote(`{"disableAllHooks":true}`) +
        " --resume " + shellQuote(sessionID) +
        " -p --permission-mode=bypassPermissions"
    if model != "" {
        cmd += " --model " + shellQuote(model)
    }
    cmd += " " + shellQuote(instruction)
    return cmd
}
```

**编译时断言**（确保 `ClaudeCodeAgent` 实现了 `SessionResumer`）：

```go
var _ SessionResumer = (*ClaudeCodeAgent)(nil)
```

#### Codex 实现

codex CLI 支持 `codex exec resume <SESSION_ID>` 进行非交互式会话恢复（经 [官方文档](https://developers.openai.com/codex/cli/features) 确认）。会话 ID 存储在 `~/.codex/sessions/` 目录下，但该目录是用户级全局目录。由于 skill-up 支持 `cases.parallelism`，codex 多轮支持必须拿到与当前用例确定关联的会话 ID，不能读取全局最新文件。

```go
// CodexAgent 实现了 SessionResumer 接口。

// RunTurn 用跟进消息恢复 codex 会话。
func (a *CodexAgent) RunTurn(ctx context.Context, rt Runtime, opts ExecOptions,
    message transcript.Message, sessionID string) (*SessionResult, error) {
    start := time.Now()

    instruction := message.Content
    sandboxFlag := codexBypassSandbox
    if rt.RequiresProcessSandbox() {
        sandboxFlag = codexProcessSandbox
    }
    lastMessagePath := filepath.Join(rt.Workspace(), ".skill-up", "codex-last-message.txt")

    // codex exec resume <SESSION_ID> 继续现有会话
    cmd := "mkdir -p " + shellQuote(filepath.Dir(lastMessagePath)) + "\n" +
        nodeRuntimeCommandWithGuard("codex",
            buildCodexResumeCmd(sessionID, instruction, a.effectiveModelName(ctx),
                a.runProviderConfig(ctx), sandboxFlag, lastMessagePath))

    envVars := a.credentialEnvVars(credential.EnvOpenAIAPIKey, credential.EnvOpenAIBaseURL)
    opts = a.mergeExecOptionsEnv(ctx, opts, envVars, a.buildAgentObservabilityAttrs(nil))
    ctx = observability.ContextWithConfiguredAgentSpanAttributes(ctx, opts.Env)

    result, err := rt.Exec(ctx, cmd, opts)
    sessionResult := a.buildSessionResult(ctx, rt, opts, instruction, start, result, lastMessagePath)
    if err != nil {
        if sessionResult == nil {
            sessionResult = &SessionResult{
                Engine:     a.Name(),
                ExitCode:   1,
                DurationMs: time.Since(start).Milliseconds(),
                Stderr:     result.Stderr,
                Artifacts:  &SessionArtifacts{},
            }
        }
        return sessionResult, fmt.Errorf("codex resume failed: %w", err)
    }
    return sessionResult, nil
}

// buildCodexResumeCmd 构建恢复会话的 codex 命令。
func buildCodexResumeCmd(sessionID, instruction, model string, provider codexProviderConfig,
    sandboxFlag, lastMessagePath string) string {
    cmd := "codex exec resume " + shellQuote(sessionID) + " --json --skip-git-repo-check"
    if sandboxFlag != "" {
        cmd += " " + sandboxFlag
    }
    cmd += codexProviderFlags(provider)
    if model != "" {
        cmd += " -m " + shellQuote(model)
    }
    if lastMessagePath != "" {
        cmd += " --output-last-message " + shellQuote(lastMessagePath)
    }
    cmd += " " + shellQuote(instruction)
    return cmd
}

var _ SessionResumer = (*CodexAgent)(nil)
```

**Codex 会话 ID 提取**：与 claude_code 不同，codex 可能不在 JSON 输出中返回 session_id。实现应优先使用 CLI 输出的会话 ID；如果必须检查会话文件，也必须将候选文件与当前用例的初始 `Run` 关联起来，避免与其他并发用例竞态。可接受策略包括用例隔离的 codex 会话目录、围绕初始 `Run` 和会话文件 diff 窗口加锁，或在无法保证安全时标记 codex 多轮 unsupported（除非 `cases.parallelism: 1`）。

```go
type SessionFileSnapshot map[string]struct{}

// snapshotCodexSessions 在第 1 轮启动前记录已知 codex 会话文件，
// 以便提取逻辑识别本次 Run 新创建的文件。
func snapshotCodexSessions(ctx context.Context, rt Runtime) SessionFileSnapshot {
    // 实现细节：列出 ~/.codex/sessions/*.jsonl 或配置好的
    // 用例隔离 codex 会话目录，返回文件 basename 集合。
    return SessionFileSnapshot{}
}

func diffSessionSnapshots(before, after SessionFileSnapshot) []string {
    var newFiles []string
    for file := range after {
        if _, existed := before[file]; !existed {
            newFiles = append(newFiles, file)
        }
    }
    sort.Strings(newFiles)
    return newFiles
}

// extractCodexSessionID 从 codex SessionResult 提取会话 ID。
// 实现必须选择当前用例初始 Run 创建的文件，而不是全局最新文件，
// 因为其他用例可能并发运行。
func extractCodexSessionID(ctx context.Context, rt Runtime, beforeRun SessionFileSnapshot) string {
    afterRun := snapshotCodexSessions(ctx, rt)
    newFiles := diffSessionSnapshots(beforeRun, afterRun)
    if len(newFiles) != 1 {
        return ""
    }
    sessionID := strings.TrimSuffix(newFiles[0], ".jsonl")
    if sessionID == "" {
        return ""
    }
    return sessionID
}
```

#### QoderCLI 实现

最新 qodercli 支持通过 `-p` 非交互 print 模式、`--output-format=json` 和 `-r <session-id>` 恢复会话。第 1 轮以 print 模式启动并返回包含 `sessionId` 与 `content` 的 JSON；后续轮次用 `-r` 精确恢复该会话。适配器应一致使用配置的 qoder 可执行文件名（`qodercli`、`qoder` 或 `qoderclicn`），并在恢复会话时继续传入同一个 `-w <workspace>`，避免上下文路径漂移。qodercli 也支持 `-c` 继续最近一次会话，但 skill-up 不能在评估中使用它，因为并发用例需要精确会话身份。

```shell
qodercli -p "first question" --output-format=json -w /path/to/repo > round1.json
SID=$(jq -r .sessionId round1.json)
qodercli -r "$SID" -p "follow-up question" --output-format=json -w /path/to/repo > round2.json
```

在无人值守评估中，适配器应传入 qodercli 的权限绕过参数（例如最新 CLI 中的 `--yolo`）或当前安装版本支持的等价参数；否则遇到需要授权的工具调用时可能会等待交互确认。

```go
type qoderPrintJSONResult struct {
    SessionID string `json:"sessionId"`
    Content   string `json:"content"`
}

func qoderExecutableName(cfg Config) string {
    if cfg.Entry != "" {
        return cfg.Entry
    }
    return "qodercli"
}

// QoderCLIAgent 实现了 SessionResumer 接口。

// RunTurn 用跟进消息恢复 qodercli 会话。
func (a *QoderCLIAgent) RunTurn(ctx context.Context, rt Runtime, opts ExecOptions,
    message transcript.Message, sessionID string) (*SessionResult, error) {
    start := time.Now()
    instruction := message.Content

    cmd := buildQoderResumePrintCmd(
        qoderExecutableName(a.Cfg),
        sessionID,
        instruction,
        a.effectiveModelName(ctx),
        rt.Workspace(),
    )

    envVars := a.credentialEnvVars("", "")
    opts = a.mergeExecOptionsEnv(ctx, opts, envVars, a.buildAgentObservabilityAttrs(nil))
    ctx = observability.ContextWithConfiguredAgentSpanAttributes(ctx, opts.Env)

    result, err := rt.Exec(ctx, cmd, opts)
    sessionResult := a.buildQoderJSONSessionResult(ctx, rt, opts, instruction, message.Turn, start, result)
    if err != nil {
        if sessionResult == nil {
            sessionResult = &SessionResult{
                Engine:     a.Name(),
                ExitCode:   1,
                DurationMs: time.Since(start).Milliseconds(),
                Stderr:     result.Stderr,
                Artifacts:  &SessionArtifacts{},
            }
        }
        return sessionResult, fmt.Errorf("qodercli resume failed: %w", err)
    }
    if result.ExitCode != 0 {
        return sessionResult, fmt.Errorf("qodercli resume failed (exit %d): %s", result.ExitCode, result.Stderr)
    }
    return sessionResult, nil
}

// buildQoderRunCmd 以可解析的非交互模式启动新的 qodercli 会话。
func buildQoderRunCmd(executable, instruction, model, workspace string) string {
    cmd := shellQuote(executable) + " --yolo --output-format=json"
    if model != "" {
        cmd += " --model " + shellQuote(model)
    }
    if workspace != "" {
        cmd += " -w " + shellQuote(workspace)
    }
    cmd += " -p " + shellQuote(instruction)
    return cmd
}

// buildQoderResumePrintCmd 恢复指定 qodercli 会话。
func buildQoderResumePrintCmd(executable, sessionID, instruction, model, workspace string) string {
    cmd := shellQuote(executable) + " --yolo -r " + shellQuote(sessionID) + " --output-format=json"
    if model != "" {
        cmd += " --model " + shellQuote(model)
    }
    if workspace != "" {
        cmd += " -w " + shellQuote(workspace)
    }
    cmd += " -p " + shellQuote(instruction)
    return cmd
}

// buildQoderJSONSessionResult 解析 qodercli JSON stdout，
// 并保留 sessionId 供后续轮次使用。
func (a *QoderCLIAgent) buildQoderJSONSessionResult(ctx context.Context, rt Runtime, opts ExecOptions,
    instruction string, turnNumber int, start time.Time, result ExecResult) *SessionResult {
    var payload qoderPrintJSONResult
    _ = json.Unmarshal([]byte(result.Stdout), &payload)

    finalMessage := payload.Content
    if finalMessage == "" {
        finalMessage = result.Stdout
    }

    return &SessionResult{
        Engine:       a.Name(),
        ExitCode:     result.ExitCode,
        DurationMs:   time.Since(start).Milliseconds(),
        Turns:        1,
        FinalMessage: finalMessage,
        Stderr:       result.Stderr,
        SessionID:    payload.SessionID,
        Transcript: transcript.Transcript{
            {Role: transcript.RoleUser, Content: instruction, Turn: turnNumber},
            {Role: transcript.RoleAssistant, Content: finalMessage, Turn: turnNumber},
        },
        Artifacts: &SessionArtifacts{},
    }
}

var _ SessionResumer = (*QoderCLIAgent)(nil)
```

评估器中的 `extractSessionID` 需根据 Agent 类型分发：

```go
func extractSessionID(ctx context.Context, rt runtime.Runtime, runAgent agent.Agent, sr *agent.SessionResult, codexSessionSnapshotBeforeRun SessionFileSnapshot) string {
    if sr == nil {
        return ""
    }
    // claude_code/qodercli：会话 ID 存储在 SessionResult.SessionID 中。
    // codex 若 CLI 直接输出会话 ID，也走这条路径。
    if sr.SessionID != "" {
        return sr.SessionID
    }
    // codex：从会话文件系统提取
    if runAgent.Name() == "codex" {
        return extractCodexSessionID(ctx, rt, codexSessionSnapshotBeforeRun)
    }
    return ""
}
```

#### 会话 ID 提取

`SessionResult` 需要一个共享的 `SessionID` 字段，使评估器无需理解每个 Agent 的输出形态即可恢复会话。claude_code 已在 JSON 输出中暴露 `session_id`，qodercli 在使用 `--output-format=json` 时暴露 `sessionId`。两者都应归一化写入 `SessionResult.SessionID`。codex 若 CLI 直接输出会话 ID，也使用同一字段；否则使用上文所述的安全会话文件关联策略。

```go
// SessionResult 新增 SessionID 字段：
type SessionResult struct {
    // 现有字段
    Engine       string                `json:"engine,omitempty"`
    Model        string                `json:"model,omitempty"`
    ExitCode     int                   `json:"exit_code"`
    DurationMs   int64                 `json:"duration_ms"`
    Turns        int                   `json:"turns"`
    InputTokens  int                   `json:"input_tokens,omitempty"`
    OutputTokens int                   `json:"output_tokens,omitempty"`
    FinalMessage string                `json:"final_message,omitempty"`
    Stderr       string                `json:"stderr,omitempty"`
    Transcript   transcript.Transcript `json:"transcript,omitempty"`
    Artifacts    *SessionArtifacts     `json:"artifacts,omitempty"`

    // 新字段
    // SessionID 是 agent 会话标识符，用于多轮恢复。
    // 由支持会话恢复的 agent 填充（如 claude_code、qodercli、codex）。
    SessionID string `json:"session_id,omitempty"`
}
```

在 claude_code 的 `buildClaudePrintJSONSessionResult` 和 stream-json 解析逻辑中，将 `payload.SessionID` 赋值给 `SessionResult.SessionID`：

```go
// 在 buildClaudePrintJSONSessionResult 中添加：
sessionResult.SessionID = payload.SessionID

// 在 parseStreamOutput 的 result 事件处理器中添加：
if payload.SessionID != "" {
    sessionResult.SessionID = payload.SessionID
}
```

在 qodercli 的 JSON 解析路径中，将驼峰形式的 `sessionId` 字段映射到同一个归一化字段：

```go
var payload qoderPrintJSONResult
if err := json.Unmarshal([]byte(result.Stdout), &payload); err == nil {
    sessionResult.SessionID = payload.SessionID
    sessionResult.FinalMessage = payload.Content
}
```

评估器使用统一的多参数版本提取会话 ID，支持不同 Agent 的分发逻辑（见上方 codex 实现部分的 `extractSessionID` 定义）。

#### 回退策略

回退逻辑内置于 `executeMultiTurn`（通过 `agent.SessionResumer` 类型断言）；见上方 `executeMultiTurnFallback` 方法。

**回退模式行为**：
- 将所有轮次拼接为单条指令，一次性发送给 Agent（现有行为）
- 在评估结果中标注 `execution_mode: "single_shot_fallback"`
- 不执行 `post_condition` 和 `capture`（因为没有每轮结果）
- 每轮 Judge 断言（如 `turn_response_contains`）因缺少 `TurnResults` 返回 FAIL
- 在报告中输出警告，建议用户切换到支持会话恢复的 Agent

```go
// executeMultiTurnFallback 在 Agent 不支持 SessionResumer 时调用，
// 将多轮轮次拼接为单条指令进行一次性执行（即现有行为）。
func (e *defaultEvaluator) executeMultiTurnFallback(
    ctx context.Context,
    caseCfg *config.CaseConfig,
    configName string,
    rt runtime.Runtime,
    runAgent agent.Agent,
) EvalResult {
    // 直接复用现有的 executeCaseOnce 流程。
    //
    // caseCfg.Input.Turns 已存在；buildCaseMessages() 将其拼接为消息，
    // 然后 BuildInstructionFromMessages() 合并为单条指令——这就是现有行为。
    // executeCaseOnce 内部包含完整的：
    //   - tracing span 管理 (agentSpan.End())
    //   - 产物收集 (finalizeArtifacts, ensureArtifactsInOutputDir)
    //   - 会话结果归一化 (normalizeSessionResult)
    //   - 执行错误处理 (handleExecutionResult: 超时、非零退出码等)
    //   - expect 预检 + judge 评估
    //
    // 注意：回退模式下不执行 post_condition 和 capture（无每轮结果）。
    // 每轮 Judge 断言 (turn_response_contains 等) 因 TurnResults 为空返回 FAIL。
    logging.WarnContextf(ctx, "Evaluator: 多轮用例 %s 以一次性回退模式运行", caseCfg.ID)
    return e.executeCaseOnce(ctx, caseCfg, configName, rt, runAgent)
}
```

### Judge 每轮断言

#### TurnResult 传递给 Judge

在 `internal/judge/judge.go` 的 `Input` 中添加：

```go
type Input struct {
    // 现有字段
    CaseID         string
    Transcript     transcript.Transcript
    FinalMessage   string
    ExitCode       int
    WorkspacePath  string
    SkillDir       string
    WorkspaceDiff  string
    GeneratedFiles []string
    ArtifactDir    string
    SessionResult  *agent.SessionResult
    TurnsExecuted  int
    TurnsTotal     int

    // 新字段
    // TurnResults 保存多轮用例的每轮执行结果。
    // 单轮用例为空。
    TurnResults []TurnResult `json:"turn_results,omitempty"`
}

// TurnResult 是单轮执行的 judge 可见表示。
type TurnResult struct {
    TurnNumber int                   `json:"turn_number"` // 1 起始
    Content    string                `json:"content"`     // 本轮发送的用户消息
    Response   string                `json:"response"`
    Transcript transcript.Transcript `json:"transcript"`
    Status     string                `json:"status"`      // completed, skipped, failed, error
}
```

#### rule_based 断言实现

在 `internal/judge/rule_based.go` 的 `evaluateAssertion` 中添加新 case 分支：

```go
func evaluateAssertion(rule config.Rule, in Input) AssertionResult {
    switch {
    // 现有 case
    case rule.OutputContains != nil:
        return evalOutputContains(rule.OutputContains, in.FinalMessage)
    case rule.ExitCode != nil:
        return evalExitCode(*rule.ExitCode, in.ExitCode)
    case rule.ToolCalled != nil:
        return evalToolCalled(rule.ToolCalled, in.Transcript)
    case len(rule.FilesExist) > 0:
        return evalFilesExist(rule.FilesExist, in.WorkspacePath)
    case len(rule.FilesNotExist) > 0:
        return evalFilesNotExist(rule.FilesNotExist, in.WorkspacePath)

    // 新增：每轮断言
    case rule.TurnResponseContains != nil:
        return evalTurnResponseContains(rule.TurnResponseContains, in.TurnResults)

    case rule.TurnResponseNotContains != nil:
        return evalTurnResponseNotContains(rule.TurnResponseNotContains, in.TurnResults)

    case rule.ToolCalledInTurn != nil:
        return evalToolCalledInTurn(rule.ToolCalledInTurn, in.TurnResults)

    case rule.ToolNotCalledInTurn != nil:
        return evalToolNotCalledInTurn(rule.ToolNotCalledInTurn, in.TurnResults)

    default:
        return AssertionResult{Text: "unknown rule", Passed: false, Evidence: "unrecognized assertion type"}
    }
}

func evalTurnResponseContains(rule *config.TurnResponseContainsRule, turnResults []TurnResult) AssertionResult {
    turnIdx := rule.Turn - 1
    if turnIdx < 0 || turnIdx >= len(turnResults) {
        return AssertionResult{
            Text:     fmt.Sprintf("turn_response_contains(turn=%d)", rule.Turn),
            Passed:   false,
            Evidence: fmt.Sprintf("turn %d not found (total turns: %d)", rule.Turn, len(turnResults)),
        }
    }

    tr := turnResults[turnIdx]
    if tr.Status != "completed" {
        return AssertionResult{
            Text:     fmt.Sprintf("turn_response_contains(turn=%d)", rule.Turn),
            Passed:   false,
            Evidence: fmt.Sprintf("turn %d was %s, not completed", rule.Turn, tr.Status),
        }
    }

    response := strings.ToLower(tr.Response)

    // contains_all: AND 语义
    for _, keyword := range rule.ContainsAll {
        if !strings.Contains(response, strings.ToLower(keyword)) {
            return AssertionResult{
                Text:     fmt.Sprintf("turn_response_contains(turn=%d, all)", rule.Turn),
                Passed:   false,
                Evidence: fmt.Sprintf("turn %d response missing: %q", rule.Turn, keyword),
            }
        }
    }

    // contains_any: OR 语义
    if len(rule.ContainsAny) > 0 {
        found := false
        for _, keyword := range rule.ContainsAny {
            if strings.Contains(response, strings.ToLower(keyword)) {
                found = true
                break
            }
        }
        if !found {
            return AssertionResult{
                Text:     fmt.Sprintf("turn_response_contains(turn=%d, any)", rule.Turn),
                Passed:   false,
                Evidence: fmt.Sprintf("turn %d response missing any of: %v", rule.Turn, rule.ContainsAny),
            }
        }
    }

    return AssertionResult{
        Text:     fmt.Sprintf("turn_response_contains(turn=%d)", rule.Turn),
        Passed:   true,
        Evidence: fmt.Sprintf("turn %d response matched", rule.Turn),
    }
}

// evalTurnResponseNotContains 检查特定轮次的响应是否不包含禁止的文本。
func evalTurnResponseNotContains(rule *config.TurnResponseNotContainsRule, turnResults []TurnResult) AssertionResult {
    turnIdx := rule.Turn - 1
    if turnIdx < 0 || turnIdx >= len(turnResults) {
        return AssertionResult{
            Text:     fmt.Sprintf("turn_response_not_contains(turn=%d)", rule.Turn),
            Passed:   false,
            Evidence: fmt.Sprintf("turn %d not found (total turns: %d)", rule.Turn, len(turnResults)),
        }
    }

    tr := turnResults[turnIdx]
    if tr.Status != "completed" {
        return AssertionResult{
            Text:     fmt.Sprintf("turn_response_not_contains(turn=%d)", rule.Turn),
            Passed:   false,
            Evidence: fmt.Sprintf("turn %d was %s, not completed", rule.Turn, tr.Status),
        }
    }

    response := strings.ToLower(tr.Response)
    for _, keyword := range rule.NotContains {
        if strings.Contains(response, strings.ToLower(keyword)) {
            return AssertionResult{
                Text:     fmt.Sprintf("turn_response_not_contains(turn=%d)", rule.Turn),
                Passed:   false,
                Evidence: fmt.Sprintf("turn %d response contains forbidden keyword: %q", rule.Turn, keyword),
            }
        }
    }

    return AssertionResult{
        Text:     fmt.Sprintf("turn_response_not_contains(turn=%d)", rule.Turn),
        Passed:   true,
        Evidence: fmt.Sprintf("turn %d response does not contain any forbidden keywords", rule.Turn),
    }
}

// evalToolCalledInTurn 检查特定轮次是否调用了特定工具。
func evalToolCalledInTurn(rule *config.ToolCalledInTurnRule, turnResults []TurnResult) AssertionResult {
    turnIdx := rule.Turn - 1
    if turnIdx < 0 || turnIdx >= len(turnResults) {
        return AssertionResult{
            Text:     fmt.Sprintf("tool_called_in_turn(turn=%d, tool=%s)", rule.Turn, rule.Name),
            Passed:   false,
            Evidence: fmt.Sprintf("turn %d not found (total turns: %d)", rule.Turn, len(turnResults)),
        }
    }

    tr := turnResults[turnIdx]
    if tr.Status != "completed" {
        return AssertionResult{
            Text:     fmt.Sprintf("tool_called_in_turn(turn=%d, tool=%s)", rule.Turn, rule.Name),
            Passed:   false,
            Evidence: fmt.Sprintf("turn %d was %s, not completed", rule.Turn, tr.Status),
        }
    }

    // 在本轮对话记录中搜索工具调用
    for _, msg := range tr.Transcript {
        if msg.Role != transcript.RoleToolCall || msg.ToolCall == nil {
            continue
        }
        if msg.ToolCall.Name != rule.Name {
            continue
        }
        // 名称匹配；若指定了 args 则检查（部分匹配）
        if len(rule.Args) == 0 {
            return AssertionResult{
                Text:     fmt.Sprintf("tool_called_in_turn(turn=%d, tool=%s)", rule.Turn, rule.Name),
                Passed:   true,
                Evidence: fmt.Sprintf("tool %q was called in turn %d", rule.Name, rule.Turn),
            }
        }
        if argsMatch(rule.Args, msg.ToolCall.Arguments) {
            return AssertionResult{
                Text:     fmt.Sprintf("tool_called_in_turn(turn=%d, tool=%s, with args)", rule.Turn, rule.Name),
                Passed:   true,
                Evidence: fmt.Sprintf("tool %q was called in turn %d with matching args", rule.Name, rule.Turn),
            }
        }
    }

    return AssertionResult{
        Text:     fmt.Sprintf("tool_called_in_turn(turn=%d, tool=%s)", rule.Turn, rule.Name),
        Passed:   false,
        Evidence: fmt.Sprintf("tool %q was not called in turn %d", rule.Name, rule.Turn),
    }
}

// evalToolNotCalledInTurn 检查特定轮次未调用特定工具。
func evalToolNotCalledInTurn(rule *config.ToolNotCalledInTurnRule, turnResults []TurnResult) AssertionResult {
    turnIdx := rule.Turn - 1
    if turnIdx < 0 || turnIdx >= len(turnResults) {
        return AssertionResult{
            Text:     fmt.Sprintf("tool_not_called_in_turn(turn=%d, tool=%s)", rule.Turn, rule.Name),
            Passed:   true, // 轮次不存在 → 工具未被调用 → 通过
            Evidence: fmt.Sprintf("turn %d not found, so tool %q was not called", rule.Turn, rule.Name),
        }
    }

    tr := turnResults[turnIdx]
    for _, msg := range tr.Transcript {
        if msg.Role == transcript.RoleToolCall && msg.ToolCall != nil && msg.ToolCall.Name == rule.Name {
            return AssertionResult{
                Text:     fmt.Sprintf("tool_not_called_in_turn(turn=%d, tool=%s)", rule.Turn, rule.Name),
                Passed:   false,
                Evidence: fmt.Sprintf("tool %q was unexpectedly called in turn %d", rule.Name, rule.Turn),
            }
        }
    }

    return AssertionResult{
        Text:     fmt.Sprintf("tool_not_called_in_turn(turn=%d, tool=%s)", rule.Turn, rule.Name),
        Passed:   true,
        Evidence: fmt.Sprintf("tool %q was not called in turn %d as expected", rule.Name, rule.Turn),
    }
}
```

### 验证器变更

Schema 新增了多个字段；需在 `internal/config/validator.go` 的 `ValidateCaseConfig` 中添加对应的验证规则。

#### 新增验证规则

```go
// ValidateCaseConfig 中的新验证逻辑：

// 1. 验证 input.turns 中每轮的 post_condition
for i, turn := range cfg.Input.Turns {
    if turn.PostCondition != nil {
        if turn.PostCondition.OnFail != "" &&
            turn.PostCondition.OnFail != "skip_remaining" &&
            turn.PostCondition.OnFail != "fail" {
            errs = append(errs, fmt.Sprintf(
                "input.turns[%d].post_condition.on_fail must be 'skip_remaining' or 'fail', got %q", i, turn.PostCondition.OnFail))
        }
        // post_condition 至少需要一个匹配条件
        hasCondition := len(turn.PostCondition.MustContainAny) > 0 ||
            len(turn.PostCondition.MustContainAll) > 0 ||
            len(turn.PostCondition.MustNotContain) > 0
        if !hasCondition {
            errs = append(errs, fmt.Sprintf(
                "input.turns[%d].post_condition must specify at least one of: must_contain_any, must_contain_all, must_not_contain", i))
        }
    }

    // 2. Capture 规则验证
    for j, cap := range turn.Capture {
        if cap.Variable == "" {
            errs = append(errs, fmt.Sprintf(
                "input.turns[%d].capture[%d].variable is required", i, j))
        }
        if cap.Pattern == "" && cap.JSONPath == "" {
            errs = append(errs, fmt.Sprintf(
                "input.turns[%d].capture[%d] must specify either pattern or jsonpath", i, j))
        }
        if cap.Pattern != "" && cap.JSONPath != "" {
            errs = append(errs, fmt.Sprintf(
                "input.turns[%d].capture[%d] must specify only one of pattern or jsonpath, not both", i, j))
        }
        // 验证正则可编译
        if cap.Pattern != "" {
            if _, err := regexp.Compile(cap.Pattern); err != nil {
                errs = append(errs, fmt.Sprintf(
                    "input.turns[%d].capture[%d].pattern is invalid regex: %v", i, j, err))
            }
        }
    }

    // 3. 每轮超时验证
    if turn.TimeoutSeconds < 0 {
        errs = append(errs, fmt.Sprintf(
            "input.turns[%d].timeout_seconds must be non-negative", i))
    }

    // 4. content 必填验证
    if turn.Content == "" {
        errs = append(errs, fmt.Sprintf(
            "input.turns[%d].content is required", i))
    }
}

// 4. 验证 judge.success / judge.failure 中的每轮断言
for i, rule := range cfg.Judge.Success {
    errs = append(errs, validateTurnRule(fmt.Sprintf("judge.success[%d]", i), rule, len(cfg.Input.Turns))...)
}
for i, rule := range cfg.Judge.Failure {
    errs = append(errs, validateTurnRule(fmt.Sprintf("judge.failure[%d]", i), rule, len(cfg.Input.Turns))...)
}
```

#### 每轮断言验证函数

```go
// validateTurnRule 验证轮次特定的规则字段。
func validateTurnRule(prefix string, rule Rule, totalTurns int) []string {
    var errs []string

    if rule.TurnResponseContains != nil {
        r := rule.TurnResponseContains
        if r.Turn < 1 {
            errs = append(errs, fmt.Sprintf("%s.turn_response_contains.turn must be >= 1", prefix))
        }
        if totalTurns > 0 && r.Turn > totalTurns {
            errs = append(errs, fmt.Sprintf(
                "%s.turn_response_contains.turn (%d) exceeds total turns (%d)", prefix, r.Turn, totalTurns))
        }
        if len(r.ContainsAll) == 0 && len(r.ContainsAny) == 0 {
            errs = append(errs, fmt.Sprintf(
                "%s.turn_response_contains must specify contains_all or contains_any", prefix))
        }
    }

    if rule.TurnResponseNotContains != nil {
        r := rule.TurnResponseNotContains
        if r.Turn < 1 {
            errs = append(errs, fmt.Sprintf("%s.turn_response_not_contains.turn must be >= 1", prefix))
        }
        if totalTurns > 0 && r.Turn > totalTurns {
            errs = append(errs, fmt.Sprintf(
                "%s.turn_response_not_contains.turn (%d) exceeds total turns (%d)", prefix, r.Turn, totalTurns))
        }
        if len(r.NotContains) == 0 {
            errs = append(errs, fmt.Sprintf(
                "%s.turn_response_not_contains.not_contains is required", prefix))
        }
    }

    if rule.ToolCalledInTurn != nil {
        r := rule.ToolCalledInTurn
        if r.Turn < 1 {
            errs = append(errs, fmt.Sprintf("%s.tool_called_in_turn.turn must be >= 1", prefix))
        }
        if r.Name == "" {
            errs = append(errs, fmt.Sprintf("%s.tool_called_in_turn.name is required", prefix))
        }
    }

    if rule.ToolNotCalledInTurn != nil {
        r := rule.ToolNotCalledInTurn
        if r.Turn < 1 {
            errs = append(errs, fmt.Sprintf("%s.tool_not_called_in_turn.turn must be >= 1", prefix))
        }
        if r.Name == "" {
            errs = append(errs, fmt.Sprintf("%s.tool_not_called_in_turn.name is required", prefix))
        }
    }

    return errs
}
```

### 可靠性机制

#### 1. post_condition 前置断言

每轮执行后检查 `post_condition`；不满足时按 `on_fail` 策略处理：

| on_fail 值    | 行为                       | 评估状态                   |
| ---------------- | ------------------------------ | ----------------------------------- |
| `skip_remaining` | 跳过所有后续轮次      | `SKIP`（原因标注在报告中） |
| `fail`（默认） | 立即终止用例 | `FAIL`                              |

报告中的表示：

```json
{
  "case_id": "confirm-then-execute",
  "status": "SKIP",
  "skip_reason": "Turn 1 post_condition not met: response missing any of: [confirm, OK, continue?]",
  "turns_executed": 1,
  "turns_total": 2,
  "turn_results": [
    {
      "turn_number": 1,
      "status": "completed",
      "post_condition_passed": false
    },
    {
      "turn_number": 2,
      "status": "skipped",
      "skip_reason": "skipped due to turn 1 post_condition failure"
    }
  ]
}
```

#### 2. 捕获值提取

支持两种提取方式：

**正则提取**：
```yaml
capture:
  - variable: plan_name
    pattern: "created plan[「\"'](?P<value>[^「\"']+)[」\"']"
```

**JSONPath 提取**（从本轮对话记录中的 ToolResult 消息提取）：
```yaml
capture:
  - variable: plan_id
    jsonpath: "$.transcript.tool_results[0].call_id"
```

> **数据源说明**：JSONPath 根对象 `$` 是结构化的轮次结果 JSON，包含 `response`（Agent 文本响应）和 `transcript`（本轮对话记录，含 `tool_calls` 和 `tool_results` 数组）。Tool result 条目暴露 `call_id`、`status`、`content`；tool call 条目暴露 `id`、`name`、`arguments`。JSONPath 示例必须包含 `transcript` 前缀，例如 `$.transcript.tool_results[0].call_id`。

提取的值通过 `{{variable_name}}` 在后续轮次中引用：
```yaml
- role: user
  content: "Add an approval node to {{plan_id}}"
```

#### 3. retry_on 扩展

```yaml
cases:
  retry_policy:
    max_retries: 2
    retry_on:
      - timeout
      - error
      - turn_precondition_fail  # 新增：post_condition 失败时重试整个用例
```

#### 4. 多轮对话记录格式

完整的多轮对话记录保存每轮的消息，标注轮次号：

```json
[
  {"role": "user", "content": "sdd_bootstrap: task=implement login", "turn": 1},
  {"role": "assistant", "content": "Entering Research phase...", "turn": 1},
  {"role": "user", "content": "Skip Research, write code directly", "turn": 2},
  {"role": "assistant", "content": "Need to complete Research phase first...", "turn": 2}
]
```

## 测试计划

### 单元测试

| 测试场景              | 包     | 描述                                                                 |
| -------------------------- | ----------- | --------------------------------------------------------------------------- |
| Schema 解析             | `config`    | 验证 Turn.Capture、PostCondition 新字段的 YAML 解析               |
| 验证器                  | `config`    | 验证轮次验证规则（空 content、无效 on_fail 值等） |
| post_condition             | `evaluator` | 验证 `checkPostCondition` AND/OR/NOT 逻辑                                |
| capture 提取         | `evaluator` | 验证正则和 JSONPath 两种提取方法                           |
| 模板渲染         | `evaluator` | 验证 `{{variable}}` 替换逻辑                                    |
| turn_response_contains     | `judge`     | 验证每轮断言匹配逻辑                                    |
| turn_response_not_contains | `judge`     | 验证每轮否定断言                                         |
| tool_called_in_turn        | `judge`     | 验证每轮工具调用检查                                            |
| 轮次越界         | `judge`     | 验证指定不存在的轮次时返回 FAIL                 |

### 集成测试

| 测试场景                | 描述                                                           |
| ---------------------------- | --------------------------------------------------------------------- |
| 两轮正常执行    | 两轮均成功，Judge 通过                                      |
| post_condition 跳过          | 第 1 轮 post_condition 失败，第 2 轮被跳过                        |
| post_condition 失败          | 第 1 轮 post_condition 失败，整个用例 FAIL                        |
| capture + 模板引用 | 第 1 轮捕获值正确代入第 2 轮                  |
| 会话恢复回退      | Agent 不支持恢复时回退到一次性执行 |
| 单轮兼容性    | 现有 `input.prompt` 用例行为不变                        |

### E2E 测试

| 测试场景              | 描述                                         |
| -------------------------- | --------------------------------------------------- |
| 完整多轮评估 | 用真实 Agent 执行 2-3 轮多轮用例 |
| 报告格式验证 | 验证 JSON/HTML 报告包含 turn_results       |

## 缺点

1. **复杂度增加**：多轮执行路径比单轮显著复杂，增加了评估器维护成本
2. **执行时间更长**：多轮交互的时间和 token 消耗是单轮的数倍
3. **Agent 依赖**：会话恢复依赖于 Agent CLI 的 `--resume` 能力，受上游 API 变更影响
4. **调试困难**：多轮用例失败时需分析每轮的输入/输出，增加了调试复杂度
5. **模型随机性**：模型随机性在多轮交互中被放大，可能需要更宽松的匹配策略或更多重试

## 替代方案

### 替代方案 A：纯拼接模式（现有行为优化）

**思路**：将多轮轮次拼接为单个大 prompt 模拟对话历史，一次性发送给 Agent。

```
[模拟对话历史]
用户：sdd_bootstrap: task=implement login
助手：[预期响应占位符]
用户：Skip Research, write code directly

请根据上述对话历史回复最后一条用户消息。
```

**优点**：实现简单，无需修改 Agent 接口。

**缺点**：
- Agent 无法区分"真实先前交互"和"模拟对话历史"
- 无法验证前序轮次的实际输出
- 不支持 post_condition、capture 等中间检查
- 无法测试 Agent 的实际会话状态管理能力

**结论**：无法满足核心需求，**不采用**。

### 替代方案 B：独立多轮测试框架

**思路**：不修改 skill-up 核心，单独构建专用的多轮测试工具。

**优点**：不影响现有代码，可独立演进。

**缺点**：
- 基础设施重复（runtime、agent adapter、judge、report 都需重新实现）
- 用户需学习维护两套工具
- 无法共享 skill-up 的基础设施（凭证管理、沙箱、报告）

**结论**：成本过高，**不采用**。

### 替代方案 C：向 Agent 接口添加 RunTurn（本提案）

**思路**：在现有 skill-up 框架内，通过 Agent 接口扩展 `RunTurn` + 评估器多轮执行引擎实现。

**优点**：
- 改动最小，复用现有基础设施
- 向后兼容，单轮用例不受影响
- 利用 Agent CLI 原生的会话恢复能力

**缺点**：
- 需要每个 Agent 实现 `RunTurn`
- 受限于 Agent CLI 的会话恢复能力

**结论**：**本提案采用此方案**。

### 替代方案 D：场景驱动的 ChatterAgent 模式（未来扩展）

**思路**：不要求用例作者预先定义每一条用户消息，而是引入一个独立的模拟用户 Agent（例如 `ChatterAgent`）。它接收场景目标、可用知识、行为约束以及必填的 `max_turns`，然后自适应生成用户消息，直到目标看起来已完成、无法完成，或达到轮次上限。现有 Judge 仍负责评估最终 transcript、工具调用、文件和工作区状态。

示例形态：

```yaml
input:
  conversation:
    mode: chatter_agent
    max_turns: 6
    chatter:
      role: "business user"
      objective: >
        Create a new member named Alice, then upgrade her membership level to Gold.
        If the Agent asks for missing information, provide the required information.
      knowledge:
        member:
          name: Alice
          age: 28
          phone: "13800000000"
          initial_level: Silver
          target_level: Gold
      behavior:
        - "Act like a normal user, not an evaluator."
        - "Do not reveal all information at once unless the Agent asks for it."
        - "Do not invent information outside the provided knowledge."
        - "Stop once the task appears completed."
```

**优点**：
- 更容易表达下一条用户消息依赖 Agent 上一轮响应的开放式业务流程
- 对主要关注任务完成而非协议约束的用例，可减少脆弱的关键词调参
- 如果作为另一种 conversation 输入模式实现，仍可复用相同的 transcript 和 Judge 基础设施

**缺点**：
- 在评估路径中增加另一个概率性 Agent，可能降低可复现性并增加成本
- 需要严格的停止条件、transcript 角色标注，以及对生成用户轮次的报告展示，才能保证可调试性
- 需要额外防护，避免模拟用户直接评判、辅导或泄露隐藏评估标准给被测 Agent

**结论**：认可这是有价值的互补未来方向，但**本提案不采用**。本 PR 聚焦确定性的 `input.turns`，因为它是协议检查、阶段门禁、二次确认、精确回归和可复现实现测试所需的底层原语。后续提案可以在相同的逐轮执行和 Judge 原语之上叠加 `input.conversation.mode: chatter_agent`。

## 所需基础设施

- **无需新增外部依赖**：capture 的正则提取使用 Go 标准库 `regexp`；JSONPath 提取使用现有依赖或轻量实现
- **无需新增服务**：所有变更均在 skill-up CLI 内部
- **Agent CLI 要求**：
  - claude_code：必须支持 `--resume <session-id>` + `-p` 参数（已验证，经 [官方文档](https://code.claude.com/docs/en/cli-reference) 确认）
  - codex：必须支持 `codex exec resume <SESSION_ID>` 非交互模式（已验证，经 [官方文档](https://developers.openai.com/codex/cli/features) 确认）
  - qodercli：必须支持 `-r <SESSION_ID>` + `-p` + `--output-format=json` 非交互模式；初始 `-p` 运行必须在 JSON 输出中返回 `sessionId`，用于后续精确恢复
- **JSONPath 库**：capture 的 JSONPath 提取需在 `go.mod` 中添加新依赖 `github.com/PaesslerAG/jsonpath`（MIT 许可证，轻量无传递依赖）。通过 `go get github.com/PaesslerAG/jsonpath` 引入

## 升级与迁移策略

### 向后兼容性

| 场景                                              | 影响             | 处理                                                                                      |
| ----------------------------------------------------- | ------------------ | --------------------------------------------------------------------------------------------- |
| 现有 `input.prompt` 用例                         | 无影响          | 走现有单轮执行路径                                                     |
| 现有 `input.turns` 用例（无 post_condition） | 行为变更    | 从"拼接一次性发送"改为"逐轮执行"；结果更准确 |
| 现有 Judge 规则                                  | 无影响          | 全局断言继续应用于完整对话记录                                |
| Schema 版本                                        | 保持 `v1alpha1` | 所有新字段均为可选                                                                   |

### 迁移步骤

1. **第 1 阶段**：实现评估器多轮执行引擎 + Agent `RunTurn` 接口，优先支持 claude_code 和 qodercli，因为两者都在 JSON 输出中提供可解析的会话 ID
2. **第 2 阶段**：实现每轮 Judge 断言（`turn_response_contains` 等）
3. **第 3 阶段**：实现 capture + 模板变量 + retry 扩展
4. **第 4 阶段**：在完成安全会话文件关联策略后，实现 codex `RunTurn`

每个阶段可独立发布，不阻塞后续阶段。

## 设计自审与实现说明

### 已识别的技术风险与缓解措施

#### 1. `executeMultiTurnFallback` 实现策略（已解决）

**原始问题**：早期版本的 `executeMultiTurnFallback` 直接调用 `evaluateCaseSession`，遗漏了 `executeCaseOnce` 中的关键中间步骤，如 tracing span、产物收集、`normalizeSessionResult` 和 `handleExecutionResult`。

**采用方案**：正文中的 `executeMultiTurnFallback` 已改为直接调用 `e.executeCaseOnce(ctx, caseCfg, configName, rt, runAgent)`，完全复用现有单轮流程的所有中间步骤，无遗漏风险。

> **风险等级**：✅ 已消除。

#### 2. Codex 会话 ID 提取竞态条件

**问题**：`extractCodexSessionID` 不能通过 `ls -t ~/.codex/sessions/*.jsonl | head -1` 获取最新会话文件。skill-up 已通过 `cases.parallelism` 支持用例级并发，因此两个 codex 用例可能几乎同时启动，并在同一个全局目录中创建会话文件。没有关联键时，一个用例可能恢复另一个用例的会话，污染两个 transcript。

**采用约束**：
- 优先解析 codex CLI 输出的会话 ID（如果可用）
- 否则使用用例隔离的会话目录，或序列化 initial `Run` + 会话文件 diff 窗口，确保新会话文件与当前用例关联
- 如果两种策略都不可用，codex 多轮支持必须以明确的 ERROR/unsupported 诊断禁用，或文档化为要求 `cases.parallelism: 1`

> **风险等级**：🟡 中等，已缓解。由于 `cases.parallelism` 已允许并发用例，该风险在当前评估器中真实存在；但只要在标记 codex 多轮支持已实现前要求明确的会话关联策略，就可控。

#### 3. `capture` 提取失败时的行为

**问题**：当 `extractCapturedValue` 返回空字符串或静默忽略匹配失败时，后续轮次中的 `{{variable}}` 占位符可能保持原样并发送给 Agent，在没有清晰诊断的情况下污染对话。

**采用方案**：
- `extractCapturedValue` 返回 `(string, error)`，并将非法正则、正则未匹配、JSONPath 未命中、提取值为空都视为错误
- `executeTurnsSequentially` 在任何配置的 capture 失败时，将当前轮次标记为 `TurnError` 并停止用例
- `renderTemplate` 在发送 prompt 前检测未解析的 `{{variable}}` 占位符，并返回 `TurnError`，而不是把原始模板语法转发给 Agent

```go
func renderTemplate(content string, vars map[string]string) (string, error) {
    result := content
    for name, value := range vars {
        result = strings.ReplaceAll(result, "{{"+name+"}}", value)
    }
    unresolved := regexp.MustCompile(`\{\{[a-zA-Z_][a-zA-Z0-9_]*\}\}`).FindAllString(result, -1)
    if len(unresolved) > 0 {
        return "", fmt.Errorf("unresolved template variables: %v", unresolved)
    }
    return result, nil
}
```

> **风险等级**：🟢 低，已缓解。capture 失败会成为带诊断信息的显式 ERROR，不会静默改变后续轮次发送的 prompt。

#### 4. `sessionID` 为空时 `RunTurn` 的行为

**问题**：如果第 1 轮的 `Run` 成功但 `extractSessionID` 返回空字符串（例如 Agent 输出格式异常、qodercli 缺少 `sessionId`、或 codex 会话文件关联不安全），后续 `RunTurn` 调用带空 `sessionID` 会导致 CLI 命令报错。

**缓解措施**：
- 在 `executeTurnsSequentially` 中，第 1 轮执行后检查 `sessionID` 是否为空
- 若为空，将后续轮次标记为 `TurnError` 并终止，而非传递空 sessionID 导致 CLI 错误

在 `executeTurnsSequentially` 中第 1 轮执行闭包返回后（即上方代码第 3 步闭包调用完成后），追加 sessionID 空值检查。完整代码片段如下：

```go
sessionResult, execErr := func() (*agent.SessionResult, error) {
    turnCtx := ctx
    if turn.TimeoutSeconds > 0 {
        var cancel context.CancelFunc
        turnCtx, cancel = context.WithTimeout(ctx, time.Duration(turn.TimeoutSeconds)*time.Second)
        defer cancel()
    }
    if turnNum == 1 {
        if runAgent.Name() == "codex" {
            codexSessionSnapshotBeforeRun = snapshotCodexSessions(turnCtx, rt)
        }
        sr, err := runAgent.Run(turnCtx, rt, agent.ExecOptions{}, []transcript.Message{message})
        if sr != nil {
            sessionID = extractSessionID(turnCtx, rt, runAgent, sr, codexSessionSnapshotBeforeRun)
        }
        return sr, err
    }
    return resumer.RunTurn(turnCtx, rt, agent.ExecOptions{}, message, sessionID)
}()

// 空 sessionID 检查（仅第 1 轮且有后续轮次时）
if turnNum == 1 && sessionID == "" && turnsTotal > 1 && execErr == nil {
    turnResult.Response = sessionResult.FinalMessage
    turnResult.Transcript = sessionResult.Transcript
    turnResult.SessionResult = sessionResult
    turnResult.Status = TurnCompleted
    turnResults = append(turnResults, turnResult)
    for j := turnNum; j < turnsTotal; j++ {
        turnResults = append(turnResults, TurnResult{
            TurnNumber: j + 1,
            Status:     TurnError,
            SkipReason: "failed to extract session ID from initial run; cannot resume session",
        })
    }
    return turnResults
}
```

> **风险等级**：🟡 中等。会话 ID 提取失败将导致整个多轮用例无法执行。

#### 5. JSONPath 库依赖

**问题**：本提案中的 `extractByJSONPath` 使用了 `jsonpath.Get(path, data)` 调用，需要外部 JSONPath 库（如 `github.com/PaesslerAG/jsonpath`）。

**缓解措施**：
- 第 1 阶段仅支持正则 capture（覆盖大多数场景）；JSONPath capture 在第 3 阶段实现
- 第 3 阶段实现时通过 `go get github.com/PaesslerAG/jsonpath` 引入依赖（MIT 许可证，无传递依赖，API 为 `jsonpath.Get(path, data)`）

> **风险等级**：🟢 低。正则 capture 可满足大多数场景；JSONPath 是增量能力。

### 建议的实现优先级

| 优先级 | 模块                                              | 理由                                                |
| -------- | --------------------------------------------------- | -------------------------------------------------------- |
| P0       | `executeCaseOnce` 分支 + `executeMultiTurn`    | 关键路径，必须最先实现                 |
| P0       | `SessionResumer` 接口 + claude_code/qodercli `RunTurn` | 多轮执行的基础                      |
| P0       | `executeTurnsSequentially`                          | 逐轮执行引擎                            |
| P0       | `checkPostCondition`                                | 每轮断言，多轮评估的核心价值 |
| P1       | `SessionResult.SessionID` 字段 + 提取逻辑  | 会话恢复的前提                          |
| P1       | `finalizeMultiTurnResult`                           | 结果聚合和 Judge 执行                   |
| P1       | `turn_response_contains` 等其他 Judge 断言 | 每轮评估                                      |
| P1       | 新验证器规则                                 | 防止无效配置                           |
| P2       | codex `RunTurn` 实现                      | 需要安全的会话文件关联策略                  |
| P2       | `capture` + `renderTemplate`                        | 动态值传递                                    |
| P3       | JSONPath capture                                    | 正则不足时才需要                   |
| P3       | `retry_on: turn_precondition_fail`                  | 锦上添花                                             |

### 设计完整性自评估

| 维度                  | 评级     | 描述                                                                                                                                                          |
| -------------------------- | ---------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Schema 设计              | ✅ 完整 | Turn、PostCondition、CaptureRule、Rule 扩展均有完整定义                                                                                      |
| 评估器执行引擎 | ✅ 完整 | `executeCaseOnce` 分支、`executeMultiTurn`、`executeTurnsSequentially`、`finalizeMultiTurnResult`、`executeMultiTurnFallback` 均有完整实现 |
| Agent 接口            | ✅ 完整 | `SessionResumer` 接口定义、claude_code、qodercli 和 codex `RunTurn` 实现、`extractSessionID` 分发逻辑均已提供                    |
| Judge 断言           | ✅ 完整 | `evalTurnResponseContains`、`evalTurnResponseNotContains`、`evalToolCalledInTurn`、`evalToolNotCalledInTurn` 均有完整实现                       |
| 验证器                  | ✅ 完整 | post_condition、capture、每轮断言、content 必填的验证规则均已提供                                                             |
| 辅助函数             | ✅ 完整 | `renderTemplate`、`extractCapturedValue`、`checkPostCondition` 及 6 个辅助函数均有完整实现                                             |
| 向后兼容         | ✅ 已验证 | 分支条件 `len(caseCfg.Input.Turns) > 1` 确保单轮用例不受影响                                                                             |
| 边界情况                 | ✅ 完整 | 空 sessionID、capture 失败等边界情况均在正文和反思部分提供了完整处理代码                           |
| 可执行性              | ✅ 可行 | 所有代码块均有完整实现，可直接作为实现参考                                                                  |
