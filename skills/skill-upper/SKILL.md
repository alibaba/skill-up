---
name: skill-upper
description: "Capture and review Agent Skill observations, and create, run, diagnose, or iteratively improve Skill evaluations (evals) with the skill-up CLI. Use when the user asks to record explicitly attributed Skill usage or feedback; review observations or user-supplied traces/transcripts; turn an approved observation into a regression case; evaluate, test, regress, verify, fix, improve, iterate, or evolve a Skill; add or strengthen eval cases; write eval.yaml/case.yaml; run skill-up run/validate/list-cases/report/import/init; or migrate from Anthropic evals.json. Plugin observation capture/review require either the Codex skill-up plugin or an observer-enabled DSH skill-up plugin. User-supplied external evidence can be reviewed without a plugin; evaluation remains multi-engine."
---

# use-skill-up-cli

Help the user evaluate and evolve Agent Skills through the `skill-up` CLI.

Manual: <https://alibaba.github.io/skill-up/>

## Language Policy

**Default to English when responding to the user. If the user writes in Chinese (or any other language), switch to that language and stay consistent with the user's input throughout the session.**

Detection rules (highest priority first):

1. The user explicitly specifies a language in the current message (e.g. "answer in English" or "reply in Chinese") → follow the user's instruction.
2. The natural language used in the user's current message → match it.
3. None of the above → use English (default).

Regardless of the response language, technical identifiers in this SKILL — CLI commands, `eval.yaml` / `case.yaml` field names, report field names, etc. — MUST stay in their original English form. Do not translate them.

### Language Rules for Generated Artifacts

Use the user's current language for replies, newly authored explanatory prose,
and YAML comments. Preserve the language and semantics of test inputs, expected
output literals, identifiers, commands, paths, and existing regression cases.
Do not translate a deterministic assertion merely because the conversation
language changed. When testing localized behavior, keep the original business
text even in an English conversation.

Treat `assets/*.tmpl` as structural references: adapt placeholder prose and
comments to the requested language. Review only files created or changed for
this task; never rewrite unrelated cases to enforce a language preference.

Use short field-leading comments for generated YAML. Explain actual configured
fields: `schema_version`, `environment.type`, `engine.name`, `cases.files`, and
`report.formats` in eval.yaml; `id`, `title`, `input.prompt`, and `judge.type` in
case YAML. For nested fields, place the comment inside the parent mapping
immediately before the child field, rather than only before the parent.
Comment `expect` and a top-level `judge` when present; do not add an
optional field just to satisfy a comment check. Keep field names and enum values
unchanged. `skill-up import` does not preserve template comments.

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
- The user asks to "fix / improve / iterate / evolve this skill" from eval failures.
- The user wants to "add evals, test cases, or regression cases to a skill".
- The user asks to record explicitly attributed Skill usage, evidence, or feedback.
- The user wants to review captured Skill observations or turn approved feedback into a regression case.
- The user wants to edit `eval.yaml` / `case.yaml`, or asks you to choose an appropriate `judge` type.
- The user mentions `skill-up run/validate/list-cases/report/import/init`.
- The user wants to migrate from Anthropic `evals.json` to skill-up.
- The current working directory contains `evals/eval.yaml` or `evals/evals.json` and the user wants to run it.

## Choose the requested workflow

- **Diagnose or plan:** read the target Skill and supplied evidence, identify
  concrete problems, and propose changes or cases. Do not install tools, create
  eval files, change configuration, or run evaluations unless requested.
- **Create or edit cases:** use Steps 1–4 to create or update the suite and
  validate its configuration. Stop before credentials and execution unless the
  user also requests a run. Report the actual config/case paths and validation
  status in the final reply.
- **Evaluate:** follow Steps 0–7 for the requested suite; report failures without
  automatically editing the Skill.
- **Improve:** follow the evaluation workflow and Step 8 within the requested
  scope. Preserve valid existing cases and assertions.

## Evaluation workflow

### Observation capture mode (optional)

Use this mode when the user asks to record the current Skill interaction or
feedback and observation tools are available. It requires either a current
Codex release with the `codex-skill-up` plugin and lifecycle-hook support, or DSH with
the skill-up plugin's observer explicitly enabled. Do not retrofit observation
capture onto the pinned Codex 0.80.0 evaluation adapter.

If the user asks how to install either host plugin, or the required observation
tools are missing and the user asks to enable them, read
`references/install.md` under "Install an observation host plugin (optional)". Do not
install or enable a plugin unless the user asks.

Claude Code, qodercli, Qwen Code, and other Agent Engines are not supported for
this observation workflow. This restriction does not apply to normal `skill-up`
evaluation runs, which remain multi-engine.

1. Attribute the interaction only when the user explicitly references a Skill
   (for example, `$my-skill`) or the responsible Skill is otherwise known with
   certainty.
2. In Codex, if explicit attribution is absent but certain, call
   `mark_skill_invocation` once with the Skill name and optional version. DSH
   derives exact attribution from its durable `/skill` injection or successful
   `skill` tool result; do not add inferred attribution.
3. In Codex, use `attach_skill_evidence` only for references that help reproduce or
   evaluate the behavior. Prefer paths and short summaries over copied file
   contents.
4. In Codex, use `record_skill_feedback` during the observed turn. In DSH,
   the observer stores the next user turn in the same session as an unclassified
   follow-up candidate. For explicit feedback, use `record_observation_feedback`
   with the completed observation's ID. Never invent sentiment or comments.

Both adapters redact common credential shapes before local persistence, but still
avoid sending secrets to marker tools. If the request is capture-only, stop
after recording it; do not edit, evaluate, commit, or publish a Skill unless
the user also asks for that work.

### Observation input mode (optional)

When the request starts from a captured Skill observation, read
`references/observations.md` first. Use the available observation tools to
inspect and preview the observation, require explicit approval for the exact
candidate, write it only after approval, and then continue at Step 4. If the
request does not start from an observation, use the normal flow below.

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

More details, including optional Codex and DSH observation host plugins:
`references/install.md`.

### Step 0.5 (optional): User config and telemetry

For OTLP defaults, `runtime_kwargs` (e.g. OpenSandbox `base_url`), etc.:

```bash
skill-up init
skill-up init --local
skill-up init --print
skill-up init --force
```

Precedence (low → high): embedded empty defaults < user config < project `.skill-up.yaml` < `--config`. `SKILL_UP_CONFIG` can point at the user config file (env var name is historical). See the upstream README "User config".

### Step 1: Locate the target Skill

1. Identify the root directory of the target Skill (the directory containing `SKILL.md`). Search in this priority: user path → nearest `SKILL.md` upward from CWD → recently viewed files.
2. Read the target `SKILL.md` for scope, triggers, and dependencies. Preserve localized behavior and expected literals when designing cases.
3. Check `evals/`:
   - `evals/eval.yaml` exists → Step 4 (optionally Step 3).
   - Only `evals/evals.json` → `references/migrate-anthropic.md` (`skill-up run --auto` or `skill-up import`).
   - Nothing → Step 2.

### Step 2: Scaffold the evals (only when none exist)

- Copy `assets/eval.yaml.tmpl` to `<skill-root>/evals/eval.yaml`.
- Copy `assets/case.yaml.tmpl` to `<skill-root>/evals/cases/<case-id>.yaml`.

Adapt language per "Language Rules for Generated Artifacts". Replace the templates' generic English placeholders with case-specific prose in the requested output language. Preserve short field-leading comments in generated YAML; translate them when the output language differs from English, while keeping field names and enum values in English.

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

For a case-authoring request, finish here with the config and case paths plus
validation status. Do not start a background evaluation as part of scaffolding.

### Step 5: Prepare credentials

Check authentication for the selected engine and provider. Provider API keys
resolve from `--api-key`, environment variables, or `~/.skill-up/credentials.yaml`;
engine-specific tokens and saved login sessions follow the engine's auth rules.
Never dump environment variables or credential files, or print secret values
into tool output, logs, or the conversation. Check only whether a relevant
variable has a non-empty value. For example, for an OpenAI API-key workflow:

```bash
if printenv OPENAI_API_KEY | grep -q .; then
  printf '%s\n' 'OPENAI_API_KEY: configured'
else
  printf '%s\n' 'OPENAI_API_KEY: not configured in the environment'
fi
```

An absent environment variable does not rule out file-based credentials or an
existing engine login. If the selected authentication path is unavailable,
**stop and ask** the user to configure it locally; do not ask them to paste a
secret into the conversation or write secrets into YAML without consent.

For `opensandbox`, also ensure `OPENSANDBOX_API_KEY` (and related env) as needed.

### Step 6: Run the evaluation

```bash
skill-up run <skill-root>/evals/eval.yaml
```

| Scenario                         | Command                               |
| -------------------------------- | ------------------------------------- |
| Subset                           | `--include-case-name "basic-*"`       |
| Exclude                          | `--exclude-case-name "*-flaky"`       |
| HTML report                      | `--format html`                       |
| Engine override                  | `--engine codex --model openai/gpt-4` |
| Parallelism                      | `--parallelism 4` (1–256)             |
| Anthropic JSON                   | `--auto`                              |
| Stability/flakiness sampling     | `--iteration 3`                       |
| Auto-append after last iteration | `--iteration 0` (default behavior)    |
| Verbose                          | `-v`, `-vv`                           |

Exit `0` = all passed; `1` = failure or error — suitable for CI. When
an explicit positive `--iteration N` runs more than one sample, inspect the
terminal's simple current-command summary for lines like
`case_a: 3 trials, 2 PASS, 1 FAIL -> flaky`.

### Step 7: Interpret the report

Artifacts under `<skill-root>/<skill-name>-workspace/iteration-N/`:

- `result.json`, `benchmark.json`, optional `report.html`
- `<case-id>/with_skill/grading.json`, `outputs/`

Summarize: pass rate and timing; for failures, case id, assertion `text`, and `evidence`; benchmark deltas if enabled; offer HTML path or `skill-up report result.json --format html`.

### Step 8: Evolve the Skill when requested

Only enter this loop when the user asks to fix, improve, iterate, or evolve the
target Skill. If the user only asks to evaluate or report results, stop after
Step 7 without modifying it.

1. Diagnose failures from `result.json`, `grading.json`, and output evidence.
2. Fix `SKILL.md` or supporting files when the Skill behavior is incorrect.
3. Add or refine eval cases when coverage is missing.
4. Do not weaken valid assertions merely to make a failure pass.
5. Rerun failed cases first, then run the full eval suite.
6. Continue until the evals pass or clearly report what remains blocked.

## Command quick reference

| Command                                       | Purpose                           |
| --------------------------------------------- | --------------------------------- |
| `skill-up validate <eval.yaml>`               | Validate before `run`.            |
| `skill-up list-cases <eval.yaml>`             | List cases.                       |
| `skill-up run [eval.yaml]`                    | Run evals.                        |
| `skill-up run --auto`                         | Run from `evals/evals.json`.      |
| `skill-up report <result.json> --format html` | Re-render reports.                |
| `skill-up import <evals.json>`                | Convert Anthropic format to YAML. |
| `skill-up init`                               | Write user-config template.       |
| `skill-up debug judge <input.json>`           | Debug judge.                      |
| `skill-up debug report <input.json>`          | Debug report.                     |

Full flags: `references/cli.md`.

## Common pitfalls

- Model IDs vs proxy aliases — preserve what works for the user's `base_url`.
- `opensandbox` without `OPENSANDBOX_API_KEY` — auth failures.
- Match the language of deterministic assertions to the behavior under test, not merely to the conversation language.
- Abusing `agent_judge`.
- Anthropic `evals.json` expectations → default `agent_judge`; use `import` + hand edits for deterministic checks.
- Paths relative to Skill root (`SKILL.md` directory).
- `--iteration 0` appends one run after the latest existing iteration without
  summarizing history; positive `--iteration N` runs N samples of the selected
  cases and, when N > 1, prints a simple stability/flakiness summary covering
  only samples from the current command.

## References

- `references/install.md`
- `references/eval-yaml.md`
- `references/case-yaml.md`
- `references/judge-types.md`
- `references/cli.md`
- `references/migrate-anthropic.md`
- `references/observations.md`
- `assets/eval.yaml.tmpl`, `assets/case.yaml.tmpl`
