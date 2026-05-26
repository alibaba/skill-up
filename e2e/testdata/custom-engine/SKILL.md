# Custom Engine e2e fixture

This directory is a minimal skill-up fixture that exercises the **Custom
Engine** local transport end-to-end. It is consumed by `e2e/custom_engine_test.go`.

`agent.sh` is a deterministic stand-in for a real custom agent: it reads the
`SessionInput` JSON the framework writes to `${input_file}` and emits a fixed
`SessionResult` on stdout. The case asserts that `final_message` flows from
the agent's stdout into the report.
