# Custom Engine e2e fixture

This directory is a minimal skill-up fixture that exercises the **Custom
Engine** local and http transports end-to-end. It is consumed by
`e2e/custom_engine_test.go`.

`agent.sh` + `evals/eval.yaml` cover the **local** transport: `agent.sh` is a
deterministic stand-in for a real custom agent that reads the `SessionInput`
JSON the framework writes to `${input_file}` and emits a fixed `SessionResult`
on stdout. The case asserts that `final_message` flows from the agent's stdout
into the report.

`evals/eval-http.yaml` exercises the **http** transport against the same
`hello.yaml` case. `TestPipeline_CustomEngine_HTTPTransport` starts an
in-process `httptest` server, points `${CUSTOM_AGENT_ENDPOINT}` at it, and
asserts the posted `SessionInput` reaches the server and the returned
`SessionResult` flows into the report — so the http stand-in agent lives in the
test rather than in `agent.sh`.
