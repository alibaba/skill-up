---
name: skill-upper
description: 'Set up, run, and interpret Agent Skill evaluations (evals) with the skill-up CLI / 使用 skill-up CLI 给 Agent Skill 搭建和运行评测. Use when the user asks to evaluate, test, regress, or verify a Skill; add evals or cases; write eval.yaml/case.yaml; run skill-up run/validate/list-cases/report/import/init; or migrate from Anthropic evals.json. This file is the authoritative workflow for English-context tasks. Handles Skill discovery, evals scaffolding, judge authoring, credentials, user config, validation, runs, and reports. English prompts require English replies and case YAML with no Chinese/CJK prose or comments.'
---

# use-skill-up-cli

Help the user set up, run, and interpret evaluations for Agent Skills via the `skill-up` CLI.

Manual: <https://alibaba.github.io/skill-up/>

> Chinese: [`SKILL.zh.md`](./SKILL.zh.md).

## English Context Authority

Use this file as the authoritative workflow whenever the user's current message is primarily English or explicitly asks for English output. Do not derive English behavior from the Chinese `SKILL.md`; follow the instructions in this file for locating Skills, scaffolding evals, writing cases, selecting judges, validating, running, and summarizing results.

## Language Policy

**Default to English when responding to the user. If the user writes in Chinese (or any other language), switch to that language and stay consistent with the user's input throughout the session.**

Detection rules (highest priority first):

1. The user explicitly specifies a language in the current message (e.g. "answer in English" / "用中文回答") → follow the user's instruction.
2. The natural language used in the user's current message → match it.
3. None of the above → use English (default).

Regardless of the response language, technical identifiers in this SKILL — CLI commands, `eval.yaml` / `case.yaml` field names, report field names, etc. — MUST stay in their original English form. Do not translate them.

### Language Rules for Generated Artifacts

When creating or editing `eval.yaml`, `case.yaml`, grading scripts, README snippets, final replies, or any other user-visible artifact, treat the language of the user's current message as the output language for this turn:

- If the user asks in Chinese, write the final response and all generated natural-language content in Chinese, including YAML comments, `title`, `description`, `input.prompt`, `expect` keywords, and `judge.criteria`.
- If the user asks in English, write the final response and all generated natural-language content in English, including YAML comments, `title`, `description`, `input.prompt`, `expect` keywords, and `judge.criteria`; do not leave Chinese or CJK characters in generated case files.
- If the target Skill itself is written in Chinese but the user asks in English, translate the Skill's functional intent into English test prompts and assertions instead of copying Chinese prose from the target Skill or templates.
- In an English context, deterministic keywords in `rule_based` cases, including `expect.must_contain` and `judge.success.output_contains`, must also be English keywords. Translate terms such as `资源泄漏`, `关闭`, and `异常处理` into `resource leak`, `close`, and `exception handling`; do not write bilingual parentheticals like `"资源" (resources)`.
- Keep technical identifiers unchanged, such as `schema_version`, `environment.type`, `engine.name`, `rule_based`, `agent_judge`, `script_path`, file paths, and commands.
- Treat `assets/*.tmpl` as structural references only. Rewrite placeholder prose and comments into the current output language; in an English context, translate or remove every Chinese comment and Chinese placeholder before writing generated files.
- In an English context, after generating all files but BEFORE submitting the final reply, you **MUST perform a CJK self-check**: open every `evals/cases/*.yaml` and `evals/eval.yaml` and scan for CJK characters (Unicode ranges `\u4e00-\u9fff\u3400-\u4dbf\uf900-\ufaff\u3000-\u303f\uff00-\uffef`), including but not limited to `title`, `description`, `input.prompt`, `expect` keywords, `judge.criteria`, and YAML comments. If any CJK character is found, **replace it with an equivalent English expression before finishing the task**. This step is mandatory and must not be skipped.

## What is skill-up

`skill-up` is an evaluation CLI for Agent Skill authors. It installs the Skill into a real Agent Engine (Claude Code, Codex, qodercli, etc.), spins up an execution environment for each case, runs the prompt, then grades the result via declared rules / LLM judges / custom scripts, and finally produces a report.

Typical layout:

```
my-skill/
  SKILL.md
  evals/
    eval.yaml
    cases/
      <case-id>.yaml
    fixtures/
```

## When to trigger

Use this skill in any of the following situations:

- The user asks to "run / evaluate / verify / test this skill".
- The user wants to "add evals, test cases, or regression cases to a skill".
- The user wants to edit `eval.yaml` / `case.yaml`, or asks you to choose an appropriate `judge` type.
- The user mentions `skill-up run/validate/list-cases/report/import/init`.
- The user wants to migrate from Anthropic `evals.json` to skill-up.
- The current working directory contains `evals/eval.yaml` or `evals/evals.json` and the user wants to run it.

## Main flow (follow this order strictly)

### Step 0: Make sure skill-up is installed

Before doing anything, verify `skill-up` is available:

```bash
command -v skill-up && skill-up --version
```

If a version is printed, continue. If you see `command not found`, on **macOS / Linux**:

```bash
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash

export SKILL_UP_VERSION=v0.1.0
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash

export INSTALL_DIR="$HOME/bin"
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash
```

> **Platform:** `skill-up` currently supports **macOS / Linux** only; Windows is not supported.

After installing, run `skill-up --version` again. If the command is still missing, add `~/.local/bin` to `PATH`.

More details: `references/install.md`.

### Step 0.5 (optional): User config and telemetry

For OTLP defaults, `runtime_kwargs` (e.g. OpenSandbox `base_url`), etc.:

```bash
skill-up init
skill-up init --local
skill-up init --print
skill-up init --force
```

Precedence (low → high): embedded empty defaults < user config < project `.skill-up.yaml` < `--config`. `SKILL_EVAL_CONFIG` can point at the user config file (env var name is historical). See the upstream README "User config".

### Step 1: Locate the target Skill

1. Identify the root directory of the target Skill (the directory containing `SKILL.md`). Search in this priority: user path → nearest `SKILL.md` upward from CWD → recently viewed files.
2. Read the target `SKILL.md` for scope, triggers, and dependencies. If the Skill is Chinese but the user writes in English, translate capabilities into English for prompts and assertions.
3. Check `evals/`:
   - `evals/eval.yaml` exists → Step 4 (optionally Step 3).
   - Only `evals/evals.json` → `references/migrate-anthropic.md` (`skill-up run --auto` or `skill-up import`).
   - Nothing → Step 2.

### Step 2: Scaffold the evals (only when none exist)

- Copy `assets/eval.yaml.tmpl` to `<skill-root>/evals/eval.yaml`.
- Copy `assets/case.yaml.tmpl` to `<skill-root>/evals/cases/<case-id>.yaml`.

Adapt language per "Language Rules for Generated Artifacts". In an English context, it is **prohibited** to copy Chinese placeholder text from the templates into generated files — all prose must be rewritten in English. The Chinese in the templates is for structural reference only, not to be carried over.

Selection guidelines:

- `environment.type`: use `none` for pure-text Skills; use `opensandbox` when you need a remote sandbox (set `OPENSANDBOX_API_KEY`, put non-secrets in `environment.kwargs`).
- `engine.name` + `engine.model`: default `claude_code`; `model` is optional. For `qodercli`, often omit `model`.
- `judge.type`: `rule_based` (preferred), `script`, `agent_judge` (expensive) — see `references/judge-types.md`.
- Case ID = filename without `.yaml`; prompts should exercise real Skill value.

See `references/eval-yaml.md` and `references/case-yaml.md`.

### Step 3: Fill the gaps (when evals already exist)

- `skill-up list-cases <path>`
- Review `eval.yaml` and representative cases; avoid `agent_judge` abuse.
- Add or edit YAML under `cases/` as needed.

### Step 4: Validate the configuration

```bash
skill-up validate <skill-root>/evals/eval.yaml
```

Expect: `✓ eval.yaml is valid (loaded N case(s))`.

### Step 5: Prepare credentials

Priority: `--api-key` > env (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `QODER_PERSONAL_ACCESS_TOKEN`) > `~/.skill-up/credentials.yaml`.

```bash
printenv | grep -E 'ANTHROPIC_API_KEY|OPENAI_API_KEY|QODER_PERSONAL_ACCESS_TOKEN'
```

If missing, **stop and ask**; do not write secrets into YAML without consent.

For `opensandbox`, also ensure `OPENSANDBOX_API_KEY` (and related env) as needed.

### Step 6: Run the evaluation

```bash
skill-up run <skill-root>/evals/eval.yaml
```

| Scenario | Command |
|---|---|
| Subset | `--include-case-name "basic-*"` |
| Exclude | `--exclude-case-name "*-flaky"` |
| HTML report | `--format html` |
| Engine override | `--engine codex --model openai/gpt-4` |
| Parallelism | `--parallelism 4` (1–256) |
| Anthropic JSON | `--auto` |
| N rounds | `--iteration 3` |
| Auto-append after last iteration | `--iteration 0` (default behavior) |
| Verbose | `-v`, `-vv` |

Exit `0` = all passed; `1` = failure or error — suitable for CI.

### Step 7: Interpret the report

Artifacts under `<skill-root>/<skill-name>-workspace/iteration-N/`:

- `result.json`, `benchmark.json`, optional `report.html`
- `<case-id>/with_skill/grading.json`, `outputs/`

Summarize: pass rate and timing; for failures, case id, assertion `text`, and `evidence`; benchmark deltas if enabled; offer HTML path or `skill-up report result.json --format html`.

## Command quick reference

| Command | Purpose |
|---|---|
| `skill-up validate <eval.yaml>` | Validate before `run`. |
| `skill-up list-cases <eval.yaml>` | List cases. |
| `skill-up run [eval.yaml]` | Run evals. |
| `skill-up run --auto` | Run from `evals/evals.json`. |
| `skill-up report <result.json> --format html` | Re-render reports. |
| `skill-up import <evals.json>` | Convert Anthropic format to YAML. |
| `skill-up init` | Write user-config template. |
| `skill-up debug judge <input.json>` | Debug judge. |
| `skill-up debug report <input.json>` | Debug report. |

Full flags: `references/cli.md`.

## Common pitfalls

- Model IDs vs proxy aliases — preserve what works for the user's `base_url`.
- `opensandbox` without `OPENSANDBOX_API_KEY` — auth failures.
- Chinese `expect.must_contain` vs English model output — align language in prompts/assertions.
- Abusing `agent_judge`.
- Anthropic `evals.json` expectations → default `agent_judge`; use `import` + hand edits for deterministic checks.
- Paths relative to Skill root (`SKILL.md` directory).
- `--iteration 0` appends after the latest existing iteration; positive `--iteration N` runs N rounds.

## References

- `references/install.md`
- `references/eval-yaml.md`
- `references/case-yaml.md`
- `references/judge-types.md`
- `references/cli.md`
- `references/migrate-anthropic.md`
- `assets/eval.yaml.tmpl`, `assets/case.yaml.tmpl`
