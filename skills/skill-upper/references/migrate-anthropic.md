# Migrate from Anthropic evals.json (skill-up)

A Skill created with Anthropic skill-creator may contain `evals/evals.json`. skill-up can run it directly or convert it once to native YAML.

## Option 1: Run directly with `--auto`

```bash
cd my-skill/
skill-up run --auto
skill-up run ./my-skill/ --auto
skill-up run --auto --engine codex
```

Use this for quick CI regression, validation across engines, and staying in sync with the Anthropic source file.

Limitations:

- Multi-turn conversations are unsupported.
- `expectations` often become `agent_judge.criteria`, which consumes judge tokens.
- Advanced OpenSandbox, MCP, or strict `expect` gates are easier to add after converting to YAML.

## Option 2: Convert to native YAML with `import`

```bash
skill-up import ./evals/evals.json
skill-up import ./evals/evals.json --output ./evals-v2
```

The generated `eval.yaml` and `cases/*.yaml` can then be edited to add deterministic `expect` gates, use `rule_based` or `script` judges, configure multi-turn `turns`, or add `environment.type: opensandbox` and MCP.

## Comparison

| | `--auto` | `import` |
| --- | --- | --- |
| Operation | Read at run time | Write YAML once |
| Updates | Follow `evals.json` changes | Maintain YAML independently |
| Customization | Limited by JSON | Fully editable |

A practical sequence is to run `--auto`, import cases that need richer checks, and maintain their YAML separately.

## Field mapping (summary)

| evals.json | skill-up YAML |
| --- | --- |
| `prompt` | `input.prompt` |
| `expectations` | `judge.agent_judge.criteria` by default |
| `expected_output` | Often `description` |
| `files` | `context.files`, etc. |

## Suggested workflow

1. Run `skill-up run --auto` to verify the source evaluation works.
2. Run `skill-up import ./evals/evals.json --output ./evals-native`.
3. Edit YAML to add `expect`, `rule_based`, OpenSandbox, or MCP as needed.
4. Run `skill-up run ./evals-native/eval.yaml`.

This allows Anthropic-side Skill iteration with `--auto` in CI, then native YAML for deeper long-term scenarios.
