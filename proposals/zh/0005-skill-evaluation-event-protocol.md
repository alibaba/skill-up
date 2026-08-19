---
title: Skill 评测事件日志协议
authors:
  - "JHWang-1997"
creation-date: 2026-08-18
last-updated: 2026-08-19
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

进度上报是第一个使用场景，而不是协议边界。同一日志未来可通过文件、回调、进
程内或远程订阅者承载生命周期、摘要、诊断和产物事件。现有 stdout 继续作为独
立的、面向人的界面。

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
- 按任务描述 Benchmark；一个源 Case 可以展开为多个配置任务。
- 使事件生产独立于具体 Sink 和传输方式。
- 首版只覆盖 CI 所需的最小 run、iteration、case 生命周期。

### 非目标

- 在本 PR 中实现 `--event-log` 或修改运行时行为。
- 定义远程事件服务、鉴权、重试或保留策略。
- 在 v1 中定义 summary、diagnostic、artifact、turn、retry、judge 或 tool
  事件；协议只为它们预留扩展空间。
- 传输 prompt、模型响应、transcript、凭据或产物内容。
- 完整复制 Bazel BEP 的图结构或 protobuf schema。

## 需求

- 每个完整记录都是一个合法 JSON 对象，并以 `\n` 结尾。
- 每条事件都包含数字 schema 版本、连续序号、稳定 invocation ID、时间戳、事
  件类型和类型化 payload。
- 到达可控收尾阶段的调用恰好发出一条终止 `run_finished` 事件，包括存在失败
  Case 的已完成调用，以及以调用级错误或取消结束的调用。
- 消费方必须兼容受支持 schema 大版本中的未知事件类型和 payload 字段。
- 基础事件 schema 不得包含敏感评测内容。
- 评测任务并发执行时，事件发射必须安全。

## 提案

### 通用评测事件日志

首个实现建议提供：

```bash
skill-up run --event-log ./events.jsonl
```

该名称描述持久抽象，而不是首个消费场景。CI 可从 Case 事件推导进度；未来集成
可以订阅其他事件族，无需再增加一个输出参数或改变核心发布边界。

### 事件信封

所有事件使用以下公共信封：

```json
{
  "schema_version": 1,
  "sequence_number": 3,
  "invocation_id": "018f8f20-7a7d-7d90-a192-4f5ec8f07a2a",
  "time": "2026-08-19T10:01:10.451Z",
  "event": "case_completed",
  "payload": {}
}
```

| 字段 | JSON 类型 | 是否必填 | 约束 |
| --- | --- | --- | --- |
| `schema_version` | integer | 是 | 本规范中必须为 `1` |
| `sequence_number` | integer | 是 | invocation 全局连续顺序，范围为 `[1, 9007199254740991]` |
| `invocation_id` | string | 是 | 同一次 `skill-up run` 所有事件共享的稳定 UUID |
| `time` | string | 是 | 使用 `Z` 时区偏移的 UTC RFC 3339 时间戳 |
| `event` | string | 是 | 本注册表或兼容 v1 扩展中的非空事件类型 |
| `last_event` | boolean | 否 | 出现时必须为 `true` 且仅用于 `run_finished`；不得为 `null` |
| `payload` | object | 是 | 包含对应事件字段的非空对象 |

（`invocation_id`、`sequence_number`）是事件用于排序和去重的稳定标识。V1
刻意不包含 event ID、parent ID 或图关系。

### 首批事件类型

| 事件 | 必填 payload 字段 |
| --- | --- |
| `run_started` | `engine`、`skill_name`、`task_total`、`iterations_total` |
| `iteration_started` | `iteration` |
| `case_started` | `task_id`、`iteration`、`case_id`、`configuration`、`task_index`、`task_total`、`title` |
| `case_completed` | `task_id`、`iteration`、`case_id`、`configuration`、`task_index`、`task_total`、`completed_tasks`、`status`、`duration_ms`；可选 `pass_rate` |
| `iteration_completed` | `iteration`、`completed_tasks`、`passed`、`failed`、`errored`、`skipped`、`duration_ms` |
| `run_finished` | `status`、`completed_tasks`、`passed`、`failed`、`errored`、`skipped`、`duration_ms` |

### Payload 字段定义

以下传输格式定义具有规范性。所有整数都是 JSON integer，且不得超过 JavaScript
最大安全整数 `9007199254740991`。所有已定义的 v1 字段在出现时都不得为
`null`；可选字段没有值时应省略，而不是编码为 `null`。

| 事件 | 字段 | JSON 类型 | 约束 |
| --- | --- | --- | --- |
| `run_started` | `engine` | string | 非空的最终引擎名称 |
| `run_started` | `skill_name` | string | 非空的最终 Skill 名称 |
| `run_started`、Case 事件 | `task_total` | integer | invocation 全局最终总数，范围为 `[1, 9007199254740991]` |
| `run_started` | `iterations_total` | integer | 本次 invocation 计划的 iteration 数量，至少为 `1` |
| Iteration 和 Case 事件 | `iteration` | integer | 实际 iteration 编号，至少为 `1`；向已有工作区追加时可从大于 `1` 开始 |
| Case 事件 | `task_id` | string | 非空，在 invocation 内稳定且唯一 |
| Case 事件 | `case_id` | string | 非空的源 Case 标识 |
| Case 事件 | `configuration` | string | 必须为 `with_skill` 或 `without_skill` |
| Case 事件 | `task_index` | integer | invocation 全局稳定计划索引，范围为 `[1, task_total]` |
| Case 事件 | `title` | string | 人类可读的 Case 标题 |
| 完成事件 | `completed_tasks` | integer | invocation 全局累计终态任务数，范围为 `[0, task_total]` |
| `case_completed` | `status` | string | 必须为 `PASS`、`FAIL`、`ERROR` 或 `SKIP` |
| `case_completed` | `pass_rate` | number | 可选的有限数值，范围为 `[0, 1]`；不可用时省略 |
| 完成事件 | `duration_ms` | integer | 非负的毫秒耗时 |
| Iteration 和 Run 完成事件 | `passed`、`failed`、`errored`、`skipped` | integer | 非负任务数；在 `iteration_completed` 中仅统计当前 iteration，在 `run_finished` 中统计整个 invocation |
| `run_finished` | `status` | string | 必须为 `COMPLETED`、`ERROR` 或 `CANCELLED` |

每个 `duration_ms` 都是对应 started 与 completed/finished 生命周期边界之间，基
于单调时钟计算的墙上时间。Case 耗时覆盖 `case_started` 之后的全部工作，包括
Agent 执行和 Judge；它不是现有仅包含 Agent 耗时的
`EvalResult.DurationMs`。由于任务可以并发，Iteration 和 Run 耗时不是子任务耗
时之和。

`COMPLETED` 是生命周期状态，表示所有计划任务都已进入 Case 终态；它不表示所
有 Case 都通过，也不单独保证命令退出码为零。`ERROR` 表示调用级错误阻止了正
常完成；`CANCELLED` 表示调用因取消而停止。Case 结果以各状态计数为准。

### 内部事件模型

第一个实现应在传输格式契约稳定前把事件系统保留为内部能力。建议使用
`internal/evalevent` 包；v1 不向遵循语义化版本的 `pkg/` 公共 API 增加类型。

具体 Go 模型应使用强类型 payload，而不是 `map[string]any`。以下定义展示预期
API；其中 JSON tag 和传输值具有规范性：

```go
type Type string

const (
    SchemaVersion  uint64 = 1
    MaxSafeInteger uint64 = 9007199254740991
)

const (
    EventRunStarted         Type = "run_started"
    EventIterationStarted   Type = "iteration_started"
    EventCaseStarted        Type = "case_started"
    EventCaseCompleted      Type = "case_completed"
    EventIterationCompleted Type = "iteration_completed"
    EventRunFinished        Type = "run_finished"
)

type Configuration string
type CaseStatus string
type RunStatus string

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
)

type Payload interface {
    eventType() Type
}

type Event struct {
    SchemaVersion  uint64    `json:"schema_version"`
    SequenceNumber uint64    `json:"sequence_number"`
    InvocationID   string    `json:"invocation_id"`
    Time           time.Time `json:"time"`
    Type           Type      `json:"event"`
    LastEvent      bool      `json:"last_event,omitempty"`
    Payload        Payload   `json:"payload"`
}

type RunStartedPayload struct {
    Engine          string `json:"engine"`
    SkillName       string `json:"skill_name"`
    TaskTotal       uint64 `json:"task_total"`
    IterationsTotal uint64 `json:"iterations_total"`
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
    Iteration      uint64 `json:"iteration"`
    CompletedTasks uint64 `json:"completed_tasks"`
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
Publisher 在发布前校验安全整数范围和枚举值。`Payload.eventType` 让 Publisher
推导 `Event.Type`，避免 payload 与事件类型不匹配。只有 Publisher 可以设置公
共信封字段和 `LastEvent`，调用方不能覆盖。Publisher 为 invocation 生成一个
UUID，并在序列化前把时间转换为 UTC，使 `time.Time` 按要求编码为 `Z` 时区偏
移。

### 任务进度模型

一个任务是（`iteration`、`case_id`、`configuration`）元组。因此 Benchmark
模式会把一个源 Case 展开为两个任务。`task_id` 在 invocation 内稳定且唯一，可
读形式为：

```text
iteration-1/case-1/with_skill
```

任务规划在 `run_started` 前完成。该事件恰好发出一次，并且位于所有 Iteration
或 Case 事件之前；其中的 `task_total` 是 Case 过滤、Benchmark 配置展开和
iteration 展开后的最终值。

每个计划任务获得一个 invocation 全局、稳定且从 `1` 开始的 `task_index`；该索
引与并发任务实际开始或完成的顺序无关。`completed_tasks` 是 invocation 全局计
数，从 `0` 开始，并在任务发出终态 `case_completed` 时恰好增加一次。因此实现
不能直接透传每个 iteration 都会重置的 `ProgressObserver` 索引。

`iteration_completed` 中的状态计数只覆盖当前 iteration，`run_finished` 中的计数
覆盖整个 invocation。每个 `run_finished` 都满足：

```text
completed_tasks == passed + failed + errored + skipped
```

当 `run_finished.status` 为 `COMPLETED` 时，还必须满足：

```text
completed_tasks == task_total
```

以 `ERROR` 或 `CANCELLED` 可控收尾的调用可以满足
`completed_tasks < task_total`。即使启用 Benchmark、多 iteration 和并行执行，
CI 也能据此展示无歧义的进度。

### 任务规划与生命周期 Emitter

实现必须在打开事件流前构建一份不可变的 invocation 全局任务计划。计划确定：

- 最终输出目录、起始 iteration 和 iteration 数量；
- include/exclude 过滤后的选中 Case；
- Benchmark 展开（先 `with_skill`，启用时再 `without_skill`）；
- 每个展开任务的全局 `task_index`、`task_id` 和 `task_total`。

计划顺序固定为：iteration 编号升序、过滤后 Case 顺序，然后先 `with_skill` 后
`without_skill`。v1 规范任务 ID 为：

```text
iteration-<iteration>/<case_id>/<configuration>
```

Case ID 已经过不得包含路径分隔符的校验。并发执行时，任务开始和完成事件可以按
任意顺序出现，但计划身份始终不变。Runner 必须执行这份计划，不能再重新计算第
二份任务列表，从而避免事件总数和实际工作发生偏差。

invocation 级生命周期 Emitter 位于通用 Publisher 之上。它持有任务计划、互斥
锁、已开始/已完成任务集合、invocation 计数和每个 iteration 的计数。建议提供：

```go
Start(ctx, engine, skillName)
IterationStarted(ctx, iteration)
CaseStarted(ctx, plannedTask)
CaseCompleted(ctx, plannedTask, status, passRate, duration)
IterationCompleted(ctx, iteration, duration)
Finish(ctx, runStatus, duration)
```

Emitter 把每次生命周期状态变更和事件发布一起串行化。如果在发布顺序之外递增
`completed_tasks`，可能出现较大的序号反而带有更小计数，因此不能只使用原子计
数。Emitter 还会拒绝重复开始或完成、未知任务、`run_started` 前的事件，以及
`run_finished` 后的事件。

每个计划 iteration 在自身所有 Case 事件前恰好发出一条 `iteration_started`。
只有该 iteration 的所有计划任务都进入 Case 终态后，才发出
`iteration_completed`；中断的 iteration 可以没有该事件。每个已开始任务恰好
发出一条 `case_started`，随后恰好发出一条 `case_completed`，包括映射为
`ERROR` 或 `SKIP` 的任务。因调用错误或取消而从未开始的任务不发出任何 Case
事件。

`CaseCompleted` 恰好一次地递增 invocation 和 iteration 计数。
`IterationCompleted` 和 `Finish` 从 Emitter 状态生成 payload 计数，不接受调用
方传入的计数。`Finish(COMPLETED)` 校验所有计划任务都已完成；`ERROR` 和
`CANCELLED` 允许存在未完成任务。现有 `ProgressObserver` 保持不变，只负责终
端 UI，不作为协议身份或汇总进度的数据源。

### 事件流示例

下例在 Benchmark 模式下评测一个源 Case，因此生成两个任务。JSONL 文件中每个
对象各占一个物理行。

```jsonl
{"schema_version":1,"sequence_number":1,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:00.000Z","event":"run_started","payload":{"engine":"qodercli","skill_name":"my-skill","task_total":2,"iterations_total":1}}
{"schema_version":1,"sequence_number":2,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:00.001Z","event":"iteration_started","payload":{"iteration":1}}
{"schema_version":1,"sequence_number":3,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:00.002Z","event":"case_started","payload":{"task_id":"iteration-1/case-1/with_skill","iteration":1,"case_id":"case-1","configuration":"with_skill","task_index":1,"task_total":2,"title":"Basic flow"}}
{"schema_version":1,"sequence_number":4,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:01:10.451Z","event":"case_completed","payload":{"task_id":"iteration-1/case-1/with_skill","iteration":1,"case_id":"case-1","configuration":"with_skill","task_index":1,"task_total":2,"completed_tasks":1,"status":"FAIL","pass_rate":0.5,"duration_ms":70449}}
{"schema_version":1,"sequence_number":5,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:01:10.452Z","event":"case_started","payload":{"task_id":"iteration-1/case-1/without_skill","iteration":1,"case_id":"case-1","configuration":"without_skill","task_index":2,"task_total":2,"title":"Basic flow"}}
{"schema_version":1,"sequence_number":6,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:02:05.118Z","event":"case_completed","payload":{"task_id":"iteration-1/case-1/without_skill","iteration":1,"case_id":"case-1","configuration":"without_skill","task_index":2,"task_total":2,"completed_tasks":2,"status":"PASS","pass_rate":1.0,"duration_ms":54666}}
{"schema_version":1,"sequence_number":7,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:02:05.900Z","event":"iteration_completed","payload":{"iteration":1,"completed_tasks":2,"passed":1,"failed":1,"errored":0,"skipped":0,"duration_ms":125898}}
{"schema_version":1,"sequence_number":8,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:02:05.901Z","event":"run_finished","last_event":true,"payload":{"status":"COMPLETED","completed_tasks":2,"passed":1,"failed":1,"errored":0,"skipped":0,"duration_ms":125901}}
```

### 消费规则

- 在解释任何其他字段前，先解析足够的信封内容以读取 `schema_version`。v1 消费
  方必须拒绝或隔离大版本缺失、格式错误、混用或不受支持的 invocation。
- 按 `invocation_id` 分组，并按 `sequence_number` 处理事件。
- 将缺失序号识别为缺口，并用（`invocation_id`、`sequence_number`）对重放去重。
- 在受支持的大版本内，忽略未知事件类型及未知 payload 字段。
- 不假设并发任务按声明顺序产生事件。
- 只有带 `last_event: true` 的 `run_finished` 才能证明事件流正常结束；进程崩溃
  可能只留下合法前缀。
- 只解析以换行结束的记录；尾部记录未结束时应缓冲或忽略，等待更多字节。

### Sink 与传输模型

事件生产面向可扩展 Sink 边界，而不是继续向 `ProgressObserver` 增加位置参数：

```go
type EventSink interface {
    Publish(context.Context, Event) error
}
```

具体 Publisher API 应提供 `Publish`、`Err` 和 `Close`。`Publish` 接收强类型
`Payload`，构造一条完整封装的事件，执行扇出，并按 Sink 记录粘性错误。
`Err` 返回这些错误的 `errors.Join` 汇总且不清除状态。`Close` 关闭同时实现
`io.Closer` 的 Sink，把关闭错误记录到同一汇总中，并且可重复调用。生命周期调
用点可以记录当前事件返回的错误，但命令收尾时必须使用 `Err`。Payload 以只包
含标量字段的值保存，每个 Sink 都收到同一条不可变事件的副本。

CLI 可以把一条事件扇出到 JSONL 文件 Sink、UI 适配器、回调、OTLP 适配器、
socket 或未来远程发布器。现有 stdout 格式保持不变，也不属于事件日志兼容契约。

invocation 级 Publisher 或 Dispatcher 负责事件身份和顺序。它将并发发布串行化，
在同一排序临界区内只分配一次 `invocation_id`、下一个连续
`sequence_number` 和 `time`，再把同一个不可变、完整封装的 `Event` 传递给每
个 Sink。扇出顺序与 `sequence_number` 一致；Sink 只负责传输或序列化，不得
分配或修改事件身份。

JSONL 是 v1 序列化；文件是首个建议 Sink，而不是协议本身。

### JSONL 文件语义

后续 JSONL 文件 Sink 必须：

- 启动时只创建或截断一次指定路径；
- 在 Sink 写入锁内序列化已完整封装的事件、追加 `\n` 并写入完整记录；
- 每条记录执行一次 `Write`，并让完整记录及时可见；
- 不对每条事件执行 `fsync`，也不依赖延迟的缓冲 flush；
- 只保证每个以换行结束的记录都是合法 JSON。

并发读取进程可能观察到部分写入的最后一条记录，特别是在进程崩溃或磁盘失败
后。因此消费方不得把未以换行结束的尾部记录解析为完整事件。

### 命令行契约

只有 `skill-up run` 新增参数：

```text
--event-log <path>   Write v1 evaluation events as JSON Lines to path
```

v1 命令契约如下：

- 参数可选、接收一个路径值，并且没有配置文件或环境变量形式；
- 空值和 `-` 非法；stdout 继续面向人类，永不作为事件传输通道；
- 相对路径以进程工作目录为基准解析；
- 父目录必须已存在；命令以受进程 umask 影响的 `0644` 模式创建文件，或在启动
  时恰好截断一次已有文件；
- 不要求文件扩展名，每个文件只包含一次 invocation；
- 规范化后的路径不得位于任何计划执行的 `iteration-N` 目录中，因为 Runner
  会在评测前删除并重建这些目录；
- v1 中 `--event-log` 与 `--dry-run` 互斥，因为 dry-run 不执行任何生命周期任
  务；
- 未指定参数时，事件发布为 no-op，现有 stdout、报告和退出行为保持不变。

计划和路径校验必须在打开文件前完成，防止无效计划截断已有事件日志。成功打开
文件并发布 `run_started` 后，事件日志才算启动完成。打开失败时不产生事件流。
如果 `run_started` 扇出失败，命令会尝试向所有仍然健康的 Sink 发布状态为
`ERROR` 的 `run_finished`，关闭 Publisher，然后在加载凭据或调用 Agent 前退
出。

### 命令执行流程

`runEval` 应重构为一条明确的收尾路径：

1. 解析参数，加载并校验配置，应用 Case 过滤和命令行覆盖。
2. 拒绝 dry-run/event-log 冲突，然后构建不可变任务计划。
3. 校验并打开请求的 JSONL Sink，创建 Publisher 和生命周期 Emitter，发布
   `run_started`。首条事件失败时采用上述启动收尾行为。
4. 加载凭据并创建 Agent。从这一步开始，所有可控失败都产生状态为 `ERROR` 的
   `run_finished`。
5. 执行计划中的 iteration；Runner 发出 iteration 边界，Evaluator 使用计划元
   数据发出 Case 边界。
6. 所有计划任务都进入 Case 终态时选择 `COMPLETED`；遇到
   `context.Canceled` 或 `context.DeadlineExceeded` 时选择 `CANCELLED`；其他
   调用级失败选择 `ERROR`。
7. 恰好发布一次 `run_finished`，关闭所有 Sink，并使用 `errors.Join` 合并评测
   错误、Case 结果错误、Publisher 错误和关闭错误。

只要所有计划任务都执行过，Case 失败和 Case 错误不会把 `COMPLETED` 改成其他
状态；现有 `exitStatusError` 仍会让相关 `with_skill` 结果使命令失败。Sink 错
误同样不会改写生命周期状态，但其粘性错误会参与最终的非零命令结果。如果进程
在可控收尾前被杀死或发生 panic，事件流可以像消费契约已定义的那样缺少
`run_finished`。

### 失败语义

- 无法创建或打开显式请求的事件日志时，启动失败。
- 即使前面的 Sink 失败，扇出仍会为当前事件尝试所有健康 Sink。Sink 失败按 Sink
  记录并保持粘性；它不会阻止其他健康 Sink 接收当前事件、后续事件或
  `run_finished`。v1 不要求重试已失败的 Sink。
- Sink 在运行中失败后，评测可以继续生成报告。所有粘性 Sink 错误最终汇总到命
  令错误中，并使命令在收尾后以非零状态退出。
- 失败的 Sink 不得被报告为完整事件流；仅告警后成功退出会让 CI 错信不完整进
  度。
- `COMPLETED` 不是退出码的别名。已完成的运行仍可能因为 `with_skill` Case 失
  败或出错，或请求的 Sink 失败而非零退出。`ERROR`、`CANCELLED` 和任一粘
  性 Sink 错误都要求非零退出。

### 说明/约束/注意事项

- v1 内新增可选事件类型或 payload 字段属于兼容扩展。
- 事件时间用于诊断；顺序由 `sequence_number` 定义。
- 未来事件族可以包含摘要、诊断和产物引用，但默认不包含敏感内容。
- V1 不预告未来子事件，也不要求 DAG。

### 风险与缓解

- **过早锁定 schema。** 保持信封足够小、每种事件使用独立 payload，并要求消费
  方忽略增量字段。
- **进度含义模糊。** 统计展开后的任务，而不是源 Case。
- **重复、缺失或截断投递。** 提供调用标识、序号、终止语义和尾部记录规则。
- **敏感数据泄露。** 基础 schema 不包含 prompt、响应、transcript、凭据或产物
  内容。

## 设计细节

本次文档 PR 固定 v1 信封、生命周期注册表、消费规则和 Sink 抽象。后续实现再决
定包位置和具体类型，但必须保持这些契约。

首个实现预计增加 `--event-log`、并发安全 JSONL 文件 Sink、`EventSink` 扇出，
并在 run、iteration、case 生命周期边界发布事件。它不得继续为
`ProgressObserver` 增加位置参数。

建议的实现边界为：

| 位置 | 职责 |
| --- | --- |
| `internal/evalevent/event.go` | 信封、事件类型、枚举和强类型 payload |
| `internal/evalevent/publisher.go` | 序号分配、有序扇出、按 Sink 粘性错误和关闭错误汇总 |
| `internal/evalevent/lifecycle.go` | 生命周期状态机、任务去重、累计和每 iteration 计数 |
| `internal/evalevent/jsonl.go` | 不缓冲、换行结尾的文件 Sink |
| `internal/evaluator/plan.go` | 由执行和事件共享的确定性展开任务元数据 |
| `internal/runner/runner.go` | invocation 和 iteration 边界；执行不可变任务计划 |
| `internal/evaluator/evaluator.go` | Case 边界以及结果到事件状态的映射 |
| `internal/cli/run.go` | 参数校验、Sink 生命周期、终止状态选择和最终错误组合 |

`internal/evalevent` 除标准库和 UUID 生成外不依赖其他包；它不得导入 CLI、
Runner、Evaluator、Judge、Report 或 UI 包。Runner/Evaluator 适配器在边界把领
域类型映射为事件枚举。这使传输层保持无环，也避免事件序列化与终端展示耦合。

## 测试计划

针对本次纯文档 PR：

- 使用 JSON 解析器校验所有示例行；
- 校验连续序号、字段类型约束、任务总数、累计进度、终态不变量和唯一终止事件；
- 校验中英文事件表一致。

未来实现必须补充序列化、不支持大版本拒绝、并发 Publisher 排序、扇出间事件身
份一致、按 Sink 粘性失败、尾部残缺记录、Benchmark、多 iteration 和端到端消
费测试。命令测试还必须覆盖参数帮助、空路径和 `-`、dry-run 冲突、相对路径解
析、已有文件截断、父目录不存在、iteration 工作区冲突、启动写入失败、终止关
闭失败，以及未指定参数时行为不变。生命周期测试必须覆盖 `run_started` 后的凭
据失败、Case 失败但状态为 `COMPLETED`、取消、部分完成的 `ERROR`，以及恰好
一条终止事件。

## 缺点

- 在代码实现前定义事件契约，会提前产生兼容性承诺。
- 相比只发射 Case 计数，公共信封更冗长。
- JSONL 易于检查，但不如二进制协议紧凑。
- 即使评测报告成功生成，粘性 Sink 错误仍会使命令失败。

## 备选方案

- **保留 `--progress-file`。** 对首个场景很直观，但无法覆盖摘要、诊断和产物事
  件。
- **解析 stdout。** 不增加新契约，但终端文本不是稳定 API。
- **使用最终报告。** 适合已完成结果，但无法增量消费。
- **仅使用 OTLP。** 适合可观测平台，但对简单 CI 消费过重，也不是产品事件契
  约。
- **完整复制 Bazel BEP 和 protobuf。** 成熟且表达力强，但对 skill-up v1 过
  于复杂。

## 所需基础设施

无。本提案只是纯文档协议定义。

## 升级与迁移策略

Schema 版本 `1` 只做增量演进：生产者可以增加事件类型或可选 payload 字段，
消费方必须忽略无法理解的内容。重命名、删除、改变既有字段的含义或类型，需要
提升 `schema_version` 大版本。

消费方必须先校验大版本，再解释信封剩余部分或 payload。不受支持的大版本必须
被拒绝或隔离，不能作为兼容的未知 v1 事件处理。

首个实现将在
[GitHub Issue #206](https://github.com/alibaba/skill-up/issues/206) 中单独跟踪。
