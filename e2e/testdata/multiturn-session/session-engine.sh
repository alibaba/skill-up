#!/bin/bash
# session-engine.sh — a mock that impersonates qodercli closely enough to
# exercise skill-up's *session handling* (which the plain mock-engine cannot,
# because it never writes a session transcript).
#
# Faithful behaviours that matter for multi-turn evaluation:
#   1. Session transcripts live at
#      $HOME/.qoder/projects/<project-key>/<session-id>.jsonl, where
#      <project-key> is the working directory with every non-alphanumeric
#      character replaced by "-" (the CLI's own naming rule).
#   2. The first assistant event of a turn carries only a tool_use block; the
#      tool result and the real answer arrive afterwards, and the tool result is
#      recorded as a "user" event.
#   3. A Skill invocation also writes a sub-agent transcript under
#      <session-id>/subagents/, inside the same project tree.
#   4. Context is recalled only when -r resumes a session file that exists;
#      otherwise the turn behaves like a brand-new conversation.
set -euo pipefail

if [[ "${1:-}" == "--version" ]]; then
  echo "qodercli 0.0.0-test"
  exit 0
fi

PROMPT=""
RESUME=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -p)
      shift
      PROMPT="${1:-}"
      ;;
    -r)
      shift
      RESUME="${1:-}"
      ;;
  esac
  shift || true
done

# Large prompts are piped in on stdin as `-p -`.
if [[ -z "$PROMPT" || "$PROMPT" == "-" ]]; then
  if [[ ! -t 0 ]]; then
    PROMPT="$(cat)"
  fi
fi

# Flatten the prompt so it can be embedded in a JSON string literal.
FLAT_PROMPT="$(printf '%s' "$PROMPT" | tr '\n\r' '  ' | tr '"' "'" | sed 's/[[:space:]]\{1,\}/ /g; s/^ //; s/ $//')"

PROJECT_KEY="$(printf '%s' "$PWD" | sed 's/[^a-zA-Z0-9]/-/g')"
ROOT="$HOME/.qoder/projects/$PROJECT_KEY"
mkdir -p "$ROOT"

rand_hex() {
  od -An -tx1 -N"$1" /dev/urandom | tr -d ' \n'
}

RECALLED=""
if [[ -n "$RESUME" && -f "$ROOT/$RESUME.jsonl" ]]; then
  SESSION_ID="$RESUME"
  # Only reachable when the resumed session file is the one this mock wrote
  # earlier: recall the very first user prompt of that session.
  RECALLED="$(sed -n 's/^{"type":"user","cwd":"[^"]*","message":{"content":"\([^"]*\)".*/\1/p' "$ROOT/$SESSION_ID.jsonl" | head -1)"
else
  SESSION_ID="$(rand_hex 4)-$(rand_hex 2)-$(rand_hex 2)-$(rand_hex 2)-$(rand_hex 6)"
fi

SESSION_FILE="$ROOT/$SESSION_ID.jsonl"
TOOL_ID="toolu_$(rand_hex 6)"

if [[ -n "$RECALLED" ]]; then
  ANSWER="context-recall: ${RECALLED} | apologies, escalating to the support desk"
else
  ANSWER="TIER11 answer, source: alidocs.dingtalk.com/i/p/tiering"
fi

{
  printf '{"type":"workspace-directories","sessionId":"%s","directories":["%s"]}\n' "$SESSION_ID" "$PWD"
  printf '{"type":"user","cwd":"%s","message":{"content":"%s","role":"user"}}\n' "$PWD" "$FLAT_PROMPT"
  printf '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"%s","name":"Skill","input":{"skill":"kb"}}],"role":"assistant"}}\n' "$TOOL_ID"
  printf '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"%s","content":"Launching skill: kb"}],"role":"user"}}\n' "$TOOL_ID"
  printf '{"type":"assistant","message":{"content":[{"type":"text","text":"%s"}],"role":"assistant"}}\n' "$ANSWER"
} >>"$SESSION_FILE"

# The Skill call spawns a sub-agent, whose transcript lands inside the project
# tree and is the most recently modified file there.
SUBAGENT_FILE="$ROOT/$SESSION_ID/subagents/agent-aExplore-$(rand_hex 4).jsonl"
mkdir -p "$(dirname "$SUBAGENT_FILE")"
printf '{"type":"user","message":{"content":"sub-agent scratch work","role":"user"}}\n' >"$SUBAGENT_FILE"
touch -t 203001010000 "$SUBAGENT_FILE"

echo "$ANSWER"
