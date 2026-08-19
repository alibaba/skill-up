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
  - [任务进度模型](#任务进度模型)
  - [事件流示例](#事件流示例)
  - [消费规则](#消费规则)
  - [Sink 与传输模型](#sink-与传输模型)
  - [JSONL 文件语义](#jsonl-文件语义)
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
- 每条事件都包含数字 schema 版本、单调递增序号、稳定 invocation ID、时间
  戳、事件类型和类型化 payload。
- 到达正常收尾阶段的调用恰好发出一条终止 `run_finished` 事件，包括存在失败
  Case 的调用。
- 消费方必须兼容未知事件类型和 payload 字段。
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

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `schema_version` | integer | 协议大版本，初始为 `1` |
| `sequence_number` | integer | invocation 内从 `1` 开始单调递增的发射顺序 |
| `invocation_id` | string | 同一次 `skill-up run` 所有事件共享的稳定 UUID |
| `time` | string | UTC 的 RFC 3339 时间戳 |
| `event` | string | 下方注册表中的事件类型 |
| `last_event` | boolean | 可选；只在 `run_finished` 上出现且为 `true` |
| `payload` | object | 由事件类型定义的字段 |

（`invocation_id`、`sequence_number`）是事件用于排序和去重的稳定标识。V1
刻意不包含 event ID、parent ID 或图关系。

### 首批事件类型

| 事件 | 必填 payload 字段 |
| --- | --- |
| `run_started` | `engine`、`skill_name`、`task_total`、`iterations_total` |
| `iteration_started` | `iteration` |
| `case_started` | `task_id`、`iteration`、`case_id`、`configuration`、`task_index`、`task_total`、`title` |
| `case_completed` | `task_id`、`iteration`、`case_id`、`configuration`、`task_index`、`task_total`、`completed_tasks`、`status`、`duration_ms`；可选 `pass_rate` |
| `iteration_completed` | `iteration`、`completed_tasks`、`passed`、`failed`、`errored`、`skipped`、`duration_ms`；可选 `report_dir` |
| `run_finished` | `status`、`completed_tasks`、`passed`、`failed`、`errored`、`skipped`、`duration_ms` |

`configuration` 为 `with_skill` 或 `without_skill`。Case 的 `status` 为
`PASS`、`FAIL`、`ERROR` 或 `SKIP`。Run 的 `status` 为 `SUCCESS`、
`ERROR` 或 `CANCELLED`；`SUCCESS` 表示调用到达正常收尾阶段，不代表所有
Case 都通过。Case 结果以各状态计数为准。

### 任务进度模型

一个任务是（`iteration`、`case_id`、`configuration`）元组。因此 Benchmark
模式会把一个源 Case 展开为两个任务。`task_id` 在 invocation 内稳定且唯一，可
读形式为：

```text
iteration-1/case-1/with_skill
```

`task_index` 和 `task_total` 描述整个 invocation 的计划任务；
`completed_tasks` 是事件发出时累计进入终态的 Case 任务数。
`iteration_completed` 中的状态计数只覆盖当前 iteration，`run_finished` 中的计数
覆盖整个 invocation。即使启用 Benchmark、多 iteration 和并行执行，CI 也能据
此展示无歧义的进度。

### 事件流示例

下例在 Benchmark 模式下评测一个源 Case，因此生成两个任务。JSONL 文件中每个
对象各占一个物理行。

```jsonl
{"schema_version":1,"sequence_number":1,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:00.000Z","event":"run_started","payload":{"engine":"qodercli","skill_name":"my-skill","task_total":2,"iterations_total":1}}
{"schema_version":1,"sequence_number":2,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:00.001Z","event":"iteration_started","payload":{"iteration":1}}
{"schema_version":1,"sequence_number":3,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:00:00.002Z","event":"case_started","payload":{"task_id":"iteration-1/case-1/with_skill","iteration":1,"case_id":"case-1","configuration":"with_skill","task_index":1,"task_total":2,"title":"Basic flow"}}
{"schema_version":1,"sequence_number":4,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:01:10.451Z","event":"case_completed","payload":{"task_id":"iteration-1/case-1/with_skill","iteration":1,"case_id":"case-1","configuration":"with_skill","task_index":1,"task_total":2,"completed_tasks":1,"status":"PASS","pass_rate":1.0,"duration_ms":70449}}
{"schema_version":1,"sequence_number":5,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:01:10.452Z","event":"case_started","payload":{"task_id":"iteration-1/case-1/without_skill","iteration":1,"case_id":"case-1","configuration":"without_skill","task_index":2,"task_total":2,"title":"Basic flow"}}
{"schema_version":1,"sequence_number":6,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:02:05.118Z","event":"case_completed","payload":{"task_id":"iteration-1/case-1/without_skill","iteration":1,"case_id":"case-1","configuration":"without_skill","task_index":2,"task_total":2,"completed_tasks":2,"status":"FAIL","pass_rate":0.5,"duration_ms":54666}}
{"schema_version":1,"sequence_number":7,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:02:05.900Z","event":"iteration_completed","payload":{"iteration":1,"completed_tasks":2,"passed":1,"failed":1,"errored":0,"skipped":0,"duration_ms":125898,"report_dir":"my-skill-workspace/iteration-1"}}
{"schema_version":1,"sequence_number":8,"invocation_id":"018f8f20-7a7d-7d90-a192-4f5ec8f07a2a","time":"2026-08-19T10:02:05.901Z","event":"run_finished","last_event":true,"payload":{"status":"SUCCESS","completed_tasks":2,"passed":1,"failed":1,"errored":0,"skipped":0,"duration_ms":125901}}
```

### 消费规则

- 按 `invocation_id` 分组，并按 `sequence_number` 处理事件。
- 将缺失序号识别为缺口，并用（`invocation_id`、`sequence_number`）对重放去重。
- 忽略未知事件类型及未知 payload 字段。
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

CLI 可以把一条事件扇出到 JSONL 文件 Sink、UI 适配器、回调、OTLP 适配器、
socket 或未来远程发布器。现有 stdout 格式保持不变，也不属于事件日志兼容契约。

JSONL 是 v1 序列化；文件是首个建议 Sink，而不是协议本身。

### JSONL 文件语义

后续 JSONL 文件 Sink 必须：

- 启动时只创建或截断一次指定路径；
- 在同一把锁内分配 `sequence_number`、序列化事件、追加 `\n` 并写入完整记录；
- 每条记录执行一次 `Write`，并让完整记录及时可见；
- 不对每条事件执行 `fsync`，也不依赖延迟的缓冲 flush；
- 只保证每个以换行结束的记录都是合法 JSON。

并发读取进程可能观察到部分写入的最后一条记录，特别是在进程崩溃或磁盘失败
后。因此消费方不得把未以换行结束的尾部记录解析为完整事件。

### 失败语义

- 无法创建或打开显式请求的事件日志时，启动失败。
- 运行中写入失败具有粘性。评测可继续以生成报告，但命令在收尾后返回非零状态。
- 失败的 Sink 不得被报告为完整事件流；仅告警后成功退出会让 CI 错信不完整进
  度。

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

## 测试计划

针对本次纯文档 PR：

- 使用 JSON 解析器校验所有示例行；
- 校验递增序号、任务总数、累计进度和唯一终止事件；
- 校验中英文事件表一致。

未来实现必须补充序列化、并发发布、粘性写错误、尾部残缺记录、Benchmark、多
iteration 和端到端消费测试。

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

首个实现将在
[GitHub Issue #206](https://github.com/alibaba/skill-up/issues/206) 中单独跟踪。
