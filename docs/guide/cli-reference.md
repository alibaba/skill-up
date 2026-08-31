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
| `--iteration`          | `0` (auto)                    | Repeat selected cases for stability/flakiness sampling. `0` auto-appends one run after the latest `iteration-N/` without summarizing history; positive `N` runs N samples and writes `iteration-1/` … `iteration-N/`; when `N > 1`, the terminal summary covers only samples from the current command |
| `--engine`             | From config                   | Override engine name                                                                                                                                       |
| `--provider`           | From config                   | Override `engine.model.provider`. When set, the complete `--model` value is preserved as an opaque model ID.                                               |
| `--model`              | From config                   | Override model. Without `--provider`, the legacy `provider/name` form is split when the prefix is configured; otherwise the complete value is preserved.   |
| `--parallelism`        | From config                   | Override `cases.parallelism`. Allowed range: 1–256                                                                                                          |
| `--baseline`           | From config                   | Override `benchmark.enabled` to `true` for this run                                                                                                         |
| `--api-key`            | —                             | Pass an API key (higher precedence than env vars)                                                                                                          |
| `-v, --verbose`        | `0`                           | Increase log verbosity. Default `info`; `-v` / `--verbose` / `--verbose=true` → `debug`; `-vv` / `--verbose=2` → `trace`; `--verbose=false` disables extra detail |

> **Validation scope.** `run` validates the eval-level config plus **only the
> cases selected** after `--include-case-name` / `--exclude-case-name` filters.
> An invalid case that is filtered out does not block the run — so a shared
> eval can hold both a quick `smoke` subset and heavier cases without the latter
> blocking a filtered smoke run. Use [`skill-up validate`](#skill-up-validate)
> to validate the **whole** suite (every case) regardless of filters.

### Examples

```bash
# Run all cases
skill-up run ./evals/eval.yaml

# Run a subset
skill-up run ./evals/eval.yaml --include-case-name "basic-*"

# Exclude cases
skill-up run ./evals/eval.yaml --exclude-case-name "*-old" --exclude-case-name "*-deprecated"

# Override engine and model
skill-up run ./evals/eval.yaml --engine codex --model openai/gpt-4

# Preserve an opaque upstream model ID when its prefix is not a configured provider
skill-up run ./evals/eval.yaml --engine claude_code --model anthropic_modelscope/deepseek-v4-pro

# Temporarily override case parallelism
skill-up run ./evals/eval.yaml --parallelism 4

# Run with baseline comparison
skill-up run ./evals/eval.yaml --baseline

# Multiple report formats
skill-up run ./evals/eval.yaml --format json --format html --format junit

# Three samples in this command, one folder per run, plus a simple flaky summary
skill-up run ./evals/eval.yaml --iteration 3

# Auto-detect mode (consumes Anthropic evals.json directly)
skill-up run --auto
skill-up run --auto --engine codex
skill-up run ./my-skill/ --auto
```

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

`markdown` is intentionally limited to `skill-up report` as an offline conversion format for an existing `result.json`. It is not currently accepted by `skill-up run --format`, `skill-up debug report --format`, `skill-up debug judge --report`, or `eval.yaml report.formats`.

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
