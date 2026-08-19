---
title: Skill 评测事件协议
authors:
  - "JHWang-1997"
creation-date: 2026-08-18
last-updated: 2026-08-19
status: draft
---

# SUP-0005: Skill 评测事件协议

语言：[English](../0005-skill-evaluation-event-protocol.md) | 中文

<!-- toc -->
- [摘要](#摘要)
- [动机](#动机)
  - [目标](#目标)
  - [非目标](#非目标)
- [需求](#需求)
- [提案](#提案)
  - [事件信封](#事件信封)
  - [首批事件类型](#首批事件类型)
  - [事件流示例](#事件流示例)
  - [消费规则](#消费规则)
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

本提案为 Skill 评测定义一个小型、版本化的事件协议。首个序列化格式为 JSON
Lines：每行是一条独立事件，用于描述 run、iteration 或 case 的生命周期。

本提案先定义协议，不实现生产者或传输方式。后续可由 `--progress-file` 将事件
写入本地文件供 CI 消费，其他发布者和订阅者也可复用同一个事件契约。

## 动机

目前，需要实时评测进度的集成只能解析终端输出，或等待 iteration 完成后才写出
的报告。终端文本是面向人的界面；对 CI 进度上报、看板或其他订阅者而言，最终
报告又出现得太晚。

[Bazel Build Event Protocol](https://bazel.build/remote/bep) 通过带标识、关系和
类型化 payload 的结构化事件描述一次调用，解决了相似问题。SUP-0005 借鉴这些
关键性质，但采用更小、更适合 skill-up 的 JSON 优先格式。

### 目标

- 为评测生命周期定义一个机器可读契约。
- 支持在评测运行过程中增量解析。
- 让消费方可以关联、排序和去重事件。
- 使协议独立于文件、回调、队列或网络服务。
- 首版只覆盖 CI 所需的最小 run、iteration、case 生命周期。

### 非目标

- 在本 PR 中实现 `--progress-file` 或修改 `ProgressObserver`。
- 定义远程事件服务、鉴权、重试或保留策略。
- 传输 prompt、模型响应、transcript、凭据或产物内容。
- 在 v1 中定义 turn、重试尝试、Judge 步骤或工具调用事件。
- 完整复制 Bazel BEP 的图结构或 protobuf schema。

## 需求

- 每条事件都是一个合法 JSON 对象，并以 `\n` 结尾。
- 每条事件都包含 schema 版本、唯一事件 ID、事件类型、时间戳、run ID，以及在
  该 run 内单调递增的序号。
- 事件专属字段放在类型化的 `payload` 对象中。
- 可选的父事件 ID 用于表达生命周期关系。
- 消费方必须兼容未知事件类型和字段。
- 基础事件 schema 不得包含敏感的评测内容。

## 提案

### 事件信封

所有事件使用以下公共信封：

```json
{
  "schema_version": "1",
  "event_id": "evt-01",
  "event": "case_completed",
  "sequence": 4,
  "time": "2026-08-19T02:01:10.451Z",
  "run_id": "run-7f3a",
  "parent_event_id": "evt-03",
  "payload": {}
}
```

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `schema_version` | string | 协议大版本，初始为 `"1"` |
| `event_id` | string | 在当前 run 内唯一的事件标识 |
| `event` | string | 下方注册表中的事件类型 |
| `sequence` | integer | 在当前 run 内单调递增的发射顺序 |
| `time` | string | UTC 的 RFC 3339 时间戳 |
| `run_id` | string | 同一次调用所有事件共享的稳定标识 |
| `parent_event_id` | string | 可选；包含或启动当前工作的生命周期事件 ID |
| `payload` | object | 由事件类型定义的字段 |

`event_id` 和 `parent_event_id` 让订阅方在需要时构建一个小型事件图；
`sequence` 则是文件 tail 与流式处理使用的简单游标。

### 首批事件类型

| 事件 | 必填 payload 字段 |
| --- | --- |
| `run_started` | `engine`、`skill_name`、`cases_total`、`iterations_total` |
| `iteration_started` | `iteration` |
| `case_started` | `iteration`、`case_index`、`case_total`、`case_id`、`configuration`、`title` |
| `case_completed` | `iteration`、`case_index`、`case_total`、`case_id`、`configuration`、`status`、`duration_ms`；可选 `pass_rate` |
| `iteration_completed` | `iteration`、`passed`、`failed`、`errored`、`skipped`；可选 `report_dir` |
| `run_completed` | `iterations_completed`、`status` |

`configuration` 为 `with_skill` 或 `without_skill`。Case 的 `status` 为
`PASS`、`FAIL`、`ERROR` 或 `SKIP`。Run 的 `status` 为 `SUCCEEDED`、
`FAILED` 或 `CANCELLED`；它描述调用是否完成，不表示全部 Case 是否通过。

### 事件流示例

下例为了便于阅读而换行展示；真实 JSONL 流中的每个对象各占一行。

```jsonl
{"schema_version":"1","event_id":"evt-01","event":"run_started","sequence":1,"time":"2026-08-19T02:00:00.000Z","run_id":"run-7f3a","payload":{"engine":"qodercli","skill_name":"my-skill","cases_total":1,"iterations_total":1}}
{"schema_version":"1","event_id":"evt-02","event":"iteration_started","sequence":2,"time":"2026-08-19T02:00:00.001Z","run_id":"run-7f3a","parent_event_id":"evt-01","payload":{"iteration":1}}
{"schema_version":"1","event_id":"evt-03","event":"case_started","sequence":3,"time":"2026-08-19T02:00:00.002Z","run_id":"run-7f3a","parent_event_id":"evt-02","payload":{"iteration":1,"case_index":1,"case_total":1,"case_id":"case-1","configuration":"with_skill","title":"Basic flow"}}
{"schema_version":"1","event_id":"evt-04","event":"case_completed","sequence":4,"time":"2026-08-19T02:01:10.451Z","run_id":"run-7f3a","parent_event_id":"evt-03","payload":{"iteration":1,"case_index":1,"case_total":1,"case_id":"case-1","configuration":"with_skill","status":"PASS","pass_rate":1.0,"duration_ms":70449}}
{"schema_version":"1","event_id":"evt-05","event":"iteration_completed","sequence":5,"time":"2026-08-19T02:01:10.500Z","run_id":"run-7f3a","parent_event_id":"evt-02","payload":{"iteration":1,"passed":1,"failed":0,"errored":0,"skipped":0,"report_dir":"my-skill-workspace/iteration-1"}}
{"schema_version":"1","event_id":"evt-06","event":"run_completed","sequence":6,"time":"2026-08-19T02:01:10.501Z","run_id":"run-7f3a","parent_event_id":"evt-01","payload":{"iterations_completed":1,"status":"SUCCEEDED"}}
```

### 消费规则

- 在同一个 `run_id` 内按 `sequence` 升序处理事件。
- 使用（`run_id`、`event_id`）对重放的事件去重。
- 忽略未知事件类型及未知 payload 字段。
- 不假设并发 Case 按声明顺序产生事件。
- 缺少预期完成事件的流应视为不完整，而不是成功；崩溃或传输中断可能只留下
  一个合法前缀。
- 使用（`case_id`、`configuration`、`iteration`）标识一个 Case 任务。

### 说明/约束/注意事项

- JSONL 是首个序列化格式，不是传输契约。后续本地文件、回调、进程内
  Observer、队列或事件服务都可以承载同一个信封。
- `parent_event_id` 表达有用的生命周期关系，但 v1 不要求消费方构建或校验完整
  DAG。
- 事件时间用于诊断；顺序由 `sequence` 而非时间戳定义。
- 协议只包含摘要和元数据。

### 风险与缓解

- **过早锁定 schema。** 保持 v1 足够小、按类型使用 payload，并要求消费方忽略
  增量字段。
- **重复或重放投递。** 提供稳定事件 ID 和明确的去重规则。
- **事件流不完整。** 定义完成事件，并要求消费方能识别合法前缀而不假设成功。
- **敏感数据泄露。** 基础 schema 不包含 prompt、响应、transcript 或凭据。

## 设计细节

本 PR 只定义线上协议。后续提案或实现 PR 再决定：

- 如何生成 event ID、run ID 和 sequence；
- 内部评测回调如何生成协议事件；
- 首个传输方式是 `--progress-file`、环境变量还是其他集成点；
- 文件生命周期、缓冲、flush 及写入失败行为；
- 未来事件注册表是否增加 turn、retry、judge、artifact 或诊断事件。

除非修订本提案，这些决定都必须保持 v1 信封与消费规则。

## 测试计划

针对本次纯文档 PR：

- 使用 JSON 解析器校验所有示例行；
- 校验 sequence 和父事件引用一致；
- 校验中英文事件表一致。

未来实现必须补充序列化、并发、重放、不完整流及端到端消费测试。

## 缺点

- 在代码实现前定义事件契约，会提前产生兼容性承诺。
- 相比只发射 Case 计数，公共信封更冗长。
- JSONL 易于检查，但不如二进制协议紧凑。

## 备选方案

- **解析 stdout。** 不增加新契约，但终端文本不是稳定 API。
- **使用最终报告。** 适合已完成结果，但无法增量消费。
- **仅使用 OTLP。** 适合可观测平台，但对简单 CI 进度消费过重，也不是产品事件
  契约。
- **完整复制 Bazel BEP 和 protobuf。** 成熟且表达力强，但对 skill-up 首批生命
  周期事件过于复杂。

## 所需基础设施

无。本提案只是纯文档协议定义。

## 升级与迁移策略

版本 `"1"` 只做增量演进：生产者可以增加事件类型或可选 payload 字段，消费方
必须忽略无法理解的内容。重命名、删除、改变既有字段的含义或类型，需要提升
`schema_version` 大版本。

首个实现将单独跟踪，参见
[GitHub issue #206](https://github.com/alibaba/skill-up/issues/206)。
