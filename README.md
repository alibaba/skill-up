<div align="center">
  <p align="center">
    <img src="assets/logo.png" alt="skill-up logo" width="150" />
  </p>

  <h1>skill-up</h1>

  <p align="center">
    <a href="https://github.com/alibaba/skill-up/actions">
      <img src="https://github.com/alibaba/skill-up/actions/workflows/ci.yml/badge.svg" alt="CI" />
    </a>
    <a href="https://deepwiki.com/alibaba/skill-up">
      <img src="https://deepwiki.com/badge.svg" alt="Ask DeepWiki" />
    </a>
    <a href="./.github/badges/coverage.json">
      <img src="https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/alibaba/skill-up/badges/.github/badges/coverage.json" alt="Coverage" />
    </a>
    <a href="https://go.dev/">
      <img src="https://img.shields.io/badge/go-%3E%3D1.25-blue" alt="Go Version" />
    </a>
    <a href="LICENSE">
      <img src="https://img.shields.io/badge/license-Apache%202.0-green" alt="License" />
    </a>
    <a href="https://goreportcard.com/report/github.com/alibaba/skill-up">
      <img src="https://goreportcard.com/badge/github.com/alibaba/skill-up" alt="Go Report Card" />
    </a>
    <a href="https://github.com/alibaba/skill-up/releases">
      <img src="https://img.shields.io/github/v/release/alibaba/skill-up" alt="Release" />
    </a>
  </p>

  <p align="center">
    <b>English</b> | <a href="./README.zh.md">中文</a>
  </p>

  <p align="center">
    📖 <a href="https://alibaba.github.io/skill-up/">User Manual</a> · <a href="https://alibaba.github.io/skill-up/zh/">用户手册</a>
  </p>

  <hr />
</div>

## Overview

**skill-up** is a CLI evaluation framework for Agent Skill developers. Declare your eval environment, dependencies, test cases, and grading strategy in `evals/eval.yaml` and `evals/cases/*.yaml`, then run evaluations locally or in CI to generate structured reports.

> [!WARNING]
> This project is still in an **early evolution** stage: the code is not yet fully stable, and some CLI commands, configuration fields, and public APIs may still change in future releases. Please review the [CHANGELOG](CHANGELOG.md) and verify compatibility before using it in production.

## Features

- **Declarative Eval Config**: Define evaluation environment, engine, model, and cases through YAML (`eval.yaml` + `cases/*.yaml`).
- **Multi-Engine Support**: Works with Qoder CLI, Claude Code, and Codex as built-in Agent Engines, plus user-defined agents via `engine.custom` (local transport — see [docs/design/custom-engine.md](docs/design/custom-engine.md)).
- **Flexible Judging**: Supports `rule_based`, `script`, and `agent_judge` evaluation strategies.
- **Structured Reports**: Outputs Anthropic-compatible `grading.json`, `benchmark.json`, `benchmark.md`, plus `result.json`, JUnit XML, and HTML reports.
- **Anthropic Compatible**: Import `evals.json` via `skill-up import`, or auto-detect with `--auto`.
- **CI-Ready**: Designed for local development and continuous integration pipelines.

## Why skill-up

The official [Agent Skills evaluation guide](https://agentskills.io/skill-creation/evaluating-skills) describes the right evaluation loop: write realistic cases, run with and without the Skill, grade outputs, aggregate results, and iterate. `skill-up` turns that workflow into a reusable CLI:

- Replaces ad hoc run folders with a declarative `eval.yaml` + `cases/*.yaml` format.
- Automates workspace setup, Skill installation, Agent Engine invocation, judging, and report generation.
- Supports multiple engines (`claude_code`, `codex`, `qodercli`) instead of tying the workflow to one client.
- Keeps compatibility with Anthropic-style `evals.json` while adding richer judges, CI-friendly commands, and structured reports.

## Recommended Usage: AI-Assisted with skill-upper

For the best experience, use **skill-upper** — the Agent Skill shipped in this
repository. It lets you ask an AI agent to scaffold, validate, run, and explain
evals instead of hand-writing every YAML file first.

### 1. Install the `skill-upper` Agent Skill

Recommended: install it with the `skills` CLI:

```bash
# Codex, global install
npx skills add https://github.com/alibaba/skill-up/tree/main/skills/skill-upper -g -a codex -y

# Claude Code, global install
npx skills add https://github.com/alibaba/skill-up/tree/main/skills/skill-upper -g -a claude-code -y
```

You do not need to install `skill-up` before installing this Skill.
`skill-upper` checks whether the `skill-up` command is available when it runs
and guides the agent through installation if it is missing.

### 2. Add and run evals

Open the target Skill project in your AI agent. The target project should have
this shape:

```text
my-skill/
  SKILL.md
```

Then ask the agent something concrete:

```text
Use skill-upper to add evals for this Skill.
Add this evaluation case:
- Input: write a hello world program.
- Evaluation: check that the output contains hello and world.

After that run skill-up to validate and run.
```

The agent should create files like:

```text
my-skill/
  SKILL.md
  evals/
    eval.yaml
    cases/
      basic.yaml
my-skill-workspace/
  iteration-1/
    result.json
```

When `evals/eval.yaml` lives under a directory containing `SKILL.md`,
`skill-up` automatically installs that local Skill for the run, so you usually
do not need to list the Skill path manually in `eval.yaml`.

## Installation

Install with the script:

```bash
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash
```

The installer downloads the matching binary from [GitHub Releases](https://github.com/alibaba/skill-up/releases).

To build locally from a checkout, install [Go](https://go.dev/dl/) 1.25 or later:

```bash
make build
# or
go build -o bin/skill-up ./cmd/skill-up
```

**Windows users**: skill-up runs natively on Windows. See
[Windows Support](docs/guide/windows.md) for the recommended workflow,
known limitations (notably: native agent CLI execution requires Git
Bash), and the PowerShell tooling under `scripts/windows/`.

## Quick Start

### 1. Create Eval Config

In your Skill directory, create `evals/eval.yaml`:

```yaml
schema_version: v1alpha1

environment:
  type: none

engine:
  name: claude_code

cases:
  files:
    - evals/cases/hello-world.yaml
```

When `evals/eval.yaml` lives under a directory that contains `SKILL.md`, skill-up installs the current Skill automatically. The omitted fields use defaults: JSON report output, `timeout_seconds: 300`, `max_turns: 10`, and `parallelism: 1`.

For the full `eval.yaml` schema, see [Writing Evals](docs/guide/writing-evals.md).

### 2. Write an Eval Case

Create `evals/cases/hello-world.yaml`:

```yaml
input:
  prompt: |
    Please generate a Hello World program

expect:
  must_contain:
    - "Hello"
    - "World"
```

The case `id` defaults to the filename (`hello-world`). Add a `judge` block only when you need script-based or agent-based grading.

### 3. Validate Config

```bash
skill-up validate
```

This step is optional, but useful before the first run: it checks `eval.yaml` and all referenced case files without starting an Agent Engine.

### 4. Run Evaluation

```bash
skill-up run
```

Results are written to `<skill-name>-workspace/iteration-1/`.

For engineering conventions (Conventional Commits, Git hooks, golangci-lint), see [CONTRIBUTING.md](CONTRIBUTING.md).

## User config

skill-up auto-loads an optional user-level config that supplies default OpenTelemetry env vars and per-environment runtime kwargs. The embedded defaults are empty; downstream consumers maintain their own config file.

### Discovery chain (lowest to highest precedence)

```
embed (empty) < user (~/.config/skill-up/config.yaml) < project ($PWD/.skill-up.yaml) < explicit (--config)
```

| Source     | Path                                                                                                    |
| ---------- | ------------------------------------------------------------------------------------------------------- |
| `embed`    | empty `Config{}` — no vendor defaults baked in                                                          |
| `user`     | `$SKILL_UP_CONFIG`, else `$XDG_CONFIG_HOME/skill-up/config.yaml`, else `~/.config/skill-up/config.yaml` |
| `project`  | `$PWD/.skill-up.yaml`                                                                                   |
| `explicit` | `--config <path>` (must exist)                                                                          |

Missing files at the `user` and `project` layers are silently skipped; a missing `--config` path is a hard error. A corrupt config at any layer also fails the run.

### Quickstart

```bash
skill-up init                            # writes a template to ~/.config/skill-up/config.yaml (XDG-aware)
skill-up init --local                    # writes a template to $PWD/.skill-up.yaml
skill-up init --print                    # prints the template to stdout
skill-up init --force                    # overwrite an existing file
skill-up init --config foo.yaml          # reads foo.yaml, writes it to ~/.config/skill-up/config.yaml
skill-up init --config foo.yaml --local  # reads foo.yaml, writes it to $PWD/.skill-up.yaml
```

With `--config <path>`, `init` reads that file (validating it as a skill-up
config) and writes its raw bytes to the target — comments and formatting are
preserved. Without `--config`, `init` writes a commented YAML template.

### Schema

```yaml
schema_version: v1alpha1
kind: SkillUpConfig

telemetry:
  service_name: skill-up                              # OTEL_SERVICE_NAME
  traces_exporter: otlp                                 # OTEL_TRACES_EXPORTER
  traces:
    endpoint: http://localhost:4317                     # OTEL_EXPORTER_OTLP_TRACES_ENDPOINT (4317 for grpc, 4318/v1/traces for http/protobuf)
    protocol: grpc                                      # OTEL_EXPORTER_OTLP_TRACES_PROTOCOL (grpc | http/protobuf); skill-up defaults to grpc
  resource_attributes:                                  # serialized into OTEL_RESOURCE_ATTRIBUTES
    deployment.environment: local
  verbose: false                                        # if true, also enables OTEL_LOG_* payload capture

env:                                                    # arbitrary defaults, applied only-if-unset
  OTEL_EXPORTER_OTLP_HEADERS: authorization=${OTLP_TOKEN}

runtime_kwargs:                                         # keyed by environment.type
  opensandbox:
    base_url: http://localhost:8080
    # extensions: '{}'
```

### Precedence

For environment variables: any value already set in the process environment wins; the config only fills in missing keys.

For `runtime_kwargs`: explicit `--runtime-kwarg` on `run` > `eval.yaml` `environment.kwargs` > user-config `runtime_kwargs[environment.type]`.

### Secrets

Prefer `${ENV_VAR}` references inside the config file rather than baking secret literals. The redaction mechanism (`userconfig.Redact`) masks fields tagged `secret:"true"` when printing; currently no Config field carries the tag, but the mechanism is in place for future fields.

## Importing `evals.json`

Use `skill-up import` to migrate an Anthropic-compatible `evals.json` into the YAML layout used by this repo:

```bash
skill-up import ./evals/evals.json --output ./evals
```

## CLI Overview

| Command                              | Description                                 |
| ------------------------------------ | ------------------------------------------- |
| `skill-up run [path]`                | Run evaluation cases and produce reports    |
| `skill-up validate [path]`           | Validate `eval.yaml` and case files         |
| `skill-up list-cases [path]`         | List all cases referenced by the config     |
| `skill-up report <result.json>`      | Generate reports from a previous run        |
| `skill-up import <evals.json>`       | Import Anthropic `evals.json` to YAML cases |
| `skill-up debug judge <input.json>`  | Debug judge module with a JSON input        |
| `skill-up debug report <input.json>` | Debug report module with a JSON input       |

## License

Apache License 2.0 — see [LICENSE](LICENSE).
