---
title: Skill 评测事件日志协议
authors:
  - "JHWang-1997"
creation-date: 2026-08-18
last-updated: 2026-09-01
status: draft
---

# SUP-0005: Skill 评测事件日志协议

语言：[English](../0005-skill-evaluation-event-protocol.md) | 中文

<!-- toc -->
- [摘要](#摘要)
- [动机](#动机)
  - [目标](#目标)
  - [非目标](#非目标)
- [需求](#需求)
- [提案](#提案)
  - [通用评测事件日志](#通用评测事件日志)
  - [事件信封](#事件信封)
  - [首批事件类型](#首批事件类型)
  - [Payload 字段定义](#payload-字段定义)
  - [内部事件模型](#内部事件模型)
  - [任务进度模型](#任务进度模型)
  - [任务规划与生命周期 Emitter](#任务规划与生命周期-emitter)
  - [事件流示例](#事件流示例)
  - [消费规则](#消费规则)
  - [Sink 与传输模型](#sink-与传输模型)
  - [JSONL 文件语义](#jsonl-文件语义)
  - [命令行契约](#命令行契约)
  - [命令执行流程](#命令执行流程)
  - [失败语义](#失败语义)
  - [说明/约束/注意事项](#说明约束注意事项)
  - [风险与缓解](#风险与缓解)
- [设计细节](#设计细节)
- [测试计划](#测试计划)
- [缺点](#缺点)
- [备选方案](#备选方案)
- [所需基础设施](#所需基础设施)
- [升级与迁移策略](#升级与迁移策略)
<!-- /toc -->

## 摘要

本提案为 skill-up 定义一个轻量、版本化的评测事件日志。首个序列化格式为
JSON Lines，每个以换行结束的记录包含一条独立事件。

进度上报是第一个使用场景，而不是协议边界。同一日志未来可以承载生命周期、摘
要、诊断和产物事件。V1 有意只标准化本地 JSONL 文件 Sink；远程投递留待后续单
独设计。现有 stdout 继续作为独立的、面向人的界面。

## 动机

目前，需要实时评测状态的集成只能解析终端输出，或等待 iteration 完成后写出
的报告。终端文本面向人类；CI、看板和自动化则需要稳定的结构化事件流。

[Bazel Build Event Protocol](https://bazel.build/remote/bep) 展示了核心分离原
则：控制台输出服务人类，独立事件流服务程序化消费方。根据
[Issue #206 中的设计建议](https://github.com/alibaba/skill-up/issues/206#issuecomment-5336855301)，
SUP-0005 采用这一原则，但不复制 BEP 的 protobuf 编码或完整事件 DAG。

### 目标

- 定义通用事件日志，而不是只服务 Case 进度的文件。
- 提供稳定的调用标识和事件顺序。
- 让消费方能够发现缺口、重放和被截断的事件流。
- 让公共信封与每个事件 Payload 分别独立版本化。
- 按任务描述 Benchmark；一个源 Case 可以展开为多个配置任务。
- 提供周期性的 invocation 级进度快照和存活更新。
- 携带有大小限制、带命名空间的关联属性，同时不削弱类型化事件 Payload。
- 使事件生产独立于具体 Sink 和传输方式。
- 首版只覆盖 CI 所需的最小 run、progress、iteration、case 生命周期。

### 非目标

- 在本 PR 中实现 `--event-log` 或修改运行时行为。
- 定义远程事件服务、回调、OTLP 适配器、鉴权、重试、确认、排队或保留策略。
- 在 v1 中定义 summary、diagnostic、artifact、turn、retry、judge 或 tool
  事件；协议只为它们预留扩展空间。
- 定义 task 级执行阶段、预计剩余时间、动态任务发现或任务计划更新。
- 传输 prompt、模型响应、transcript、凭据或产物内容。
- 完整复制 Bazel BEP 的图结构或 protobuf schema。
- 采用 CloudEvents 作为 v1 传输格式；未来适配器可以映射记录，而无需改变本协
  议的有序日志契约。

## 需求

- 每个完整记录都是一个合法 JSON 对象，并以 `\n` 结尾。
- 每条事件都包含数字协议版本和事件版本、连续序号、稳定 invocation ID、时间
  戳、事件类型和类型化 Payload。
- 可选的事件属性是有大小限制的字符串对，不得承载核心事件语义或敏感
  评测内容。
- 启用事件的 invocation 一旦发出 `run_started`，可控收尾时就恰好发出一条结束
  生命周期的 `run_finished` 事件，包括存在失败 Case 的已完成运行，以及以调用
  级错误或取消结束的运行。
- 可控收尾的事件流恰好包含一个 `last_event: true`；该标记属于信封，不与某种
  事件类型永久绑定。
- 在受支持的协议大版本中，消费方必须兼容未知的可选信封字段、事件类型、事件
  版本、属性和 Payload 字段。
- 基础事件 schema 不得包含敏感评测内容。
- 评测任务并发执行时，事件发射必须安全。

## 提案

### 通用评测事件日志

首个实现建议提供：

```bash
skill-up run --event-log ./events.jsonl
```

该名称描述持久抽象，而不是首个消费场景。CI 可以消费类型化进度快照和 Case 生
命周期事件；未来事件族可以复用同一个文件和 Publisher 边界，无需再增加输出参
数。

### 事件信封

所有事件使用以下公共信封：

```json
{
  "protocol_version": 1,
  "event_version": 1,
  "sequence_number": 7,
  "invocation_id": "018f8f20-7a7d-7d90-a192-4f5ec8f07a2a",
  "time": "2026-08-19T10:01:10.451Z",
  "event": "case_completed",
  "attributes": {
    "com.alibaba.aone.eval_task_id": "123456"
  },
  "payload": {
    "task_id": "task-1",
    "iteration": 1,
    "case_id": "case-1",
    "configuration": "with_skill",
    "task_index": 1,
    "task_total": 2,
    "title": "Basic flow",
    "completed_tasks": 1,
    "status": "PASS",
    "pass_rate": 1.0,
    "duration_ms": 10000
  }
}
```

| 字段 | JSON 类型 | 是否必填 | 约束 |
| --- | --- | --- | --- |
| `protocol_version` | integer | 是 | 公共信封和有序日志的大版本；本规范中必须为 `1` |
| `event_version` | integer | 是 | 该事件类型 Payload 的大版本，范围为 `[1, 9007199254740991]`；本规范定义的每种核心事件都必须为 `1` |
| `sequence_number` | integer | 是 | invocation 全局连续顺序，范围为 `[1, 9007199254740991]` |
| `invocation_id` | string | 是 | 同一次 `skill-up run` 所有事件共享的稳定 UUID |
| `time` | string | 是 | 生产方创建事件的时间，采用带 `Z` 时区偏移的 UTC RFC 3339 格式 |
| `event` | string | 是 | 本注册表或兼容 v1 扩展中的非空事件类型 |
| `last_event` | boolean | 否 | 出现时必须为 `true`，只能位于最终记录，且不得为 `null` |
| `attributes` | object | 否 | 与本事件关联的不透明属性；键和值都只能是字符串；不得为 `null` |
| `payload` | object | 是 | 包含对应事件字段的非空对象 |

（`invocation_id`、`sequence_number`）是事件用于排序和去重的稳定标识。V1
刻意不包含 event ID、parent ID、subject 或图关系。每个文件由一个生产方创建一
次 invocation，因此对于本地有序日志 Profile，该组合标识已经足够。跨传输信
封映射有意留给引入对应传输方式的提案定义。

`attributes` 承载不透明的跨系统关联信息，例如 Eval 任务 ID、CI 构建 ID、请求
ID、重试次数或 Trace Context。V1 CLI 属性是 invocation 默认值：Publisher 将
它们原样复制到每条事件。传输字段本身仍是 event-scoped，因此消费方不得依赖同
一 invocation 内每条事件的属性值完全相同；未来生产方可以在不改变信封的情况
下增加事件局部属性。Key 必须带有命名空间，例如
`com.alibaba.aone.eval_task_id`，并匹配
`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$`。`skill-up.*` 命名空间保留给本
项目的生产方定义属性，`--event-attribute` 会拒绝该前缀。其他扩展所有方应使
用自己控制的反向 DNS 前缀。核心事件语义绝不能依赖属性，中继方必须原样保留未
知属性。属性不得承载进度计数、阶段、结果、Prompt、模型输出、凭据或其他敏感
数据。V1 限制每条事件最多包含 32 个属性；UTF-8 Key 最多 128 字节，UTF-8
Value 最多 1024 字节，序列化后的整个 attributes 对象最多 16 KiB。空 Key 或
空 Value 非法。

本注册表中的核心事件名称保留给 skill-up。扩展事件类型必须使用由扩展所有方控
制的点分命名空间，并匹配与属性 Key 相同的小写 Token 模式，例如
`com.example.evaluation.cached`。

### 首批事件类型

| 事件 | 必填 payload 字段 |
| --- | --- |
| `run_started` | `engine`、`skill_name`、`task_total`、`iterations_total` |
| `run_progress` | `phase`、`task_total`、`completed_tasks`、`running_tasks`、`passed`、`failed`、`errored`、`skipped`、`elapsed_ms` |
| `iteration_started` | `iteration` |
| `case_started` | `task_id`、`iteration`、`case_id`、`configuration`、`task_index`、`task_total`、`title` |
| `case_completed` | `task_id`、`iteration`、`case_id`、`configuration`、`task_index`、`task_total`、`title`、`completed_tasks`、`status`、`duration_ms`；可选 `pass_rate` |
| `iteration_completed` | `iteration`、`invocation_completed_tasks`、`passed`、`failed`、`errored`、`skipped`、`duration_ms` |
| `run_finished` | `status`、`completed_tasks`、`passed`、`failed`、`errored`、`skipped`、`duration_ms` |

### Payload 字段定义

以下传输格式定义具有规范性。所有整数都是 JSON integer，且不得超过 JavaScript
最大安全整数 `9007199254740991`。所有已定义的 v1 字段在出现时都不得为
`null`；可选字段没有值时应省略，而不是编码为 `null`。

| 事件 | 字段 | JSON 类型 | 约束 |
| --- | --- | --- | --- |
| `run_started` | `engine` | string | 非空的最终引擎名称 |
| `run_started` | `skill_name` | string | 非空的最终 Skill 名称 |
| `run_started`、`run_progress`、Case 事件 | `task_total` | integer | invocation 全局最终总数，范围为 `[1, 9007199254740991]` |
| `run_started` | `iterations_total` | integer | 本次 invocation 计划的 iteration 数量，至少为 `1` |
| `run_progress` | `phase` | string | 非空的 Run 级阶段 Token；v1 生产方使用 `preparing`、`executing` 或 `finalizing`；消费方必须把未知值映射为未知阶段 |
| `run_progress` | `running_tasks` | integer | 当前快照中 invocation 全局正在执行的任务数，范围为 `[0, task_total]` |
| `run_progress` | `elapsed_ms` | integer | 当前快照中 invocation 已运行的非负毫秒数 |
| Iteration 和 Case 事件 | `iteration` | integer | 实际 iteration 编号，至少为 `1`；向已有工作区追加时可从大于 `1` 开始 |
| Case 事件 | `task_id` | string | 不透明、非空，在 invocation 内稳定且唯一的关联 ID；消费方不得解析其结构 |
| Case 事件 | `case_id` | string | 非空的源 Case 标识 |
| Case 事件 | `configuration` | string | 必须为 `with_skill` 或 `without_skill` |
| Case 事件 | `task_index` | integer | invocation 全局稳定计划索引，范围为 `[1, task_total]` |
| Case 事件 | `title` | string | 人类可读的 Case 标题 |
| `run_progress`、Case 完成事件和 Run 完成事件 | `completed_tasks` | integer | invocation 全局累计终态任务数，范围为 `[0, task_total]` |
| `iteration_completed` | `invocation_completed_tasks` | integer | iteration 完成时 invocation 全局累计终态任务数，范围为 `[0, task_total]` |
| `case_completed` | `status` | string | 必须为 `PASS`、`FAIL`、`ERROR` 或 `SKIP` |
| `case_completed` | `pass_rate` | number | 可选的有限数值，范围为 `[0, 1]`；不可用时省略 |
| Case、Iteration 和 Run 完成事件 | `duration_ms` | integer | 非负的毫秒耗时 |
| `run_progress`、Iteration 和 Run 完成事件 | `passed`、`failed`、`errored`、`skipped` | integer | 非负任务数；在 `run_progress` 和 `run_finished` 中统计整个 invocation，在 `iteration_completed` 中仅统计当前 iteration |
| `run_finished` | `status` | string | 必须为 `COMPLETED`、`ERROR` 或 `CANCELLED` |

每个 `duration_ms` 和 `elapsed_ms` 都基于单调时钟计算。Case 耗时覆盖
`case_started` 之后的全部工作，包括 Agent 执行和 Judge；它不是现有仅包含
Agent 耗时的 `EvalResult.DurationMs`。由于任务可以并发，Iteration 和 Run 耗
时不是子任务耗时之和。

每条 `run_progress` 都是累计的、可替换的运行态快照，并满足：

```text
completed_tasks == passed + failed + errored + skipped
completed_tasks + running_tasks <= task_total
```

Emitter 会在 Run 阶段变化、任务开始或完成时发布快照；Run 保持活跃时，即使阶
段和计数没有变化也会周期发布。阶段和计数保持不变、但 `elapsed_ms` 已刷新的周
期快照就是存活心跳；v1 不增加单独的心跳事件或标记。首个实现使用固定的 30 秒
周期。该周期是生产方策略，而不是传输 schema 字段。消费方在判断停滞时应记录
并使用自己的接收时间，而不应假设生产方时钟已经同步。超时表示进度不确定或中
断；不得据此合成 Case 结果。

`COMPLETED` 是生命周期状态，表示所有计划任务都已进入 Case 终态；它不表示所
有 Case 都通过，也不单独保证命令退出码为零。`ERROR` 表示调用级错误阻止了正
常完成；`CANCELLED` 表示调用因取消而停止。进度快照不是最终评测结论；完整报
告仍是评测结果的权威来源，事件日志描述的是运行生命周期和增量状态。

### 内部事件模型

第一个实现应在传输格式契约稳定前把事件系统保留为内部能力。建议使用
`internal/evalevent` 包；v1 不向遵循语义化版本的 `pkg/` 公共 API 增加类型。

具体 Go 模型应使用强类型 payload，而不是 `map[string]any`。以下定义展示预期
API；其中 JSON tag 和传输值具有规范性：

```go
type Type string

const (
    ProtocolVersion uint64 = 1
    MaxSafeInteger  uint64 = 9007199254740991
)

const (
    EventRunStarted         Type = "run_started"
    EventRunProgress        Type = "run_progress"
    EventIterationStarted   Type = "iteration_started"
    EventCaseStarted        Type = "case_started"
    EventCaseCompleted      Type = "case_completed"
    EventIterationCompleted Type = "iteration_completed"
    EventRunFinished        Type = "run_finished"
)

type Configuration string
type CaseStatus string
type RunStatus string
type RunPhase string

const (
    ConfigurationWithSkill    Configuration = "with_skill"
    ConfigurationWithoutSkill Configuration = "without_skill"

    CaseStatusPass  CaseStatus = "PASS"
    CaseStatusFail  CaseStatus = "FAIL"
    CaseStatusError CaseStatus = "ERROR"
    CaseStatusSkip  CaseStatus = "SKIP"

    RunStatusCompleted RunStatus = "COMPLETED"
    RunStatusError     RunStatus = "ERROR"
    RunStatusCancelled RunStatus = "CANCELLED"

    RunPhasePreparing  RunPhase = "preparing"
    RunPhaseExecuting  RunPhase = "executing"
    RunPhaseFinalizing RunPhase = "finalizing"
)

type Payload interface {
    eventType() Type
    eventVersion() uint64
}

type Event struct {
    ProtocolVersion uint64            `json:"protocol_version"`
    EventVersion    uint64            `json:"event_version"`
    SequenceNumber  uint64            `json:"sequence_number"`
    InvocationID    string            `json:"invocation_id"`
    Time            time.Time         `json:"time"`
    Type            Type              `json:"event"`
    LastEvent       bool              `json:"last_event,omitempty"`
    Attributes     map[string]string `json:"attributes,omitempty"`
    Payload         Payload           `json:"payload"`
}

type RunStartedPayload struct {
    Engine          string `json:"engine"`
    SkillName       string `json:"skill_name"`
    TaskTotal       uint64 `json:"task_total"`
    IterationsTotal uint64 `json:"iterations_total"`
}

type RunProgressPayload struct {
    Phase          RunPhase `json:"phase"`
    TaskTotal      uint64   `json:"task_total"`
    CompletedTasks uint64   `json:"completed_tasks"`
    RunningTasks   uint64   `json:"running_tasks"`
    ResultCounts
    ElapsedMS uint64 `json:"elapsed_ms"`
}

type IterationStartedPayload struct {
    Iteration uint64 `json:"iteration"`
}

type TaskFields struct {
    TaskID        string        `json:"task_id"`
    Iteration     uint64        `json:"iteration"`
    CaseID        string        `json:"case_id"`
    Configuration Configuration `json:"configuration"`
    TaskIndex     uint64        `json:"task_index"`
    TaskTotal     uint64        `json:"task_total"`
    Title         string        `json:"title"`
}

type CaseStartedPayload struct {
    TaskFields
}

type CaseCompletedPayload struct {
    TaskFields
    CompletedTasks uint64     `json:"completed_tasks"`
    Status         CaseStatus `json:"status"`
    PassRate       *float64   `json:"pass_rate,omitempty"`
    DurationMS     uint64     `json:"duration_ms"`
}

type ResultCounts struct {
    Passed  uint64 `json:"passed"`
    Failed  uint64 `json:"failed"`
    Errored uint64 `json:"errored"`
    Skipped uint64 `json:"skipped"`
}

type IterationCompletedPayload struct {
    Iteration                uint64 `json:"iteration"`
    InvocationCompletedTasks uint64 `json:"invocation_completed_tasks"`
    ResultCounts
    DurationMS uint64 `json:"duration_ms"`
}

type RunFinishedPayload struct {
    Status         RunStatus `json:"status"`
    CompletedTasks uint64    `json:"completed_tasks"`
    ResultCounts
    DurationMS uint64 `json:"duration_ms"`
}
```

`PassRate` 使用指针，因为零是合法值，而不可用值必须省略。构造函数和
Publisher 在发布前校验安全整数范围和枚举值。`Payload.eventType` 和
`eventVersion` 让 Publisher 推导 `Event.Type` 和 `Event.EventVersion`，避免
Payload 与事件类型或版本不匹配。Publisher 对 invocation 属性校验并复制一
次，随后把副本视为不可变，并附加到每条事件。只有 Publisher 可以设置其余信封
字段和 `LastEvent`，调用方不能覆盖。Publisher 为 invocation 生成一个 UUID，
并把时间转换为 UTC，使 `time.Time` 按要求编码为 `Z` 时区偏移。

### 任务进度模型

一个任务是（`iteration`、`case_id`、`configuration`）元组。因此 Benchmark
模式会把一个源 Case 展开为两个任务。`task_id` 在 invocation 内稳定且唯一。
消费方使用（`invocation_id`、`task_id`）关联任务事件，并使用显式元组字段理解
任务语义；不得从 `task_id` 中解析结构。

例如，生产方可以分配以下不透明 ID：

```text
task-1
```

该表示形式是非规范示例，不属于传输格式兼容契约。

任务规划在 `run_started` 前完成。该事件恰好发出一次，并且位于所有进度、
Iteration 或 Case 事件之前；其中的 `task_total` 是 Case 过滤、Benchmark 配置
展开和 iteration 展开后的最终值。V1 有意只覆盖成功规划之后的评测执行；配置
和规划失败通过命令结果体现，而不使用更早的 invocation 事件。

每个计划任务获得一个 invocation 全局、稳定且从 `1` 开始的 `task_index`；该索
引与并发任务实际开始或完成的顺序无关。`completed_tasks` 是 invocation 全局计
数，从 `0` 开始，并在任务发出终态 `case_completed` 时恰好增加一次。因此实现
不能直接透传每个 iteration 都会重置的 `ProgressObserver` 索引。

`run_progress` 和 `run_finished` 中的计数覆盖整个 invocation。
`iteration_completed` 中的状态计数只覆盖当前 iteration，而其中名称明确的
`invocation_completed_tasks` 字段报告同一时刻的全局进度。每个
`run_finished` 都满足：

```text
completed_tasks == passed + failed + errored + skipped
```

当 `run_finished.status` 为 `COMPLETED` 时，还必须满足：

```text
completed_tasks == task_total
```

以 `ERROR` 或 `CANCELLED` 可控收尾的调用可以满足
`completed_tasks < task_total`。即使启用 Benchmark、多 iteration 和并行执
行，CI 也能据此展示无歧义的任务进度。除非产品界面针对每个 `case_id` 单独聚合
了全部展开任务，否则不得把 `task_total` 标示为源 Case 总数。

### 任务规划与生命周期 Emitter

实现必须在打开事件流前构建一份不可变的 invocation 全局任务计划。计划确定：

- 最终输出目录、起始 iteration 和 iteration 数量；
- include/exclude 过滤后的选中 Case；
- Benchmark 展开（先 `with_skill`，启用时再 `without_skill`）；
- 每个展开任务的全局 `task_index`、`task_id` 和 `task_total`。

计划顺序固定为：iteration 编号升序、过滤后 Case 顺序，然后先 `with_skill` 后
`without_skill`。Planner 在构造不可变计划时分配不透明 `task_id`。首个实现可以
从全局索引派生，例如：

```text
task-<task_index>
```

该表示形式是实现细节，不属于传输格式契约。本协议不对 `case_id` 增加词法限制，
也不依赖或改变命令层的产物路径校验。并发执行时，任务开始和完成事件可以按任
意顺序出现，但计划身份始终不变。Runner 必须执行这份计划，不能再重新计算第二
份任务列表，从而避免事件总数和实际工作发生偏差。

invocation 级生命周期 Emitter 位于通用 Publisher 之上。它持有任务计划、互斥
锁、已开始/已完成任务集合、invocation 计数、每个 iteration 的计数、当前 Run
阶段、invocation 开始时间和周期进度循环。建议提供：

```go
Start(ctx, engine, skillName)
SetPhase(ctx, phase)
Heartbeat(ctx)
IterationStarted(ctx, iteration)
CaseStarted(ctx, plannedTask)
CaseCompleted(ctx, plannedTask, status, passRate, duration)
IterationCompleted(ctx, iteration, duration)
Finish(ctx, runStatus, duration)
```

Emitter 在同一排序锁下串行化每次生命周期状态变更、对应的生命周期事件和由此
产生的进度快照。如果在发布顺序之外递增 `completed_tasks`，可能出现较大的序
号反而带有更小计数。Emitter 还会拒绝重复开始或完成、未知任务、
`run_started` 前的事件，以及 `run_finished` 后的生命周期事件。

每个计划 iteration 在自身所有 Case 事件前发出一条 `iteration_started`。只有
该 iteration 的所有计划任务都进入 Case 终态后，才发出
`iteration_completed`；中断的 iteration 可以没有该事件。到达任务级结果的任
务恰好发出一条 `case_started`，随后恰好发出一条 `case_completed`，包括映射
为 `ERROR` 或 `SKIP` 的结果。invocation 级错误或取消可能在
`case_completed` 之前中断活动任务；从未开始的任务不发出任何 Case 事件。

`Start` 发出 `run_started` 和一条处于 `preparing` 阶段的初始
`run_progress` 快照。`SetPhase`、`CaseStarted` 和 `CaseCompleted` 在状态变更
后发出新的 `run_progress`；`Heartbeat` 只发出一条新快照，其中当前阶段和计数
保持不变，`elapsed_ms` 刷新。进度循环每 30 秒调用一次 `Heartbeat`，直到
`Finish` 开始，并且不创建单独的事件类型。

`CaseCompleted` 恰好一次地递增 invocation 和 iteration 计数。
`IterationCompleted`、进度快照和 `Finish` 从 Emitter 状态生成 Payload 计
数，不接受调用方传入的计数。`Finish` 首先停止心跳、进入 `finalizing`、清除因
invocation 取消或错误而已不再执行的活动任务且不把它们计为完成，然后发出最终
进度快照。`Finish(COMPLETED)` 校验所有计划任务都已完成；`ERROR` 和
`CANCELLED` 允许存在未完成任务。v1 随后通过 `PublishLast` 发布
`run_finished`。通用 Publisher 仍允许未来调用方以普通方式发布
`run_finished`，并把最终标记放在后续非生命周期记录上；生命周期 Emitter 保持
关闭。

现有 `ProgressObserver` 保持不变，只负责终端 UI，不作为协议身份或汇总进度的
数据源。

### 事件流示例

下例在 Benchmark 模式下评测一个源 Case，因此生成两个任务。第二个任务运行时
间足够长，因而发出了一条阶段和计数保持不变、`elapsed_ms` 已刷新的周期进度心
跳。JSONL 文件中每个对象各占一个物理行。

```jsonl
{"protocol_version":1,"event_version":1,"sequence_number":1,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:00.000Z","event":"run_started","payload":{"engine":"qodercli","skill_name":"my-skill","task_total":2,"iterations_total":1}}
{"protocol_version":1,"event_version":1,"sequence_number":2,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:00.001Z","event":"run_progress","payload":{"phase":"preparing","task_total":2,"completed_tasks":0,"running_tasks":0,"passed":0,"failed":0,"errored":0,"skipped":0,"elapsed_ms":1}}
{"protocol_version":1,"event_version":1,"sequence_number":3,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:00.002Z","event":"run_progress","payload":{"phase":"executing","task_total":2,"completed_tasks":0,"running_tasks":0,"passed":0,"failed":0,"errored":0,"skipped":0,"elapsed_ms":2}}
{"protocol_version":1,"event_version":1,"sequence_number":4,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:00.003Z","event":"iteration_started","payload":{"iteration":1}}
{"protocol_version":1,"event_version":1,"sequence_number":5,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:00.004Z","event":"case_started","payload":{"task_id":"task-1","iteration":1,"case_id":"case-1","configuration":"with_skill","task_index":1,"task_total":2,"title":"Basic flow"}}
{"protocol_version":1,"event_version":1,"sequence_number":6,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:00.005Z","event":"run_progress","payload":{"phase":"executing","task_total":2,"completed_tasks":0,"running_tasks":1,"passed":0,"failed":0,"errored":0,"skipped":0,"elapsed_ms":5}}
{"protocol_version":1,"event_version":1,"sequence_number":7,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:10.004Z","event":"case_completed","payload":{"task_id":"task-1","iteration":1,"case_id":"case-1","configuration":"with_skill","task_index":1,"task_total":2,"title":"Basic flow","completed_tasks":1,"status":"FAIL","pass_rate":0.5,"duration_ms":10000}}
{"protocol_version":1,"event_version":1,"sequence_number":8,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:10.005Z","event":"run_progress","payload":{"phase":"executing","task_total":2,"completed_tasks":1,"running_tasks":0,"passed":0,"failed":1,"errored":0,"skipped":0,"elapsed_ms":10005}}
{"protocol_version":1,"event_version":1,"sequence_number":9,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:10.006Z","event":"case_started","payload":{"task_id":"task-2","iteration":1,"case_id":"case-1","configuration":"without_skill","task_index":2,"task_total":2,"title":"Basic flow"}}
{"protocol_version":1,"event_version":1,"sequence_number":10,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:10.007Z","event":"run_progress","payload":{"phase":"executing","task_total":2,"completed_tasks":1,"running_tasks":1,"passed":0,"failed":1,"errored":0,"skipped":0,"elapsed_ms":10007}}
{"protocol_version":1,"event_version":1,"sequence_number":11,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:40.007Z","event":"run_progress","payload":{"phase":"executing","task_total":2,"completed_tasks":1,"running_tasks":1,"passed":0,"failed":1,"errored":0,"skipped":0,"elapsed_ms":40007}}
{"protocol_version":1,"event_version":1,"sequence_number":12,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:50.006Z","event":"case_completed","payload":{"task_id":"task-2","iteration":1,"case_id":"case-1","configuration":"without_skill","task_index":2,"task_total":2,"title":"Basic flow","completed_tasks":2,"status":"PASS","pass_rate":1.0,"duration_ms":40000}}
{"protocol_version":1,"event_version":1,"sequence_number":13,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:50.007Z","event":"run_progress","payload":{"phase":"executing","task_total":2,"completed_tasks":2,"running_tasks":0,"passed":1,"failed":1,"errored":0,"skipped":0,"elapsed_ms":50007}}
{"protocol_version":1,"event_version":1,"sequence_number":14,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:50.008Z","event":"iteration_completed","payload":{"iteration":1,"invocation_completed_tasks":2,"passed":1,"failed":1,"errored":0,"skipped":0,"duration_ms":50005}}
{"protocol_version":1,"event_version":1,"sequence_number":15,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:50.009Z","event":"run_progress","payload":{"phase":"finalizing","task_total":2,"completed_tasks":2,"running_tasks":0,"passed":1,"failed":1,"errored":0,"skipped":0,"elapsed_ms":50009}}
{"protocol_version":1,"event_version":1,"sequence_number":16,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:50.010Z","event":"run_finished","last_event":true,"payload":{"status":"COMPLETED","completed_tasks":2,"passed":1,"failed":1,"errored":0,"skipped":0,"duration_ms":50010}}
```

### 消费规则

- 在解释任何事件 Payload 前，先解析足够的信封内容以读取
  `protocol_version`。v1 消费方必须拒绝或隔离协议大版本缺失、格式错误、混用
  或不受支持的 invocation。
- 按 `invocation_id` 分组，并按 `sequence_number` 处理事件。
- 将缺失序号识别为缺口，并用（`invocation_id`、`sequence_number`）对重放去重。
- 在受支持的协议版本内，忽略未知的可选信封字段、未知事件类型、不支持的
  `event_version` 值和未知 Payload 字段。即使忽略 Payload，也仍要处理
  `sequence_number` 和 `last_event` 等已知信封字段。
- 对于受支持的 `run_progress` 版本，保留其计数，但把未知 `phase` Token 渲染
  为未知阶段；`phase` 有意设计为开放值。
- 解释事件时忽略未知属性。透明中间方使用相同的（`invocation_id`、
  `sequence_number`）身份重新发出事件时，必须保留完整的语义记录，包括未知信
  封字段、Payload 字段和属性；经过转换的记录不得复用原始身份。
- 不假设并发任务按声明顺序产生事件。
- 把唯一一条带 `last_event: true` 的记录视为生产方已经完成逻辑事件流的证明。
  它必须具有最大的序号，且其后不得存在任何记录。V1 把该标记放在
  `run_finished` 上，但通用消费方不得将该标记永久绑定到这种事件类型。
- 只解析以换行结束的记录；尾部记录未结束时应缓冲或忽略，等待更多字节。
- 把文件中间完整但格式错误的记录视为事件流损坏，而不是跳过它并静默闭合序号
  缺口。

`run_progress` 是运行态快照，而不是结果账本。消费方可以用它展示进度和识别生
产方停滞，但最终报告仍是详细评测结果的权威来源。如果报告与事件日志不一致，
消费方应展示该不一致，并使用报告中的结果数据。

消费方必须区分四种结果：`run_finished` 结束 Run 生命周期，`last_event` 结束逻
辑事件流，最终报告承载详细评测结果，而进程退出码还反映 Case 结果策略和请求的
Sink 投递情况。V1 通常会同时产生这些结果，但它们不是彼此的别名。

### Sink 与传输模型

事件生产面向可扩展 Sink 边界，而不是继续向 `ProgressObserver` 增加位置参数：

```go
type EventSink interface {
    Publish(context.Context, Event) error
}
```

具体 Publisher API 提供 `Publish`、`PublishLast`、`Err` 和 `Close`。两个发布
方法都接收强类型 `Payload`，并构造一条完整封装的不可变事件。
`PublishLast` 设置 `last_event: true`，并在该记录后永久关闭发布，从而让一个组
件独占唯一最终标记。`Err` 返回粘性的发布/关闭错误且不清除状态，`Close` 可重
复调用。生命周期调用点可以记录当前事件返回的错误，但命令收尾时必须使用
`Err`。

Publisher 对 invocation 的不可变属性校验一次，并把它们复制到每条事件。事件
类型和事件版本来自类型化 Payload；调用方不能把任意类型字符串与无关的
Payload 组合。

invocation 级 Publisher 或 Dispatcher 负责事件身份和顺序。它将并发发布串行化，
只分配一次 `invocation_id`、下一个连续 `sequence_number` 和 `time`。Sink 只
负责传输或序列化事件，不得分配或修改事件身份。

V1 标准化一个同步的本地 JSONL 文件 Sink。`EventSink` 边界让序列化与生命周期
状态保持分离，但 v1 不对投递、重试、扇出、回调、Socket 或 OTLP 作出承诺。任
何远程或多 Sink 传输都需要单独提案。现有 stdout 格式保持不变，也不属于事件
日志兼容契约。Context 可以在写入开始前阻止发布，但不能让已经进行中的同步文
件写入变得可中断。

### JSONL 文件语义

JSONL 文件 Sink 必须：

- 启动时只创建或截断一次指定路径；
- 编码 UTF-8 JSON Lines，在 Sink 写入锁内序列化已完整封装的事件、追加 `\n`
  并写入完整字节切片；
- 处理短写且不交错记录，并让完整记录及时可见；
- 不对每条事件执行 `fsync`，也不依赖延迟的缓冲 flush；
- 拒绝序列化后超过 1 MiB 的事件，并把错误记录为粘性 Sink 失败；
- 只保证每个成功写入且以换行结束的记录都是合法 JSON。

并发读取进程可能观察到部分写入的最后一条记录，特别是在进程崩溃或磁盘失败
后。因此消费方不得把未以换行结束的尾部记录解析为完整事件。

### 命令行契约

只有 `skill-up run` 新增参数：

```text
--event-log <path>             Write v1 evaluation events as JSON Lines to path
--event-attribute <key=value>  Attach a namespaced string attribute (repeatable)
```

v1 命令契约如下：

- 两个参数都是可选的，并且都没有配置文件或环境变量形式；
- 空值和 `-` 非法；stdout 继续面向人类，永不作为事件传输通道；
- 相对路径以进程工作目录为基准解析；
- 父目录必须已存在；命令以受进程 umask 影响的 `0600` 模式创建文件，或者在不
  改变已有文件权限的情况下将其截断一次；
- 不要求文件扩展名，每个文件只包含一次 invocation；
- 规范化后的路径不得位于任何计划执行的 `iteration-N` 目录中，因为 Runner
  会在评测前删除并重建这些目录；
- v1 中 `--event-log` 与 `--dry-run` 互斥，因为 dry-run 不执行任何生命周期任
  务；
- `--event-attribute` 要求同时指定 `--event-log`；每个值在第一个 `=` 处分
  隔，且必须包含该分隔符；Key 和 Value 都不得为空；重复 Key 非法；同时还要
  拒绝保留的 `skill-up.*` 前缀，并满足事件信封章节中的命名空间和大小规则；
- 未指定参数时，事件发布为 no-op，现有 stdout、报告和退出行为保持不变。

计划、路径和属性校验必须在打开文件前完成，防止无效计划截断已有事件日志。成
功打开文件并发布 `run_started` 后，事件日志才算启动完成。打开失败时不产生事
件流。如果首条记录无法写入，命令关闭 Publisher，并在加载凭据或调用 Agent 前
退出；它不会假装失败文件包含完整的终止事件流。

### 命令执行流程

`runEval` 应重构为一条明确的收尾路径：

1. 解析参数和属性，加载并校验配置，应用 Case 过滤和命令行覆盖。
2. 拒绝 dry-run/event-log 冲突，然后构建不可变任务计划。
3. 校验并打开请求的 JSONL Sink，创建 Publisher 和生命周期 Emitter，发布
   `run_started` 和初始 `preparing` 进度快照。首条记录失败时采用上述启动行
   为。
4. 加载凭据并创建 Agent。从这一步开始，所有可控失败都会开始尽力收尾，并尝试
   产生状态为 `ERROR` 的 `run_finished`。
5. 进入 `executing`，执行计划中的 iteration，并在阶段和 Case 变化后以及工作
   活跃期间每 30 秒发出进度。
6. 停止心跳，进入 `finalizing`，清除因 invocation 取消/错误而已不再活动的任
   务且不把它们计为完成；当所有计划任务都进入 Case 终态时选择
   `COMPLETED`；遇到
   `context.Canceled` 或 `context.DeadlineExceeded` 时选择 `CANCELLED`；其他
   调用级失败选择 `ERROR`。
7. 通过 `PublishLast` 发布 `run_finished`，关闭文件，并使用 `errors.Join` 合
   并评测错误、Case 结果错误、Publisher 错误和关闭错误。

步骤 6 和 7 不得复用已经取消的 invocation Context，而使用新的 Context，使可
控取消能够尽力写入最终快照和 `run_finished`。V1 不保证收尾耗时上界：同步文
件写入一旦开始，Context 取消不能中断该写入。CI 必须保留外层任务或进程超时。
可中断或异步 Sink 需要另行设计传输方案。

只要所有计划任务都执行过，Case 失败和 Case 错误不会把 `COMPLETED` 改成其他
状态；现有 `exitStatusError` 仍会让相关 `with_skill` 结果使命令失败。Sink 错
误同样不会改写生命周期状态，但其粘性错误会参与最终的非零命令结果。如果进程
在可控收尾前被杀死或发生 panic，事件流可以像消费契约已定义的那样缺少
`run_finished`。

### 失败语义

- 无法创建或打开显式请求的事件日志时，启动失败。
- 第一次 JSONL 写入失败是粘性的，并会禁用该 Sink，防止部分或有缺口的文件在
  后续获得会造成误导的最终标记。V1 不重试，也不声称后续记录到达了失败的
  Sink。
- 同步文件写入如果一直阻塞而不返回，可能阻止收尾，并使事件流缺少
  `run_finished` 或 `last_event`。V1 不能代替外层 CI 任务或进程超时。
- Sink 在运行中失败后，评测可以继续生成报告。所有粘性 Sink 错误最终汇总到命
  令错误中，并使命令在收尾后以非零状态退出。
- 写入失败会留下不完整的逻辑事件流。仅由 `Close` 报告的失败可能发生在消费方
  已观察到逻辑完整事件流之后，并且无法撤回其最终标记。两种情况下 CLI 都以非
  零退出；CI 只有同时看到逻辑完整的文件和成功的命令结果，才能认为事件投递成
  功。
- `last_event: true` 表示生产方在逻辑上完成了事件流；它不保证 `fsync` 持久
  性，也不保证整个命令以零退出。
- `COMPLETED` 不是退出码的别名。已完成的运行仍可能因为 `with_skill` Case 失
  败或出错，或请求的 Sink 失败而非零退出。`ERROR`、`CANCELLED` 和任一粘
  性 Sink 错误都要求非零退出。

### 说明/约束/注意事项

- `protocol_version` 对信封和有序日志规则进行版本化；`event_version` 对某一种
  事件类型的 Payload 进行版本化。
- 事件时间用于诊断；顺序由 `sequence_number` 定义。
- 进度消费方应从观察到一条完整记录的时刻开始计算陈旧时间，而不是使用生产方
  的墙上时间 `time`。
- 未来事件族可以包含摘要、诊断和产物引用。只有在 `run_finished` 被有意设置
  为非最终事件，并且后续恰好有一条记录持有 `last_event` 时，这些事件才能位于
  `run_finished` 之后。
- V1 不预告未来子事件、不要求 DAG、不描述 task 级阶段，也不估算剩余时间。

### 风险与缓解

- **过早锁定 schema。** 分别对信封和每种事件 Payload 进行版本化、保持两者足
  够小，并要求消费方忽略增量字段。
- **进度含义模糊。** 统计展开后的任务，而不是源 Case。
- **错误的存活信号。** 以有上限的频率发出周期快照，并要求消费方使用本地观察
  时间判断停滞。
- **重复、缺失或截断投递。** 提供调用标识、序号、终止语义和尾部记录规则。
- **敏感或过量上下文。** 基础 schema 不包含 Prompt、响应、transcript、凭据
  或产物内容；属性只能是字符串，且有大小限制。

## 设计细节

本次文档 PR 固定 v1 信封、生命周期注册表、消费规则和本地文件 Sink 契约。后
续实现再决定包位置和具体类型，但必须保持这些契约。后续实现还应为每个受支持
的（`protocol_version`、`event`、`event_version`）元组发布机器可读的 JSON
Schema。

下面的 Go 结构和文件位置是不具规范性的实现草图。传输字段、生命周期不变量、
CLI 行为和可观察的失败语义具有规范性；实现可以采用不同的代码组织方式，只要
保持这些契约即可。

Schema Bundle 必须分层：公共信封 Schema 允许未知的可选信封字段、事件类型和
事件版本，而已知元组的 Schema 校验其已知 Payload 字段，同时允许未知的可选
Payload 字段。封闭的顶层 `oneOf` 如果拒绝其他合法的未知事件，就会违反前向兼
容契约。

首个实现预计增加 `--event-log`、可重复的 `--event-attribute`、并发安全 JSONL
文件 Sink、`run_progress` 快照与心跳，以及 run、iteration、case 生命周期边界
事件。它不得继续为 `ProgressObserver` 增加位置参数。

建议的实现边界为：

| 位置 | 职责 |
| --- | --- |
| `internal/evalevent/event.go` | 信封、事件类型、枚举和强类型 Payload |
| `internal/evalevent/publisher.go` | 协议/事件版本、属性复制、序号分配、`PublishLast`、粘性错误和关闭错误汇总 |
| `internal/evalevent/lifecycle.go` | 生命周期状态机、进度心跳、任务去重、累计和每 iteration 计数 |
| `internal/evalevent/jsonl.go` | 不缓冲、换行结尾的文件 Sink |
| `internal/evaluator/plan.go` | 由执行和事件共享的确定性展开任务元数据 |
| `internal/runner/runner.go` | invocation 和 iteration 边界；执行不可变任务计划 |
| `internal/evaluator/evaluator.go` | Case 边界以及结果到事件状态的映射 |
| `internal/cli/run.go` | Event Log/属性参数校验、Sink 生命周期、终止状态选择和最终错误组合 |

`internal/evalevent` 除标准库和 UUID 生成外不依赖其他包；它不得导入 CLI、
Runner、Evaluator、Judge、Report 或 UI 包。Runner/Evaluator 适配器在边界把领
域类型映射为事件枚举。这使传输层保持无环，也避免事件序列化与终端展示耦合。

## 测试计划

针对本次纯文档 PR：

- 使用 JSON 解析器校验所有示例行；
- 校验连续序号、协议/事件版本、字段类型约束、任务总数、进度不变量、心跳示例、
  终态不变量和唯一 `last_event` 标记；
- 校验中英文事件表一致。

未来实现必须补充序列化、不支持的协议拒绝、未知信封/事件/版本跳过、属性校验和
透明中继保留、并发 Publisher 排序、粘性 Sink 失败、中间格式错误记录、尾部残
缺记录、UTF-8 和记录大小限制、Benchmark、多 iteration、假时钟心跳和端到端
消费测试。Planner 和生命周期 Emitter 测试必须验证不透明任务身份与包含路径分
隔符、百分号和非 ASCII 文本的源 Case ID 解耦。命令测试还必须覆盖参数帮助、
可重复属性、空路径和 `-`、dry-run 冲突、相对路径解析、`0600` 创建模式、已有文
件截断、父目录不存在、iteration 工作区冲突、启动写入失败、终止关闭失败，以及
未指定参数时行为不变。生命周期测试必须覆盖 `run_started` 后的凭据失败、Case
失败但状态为 `COMPLETED`、取消后没有剩余活动任务、invocation Context 取消后
的终止事件发布、部分完成的 `ERROR`、并发 Case 下的进度，以及恰好一个通用
`last_event` 标记。

## 缺点

- 在代码实现前定义事件契约，会提前产生兼容性承诺。
- 相比只发射 Case 计数，公共信封更冗长。
- 每事件版本和重复进度快照会增加少量传输与实现复杂度。
- JSONL 易于检查，但不如二进制协议紧凑。
- 同步文件写入停滞时可能阻塞收尾；v1 依赖外层任务或进程超时处理该故障模式。
- 即使评测报告成功生成，粘性 Sink 错误仍会使命令失败。

## 备选方案

- **保留 `--progress-file`。** 对首个场景很直观，但无法覆盖摘要、诊断和产物事
  件。
- **解析 stdout。** 不增加新契约，但终端文本不是稳定 API。
- **使用最终报告。** 适合已完成结果，但无法增量消费。
- **仅使用 OTLP。** 适合可观测平台，但对简单 CI 消费过重，也不是产品事件契
  约。
- **每条记录都使用 CloudEvents。** 它的标准信封、扩展和 SDK 生态对路由事件
  很有用，但没有定义本协议的有序 JSONL 容器、连续序号、缺口或最终标记规则。
  因此 v1 选择更小的直接本地契约，代价是无法立即获得 CloudEvents SDK/Router
  兼容性。任何未来适配器提案都必须定义完整的映射和保留规则；当前属性 Key 不
  承诺直接映射为 CloudEvents 扩展属性。
- **完整复制 Bazel BEP 和 protobuf。** 成熟且表达力强，但对 skill-up v1 过
  于复杂。
- **增加独立心跳事件。** 这样能明确表达存活性，但重复 `run_progress` 更简单，
  并能让较晚开始的消费方获得完整的当前快照。
- **把进度放在信封属性中。** 并非每条事件都需要进度，而不透明字符串会丢失数
  字校验和快照不变量；类型化 `run_progress` Payload 能让公共信封保持通用。
- **增加 `stream_finished`。** 第二个终止事件可以分离事件流和 Run 生命周期，
  但通用 `last_event` 标记用更少的事件类型提供了所需扩展性。

## 所需基础设施

无。本提案只是纯文档协议定义。

## 升级与迁移策略

只有信封或有序日志语义发生不兼容变更时，才改变 `protocol_version`。在同一版
本内增加可选信封字段是兼容变更；删除字段，或改变现有字段的含义、类型或必填
状态时，应提升协议版本。消费方必须先校验版本，再解释任何 Payload。协议版本
缺失、混用或不受支持时，应拒绝或隔离整个 invocation，而不能把它当作未知 v1
事件。

每种事件类型的 `event_version` 独立演进。在同一版本中增加可选 Payload 字段是
兼容变更。重命名、删除、改变字段含义/类型或把可选字段改为必填，只增加对应事
件类型的版本。消费方不支持某个事件/版本组合时，忽略其 Payload，但仍检查序号
和 `last_event`。

核心事件名称保留给 skill-up。第三方事件类型和属性使用命名空间，因此扩展不需
要提升协议版本。`run_progress.phase` 是开放 Token：增加阶段不提升事件版本，
旧消费方将其渲染为未知。`configuration`、Case `status` 和 Run `status` 集合
是封闭的；为其中任一集合增加传输值时，应提升受影响的事件版本。未知枚举值绝
不能被静默映射为已有含义。

首个实现将在
[GitHub Issue #206](https://github.com/alibaba/skill-up/issues/206) 中单独跟踪。
