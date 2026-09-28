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
