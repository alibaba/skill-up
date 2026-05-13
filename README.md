<div align="center">
  <p align="center">
    <img src="assets/logo.png" alt="skill-up logo" width="150" />
  </p>

  <h1>skill-up</h1>

  <p align="center">
    <a href="https://github.com/alibaba/skill-up/actions">
      <img src="https://github.com/alibaba/skill-up/actions/workflows/ci.yml/badge.svg" alt="CI" />
    </a>
    <a href="https://codecov.io/gh/alibaba/skill-up">
      <img src="https://codecov.io/gh/alibaba/skill-up/branch/main/graph/badge.svg" alt="Coverage" />
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
> The core business logic of this repository is implemented, but the project is still in an **early evolution** stage: the code is not yet fully stable, and some CLI commands, configuration fields, and public APIs may still change in future releases. Please review the [CHANGELOG](CHANGELOG.md) and verify compatibility before using it in production.

## Features

- **Declarative Eval Config**: Define evaluation environment, engine, model, and cases through YAML (`eval.yaml` + `cases/*.yaml`).
- **Multi-Engine Support**: Works with Qoder CLI, Claude Code, and Codex as Agent Engines.
- **Flexible Judging**: Supports `rule_based`, `script`, and `agent_judge` evaluation strategies.
- **Structured Reports**: Outputs Anthropic-compatible `grading.json`, `benchmark.json`, `benchmark.md`, plus `result.json`, JUnit XML, and HTML reports.
- **Anthropic Compatible**: Import `evals.json` via `skill-up import`, or auto-detect with `--auto`.
- **CI-Ready**: Designed for local development and continuous integration pipelines.

## Requirements

- [Go](https://go.dev/dl/) 1.25 or later — required for building and running the CLI.

## Installation

**From source:**

```bash
go install github.com/alibaba/skill-up/cmd/skill-up@latest
```

**Prebuilt binaries:**
Download from [GitHub Releases](https://github.com/alibaba/skill-up/releases).

**Build locally:**

```bash
make build
# or
go build -o bin/skill-up ./cmd/skill-up
```

## Quick Start

### 1. Create Eval Config

In your Skill directory, create `evals/eval.yaml`:

```yaml
schema_version: v1alpha1

environment:
  type: none

skills:
  - source: local_path
    path: .

engine:
  name: claude_code

cases:
  files:
    - evals/cases/hello-world.yaml
  defaults:
    timeout_seconds: 120
    max_turns: 5

report:
  formats: [json]
```

### 2. Write a Test Case

Create `evals/cases/hello-world.yaml`:

```yaml
id: hello-world
title: Skill should respond to basic requests

input:
  prompt: |
    Please generate a Hello World program

expect:
  must_contain:
    - "Hello"
    - "World"

judge:
  type: rule_based
  success:
    - output_contains:
        all: ["Hello", "World"]
```

### 3. Validate Config

```bash
skill-up validate ./evals/eval.yaml
```

### 4. Run Evaluation

```bash
skill-up run ./evals/eval.yaml
```

Results are written to `<skill-name>-workspace/iteration-1/`.

For engineering conventions (Conventional Commits, Git hooks, golangci-lint), see [CONTRIBUTING.md](CONTRIBUTING.md).

## User config

skill-up auto-loads an optional user-level config that supplies default OpenTelemetry env vars and per-environment runtime kwargs. The embedded defaults are empty; downstream consumers maintain their own config file.

### Discovery chain (lowest to highest precedence)

```
embed (empty) < user (~/.config/skill-up/config.yaml) < project ($PWD/.skill-up.yaml) < explicit (--config)
```

| Source     | Path                                                                                   |
| ---------- | -------------------------------------------------------------------------------------- |
| `embed`    | empty `Config{}` — no vendor defaults baked in                                         |
| `user`     | `$SKILL_EVAL_CONFIG`, else `$XDG_CONFIG_HOME/skill-up/config.yaml`, else `~/.config/skill-up/config.yaml` |
| `project`  | `$PWD/.skill-up.yaml`                                                                |
| `explicit` | `--config <path>` (must exist)                                                         |

Missing files at the `user` and `project` layers are silently skipped; a missing `--config` path is a hard error. A corrupt config at any layer also fails the run.

### Quickstart

```bash
skill-up init              # writes ~/.config/skill-up/config.yaml (XDG-aware)
skill-up init --local      # writes $PWD/.skill-up.yaml
skill-up init --print      # writes the template to stdout
skill-up init --force      # overwrite an existing file
```

### Schema

```yaml
schema_version: v1alpha1
kind: SkillEvalConfig

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

| Command | Description |
|---------|-------------|
| `skill-up run [path]` | Run evaluation cases and produce reports |
| `skill-up validate [path]` | Validate `eval.yaml` and case files |
| `skill-up list-cases [path]` | List all cases referenced by the config |
| `skill-up report <result.json>` | Generate reports from a previous run |
| `skill-up import <evals.json>` | Import Anthropic `evals.json` to YAML cases |
| `skill-up debug judge <input.json>` | Debug judge module with a JSON input |
| `skill-up debug report <input.json>` | Debug report module with a JSON input |

## Project Structure

```text
skill-up/
├── cmd/skill-up/          # CLI entrypoint
├── internal/              # Private implementation
│   ├── cli/               # Cobra commands
│   ├── config/            # YAML config loader & validator
│   ├── credential/        # API key & credential resolution
│   ├── runtime/           # Workspace runtime (none / opensandbox)
│   ├── agent/             # Agent Engine adapters
│   ├── judge/             # Evaluation judges
│   ├── report/            # Report generators (JSON / JUnit / HTML)
│   └── runner/            # End-to-end orchestration
├── pkg/transcript/        # Public transcript parsing API
├── docs/                  # VitePress documentation site
│   ├── .vitepress/        # VitePress config
│   ├── guide/             # English user guide
│   ├── zh/                # Chinese user guide
│   └── public/            # Static assets (logo, etc.)
├── e2e/                   # End-to-end tests
├── examples/              # Example fixtures and scripts
├── Makefile               # Build & quality targets
├── go.mod / go.sum        # Go module dependencies
└── README.md              # This file
```

## License

Apache License 2.0 — see [LICENSE](LICENSE).
