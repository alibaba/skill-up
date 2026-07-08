---
title: agent_judge 专用评审 Skill 支持
authors:
  - "kongtang"
creation-date: 2026-07-07
last-updated: 2026-07-07
status: draft
---

# SUP-0002: agent_judge 专用评审 Skill 支持

语言：[English](../0002-agent-judge-specific-skill.md) | 中文

<!-- toc -->
- [摘要](#摘要)
- [动机](#动机)
  - [目标](#目标)
  - [非目标](#非目标)
- [需求](#需求)
- [提案](#提案)
  - [用户场景速查](#用户场景速查)
  - [注意事项、约束与说明](#注意事项约束与说明)
  - [风险与缓解措施](#风险与缓解措施)
- [设计细节](#设计细节)
  - [配置 Schema](#配置-schema)
  - [配置合并语义](#配置合并语义)
  - [路径解析与安装目标](#路径解析与安装目标)
  - [Agent 安装适配与渐进式加载](#agent-安装适配与渐进式加载)
  - [强制使用语义](#强制使用语义)
  - [评估器执行流程](#评估器执行流程)
  - [隔离语义](#隔离语义)
  - [报告元数据](#报告元数据)
  - [文档与模板更新](#文档与模板更新)
- [测试计划](#测试计划)
- [缺点](#缺点)
- [替代方案](#替代方案)
- [所需基础设施](#所需基础设施)
- [升级与迁移策略](#升级与迁移策略)
<!-- /toc -->

## 摘要

`agent_judge` 目前只能通过 `judge.criteria` 描述评审标准，无法为评审 Agent 安装专门的 Skill。对于需要领域知识、可复用评分规约、严格输出格式或长期维护 rubric 的评估，这会迫使作者把大量评审逻辑塞进每个 case 的 YAML 中，也会绕过各 Agent 的 Skill 渐进式加载机制，增加 context 被大文档撑爆的风险。

本提案根据 [GitHub issue #134](https://github.com/alibaba/skill-up/issues/134) 设计 `judge.skills` 配置，使 `agent_judge` 可以在评审阶段通过对应 Agent 的原生 Skill 安装方式安装一个或多个专用评审 Skill。该能力与被测 Skill 的安装路径隔离，保持现有 `judge.criteria`、benchmark `with_skill` / `without_skill` 和 Agent 运行流程向后兼容，并在报告中记录实际使用的 judge Skill 信息。

## 动机

`agent_judge` 适合处理无法用确定性规则表达的语义评估，但当前配置只有自然语言 criteria、模型、阈值和超时。实际 eval 作者常遇到以下问题：

1. **评审规则过长**：领域规范、风格指南、评分细则和反例库不适合长期内嵌在 YAML 中。
2. **评审逻辑需要复用**：多个 eval 或多个 case 共享同一套 rubric 时，重复 criteria 容易漂移。
3. **评审 Agent 需要不同上下文**：主运行 Agent 应安装被测 Skill，而 judge Agent 可能需要一个完全不同的评审 Skill。
4. **评审提示需要独立演进**：评审 Skill 可以随版本迭代，不必修改所有 case 文件。
5. **隔离性要求明确**：评审辅助 Skill 不能泄漏给主运行 Agent，否则 benchmark 会把评分工具错误地变成被测能力的一部分。
6. **需要渐进式加载**：使用 Skill 的目的不是把 `SKILL.md`、references、assets 等内容全部拼进 judge prompt，而是依赖 Agent 自身的 Skill 发现、选择和按需加载能力，让长 rubric 在需要时才进入上下文。
7. **需要可审计性**：评审结果应说明用了哪些 judge Skill，否则报告读者难以判断评分依据和复现环境。

当前代码路径也体现了这个缺口：

- `config.JudgeConfig` 已包含 `type`、`model`、`criteria`、`pass_threshold`、`timeout_seconds` 等字段，但没有 judge 级 Skill 引用。
- `defaultEvaluator.setupCaseEnvironment()` 只在主运行 Agent 上安装 `evalCfg.Skills`。
- `agent_judge` 在评审阶段通过 `resolveJudgeAgent()` 创建或复用 judge Agent，然后 `judge.NewJudge()` 包装为 `AgentJudge`，最终由 `AgentJudge.Evaluate()` 调用 `judgeAgent.Run()`。
- 评审阶段没有调用 `judgeAgent.InstallSkill(...)` 的安装步骤。

### 目标

1. **支持 judge 级 Skill 配置**：在 `JudgeConfig` 中新增 `skills` 字段，复用现有 `SkillRef` 语义。
2. **仅作用于 `agent_judge`**：`judge.skills` 只在 `judge.type: agent_judge` 时生效。
3. **安装到 judge Agent**：在 `AgentJudge.Evaluate()` 调用 `Run()` 前，把 `judge.skills` 安装到 judge Agent 所在 runtime。
4. **与主运行 Agent 隔离**：`judge.skills` 不安装到主运行 Agent；`eval.skills` 也不自动安装到 judge Agent。
5. **保持 benchmark 语义**：无论当前配置变体是 `with_skill` 还是 `without_skill`，judge Skill 都应安装，因为它是评分工具，不是被测 Skill。
6. **保持向后兼容**：不改变已有 `agent_judge` 配置的行为；没有 `judge.skills` 的 eval 无需迁移。
7. **保留 Agent 原生 Skill 机制**：不同 Agent 的 Skill 安装目录、manifest、索引或发现机制可能不同，skill-up 只通过 Agent adapter 的 `InstallSkill` 抽象触发安装，不把 Skill 文档展开注入 prompt。
8. **强制使用 judge Skill**：当用户为 `agent_judge` 配置 `judge.skills` 时，评审 prompt 必须显式要求 judge Agent 使用这些已安装 Skill 作为权威评审依据；安装不是充分条件。
9. **报告可审计**：报告中记录本次评审配置使用的 judge Skill 元数据。
10. **覆盖测试与文档**：补充配置加载、校验、合并、安装隔离、强制使用、报告元数据和文档示例。

### 非目标

1. **不引入新的 Skill 包管理系统**：本提案复用现有 `SkillRef` / `runtime.SkillConfig`，不设计 registry 下载、版本锁定或依赖解析。
2. **不改变主运行 Agent 的 Skill 安装语义**：`eval.skills` 仍然只表示被测 Skill 或主运行 Agent 所需 Skill。
3. **不重写 `AgentJudge` 评分协议**：本阶段继续使用 criteria 驱动的 JSON 评分结果解析。
4. **不让 judge Skill 自动决定 criteria 列表**：Skill 可以提供 rubric 和输出约束，但结构化评分项仍由 `judge.criteria` 声明。
5. **不支持非 `agent_judge` 的 Skill 安装**：`rule_based` 和 `script` judge 不读取 `judge.skills`。
6. **不提供 prompt 拼接降级方案**：如果某个 Agent adapter 不支持 Skill 安装，本提案不允许把 Skill 文件内容直接拼进 judge prompt 作为替代实现。

## 需求

### 必须有

| ID  | 需求 | 验收标准 |
| --- | --- | --- |
| R1 | `JudgeConfig` 支持 `skills` | YAML 中可在 `judge:` 下声明 `skills` 数组，并被加载到配置结构 |
| R2 | 仅 `agent_judge` 可使用 | `rule_based` / `script` 配置 `judge.skills` 时校验失败或给出明确错误 |
| R3 | 评审前安装 | `AgentJudge.Evaluate()` 调用 judge Agent 前，配置的 judge Skills 已安装完成 |
| R4 | 安装隔离 | 主运行 Agent 只安装 `eval.skills`；judge Agent 只额外安装 `judge.skills` |
| R5 | benchmark 不影响评审 Skill | `without_skill` 变体仍安装 `judge.skills`，但不安装 `eval.skills` |
| R6 | 路径解析一致 | 本地 judge Skill 路径相对 Skill 根目录解析，与现有 `eval.skills` 约定一致 |
| R7 | 向后兼容 | 旧配置不需要修改，现有 `judge.criteria` 行为保持不变 |
| R8 | 原生 Skill 渐进式加载 | 实现必须调用 judge Agent adapter 的 `InstallSkill`；不得读取 Skill 文档并拼接到 judge prompt |
| R9 | 报告记录 judge Skill | JSON/HTML 报告能展示本次评审使用的 judge Skill 列表，JUnit 至少以 properties 形式暴露 |
| R10 | 强制使用 judge Skill | 当 `judge.skills` 非空时，`AgentJudge` 发送给 judge Agent 的 prompt 必须包含强制使用这些 Skill 的指令和 Skill 标识 |

### 应该有

| ID  | 需求 | 验收标准 |
| --- | --- | --- |
| S1 | 支持多个 judge Skills | `judge.skills` 可声明多个 Skill，并按配置顺序安装 |
| S2 | case 级覆盖 | case 内 `judge.skills` 可覆盖 eval 级 judge 配置 |
| S3 | 清晰诊断 | 安装失败时错误信息包含 judge Skill 路径和 judge 阶段上下文 |
| S4 | 文档更新 | 英文/中文 eval 写作指南与 skill-upper 参考文档包含示例 |
| S5 | Agent 差异文档化 | 文档说明 judge Skill 安装依赖具体 Agent 的 Skill 支持能力，不承诺所有 Agent 行为完全一致 |

### 最好有

| ID  | 需求 | 验收标准 |
| --- | --- | --- |
| N1 | 更丰富的报告元数据 | 报告可选记录 judge Skill 摘要哈希、安装目标和安装状态，不暴露敏感绝对路径 |
| N2 | 未来兼容 skill-only judge | 后续可在不破坏本设计的前提下，让 judge Skill 提供默认 criteria |

## 提案

新增 `judge.skills` 字段，并采用复数形式，与顶层 `skills` 保持一致：

```yaml
judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
  skills:
    - source: local_path
      path: evals/fixtures/judge-skill
  criteria:
    - "根据已安装的评审 Skill 中的 rubric 判断答案是否正确"
```

高层执行流程：

1. 加载 `eval.yaml` 和 case YAML 时解析 `judge.skills`。
2. 校验 `judge.skills` 只与 `agent_judge` 搭配使用。
3. 执行 case 时，先按现有逻辑准备 runtime、安装主运行 Agent、MCP 和 `eval.skills`。
4. 运行主 Agent，得到 `SessionResult`、workspace diff、transcript 等评审输入。
5. 进入 judge 阶段，解析或创建 judge Agent。
6. 在创建 `AgentJudge` 或调用 `Evaluate()` 前，通过 judge Agent adapter 的 `InstallSkill` 把合并后的 `judge.skills` 安装到 judge Agent。
7. 构建评审提示时，如果 `judge.skills` 非空，加入强制使用已安装 judge Skill 的指令，不允许以普通 criteria-only 方式评分。
8. 调用 judge Agent 运行评审提示，解析结构化 JSON 评分结果。
9. 将本次评审使用的 judge Skill 元数据写入报告。

```
┌─────────────────────────────────────────────────────────┐
│                       Case Runtime                       │
│                                                         │
│  setupCaseEnvironment                                  │
│  ├─ install run agent                                  │
│  ├─ install MCP                                        │
│  └─ install eval.skills ───────────────┐               │
│                                        │               │
│                              ┌─────────▼─────────┐     │
│                              │ Main Run Agent    │     │
│                              │ runs evaluated    │     │
│                              │ Skill             │     │
│                              └─────────┬─────────┘     │
│                                        │               │
│                              transcript/diff/output    │
│                                        │               │
│  judge phase                           │               │
│  ├─ resolve judge agent                │               │
│  ├─ install judge.skills ───────┐      │               │
│  └─ AgentJudge.Evaluate         │      │               │
│                                 ▼      ▼               │
│                         ┌────────────────────┐         │
│                         │ Judge Agent         │         │
│                         │ grades with rubric  │         │
│                         │ Skill + criteria    │         │
│                         └────────────────────┘         │
└─────────────────────────────────────────────────────────┘
```

### 用户场景速查

#### 场景 1：复用领域评审 rubric

```yaml
schema_version: v1alpha1

skills:
  - source: local_path
    path: .

judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
  skills:
    - source: local_path
      path: evals/fixtures/sql-judge-skill
  criteria:
    - "SQL 修改符合评审 Skill 中定义的安全与兼容性规则"
    - "评审结论必须引用具体证据，而不是泛泛评价"
```

主运行 Agent 安装被测 Skill；judge Agent 安装 `sql-judge-skill`，用于加载数据库变更审核规则、反例和输出格式要求。

#### 场景 2：case 级评审 Skill 覆盖

```yaml
# evals/eval.yaml
judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
  skills:
    - source: local_path
      path: evals/fixtures/default-judge-skill
  criteria:
    - "根据默认评审 Skill 判断输出质量"
```

```yaml
# evals/cases/security-review.yaml
judge:
  type: agent_judge
  skills:
    - source: local_path
      path: evals/fixtures/security-judge-skill
  criteria:
    - "根据安全评审 Skill 判断是否发现高风险问题"
```

当 case 声明自己的 `judge.type` 时，case 级 judge 配置视为完整评审策略：`skills` 和 `criteria` 使用 case 级配置，`model`、`pass_threshold`、`timeout_seconds` 可继续沿用全局默认。

#### 场景 3：benchmark 中保持评分工具稳定

```yaml
benchmark:
  enabled: true

skills:
  - source: local_path
    path: .

judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
  skills:
    - source: local_path
      path: evals/fixtures/judge-rubric
  criteria:
    - "根据 judge-rubric 判断输出是否满足验收标准"
```

benchmark 会分别执行：

- `with_skill`：主 Agent 安装 `eval.skills`，judge Agent 安装 `judge.skills`。
- `without_skill`：主 Agent 不安装 `eval.skills`，judge Agent 仍安装 `judge.skills`。

这样比较的是被测 Skill 对结果的影响，而不是评审工具是否存在。

### 注意事项、约束与说明

1. **继续要求 `criteria`**：本阶段 `judge.criteria` 仍是结构化评分项来源。judge Skill 可以承载长 rubric，但 YAML 中至少保留一条 criteria，用于确定评分结果数量和报告结构。
2. **复用 `SkillRef`**：`judge.skills` 使用 `source`、`path`、`target`，不新增单数 `judge.skill`，避免两套配置语义并存。
3. **必须依赖 Agent Skill 机制**：judge Skill 的价值在于 Agent 可以按需发现和加载 Skill 资源。实现不得把 Skill 目录内容整体读入 `criteria` 或 judge prompt。
4. **Agent 安装方式允许差异**：Claude Code、Codex、Qoder CLI、自定义 Agent 的 Skill 安装位置和索引机制可以不同；skill-up 的职责是把同一个 `runtime.SkillConfig` 交给对应 adapter。
5. **本地路径优先**：第一阶段按现有本地 Skill 安装能力实现；如果未来顶层 `skills` 支持 registry，`judge.skills` 可自然复用。
6. **安装失败是 ERROR**：judge Skill 安装失败说明评审环境未准备好，应标记为 ERROR，而不是 FAIL。
7. **不静默回退**：配置了 `judge.skills` 但无法安装时，不应继续用无 Skill 的 judge Agent 评分。

### 风险与缓解措施

| 风险 | 影响 | 概率 | 缓解措施 |
| --- | --- | --- | --- |
| judge Skill 意外安装到主 Agent | benchmark 结果被污染 | 中 | 安装逻辑只接收 `judgeAgent`，单独测试 run/judge agent 的安装记录 |
| `without_skill` 跳过 judge Skill | baseline 无法被同一 rubric 评审 | 中 | 安装 judge Skills 不受 `configName` 控制 |
| case/global 合并语义不清 | eval 作者难以预测使用哪个 judge Skill | 中 | 沿用现有 `MergeJudgeConfig` 的“case type 完整覆盖”规则，并在文档写明 |
| criteria 与 judge Skill rubric 冲突 | 评审结果不稳定 | 中 | 文档建议 criteria 写成稳定评分项，Skill 中放详细规则和输出约束 |
| 安装路径逃逸 Skill 根目录 | 可能读取非预期本地文件 | 低 | 复用或补强现有本地 Skill 路径校验，确保相对路径解析后仍在 Skill 根目录内 |
| 为兼容某些 Agent 而拼接 Skill 文档到 prompt | 失去渐进式加载，context 爆炸，行为偏离真实 Agent Skill 使用方式 | 中 | 明确禁止 prompt 拼接降级；adapter 不支持安装时直接 ERROR |
| 不同 Agent 的 Skill 发现机制不一致 | 同一 judge Skill 在不同引擎下行为有差异 | 中 | 保持 adapter 级安装实现和测试，报告记录 engine 与 judge Skill 信息 |
| 报告缺少 judge Skill 信息 | 评审依据不可审计，难以复现 | 中 | 在 EvalResult/报告生成链路新增 judge skill 元数据 |
| judge Skill 已安装但 Agent 未使用 | 评分仍按普通 criteria 执行，用户定义 rubric 失效 | 中 | `AgentJudge` prompt 强制要求使用已安装 judge Skill；单元测试断言 prompt，集成 fixture 验证行为 |

## 设计细节

### 配置 Schema

在 `internal/config/schema.go` 中扩展 `JudgeConfig`：

```go
// JudgeConfig describes the evaluation strategy.
type JudgeConfig struct {
    Type       string     `json:"type"                     yaml:"type"`
    ScriptPath string     `json:"script_path,omitempty"    yaml:"script_path,omitempty"`
    Model      string     `json:"model,omitempty"          yaml:"model,omitempty"`
    Criteria   []string   `json:"criteria,omitempty"       yaml:"criteria,omitempty"`
    Skills     []SkillRef `json:"skills,omitempty"         yaml:"skills,omitempty"`

    PassThreshold  *float64 `json:"pass_threshold,omitempty"  yaml:"pass_threshold,omitempty"`
    TimeoutSeconds *int     `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
    Success        []Rule   `json:"success,omitempty"         yaml:"success,omitempty"`
    Failure        []Rule   `json:"failure,omitempty"         yaml:"failure,omitempty"`
}
```

校验规则：

1. `judge.skills` 非空时，`judge.type` 必须是 `agent_judge`。
2. 每个 `judge.skills[*].source` 目前应为 `local_path` 或沿用顶层 `skills` 已支持的 source 值。
3. `source: local_path` 时 `path` 必填，且不能是空白字符串。
4. `target` 可选；语义与顶层 `skills[*].target` 一致。
5. `agent_judge` 仍要求 `model` 和至少一条 `criteria`，除非后续另行修改 `AgentJudge` 评分协议。
6. 依赖继承默认值的校验，尤其是 `agent_judge` 必填的 `model`，必须在 `judge.MergeJudgeConfig(global, caseLevel)` 得到有效 judge 配置后执行。raw case 校验可以继续检查只依赖 case 本身的约束，但不能仅因为 case 级 `agent_judge` 的 `model` 来自全局 judge 配置就拒绝该 case。

### 配置合并语义

沿用当前 `judge.MergeJudgeConfig(global, caseLevel)` 的设计：

- 如果 case 没有声明 `judge.type`，使用全局 judge 配置，包括全局 `judge.skills`。
- 如果 case 声明了 `judge.type`，case 级配置视为完整覆盖；`skills`、`criteria`、`success`、`failure`、`script_path` 等使用 case 级值。
- `model`、`pass_threshold`、`timeout_seconds` 可继续从全局配置继承，因为现有逻辑已这样处理。

新增字段后的合并逻辑无需特殊“追加”行为。原因是 judge Skill 通常代表一套完整评分上下文，隐式合并全局和 case 级 Skill 容易造成 rubric 冲突。如果 eval 作者确实需要多个 Skill，应在 case 级 `judge.skills` 中显式列出全部 Skill。

### 路径解析与安装目标

本地 judge Skill 路径解析规则与顶层 `skills` 保持一致：

```go
skillSourceDir := e.loader.SkillDir()
skillSource := filepath.Join(skillSourceDir, judgeSkillRef.Path)
skillCfg := runtime.SkillConfig{
    Source: skillSource,
    Target: judgeSkillRef.Target,
}
```

实现时建议抽取一个小的共享 helper，例如：

```go
func resolveSkillConfig(skillDir string, ref config.SkillRef) runtime.SkillConfig {
    return runtime.SkillConfig{
        Source: filepath.Join(skillDir, ref.Path),
        Target: ref.Target,
    }
}
```

这样顶层 `eval.skills` 和 `judge.skills` 共享路径解析，减少未来 drift。

需要注意：

- 相对路径以 Skill 根目录为基准，而不是当前工作目录或 `eval.yaml` 所在目录。
- 安装顺序按 `judge.skills` 数组顺序执行。
- 错误信息应包含 `judge.skills[i].path`，便于定位。

### Agent 安装适配与渐进式加载

不同 Agent 对 Skill 的约定可能不同：

- Claude Code 可能有自己的 Skill 目录和索引规则。
- Codex 可能有自己的 Skill 搜索、资源发现和加载约定。
- Qoder CLI 可能有不同的安装命令、目标目录或 manifest 处理方式。
- custom agent 可能通过 `InstallSkillCmd` 或自身协议实现安装。

因此，本提案不要求 evaluator 理解每种 Agent 的 Skill 内部结构。evaluator 只做三件事：

1. 根据配置解析出 `runtime.SkillConfig{Source, Target}`。
2. 在正确阶段调用最终 judge Agent 的 `InstallSkill(ctx, rt, skillCfg)`。
3. 记录安装的 judge Skill 元数据，供报告和诊断使用。

具体安装方式由 agent adapter 负责：

```go
type Agent interface {
    Run(ctx context.Context, rt Runtime, opts ExecOptions, messages []Message) (*SessionResult, error)
    Install(ctx context.Context, rt Runtime) error
    InstallMCP(ctx context.Context, rt Runtime, cfg runtime.MCPConfig) error
    InstallSkill(ctx context.Context, rt Runtime, skillCfg runtime.SkillConfig) error
    CheckCredentials(ctx context.Context) error
    Name() string
}
```

关键约束：

- `InstallSkill` 必须尽量使用该 Agent 的原生 Skill 安装方式，包括目录布局、索引文件、manifest、缓存刷新或命令行安装入口。
- evaluator 不读取 judge Skill 的 `SKILL.md`、`references/`、`assets/`，也不把这些文件拼入 judge prompt。
- 如果某个 adapter 无法安装 Skill，应返回明确错误；配置了 `judge.skills` 时不允许静默退回到“把文档塞进 prompt”的模式。
- 对自定义 Agent，继续沿用已有 `InstallSkillCmd` 能力；没有安装能力而又配置 `judge.skills` 时，应在评审阶段失败为 ERROR。

这样可以保留 Skill 的核心价值：Agent 在评审过程中根据任务需要渐进式加载相关说明和资源，而不是一次性消耗上下文窗口。

### 强制使用语义

仅安装 judge Skill 不代表 `agent_judge` 一定会使用它。为了让用户定义的 judge Skill 真正参与评分，`AgentJudge` 的 prompt 构建逻辑必须在 `judge.skills` 非空时加入一个明确的强制段，例如：

```text
You MUST use the installed judge Skill(s) listed below as the authoritative
grading rubric before evaluating the case. Do not grade this case using only
the inline criteria. The inline criteria identify the result dimensions, while
the judge Skill(s) define the detailed rubric, constraints, and evidence rules.

Installed judge Skill(s):
- evals/fixtures/judge-skill
```

这段指令只引用 Skill 标识和配置路径，不展开 Skill 文件内容。它的作用是触发 Agent 的 Skill 发现/选择机制，并把评分依据的优先级说清楚：

1. `judge.skills` 提供详细 rubric、约束、风格指南和证据要求。
2. `judge.criteria` 提供结构化评分维度和报告结果数量。
3. 当二者存在冲突时，评审 prompt 应明确以 judge Skill 中的 rubric 为准，除非 criteria 定义了更具体的用例级验收项。

实现要求：

- `AgentJudge.buildPrompt()` 或等价函数接收已解析的 judge Skill 元数据。
- 当 `judge.skills` 非空时，prompt 中必须包含“必须使用已安装 judge Skill”的强制指令和每个 Skill 的稳定标识。
- 当 `judge.skills` 为空时，prompt 保持现有 criteria-only 行为，确保向后兼容。
- 不要求 evaluator 证明 Agent 内部实际加载了哪个文件；但必须让可观测输入明确要求使用 Skill，并通过集成 fixture 证明支持 Skill 的 Agent 会按该指令使用 Skill。
- 报告中应区分“configured/installed judge skills”和未来可能支持的“agent-acknowledged used judge skills”。本提案至少要求前者。

### 评估器执行流程

在 `defaultEvaluator.newJudgeForCase()` 附近加入 judge Skill 安装步骤：

```go
func (e *defaultEvaluator) newJudgeForCase(
    ctx context.Context,
    rt runtime.Runtime,
    judgeCfg config.JudgeConfig,
    runAgent agent.Agent,
) (judge.Judge, error) {
    judgeCfg = resolveJudgeScriptPath(e.judgeScriptBaseDir(), judgeCfg)

    judgeAgent, err := e.resolveJudgeAgent(ctx, judgeCfg, runAgent)
    if err != nil {
        return nil, err
    }

    if err := e.installJudgeSkills(ctx, rt, judgeCfg, judgeAgent); err != nil {
        return nil, err
    }

    j, err := judge.NewJudge(judgeCfg, judgeAgent, rt)
    if err != nil {
        return nil, fmt.Errorf("failed to create judge: %w", err)
    }
    return j, nil
}
```

`installJudgeSkills` 的核心规则：

```go
func (e *defaultEvaluator) installJudgeSkills(
    ctx context.Context,
    rt runtime.Runtime,
    judgeCfg config.JudgeConfig,
    judgeAgent agent.Agent,
) error {
    if judgeCfg.Type != "agent_judge" || len(judgeCfg.Skills) == 0 {
        return nil
    }
    if e.loader == nil {
        return errors.New("judge.skills requires a loader to resolve local paths")
    }

    skillDir := e.loader.SkillDir()
    for i, ref := range judgeCfg.Skills {
        skillCfg := resolveSkillConfig(skillDir, ref)
        if err := judgeAgent.InstallSkill(ctx, rt, skillCfg); err != nil {
            return fmt.Errorf("failed to install judge skill judge.skills[%d].path=%q: %w", i, ref.Path, err)
        }
        logging.DebugContextf(ctx, "Evaluator: judge skill installed: %s", filepath.Base(skillCfg.Source))
    }
    return nil
}
```

安装时机选择在 `resolveJudgeAgent()` 之后、`judge.NewJudge()` 之前，有三个好处：

1. 已经拿到最终 judge Agent，不会误装到 run Agent。
2. runtime 已经创建，主运行阶段输出和 workspace diff 已准备好。
3. 如果安装失败，可以在进入 `AgentJudge.Evaluate()` 之前返回明确 ERROR。

### 隔离语义

| 配置 | 安装到主运行 Agent | 安装到 judge Agent |
| --- | --- | --- |
| `eval.skills` + `with_skill` | 是 | 否 |
| `eval.skills` + `without_skill` | 否 | 否 |
| `judge.skills` + `with_skill` | 否 | 是 |
| `judge.skills` + `without_skill` | 否 | 是 |

如果当前实现中 judge Agent 复用 `runAgent` 实例，仍应按“逻辑角色”处理安装：`eval.skills` 的安装发生在主运行前，`judge.skills` 的安装发生在评审前。对于内置引擎，优先保持 `agent_judge` 使用独立 judge Agent；如果不得不复用实例，文档和测试必须确保 judge Skill 安装不会影响主运行阶段，因为主运行已经结束。

### 报告元数据

报告应记录本次评审实际使用的 judge Skill 信息，便于审计和复现。建议在 case 结果或 grading metadata 中增加只含非敏感信息的结构：

```go
// JudgeSkillInfo describes a judge Skill used during agent_judge evaluation.
type JudgeSkillInfo struct {
    Source string `json:"source,omitempty"` // e.g. local_path
    Path   string `json:"path,omitempty"`   // config path, relative when configured that way
    Target string `json:"target,omitempty"`
    Name   string `json:"name,omitempty"`   // derived from path basename or Skill metadata
}
```

报告约定：

- JSON 报告：在每个 case/config result 下包含 `judge_skills` 或等价字段。
- HTML 报告：在 case 详情中展示 judge Skill 名称、配置路径和 target。
- JUnit 报告：通过 testcase properties 暴露 `judge.skills.count` 和 `judge.skills.<n>.path` 等基础字段。
- Anthropic `grading.json`：如果格式没有合适扩展点，可不写入正式 grading 字段，但应在 skill-up 自有 JSON/HTML 报告中保留。

安全与隐私约束：

- 默认记录用户配置中的相对 `path`，不记录展开后的本机绝对路径。
- 不记录 Skill 文件全文。
- 如未来增加 hash，可记录 Skill 目录摘要用于复现，但应避免把大文件内容直接嵌入报告。

### 文档与模板更新

需要更新：

- `docs/guide/writing-evals.md`：在 `judge: agent_judge` 小节加入 `skills` 示例和隔离说明。
- `docs/zh/guide/writing-evals.md`：同步中文说明。
- `skills/skill-upper/references/eval-yaml.md`：补充 `judge.skills` 字段。
- `skills/skill-upper/references/judge-types.md`：说明何时使用 judge Skill，何时继续使用 inline criteria。
- Agent 文档或自定义引擎文档：说明 judge Skill 安装依赖对应 adapter 的 `InstallSkill` 能力，不支持时不会拼接文档降级。
- 相关模板：如果模板包含 `agent_judge` 示例，可加入注释性示例，但不默认启用，以免增加成本。

## 测试计划

### 单元测试

1. **配置加载**
   - eval 级 `judge.skills` 可正确反序列化到 `JudgeConfig.Skills`。
   - case 级 `judge.skills` 可正确反序列化。
   - 多个 Skill 保持配置顺序。

2. **配置校验**
   - `judge.type: agent_judge` + `judge.skills` 校验通过。
   - `judge.type: rule_based` + `judge.skills` 校验失败。
   - `judge.type: script` + `judge.skills` 校验失败。
   - `judge.skills[*].path` 为空时校验失败。
   - `agent_judge` 缺少 `criteria` 时保持现有失败行为。

3. **配置合并**
   - case 不声明 `judge.type` 时继承全局 `judge.skills`。
   - case 声明 `judge.type` 且声明自己的 `judge.skills` 时，不追加全局 `judge.skills`。
   - case 声明 `judge.type` 但未声明 `model` 时，仍继承全局 `model`。

4. **评估器安装行为**
   - mock run Agent 只收到 `eval.skills` 安装。
   - mock judge Agent 只收到 `judge.skills` 安装。
   - `without_skill` 变体不安装 `eval.skills`，但安装 `judge.skills`。
   - judge Skill 安装失败时，case 状态为 ERROR，且不调用 judge Agent `Run()`。
   - adapter 不支持 Skill 安装且配置 `judge.skills` 时，不拼接 Skill 文档到 prompt，而是返回 ERROR。

5. **强制使用 prompt**
   - `judge.skills` 非空时，`AgentJudge` prompt 包含必须使用已安装 judge Skill 的指令。
   - prompt 包含每个 judge Skill 的稳定标识或配置路径。
   - prompt 不包含 judge Skill 文件正文。
   - `judge.skills` 为空时，prompt 与现有 criteria-only 行为保持兼容。

6. **路径解析**
   - 相对路径基于 Skill 根目录解析。
   - `target` 正确传递到 `runtime.SkillConfig.Target`。

7. **报告元数据**
   - JSON 报告包含本次使用的 judge Skill 列表。
   - HTML 报告能展示 judge Skill 名称和配置路径。
   - JUnit properties 包含 judge Skill 数量和路径。
   - 报告不包含 judge Skill 文件全文或本机绝对路径。

### 集成 / E2E 风格测试

新增一个 fixture，例如：

```text
e2e/testdata/agent-judge-skill/
  SKILL.md
  evals/
    eval.yaml
    cases/
      uses-judge-skill.yaml
    fixtures/
      judge-skill/
        SKILL.md
```

该 fixture 使用可控的 mock/custom agent，验证：

- 主运行 Agent 不读取 judge Skill。
- judge Agent 收到的评审提示明确要求使用已安装 judge Skill。
- judge Agent 通过 Skill 机制读取 judge Skill 的规则，而不是从 prompt 正文直接获得规则。
- 缺少 judge Skill 时同一 case 会失败或 ERROR，从而证明安装确实生效。
- mock/custom agent 通过自身 Skill 发现机制加载 judge Skill，测试不依赖把 `SKILL.md` 内容拼进 prompt。
- 生成的报告包含 judge Skill 元数据。

## 缺点

1. **配置表面积增加**：`judge` 下新增 `skills` 字段，需要文档解释它与顶层 `skills` 的区别。
2. **评审可复现性依赖 Skill 内容**：rubric 从 YAML 转移到 Skill 后，review 时需要同时查看 Skill 文件。
3. **安装成本增加**：每次 `agent_judge` 都可能多一次或多次 Skill 安装。
4. **criteria 仍需保留**：第一阶段没有完全实现“Skill 独立定义所有评分项”，对部分用户来说仍需要一条简短 criteria。
5. **跨 Agent 行为不完全一致**：不同 Agent 的 Skill 发现和加载机制本来就不同，同一 judge Skill 在不同引擎下可能有细微行为差异。
6. **报告需要扩展**：JSON/HTML/JUnit 报告链路都要携带 judge Skill 元数据，增加少量实现工作。

## 替代方案

### 方案 A：新增单数 `judge.skill`

```yaml
judge:
  type: agent_judge
  skill:
    source: local_path
    path: evals/fixtures/judge-skill
```

优点是对单个 Skill 场景更简洁。缺点是顶层已有 `skills` 数组，新增单数会制造两套语义；未来支持多个 judge Skills 时还要迁移。因此不采用。

### 方案 B：把 judge Skill 放入顶层 `skills`

```yaml
skills:
  - source: local_path
    path: .
  - source: local_path
    path: evals/fixtures/judge-skill
```

优点是不改 schema。缺点是 judge Skill 会安装到主运行 Agent，污染被测能力，尤其破坏 benchmark `without_skill` 的解释性。因此不采用。

### 方案 C：只扩展 `criteria`，支持引用外部 Markdown

```yaml
judge:
  type: agent_judge
  criteria_file: evals/fixtures/rubric.md
```

优点是实现简单。缺点是无法利用 Agent Skill 的既有约定、目录结构、资源文件、安装机制和渐进式加载能力。如果实现为读取 Markdown 后拼入 prompt，还会把长 rubric 一次性塞进 context，正好违背 issue 中希望使用 Skill 的核心动机。因此不作为本提案主方向。

### 方案 C2：安装失败时把 Skill 文档拼进 prompt

这种方案看似提高兼容性，但会让不同 Agent 的 Skill 行为退化成普通长 prompt：

- 失去按需加载能力。
- 容易把 `references/` 和资产说明一次性塞爆上下文。
- 无法验证 judge Skill 在真实 Agent Skill 机制下是否可用。
- 报告里很难说明到底是“安装了 Skill”还是“拼接了文档”。

因此本提案明确不采用该降级方案。

### 方案 D：允许 judge Skill 完全替代 criteria

```yaml
judge:
  type: agent_judge
  skills:
    - source: local_path
      path: evals/fixtures/judge-skill
```

这是有价值的未来方向，但需要调整 `AgentJudge` 的 prompt 和 JSON 结果解析：当前报告结构依赖 criteria 数量来校验评审结果。本提案先保持 `criteria` 必填，降低实现风险。

## 所需基础设施

不需要新增外部服务或第三方依赖。实现仅复用现有配置加载、runtime Skill 安装、Agent 接口和测试基础设施。

可能需要的本地测试资产：

- 一个 judge Skill fixture。
- 一个记录 `InstallSkill` 调用的 mock judge Agent。
- 一个用于集成验证的 custom/mock agent fixture。

## 升级与迁移策略

该变更向后兼容：

- 现有 `agent_judge` 配置无需修改。
- 不配置 `judge.skills` 时行为完全不变。
- 顶层 `skills` 语义不变。
- benchmark 输出语义不变，只是允许评审侧拥有稳定的专用 rubric Skill。

推荐迁移路径：

1. 先把长篇 judge rubric 提取到 `evals/fixtures/<name>-judge-skill/SKILL.md`。
2. 在 `judge.skills` 中引用该路径。
3. 将 `judge.criteria` 收敛为少量稳定评分项，例如“根据已安装评审 Skill 判断是否满足安全规则”。
4. 运行 `skill-up validate` 和至少一个小规模 eval，确认 judge Agent 能读取评审 Skill。
