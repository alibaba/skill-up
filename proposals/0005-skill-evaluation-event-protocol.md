---
title: "Skill Evaluation Event Log Protocol"
authors:
  - "JHWang-1997"
creation-date: 2026-08-18
last-updated: 2026-08-19
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
events through file, callback, in-process, or remote subscribers. Existing
stdout remains a separate human-readable interface.

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
- Model benchmark execution as tasks, where one source case may expand into
  multiple configurations.
- Keep event production independent of concrete sinks and transports.
- Start with the minimum run, iteration, and case lifecycle needed by CI.

### Non-Goals

- Implementing `--event-log` or changing runtime behavior in this PR.
- Defining a remote event service, authentication, retry, or retention policy.
- Defining summary, diagnostic, artifact, turn, retry, judge, or tool events
  in v1; the protocol only reserves room for them.
- Streaming prompts, model responses, transcripts, credentials, or artifact
  contents.
- Reproducing the full Bazel BEP graph or protobuf schema.

## Requirements

- Each complete record is one valid JSON object terminated by `\n`.
- Every event has a numeric schema version, contiguous sequence number, stable
  invocation ID, timestamp, event type, and typed payload.
- An invocation that reaches graceful finalization emits exactly one terminal
  `run_finished` event, including completed runs with failed cases and runs
  that end with an invocation-level error or cancellation.
- Consumers remain compatible with unknown event types and payload fields
  within a supported schema major version.
- The base event schema contains no sensitive evaluation content.
- Event emission is safe when evaluation tasks execute concurrently.

## Proposal

### A General Evaluation Event Log

The first implementation should expose:

```bash
skill-up run --event-log ./events.jsonl
```

The name deliberately describes the durable abstraction rather than its first
consumer. CI can derive progress from case events, while future integrations
can subscribe to other event families without introducing another output
flag or changing the core publisher boundary.

### Event Envelope

Every event uses this envelope:

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

| Field | JSON type | Required | Constraints |
| --- | --- | --- | --- |
| `schema_version` | integer | yes | Exactly `1` for this specification |
| `sequence_number` | integer | yes | Contiguous invocation-wide order in `[1, 9007199254740991]` |
| `invocation_id` | string | yes | Stable UUID shared by all events from one `skill-up run` invocation |
| `time` | string | yes | RFC 3339 timestamp in UTC, using the `Z` offset |
| `event` | string | yes | Non-empty event type from this registry or a compatible v1 extension |
| `last_event` | boolean | no | When present, exactly `true` and only on `run_finished`; never `null` |
| `payload` | object | yes | Non-null object with fields defined by the event type |

The pair (`invocation_id`, `sequence_number`) is the event's stable identity
for ordering and de-duplication. V1 intentionally has no event ID, parent ID,
or graph relationship.

### Initial Event Types

| Event | Required payload fields |
| --- | --- |
| `run_started` | `engine`, `skill_name`, `task_total`, `iterations_total` |
| `iteration_started` | `iteration` |
| `case_started` | `task_id`, `iteration`, `case_id`, `configuration`, `task_index`, `task_total`, `title` |
| `case_completed` | `task_id`, `iteration`, `case_id`, `configuration`, `task_index`, `task_total`, `completed_tasks`, `status`, `duration_ms`; optional `pass_rate` |
| `iteration_completed` | `iteration`, `completed_tasks`, `passed`, `failed`, `errored`, `skipped`, `duration_ms` |
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
| `run_started`, case events | `task_total` | integer | Final invocation-wide total in `[1, 9007199254740991]` |
| `run_started` | `iterations_total` | integer | Number of iterations planned by this invocation, at least `1` |
| iteration and case events | `iteration` | integer | Actual iteration number, at least `1`; it may start above `1` when appending to an existing workspace |
| case events | `task_id` | string | Non-empty, stable, and unique within the invocation |
| case events | `case_id` | string | Non-empty source case identifier |
| case events | `configuration` | string | Exactly `with_skill` or `without_skill` |
| case events | `task_index` | integer | Stable invocation-wide planned index in `[1, task_total]` |
| case events | `title` | string | Human-readable case title |
| completion events | `completed_tasks` | integer | Invocation-wide cumulative terminal task count in `[0, task_total]` |
| `case_completed` | `status` | string | Exactly `PASS`, `FAIL`, `ERROR`, or `SKIP` |
| `case_completed` | `pass_rate` | number | Optional finite value in `[0, 1]`; omitted when unavailable |
| completion events | `duration_ms` | integer | Non-negative elapsed duration in milliseconds |
| iteration and run completion events | `passed`, `failed`, `errored`, `skipped` | integer | Non-negative task counts; iteration-scoped on `iteration_completed` and invocation-scoped on `run_finished` |
| `run_finished` | `status` | string | Exactly `COMPLETED`, `ERROR`, or `CANCELLED` |

Each `duration_ms` is monotonic wall-clock elapsed time between its matching
started and completed/finished lifecycle boundaries. Case duration covers all
work after `case_started`, including agent execution and judging; it is not the
existing agent-only `EvalResult.DurationMs`. Iteration and run durations are
not sums of child durations because tasks may execute concurrently.

`COMPLETED` is a lifecycle state: all planned tasks reached a terminal case
status. It does not mean every case passed and does not by itself imply a zero
command exit code. `ERROR` means an invocation-level failure prevented normal
completion; `CANCELLED` means the invocation stopped after cancellation.
Result counts remain authoritative for case outcomes.

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

`PassRate` is a pointer because zero is valid while unavailable values must be
omitted. Constructors and the publisher validate safe-integer ranges and enum
values before publication. `Payload.eventType` lets the publisher derive
`Event.Type`, preventing a payload/type mismatch. The publisher alone sets the
envelope fields and `LastEvent`; callers cannot override them. It generates
one UUID for the invocation and converts timestamps to UTC so `time.Time`
encodes with the required `Z` offset.

### Task Progress Model

A task is one (`iteration`, `case_id`, `configuration`) tuple. Benchmark mode
therefore creates two tasks from one source case. `task_id` is stable and
unique within the invocation; a readable form is:

```text
iteration-1/case-1/with_skill
```

Planning completes before `run_started`. The event is emitted exactly once
before any iteration or case event, and its `task_total` is final after case
filtering, benchmark configuration expansion, and iteration expansion.

Each planned task receives one stable, invocation-wide, 1-based `task_index`;
the index is unrelated to concurrent start or completion order.
`completed_tasks` is invocation-wide, starts at `0`, and increases exactly
once when a task emits its terminal `case_completed` event. An implementation
therefore cannot directly forward `ProgressObserver` indices that reset for
each iteration.

Status counts on `iteration_completed` cover that iteration; counts on
`run_finished` cover the whole invocation. Every `run_finished` satisfies:

```text
completed_tasks == passed + failed + errored + skipped
```

When `run_finished.status` is `COMPLETED`, it additionally satisfies:

```text
completed_tasks == task_total
```

An `ERROR` or `CANCELLED` invocation may finish gracefully with
`completed_tasks < task_total`. These rules let CI display unambiguous progress
even with benchmark mode, multiple iterations, and parallel execution.

### Task Planning and Lifecycle Emitter

The implementation must build one immutable invocation-wide task plan before
opening the event stream. The plan resolves:

- the final output directory, start iteration, and iteration count;
- selected cases after include/exclude filtering;
- benchmark expansion (`with_skill`, followed by `without_skill` when
  enabled); and
- one global `task_index`, `task_id`, and `task_total` for every expanded task.

Plan order is deterministic: iteration number ascending, filtered case order,
then `with_skill` before `without_skill`. The canonical v1 task ID is:

```text
iteration-<iteration>/<case_id>/<configuration>
```

Case IDs are already validated not to contain path separators. Task start and
completion events may appear in any order under concurrency, but their planned
identity never changes. The runner must execute this plan rather than
recompute a second task list; event totals and actual work therefore cannot
drift apart.

An invocation-scoped lifecycle emitter sits above the generic publisher. It
owns the plan, a mutex, started/completed task sets, invocation counts, and
per-iteration counts. Its proposed operations are:

```go
Start(ctx, engine, skillName)
IterationStarted(ctx, iteration)
CaseStarted(ctx, plannedTask)
CaseCompleted(ctx, plannedTask, status, passRate, duration)
IterationCompleted(ctx, iteration, duration)
Finish(ctx, runStatus, duration)
```

The emitter serializes each lifecycle state transition together with event
publication. This is required because incrementing `completed_tasks` outside
the publication order could produce a later sequence number with a smaller
counter. It also rejects duplicate starts or completions, unknown tasks,
events before `run_started`, and events after `run_finished`.

Every scheduled iteration emits one `iteration_started` before any of its case
events. It emits `iteration_completed` only after all tasks planned for that
iteration reach a terminal case status; an interrupted iteration may omit it.
Every task that starts emits exactly one `case_started` followed by exactly one
`case_completed`, including tasks mapped to `ERROR` or `SKIP`. Tasks never
started because of invocation error or cancellation emit neither case event.

`CaseCompleted` increments invocation and iteration counts exactly once.
`IterationCompleted` and `Finish` derive their payload counts from emitter
state rather than caller-supplied counters. `Finish(COMPLETED)` validates that
all planned tasks completed; `ERROR` and `CANCELLED` allow unfinished tasks.
The existing `ProgressObserver` remains unchanged and only drives terminal UI;
it is not a source of protocol identity or aggregate progress.

### Example Stream

This compact example evaluates one source case with benchmark mode enabled,
so it produces two tasks. Every object is one physical line in the JSONL file.

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

### Consumer Rules

- Parse enough of the envelope to read `schema_version` before interpreting
  any other field. A v1 consumer must reject or quarantine an invocation with
  a missing, malformed, mixed, or unsupported major version.
- Group events by `invocation_id` and process them by `sequence_number`.
- Detect missing sequence numbers as gaps and de-duplicate replay by
  (`invocation_id`, `sequence_number`).
- Within a supported major version, ignore unknown event types and unknown
  payload fields.
- Do not assume declaration order for concurrently executing tasks.
- Treat `run_finished` with `last_event: true` as the only proof of normal
  stream termination. A process crash may leave a valid prefix without it.
- Parse only newline-terminated records. Buffer or ignore an unterminated
  trailing record until more bytes arrive.

### Sink and Transport Model

Event production targets an extensible sink boundary instead of adding more
positional parameters to `ProgressObserver`:

```go
type EventSink interface {
    Publish(context.Context, Event) error
}
```

The concrete publisher API is expected to provide `Publish`, `Err`, and
`Close`. `Publish` accepts a typed `Payload`, constructs one fully enveloped
event, fans it out, and records per-sink sticky errors. `Err` returns their
`errors.Join` aggregate without clearing it. `Close` closes sinks that also
implement `io.Closer`, records close failures in the same aggregate, and is
idempotent.
Lifecycle call sites may log a returned current-event error but must use
`Err` during command finalization. Payloads are stored as values containing
only scalar fields, and every sink receives a copy of the same immutable
event.

The CLI can fan out one event to a JSONL file sink, UI adapter, callback, OTLP
adapter, socket, or future remote publisher. Existing stdout formatting stays
unchanged and is not part of the event-log compatibility contract.

An invocation-scoped publisher or dispatcher owns event identity and ordering.
It serializes concurrent publication, assigns `invocation_id`, the next
contiguous `sequence_number`, and `time` exactly once, and then passes the same
immutable, fully enveloped `Event` to every sink. Fan-out remains ordered by
`sequence_number`; sinks only transport or serialize events and never assign
or mutate their identity.

JSONL is the v1 serialization. Files are the first proposed sink, not the
protocol itself.

### JSONL File Semantics

The future JSONL file sink must:

- create or truncate the requested path once at startup;
- marshal the fully enveloped event, append `\n`, and write the complete
  record under the sink's write lock;
- perform one `Write` per record and make completed records promptly visible;
- avoid `fsync` on every event and avoid a delayed buffered flush;
- guarantee only that each newline-terminated record is valid JSON.

A concurrently reading process may observe a partial final write, especially
after a crash or disk failure. Consumers therefore must not parse an
unterminated trailing record as a complete event.

### Command-Line Contract

Only `skill-up run` gains the flag:

```text
--event-log <path>   Write v1 evaluation events as JSON Lines to path
```

The v1 command contract is:

- the flag is optional, takes one path value, and has no config-file or
  environment-variable equivalent;
- an empty value and `-` are rejected; stdout remains human-readable and is
  never an event transport;
- a relative path is resolved against the process working directory;
- the parent directory must already exist; the command creates the file with
  mode `0644` subject to the process umask, or truncates an existing file once;
- no filename extension is required and one file contains one invocation;
- the canonicalized path must not resolve inside any scheduled
  `iteration-N` directory, because the runner deletes and recreates those
  directories before evaluation;
- `--event-log` and `--dry-run` are mutually exclusive in v1 because dry-run
  executes no lifecycle tasks; and
- when the flag is absent, event publishing is a no-op and existing stdout,
  reports, and exit behavior remain unchanged.

Planning and path validation occur before the file is opened, so an invalid
plan cannot truncate an existing event log. Successful open and publication
of `run_started` complete event-log startup. An open failure stops without an
event stream. If `run_started` fan-out fails, the command attempts
`run_finished` with `ERROR` on every still-healthy sink, closes the publisher,
and stops before credentials are loaded or an agent is invoked.

### Command Execution Flow

`runEval` should be refactored around one explicit finalization path:

1. Parse flags, load and validate configuration, apply filters and overrides.
2. Reject an incompatible dry-run/event-log combination, then build the
   immutable task plan.
3. Validate and open the requested JSONL sink, create the publisher and
   lifecycle emitter, and publish `run_started`. A first-event failure uses
   the startup finalization behavior above.
4. Load credentials and construct the agent. Any graceful failure from this
   point produces `run_finished` with `ERROR`.
5. Execute the planned iterations. The runner emits iteration boundaries and
   the evaluator emits case boundaries using the plan metadata.
6. Choose `COMPLETED` when every planned task reached a case terminal status,
   `CANCELLED` for `context.Canceled` or `context.DeadlineExceeded`, and
   `ERROR` for other invocation-level failures.
7. Publish exactly one `run_finished`, close all sinks, and combine evaluation,
   case-result, publisher, and close errors with `errors.Join`.

Case failures and case errors do not change `COMPLETED` when every planned
task ran; the existing `exitStatusError` still makes a relevant `with_skill`
result fail the command. A sink error likewise does not rewrite lifecycle
status, but its sticky error participates in the final non-zero command
result. If the process is killed or panics before graceful finalization, the
stream may end without `run_finished` as already defined by the consumer
contract.

### Failure Semantics

- Failure to create or open an explicitly requested event log fails startup.
- Fan-out attempts every healthy sink for the current event even if an earlier
  sink fails. A sink failure is sticky and recorded per sink; it does not
  prevent other healthy sinks from receiving the current event, later events,
  or `run_finished`. V1 does not require retrying a failed sink.
- Evaluation may continue after a mid-run sink failure so reports can still be
  produced. All sticky sink errors are aggregated into the final command error
  and force a non-zero exit status after finalization.
- A failed sink must not be reported as a complete stream. In particular,
  warning and exiting successfully would let CI trust incomplete progress.
- `COMPLETED` is not an exit-code alias. A completed run can still exit
  non-zero because a `with_skill` case failed or errored, or because a requested
  sink failed. `ERROR`, `CANCELLED`, and any sticky sink failure require a
  non-zero exit.

### Notes/Constraints/Caveats

- A new optional event type or payload field is additive within v1.
- Event time is diagnostic; ordering is defined by `sequence_number`.
- Future event families may include summaries, diagnostics, and artifact
  references, but not sensitive contents by default.
- V1 does not announce future child events or require a DAG.

### Risks and Mitigations

- **Premature schema lock-in.** Keep the envelope small, use payloads per
  event type, and require consumers to ignore additive fields.
- **Ambiguous progress.** Count expanded tasks rather than source cases.
- **Duplicate, missing, or truncated delivery.** Provide invocation identity,
  sequence numbers, terminal semantics, and trailing-record rules.
- **Sensitive data leakage.** Keep prompts, responses, transcripts,
  credentials, and artifact contents out of the base schema.

## Design Details

This documentation PR fixes the v1 envelope, lifecycle registry, consumer
rules, and sink abstraction. Follow-up implementation work will decide package
placement and concrete types while preserving these contracts.

The first implementation is expected to add `--event-log`, a concurrency-safe
JSONL file sink, an `EventSink` fan-out, and lifecycle publication at run,
iteration, and case boundaries. It must not expand `ProgressObserver` with
additional positional parameters.

The intended implementation boundaries are:

| Location | Responsibility |
| --- | --- |
| `internal/evalevent/event.go` | Envelope, event types, enums, and typed payloads |
| `internal/evalevent/publisher.go` | Sequence assignment, ordered fan-out, sticky per-sink errors, and close aggregation |
| `internal/evalevent/lifecycle.go` | Lifecycle state machine, task de-duplication, cumulative and per-iteration counts |
| `internal/evalevent/jsonl.go` | Unbuffered newline-terminated file sink |
| `internal/evaluator/plan.go` | Deterministic expanded task metadata shared by execution and events |
| `internal/runner/runner.go` | Invocation and iteration boundaries; execute the immutable task plan |
| `internal/evaluator/evaluator.go` | Case boundaries and result-to-event status mapping |
| `internal/cli/run.go` | Flag validation, sink lifecycle, terminal status selection, and final error composition |

`internal/evalevent` depends only on the standard library and UUID generation;
it must not import CLI, runner, evaluator, judge, report, or UI packages.
Runner/evaluator adapters map their domain types into event enums at the
boundary. This keeps the wire layer acyclic and prevents event serialization
from becoming coupled to terminal presentation.

## Test Plan

For this documentation-only PR:

- validate every example line with a JSON parser;
- verify contiguous sequence numbers, typed field constraints, task totals,
  cumulative progress, terminal invariants, and the single terminal event;
- verify that the English and Chinese event tables match.

Future implementations must add serialization, unsupported-major rejection,
concurrent publisher ordering, identical event identity across fan-out,
per-sink sticky failure, partial trailing record, benchmark, multi-iteration,
and end-to-end consumer tests. Command tests must additionally cover flag help,
empty and `-` paths, dry-run incompatibility, relative-path resolution,
existing-file truncation, missing parent directories, iteration-workspace
collision, startup write failure, terminal close failure, and absence of
behavioral changes when the flag is omitted. Lifecycle tests must cover
credential failure after `run_started`, case failure with `COMPLETED`,
cancellation, partially completed `ERROR`, and exactly one terminal event.

## Drawbacks

- A documented event contract creates a compatibility commitment before code
  exists.
- The common envelope is more verbose than case-only counters.
- JSONL is easy to inspect but less compact than a binary protocol.
- A sticky sink error can make the command fail even when evaluation reports
  were produced successfully.

## Alternatives

- **Keep `--progress-file`.** Clear for the first use case, but too narrow for
  summary, diagnostic, and artifact events.
- **Parse stdout.** No new contract, but terminal text is not a stable API.
- **Use final reports.** Stable for completed results, but not incremental.
- **Use OTLP only.** Useful for observability platforms, but too heavy for
  simple CI consumers and not a product event contract.
- **Copy Bazel BEP and protobuf.** Mature and expressive, but unnecessarily
  complex for skill-up v1.

## Infrastructure Needed

None for this proposal. It is a documentation-only protocol definition.

## Upgrade & Migration Strategy

Schema version `1` is additive: producers may add event types or optional
payload fields, and consumers must ignore what they do not understand.
Renaming, removing, or changing the meaning or type of an existing field
requires a new major `schema_version`.

A consumer must validate the major version before interpreting the remaining
envelope or payload. Unsupported major versions are rejected or quarantined;
they are not treated as compatible unknown v1 events.

The first implementation will be tracked separately in
[GitHub issue #206](https://github.com/alibaba/skill-up/issues/206).
