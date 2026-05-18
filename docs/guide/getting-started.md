# Getting Started

skill-up is an evaluation tool for Agent Skill developers. Use it to verify that your Skill behaves correctly inside real Agent Engines (Claude Code, Codex, Qoder CLI) and to run continuous regression locally or in CI.

---

## Recommended Usage: AI-Assisted with skill-upper

For the best experience, we recommend using **skill-upper** — an Agent Skill that helps you set up and run evals through natural conversation with AI agents like Cursor, Claude Code, or Qoder.

Instead of manually writing YAML configs and running CLI commands, you can simply tell your AI agent:

```
"Add evals for this skill"
"Run the evaluation suite"
"Why did this test case fail?"
```

The skill-upper skill will automatically:
- Locate your Skill directory and read `SKILL.md`
- Generate appropriate `eval.yaml` and `case.yaml` files
- Run `skill-up validate` and `skill-up run`
- Interpret results and explain failures
- Handle credentials and configuration

To use skill-upper:
1. Install the skill-upper skill in your AI agent (e.g., Cursor Skills, Claude Skills)
2. Open your Skill project in the AI agent
3. Ask the agent to help you create or run evals

This approach is especially helpful for beginners or when you want to quickly iterate on test cases without memorizing CLI syntax.

---

## Manual Installation

### Install with the script

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

engine:
  name: claude_code             # Use Claude Code as the Agent Engine

cases:
  files:
    - evals/cases/hello-world.yaml
```

> **Tip:** When `evals/eval.yaml` lives under a directory that contains `SKILL.md`, skill-up installs the current Skill automatically. The omitted fields use defaults: JSON report output, `timeout_seconds: 300`, `max_turns: 10`, and `parallelism: 1`. Add `engine.model`, `skills`, `cases.defaults`, or `report` only when you need to override them.

For the full `eval.yaml` schema, see [Writing Evals](./writing-evals).

### Step 2 — Write an Eval Case

Create `evals/cases/hello-world.yaml`:

```yaml
input:
  prompt: |
    Please generate a Hello World program

expect:
  must_contain:
    - "Hello"
    - "World"
  must_not_contain:
    - "error"
```

The case `id` defaults to the filename (`hello-world`). Add a `judge` block only when you need script-based or agent-based grading.

### Step 3 — Validate the config

This step is optional, but useful before the first run: it checks `eval.yaml` and all referenced case files without starting an Agent Engine.

```bash
skill-up validate
```

On success you should see:

```text
✓ eval.yaml is valid (loaded 1 case(s))
```

### Step 4 — Run the evaluation

```bash
skill-up run
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
