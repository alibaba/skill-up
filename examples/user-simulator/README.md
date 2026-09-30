# User simulator example

This example exercises both ways to generate user replies: a `respond` turn
between fixed turns, and an autonomous conversation where the simulator
chooses the later messages and when to stop. In both cases the evaluator
enforces `max_turns`, and the rule-based judge checks the final answer.

The agent uses the local Codex login. The simulator uses its own OpenAI model
connection; it does not reuse the agent or judge credentials. Set
`OPENAI_API_KEY` through your usual secret-management mechanism, and change
`user_simulator.model` in `evals/eval.yaml` if your account uses another
Chat Completions-compatible model.

From the repository root:

```bash
make build
./bin/skill-up validate ./examples/user-simulator/evals/eval.yaml
./bin/skill-up run ./examples/user-simulator/evals/eval.yaml
```

The mixed case expects the final correction to three replicas. The autonomous
case expects two replicas and an explicit simulator stop. Model output can vary;
inspect the transcript artifact and per-turn `source` / `stop_reason` fields
when diagnosing a failure.

## DashScope live-model variant

`evals/eval-dashscope.yaml` runs the same cases with local OpenCode and
DashScope's `qwen3.6-plus` for both the agent and simulated user. Supply
`DASHSCOPE_API_KEY` for the agent, and `SIMULATION_API_KEY` plus
`SIMULATION_BASE_URL` for the simulator through your secret manager. The
connections resolve independently even when the same authorized key is used.

With `DASHSCOPE_API_KEY` already supplied to the process:

```bash
export SIMULATION_API_KEY="$DASHSCOPE_API_KEY"
export SIMULATION_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1
./bin/skill-up validate ./examples/user-simulator/evals/eval-dashscope.yaml
./bin/skill-up run ./examples/user-simulator/evals/eval-dashscope.yaml
```

Install OpenCode before using the `none` runtime. This variant was tested with
OpenCode 1.14.24: the mixed conversation completed three turns, and the
autonomous conversation completed two turns and stopped. All turns within
each case shared the same session ID. These are live-run observations, not
fixed expectations for the number of autonomous turns.

The credential-gated `TestUserSimulator_DashScope` E2E runs this configuration,
checks final assertions, turn sources, autonomous stop reasons, and session
continuity from the raw OpenCode event artifacts. It runs in the existing
full-mode model E2E workflow; quick E2E skips it. To run only this test with
`DASHSCOPE_API_KEY` supplied:

```bash
SKILL_UP_FULL_E2E=1 go test -tags e2e -race -timeout 15m -count=1 \
  -run '^TestUserSimulator_DashScope$' -v ./e2e
```
