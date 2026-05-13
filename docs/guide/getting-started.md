# Getting Started

skill-up is an evaluation tool for Agent Skill developers. Use it to verify that your Skill behaves correctly inside real Agent Engines (Claude Code, Codex, Qoder CLI) and to run continuous regression locally or in CI.

---

## Installation

skill-up ships as a prebuilt binary and has no runtime dependencies.

### From source (recommended)

```bash
go install github.com/alibaba/skill-up/cmd/skill-up@latest
```

Or build locally from a checkout:

```bash
make build
# or
go build -o bin/skill-up ./cmd/skill-up
```

### Prebuilt binaries

Download from [GitHub Releases](https://github.com/alibaba/skill-up/releases).

### Verify the install

```bash
skill-up --version
```

---

## Core concepts

To evaluate a Skill with skill-up you need two things:

1. **eval.yaml** — the entrypoint config that declares the runtime environment, the Agent Engine and model, and the global grading strategy.
2. **case.yaml** — a single evaluation case that defines the prompt sent to the Agent, the expected output, and grading rules.

They live inside the `evals/` folder of your Skill:

```text
my-skill/
  SKILL.md              # Your Skill definition
  evals/                # Evaluation root
    eval.yaml           # Entrypoint config
    cases/              # One file per case
      basic-test.yaml
      edge-case.yaml
    fixtures/           # Optional test resources
      repos/            # Repository templates
      scripts/          # Grading scripts
```

---

## 5-minute quick start

### Step 1 — Create the eval config

Inside your Skill directory, create `evals/eval.yaml`:

```yaml
schema_version: v1alpha1

environment:
  type: none                    # Plain-text Skills don't need an isolated container

skills:
  - source: local_path
    path: .                     # The current Skill directory
  - source: local_path
    path: ./dependency_skill_dir   # Relative to the Skill under test, when depending on another SKILL.md

engine:
  name: claude_code             # Use Claude Code as the Agent Engine
  # `model` is optional. If omitted, the engine uses its local default model
  # and `--model` is NOT passed on the command line. To pin a model, uncomment:
  # model:
  #   provider: anthropic
  #   name: claude-sonnet-4-6

cases:
  files:
    - evals/cases/hello-world.yaml
  defaults:
    timeout_seconds: 120
    max_turns: 5

report:
  formats: [json]
```

> **Tip:** `engine.model.provider` and `engine.model.name` are both optional. When omitted, the engine falls back to its own default and skill-up will not append `--model`. Add an explicit `model` block under `engine` only when you need to pin a specific model.

### Step 2 — Write a case

Create `evals/cases/hello-world.yaml`:

```yaml
id: hello-world
title: Skill should respond to a basic request

input:
  prompt: |
    Please generate a Hello World program

expect:
  must_contain:
    - "Hello"
    - "World"
  must_not_contain:
    - "error"

judge:
  type: rule_based
  success:
    - output_contains:
        all: ["Hello", "World"]
```

### Step 3 — Validate the config

Always validate before running:

```bash
skill-up validate ./evals/eval.yaml
```

On success you should see:

```text
✓ eval.yaml is valid (loaded 1 case(s))
```

### Step 4 — Run the evaluation

```bash
skill-up run ./evals/eval.yaml
```

You will see output similar to:

```text
Running 1 case(s) with agent claude_code
[Runner] Running 1 cases with agent claude_code
[Evaluator] Skill installed: <skill-name>
[Evaluator] Running case hello-world (with_skill): Skill should respond to a basic request
[Evaluator] Case hello-world: PASS (pass_rate: 100.0%)
[INFO] Results written to ./<skill-name>-workspace/iteration-1
```

---

## Next steps

- [Writing Evals](./writing-evals) — full reference for `eval.yaml` and case files.
- [CLI Reference](./cli-reference) — every command and flag.
- [Migrating from Anthropic](./migration) — if you already have an Anthropic skill-creator `evals.json`.
