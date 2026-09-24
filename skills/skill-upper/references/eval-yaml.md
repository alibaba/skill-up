# eval.yaml field reference (skill-up)

`eval.yaml` is the evaluation entry point. It specifies the environment, Agent Engine, cases, judge, and reports. See the [writing evaluations guide](https://alibaba.github.io/skill-up/guide/writing-evals.html).

## Full configuration example

```yaml
schema_version: v1alpha1

environment:
  type: none                      # none | opensandbox | docker

mcp:
  servers:
    - name: github
      mode: real                  # real; mocked is reserved
      transport: http             # http | stdio; inferred from endpoint/command when omitted
      config_ref: evals/fixtures/mcp/github.yaml

skills:
  - source: local_path
    path: .
    include: [SKILL.md, "references/**", "scripts/**"]  # Optional; all files by default
    exclude: [".qoder/repowiki/**"]                     # Optional; exclude takes priority

engine:
  name: claude_code               # claude_code | codex | qodercli (qoder-cli also accepted)
  model:
    provider: anthropic
    name: claude-sonnet-4-6
    base_url: ""

cases:
  files:
    - evals/cases/a.yaml
  defaults:
    timeout_seconds: 300
    max_turns: 12
    collect_artifacts:          # Optional: workspace files selected by glob
      - "**/*.json"
      - "report/**"
    expect:                     # Optional: gate checks for every case
      exit_code: 0
      must_not_contain:
        - "TODO"
        - "I cannot"
  parallelism: 2
  retry_policy:
    max_retries: 1
    retry_on: [timeout, error]

judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
  skills:                         # Optional: rubric Skills installed only for the judge
    - source: local_path
      path: evals/fixtures/judge-rubric
  criteria:
    - "Apply the scoring rules in judge-rubric to assess the output"

benchmark:
  enabled: false

report:
  formats: [json, html]
  artifacts: [transcript]
```

`skill-up run --parallelism N` temporarily overrides `cases.parallelism` (1–256). `skill-up run --baseline` temporarily sets `benchmark.enabled: true`.

`collect_artifacts`, defined in `cases.defaults` or a case file, selects workspace files using [doublestar](https://github.com/bmatcuk/doublestar) globs (`*` within one directory; `**` across directories). Matching files are downloaded with relative paths to `<output-dir>/<case>/<config>/outputs/workspace/` even if the agent fails or times out. Default and case patterns form a deduplicated union. This is separate from `report.artifacts` (artifact types) and the git diff string passed to `agent_judge`.

`skills[].include` and `skills[].exclude` use doublestar globs relative to `skills[].path`, with `/` separators. An empty `include` selects all files; `exclude` is applied afterward and takes priority. `evals/` is never installed. An explicit `include` must contain `SKILL.md`. `judge.skills` supports the same filters.

`judge.skills` is supported only with `judge.type: agent_judge`. These reusable rubric Skills install for the judge, not the run agent. Top-level `skills` are not automatically installed for the judge. Both `with_skill` and `without_skill` benchmark runs install judge Skills because they are scoring tools. Paths are resolved relative to the Skill root. Installation requires native Skill support in the selected Agent adapter; do not paste Skill contents into `criteria`.

## Execution environments

| Type | Use | Requirements |
| --- | --- | --- |
| `none` | Text I/O without a required sandbox | Fastest startup |
| `opensandbox` | Remote sandbox for files and commands | `OPENSANDBOX_API_KEY`; service settings in `environment.kwargs` or `OPENSANDBOX_BASE_URL` |
| `docker` | Local container isolation | Docker CLI and daemon; pull the image beforehand |

### OpenSandbox example

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

Common `kwargs` include `base_url`, `extensions` (a JSON string), `request_timeout_seconds`, and `file_transfer_parallelism`. Authentication comes from `OPENSANDBOX_API_KEY`.

### Docker example

```yaml
environment:
  type: docker
  image: node:22                    # Required; pull the image first
  workspace_mount: /workspace       # Default: /workspace
  env:
    NPM_CONFIG_REGISTRY: https://registry.npmmirror.com
  setup_steps:
    - run: npm install -g typescript
  entrypoint: ["sleep", "infinity"] # Default: sleep infinity
```

Docker CLI and daemon are required. `network_policy: deny_all` creates a container with `--network=none`; `allow_declared` is not yet supported.

## MCP

- `mode: real` installs a real MCP server for the agent.
- HTTP MCP may be configured inline or with `config_ref` pointing to `evals/fixtures/mcp/*.yaml`.
- Stdio MCP accepts `command` and `args`.
- Environment references use `${VAR}` or a whole-value `$VAR`; `required_env` injects variables into the agent environment.
- Evaluation-level `mcp` provides defaults. A case may declare `mcp.servers` (currently `mode: mocked` only), replacing a same-named server as a whole to swap mocked fixtures under the same server and tool names. `config_ref` remains relative to the Skill root.

## Engine and model

- `engine.model` is optional; if omitted, the engine chooses its local default model.
- CLI model IDs combine `provider` and `name`, such as `anthropic/claude-sonnet-4-6` or `openai/gpt-4`.
- `qodercli` usually needs no `model` configuration.

### `engine.kwargs`: engine-specific switches

`engine.kwargs` is a map of string keys and values. Each agent reads only keys it recognizes; unknown keys are ignored and reported at DEBUG level with `-v` (useful for typos such as `bypas_sandbox`). The repeatable CLI equivalent is `--engine-kwarg key=value` or `--ek key=value`. Priority: CLI option > `engine.kwargs` > default.

```yaml
engine:
  name: codex
  kwargs:
    bypass_sandbox: "true"
```

| Key | Agent | Behavior when true | Default or false |
| --- | --- | --- | --- |
| `bypass_sandbox` | `codex` | Forces `--dangerously-bypass-approvals-and-sandbox`, overriding the runtime-derived sandbox flag. Useful where the host kernel lacks Landlock support, such as some CI containers. | `none` uses `--sandbox workspace-write`; other runtimes already bypass the sandbox. |
| `bypass_sandbox` | `claude_code` | No-op; Claude already uses `--permission-mode=bypassPermissions`. | No-op. |
| `bypass_sandbox` | `qodercli` | No-op; qoder CLI has no corresponding flag. | No-op. |

## Common errors

- `opensandbox` without authentication or `base_url`: runtime failure.
- `engine.model` incompatible with the gateway: connection error.
- Missing `cases.files` path: validation failure.
- All relative paths are relative to the **Skill root**, the directory containing `SKILL.md`.
