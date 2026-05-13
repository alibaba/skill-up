# MCP stdio marker fixture

Reusable local/CI fixture for verifying that a real agent can install and call
a stdio MCP server through `skill-up`. The fixture uses `node -e` so it can
run in the same local and CI images that provide Claude/Qoder CLIs.

Example:

```bash
go build -o bin/skill-up ./cmd/skill-up
bin/skill-up run e2e/testdata/mcp-stdio-marker/evals/eval.yaml --engine claude_code --model dashscope/qwen3.6-plus --output-dir /tmp/skill-up-mcp-stdio
bin/skill-up run e2e/testdata/mcp-stdio-marker/evals/eval.yaml --engine qodercli --model qoder/auto --output-dir /tmp/skill-up-mcp-stdio-qoder
```
