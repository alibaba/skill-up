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
  - [Task Progress Model](#task-progress-model)
  - [Example Stream](#example-stream)
  - [Consumer Rules](#consumer-rules)
  - [Sink and Transport Model](#sink-and-transport-model)
  - [JSONL File Semantics](#jsonl-file-semantics)
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
- Every event has a numeric schema version, monotonically increasing sequence
  number, stable invocation ID, timestamp, event type, and typed payload.
- An invocation that reaches normal finalization emits exactly one terminal
  `run_finished` event, including invocations with failed cases.
- Consumers remain compatible with unknown event types and payload fields.
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

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | Protocol major version, initially `1` |
| `sequence_number` | integer | Monotonically increasing emission order, starting at `1` within an invocation |
| `invocation_id` | string | Stable UUID shared by all events from one `skill-up run` invocation |
| `time` | string | RFC 3339 timestamp in UTC |
| `event` | string | Event type from the registry below |
| `last_event` | boolean | Optional; present and `true` only on `run_finished` |
| `payload` | object | Fields defined by the event type |

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
| `iteration_completed` | `iteration`, `completed_tasks`, `passed`, `failed`, `errored`, `skipped`, `duration_ms`; optional `report_dir` |
| `run_finished` | `status`, `completed_tasks`, `passed`, `failed`, `errored`, `skipped`, `duration_ms` |

`configuration` is `with_skill` or `without_skill`. Case `status` is `PASS`,
`FAIL`, `ERROR`, or `SKIP`. Run `status` is `SUCCESS`, `ERROR`, or
`CANCELLED`; `SUCCESS` means the invocation reached normal finalization and
does not mean every case passed. Result counts remain authoritative for case
outcomes.

### Task Progress Model

A task is one (`iteration`, `case_id`, `configuration`) tuple. Benchmark mode
therefore creates two tasks from one source case. `task_id` is stable and
unique within the invocation; a readable form is:

```text
iteration-1/case-1/with_skill
```

`task_index` and `task_total` describe planned tasks across the whole
invocation. `completed_tasks` is the cumulative number of terminal case tasks
at the instant an event is emitted. Status counts on `iteration_completed`
cover that iteration; counts on `run_finished` cover the whole invocation.
These fields let CI display unambiguous progress even with benchmark mode,
multiple iterations, and parallel execution.

### Example Stream

This compact example evaluates one source case with benchmark mode enabled,
so it produces two tasks. Every object is one physical line in the JSONL file.

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

### Consumer Rules

- Group events by `invocation_id` and process them by `sequence_number`.
- Detect missing sequence numbers as gaps and de-duplicate replay by
  (`invocation_id`, `sequence_number`).
- Ignore unknown event types and unknown payload fields.
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

The CLI can fan out one event to a JSONL file sink, UI adapter, callback, OTLP
adapter, socket, or future remote publisher. Existing stdout formatting stays
unchanged and is not part of the event-log compatibility contract.

JSONL is the v1 serialization. Files are the first proposed sink, not the
protocol itself.

### JSONL File Semantics

The future JSONL file sink must:

- create or truncate the requested path once at startup;
- assign `sequence_number`, marshal the event, append `\n`, and write the
  complete record under the same lock;
- perform one `Write` per record and make completed records promptly visible;
- avoid `fsync` on every event and avoid a delayed buffered flush;
- guarantee only that each newline-terminated record is valid JSON.

A concurrently reading process may observe a partial final write, especially
after a crash or disk failure. Consumers therefore must not parse an
unterminated trailing record as a complete event.

### Failure Semantics

- Failure to create or open an explicitly requested event log fails startup.
- A mid-run write failure is sticky. Evaluation may continue so reports can
  still be produced, but the command returns a non-zero exit status after
  finalization.
- A failed sink must not be reported as a complete stream. In particular,
  warning and exiting successfully would let CI trust incomplete progress.

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

## Test Plan

For this documentation-only PR:

- validate every example line with a JSON parser;
- verify monotonic sequence numbers, task totals, cumulative progress, and the
  single terminal event;
- verify that the English and Chinese event tables match.

Future implementations must add serialization, concurrent publishing, sticky
write failure, partial trailing record, benchmark, multi-iteration, and
end-to-end consumer tests.

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

The first implementation will be tracked separately in
[GitHub issue #206](https://github.com/alibaba/skill-up/issues/206).
