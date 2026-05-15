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
  type: claude_code
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
  type: none                      # none / opensandbox

# ========== 3. MCP servers ==========
mcp:
  servers:
    - name: github                # MCP server name
      mode: real                  # `real` is supported; `mocked` is reserved
      transport: http             # http / stdio; inferred from endpoint/command if omitted
      config_ref: evals/fixtures/mcp/github.yaml  # Path to config file

# ========== 4. Skill installation ==========
skills:
  - source: local_path            # local_path (a directory on disk)
    path: .                       # Path to the Skill

# ========== 5. Agent Engine ==========
engine:
  type: claude_code               # claude_code / codex / qodercli (also accepts qoder-cli)
  model:
    provider: anthropic
    name: claude-sonnet-4-6
    base_url: ""                  # Custom API endpoint (optional)

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

### MCP configuration

MCP currently supports `mode: real`, which installs a real MCP server into Agents such as `claude_code`, `qodercli`, or `codex`. `mode: mocked` is reserved and currently raises an error to avoid silently shipping a non-mocked server.

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

Environment-variable references support both `${VAR}` and full-value `$VAR` forms; the variable name must match `[A-Za-z_][A-Za-z0-9_]*`. Variables listed in `required_env` are injected into the Agent process; full env-var references inside `headers` are also recorded by name so the Agent can pick the right transport mechanism when installing the MCP server.

### Choosing a runtime environment

| Environment   | When to use                                       | Example Skills                              |
| :-----------: | :-----------------------------------------------: | :-----------------------------------------: |
| `none`        | Plain-text I/O, no filesystem dependencies        | Command routing, Q&A, text generation       |
| `opensandbox` | Requires a remote sandbox service                  | Code review, project scaffolding, scripting |

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
| `kwargs.file_transfer_parallelism` | Concurrency for directory upload/download. |

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
  type: claude_code
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
