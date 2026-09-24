# case.yaml field reference (skill-up)

Each `evals/cases/*.yaml` defines one evaluation case. The case ID is the filename without `.yaml`. These examples follow the built-in skill-up schema.

## Single-turn case

```yaml
id: find-null-bug
title: Identify a null dereference bug
description: Verify that the Skill detects a null dereference during code review

input:
  prompt: |
    Review the current diff and report findings.

context:
  repo_fixture: fixtures/repos/null-check-bug
  git:
    init: true
    checkout: main
    apply_diff: fixtures/diffs/null-check.patch

constraints:
  timeout_seconds: 180
  max_turns: 8

expect:
  must_contain: ["null", "bug"]
  must_not_contain: ["LGTM"]
  exit_code: 0

judge:
  type: rule_based
  success:
    - output_contains:
        all: ["null", "bug"]
    - exit_code: 0
```

## Multi-turn conversation

```yaml
input:
  turns:
    - role: user
      content: "sdd_bootstrap: task=implement user login"
      post_condition:
        must_contain_any: ["Research", "analysis"]
        on_fail: skip_remaining      # or fail
      capture:
        - variable: phase
          pattern: "(?P<value>Research|Implementation)"
    - role: user
      content: "Skip Research and write the code directly"
```

`post_condition` checks the response after each turn. `on_fail: skip_remaining` marks the remaining turns SKIP; `fail` fails the whole case.

`capture` extracts values from the response for `{{variable}}` substitution in later turns:

- Specify exactly one extractor: `pattern` (regular expression) or `jsonpath`.
- Prefer a named `(?P<value>...)` capture group.
- No match or an empty value puts the case in ERROR state.
- Captured values are scoped to this case execution.

### Per-turn judge assertions

```yaml
judge:
  type: rule_based
  success:
    - turn_response_contains:
        turn: 2
        contains_any: ["must complete", "cannot skip", "Research"]
    - turn_response_not_contains:
        turn: 2
        not_contains: ["LGTM"]
    - tool_called_in_turn:
        turn: 1
        name: write_file
    - tool_not_called_in_turn:
        turn: 2
        name: delete_file
```

Only turns with `status=completed` can be asserted. Referencing a missing or incomplete turn fails the assertion.

## context: initialize the workspace

```yaml
context:
  repo_fixture: fixtures/repos/my-project
  git:
    init: true
    checkout: feature-branch
    apply_diff: fixtures/diffs/my.patch
    remotes:
      - name: origin
        url: https://github.com/user/repo
  files:
    "src/main.py": |
      def hello():
          print("Hello World")
    "config.json": '{"debug": true}'
```

## expect: inexpensive gate checks

```yaml
expect:
  must_contain:
    - "review"
    - "bug"
  must_not_contain:
    - "LGTM"
    - "error"
  exit_code: 0
  files_exist:
    - "review.md"
    - "output.json"
  files_not_exist:
    - "temp.log"
  file_contains:
    - path: "review.md"
      content: "security"
  golden_file: "expected.txt"
```

If `expect` fails, the judge is skipped. Use it to reject clearly unsuitable results before spending judge tokens.

**Merging with `cases.defaults.expect`:**

- A case's `expect` merges with any defaults defined in `eval.yaml`, such as `exit_code: 0` or `must_not_contain: ["TODO"]`.
- List fields (`must_contain`, `must_not_contain`, `files_exist`, `files_not_exist`, `file_contains`) append and deduplicate. Case values override scalar fields (`exit_code`, `golden_file`).
- If a case omits `expect`, the defaults apply directly.

## Common patterns

- **Text-routing Skill:** `expect.must_contain` and `judge.rule_based.output_contains`.
- **MCP tool use:** `judge.rule_based.success[tool_called]`.
- **Semantic quality:** `judge.agent_judge.criteria`.
- **Complex structured checks:** `judge.script`, which can read `$EVAL_TRANSCRIPT_PATH` and other inputs.
