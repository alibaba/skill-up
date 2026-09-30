# User simulator example

This example exercises both ways to generate user replies: a `respond` turn
between fixed turns, and an autonomous conversation where the simulator
chooses the later messages and when to stop. In both cases the evaluator
enforces `max_turns`, and the rule-based judge checks the final answer.

`evals/eval.yaml` runs both cases with local OpenCode and
DashScope's `qwen3.6-plus` for both the agent and simulated user. Supply
`DASHSCOPE_API_KEY` for the agent, and `SIMULATION_API_KEY` plus
`SIMULATION_BASE_URL` for the simulator through your secret manager. The
connections resolve independently even when the same authorized key is used.

With `DASHSCOPE_API_KEY` already supplied to the process:

```bash
make build
export SIMULATION_API_KEY="$DASHSCOPE_API_KEY"
export SIMULATION_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1
./bin/skill-up validate ./examples/user-simulator/evals/eval.yaml
./bin/skill-up run ./examples/user-simulator/evals/eval.yaml
```

Install OpenCode before using the `none` runtime.
This example has been live-tested only with the local OpenCode/DashScope setup
below; other engines, models, and runtimes are not covered by that verification.
With OpenCode 1.14.24: the mixed conversation completed three turns, and the
autonomous conversation completed two turns and stopped. All turns within
each case shared the same session ID. These are live-run observations, not
fixed expectations for the number of autonomous turns.

The mixed case expects the final correction to three replicas; the autonomous
case expects two replicas and an explicit simulator stop. Inspect the transcript
and per-turn `source` / `stop_reason` fields when diagnosing a failure.

The credential-gated `TestUserSimulator_DashScope` E2E runs this configuration,
checks final assertions, turn sources, autonomous stop reasons, and session
continuity from the raw OpenCode event artifacts. The existing manually
dispatched full-mode model E2E workflow includes this test; quick E2E skips it. To run only this test with
`DASHSCOPE_API_KEY` supplied:

```bash
SKILL_UP_FULL_E2E=1 go test -tags e2e -race -timeout 15m -count=1 \
  -run '^TestUserSimulator_DashScope$' -v ./e2e
```
