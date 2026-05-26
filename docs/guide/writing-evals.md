# Writing Evals

This page describes how to author a complete evaluation config for your Skill: how to declare the runtime environment, write cases, and configure grading strategies.

---

## Directory layout

Evaluation files live under the `evals/` folder of your Skill:

```text
my-skill/
  SKILL.md                        # Skill definition
  evals/
    eval.yaml                     # Entrypoint config (required)
    cases/                        # One file per case
      basic-success.yaml
      edge-case-null.yaml
      regression-001.yaml
    fixtures/                     # Optional test resources
      repos/                      # Repository templates
        sample-project/
      diffs/                      # Patch files
        null-check.patch
      scripts/                    # Grading scripts
        check-output.sh
      mcp/                        # MCP server configs
        github.json
```

> **Naming convention:** the case file basename (without `.yaml`) is the case ID. For example, `basic-success.yaml` defines a case with ID `basic-success`.

---

## eval.yaml — entrypoint config

`eval.yaml` is the global config: *which environment*, *which engine*, *how to grade*.

### Minimal config

```yaml
schema_version: v1alpha1

environment:
  type: none

engine:
  name: claude_code
  model:
    provider: anthropic
    name: claude-sonnet-4-6

cases:
  files:
    - evals/cases/my-test.yaml
```

### Full reference

```yaml
# ========== 1. Schema version ==========
schema_version: v1alpha1          # Fixed value, required

# ========== 2. Runtime environment ==========
environment:
  type: none                      # none / opensandbox / docker

# ========== 3. MCP servers ==========
mcp:
  servers:
    - name: github                # MCP server name
      mode: real                  # real / mocked
      transport: http             # http / stdio; inferred from endpoint/command if omitted
      config_ref: evals/fixtures/mcp/github.yaml  # Path to config file

# ========== 4. Skill installation ==========
skills:
  - source: local_path            # local_path (a directory on disk)
    path: .                       # Path to the Skill

# ========== 5. Agent Engine ==========
engine:
  name: claude_code               # claude_code / codex / qodercli (also accepts qoder-cli)
  model:
    provider: anthropic
    name: claude-sonnet-4-6
    base_url: ""                  # Custom API endpoint (optional)
  # kwargs: { ... }               # Agent-specific switches — see "Engine kwargs" below

# ========== 6. Cases ==========
cases:
  files:                          # Case file paths (relative to the Skill root)
    - evals/cases/basic-success.yaml
    - evals/cases/edge-case.yaml
  defaults:
    timeout_seconds: 300          # Per-case timeout, default 300s
    max_turns: 12                 # Max conversation turns, default 12
  parallelism: 2                  # Case parallelism, default 1
  retry_policy:
    max_retries: 1
    retry_on: [timeout, error]

# ========== 7. Benchmark (optional) ==========
benchmark:
  enabled: false                  # When true, runs both with_skill and without_skill

# ========== 8. Reports ==========
report:
  formats: [json, html]           # json / junit / html
  artifacts: [transcript]
```

`cases.parallelism` is the file-level default. To override it for a single run, use `skill-up run --parallelism N` without modifying `eval.yaml`. Allowed range: **1 to 256**.

### Engine kwargs (agent-specific switches)

`engine.kwargs` is a free-form string map. Each agent reads only the keys it recognises; unknown keys are ignored. Unrecognised keys (typos like `bypas_sandbox`) emit a DEBUG log line — run with `-v` to surface them. CLI override: `--engine-kwarg key=value` (alias `--ek`), repeatable. Precedence: `--engine-kwarg` > `engine.kwargs` > default.

| key | agent | `true` behaviour | unset / `false` |
| :---: | :---: | :--- | :--- |
| `bypass_sandbox` | `codex` | Forces `--dangerously-bypass-approvals-and-sandbox`; overrides the runtime-derived choice. Use when the host kernel lacks Landlock support (e.g. some CI containers) | Default: `none` runtime → `--sandbox workspace-write`; other runtimes already bypass |
| `bypass_sandbox` | `claude_code` | No-op — claude already runs with `--permission-mode=bypassPermissions` | No-op |
| `bypass_sandbox` | `qodercli` | No-op — no equivalent flag | No-op |

```bash
# One-off override at the call site
skill-up run evals/eval.yaml --engine-kwarg bypass_sandbox=true
```

### Custom Engine

When `engine.name` is not one of the built-ins (`claude_code`, `codex`, `qodercli`), declare an `engine.custom` block so skill-up knows how to invoke your agent. Only `transport: local` is implemented today; `transport: http` is reserved and currently fails validation with "not yet implemented".

```yaml
engine:
  name: my-agent
  model:
    provider: anthropic
    name: claude-sonnet-4-6
  custom:
    transport: local             # local (implemented) | http (planned)
    response_format: session_result   # session_result (default) | text
    timeout_seconds: 300
    env:                         # credentials and secrets — NEVER reference these in command/args
      MY_AGENT_TOKEN: ${MY_AGENT_TOKEN}
    kwargs:                      # non-secret knobs exposed as ${kwargs.<key>}
      profile: production
    local:
      command: /opt/my-agent/bin/run
      args:
        - --input
        - ${input_file}          # path to the SessionInput JSON skill-up writes
        - --output
        - ${output_file}         # path your agent should write its SessionResult JSON to
      cwd: ${workspace}          # optional; confined to the runtime workspace
      input_file: inputs/messages.json   # optional override (relative to workspace)
      output_file: outputs/session-result.json   # optional override
```

Template variables available in `command` / `args` / `cwd` / `env` / `input_file` / `output_file`:
`${workspace}`, `${input_file}`, `${output_file}`, `${model}`, `${model_provider}`, `${model_name}`, `${case_id}`, `${variant}`, `${max_turns}`, `${timeout_seconds}`, `${kwargs.<key>}`, plus environment variables via `${VAR}` / `${VAR:-default}` / `${VAR?error message}`.

Secret-handling rules (enforced at config load):

- `${api_key}` and any kwarg whose key looks like a credential (`token`, `secret`, `api_key`, `apiKey`, `bearerToken`, …) cannot be referenced from `command` / `args` / `cwd` / `input_file` / `output_file`. Pass them through `engine.custom.env`, where they reach your agent as process environment variables instead of leaking into process listings.
- `${SOMEVAR:-...}` defaults that contain recognizable credential shapes (`sk-...`, `sk-ant-...`, `ghp_...`, `AIza...`, `AKIA...`, JWTs) are likewise rejected in command-line contexts.

See `docs/design/custom-engine.md` for the full SessionInput / SessionResult schema your agent must conform to.


### MCP configuration

MCP supports `mode: real` and `mode: mocked`. `real` installs a real MCP server into Agents such as `claude_code`, `qodercli`, or `codex`; `mocked` makes `internal/mcp` generate a local stdio mock server that is then installed into the Agent like any other MCP server.

HTTP MCP servers can be declared inline or pulled in via `config_ref`:

```yaml
mcp:
  servers:
    - name: agent-sandbox
      mode: real
      transport: http
      config_ref: evals/fixtures/mcp/agent-sandbox.yaml
```

A `config_ref` file supports:

```yaml
transport: http
endpoint: https://mcp.example.com/mcp?token=${MCP_TOKEN}
required_env:
  - MCP_TOKEN
headers:
  PRIVATE-TOKEN: ${PRIVATE_TOKEN}
```

stdio MCP servers use `command` and `args`:

```yaml
mcp:
  servers:
    - name: marker
      mode: real
      transport: stdio
      command: /usr/bin/python3
      args: [evals/fixtures/mcp/marker_server.py]
```

A mocked MCP server can use the built-in `filesystem` mock server directly:

```yaml
mcp:
  servers:
    - name: filesystem
      mode: mocked
```

Or define tool responses through `config_ref`:

```yaml
mcp:
  servers:
    - name: project-mgmt
      mode: mocked
      config_ref: evals/fixtures/mcp/project-mgmt.yaml
```

```yaml
tool_responses:
  create_publish_plan_simple:
    default:
      id: 999
      name: "{{params.name}}"
      status: ONGOING
```

Environment-variable references support both `${VAR}` and full-value `$VAR` forms; the variable name must match `[A-Za-z_][A-Za-z0-9_]*`. Variables listed in `required_env` are injected into the Agent process; full env-var references inside `headers` are also recorded by name so the Agent can pick the right transport mechanism when installing the MCP server.

### Choosing a runtime environment

| Environment   | When to use                                       | Example Skills                              |
| :-----------: | :-----------------------------------------------: | :-----------------------------------------: |
| `none`        | Plain-text I/O, no filesystem dependencies        | Command routing, Q&A, text generation       |
| `opensandbox` | Requires a remote sandbox service                  | Code review, project scaffolding, scripting |
| `docker`      | Local container isolation, no remote dependency    | Custom toolchains, reproducible CI, offline |

> **Tip:** if your Skill does not touch the filesystem, `none` avoids sandbox provisioning and is significantly faster.

#### OpenSandbox configuration

When `environment.type: opensandbox` is used, sandbox auth is read from the `OPENSANDBOX_API_KEY` environment variable. Non-secret options such as service URL or extension flags belong in `environment.kwargs`. The Agent runtime handles its own binary path; you usually do **not** need to set `PATH` inside the eval config.

```yaml
environment:
  type: opensandbox
  image: registry.example.com/your-org/sandbox-base:latest
  workspace_mount: /workspace
  ready_timeout_seconds: 300
  kwargs:
    base_url: https://agent-sandbox.example.com
    extensions: '{"profile":"ci"}'
    request_timeout_seconds: "900"
    file_transfer_parallelism: "8"
```

Common fields:

| Field | Description |
| --- | --- |
| `image` | Sandbox image; falls back to the OpenSandbox runtime default when omitted. |
| `workspace_mount` | Workspace path inside the sandbox; defaults to `/workspace`. |
| `env` | Environment variables injected into sandbox commands. To extend `PATH`, use `PATH: $CUSTOM_BIN:$PATH` — the runtime expands it inside the sandbox. |
| `setup_steps` | Init commands executed inside the workspace after the sandbox starts. |
| `kwargs.base_url` | OpenSandbox service URL; can also be set via `OPENSANDBOX_BASE_URL`. |
| `kwargs.extensions` | OpenSandbox extension config as a JSON string. |
| `kwargs.request_timeout_seconds` | Request timeout for the OpenSandbox SDK. |
| `kwargs.file_transfer_parallelism` | Concurrency for directory download. |

#### Docker configuration

When `environment.type: docker` is used, the agent runs inside a local Docker container. This provides container-level isolation (filesystem, process, network) without any remote service dependency.

**Prerequisites:** a working `docker` CLI on PATH and a running Docker daemon. The runtime does not pull images automatically — run `docker pull <image>` beforehand.

```yaml
environment:
  type: docker
  image: node:22                    # Required — must be pre-pulled locally
  workspace_mount: /workspace       # Default: /workspace
  env:
    NPM_CONFIG_REGISTRY: https://registry.npmmirror.com
  setup_steps:
    - run: npm install -g typescript
  entrypoint: ["sleep", "infinity"] # Override container entrypoint (default: sleep infinity)
```

Common fields:

| Field | Description |
| --- | --- |
| `image` | **Required.** Docker image name. Must be available locally (pre-pull with `docker pull`). |
| `workspace_mount` | Workspace path inside the container; defaults to `/workspace`. Must be absolute. |
| `env` | Environment variables injected into container commands. |
| `setup_steps` | Init commands executed inside the container after it starts. |
| `entrypoint` | Override the container's ENTRYPOINT. Defaults to `["sleep", "infinity"]`. |
| `network_policy` | `deny_all` creates the container with `--network=none` (no network access). `allow_declared` is not yet supported — use `opensandbox` if you need FQDN-level egress filtering. |

> **Tip:** Docker runtime is a good fit for evaluations that need custom system packages, specific language runtimes, or offline/air-gapped environments. For remote sandboxing with managed infrastructure, use `opensandbox` instead.

---

## case.yaml — evaluation case

Each `.yaml` file under `cases/` defines one case: *what prompt to send* and *how to verify the result*.

### Single-turn case

Most scenarios only need a single-turn prompt:

```yaml
id: find-null-bug
title: Should detect a null pointer bug
description: Verify that the Skill catches null dereferences during code review

input:
  prompt: |
    Review the current diff and report findings.

context:
  repo_fixture: evals/fixtures/repos/null-check-bug    # Load a repo template
  git:
    init: true
    checkout: main
    apply_diff: evals/fixtures/diffs/null-check.patch

constraints:
  timeout_seconds: 180
  max_turns: 8

expect:                           # Cheap gating checks
  must_contain:
    - "null"
    - "bug"
  must_not_contain:
    - "LGTM"
  exit_code: 0

judge:                            # Quality grading
  type: rule_based
  success:
    - output_contains:
        all: ["null", "bug"]
    - exit_code: 0
```

---

## Case context

`context` prepares the initial workspace for a case.

### Load a repository template

```yaml
context:
  repo_fixture: evals/fixtures/repos/my-project    # Copy contents into the workspace
```

### Git operations

```yaml
context:
  repo_fixture: evals/fixtures/repos/my-project
  git:
    init: true
    checkout: feature-branch
    apply_diff: evals/fixtures/diffs/my.patch
    remotes:
      - name: origin
        url: https://github.com/user/repo
```

### Inline files

```yaml
context:
  files:
    "src/main.py": |
      def hello():
          print("Hello World")
    "config.json": |
      {"debug": true}
```

---

## Grading strategies

Grading happens in two layers: **expect** (gating checks) and **judge** (quality assessment).

### expect — fast gating

`expect` is a zero-cost local check. If `expect` fails, `judge` is skipped.

```yaml
expect:
  must_contain:                 # Output must contain ALL of these
    - "review"
    - "bug"
  must_not_contain:             # Output must NOT contain any of these
    - "LGTM"
    - "error"
  exit_code: 0                  # Expected exit code
  files_exist:                  # Files that must exist
    - "review.md"
    - "output.json"
  files_not_exist:              # Files that must not exist
    - "temp.log"
```

### judge: rule_based — deterministic rules

Decide pass/fail by declarative rules — fully deterministic and reproducible:

```yaml
judge:
  type: rule_based
  success:                                    # All conditions must be met
    - output_contains:
        all: ["bug", "null"]                  # Must contain ALL
        any: ["suggest fix", "recommend"]     # Must contain at least one
        not: ["LGTM"]                         # Must NOT contain
    - exit_code: 0
    - tool_called:                            # Agent must invoke this tool
        name: "github::create_pull_request"
        args:                                 # Partial-match against tool args
          title: "Fix null check"
  failure:                                    # If ANY rule matches → immediate fail
    - output_contains:
        any: ["no changes needed", "code is correct"]
```

> **Evaluation order:** `failure` outranks `success`. If any `failure` rule matches, the case fails immediately. Otherwise every `success` rule must pass.

### judge: script — custom script

Run your own script (in any language) to grade results:

```yaml
judge:
  type: script
  script_path: evals/fixtures/scripts/check-quality.sh
  timeout_seconds: 30
```

Script contract:

- Exit code **0** = pass, anything non-zero = fail
- Working directory is the case workspace root
- Available env vars: `$EVAL_FINAL_MESSAGE`, `$EVAL_EXIT_CODE`
- `$EVAL_TRANSCRIPT_PATH` is set only when a transcript was produced; otherwise it is empty
- Stdout from the script is captured as the grading rationale in the report

### judge: agent_judge — LLM rubric

Let an LLM grade against rubric criteria — useful when semantic understanding is required:

```yaml
judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6        # Model used by the judge
  criteria:                                  # Natural-language rubric
    - "Identifies a real bug with an accurate location"
    - "Does not flag correct code as a bug"
    - "Recommendations are actionable, not generic"
  pass_threshold: 0.7                        # Default 0.7
  timeout_seconds: 60                        # Optional: bound a single judge call (0 = no judge-level deadline, parent case timeout still applies)
```

> **Cost note:** `agent_judge` consumes additional tokens. Prefer `expect` or `rule_based` for deterministic checks and reserve `agent_judge` for assertions that genuinely require semantic understanding.

---

## Benchmark mode

Setting `benchmark.enabled: true` runs every case **twice**:

1. **with_skill** — Skill installed (treatment)
2. **without_skill** — Skill removed (baseline)

The diff highlights the value the Skill adds (pass-rate uplift, time/token deltas).

```yaml
benchmark:
  enabled: true
```

> **Note:** benchmark mode doubles wall time and token spend. It is disabled by default.

---

## Credentials

Evaluations call Agent Engines and model APIs, so credentials are required. Resolution order, highest priority first:

### 1. CLI flag (transient override)

```bash
skill-up run ./evals/eval.yaml --api-key sk-xxx
```

### 2. Environment variables (recommended)

```bash
export ANTHROPIC_API_KEY=sk-ant-xxx
export OPENAI_API_KEY=sk-xxx
skill-up run ./evals/eval.yaml
```

Variables follow the `<PROVIDER>_<FIELD>` pattern. Supported fields: `API_KEY`, `BASE_URL`, `MODEL`.

| Provider  | API Key             | Base URL             | Model              |
| :-------: | :-----------------: | :------------------: | :----------------: |
| anthropic | `ANTHROPIC_API_KEY` | `ANTHROPIC_BASE_URL` | `ANTHROPIC_MODEL`  |
| openai    | `OPENAI_API_KEY`    | `OPENAI_BASE_URL`    | `OPENAI_MODEL`     |
| other     | `<PROVIDER>_API_KEY`| `<PROVIDER>_BASE_URL`| `<PROVIDER>_MODEL` |

A `.env` file at the project root is also auto-loaded on startup.

### 3. Config file (persistent)

Create `~/.skill-up/credentials.yaml`:

```yaml
providers:
  anthropic:
    api_key: sk-ant-xxx
  openai:
    api_key: sk-xxx
    base_url: https://api.openai.com/v1    # Optional, useful for proxies
```

### qodercli credentials

qodercli authentication is **completely separate** from model-layer credentials such as `ANTHROPIC_API_KEY`. The two layers cannot be mixed.

| Layer              | Environment variable             | Purpose                                                  |
| :----------------: | :------------------------------: | :------------------------------------------------------: |
| qodercli service   | `QODER_PERSONAL_ACCESS_TOKEN`    | Authenticates against the qodercli service               |
| Model layer        | `ANTHROPIC_API_KEY`, etc.        | Managed internally by qodercli; users do not configure it |

Setup:

```bash
# Option 1: export the env var directly
export QODER_PERSONAL_ACCESS_TOKEN=your_token_here

# Option 2: write it into the project root .env file
echo 'QODER_PERSONAL_ACCESS_TOKEN=your_token_here' >> .env
```

> **Tip:** `QODER_PERSONAL_ACCESS_TOKEN` is optional. When unset, qodercli falls back to the local login state under `~/.qoder/`, the same as running `qodercli` manually. Use `qodercli /login` to log in locally.
>
> **Note:** the `--api-key` flag and any provider API key declared in `eval.yaml` are **not** used as the qodercli auth token. qodercli only reads `QODER_PERSONAL_ACCESS_TOKEN` or the local login state.

qodercli also has model-parameter restrictions:

- `model` must be one of qodercli's predefined values: `lite`, `efficient`, `auto`, `performance`, `ultimate`
- `base_url` has no effect for qodercli

---

## Worked examples

### Example A — plain-text routing Skill

Lightweight scenario without filesystem state, verifying that the Skill routes the right command:

```yaml
# eval.yaml
schema_version: v1alpha1
environment:
  type: none
engine:
  name: claude_code
  model:
    provider: anthropic
    name: claude-sonnet-4-6
cases:
  files:
    - evals/cases/route-to-summary.yaml
  parallelism: 4                  # Stateless, fully parallelizable
judge:
  type: rule_based
```

```yaml
# cases/route-to-summary.yaml
id: route-to-summary
title: Resource overview should route to `app summary`

input:
  prompt: |
    Show the resource overview of my-app, including machine count.

expect:
  must_contain:
    - "app summary"
  must_not_contain:
    - "app get"

judge:
  type: rule_based
  success:
    - output_contains:
        all: ["app summary", "--name"]
```

### Example B — MCP tool-call Skill

Validate that the Skill invokes a specific MCP tool:

```yaml
# cases/create-plan.yaml
id: create-plan
title: Should call the create-publish-plan tool correctly

input:
  prompt: |
    Create a release plan called "Q1 release" scheduled for 2026-04-03.

judge:
  type: rule_based
  success:
    - tool_called:
        name: "project-mgmt::create_publish_plan_simple"
        args:
          name: "Q1 release"
          planReleaseDate: "2026-04-03"
```

---

## FAQ

### How are paths in `eval.yaml` resolved?

All paths (including `cases.files` and fixture paths) are resolved relative to the **Skill root** — the directory that contains `SKILL.md`. For example, `evals/fixtures/repos/my-project` means `<skill-root>/evals/fixtures/repos/my-project`.

### When should I use `expect` vs `judge`?

Use `expect` for fast, zero-cost gating (file existence, keyword presence). Use `judge` for richer quality grading. They compose well — when `expect` fails, `judge` is skipped, saving time and tokens.
