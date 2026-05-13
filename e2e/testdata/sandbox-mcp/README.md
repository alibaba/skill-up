# Sandbox MCP fixture

Reusable fixture for verifying real HTTP MCP provisioning against a Sandbox MCP
endpoint. The fixture ships with an `example.com` placeholder endpoint — set
`endpoint` in `evals/fixtures/mcp/agent-sandbox.yaml` to your actual Sandbox MCP
URL before running.

Required environment:

- `OPENSANDBOX_API_KEY`: token passed as the MCP endpoint query parameter.
- `SANDBOX_MCP_TOKEN`: token passed as the `PRIVATE-TOKEN` MCP header.

Example:

```bash
go build -o bin/skill-up ./cmd/skill-up
OPENSANDBOX_API_KEY=... SANDBOX_MCP_TOKEN=... \
  bin/skill-up run e2e/testdata/sandbox-mcp/evals/eval.yaml \
  --engine claude_code --model anthropic/qwen3.6-plus \
  --output-dir /tmp/skill-up-sandbox-mcp
```
