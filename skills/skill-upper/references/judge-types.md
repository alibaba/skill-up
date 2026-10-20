# Choose and configure a judge (skill-up)

skill-up evaluates in two stages: `expect` (inexpensive gate checks) and `judge` or `judges` (quality evaluation). A failed `expect` skips all judges, so use it for straightforward checks first. Use a singular `judge` for one strategy or `judges` for several independent scores from the same agent run.

## Choose a judge

1. Can keywords, files, exit codes, or tool calls determine success? Use **rule_based**.
2. Does structured output need a custom check? Use **script**.
3. Does the result require semantic judgment? Use **agent_judge**, which has the highest cost.

## rule_based: deterministic rules

```yaml
judge:
  type: rule_based
  success:
    - output_contains:
        all: ["bug", "null"]
        any: ["suggest a fix", "recommend a change"]
        not: ["LGTM"]
    - output_matches:
        all: ["(?m)^## Status$", "(?m)^## Evidence$"]
        any: ["(?i)pass", "(?i)success"]
        not: ["(?i)api[_-]?key\\s*="]
    - exit_code: 0
    - tool_called:
        name: "mcp__github__create_pull_request"
        args:
          title: "Fix null check"
  failure:
    - output_contains:
        any: ["no changes needed", "code is correct"]
```

`failure` has priority: any matching failure assertion immediately fails the case. Otherwise, every `success` assertion must hold.

`tool_called` matches `name` exactly. Claude Code, Codex, and Qoder CLI record MCP tools as `mcp__<server>__<tool>`, where `<server>` is the server `name` from `mcp.servers`.

Supported matchers: `output_contains`, `output_matches` (Go regexp with `all`, `any`, and `not`), `exit_code`, `tool_called`, `files_exist`, and `files_not_exist`.

## agent_judge: LLM evaluation

```yaml
judge:
  type: agent_judge
  model: dashscope/qwen3.8-max
  skills:
    - source: local_path
      path: evals/fixtures/judge-rubric
      include: [SKILL.md, "references/**"]
      exclude: ["references/drafts/**"]
  criteria:
    - "Identify a real bug and follow the judge-rubric scoring rules"
    - "Do not report correct code as a bug"
    - "Provide actionable recommendations rather than generic advice"
  pass_threshold: 0.7
```

- This model example requires the OpenCode/DashScope engine and endpoint configuration in [eval-yaml.md](eval-yaml.md). Legacy `judge.model` uses the eval engine with provider-scoped connection resolution.
- This consumes additional tokens and takes longer.
- Make criteria specific and verifiable. Put deterministic checks in `expect` or `rule_based` where possible.
- Put long or reusable domain rubrics in `judge.skills`. These Skills install only for the judge agent, never the agent under test. Installation requires native Skill support in the chosen Agent adapter; their contents are not appended to the prompt as a fallback.
- `judge.skills[].include` and `exclude` use the same doublestar glob rules as top-level `skills`, relative to `path`, with `exclude` taking priority.

For an independent agent, use `judge.engine` instead of `judge.model`:

```yaml
judge:
  type: agent_judge
  engine:
    name: claude_code
    model:
      provider: anthropic
      name: claude-sonnet-4-6
  criteria: ["The result satisfies the task"]
```

The block uses the run engine schema, with required `name`, `model.provider`, and `model.name`. It does not inherit runner model, kwargs, version, custom config, or connection. Resolve credentials by its provider/protocol; never infer an engine from the provider. Case-level engine/model selections replace the eval selection. In `none`, preinstall the judge CLI. Docker/OpenSandbox install and check it during judging. Multiple judges reuse the same case runtime sequentially with fresh sessions and shared filesystem state.

## script: custom check

```yaml
judge:
  type: script
  script_path: evals/fixtures/scripts/check-quality.sh
  timeout_seconds: 30
```

The script runs from the case workspace root. Exit code `0` means PASS; any other code means FAIL. Available environment variables include `$EVAL_FINAL_MESSAGE`, `$EVAL_EXIT_CODE`, and `$EVAL_TRANSCRIPT_PATH` when available.

## judges: combine reusable checks and semantic review

```yaml
judges:
  - id: functional
    type: script
    script_path: evals/fixtures/scripts/check-quality.sh
  - id: semantic
    type: agent_judge
    model: dashscope/qwen3.8-max
    criteria:
      - "The result satisfies the user intent"
```

Judges evaluate one agent run sequentially in the same runtime (`none`, Docker or OpenSandbox). Every Agent judge starts a fresh session. Filesystem changes are shared and visible to later judges; use read-only checks when unchanged output is required. No snapshot API is needed. The case passes only if every judge passes. `evaluation.json` contains grouped results, and `grading.json` reports one top-level assertion per judge. Artifacts and verdicts remain separate by member ID.

## Relative cost

| Check | Cost | Use |
| --- | --- | --- |
| `expect` | None | First gate for simple checks |
| `rule_based` | Very low | Default judge |
| `script` | Low, depending on script | Custom checks |
| `agent_judge` | High | Semantic judgment |
