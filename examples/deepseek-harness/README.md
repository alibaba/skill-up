# DeepSeek Harness Custom Engine example

This example runs a stateful, multi-turn Skill evaluation through DeepSeek
Harness (DSH) using skill-up's local Custom Engine contract. It defaults to
DashScope's `qwen3.8-max` model and keeps the API key in the process
environment.

## Prerequisites

- skill-up built from a revision that includes PR #250 (stateful Custom
  Engines); v0.11.0 is not sufficient
- Python 3.10 or newer
- `dsh` available on `PATH`
- `DASHSCOPE_EVAL_API_KEY` available in the environment

Install the pinned DSH version used to verify this example:

```bash
npm install -g @deepseek-ai/dsh@0.1.5-rc.1
dsh --version
```

## Run

From this repository checkout:

```bash
export DSH_SKILL_UP_RUNNER="$PWD/examples/deepseek-harness/evals/fixtures/dsh_runner.py"
skill-up run ./examples/deepseek-harness/evals/eval.yaml
```

The example deliberately does not put the DashScope key in YAML. Inject it
from your secret manager or shell environment. To select another compatible
Qwen model, pass `--model <model-name>` to `skill-up run`. Override
`DASHSCOPE_BASE_URL` to use another compatible endpoint.

The runner creates a fresh `.skill-up-dsh/runs/<run-id>/` home for every case
attempt, disables DSH telemetry, configures the DashScope OpenAI-compatible
route, and exposes the installed Skill directory to DSH. It drives DSH through
the public ACP profile: the first turn creates a persistent DSH session, and
later invocations resume that exact session using skill-up's opaque
`session_id`. Each invocation returns an incremental structured
`SessionResult`, including tool calls, tool results, and token counts.

The bundled case uses two turns. The first asks the installed Skill to decode a
fixture; the second requires DSH to recall that result from the resumed session.
DSH session JSONL, ACP frames, and stderr are retained as run artifacts for
debugging after injected credentials are redacted.

This is an experimental local integration. MCP installation is not included.
