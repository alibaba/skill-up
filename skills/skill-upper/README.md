# skill-upper

An Agent Skill that helps you set up, run, and interpret evaluations (evals) for other Agent Skills using the `skill-up` CLI.

## What it does

`skill-upper` guides you through the full evaluation lifecycle:

- **Locate** the target Skill and understand its capabilities
- **Scaffold** `evals/eval.yaml` and `evals/cases/*.yaml` with proper judge types
- **Validate** configuration before running
- **Run** evaluations against real Agent Engines (Claude Code, Codex, qodercli, etc.)
- **Interpret** results — pass rates, failing assertions, benchmark deltas, and HTML reports

## When to use

- You want to evaluate, test, or regress a Skill
- You need to write `eval.yaml` / `case.yaml` or choose a judge type
- You're running `skill-up run/validate/list-cases/report/import/init`
- You're migrating from Anthropic `evals.json`

