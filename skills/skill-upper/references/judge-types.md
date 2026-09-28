# Choose and configure a judge (skill-up)

skill-up evaluates in two stages: `expect` (inexpensive gate checks) and `judge` (quality evaluation). A failed `expect` skips the judge, so use it for straightforward checks first. Each case selects exactly one judge type.

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
        name: "github::create_pull_request"
        args:
          title: "Fix null check"
  failure:
    - output_contains:
        any: ["no changes needed", "code is correct"]
```

`failure` has priority: any matching failure assertion immediately fails the case. Otherwise, every `success` assertion must hold.

Supported matchers: `output_contains`, `output_matches` (Go regexp with `all`, `any`, and `not`), `exit_code`, `tool_called`, `files_exist`, and `files_not_exist`.

## agent_judge: LLM evaluation

```yaml
judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
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

- This consumes additional tokens and takes longer.
- Make criteria specific and verifiable. Put deterministic checks in `expect` or `rule_based` where possible.
- Put long or reusable domain rubrics in `judge.skills`. These Skills install only for the judge agent, never the agent under test. Installation requires native Skill support in the chosen Agent adapter; their contents are not appended to the prompt as a fallback.
- `judge.skills[].include` and `exclude` use the same doublestar glob rules as top-level `skills`, relative to `path`, with `exclude` taking priority.

## script: custom check

```yaml
judge:
  type: script
  script_path: evals/fixtures/scripts/check-quality.sh
  timeout_seconds: 30
```

The script runs from the case workspace root. Exit code `0` means PASS; any other code means FAIL. Available environment variables include `$EVAL_FINAL_MESSAGE`, `$EVAL_EXIT_CODE`, and `$EVAL_TRANSCRIPT_PATH` when available.

## Relative cost

| Check | Cost | Use |
| --- | --- | --- |
| `expect` | None | First gate for simple checks |
| `rule_based` | Very low | Default judge |
| `script` | Low, depending on script | Custom checks |
| `agent_judge` | High | Semantic judgment |
