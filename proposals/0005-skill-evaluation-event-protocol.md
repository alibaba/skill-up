---
title: "Skill Evaluation Event Log Protocol"
authors:
  - "JHWang-1997"
creation-date: 2026-08-18
last-updated: 2026-08-31
status: draft
---

# SUP-0005: Skill Evaluation Event Log Protocol

Language: English | [中文](zh/0005-skill-evaluation-event-protocol.md)

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Requirements](#requirements)
- [Proposal](#proposal)
  - [A General Evaluation Event Log](#a-general-evaluation-event-log)
  - [Event Envelope](#event-envelope)
  - [Initial Event Types](#initial-event-types)
  - [Payload Field Definitions](#payload-field-definitions)
  - [Internal Event Model](#internal-event-model)
  - [Task Progress Model](#task-progress-model)
  - [Task Planning and Lifecycle Emitter](#task-planning-and-lifecycle-emitter)
  - [Example Stream](#example-stream)
  - [Consumer Rules](#consumer-rules)
  - [Sink and Transport Model](#sink-and-transport-model)
  - [JSONL File Semantics](#jsonl-file-semantics)
  - [Command-Line Contract](#command-line-contract)
  - [Command Execution Flow](#command-execution-flow)
  - [Failure Semantics](#failure-semantics)
  - [Notes/Constraints/Caveats](#notesconstraintscaveats)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
- [Test Plan](#test-plan)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Infrastructure Needed](#infrastructure-needed)
- [Upgrade & Migration Strategy](#upgrade--migration-strategy)
<!-- /toc -->

## Summary

This proposal defines a lightweight, versioned evaluation event log for
skill-up. Its first serialization is JSON Lines, with one independent event
per newline-terminated record.

Progress reporting is the first use case, not the boundary of the protocol.
The same log can later carry lifecycle, summary, diagnostic, and artifact
events. V1 deliberately standardizes only a local JSONL file sink; remote
delivery remains a separate future design. Existing stdout remains a separate
human-readable interface.

## Motivation

Integrations that need live evaluation state currently parse terminal output
or wait for reports written after an iteration. Terminal text is optimized for
people, while CI systems, dashboards, and automation need a stable structured
stream.

[Bazel's Build Event Protocol](https://bazel.build/remote/bep) demonstrates
the core separation: console output remains human-oriented while a distinct
event stream serves programmatic consumers. Based on
[design feedback in issue #206](https://github.com/alibaba/skill-up/issues/206#issuecomment-5336855301),
SUP-0005 adopts this principle without copying BEP's protobuf encoding or full
event DAG.

### Goals

- Define a general event log, rather than a case-progress-only file.
- Provide stable invocation identity and event ordering.
- Let consumers detect gaps, replay, and truncated streams.
- Version the common envelope independently from each event payload.
- Model benchmark execution as tasks, where one source case may expand into
  multiple configurations.
- Provide periodic invocation-level progress snapshots and liveness updates.
- Carry bounded, namespaced correlation attributes without weakening typed
  event payloads.
- Keep event production independent of concrete sinks and transports.
- Start with the minimum run, progress, iteration, and case lifecycle needed
  by CI.

### Non-Goals

- Implementing `--event-log` or changing runtime behavior in this PR.
- Defining a remote event service, callback, OTLP adapter, authentication,
  retry, acknowledgement, queueing, or retention policy.
- Defining summary, diagnostic, artifact, turn, retry, judge, or tool events
  in v1; the protocol only reserves room for them.
- Defining task-level execution phases, estimated time remaining, dynamic task
  discovery, or task-plan updates in v1.
- Streaming prompts, model responses, transcripts, credentials, or artifact
  contents.
- Reproducing the full Bazel BEP graph or protobuf schema.
- Adopting CloudEvents as the v1 wire representation. A future adapter can map
  records without changing this ordered-log contract.

## Requirements

- Each complete record is one valid JSON object terminated by `\n`.
- Every event has a numeric protocol version and event version, contiguous
  sequence number, stable invocation ID, timestamp, event type, and typed
  payload.
- Optional event attributes are bounded string pairs, never core event
  semantics or sensitive evaluation content.
- Once an event-enabled invocation has emitted `run_started`, graceful
  finalization emits exactly one lifecycle-ending `run_finished` event,
  including completed runs with failed cases and runs that end with an
  invocation-level error or cancellation.
- A gracefully finalized stream contains exactly one `last_event: true`; the
  marker belongs to the envelope and is not permanently tied to one event
  type.
- Consumers remain compatible with unknown optional envelope fields, event
  types, event versions, attributes, and payload fields within a supported
  protocol major version.
- The base event schema contains no sensitive evaluation content.
- Event emission is safe when evaluation tasks execute concurrently.

## Proposal

### A General Evaluation Event Log

The first implementation should expose:

```bash
skill-up run --event-log ./events.jsonl
```

The name deliberately describes the durable abstraction rather than its first
consumer. CI can consume typed progress snapshots and case lifecycle events,
while future event families can reuse the same file and publisher boundary
without introducing another output flag.

### Event Envelope

Every event uses this envelope:

```json
{
  "protocol_version": 1,
  "event_version": 1,
  "sequence_number": 3,
  "invocation_id": "018f8f20-7a7d-7d90-a192-4f5ec8f07a2a",
  "time": "2026-08-19T10:01:10.451Z",
  "event": "case_completed",
  "attributes": {
    "com.alibaba.aone.eval_task_id": "123456"
  },
  "payload": {}
}
```

| Field | JSON type | Required | Constraints |
| --- | --- | --- | --- |
| `protocol_version` | integer | yes | Common envelope and ordered-log major version; exactly `1` for this specification |
| `event_version` | integer | yes | Payload major version for this event type, in `[1, 9007199254740991]`; exactly `1` for every core event defined here |
| `sequence_number` | integer | yes | Contiguous invocation-wide order in `[1, 9007199254740991]` |
| `invocation_id` | string | yes | Stable UUID shared by all events from one `skill-up run` invocation |
| `time` | string | yes | Time the producer created the event, in RFC 3339 UTC using the `Z` offset |
| `event` | string | yes | Non-empty event type from this registry or a compatible v1 extension |
| `last_event` | boolean | no | When present, exactly `true`, only on the final record, and never `null` |
| `attributes` | object | no | Opaque attributes associated with this event; string keys and values only; never `null` |
| `payload` | object | yes | Non-null object with fields defined by the event type |

The pair (`invocation_id`, `sequence_number`) is the event's stable identity
for ordering and de-duplication. V1 intentionally has no event ID, parent ID,
subject, or graph relationship. One producer creates one invocation per file,
so this composite identity is sufficient for the local ordered-log profile.
Cross-transport envelope mapping is intentionally left to the proposal that
introduces such a transport.

`attributes` carries opaque cross-system correlation such as an Eval task ID,
CI build ID, request ID, retry attempt, or trace context. V1 CLI attributes are
invocation defaults: the publisher copies them unchanged onto every event. The
wire field is nevertheless event-scoped, so consumers must not rely on values
being identical across an invocation; a future producer may add event-local
attributes without changing the envelope. Keys must be
namespaced, such as `com.alibaba.aone.eval_task_id`, and match
`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$`. The `skill-up.*` namespace is
reserved for attributes defined by this project's producer and is rejected by
`--event-attribute`. Other extension owners should use a reverse-DNS prefix
they control. Core event semantics must never depend on an attribute, and
relays must preserve unknown attributes unchanged. Attributes are not a place
for progress counters, phases, results, prompts, model output, credentials, or
other sensitive data. V1 limits an event to 32 attributes, a UTF-8 key to 128
bytes, a UTF-8 value to 1024 bytes, and the serialized attributes object to 16
KiB. Empty keys or values are invalid.

Core event names in this registry are reserved by skill-up. Extension event
types must use a dotted namespace that the extension owner controls and match
the same lowercase token pattern as attribute keys, for example
`com.example.evaluation.cached`.

### Initial Event Types

| Event | Required payload fields |
| --- | --- |
| `run_started` | `engine`, `skill_name`, `task_total`, `iterations_total` |
| `run_progress` | `phase`, `task_total`, `completed_tasks`, `running_tasks`, `passed`, `failed`, `errored`, `skipped`, `elapsed_ms` |
| `iteration_started` | `iteration` |
| `case_started` | `task_id`, `iteration`, `case_id`, `configuration`, `task_index`, `task_total`, `title` |
| `case_completed` | `task_id`, `iteration`, `case_id`, `configuration`, `task_index`, `task_total`, `title`, `completed_tasks`, `status`, `duration_ms`; optional `pass_rate` |
| `iteration_completed` | `iteration`, `invocation_completed_tasks`, `passed`, `failed`, `errored`, `skipped`, `duration_ms` |
| `run_finished` | `status`, `completed_tasks`, `passed`, `failed`, `errored`, `skipped`, `duration_ms` |

### Payload Field Definitions

The following wire definitions are normative. All integers are JSON integers
and are bounded by JavaScript's maximum safe integer,
`9007199254740991`. Every defined v1 field is non-null when present. An
optional field with no value is omitted rather than encoded as `null`.

| Event(s) | Field | JSON type | Constraints |
| --- | --- | --- | --- |
| `run_started` | `engine` | string | Non-empty resolved engine name |
| `run_started` | `skill_name` | string | Non-empty resolved skill name |
| `run_started`, `run_progress`, case events | `task_total` | integer | Final invocation-wide total in `[1, 9007199254740991]` |
| `run_started` | `iterations_total` | integer | Number of iterations planned by this invocation, at least `1` |
| `run_progress` | `phase` | string | Non-empty run-level phase token; v1 producers use `preparing`, `executing`, or `finalizing`; consumers must map unknown values to an unknown phase |
| `run_progress` | `running_tasks` | integer | Invocation-wide number of tasks actively executing at this snapshot, in `[0, task_total]` |
| `run_progress` | `elapsed_ms` | integer | Non-negative invocation elapsed duration at this snapshot |
| iteration and case events | `iteration` | integer | Actual iteration number, at least `1`; it may start above `1` when appending to an existing workspace |
| case events | `task_id` | string | Opaque, non-empty correlation ID that is stable and unique within the invocation; consumers must not parse it |
| case events | `case_id` | string | Non-empty source case identifier |
| case events | `configuration` | string | Exactly `with_skill` or `without_skill` |
| case events | `task_index` | integer | Stable invocation-wide planned index in `[1, task_total]` |
| case events | `title` | string | Human-readable case title |
| `run_progress`, case completion, and run completion events | `completed_tasks` | integer | Invocation-wide cumulative terminal task count in `[0, task_total]` |
| `iteration_completed` | `invocation_completed_tasks` | integer | Invocation-wide cumulative terminal task count in `[0, task_total]` at iteration completion |
| `case_completed` | `status` | string | Exactly `PASS`, `FAIL`, `ERROR`, or `SKIP` |
| `case_completed` | `pass_rate` | number | Optional finite value in `[0, 1]`; omitted when unavailable |
| case, iteration, and run completion events | `duration_ms` | integer | Non-negative elapsed duration in milliseconds |
| `run_progress`, iteration and run completion events | `passed`, `failed`, `errored`, `skipped` | integer | Non-negative task counts; invocation-scoped on `run_progress` and `run_finished`, iteration-scoped on `iteration_completed` |
| `run_finished` | `status` | string | Exactly `COMPLETED`, `ERROR`, or `CANCELLED` |

Each `duration_ms` and `elapsed_ms` is computed from a monotonic clock. Case
duration covers all work after `case_started`, including agent execution and
judging; it is not the existing agent-only `EvalResult.DurationMs`. Iteration
and run durations are not sums of child durations because tasks may execute
concurrently.

Every `run_progress` is a cumulative, replaceable operational snapshot. It
satisfies:

```text
completed_tasks == passed + failed + errored + skipped
completed_tasks + running_tasks <= task_total
```

The emitter publishes a snapshot when the run phase changes, when a task
starts or completes, and periodically while the run remains active even if no
value changed. An unchanged periodic snapshot is the liveness heartbeat; v1
does not add a separate heartbeat event or flag. The first implementation uses
a fixed interval of 30 seconds. The interval is producer policy, not a
wire-schema field. Consumers should record their own receive time and use that,
rather than trusting producer clock synchronization, for stall detection. A
timeout means progress is uncertain or interrupted; it must not synthesize a
Case result.

`COMPLETED` is a lifecycle state: all planned tasks reached a terminal case
status. It does not mean every case passed and does not by itself imply a zero
command exit code. `ERROR` means an invocation-level failure prevented normal
completion; `CANCELLED` means the invocation stopped after cancellation.
Progress snapshots are not final evaluation conclusions. A completed report
remains authoritative for evaluation outcomes; the event log describes
operational lifecycle and incremental state.

### Internal Event Model

The first implementation should keep the event system internal while the
wire contract stabilizes. The proposed package is `internal/evalevent`; v1
does not add types to the semver-stable `pkg/` API.

The concrete Go model should use typed payload values rather than
`map[string]any`. The following definitions are illustrative of the intended
API; JSON tags and wire values are normative:

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

`PassRate` is a pointer because zero is valid while unavailable values must be
omitted. Constructors and the publisher validate safe-integer ranges and enum
values before publication. `Payload.eventType` and `eventVersion` let the
publisher derive `Event.Type` and `Event.EventVersion`, preventing a
payload/type/version mismatch. The publisher validates and copies invocation
attributes once, then treats the copy as immutable and attaches it to every
event. The publisher alone sets the remaining envelope fields and
`LastEvent`; callers cannot override them. It generates one UUID for the
invocation and converts timestamps to UTC so `time.Time` encodes with the
required `Z` offset.

### Task Progress Model

A task is one (`iteration`, `case_id`, `configuration`) tuple. Benchmark mode
therefore creates two tasks from one source case. `task_id` is stable and
unique within the invocation. Consumers correlate task events using
(`invocation_id`, `task_id`) and use the explicit tuple fields for task
semantics; they must not parse structure from `task_id`.

For example, a producer may assign this opaque ID:

```text
task-1
```

This representation is non-normative and is not part of the wire compatibility
contract.

Planning completes before `run_started`. The event is emitted exactly once
before any progress, iteration, or case event, and its `task_total` is final
after case filtering, benchmark configuration expansion, and iteration
expansion. V1 intentionally covers evaluation execution after successful
planning; configuration and planning failures remain visible through the
command result rather than an earlier invocation event.

Each planned task receives one stable, invocation-wide, 1-based `task_index`;
the index is unrelated to concurrent start or completion order.
`completed_tasks` is invocation-wide, starts at `0`, and increases exactly
once when a task emits its terminal `case_completed` event. An implementation
therefore cannot directly forward `ProgressObserver` indices that reset for
each iteration.

`run_progress` and `run_finished` counts cover the whole invocation. Status
counts on `iteration_completed` cover only that iteration, while its explicitly
named `invocation_completed_tasks` field reports global progress at the same
point. Every `run_finished` satisfies:

```text
completed_tasks == passed + failed + errored + skipped
```

When `run_finished.status` is `COMPLETED`, it additionally satisfies:

```text
completed_tasks == task_total
```

An `ERROR` or `CANCELLED` invocation may finish gracefully with
`completed_tasks < task_total`. These rules let CI display unambiguous task
progress even with benchmark mode, multiple iterations, and parallel
execution. Product surfaces must not label `task_total` as a source-case total
unless they separately aggregate all expanded tasks for each `case_id`.

### Task Planning and Lifecycle Emitter

The implementation must build one immutable invocation-wide task plan before
opening the event stream. The plan resolves:

- the final output directory, start iteration, and iteration count;
- selected cases after include/exclude filtering;
- benchmark expansion (`with_skill`, followed by `without_skill` when
  enabled); and
- one global `task_index`, `task_id`, and `task_total` for every expanded task.

Plan order is deterministic: iteration number ascending, filtered case order,
then `with_skill` before `without_skill`. The planner assigns an opaque
`task_id` while constructing the immutable plan. The initial implementation may
derive it from the global index, for example:

```text
task-<task_index>
```

That representation is an implementation detail, not a wire contract. This
protocol adds no lexical restriction to `case_id` and neither depends on nor
changes command-level artifact-path validation. Task start and completion
events may appear in any order under concurrency, but their planned identity
never changes. The runner must execute this plan rather than recompute a second
task list; event totals and actual work therefore cannot drift apart.

An invocation-scoped lifecycle emitter sits above the generic publisher. It
owns the plan, a mutex, started/completed task sets, invocation counts,
per-iteration counts, the current run phase, the invocation start time, and
the periodic progress loop. Its proposed operations are:

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

The emitter serializes each lifecycle state transition, its lifecycle event,
and the resulting progress snapshot under one ordering lock. This is required
because incrementing `completed_tasks` outside the publication order could
produce a later sequence number with a smaller counter. It also rejects
duplicate starts or completions, unknown tasks, events before `run_started`,
and lifecycle events after `run_finished`.

Every scheduled iteration emits one `iteration_started` before any of its case
events. It emits `iteration_completed` only after all tasks planned for that
iteration reach a terminal case status; an interrupted iteration may omit it.
A task that reaches a task-level outcome emits exactly one `case_started`
followed by exactly one `case_completed`, including outcomes mapped to `ERROR`
or `SKIP`. An invocation-level error or cancellation may interrupt an active
task before `case_completed`; tasks never started emit neither case event.

`Start` emits `run_started` and an initial `run_progress` snapshot in the
`preparing` phase. `SetPhase`, `CaseStarted`, and `CaseCompleted` emit their
state transition and then a fresh `run_progress`; `Heartbeat` emits only an
unchanged snapshot. The progress loop calls `Heartbeat` every 30 seconds until
`Finish` begins, without creating a separate event type.

`CaseCompleted` increments invocation and iteration counts exactly once.
`IterationCompleted`, progress snapshots, and `Finish` derive their payload
counts from emitter state rather than caller-supplied counters. `Finish` first
stops the heartbeat, moves to `finalizing`, clears any no-longer-executing
active tasks without counting them as completed, and emits a final progress
snapshot. `Finish(COMPLETED)` validates that all planned tasks completed;
`ERROR` and `CANCELLED` allow unfinished tasks. In v1, it then publishes
`run_finished` through `PublishLast`. The generic publisher nevertheless
permits a future caller to publish `run_finished` normally and put the final
marker on a later non-lifecycle record; the lifecycle emitter remains closed.

The existing `ProgressObserver` remains unchanged and only drives terminal UI;
it is not a source of protocol identity or aggregate progress.

### Example Stream

This compact example evaluates one source case with benchmark mode enabled,
so it produces two tasks. The second task runs long enough to emit one
unchanged periodic progress heartbeat. Every object is one physical line in
the JSONL file.

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

### Consumer Rules

- Parse enough of the envelope to read `protocol_version` before interpreting
  any event payload. A v1 consumer must reject or quarantine an invocation
  with a missing, malformed, mixed, or unsupported protocol major version.
- Group events by `invocation_id` and process them by `sequence_number`.
- Detect missing sequence numbers as gaps and de-duplicate replay by
  (`invocation_id`, `sequence_number`).
- Within a supported protocol version, ignore unknown optional envelope fields,
  unknown event types, unsupported `event_version` values, and unknown payload
  fields. Even when the payload is ignored, still process known envelope fields
  including `sequence_number` and `last_event`.
- For a supported `run_progress` version, preserve its counters but render an
  unknown `phase` token as an unknown phase; `phase` is intentionally open.
- Ignore unknown attributes when interpreting an event. A transparent
  intermediary that re-emits the same (`invocation_id`, `sequence_number`)
  identity must preserve the complete semantic record, including unknown
  envelope fields, payload fields, and attributes; a transformed record must
  not reuse the original identity.
- Do not assume declaration order for concurrently executing tasks.
- Treat the one record with `last_event: true` as proof that the producer
  completed the logical stream. It must have the greatest sequence number and
  no record may follow it. V1 places the marker on `run_finished`, but a generic
  consumer must not permanently couple the marker to that event type.
- Parse only newline-terminated records. Buffer or ignore an unterminated
  trailing record until more bytes arrive.
- Treat a complete malformed record in the middle of a file as stream
  corruption rather than skipping it and silently closing a sequence gap.

`run_progress` is an operational snapshot, not a result ledger. Consumers may
use it to render progress and detect a stalled producer, but the final report
remains authoritative for detailed evaluation results. If a report and the
event log disagree, consumers should surface the inconsistency and use the
report for result data.

Consumers must keep four outcomes distinct: `run_finished` closes the run
lifecycle, `last_event` closes the logical event stream, the final report owns
detailed evaluation results, and the process exit code also reflects case-result
policy and requested sink delivery. V1 commonly produces them together, but
they are not aliases.

### Sink and Transport Model

Event production targets an extensible sink boundary instead of adding more
positional parameters to `ProgressObserver`:

```go
type EventSink interface {
    Publish(context.Context, Event) error
}
```

The concrete publisher API provides `Publish`, `PublishLast`, `Err`, and
`Close`. Both publish methods accept a typed `Payload` and construct one fully
enveloped immutable event. `PublishLast` sets `last_event: true` and permanently
closes publication after that record, which gives one component ownership of
the single final marker. `Err` returns the sticky publication/close error
without clearing it, and `Close` is idempotent. Lifecycle call sites may log a
returned current-event error but must use `Err` during command finalization.

The publisher validates an invocation's immutable attributes once and copies
them onto every event. Event type and event version come from the typed payload;
callers cannot pair an arbitrary type string with an unrelated payload.

An invocation-scoped publisher or dispatcher owns event identity and ordering.
It serializes concurrent publication, assigns `invocation_id`, the next
contiguous `sequence_number`, and `time` exactly once. Sinks only transport or
serialize events and never assign or mutate their identity.

V1 standardizes one synchronous local JSONL file sink. The `EventSink` boundary
keeps serialization separate from lifecycle state, but v1 makes no delivery,
retry, fan-out, callback, socket, or OTLP promise. Any remote or multi-sink
transport requires a separate proposal. Existing stdout formatting stays
unchanged and is not part of the event-log compatibility contract. The Context
may prevent publication before a write begins, but it does not make an in-flight
synchronous file write interruptible.

### JSONL File Semantics

The JSONL file sink must:

- create or truncate the requested path once at startup;
- encode UTF-8 JSON Lines, marshal the fully enveloped event, append `\n`, and
  write the complete byte slice under the sink's write lock;
- handle short writes without interleaving records and make completed records
  promptly visible;
- avoid `fsync` on every event and avoid a delayed buffered flush;
- reject a serialized event larger than 1 MiB and record the error as a sticky
  sink failure; and
- guarantee only that each successfully written newline-terminated record is
  valid JSON.

A concurrently reading process may observe a partial final write, especially
after a crash or disk failure. Consumers therefore must not parse an
unterminated trailing record as a complete event.

### Command-Line Contract

Only `skill-up run` gains the flag:

```text
--event-log <path>             Write v1 evaluation events as JSON Lines to path
--event-attribute <key=value>  Attach a namespaced string attribute (repeatable)
```

The v1 command contract is:

- both flags are optional and have no config-file or
  environment-variable equivalent;
- an empty value and `-` are rejected; stdout remains human-readable and is
  never an event transport;
- a relative path is resolved against the process working directory;
- the parent directory must already exist; the command creates the file with
  mode `0600` subject to the process umask, or truncates an existing file once
  without changing that file's existing permissions;
- no filename extension is required and one file contains one invocation;
- the canonicalized path must not resolve inside any scheduled
  `iteration-N` directory, because the runner deletes and recreates those
  directories before evaluation;
- `--event-log` and `--dry-run` are mutually exclusive in v1 because dry-run
  executes no lifecycle tasks; and
- `--event-attribute` requires `--event-log`; each value is split on its first
  `=` and must contain that delimiter, keys and values must be non-empty,
  duplicate keys and the reserved `skill-up.*` prefix are rejected, and the
  namespacing and size rules in the envelope section apply; and
- when the flag is absent, event publishing is a no-op and existing stdout,
  reports, and exit behavior remain unchanged.

Planning plus path and attribute validation occur before the file is opened,
so an invalid plan cannot truncate an existing event log. Successful open and
publication of `run_started` complete event-log startup. An open failure stops
without an event stream. If the first record cannot be written, the command
closes the publisher and stops before credentials are loaded or an agent is
invoked; it does not pretend that the failed file contains a terminal stream.

### Command Execution Flow

`runEval` should be refactored around one explicit finalization path:

1. Parse flags and attributes, load and validate configuration, and apply
   filters and overrides.
2. Reject an incompatible dry-run/event-log combination, then build the
   immutable task plan.
3. Validate and open the requested JSONL sink, create the publisher and
   lifecycle emitter, and publish `run_started` plus the initial `preparing`
   progress snapshot. A first-record failure uses the startup behavior above.
4. Load credentials and construct the agent. Any graceful failure from this
   point starts best-effort finalization and attempts to produce `run_finished`
   with `ERROR`.
5. Enter `executing`, execute the planned iterations, and emit progress after
   phase and case changes plus every 30 seconds while work is active.
6. Stop the heartbeat, enter `finalizing`, clear tasks that are no longer
   active because of invocation cancellation/error without counting them as
   completed, and choose `COMPLETED` when every planned task reached a case
   terminal status,
   `CANCELLED` for `context.Canceled` or `context.DeadlineExceeded`, and
   `ERROR` for other invocation-level failures.
7. Publish `run_finished` through `PublishLast`, close the file, and combine
   evaluation, case-result, publisher, and close errors with `errors.Join`.

Steps 6 and 7 must not reuse an already cancelled invocation context. They use
a fresh context so graceful cancellation can make a best-effort attempt to
record the final snapshot and `run_finished`. V1 does not guarantee a
wall-clock finalization bound: cancellation cannot interrupt a synchronous
file write already in progress. CI must retain its outer job or process
timeout. An interruptible or asynchronous sink requires a separate transport
proposal.

Case failures and case errors do not change `COMPLETED` when every planned
task ran; the existing `exitStatusError` still makes a relevant `with_skill`
result fail the command. A sink error likewise does not rewrite lifecycle
status, but its sticky error participates in the final non-zero command
result. If the process is killed or panics before graceful finalization, the
stream may end without `run_finished` as already defined by the consumer
contract.

### Failure Semantics

- Failure to create or open an explicitly requested event log fails startup.
- The first JSONL write failure is sticky and disables that sink, preventing a
  partial or gapped file from later acquiring a misleading final marker. V1
  does not retry or claim that later records reached the failed sink.
- A synchronous file write that stalls without returning may prevent
  finalization and leave the stream without `run_finished` or `last_event`. V1
  does not replace the surrounding CI job or process timeout.
- Evaluation may continue after a mid-run sink failure so reports can still be
  produced. All sticky sink errors are aggregated into the final command error
  and force a non-zero exit status after finalization.
- A write failure leaves an incomplete logical stream. A failure reported only
  by `Close` may occur after a consumer has already observed a logically
  complete stream and cannot retract its final marker. In either case the CLI
  returns non-zero; CI should require both a logically complete file and a
  successful command result before treating event delivery as successful.
- `last_event: true` means the producer logically finalized the stream; it does
  not assert `fsync` durability or that the overall command exits zero.
- `COMPLETED` is not an exit-code alias. A completed run can still exit
  non-zero because a `with_skill` case failed or errored, or because a requested
  sink failed. `ERROR`, `CANCELLED`, and any sticky sink failure require a
  non-zero exit.

### Notes/Constraints/Caveats

- `protocol_version` versions the envelope and ordered-log rules;
  `event_version` versions one event type's payload.
- Event time is diagnostic; ordering is defined by `sequence_number`.
- A progress consumer should measure staleness from the time it observes a
  complete record, not from producer wall-clock `time`.
- Future event families may include summaries, diagnostics, and artifact
  references. Such events may follow `run_finished` only when `run_finished`
  was deliberately not marked last and exactly one later record owns
  `last_event`.
- V1 does not announce future child events, require a DAG, model task-level
  phases, or estimate remaining time.

### Risks and Mitigations

- **Premature schema lock-in.** Version the envelope and each event payload
  separately, keep both small, and require consumers to ignore additive fields.
- **Ambiguous progress.** Count expanded tasks rather than source cases.
- **False liveness signals.** Emit a bounded-rate periodic snapshot and require
  consumers to use local observation time for stall detection.
- **Duplicate, missing, or truncated delivery.** Provide invocation identity,
  sequence numbers, terminal semantics, and trailing-record rules.
- **Sensitive or excessive context.** Keep prompts, responses, transcripts,
  credentials, and artifact contents out of the base schema; attributes are
  string-only and size-bounded.

## Design Details

This documentation PR fixes the v1 envelope, lifecycle registry, consumer
rules, and local-file sink contract. Follow-up implementation work will decide
package placement and concrete types while preserving these contracts. It
should also publish a machine-readable JSON Schema for every supported
(`protocol_version`, `event`, `event_version`) tuple.

The Go shapes and file locations below are a non-normative implementation
sketch. The wire fields, lifecycle invariants, CLI behavior, and observable
failure semantics are normative; an implementation may organize the code
differently while preserving them.

The schema bundle must be layered: a common envelope schema permits unknown
optional envelope fields, event types, and event versions, while a known
tuple's schema validates its known payload fields and permits unknown optional
payload fields. A closed top-level `oneOf` that rejects an otherwise valid
unknown event would contradict the forward-compatibility contract.

The first implementation is expected to add `--event-log`, repeated
`--event-attribute`, a concurrency-safe JSONL file sink, `run_progress`
snapshots and heartbeats, and lifecycle publication at run, iteration, and
case boundaries. It must not expand `ProgressObserver` with additional
positional parameters.

The intended implementation boundaries are:

| Location | Responsibility |
| --- | --- |
| `internal/evalevent/event.go` | Envelope, event types, enums, and typed payloads |
| `internal/evalevent/publisher.go` | Protocol/event versioning, attribute copying, sequence assignment, `PublishLast`, sticky errors, and close aggregation |
| `internal/evalevent/lifecycle.go` | Lifecycle state machine, progress heartbeat, task de-duplication, cumulative and per-iteration counts |
| `internal/evalevent/jsonl.go` | Unbuffered newline-terminated file sink |
| `internal/evaluator/plan.go` | Deterministic expanded task metadata shared by execution and events |
| `internal/runner/runner.go` | Invocation and iteration boundaries; execute the immutable task plan |
| `internal/evaluator/evaluator.go` | Case boundaries and result-to-event status mapping |
| `internal/cli/run.go` | Event-log/attribute flag validation, sink lifecycle, terminal status selection, and final error composition |

`internal/evalevent` depends only on the standard library and UUID generation;
it must not import CLI, runner, evaluator, judge, report, or UI packages.
Runner/evaluator adapters map their domain types into event enums at the
boundary. This keeps the wire layer acyclic and prevents event serialization
from becoming coupled to terminal presentation.

## Test Plan

For this documentation-only PR:

- validate every example line with a JSON parser;
- verify contiguous sequence numbers, protocol/event versions, typed field
  constraints, task totals, progress invariants, heartbeat examples, terminal
  invariants, and the single `last_event` marker;
- verify that the English and Chinese event tables match.

Future implementations must add serialization, unsupported protocol rejection,
unknown envelope/event/version skipping, attribute validation and transparent
relay preservation, concurrent publisher ordering, sticky sink failure,
malformed middle records, partial trailing records, UTF-8 and record-size
limits, benchmark, multi-iteration, fake-clock heartbeat, and end-to-end
consumer tests. Planner and lifecycle-emitter tests must verify that opaque task
identity remains decoupled from source Case IDs containing path separators,
percent signs, and non-ASCII text. Command tests must additionally cover flag
help, repeated attributes, empty and `-` paths, dry-run incompatibility,
relative-path resolution, create mode `0600`, existing-file truncation, missing
parent directories, iteration-workspace collision, startup write failure,
terminal close failure, and absence of behavioral changes when the flag is
omitted. Lifecycle tests must cover credential failure after `run_started`,
case failure with `COMPLETED`, cancellation with no remaining active tasks,
terminal publication after invocation-context cancellation, partially
completed `ERROR`, progress under concurrent cases, and exactly one generic
`last_event` marker.

## Drawbacks

- A documented event contract creates a compatibility commitment before code
  exists.
- The common envelope is more verbose than case-only counters.
- Per-event versions and repeated progress snapshots add a small amount of
  wire and implementation complexity.
- JSONL is easy to inspect but less compact than a binary protocol.
- A stalled synchronous file write can block finalization; v1 relies on the
  surrounding job or process timeout for that failure mode.
- A sticky sink error can make the command fail even when evaluation reports
  were produced successfully.

## Alternatives

- **Keep `--progress-file`.** Clear for the first use case, but too narrow for
  summary, diagnostic, and artifact events.
- **Parse stdout.** No new contract, but terminal text is not a stable API.
- **Use final reports.** Stable for completed results, but not incremental.
- **Use OTLP only.** Useful for observability platforms, but too heavy for
  simple CI consumers and not a product event contract.
- **Use CloudEvents for every record.** Its standard envelope, extensions, and
  SDK ecosystem are useful for routed events. It does not define this
  protocol's ordered JSONL container, contiguous sequence, gap, or final-marker
  rules, so v1 chooses a smaller direct local contract at the cost of immediate
  CloudEvents SDK/router compatibility. Any future adapter proposal must define
  a complete mapping and preservation rules; current attribute keys are not
  promised to map directly to CloudEvents extension attributes.
- **Copy Bazel BEP and protobuf.** Mature and expressive, but unnecessarily
  complex for skill-up v1.
- **Add a separate heartbeat event.** It makes liveness explicit, but repeating
  `run_progress` is simpler and gives a late reader a complete current snapshot.
- **Put progress in envelope attributes.** Not every event needs progress, and
  opaque strings would lose numeric validation and snapshot invariants; a typed
  `run_progress` payload keeps the common envelope general.
- **Add `stream_finished`.** A second terminal event would separate stream and
  run lifecycle, but a generic `last_event` marker provides the needed
  extensibility with one less event type.

## Infrastructure Needed

None for this proposal. It is a documentation-only protocol definition.

## Upgrade & Migration Strategy

`protocol_version` changes only when the envelope or ordered-log semantics
change incompatibly. Adding an optional envelope field is compatible within a
version; removing a field or changing an existing field's meaning, type, or
required status increments the protocol version. A consumer must validate the
version before interpreting any payload. A missing, mixed, or unsupported
protocol version rejects or quarantines the invocation; it is not treated as
an unknown v1 event.

`event_version` evolves independently for each event type. Adding optional
payload fields is compatible within a version. Renaming, removing, changing a
field's meaning/type, or making an optional field required increments only that
event type's version. A consumer that does not support the event/version pair
ignores its payload while still checking sequence and `last_event`.

Core event names are reserved by skill-up. Third-party event types and
attributes remain namespaced, so extensions do not require a protocol-version
bump. `run_progress.phase` is an open token: adding a phase does not increment
the event version and older consumers render it as unknown. The
`configuration`, case `status`, and run `status` sets are closed; adding a wire
value to one of them increments the affected event version. Unknown enum
values must never be silently mapped to an existing meaning.

The first implementation will be tracked separately in
[GitHub issue #206](https://github.com/alibaba/skill-up/issues/206).
