# Observability

This package owns process-level OpenTelemetry wiring for `skill-up`.

The current scope is intentionally limited to standard OpenTelemetry signals.
It does not implement product analytics, Datadog-specific fanout, first-party
event logging, or diagnostic JSONL files. Those channels have different
privacy, reliability, and schema contracts and should stay separate.

## Responsibilities

`internal/observability` provides:

- optional OTLP trace initialization from standard `OTEL_*` environment variables
- optional metrics initialization from `OTEL_METRICS_EXPORTER`
- shared tracer access for CLI, runner, evaluator, and runtime spans
- low-cardinality metric helpers for run, case, and runtime exec signals
- resource creation from `OTEL_RESOURCE_ATTRIBUTES` and service metadata

The logging layer remains in `internal/logging`. Logs are still emitted through
the repository's `slog` wrapper; when the log context contains an active span,
the wrapper records a `skill_up.log` span event. The span event stores
severity and the formatted log message by default so traces contain enough
detail for debugging. The CLI prints the run trace ID once when tracing starts;
per-line `slog` output does not repeat `trace_id` or `span_id`.
Set `SKILL_UP_OTEL_LOG_MESSAGE=0` to suppress `log.message` when a low-detail
trace payload is required.

## Environment Variables

### Core OTel Export

| Variable | Effect |
| --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Enables tracing when set and no trace-specific endpoint is required. The OTel exporter SDK uses it as the base OTLP endpoint. |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | Enables tracing and configures the trace-specific OTLP endpoint. |
| `OTEL_TRACES_EXPORTER` | `none` disables trace export. `otlp` enables trace export even when endpoint variables are not set. |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | Default OTLP protocol for traces and metrics. Supported by `skill-up`: `grpc`, `http`, `http/protobuf`. |
| `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL` | Trace-specific OTLP protocol. Overrides `OTEL_EXPORTER_OTLP_PROTOCOL` for traces. |
| `OTEL_EXPORTER_OTLP_HEADERS` | Default OTLP headers read by the OTel exporter SDK. Passed through to child agents. |
| `OTEL_EXPORTER_OTLP_TRACES_HEADERS` | Trace-specific OTLP headers read by the OTel exporter SDK. Passed through to child agents. |
| `OTEL_EXPORTER_OTLP_COMPRESSION` | Default OTLP compression read by the OTel exporter SDK. Passed through to child agents. |
| `OTEL_EXPORTER_OTLP_TRACES_COMPRESSION` | Trace-specific OTLP compression read by the OTel exporter SDK. Passed through to child agents. |
| `OTEL_EXPORTER_OTLP_TIMEOUT` | Default OTLP timeout read by the OTel exporter SDK. Passed through to child agents. |
| `OTEL_EXPORTER_OTLP_TRACES_TIMEOUT` | Trace-specific OTLP timeout read by the OTel exporter SDK. Passed through to child agents. |

### Metrics

| Variable | Effect |
| --- | --- |
| `OTEL_METRICS_EXPORTER` | Enables metrics. Supported values: `otlp`, `console`, `none` or empty. |
| `OTEL_EXPORTER_OTLP_METRICS_PROTOCOL` | Metrics-specific OTLP protocol. Overrides `OTEL_EXPORTER_OTLP_PROTOCOL` for metrics. |

When `OTEL_METRICS_EXPORTER=otlp`, the OTel metric exporter also reads the
standard OTLP metric endpoint/header/compression/timeout variables supported by
the SDK, such as `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`.

### Resource And Routing Attributes

| Variable | Effect |
| --- | --- |
| `OTEL_SERVICE_NAME` | Overrides the default service name `skill-up`. |
| `OTEL_RESOURCE_ATTRIBUTES` | Standard OTel resource attributes attached to traces and metrics. |
| `SKILL_UP_OTEL_RESOURCE_ATTRIBUTES` | Appends resource attributes only to the `skill-up` process. |
| `SKILL_UP_AGENT_OTEL_RESOURCE_ATTRIBUTES` | Appends resource attributes only to child agent processes. Values override inherited `OTEL_RESOURCE_ATTRIBUTES` keys with the same name. |
| `SKILL_UP_OTEL_SPAN_ATTRIBUTES` | Adds deployment-defined attributes to every `skill-up` span. |
| `SKILL_UP_AGENT_OTEL_SPAN_ATTRIBUTES` | Adds deployment-defined attributes to local child-agent wrapper spans and propagates them to child agents as W3C baggage. |

### Trace Topology And Logs

| Variable | Effect |
| --- | --- |
| `SKILL_UP_TRACE_TOPOLOGY` | `linked` or empty emits agent and agent-judge work as linked independent traces. `single` keeps them in the parent trace. |
| `SKILL_UP_OTEL_LOG_MESSAGE` | Controls whether `skill_up.log` span events include formatted `log.message`. Default includes it; `0` or `false` suppresses it. |

### Child Agent Detail Flags

| Variable | Effect |
| --- | --- |
| `OTEL_LOG_USER_PROMPTS` | Passed through to child agents; agents that support it may include user prompts in telemetry. |
| `OTEL_LOG_TOOL_CONTENT` | Passed through to child agents; agents that support it may include tool content in telemetry. |
| `OTEL_LOG_TOOL_DETAILS` | Passed through to child agents; agents that support it may include tool details in telemetry. |

### Propagated Context

`skill-up` injects these W3C context variables into child agent processes when
parent tracing is configured:

- `TRACEPARENT`
- `TRACESTATE`
- `BAGGAGE`

## Signal Model

### Traces

Tracing is enabled when either of these is configured:

- `OTEL_EXPORTER_OTLP_ENDPOINT`
- `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`

It is disabled when `OTEL_TRACES_EXPORTER=none`.

Supported OTLP protocols:

- `grpc`
- `http/protobuf`

The trace spans model `skill-up` runtime semantics rather than arbitrary
function boundaries:

- `skill-up`
- `cli.run`
- `runner.evaluate`
- `evaluator.evaluate_all`
- `evaluator.case`
- `evaluator.judge`
- `runtime.exec`

Span attributes include values such as engine, model, case status, case
configuration, iteration, exit code, and `runtime.exec` command/cwd details.
They must not include prompts, model outputs, file contents, or API keys.

### Metrics

Metrics are controlled by `OTEL_METRICS_EXPORTER`.

Supported values:

- `otlp`: export metrics over OTLP
- `console`: print metrics locally for debugging
- `none` or empty: disable metrics

The metrics intentionally avoid high-cardinality fields such as prompt IDs,
workspace paths, command text, or user content.

Current instruments:

- `skill_up.run.count`
- `skill_up.run.case.count`
- `skill_up.case.run.count`
- `skill_up.case.duration`
- `skill_up.runtime.exec.count`
- `skill_up.runtime.exec.duration`

### Resource Attributes

`OTEL_RESOURCE_ATTRIBUTES` is read through the OpenTelemetry SDK resource
loader and is attached to both traces and metrics.

Example:

```bash
export OTEL_SERVICE_NAME=skill-up
export OTEL_RESOURCE_ATTRIBUTES=deployment.environment=local,service.namespace=skill-up
```

`OTEL_SERVICE_NAME` overrides the default service name. If it is not set,
`skill-up` is used.

`SKILL_UP_OTEL_RESOURCE_ATTRIBUTES` appends resource attributes only to the
`skill-up` process. It is useful when a deployment needs routing or ownership
metadata that should not be inherited by child agents.

`SKILL_UP_AGENT_OTEL_RESOURCE_ATTRIBUTES` appends resource attributes only to
child agent processes. Values from this variable override inherited
`OTEL_RESOURCE_ATTRIBUTES` keys with the same name, so deployments can route
CLI spans and agent spans differently while preserving the same trace tree.

Some backends route projects from span attributes instead of resource
attributes. For that case, use:

- `SKILL_UP_OTEL_SPAN_ATTRIBUTES`: attributes added to every
  `skill-up` span
- `SKILL_UP_AGENT_OTEL_SPAN_ATTRIBUTES`: attributes added to local
  agent-wrapper spans such as `runtime.exec`; the same values are also sent as
  W3C baggage to child agent processes

Example:

```bash
export SKILL_UP_OTEL_RESOURCE_ATTRIBUTES=telemetry.project.id=744,telemetry.component=cli
export SKILL_UP_AGENT_OTEL_RESOURCE_ATTRIBUTES=telemetry.project.id=745,telemetry.component=agent
export SKILL_UP_OTEL_SPAN_ATTRIBUTES=telemetry.project.id=744
export SKILL_UP_AGENT_OTEL_SPAN_ATTRIBUTES=telemetry.project.id=745
```

The attribute names are intentionally deployment-defined. `skill-up` does not
hard-code vendor-specific project or application routing keys; if a collector or
backend requires one, configure that key through the resource attribute
or span attribute variables above.

## Agent Trace Correlation

Agent processes are child processes of `skill-up`, so trace correlation should
be implemented through environment propagation instead of log post-processing.

The implementation is a generic helper, not a Claude-specific path:

```go
observability.AgentEnv(ctx, currentEnv, attrs)
```

That helper:

- activate only when the parent process has OTel tracing configured
- inject W3C propagation values into the child environment:
  - `TRACEPARENT`
  - `TRACESTATE`
  - `BAGGAGE`
- pass through the relevant `OTEL_*` allowlist for runtimes that do not inherit
  the parent process environment, including trace endpoint, protocol, headers,
  compression, timeout, exporter, and service name
- append stable low-cardinality correlation attributes to
  `OTEL_RESOURCE_ATTRIBUTES`

The evaluator adds case-level attributes to the context before invoking the
agent, and each agent adapter adds engine/model attributes. Useful correlation
attributes include:

- `skill_up.engine`
- `skill_up.case.id`
- `skill_up.case.configuration`
- `skill_up.run.iteration`
- `skill_up.parent_trace_id`
- `skill_up.parent_span_id`

The child agent must actively extract `TRACEPARENT` for a true parent-child
trace tree. If the agent only supports `OTEL_*` exporters but does not extract
W3C context from environment variables, traces can still be joined in the
backend using the resource attributes above.

### Trace Topology

By default, agent execution is emitted as a separate trace linked back to its
`evaluator.case` parent. Agent-backed judges are split the same way; non-agent
judges remain in the `skill-up` trace because they are part of the evaluator
pipeline rather than separate agent execution.

The default topology is equivalent to `SKILL_UP_TRACE_TOPOLOGY=linked`:

```text
trace A: skill-up -> cli.run -> runner.evaluate -> evaluator.case
trace B: agent.run --link--> evaluator.case
trace C: evaluator.judge --link--> evaluator.case  # only for agent_judge
```

In linked mode, `agent.run` is a new root span with a span link to the
current `evaluator.case`. The child agent receives `agent.run` as its
`TRACEPARENT`, so agent-native spans such as Claude Code spans are attached to
the independent agent trace. For `agent_judge`, `evaluator.judge` is also
emitted as a new root span linked back to the case span. Debug logs emitted
after linked roots are created include the new trace ID through the logging
context, which makes the split trace discoverable from a single run log.

The link target is also written as low-cardinality span attributes
`skill_up.linked_trace_id` and `skill_up.linked_span_id` so backends that do
not render span links can still correlate the traces.

Agent-specific telemetry enablement remains a capability mapping. For Claude
Code, local verification against version `2.1.119` showed that true
parent-child traces require both W3C `TRACEPARENT` propagation and these
Claude-specific telemetry variables:

- `CLAUDE_CODE_ENABLE_TELEMETRY=1`
- `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1`
- `ENABLE_ENHANCED_TELEMETRY_BETA=1`

`skill-up` sets those variables only for the Claude Code adapter and only
when parent OTel tracing is configured. With those variables set, pre-tracer
shows linked traces:

```text
trace A: skill-up -> cli.run -> runner.evaluate -> evaluator.case
trace B: agent.run --link--> evaluator.case
         ├── runtime.exec
         └── claude_code.interaction
             └── claude_code.llm_request
```

Set `SKILL_UP_TRACE_TOPOLOGY=single` to put agent and judge spans back into
one parent-child trace for local debugging.

Without the enhanced telemetry variables, the child process may run
successfully while only the parent `skill-up` spans are visible.

For agents that do not emit their own OTel spans, `skill-up` still emits the
generic wrapper spans in the independent agent trace:

- `agent.run`
- `runtime.exec`

These variables should not become a `skill-up`-specific global switch; they
are the Claude Code adapter's mapping from generic OTel configuration to that
agent's telemetry capability.

Claude Code emits spans for the runtime work that actually happens. A simple
single-response case may only show `claude_code.interaction` and
`claude_code.llm_request`; tool spans appear only when the agent invokes tools.
Content and tool-detail capture remain explicit opt-ins. When users set these
Claude-compatible OTel flags, `skill-up` passes them through to child agents:

- `OTEL_LOG_USER_PROMPTS`
- `OTEL_LOG_TOOL_CONTENT`
- `OTEL_LOG_TOOL_DETAILS`

## Privacy And Cardinality Rules

Do not record:

- prompts or model outputs
- file names or file contents
- API keys, tokens, or provider credentials
- unbounded IDs in metric attributes

Prefer:

- statuses and result categories
- engine names
- case configuration names
- `runtime.exec` command and cwd details when needed for debugging
- exit codes
- durations and counts
- parent trace/span identifiers for correlation

If a future signal needs sensitive or high-cardinality data, it should be routed
through a separate channel with an explicit schema and privacy review rather
than added to generic OTel attributes.
