---
title: "Skill Evaluation Event Protocol"
authors:
  - "JHWang-1997"
creation-date: 2026-08-18
last-updated: 2026-08-19
status: draft
---

# SUP-0005: Skill Evaluation Event Protocol

Language: English | [中文](zh/0005-skill-evaluation-event-protocol.md)

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Requirements](#requirements)
- [Proposal](#proposal)
  - [Event Envelope](#event-envelope)
  - [Initial Event Types](#initial-event-types)
  - [Example Stream](#example-stream)
  - [Consumer Rules](#consumer-rules)
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

This proposal defines a small, versioned event protocol for skill evaluation.
The initial representation is JSON Lines: each line is an independent event
describing the lifecycle of a run, iteration, or case.

The protocol is defined before any producer or transport is implemented. A
future `--progress-file` option can write the events to a local file for CI,
while other publishers and subscribers can reuse the same event contract.

## Motivation

Today, integrations that need live evaluation progress must parse terminal
output or wait for reports written after an iteration. Terminal text is a
human interface, and report files are too late for CI progress updates,
dashboards, or other subscribers.

[Bazel's Build Event Protocol](https://bazel.build/remote/bep) solves a similar
problem by representing an invocation as structured events with identifiers,
relationships, and typed payloads. SUP-0005 adopts those useful properties in
a deliberately smaller JSON-first format suited to skill-up.

### Goals

- Define one machine-readable contract for evaluation lifecycle events.
- Support incremental parsing while an evaluation is running.
- Let consumers correlate, order, and de-duplicate events.
- Keep the protocol independent of files, callbacks, queues, or network
  services.
- Start with the minimum run, iteration, and case lifecycle needed by CI.

### Non-Goals

- Implementing `--progress-file` or changing `ProgressObserver` in this PR.
- Defining a remote event service, authentication, retry, or retention policy.
- Streaming prompts, model responses, transcripts, credentials, or artifacts.
- Defining turn-level, retry-attempt, judge-step, or tool-call events in v1.
- Reproducing the full Bazel BEP graph or protobuf schema.

## Requirements

- Each event is one valid JSON object terminated by `\n`.
- Every event has a schema version, unique event ID, event type, timestamp,
  run ID, and monotonic sequence number within that run.
- Event-specific fields live in a typed `payload` object.
- An optional parent event ID can express lifecycle relationships.
- Consumers must remain compatible with unknown event types and fields.
- The base event schema must not contain sensitive evaluation content.

## Proposal

### Event Envelope

Every event uses this envelope:

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

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | string | Protocol major version, initially `"1"` |
| `event_id` | string | Identifier unique within the run |
| `event` | string | Event type from the registry below |
| `sequence` | integer | Monotonically increasing emission order within the run |
| `time` | string | RFC 3339 timestamp in UTC |
| `run_id` | string | Stable identifier shared by all events in one invocation |
| `parent_event_id` | string | Optional ID of the lifecycle event that contains or started this work |
| `payload` | object | Fields defined by the event type |

`event_id` and `parent_event_id` allow subscribers to build a small event
graph when useful. `sequence` remains the simple cursor for file tailing and
stream processing.

### Initial Event Types

| Event | Required payload fields |
| --- | --- |
| `run_started` | `engine`, `skill_name`, `cases_total`, `iterations_total` |
| `iteration_started` | `iteration` |
| `case_started` | `iteration`, `case_index`, `case_total`, `case_id`, `configuration`, `title` |
| `case_completed` | `iteration`, `case_index`, `case_total`, `case_id`, `configuration`, `status`, `duration_ms`; optional `pass_rate` |
| `iteration_completed` | `iteration`, `passed`, `failed`, `errored`, `skipped`; optional `report_dir` |
| `run_completed` | `iterations_completed`, `status` |

`configuration` is `with_skill` or `without_skill`. Case `status` is `PASS`,
`FAIL`, `ERROR`, or `SKIP`. Run `status` is `SUCCEEDED`, `FAILED`, or
`CANCELLED`; it describes whether the invocation completed, not whether every
case passed.

### Example Stream

The example is formatted for readability; the actual JSONL stream contains
one compact object per line.

```jsonl
{"schema_version":"1","event_id":"evt-01","event":"run_started","sequence":1,"time":"2026-08-19T02:00:00.000Z","run_id":"run-7f3a","payload":{"engine":"qodercli","skill_name":"my-skill","cases_total":1,"iterations_total":1}}
{"schema_version":"1","event_id":"evt-02","event":"iteration_started","sequence":2,"time":"2026-08-19T02:00:00.001Z","run_id":"run-7f3a","parent_event_id":"evt-01","payload":{"iteration":1}}
{"schema_version":"1","event_id":"evt-03","event":"case_started","sequence":3,"time":"2026-08-19T02:00:00.002Z","run_id":"run-7f3a","parent_event_id":"evt-02","payload":{"iteration":1,"case_index":1,"case_total":1,"case_id":"case-1","configuration":"with_skill","title":"Basic flow"}}
{"schema_version":"1","event_id":"evt-04","event":"case_completed","sequence":4,"time":"2026-08-19T02:01:10.451Z","run_id":"run-7f3a","parent_event_id":"evt-03","payload":{"iteration":1,"case_index":1,"case_total":1,"case_id":"case-1","configuration":"with_skill","status":"PASS","pass_rate":1.0,"duration_ms":70449}}
{"schema_version":"1","event_id":"evt-05","event":"iteration_completed","sequence":5,"time":"2026-08-19T02:01:10.500Z","run_id":"run-7f3a","parent_event_id":"evt-02","payload":{"iteration":1,"passed":1,"failed":0,"errored":0,"skipped":0,"report_dir":"my-skill-workspace/iteration-1"}}
{"schema_version":"1","event_id":"evt-06","event":"run_completed","sequence":6,"time":"2026-08-19T02:01:10.501Z","run_id":"run-7f3a","parent_event_id":"evt-01","payload":{"iterations_completed":1,"status":"SUCCEEDED"}}
```

### Consumer Rules

- Process events in ascending `sequence` order within one `run_id`.
- De-duplicate replayed events by (`run_id`, `event_id`).
- Ignore unknown event types and unknown payload fields.
- Do not assume declaration order for concurrently executing cases.
- Treat a stream without its expected completion events as incomplete rather
  than successful. Crashes or interrupted transports may leave such a prefix.
- Use (`case_id`, `configuration`, `iteration`) to identify one case task.

### Notes/Constraints/Caveats

- JSONL is the first serialization, not the transport contract. A local file,
  callback, in-process observer, queue, or event service may carry the same
  envelope later.
- `parent_event_id` expresses useful lifecycle relationships but v1 does not
  require consumers to construct or validate a complete DAG.
- Event time is diagnostic. Ordering is defined by `sequence`, not timestamps.
- The protocol intentionally contains summaries and metadata only.

### Risks and Mitigations

- **Premature schema lock-in.** Keep v1 small, use a payload per type, and
  require consumers to ignore additive fields.
- **Duplicate or replayed delivery.** Provide stable event IDs and explicit
  de-duplication rules.
- **Incomplete streams.** Define completion events and require consumers to
  recognize a valid prefix without assuming success.
- **Sensitive data leakage.** Keep prompts, responses, transcripts, and
  credentials out of the base schema.

## Design Details

This PR defines only the wire contract. Follow-up proposals or implementation
PRs will decide:

- how event IDs, run IDs, and sequence numbers are generated;
- how internal evaluation callbacks produce protocol events;
- whether the first transport is `--progress-file`, an environment variable,
  or another integration point;
- file lifecycle, buffering, flush, and write-failure behavior;
- whether a future event registry includes turn, retry, judge, artifact, or
  diagnostic events.

Those decisions must preserve the v1 envelope and consumer rules unless this
proposal is revised.

## Test Plan

For this documentation-only PR:

- validate every example line with a JSON parser;
- verify that sequence numbers and parent references are consistent;
- verify that the English and Chinese event tables match.

Future implementations must add serialization, concurrency, replay, partial
stream, and end-to-end consumer tests.

## Drawbacks

- A documented event contract creates a compatibility commitment before code
  exists.
- The envelope is more verbose than emitting only case counters.
- JSONL is easy to inspect but less compact than a binary protocol.

## Alternatives

- **Parse stdout.** No new contract, but terminal text is not a stable API.
- **Use final reports.** Stable for completed results, but not incremental.
- **Use OTLP only.** Useful for observability platforms, but too heavy for
  simple CI progress consumers and not a product event contract.
- **Copy Bazel BEP and protobuf.** Mature and expressive, but unnecessarily
  complex for the initial skill-up lifecycle.

## Infrastructure Needed

None for this proposal. It is a documentation-only protocol definition.

## Upgrade & Migration Strategy

Version `"1"` is additive: producers may add event types or optional payload
fields, and consumers must ignore what they do not understand. Renaming,
removing, or changing the meaning or type of an existing field requires a new
major `schema_version`.

The first implementation will be tracked separately. See
[GitHub issue #206](https://github.com/alibaba/skill-up/issues/206).
