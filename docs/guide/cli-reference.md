# CLI Reference

skill-up provides the following commands, covering the full evaluation lifecycle: validate → run → list cases → generate reports → import legacy formats.

---

## skill-up run

Run evaluation cases and produce reports.

```bash
skill-up run [path] [flags]
```

### Argument

| Argument | Description                                                           |
| :------: | :-------------------------------------------------------------------: |
| `path`   | Path to `eval.yaml`. Defaults to `evals/eval.yaml` in the current dir |

### Flags

| Flag                   | Default                       | Description                                                                                                                                                |
| :--------------------: | :---------------------------: | :--------------------------------------------------------------------------------------------------------------------------------------------------------: |
| `--auto`               | `false`                       | Auto-detect the `evals/` directory; can directly consume an Anthropic `evals.json`                                                                          |
| `--include-case-name`  | —                             | Run only matching cases (glob; can be repeated)                                                                                                            |
| `--exclude-case-name`  | —                             | Exclude matching cases (glob; can be repeated)                                                                                                             |
| `--format`             | —                             | Extra report formats: `junit` / `html` (repeatable). `result.json` is always written; `--format junit` produces `report.xml`, `--format html` produces `report.html`; `--format json` is a no-op |
| `--output-dir`         | `<skill-name>-workspace/` next to the skill dir | Output directory for reports and artifacts                                                                                                 |
| `--workspace`          | —                             | Reuse an existing host directory as the agent workspace. Supported only with `environment.type: none`, `cases.parallelism: 1`, and benchmark disabled. The agent can modify this directory, but skill-up never deletes it. |
| `--iteration`          | `0` (auto)                    | Repeat selected cases for stability/flakiness sampling. `0` auto-appends one run after the latest `iteration-N/` without summarizing history; positive `N` runs N samples and writes `iteration-1/` … `iteration-N/`; when `N > 1`, the terminal summary covers only samples from the current command |
| `--engine`             | From config                   | Override engine name                                                                                                                                       |
| `--provider`           | From config                   | Override `engine.model.provider`. When set, the complete `--model` value is preserved as an opaque model ID.                                               |
| `--model`              | From config                   | Override model. Without `--provider`, the legacy `provider/name` form is split when the prefix is configured; otherwise the complete value is preserved.   |
| `--parallelism`        | From config                   | Override `cases.parallelism`. Allowed range: 1–256                                                                                                          |
| `--baseline`           | From config                   | Override `benchmark.enabled` to `true` for this run                                                                                                         |
| `--api-key`            | —                             | Pass an API key (higher precedence than env vars)                                                                                                          |
| `--no-delete`          | `false`                       | Preserve workspaces or containers created by skill-up after evaluation for debugging.                                                                      |
| `--event-log`          | —                             | Write an ordered v1 JSONL evaluation event stream to a local file. The parent directory must already exist                                                  |
| `--event-attribute`    | —                             | Attach a namespaced `key=value` string attribute to every event (repeatable; requires `--event-log`)                                                        |
| `-v, --verbose`        | `0`                           | Increase log verbosity. Default `info`; `-v` / `--verbose` / `--verbose=true` → `debug`; `-vv` / `--verbose=2` → `trace`; `--verbose=false` disables extra detail |

> **Validation scope.** `run` validates the eval-level config plus **only the
> cases selected** after `--include-case-name` / `--exclude-case-name` filters.
> An invalid case that is filtered out does not block the run — so a shared
> eval can hold both a quick `smoke` subset and heavier cases without the latter
> blocking a filtered smoke run. Use [`skill-up validate`](#skill-up-validate)
> to validate the **whole** suite (every case) regardless of filters.

> **Existing workspace state.** `--workspace` deliberately reuses the same
> directory for every selected case, retry, and iteration. Runs are serialized,
> but configured setup/fixtures/skill installation and agent changes carry
> forward. The report `--output-dir` and `--event-log` must be outside this
> directory. Select one case and one iteration, with retries disabled, when each
> result must start from exactly the same state.

### Examples

```bash
# Run all cases
skill-up run ./evals/eval.yaml

# Run a subset
skill-up run ./evals/eval.yaml --include-case-name "basic-*"

# Evaluate an agent in an already prepared local workspace
skill-up run ./evals/eval.yaml --workspace /path/to/project --parallelism 1 --no-delete

# Exclude cases
skill-up run ./evals/eval.yaml --exclude-case-name "*-old" --exclude-case-name "*-deprecated"

# Override engine and model
skill-up run ./evals/eval.yaml --engine codex --model openai/gpt-4

# Preserve an opaque upstream model ID when its prefix is not a configured provider
skill-up run ./evals/eval.yaml --engine claude_code --model anthropic_modelscope/deepseek-v4-pro

# Preserve a slashed model ID unconditionally with a configured provider
export GATEWAY_API_KEY=your-key
export GATEWAY_BASE_URL=https://gateway.example.com/openai/v1
skill-up run ./evals/eval.yaml --engine codex --provider gateway --model team/model-v2

# Temporarily override case parallelism
skill-up run ./evals/eval.yaml --parallelism 4

# Run with baseline comparison
skill-up run ./evals/eval.yaml --baseline

# Multiple report formats
skill-up run ./evals/eval.yaml --format json --format html --format junit

# Three samples in this command, one folder per run, plus a simple flaky summary
skill-up run ./evals/eval.yaml --iteration 3

# Produce a live event stream that a CI process can tail while the run is active
skill-up run ./evals/eval.yaml \
  --event-log ./events.jsonl \
  --event-attribute com.example.ci.build_id=build-123

# Auto-detect mode (consumes Anthropic evals.json directly)
skill-up run --auto
skill-up run --auto --engine codex
skill-up run ./my-skill/ --auto
```

`--event-log` creates or truncates one file for the invocation. It cannot be
used with `--dry-run`, and the file must not be placed inside a scheduled
`iteration-N/` output directory because that directory is recreated during the
run. Consumers should read only newline-terminated records; the final record of
a gracefully completed stream carries `last_event: true`.

### Exit codes

| Exit code | Meaning                       |
| :-------: | :---------------------------: |
| `0`       | All cases passed              |
| `1`       | At least one case failed/errored |

> Use the exit code in CI to determine whether the evaluation succeeded.

### OTLP trace export

Telemetry is disabled by default. After standard OpenTelemetry env vars are set, `skill-up run` exports run traces over OTLP and decorates verbose slog logs with `trace_id` / `span_id`:

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
export OTEL_METRICS_EXPORTER=otlp
export OTEL_RESOURCE_ATTRIBUTES=deployment.environment=local,service.namespace=skill-up
skill-up run ./evals/eval.yaml -v
```

You can also use trace-specific overrides like `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` and `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL`. Currently `grpc` and `http/protobuf` are supported.

`OTEL_METRICS_EXPORTER=otlp` enables low-cardinality metrics (counters and durations for run/case/runtime exec). `OTEL_METRICS_EXPORTER=console` is also available for local debugging. `OTEL_RESOURCE_ATTRIBUTES` is forwarded as resource attributes. Set `OTEL_METRICS_EXPORTER=none` to explicitly disable metrics.

---

## skill-up validate

Validate the eval config files. Run this before `run` to catch issues early.
Unlike `run` (which validates only the selected cases), `validate` always
checks the **whole** suite — the eval config and every referenced case file —
ignoring any case-name filters.

```bash
skill-up validate [path to eval.yaml]
```

### Examples

```bash
skill-up validate
skill-up validate ./evals/eval.yaml
```

On success:

```text
✓ eval.yaml is valid (loaded 3 case(s))
```

The validator checks that:

- `eval.yaml` and every referenced case file exist and parse correctly
- All required fields are present
- Field values are within the allowed range

---

## skill-up list-cases

List every case referenced by an eval config — handy for quickly inspecting your suite.

```bash
skill-up list-cases [path to eval.yaml]
```

### Examples

```bash
skill-up list-cases
skill-up list-cases ./evals/eval.yaml
```

Sample output:

```text
ID                    Title                           Tag              Prompt
basic-success         Agent should find null bug      functional_test  Review the current diff and report ...
edge-case-empty       Handle empty input gracefully   functional_test  Review an empty repository with no ...
regression-001        Fix: no longer misreports       functional_test  Review the payment processor code ...
```

---

## skill-up report

Regenerate reports from an existing result file without re-running the evaluation.

```bash
skill-up report <path to result.json> [flags]
```

### Flags

| Flag           | Default                       | Description                                                       |
| :------------: | :---------------------------: | :---------------------------------------------------------------: |
| `--format`     | `json`                        | Report format: `json` / `junit` / `html` / `markdown` (repeatable) |
| `--output-dir` | Same dir as `result.json`      | Output directory; created if missing                                |

### Examples

```bash
# Generate an HTML report from existing results
skill-up report result.json --format html

# Multiple formats at once
skill-up report result.json --format json --format junit --format html --format markdown

# Generate a Markdown summary for CI or a PR comment
skill-up report result.json --format markdown

# Pin the output directory
skill-up report result.json --format html --output-dir ./reports
```

### Report formats

| Format  | File           | Use case                                                          |
| :-----: | :------------: | :---------------------------------------------------------------: |
| `json`  | `report.json`  | Machine-readable structured data; consumable by downstream tools   |
| `junit` | `report.xml`   | JUnit XML; parseable by CI systems (Jenkins, GitHub Actions, …)    |
| `html`  | `report.html`  | Human-readable visualization; open in a browser                    |
| `markdown` | `report.md` | Concise Markdown summary for CI logs and GitHub PR comments        |

For single-run reports, `markdown` is limited to `skill-up report` as an offline conversion format for an existing `result.json`. It is not currently accepted by `skill-up run --format`, `skill-up debug report --format`, `skill-up debug judge --report`, or `eval.yaml report.formats`.

---

## skill-up compare

Compare two or more existing canonical `result.json` (or equivalent `report.json`)
files without running evaluations or calling any Agent or Judge.

```bash
skill-up compare <baseline-result.json> <candidate-result.json>... [flags]
```

The first file is the baseline. Every remaining file is compared independently
against it, preserving argument order. Gates apply to every candidate; any failed
comparison makes the command fail. Inputs are never compared as a sequential chain.

### Flags

| Flag | Default | Description |
|---|---|---|
| `--format` | `text` | `text`, `json`, `html`, or `markdown` |
| `--output` | stdout | Write the comparison to this file; its parent directory must exist |
| `--baseline-label` | `Baseline` | Display name for the first, baseline run |
| `--candidate-label` | automatic | Repeat once per candidate to label candidates in input order; supply all labels or omit the flag |
| `--fail-on-regression` | `false` | Return a non-zero exit code for any shared case moving from PASS to another status |
| `--max-token-increase-percent` | disabled | Maximum permitted increase in run-level total tokens (finite, non-negative percentage) |

### Examples

```bash
# Compare several finished runs in one self-contained HTML report
skill-up compare baseline/result.json skill-a/result.json skill-b/result.json \
  --format html --output comparison.html

# Two-run comparisons use the same command and report structure
skill-up compare old/result.json new/result.json --format html --output pair.html

# Markdown tables for PR reviews and CI artifacts
skill-up compare old/result.json new/result.json --format markdown --output comparison.md \
  --baseline-label "Released Skill" --candidate-label "Proposed Skill"

# Explicit labels for multiple candidates (repeatable, commas remain literal)
skill-up compare baseline.json a.json b.json --format markdown --output variants.md \
  --candidate-label "Skill A" --candidate-label "Skill B"

# Produce a report even if the regression gate fails
skill-up compare old/result.json new/result.json --format html --output comparison.html \
  --fail-on-regression --max-token-increase-percent 20

# Stable machine-readable output
skill-up compare old/result.json new/result.json --format json
```

All formats use the same comparison and gates. Reports include run metrics,
metadata, original source paths, a side-by-side overview of all runs, a case
status matrix across the union of case IDs, and detailed fixed, regressed, changed,
unchanged, added, and removed cases for each candidate against the baseline.
The case matrix marks cases absent from a run explicitly; JSON uses null matrix
cells. Matrix outcomes follow input order, and baseline-only cases are retained.
JSON always uses the same top-level `baseline`, `runs`, `case_matrix`,
`comparisons`, and `gates` structure, including for exactly two input files.
`comparisons` contains one baseline comparison per candidate, with individual
gate results; top-level `gates` aggregates failures and identifies the candidate.
Default labels are `Candidate` for one candidate, or `Candidate 1`, `Candidate 2`,
and so on for multiple candidates. HTML embeds its styles and filtering script, with
no external assets or network requests; all cases remain readable without
JavaScript. Markdown includes tables suitable for GitHub PR descriptions.
Source result artifacts are preserved; output may not overwrite either input.

Cases align by case ID using the existing primary-result rule: prefer
`with_skill`, then a non-baseline configuration, falling back to `without_skill`.
Benchmark reference entries do not double-count case counts or pass rates.
Configuration identity is retained in case tables. Shared cases flag prompt changes
when both prompts were recorded; otherwise the prompt comparison is unknown.
Requested, applied, and observed agent configurations are compared when recorded. Total tokens are the recorded
run-level total across all configurations; input/output tokens sum primary cases.
Duration is run wall time, and pass-rate changes are percentage points.

Added and removed cases are shown separately, with shared-case coverage. Aggregate
changes include each run's full primary case set, so different case sets are not
paired samples. A change between two non-PASS statuses is classified as changed,
not fixed or regressed. Recorded zero tokens remain zero; missing token metrics
are `unknown` in human-readable reports and `null` in JSON, including deltas.
The token gate fails when the total-token measurement is unknown, or when a zero
baseline increases to a positive value.

Reports compare recorded outcomes only and do not regrade scores. Missing
provenance is unknown; matching case IDs do not establish identical prompts,
suites, Skill revisions, or judge/rubric definitions. Review those conditions
before interpreting a change as an improvement. Gates are opt-in, apply equally
to every output format, and write the report before returning a gate failure.

---

## skill-up init

Write a skill-up user-config file. See [User Configuration](./user-config) for the discovery chain and schema.

```bash
skill-up init [flags]
```

### Flags

| Flag       | Default                                            | Description                                                                                  |
| :--------: | :-------------------------------------------------: | :-------------------------------------------------------------------------------------------: |
| `--local`  | `false`                                            | Write target is `$PWD/.skill-up.yaml` instead of the XDG path                                |
| `--config` | —                                                  | **Source** file to read (validated, copied verbatim with comments). Without this, a commented template is written. |
| `--print`  | `false`                                            | Print to stdout instead of writing to disk                                                   |
| `--force`  | `false`                                            | Overwrite an existing target                                                                 |

> Note: for `init`, `--config` names the *source* to read. For every other subcommand (`run`, `validate`, …) it is the *load-path override* sitting at the top of the discovery chain.

### Examples

```bash
# Write a commented template
skill-up init                                  # -> ~/.config/skill-up/config.yaml
skill-up init --local                          # -> ./.skill-up.yaml
skill-up init --print                          # -> stdout

# Seed from an existing config (validates, preserves comments)
skill-up init --config ./team-config.yaml          # -> ~/.config/skill-up/config.yaml
skill-up init --config ./team-config.yaml --local  # -> ./.skill-up.yaml
skill-up init --config ./team-config.yaml --print  # validate & dump to stdout

# Overwrite
skill-up init --local --force
```

---

## skill-up import

One-shot conversion of an Anthropic `evals.json` into skill-up's native YAML format.

```bash
skill-up import <path to evals.json> [flags]
```

### Flags

| Flag       | Default                       | Description       |
| :--------: | :---------------------------: | :---------------: |
| `--output` | Same dir as `evals.json`       | Output directory  |

### Examples

```bash
# Convert in place
skill-up import ./evals/evals.json

# Custom output directory
skill-up import ./evals/evals.json --output ./new-evals
```

The import produces:

- `eval.yaml` — entrypoint config (with sensible defaults; review before running)
- `cases/*.yaml` — one case file per `evals.json` entry

> **`import` vs `--auto`:** `import` is a one-time format conversion — afterwards you maintain YAML files. `run --auto` consumes `evals.json` at runtime without producing intermediate files. See [Migrating from Anthropic](./migration).

---

## Output layout

After a run, the output directory looks like:

```text
<skill-name>-workspace/
  iteration-1/                    # First iteration
    benchmark.json                # Aggregated stats
    <case-id>/
      with_skill/                 # Run with the Skill installed
        outputs/                  # Files generated by the Agent
          workspace/              # collect_artifacts matches (relative paths preserved)
        grading.json              # Grading result
      without_skill/              # Baseline (only when benchmark.enabled=true)
        outputs/
          workspace/
        grading.json
```

The `outputs/workspace/` subtree appears only when `collect_artifacts` is configured; see [Writing evals → Collecting workspace artifacts](writing-evals.md#collecting-workspace-artifacts-collect_artifacts).

### result.json configuration identity

The iteration-level `result.json` keeps `engine_name` and the requested-value
`model_name` semantics for compatibility and also records credential-free objects:

- `requested_configuration`: the engine, provider namespace, model, and version
  selected after YAML/CLI/credential precedence;
- `applied_configuration`: the adapter protocol and model skill-up forwarded
  to the CLI. An empty applied model means model selection was delegated to
  local/default settings. Its provider is also empty when the adapter did not
  actively select one;
- `observed_configuration`: present only when every runner session explicitly
  reported the same model. If absent, the runtime model is unknown. Per-case
  `observed_model` retains an individual agent-reported value when available.

Applied configuration is not proof of the CLI's final choice: local settings
may override command-line or environment values. skill-up does not make an
extra model request merely to inspect local login state.
When a Codex custom provider cannot be applied, its provider-scoped endpoint
and credential are omitted from the fallback invocation rather than being sent
to Codex's local/default provider.

Capability warnings and requested/applied values are also attached to each
agent `SessionResult`; its legacy `model` field is reserved for an explicitly
agent-reported observation. API keys
and other credential values are never written to these fields.

### grading.json

Per-case grading result (Anthropic-compatible):

```json
{
  "expectations": [
    {
      "text": "Output contains the keyword `null`",
      "passed": true,
      "evidence": "final_message contains 'null pointer at line 42'"
    }
  ],
  "summary": {
    "passed": 1,
    "failed": 0,
    "total": 1,
    "pass_rate": 1.0
  }
}
```

> **Note:** the `grading.json` under the workspace uses the Anthropic-compatible shape — only `expectations` and `summary` at the top level. The full evaluation status (`status`, `turns_executed`, `turns_total`, `assertion_results`) lives under `case_results[].grading` inside `result.json`.

The `grading` object inside `result.json` carries the full status:

- **PASS** — all assertions passed
- **FAIL** — at least one assertion failed
- **ERROR** — execution exception (timeout, engine crash, etc.)

### benchmark.json

Aggregated statistics across all cases:

```json
{
  "run_summary": {
    "with_skill": {
      "pass_rate": { "mean": 0.83 },
      "time_seconds": { "mean": 45.0 },
      "tokens": { "mean": 3800 }
    },
    "without_skill": null,
    "delta": null
  }
}
```
