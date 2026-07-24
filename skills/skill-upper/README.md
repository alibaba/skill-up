# skill-upper

An Agent Skill that helps you evaluate and evolve other Agent Skills using the
`skill-up` CLI.

## What it does

`skill-upper` guides you through an evaluation-to-evolution loop:

- **Locate** the target Skill and understand its capabilities
- **Scaffold** `evals/eval.yaml` and `evals/cases/*.yaml` with proper judge types
- **Validate** configuration before running
- **Run** evaluations against real Agent Engines (Claude Code, Codex, qodercli, etc.)
- **Diagnose** failures from structured reports and output evidence
- **Evolve** the Skill or strengthen eval coverage, then rerun the suite

## When to use

- You want to evaluate, test, or regress a Skill
- You want to fix or iterate a Skill from eval failures
- You need to write `eval.yaml` / `case.yaml` or choose a judge type
- You're running `skill-up run/validate/list-cases/report/import/init`
- You're migrating from Anthropic `evals.json`
