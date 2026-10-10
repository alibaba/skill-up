# skill-upper

An Agent Skill that helps you evaluate and evolve other Agent Skills using the
`skill-up` CLI.

The canonical Skill instructions and references are in English. A localized
overview is available in [README.zh.md](README.zh.md).

## What it does

`skill-upper` guides you through an evaluation-to-evolution loop:

- **Locate** the target Skill and understand its capabilities
- **Scaffold** `evals/eval.yaml` and `evals/cases/*.yaml` with proper judge types
- **Simulate** responsive user turns with an independent model, alongside fixed turns or across an autonomous conversation
- **Validate** configuration before running
- **Run** evaluations against real Agent Engines (Claude Code, Codex, qodercli, etc.)
- **Diagnose** failures from structured reports and output evidence
- **Capture** explicitly attributed Skill usage, evidence, and feedback through the Codex or DSH observer integration
- **Review** locally captured Skill observations and turn approved feedback into regression cases
- **Evolve** the Skill or strengthen eval coverage, then rerun the suite

Observation capture and review support Codex and observer-enabled DSH. Claude
Code, qodercli, Qwen Code, and other Agent Engines remain available for ordinary
`skill-up` evaluation runs, but are not supported by the observation workflow.

## When to use

- You want to evaluate, test, or regress a Skill
- You want to fix or iterate a Skill from eval failures
- You want to capture explicitly attributed Skill usage or feedback
- You want to review a captured observation or convert approved feedback into a regression case
- You need to write `eval.yaml` / `case.yaml` or choose a judge type
- You're running `skill-up run/validate/list-cases/report/import/init`
- You're migrating from Anthropic `evals.json`

## User simulator scaffolding regression

`evals/eval-dashscope.yaml` runs only `scaffold-with-user-simulator` with local
OpenCode and DashScope `qwen3.8-max` as both the tested agent and the configured
judge. The case checks independent simulator configuration, mixed fixed and
simulated turns, and an autonomous case with a bounded turn count. It passed
with OpenCode 1.14.24; all five judge criteria and the file-existence check passed.
The generated YAML was also independently validated with `skill-up validate`.
The original Claude-based seven-case suite was not run in this verification.

Supply `DASHSCOPE_API_KEY` through your secret manager and install OpenCode.
From the repository root:

```bash
make build
PATH="$PWD/bin:$PATH" ./bin/skill-up run ./skills/skill-upper/evals/eval-dashscope.yaml
```

The credential-gated `TestSkillUpper_UserSimulator_DashScope` E2E uses this
configuration and runs in the manually dispatched full-mode model E2E suite.
This case generates configuration and runs validation; its `test-model` is a
placeholder. Actual simulator calls are covered separately by
[the user simulator example](../../examples/user-simulator/README.md).
