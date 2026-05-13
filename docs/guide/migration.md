# Migrating from Anthropic

If you used Anthropic's skill-creator to build your Skill, you already have an `evals/evals.json` file. skill-up consumes it directly — no rewrite required.

---

## Two onboarding paths

### Path 1 — consume `evals.json` directly (fastest)

`--auto` mode auto-detects `evals/evals.json` and runs it inline, **without producing any intermediate files**:

```bash
# Inside your Skill directory
cd my-skill/
skill-up run --auto

# Or with an explicit directory
skill-up run ./my-skill/ --auto

# Run the same suite against a different engine
skill-up run --auto --engine codex
```

**When to use it:**

- Quickly wire your Skill into CI for regression testing
- Validate the same suite against Codex or other engines
- Stay in sync with the Anthropic workflow — updates to `evals.json` are picked up automatically on the next run

### Path 2 — convert to native YAML (deep customization)

The `import` command transforms `evals.json` into skill-up's YAML format:

```bash
skill-up import ./evals/evals.json
```

After conversion you get:

```text
evals/
  eval.yaml                # Entrypoint config (review and adjust)
  cases/
    case-1.yaml            # One file per evals.json entry
    case-2.yaml
    case-3.yaml
```

Once in native form you can:

- Add `expect` gating checks (deterministic verification)
- Replace pure LLM grading with `rule_based`
- Configure MCP tool-call assertions

**When to use it:**

- You need skill-up–only capabilities (structured assertions, MCP tool-call assertions, …)
- You want to fine-tune grading logic
- You no longer need to stay in lock-step with `evals.json`

---

## Comparison

|                    | `--auto` mode                                    | `import` conversion                       |
| :----------------: | :----------------------------------------------: | :---------------------------------------: |
| **Operation**      | Zero config; consumed at runtime                  | One-time conversion; YAML thereafter       |
| **Sync**           | Updates to `evals.json` apply automatically       | Independent maintenance after conversion   |
| **Customization**  | Limited to what `evals.json` already expresses     | Full freedom                              |
| **Default judge**  | `agent_judge` (because `expectations` are NL)     | Switch to `rule_based` or `script` freely |
| **Typical user**   | Fast onboarding, CI regression                    | Long-term maintenance, deep grading       |

> The two paths are complementary. Start with `--auto` to validate quickly, then `import` the cases that need deep customization.

---

## `evals.json` format

Anthropic's `evals.json` looks like:

```json
{
  "skill_name": "my-skill",
  "evals": [
    {
      "id": 1,
      "prompt": "Help me create a release plan",
      "expected_output": "Should call the create_plan tool",
      "files": [],
      "expectations": [
        "Calls create_plan correctly",
        "The arguments include the plan name"
      ]
    }
  ]
}
```

Conversion mapping:

| evals.json field   | skill-up equivalent                                |
| :----------------: | :------------------------------------------------: |
| `prompt`           | `input.prompt`                                     |
| `expectations`     | `judge.criteria` (agent_judge rubric)               |
| `expected_output`  | The case `description`                             |
| `files`            | `context.files`                                    |

---

## Recommended migration path

```text
1. Get CI green with --auto
   skill-up run --auto

2. Once that is stable, import the cases that need customization
   skill-up import ./evals/evals.json --output ./evals-v2

3. Edit the converted YAML — add expect gating and rule_based rules

4. Run the native config
   skill-up run ./evals-v2/eval.yaml
```

The recommended user journey: **build and iterate the Skill with Anthropic skill-creator → onboard CI with `skill-up run --auto` → use `skill-up import` to switch to native YAML when deeper customization is needed.**
