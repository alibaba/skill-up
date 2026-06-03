# Custom Engine Design

> **Implementation status**: This document describes the full Custom Engine design
> (both the `local` and `http` transports). The current phase (Phase 1) implements
> `transport: local`; `transport: http` is fully designed and its config schema is
> parsed and validated, but the implementation lands in a later PR. Selecting `http`
> today is rejected by validation with a clear "not yet implemented" error.

This document defines the Custom Engine configuration interface and result contract
for `skill-up`. A Custom Engine is used to integrate agent executors that are not
built in — for example a local CLI, a script, an internal scheduled job, or a remote
HTTP agent service.

## Goals

- Support two invocation styles: `local` (local task execution) and `http` (remote
  service calls).
- Support referencing environment variables in the config — for command paths, URLs,
  headers, tokens, model parameters, etc.
- Expose a unified `SessionResult` to the runner / evaluator / judge, so downstream
  code does not need to know how the engine was invoked.
- Keep the runtime boundary clear: a local task must run inside the current runtime
  workspace via `runtime.Exec`.

## Non-goals

- No compatibility with the old single-field `engine.entry` config.
- When `engine.name` matches a built-in agent, `engine.custom` is not read.
- `skill-up` provides no implicit file-sync behavior for any agent. Whether it is a
  built-in agent, a Custom Agent, or a Custom Engine's `local` / `http` transport,
  only artifacts explicitly declared in the result are downloaded or written into the
  local report directory.

## Configuration entry point

`engine.name` is the user-defined agent name; `engine.model` is optional and only
needs to be filled in when the custom agent references model information through
template variables.

When `engine.name` matches a built-in agent (for example `claude_code`, `codex`,
`qodercli`), `skill-up` uses the built-in implementation. When `engine.name` does
not match a built-in agent, `engine.custom` must be provided, and `skill-up`
creates the agent from the Custom Engine config.

```yaml
engine:
  name: my-agent
  custom:
    transport: local
    kwargs:
      profile: strict
      max_files: "20"
```

If `engine.name` does not match a built-in agent and `engine.custom` is not provided,
a config error is reported, e.g. `unsupported agent "my-agent": missing engine.custom`.

## Minimum integration contract

When integrating a Custom Engine, you need to do three things:

1. Choose `transport: local` or `transport: http` in `eval.yaml`.
2. Make your agent accept the standard `SessionInput`.
3. Make your agent return the standard `SessionResult`.

The difference between `local` and `http` is only "how it is transported":

- A local agent reads `SessionInput` from the input file and writes `SessionResult`
  to the output file or to stdout.
- An HTTP agent reads `SessionInput` from the JSON body or the multipart `payload`
  field, and returns `SessionResult` as the HTTP response body.

### Minimal local config

```yaml
engine:
  name: review-cli
  custom:
    transport: local
    response_format: session_result
    env:
      OPENAI_API_KEY: ${api_key}
    local:
      command: ${REVIEW_AGENT_BIN}
      args:
        - run
        - --input
        - ${input_file}
        - --output
        - ${output_file}
      input_file: ${input_file}
      output_file: ${output_file}
```

The local agent reads `SessionInput` from `${input_file}` and writes the
`SessionResult` JSON to `${output_file}`.

### Minimal HTTP config

```yaml
engine:
  name: review-service
  custom:
    transport: http
    response_format: session_result
    http:
      url: ${CUSTOM_AGENT_ENDPOINT}/v1/run
      method: POST
      headers:
        Authorization: Bearer ${api_key}
      request_body: ${session_input}
```

The HTTP agent receives the `SessionInput` JSON and returns the `SessionResult` JSON.
If `custom.http.files` is configured, the request becomes a multipart request and
`SessionInput` is placed in the `payload` field.

### Integration checklist

Before completing an integration, confirm each item:

- It can read `SessionInput.messages` and treat it as the complete conversation
  history.
- It can read `SessionInput.kwargs`, treating every value as a string.
- When it needs workspace files, it relies only on `custom.http.files` or on explicit
  paths inside the local runtime workspace.
- It returns a parseable `SessionResult` for both success and failure.
- It returns at least `exit_code` and `final_message`.
- Every file that needs to be archived is written into `SessionResult.artifacts`,
  not relying on `skill-up` to auto-scan.
- It does not require secrets to be written into `eval.yaml`; the API key is
  referenced through `${api_key}` after credential resolution.
- It does not depend on implicit session state across cases, variants, or iterations.

Minimal `SessionResult`:

```json
{
  "exit_code": 0,
  "final_message": "done"
}
```

## Full configuration schema

```yaml
engine:
  name: string
  model:
    provider: string
    name: string
    base_url: string
    params:
      string: string
  custom:
    transport: local | http
    timeout_seconds: int
    response_format: session_result | text
    env:
      string: string
    kwargs:
      string: string
    local:
      command: string
      args: [string]
      cwd: string
      input_file: string
      output_file: string
    http:
      url: string
      method: POST
      headers:
        string: string
      files:
        - path: string
          required: bool
      request_body:
        string: any
```

### Field reference

| Field | Required | Description |
| --- | --- | --- |
| `engine.name` | yes | Agent name; a built-in match uses the built-in implementation, otherwise `engine.custom` is read |
| `custom.transport` | yes | Invocation style, `local` or `http` |
| `custom.timeout_seconds` | no | Engine call timeout; falls back to the case timeout when unset |
| `custom.response_format` | no | How the result is parsed, default `session_result`; keeping the default is recommended |
| `custom.env` | no | Custom environment variables; `local` injects them into the process env, `http` does not send them automatically |
| `custom.kwargs` | no | Custom parameters passed to the custom engine, typed as `dict[str]string` |
| `custom.local.command` | required for `local` | Executable command inside the runtime |
| `custom.local.args` | no | Command argument array |
| `custom.local.cwd` | no | Command working directory; defaults to `${workspace}` |
| `custom.local.input_file` | no | Input file path inside the runtime; defaults to `${input_file}` |
| `custom.local.output_file` | no | Result JSON file path inside the runtime |
| `custom.http.url` | required for `http` | HTTP call URL |
| `custom.http.method` | no | First version only supports `POST` |
| `custom.http.headers` | no | HTTP headers |
| `custom.http.files` | no | Declares the set of workspace files uploaded with the HTTP request |
| `custom.http.request_body` | no | HTTP JSON body template |

## Transport consistency principle

`local` and `http` are two carriers of the same Custom Engine contract. They should
share the same input, output, and security semantics as much as possible, diverging
only where the transport genuinely requires it:

| Dimension | Unified semantics | local carrier | http carrier |
| --- | --- | --- | --- |
| Input | `SessionInput` | Written to `custom.local.input_file` | JSON body; multipart `payload` when files are present |
| Multi-turn | `messages` is the complete conversation history | Read from the input file | Read from the request body / payload |
| Custom params | `custom.kwargs` | Appear in the input file, can be templated | Appear in the request body / payload, can be templated |
| Credentials | `${api_key}` referenced explicitly, never auto-injected | Injected via `custom.env` | Injected via `custom.http.headers` |
| Workspace input | Passed only when explicitly declared | Agent runs directly inside the runtime workspace | Uploaded explicitly via `custom.http.files` |
| Result | `SessionResult` | stdout or `output_file` | HTTP response body |
| Result parsing | `custom.response_format` | same | same |
| Artifact archiving | Explicitly declared in `SessionResult.artifacts` | same | same |

Do not introduce a separate message, kwargs, credential, or result model for one
transport. New capabilities should land on the unified contract first; only put
something under `custom.local` or `custom.http` when the carrier truly differs.

`session_result` is the main path. `text` is only suitable for throwaway scripts or
minimal integrations: `skill-up` treats the returned text as `final_message` and
builds a minimal result, but cannot obtain a full transcript, token counts,
structured artifacts, etc.

## API key

A Custom Engine does not configure secret values in `eval.yaml`. The `api_key` comes
from `skill-up`'s existing credential resolution chain — for example the CLI
`--api-key`, a provider environment variable, or `~/.skill-up/credentials.yaml`. A
Custom Engine only references the resolved API key through the template variable
`${api_key}`.

How `${api_key}` is used is decided by the Custom Engine config:

- The local transport can inject it explicitly via `custom.env`, e.g.
  `OPENAI_API_KEY: ${api_key}`.
- The HTTP transport can reference it explicitly via a header, e.g.
  `Authorization: Bearer ${api_key}`.
- If the custom agent does not need an API key, it does not have to reference
  `${api_key}`.

`api_key` must not be auto-injected into every custom agent's environment variables
or HTTP headers. Auto-injection makes the auth semantics of different providers and
agents opaque, and easily leaks credentials that should not be passed downstream.

The real value of `api_key` must be masked in logs, debug output, error messages, and
reports.

`custom.env` means different things for different transports: the local transport
injects it into the process environment; the HTTP transport does not send `custom.env`
to the server automatically. When the HTTP transport needs credentials or custom
headers, it should explicitly reference `${api_key}` or `${VAR}` in
`custom.http.headers` or `custom.http.request_body`.

## Environment variable references

String fields in the Custom Engine config support environment variable references:

```yaml
custom:
  env:
    OPENAI_API_KEY: ${OPENAI_API_KEY}
    AGENT_ENDPOINT: ${AGENT_ENDPOINT:-https://agent.example.com}
  http:
    headers:
      Authorization: Bearer ${CUSTOM_AGENT_TOKEN?CUSTOM_AGENT_TOKEN is required}
```

Supported forms:

| Form | Semantics |
| --- | --- |
| `${VAR}` | `VAR` must exist and be non-empty, otherwise config parsing fails |
| `${VAR:-default}` | Uses `default` when `VAR` is missing or empty |
| `${VAR?message}` | Reports an error with `message` when `VAR` is missing or empty |

Variable substitution applies only to string fields inside `engine.custom`, and to
string values in `engine.model.base_url` / `engine.model.params`. It must not apply
globally to the case prompt, judge criteria, or the whole YAML, to avoid accidentally
substituting user input.

Log output must hide sensitive values. Field names matching `KEY`, `TOKEN`, `SECRET`,
`PASSWORD`, `AUTHORIZATION`, or values in a URL query that look like tokens, should all
be masked.

## Custom parameters: kwargs

`custom.kwargs` passes agent-specific custom parameters and is fixed to the type
`dict[str]string`:

```yaml
engine:
  name: review-cli
  custom:
    transport: local
    kwargs:
      profile: strict
      max_files: "20"
      report_format: markdown
```

`kwargs` and `env` have different responsibilities:

| Field | Purpose | Suitable for sensitive values |
| --- | --- | --- |
| `custom.env` | Credentials, tokens, runtime environment variables | Yes, but logs must mask them |
| `custom.kwargs` | Agent behavior parameters, switches, business config | No |

Values in `kwargs` support environment variable references:

```yaml
custom:
  kwargs:
    profile: ${CUSTOM_AGENT_PROFILE:-default}
```

`kwargs` values may also reference built-in template variables (for example
`${case_id}` or `${prompt}`); they are rendered per case before being placed into the
session input and exposed as `${kwargs.<key>}`.

After resolution, `kwargs` flows into the local input file and the HTTP request body,
and can also be referenced through template variables. All kwargs values are treated
as strings; if the agent needs a number or boolean, it must parse it itself.

## Agent artifact archiving boundary

`skill-up`'s agent artifact archiving is driven by the `SessionResult` return value,
not by a workspace scan, the agent type, or the transport type. `skill-up` does not
auto-sync files just because an agent modified the workspace, a remote directory, or a
local temp directory.

Every file that needs to enter the report directory must be explicitly declared in
`SessionResult.artifacts`:

- Built-in agents and Custom Agents follow the same rule.
- An agent running inside the runtime may declare a `path` inside the runtime
  workspace.
- An HTTP or other remote agent may declare a downloadable `url`.
- Any agent may declare `content` or `content_base64` for small files.

Undeclared files are not detected, downloaded, or written into the report directory by
`skill-up`.

If an HTTP agent needs to read local workspace files, it must explicitly declare the
request input files via `custom.http.files`. This is request input, not workspace
sync; `skill-up` only uploads the declared file set and does not scan the whole
workspace.

## Built-in template variables

The Custom Engine config also supports the following template variables provided by
`skill-up`:

| Variable | Description |
| --- | --- |
| `${workspace}` | Absolute path of the current runtime workspace |
| `${prompt}` | The current case's single-turn prompt; empty for multi-turn cases |
| `${messages_json}` | The current case's message array as a JSON string |
| `${messages}` | The current case's message array; used as a structured value only inside a JSON body, payload, or input-file template |
| `${session_input}` | The standard SessionInput structure; used as a structured value only inside a JSON body, payload, or input-file template |
| `${session_input_json}` | The standard SessionInput as a JSON string |
| `${input_file}` | Suggested runtime input file path, default `inputs/messages.json` |
| `${output_file}` | Suggested runtime output file path, default `outputs/session-result.json` |
| `${model}` | Model reference in `provider/name` form; empty string when `engine.model` is unset |
| `${model_provider}` | `engine.model.provider`; empty string when unset |
| `${model_name}` | `engine.model.name`; empty string when unset |
| `${api_key}` | API key resolved by the existing credential chain; sensitive value, must be masked in logs |
| `${case_id}` | The current case ID |
| `${variant}` | `with_skill` or `without_skill` |
| `${max_turns}` | The current case's maximum number of interaction turns |
| `${timeout_seconds}` | The current Engine call timeout |
| `${kwargs}` | The structured object of `custom.kwargs`; used as a structured value only inside a JSON body or input-file template |
| `${kwargs_json}` | `custom.kwargs` as a JSON string |
| `${kwargs.<key>}` | References a single kwarg value, e.g. `${kwargs.profile}` |

Template variables and environment variables share the same syntax space. When a name
collides, the built-in template variable takes precedence.

## Multi-turn conversation input contract

A Custom Engine must support a unified message array as the standard input form.
`skill-up` normalizes the case input into `messages`:

```json
[
  { "role": "user", "content": "First read the current directory." },
  { "role": "assistant", "content": "Done." },
  { "role": "user", "content": "Now generate a report based on what you just learned." }
]
```

A single-turn case is equivalent to an array containing only one `user` message.
`prompt` is just a convenience variable exposed for simple CLIs; the primary contract
of a Custom Engine should be based on `messages`.

### SessionInput format

`skill-up` constructs a unified `SessionInput` for each agent invocation. The local
transport is recommended to write it into `${input_file}`; the HTTP transport uses it
as the JSON request body by default, or as the multipart `payload` field when file
uploads are present.

```json
{
  "case_id": "multi-turn-report",
  "variant": "with_skill",
  "workspace": "/tmp/skill-up/workspace",
  "model": "openai/gpt-4.1",
  "kwargs": {
    "profile": "strict",
    "max_files": "20"
  },
  "messages": [
    { "role": "user", "content": "First read the current directory." },
    { "role": "assistant", "content": "Done." },
    { "role": "user", "content": "Now generate a report based on what you just learned." }
  ],
  "max_turns": 12,
  "timeout_seconds": 300
}
```

`messages[*].role` supports `system`, `user`, `assistant`, and `tool`.

`content` is defined as a string in the first version. If multimodal or structured
content is needed later, it should be extended as `content_blocks` rather than
changing the meaning of `content`.

### Session state boundary

Each case variant is an independent session. A Custom Engine must not depend on
implicit remote session state across cases, variants, or iterations.

If the Engine itself supports session resume, it may only be used within a single
`Run`. The result may include the full transcript, but exposing a remote session ID
is not required; if exposed, it should be placed in `artifacts.logs` or a future
metadata field.

### Multi-turn execution semantics

When a Custom Engine receives multiple `messages`, it should treat them as the same
conversation history and continue from the last user message. It does not need to
replay each message and call the model after every user message; whether to compress
context or genuinely replay is the Engine's own decision.

The returned `transcript` should contain at least the input messages and the final
assistant reply. If the Engine produces tool calls or intermediate assistant messages
during execution, they should be appended to the transcript in order of occurrence.

## Local transport

The `local` transport runs a command via `runtime.Exec`. The command runs inside the
current runtime, so it can access the runtime workspace, installed skills, fixtures,
MCP config, and environment variables.

Example:

```yaml
engine:
  name: review-cli
  model:
    provider: openai
    name: gpt-4.1
  custom:
    transport: local
    timeout_seconds: 300
    response_format: session_result
    env:
      OPENAI_API_KEY: ${api_key}
    kwargs:
      profile: strict
      max_files: "20"
    local:
      command: ${REVIEW_AGENT_BIN}
      args:
        - run
        - --input
        - ${input_file}
        - --workspace
        - ${workspace}
        - --model
        - ${model}
        - --profile
        - ${kwargs.profile}
        - --output
        - ${output_file}
      cwd: ${workspace}
      input_file: ${input_file}
      output_file: ${output_file}
```

Invocation rules:

1. `skill-up` writes the path specified by `custom.local.input_file` inside the
   runtime, with the contents being the input-file JSON defined above.
2. `skill-up` renders `command`, `args`, `cwd`, and `env`.
3. `skill-up` assembles the command with shell-safe quoting, or executes it directly
   through an argv interface supported by the runtime.
4. The command must exit within `timeout_seconds`.
5. If `output_file` is configured, the result is read from that file first.
6. If `output_file` is not configured, the result is read from stdout.
7. With `custom.response_format: text`, stdout is used as `final_message` to build a
   minimal `SessionResult`.

File modifications by a local task should happen under `${workspace}`. If those files
need to enter the report directory, the agent must explicitly declare them in the
`artifacts` of the result.

## HTTP transport

> Not yet implemented in Phase 1; this section is the full design.

The `http` transport is used for a remote agent service or a local HTTP agent service.
It receives the standard `SessionInput`, and after execution returns text, transcript,
and artifact declarations through `SessionResult`. `skill-up` only downloads or
writes artifacts explicitly declared in the result.

Example:

```yaml
engine:
  name: remote-review-agent
  model:
    provider: openai
    name: gpt-4.1
  custom:
    transport: http
    timeout_seconds: 300
    response_format: session_result
    kwargs:
      profile: strict
      max_files: "20"
    http:
      url: ${CUSTOM_AGENT_ENDPOINT}/v1/run
      method: POST
      headers:
        Authorization: Bearer ${api_key}
        Content-Type: application/json
      files:
        - path: diff.patch
          required: true
        - path: "src/**/*.go"
          required: false
        - path: "**/*"
          required: false
      request_body: ${session_input}
```

Invocation rules:

1. `skill-up` renders the string values in the URL, headers, and request body.
2. When `custom.http.request_body` is not configured, the HTTP request body defaults
   to `${session_input}`.
3. If a field value in `request_body` is exactly `${session_input}`, `${messages}`, or
   `${kwargs}`, it is injected as a JSON structure, not as a string.
4. If `custom.http.files` is configured, `skill-up` expands the declared file set
   from the runtime workspace and uploads each file as multipart form-data.
5. With no file uploads, the request body is JSON-encoded.
6. With file uploads, multipart form-data is used; the JSON body becomes the `payload`
   field of the multipart request.
7. A non-2xx HTTP status is treated as an Engine execution error.
8. With `custom.response_format: session_result`, the response body must be
   `SessionResult` JSON.
9. With `custom.response_format: text`, the response body is used as `final_message`.

### HTTP multi-turn conversations

The multi-turn semantics of the HTTP transport are the same as the local transport:
`payload.messages` in a single request is the complete conversation history, and the
agent should continue from the last user message. The HTTP transport does not rely on
the server keeping session state across requests.

With file uploads, the multipart structure is:

- `payload`: the `SessionInput` JSON
- `files`: one or more file parts, where `filename` is the workspace-relative path

Like other transports, HTTP artifact archiving is driven by the result: files not
declared in `artifacts.files` or a compatible field are not detected, synced, or
downloaded by `skill-up`.

### HTTP input files

`custom.http.files` passes a set of files from the runtime workspace to the agent as
HTTP request input:

```yaml
custom:
  http:
    files:
      - path: diff.patch
        required: true
      - path: fixtures/context.json
        required: false
      - path: "src/**/*.go"
        required: false
      - path: "**/*"
        required: false
```

Field reference:

| Field | Required | Description |
| --- | --- | --- |
| `path` | yes | A relative file path or glob pattern inside the runtime workspace |
| `required` | no | Defaults to `true`; when `false`, a missing file is skipped |

Constraints:

- `path` must be a relative path inside the runtime workspace; it cannot be absolute
  and cannot contain `..`.
- `path` may be an exact file path or a glob pattern.
- When it contains glob metacharacters (`*`, `?`, `[`, `]`, `**`) it is expanded as a
  glob; otherwise it is treated as an exact file path.
- Globs are expanded only inside the runtime workspace; results do not escape the
  workspace.
- Globs only upload files; directories themselves are not uploaded as separate
  entries.
- `**/*` selects every matching file under the workspace.
- Each matching file is uploaded as a separate multipart file part, keeping its
  workspace-relative path.
- With `required: true`, an exact file that does not exist or a glob that matches
  nothing is treated as a config/input error.
- With `required: false`, an exact file that does not exist or a glob that matches
  nothing causes that entry to be skipped.
- Files are uploaded with their original content.
- Workspace files not explicitly selected by `custom.http.files[].path` are not
  uploaded.
- Uploaded files are only HTTP request input and do not change the artifact archiving
  rules.

The multipart file part should use the fixed field name `files`, with each part's
`filename` carrying the workspace-relative path, e.g. `src/main.go`.

## Result contract

The standard result of a Custom Engine is `SessionResult` JSON:

```json
{
  "engine": "custom",
  "model": "openai/gpt-4.1",
  "exit_code": 0,
  "duration_ms": 45200,
  "turns": 3,
  "input_tokens": 1200,
  "output_tokens": 450,
  "final_message": "Review completed. Found one issue.",
  "stderr": "",
  "transcript": [
    { "role": "user", "content": "Review the current diff." },
    { "role": "assistant", "content": "Found one issue in config parsing." }
  ],
  "artifacts": {
    "workspace_diff": "diff --git a/report.md b/report.md ...",
    "generated_files": ["outputs/report.md"],
    "logs": "agent log text"
  }
}
```

### Required fields

| Field | Type | Description |
| --- | --- | --- |
| `exit_code` | integer | Engine process or remote task exit code; `0` on success |
| `final_message` | string | The agent's final output text; may be empty, but not recommended |

### Optional fields

| Field | Type | Description |
| --- | --- | --- |
| `engine` | string | Identifier of the responder; filled by `skill-up` with `engine.name` when unset |
| `model` | string | Model reference; filled by `skill-up` from config when unset |
| `duration_ms` | integer | Engine-side elapsed time; filled by `skill-up` with the call duration when unset |
| `turns` | integer | Number of agent interaction turns |
| `input_tokens` | integer | Number of input tokens |
| `output_tokens` | integer | Number of output tokens |
| `stderr` | string | Error output or diagnostic information |
| `transcript` | array | Unified transcript |
| `artifacts` | object | Artifacts, logs, and workspace diff |

### Transcript contract

`transcript` uses a unified message structure, with `role` supporting `system`,
`user`, `assistant`, and `tool`. If the custom engine cannot provide a full
transcript, it should at least return `final_message`. `skill-up` builds a minimal
transcript from the input messages and `final_message`.

### Artifacts contract

```json
{
  "workspace_diff": "diff --git ...",
  "generated_files": ["outputs/report.md"],
  "files": [
    { "name": "report.md", "path": "outputs/report.md", "content_type": "text/markdown" },
    { "name": "remote-report.html", "url": "http://127.0.0.1:8080/artifacts/report.html", "content_type": "text/html" },
    { "name": "summary.json", "content": "{\"status\":\"pass\"}", "content_type": "application/json" }
  ],
  "logs": "agent logs"
}
```

`generated_files` is a lightweight field compatible with the existing report
structure, suitable for the local transport returning file paths that already exist
inside the runtime workspace. Relative paths are rooted at the runtime workspace; when
the local transport returns an absolute path, it must be inside the runtime workspace.

`artifacts.files` is the structured artifact field recommended for Custom Engines:

| Field | Required | Description |
| --- | --- | --- |
| `name` | yes | Artifact file name, used for archiving into the report directory |
| `path` | conditional | File path inside the runtime workspace, common for the local transport |
| `url` | conditional | Downloadable URL, common for the HTTP transport |
| `content` | conditional | Inline content for a small text artifact |
| `content_base64` | conditional | base64 content for a binary artifact |
| `content_type` | no | MIME type |

At least one of `path`, `url`, `content`, `content_base64` must be provided. A
non-local transport must not point `generated_files` or `files.path` at an arbitrary
host file path.

## Error handling

A Custom Engine call failure falls into three categories:

| Category | Condition | Handling |
| --- | --- | --- |
| Config error | Missing required field, unresolved environment variable, invalid transport | Fails before the run |
| Invocation error | Local command cannot start, non-2xx HTTP, timeout | Case result is `ERROR` |
| Result error | Returned JSON cannot be parsed, missing `exit_code`, wrong field type | Case result is `ERROR` |

If the Engine returns a valid `SessionResult` with `exit_code != 0`, the runner should
keep that `SessionResult` and mark the case as an execution error. `stderr` and
`final_message` should enter the report to aid debugging.

## Security constraints

skill-up does not trust the custom engine command or its returned `SessionResult`.
The hardening below is enforced in code; treat each item as part of the contract.

### Trust model

| Source | Trusted? | Where enforced |
| --- | --- | --- |
| `eval.yaml` (the operator) | Trusted | Validation only catches mistakes |
| The custom engine process | **Not trusted** | Process group kill, timeout, output bounds |
| The `SessionResult` JSON the engine returns | **Not trusted** | Schema validation, path confinement, size caps |
| `engine.custom.env` values at run time | Treated as secrets | Masked in captured output |

### Credential handling

- `${api_key}`, `${kwargs.<key>}` whose name normalizes to a secret-like form
  (`api_key`, `token`, `secret`, `password`, `credentials`, `authorization`), and the
  aggregate forms `${kwargs}` / `${kwargs_json}` / `${session_input}` /
  `${session_input_json}` are **rejected** by the strict resolver when referenced in
  any command-line context (`local.command`, `local.args`, `local.cwd`,
  `local.input_file`, `local.output_file`, plus the HTTP equivalents). Secrets must
  reach the agent through `engine.custom.env`.
- Environment-variable references whose name itself is secret-like
  (`API_KEY`, `*_TOKEN`, `*_SECRET`, …) are rejected in command-line contexts too.
- `${VAR:-default}` defaults whose **literal** value matches a well-known credential
  shape (`sk-…`, `sk-ant-…`, `ghp_…`, `AIza…`, `AKIA…`, `xox*-…`, JWT) are rejected
  even when the variable name is benign. The pattern list is intentionally
  conservative — it catches the common cases, not every vendor.
- `SessionResult.Stderr` and `FinalMessage` written into the report are masked: the
  configured model-layer API key and every value passed via `engine.custom.env`
  (≥ 8 characters) are replaced with `***REDACTED***` before the result leaves the
  agent.
- kwargs keys that collide with a built-in template variable (`model`, `case_id`,
  `max_turns`, `workspace`, …) are rejected at validation; rename them.

### Workspace confinement

- `local.cwd`, `local.input_file`, and `local.output_file` are confined to the
  runtime workspace. Absolute paths must already point inside the workspace;
  relative paths are joined against the workspace root. `..` traversal is rejected.
- The check runs `filepath.EvalSymlinks` against the workspace root and against the
  deepest existing ancestor of the supplied path. Symlinks pointing outside the
  workspace are caught even when planted under workspace-relative names.
- The same confinement is re-run **at every use site** (`clearStaleOutputFile`,
  `readRawResult` before `DownloadFile`, `archiveRenamedPathArtifact` before
  `DownloadFile`, `collectArtifacts` before registering paths). This closes the
  TOCTOU window where the engine, after the pre-run validation, plants a
  not-yet-existing parent (e.g. `outputs/newdir`) as a symlink pointing outside
  the workspace.
- `artifacts.files[].path` and any legacy `generated_files` entries returned by the
  engine are filtered through the same workspace check; out-of-bound entries are
  dropped with a warning rather than silently followed through `DownloadFile`.

### Result bounds

- `artifacts.files[].name` is required. Empty names previously caused inline entries
  to be silently dropped by `writeInlineArtifact`.
- `artifacts.files[].content` and the base64-decoded payload of
  `artifacts.files[].content_base64` are capped at **50 MB per file** and **200 MB
  in aggregate** per `SessionResult`. The pre-decode estimate uses
  `len(content_base64) * 3 / 4`; an exact post-decode check enforces the cap if the
  estimate was off.

### Process and time bounds

- The custom command runs in a dedicated process group (`Setpgid`). When the run
  context cancels (timeout or upstream cancel), the whole group is killed so a
  backgrounded child does not outlive its parent.
- `cmd.WaitDelay` bounds how long `Wait` blocks on inherited pipes after the
  process exits; a clean exit with lingering stdio pipes is classified as exit 0
  (not a hard error) so an agent that backgrounds children is not reported as
  failed.
- `engine.custom.timeout_seconds` is clamped to the smaller of itself and the outer
  case deadline. `${timeout_seconds}` / `SessionInput.TimeoutSeconds` always reflect
  the real wall-clock budget the agent has.

### Logging discipline

- Environment-variable resolution failures are reported at config-load time, not
  half-way through a run.
- The pre-run cleanup of an explicitly-configured `output_file` only deletes the
  file when one already existed and prints a marker so the framework can distinguish
  "cleared a stale file" from "agent never wrote one". The default `output_file`
  path is never auto-deleted — it may be ordinary fixture input.

## Implementation notes (maintainers)

- `internal/config/schema.go` defines `CustomEngineConfig`, attached to
  `EngineConfig.Custom`.
- `internal/config/customengine.go` implements env reference resolution
  (`${VAR}` / `${VAR:-default}` / `${VAR?message}`), applied only to the
  `engine.custom` config tree and `engine.model` string values; built-in template
  variable names are left for run-time resolution.
- `internal/config/validator.go` first checks whether `engine.name` matches a built-in
  agent; when it does not, it requires `engine.custom` and validates the transport and
  required fields.
- `internal/agent/custom.go` implements `CustomAgent`.
- `internal/agent/factory.go` matches built-in agents first; when there is no match
  and `engine.custom` exists, it creates a `CustomAgent`; when there is no match and
  the custom config is missing, it reports
  `unsupported agent "<name>": missing engine.custom`.
- The local transport reuses `runtime.Exec`; the HTTP transport uses a host-side HTTP
  client.
- Unit tests cover env references, sensitive-value masking, local stdout JSON, local
  output-file JSON, HTTP JSON, and non-2xx HTTP.
