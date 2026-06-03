#!/bin/bash
# custom-engine/agent.sh — a deterministic mock for the Custom Engine local
# transport. The agent receives the SessionInput JSON path as its first arg
# (per the eval.yaml in this directory) and emits a SessionResult on stdout.
#
# The script intentionally ignores the input content and returns a fixed
# response so the e2e test is deterministic. It still verifies that the
# framework actually wrote the input file at the configured path, which is the
# main piece of the transport contract this test exercises.
set -euo pipefail

INPUT_FILE="${1:-}"
if [[ -z "$INPUT_FILE" || ! -f "$INPUT_FILE" ]]; then
  echo "agent.sh: SessionInput file not provided or missing (got: '${INPUT_FILE:-}')" >&2
  exit 1
fi

cat <<'JSON'
{
  "exit_code": 0,
  "final_message": "custom-engine-handled",
  "turns": 1,
  "transcript": [
    {"role": "user", "content": "ping"},
    {"role": "assistant", "content": "custom-engine-handled"}
  ]
}
JSON
