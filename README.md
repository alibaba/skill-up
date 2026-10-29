<div align="center">
  <p align="center">
    <img src="assets/logo.png" alt="skill-up logo" width="150" />
  </p>

  <h1>skill-up</h1>

  <p align="center">
    <b>Evaluate Agent Skills, agents, and workspaces. Evolve Skills with evidence.</b>
  </p>

  <p align="center">
    <a href="https://github.com/alibaba/skill-up/actions">
      <img src="https://github.com/alibaba/skill-up/actions/workflows/ci.yml/badge.svg" alt="CI" />
    </a>
    <a href="https://deepwiki.com/alibaba/skill-up">
      <img src="assets/deepwiki-badge.svg" alt="Ask DeepWiki" />
    </a>
    <a href="./.github/badges/coverage.json">
      <img src="https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/alibaba/skill-up/badges/.github/badges/coverage.json" alt="Coverage" />
    </a>
    <a href="https://go.dev/">
      <img src="https://img.shields.io/badge/go-%3E%3D1.26-blue" alt="Go Version" />
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
    <a href="plugins/dsh-skill-up/README.md">
      <img src="https://img.shields.io/badge/DeepSeek%20Harness-plugin-4D6BFE" alt="DeepSeek Harness Plugin" />
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

**skill-up** is a CLI for evaluating Agent Skills, agents, and their workspaces. It runs declarative cases against an Agent Engine, grades the response and workspace changes, and produces reports locally or in CI.

- **Skill evaluation** measures a Skill's behavior across cases and can compare runs with and without the Skill.
- **Agent evaluation** runs the same cases without installing a Skill, so you can assess an engine or custom agent on its own.
- **Workspace evaluation** checks how an agent works with files and repositories, using case fixtures or an existing local workspace.
- **Skill evolution** uses **skill-upper** to inspect failures, repair or expand the eval suite, and rerun skill-up through conversation.

![The Skill evaluation and evolution workflow with skill-upper](docs/public/skill-up-overview.png)

## Features

- **Eval-to-Evolution Loop with skill-upper**: Create evals through natural conversation, diagnose failures, automatically repair or expand cases, and rerun skill-up until the eval suite evolves.
- **Declarative Eval Config**: Define evaluation environment, engine, model, and cases through YAML (`eval.yaml` + `cases/*.yaml`).
- **Environment Provisioning**: Prepare a local, Docker, or OpenSandbox runtime for each case, install configured real or mocked MCP servers into supported Agent Engines, and upload `context.repo_fixture` and `context.files` into the workspace.
- **Skill-Optional Runs and Workspace Cases**: Evaluate an agent without a Skill, prepare repository fixtures per case, or run against an existing local workspace with `--workspace`.
- **Multi-Engine Support**: Works with Qoder CLI, Claude Code, and Codex as built-in Agent Engines, plus user-defined agents via `engine.custom` (local commands or HTTP services — see [docs/design/custom-engine.md](docs/design/custom-engine.md)).
- **DeepSeek Harness Plugin**: Use the bundled `skill-upper` Skill, opt-in durable observation capture, approval-gated regression cases, isolated runs, and status comparison to verify evidence-backed Skill improvements. See [`plugins/dsh-skill-up`](plugins/dsh-skill-up/README.md).
- **Multi-Turn Conversations and User Simulation**: Run fixed turns, mix scripted and simulated replies, or let an independent user model drive follow-ups and decide when to stop.
- **Role-Specific Engines and Models**: Configure the evaluated agent, Agent Judge, and user simulator separately, including independent judging of custom agents.
- **Flexible Judging**: Supports `rule_based`, `script`, and `agent_judge` evaluation strategies.
- **Structured Reports**: Outputs Anthropic-compatible `grading.json`, `benchmark.json`, `benchmark.md`, plus `result.json`, JUnit XML, and HTML reports.
- **Anthropic Compatible**: Import `evals.json` via `skill-up import`, or auto-detect with `--auto`.
- **CI-Ready**: Designed for local development and continuous integration pipelines.

## Why skill-up

[Harness engineering](https://openai.com/index/harness-engineering/) treats an agent's environment, tools, constraints, and feedback loops as parts of the system to design and improve. [Behavioral evals](https://developers.googleblog.com/the-anatomy-of-harness-engineering-how-to-evaluate-iterate-and-guard-ai-coding-agents/) make expected agent actions observable and help catch regressions. `skill-up` contributes evaluation and feedback to this work: repeatable prompts and workspace setups, checks on responses, tool calls, and file changes, and structured reports.

For Skills, the official [Agent Skills evaluation guide](https://agentskills.io/skill-creation/evaluating-skills) describes a further loop: run with and without the Skill, grade outputs, aggregate results, and iterate. `skill-up` supports these workflows:

- Replaces ad hoc run folders with a declarative `eval.yaml` + `cases/*.yaml` format.
- Closes the improvement loop: skill-upper can interpret failed reports, repair or add eval cases, and drive the next skill-up run through conversation.
- Automates workspace setup, optional Skill installation, Agent Engine invocation, judging, and report generation.
- Supports multiple engines (`claude_code`, `codex`, `qodercli`, `qwen_code`) instead of tying the workflow to one client.
- Keeps compatibility with Anthropic-style `evals.json` while adding richer judges, CI-friendly commands, and structured reports.

## Quick Start: Evolve a Skill with skill-upper

For Skill evolution, use **skill-upper**, the Agent Skill shipped in this
repository. It lets your AI agent create evals, run skill-up,
understand failures, fix the Skill or its evals, add regression coverage, and
repeat the loop through conversation.

For repository development, `skills/skill-upper/` is the only editable source.
Plugin bundles are generated into ignored build directories rather than tracked
copies or symlinks; run `make bundle-plugins` when preparing them locally.
`make package-plugins VERSION=<version>` creates self-contained Codex and DSH
archives. Tagged GitHub releases attach both archives and their checksums; this
repository does not publish either plugin to an external marketplace or npm.

### 1. Install skill-upper

```bash
# Codex, global install
npx skills add https://github.com/alibaba/skill-up/tree/main/skills/skill-upper -g -a codex -y

# Claude Code, global install
npx skills add https://github.com/alibaba/skill-up/tree/main/skills/skill-upper -g -a claude-code -y
```

You normally do not need to install skill-up first. skill-upper checks for the
CLI when it runs and guides the agent through installation if needed.

### 2. Create and run the first evals

Open a project that contains your Skill's `SKILL.md` in Codex, Claude Code, or
another compatible Agent, then ask:

```markdown
Use skill-upper to evaluate this Skill.
Read SKILL.md, identify its most important behaviors, create realistic eval
cases with appropriate judges, validate the configuration, and run skill-up.
Summarize the results and the highest-impact failures.
```

skill-upper creates the declarative eval suite and drives the CLI for you:

```text
my-skill/
  SKILL.md
  evals/
    eval.yaml
    cases/
      <case-id>.yaml
my-skill-workspace/
  iteration-1/
    result.json
```

### 3. Fix, regress, and iterate

Continue in the same conversation:

```markdown
Use skill-upper to improve this Skill from the latest evaluation results.
Follow its evolution workflow, compare the original and candidate, add
regression coverage, and summarize which changes the evidence supports.
```

The comparison and acceptance rules live in
[skill-upper's evolution workflow](skills/skill-upper/SKILL.md#step-8-evolve-the-skill-when-requested).
This prompt invokes that workflow; keeping the original is a valid outcome
when a candidate does not show a supported improvement.

### Prefer manual setup?

You can still install the CLI directly and hand-write `eval.yaml` and case
files:

```bash
curl -fsSL https://raw.githubusercontent.com/alibaba/skill-up/main/install.sh | bash
```

See the official documentation for
[Getting Started](https://alibaba.github.io/skill-up/guide/getting-started),
[Writing Evals](https://alibaba.github.io/skill-up/guide/writing-evals),
[CLI Reference](https://alibaba.github.io/skill-up/guide/cli-reference), and
[User Configuration](https://alibaba.github.io/skill-up/guide/user-config).
Windows-specific setup and limitations are covered in the
[Windows guide](https://alibaba.github.io/skill-up/guide/windows).

## Evaluate an agent or workspace without a Skill

An agent-only eval does not need `SKILL.md`. Set `skills: []` explicitly: when
`skills` is omitted and skill-up finds a `SKILL.md` above the config, it installs
that Skill automatically. Put `eval.yaml` and case files under `evals/`, then
pass the config path explicitly. Case and fixture paths resolve from the
discovered Skill root, or from the directory containing `evals/` if none is found.

```yaml
# evals/eval.yaml
schema_version: v1alpha1
environment:
  type: none
skills: []
engine:
  name: codex
cases:
  files:
    - evals/cases/agent-response.yaml
```

```yaml
# evals/cases/agent-response.yaml
input:
  prompt: Reply with READY.
expect:
  must_contain: [READY]
```

```bash
skill-up validate ./evals/eval.yaml
skill-up run ./evals/eval.yaml
```

For repository tasks, a case can use `context.repo_fixture` to prepare a fresh
workspace and `expect` or a judge to check the result. To evaluate an agent in
an already prepared local directory, use a case written for that directory and
run `skill-up run ./evals/eval.yaml --workspace /path/to/project --parallelism 1`.
The existing directory is preserved, but the agent and case setup can change
its contents. This option requires `environment.type: none`, one case at a time,
and benchmark mode off. See [Writing Evals](docs/guide/writing-evals.md)
and the [CLI Reference](docs/guide/cli-reference.md#skill-up-run).

## Multi-Turn Conversations and Role Configuration

A simulated user can react to what the agent actually says, so follow-up messages
need not be scripted in advance. For example, evaluate a Twenty Questions player:

- The **evaluated agent** asks natural-language yes/no questions and guesses an object.
- The **simulated user** knows the hidden object and answers from the conversation history.
- An **independent Agent Judge** reviews the complete dialogue against explicit criteria.

This **case YAML** excerpt lets the simulator drive all replies after the opening:

```yaml
id: umbrella
title: Guess a hidden object through simulated user replies
user_simulator:
  scenario: |
    You host Twenty Questions. The hidden object is an ordinary folding umbrella.
    Answer questions using common knowledge, with only yes, no, or unsure.
    Do not reveal the object or volunteer hints. Stop immediately when the player
    guesses correctly, or after the final permitted guess.
input:
  prompt: |
    I have a common everyday object in mind. Guess it by asking yes/no questions;
    I will answer yes, no, or unsure. Ask only one question per turn, and ask at
    least two questions before guessing. You have 20 questions or incorrect
    guesses in total, followed by one final guess. Use natural language.
constraints:
  max_turns: 22
```

The hidden answer belongs to the simulator's scenario, not the player's prompt.
For example, a question about rain protection can lead to a different next
question than one about electronics; the simulator responds to the actual
question. The 22-turn runtime cap leaves room for 20 questions or incorrect
guesses, a final guess, and a closing response. The judge separately checks the
game's rules; the runtime cap alone does not enforce them.

Other conversation patterns remain available:

| Pattern | Case configuration | Behavior |
| --- | --- | --- |
| Autonomous simulation (above) | `input.prompt` plus `user_simulator.scenario`, without `input.turns` | Generate subsequent user messages and decide when to stop |
| Mixed | `content` and `respond` in `input.turns`, plus `user_simulator.scenario` | Generate selected user replies from the actual conversation history and turn instruction |
| Fixed | `input.turns[].content` | Send predefined user messages in order |

Real turn-by-turn interaction requires session resumption. `claude_code`, `codex`,
`qodercli`, and `opencode` support it; custom engines must set
`engine.custom.conversation_mode: stateful` and resume using `session_id`.
Ordinary fixed-turn cases fall back to a batch prompt on engines without resumption;
simulated cases require resumption. The opening prompt counts as turn one in an
autonomous conversation, and `max_turns` bounds the total. A simulator stop ends
the dialogue; the configured judge still determines PASS or FAIL.

### Select the evaluated agent, judge, and simulator independently

| Role | Configuration in `eval.yaml` | Independence |
| --- | --- | --- |
| Evaluated agent | `engine.name`, `engine.model`, optional `engine.custom` | Executes tasks through a built-in engine or your own local command / HTTP service |
| Agent Judge | `judge.engine`, or each member's `engine` in `judges[]` | Can use a separate engine, model, provider, and endpoint; rule and script judges need no model |
| Simulated user | `user_simulator.provider`, `protocol`, `model` | Uses its own model connection to generate replies from the scenario and history; does not select an Agent CLI engine |

This **eval.yaml** excerpt shows role configuration. Model IDs are placeholders;
replace them with IDs supported by each provider and configure case files and
environment for your evaluation. These criteria grade the game above;
`pass_threshold: 1.0` requires all criteria to pass:

```yaml
engine:
  name: codex
  model:
    provider: openai
    name: your-agent-model-id

judge:
  type: agent_judge
  engine:
    name: claude_code
    model:
      provider: anthropic
      name: your-judge-model-id
  criteria:
    - "The player asked at least two questions before guessing and correctly identified an umbrella"
    - "The player asked only one question per turn and respected the 20-attempt limit plus one final guess"
    - "The simulated user answered consistently with a folding umbrella and did not reveal the answer before a correct guess"
  pass_threshold: 1.0
  context:
    transcript: file_ref

user_simulator:
  provider: simulation
  protocol: openai
  model: your-simulator-model-id
  timeout_seconds: 60
```

`judge.engine` does not inherit the evaluated agent's model, CLI overrides, or
connection configuration; prepare the required engines and credentials separately.
It is mutually exclusive with legacy `judge.model`, which reuses the evaluated
agent's engine. The simulator above resolves `SIMULATION_API_KEY` and
`SIMULATION_BASE_URL`, or the same provider in the credentials file; `protocol`
supports `openai` (Chat Completions) and `anthropic` (Messages). An independent
judge still uses the case runtime; there is no `judge.environment`.
The host agent/model that drives skill-upper's diagnosis and edits is configured
in your host application, outside these evaluation fields.

See [Writing Evals](https://alibaba.github.io/skill-up/guide/writing-evals) for
complete fields, per-turn assertions, and the session support matrix. See the
[user simulator example](examples/user-simulator/README.md) for runnable
configurations and validation scope, and the
[Custom Engine design](docs/design/custom-engine.md) for custom agent integration.

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
| `skill-up compare <baseline> <candidate>...` | Compare multiple finished runs; export HTML, Markdown, JSON or text |
| `skill-up report <result.json>`      | Generate reports from a previous run        |
| `skill-up import <evals.json>`       | Import Anthropic `evals.json` to YAML cases |
| `skill-up debug judge <input.json>`  | Debug judge module with a JSON input        |
| `skill-up debug report <input.json>` | Debug report module with a JSON input       |

## GitHub Action

Run your Agent Skill evals in CI on every pull request — and check the same skill
**across engines** (`claude_code` / `codex` / `qodercli` / `qwen_code`) in one step. This repo
ships an action at its root ([`action.yml`](action.yml)):

```yaml
# .github/workflows/skill-eval.yml
name: Skill Eval
on:
  pull_request:
    paths: ['skills/**', 'evals/**', '**/SKILL.md']
jobs:
  eval:
    runs-on: ubuntu-latest          # Docker container action — Linux only
    steps:
      - uses: actions/checkout@v4
      - uses: alibaba/skill-up@main  # see "Versioning" below
        with:
          engine: claude_code        # or codex / qodercli / qwen_code; empty = let eval.yaml decide
          api-key: ${{ secrets.ANTHROPIC_API_KEY }}
          base-url: https://api.anthropic.com   # your model endpoint
          skill-target: evals/eval.yaml
```

Requirements for the caller: a **Linux** runner (it's a Docker container action),
and your model credential stored as a repo secret. The runner image is public, so
no extra registry auth is needed.

Key inputs: `engine`, `model`, `provider`, `api-key`, `base-url`, `skill-target`,
`parallelism`. The action prebuilds skill-up + the three engine CLIs into its
runner image, so a run is just "pull image, eval". See [`action.yml`](action.yml)
for the full input/output reference.

### Bundled skill-up version

The container image includes a deliberately pinned skill-up CLI version. The
version is not resolved from `latest` when a workflow starts, so a given image
digest always runs the same CLI.

The `skill-up-version` input is only a fallback for a custom image that does not
already contain the `skill-up` binary. The official image contains the binary,
so this input cannot override its bundled version. To find the effective
version, check the `skill-up --version` line in the Action log.

Publishing a new skill-up CLI release does not automatically update the GitHub
Action image. Maintainers must synchronize the pinned version, publish and test
a new runner image, and update the image digest in `action.yml`. The complete
maintainer procedure is documented in the
[CI maintenance runbook](docs/guide/ci-maintenance.md#manual-skill-up-version-synchronization).

The production Action must use an immutable `sha256:` image digest. Do not
replace it with `skill-up-runner:latest`; a mutable tag would allow existing
Action references to change behavior without a repository commit.

### Versioning

`uses:` points at any git ref that contains `action.yml`. Pin a **release tag**
(the first release that includes the action onward) or a commit SHA for stability;
`@main` always tracks the latest. Release tags published **before** the action was
added do not contain `action.yml` and cannot be used as the ref.

A CLI release tag captures the `action.yml` and runner-image digest that existed
when that tag was created. Because the current runner image is refreshed
manually after CLI release assets become available, do not assume that a CLI tag
automatically contains an Action image with the same CLI version. Until a
separate Action release tag process is introduced, use a post-refresh commit SHA
for an immutable reference or `@main` when intentionally following Action
updates.

## Project Structure

```text
skill-up/
├── cmd/skill-up/          # CLI entry point
├── internal/              # Private implementation packages
│   ├── cli/               # Commands and flags
│   ├── evaluator/         # Case execution and judging orchestration
│   ├── evalevent/         # Evaluation event model, lifecycle, publisher, and JSONL sink
│   ├── runner/            # End-to-end run orchestration
│   └── report/            # JSON, JUnit, HTML, and benchmark reports
├── pkg/                   # Publicly importable APIs
├── plugins/               # Agent host plugin bundles
│   ├── codex-skill-up/    # Codex adapter; release bundles add shared assets
│   └── dsh-skill-up/      # DSH bundle with a generated dist/ Skill copy
├── schemas/               # Evaluation-event and host-neutral observation contracts
├── skills/skill-upper/    # Canonical source for the distributable workflow Skill
├── docs/                  # VitePress documentation
├── e2e/                   # End-to-end tests and fixtures
└── examples/              # Example evaluations and fixtures
```

See [CONTRIBUTING.md](CONTRIBUTING.md#directory-overview) for the complete
directory map and architectural responsibilities.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
