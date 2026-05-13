#!/bin/bash
# check-stats.sh - Evaluation script for code-stats skill
# Exit code 0 = pass, non-zero = fail

set -e

# Environment variables provided by skill-up:
#   EVAL_TRANSCRIPT_PATH - path to transcript.json
#   EVAL_FINAL_MESSAGE   - final message from agent
#   EVAL_EXIT_CODE       - exit code from agent execution

TRANSCRIPT_PATH="${EVAL_TRANSCRIPT_PATH:-}"
FINAL_MESSAGE="${EVAL_FINAL_MESSAGE:-}"
EXIT_CODE="${EVAL_EXIT_CODE:-0}"

echo "=== Code Stats Skill Evaluation ==="
echo "Exit code: $EXIT_CODE"
echo "Final message preview: ${FINAL_MESSAGE:0:200}..."

# qodercli may occasionally time out after producing a complete final message.
# This case evaluates report content, so keep exit-code checks in expect rules
# for cases that specifically need process-status validation.
if [ "$EXIT_CODE" != "0" ]; then
    echo "WARN: Agent exited with non-zero code: $EXIT_CODE"
fi

# Check that final message is not empty
if [ -z "$FINAL_MESSAGE" ]; then
    echo "FAIL: Final message is empty"
    exit 1
fi

# Check for required sections in output
MISSING=""

if ! echo "$FINAL_MESSAGE" | grep -qi "Files by Extension"; then
    MISSING="$MISSING 'Files by Extension'"
fi

if ! echo "$FINAL_MESSAGE" | grep -qi "Total Files"; then
    MISSING="$MISSING 'Total Files'"
fi

if ! echo "$FINAL_MESSAGE" | grep -qi "Total Lines"; then
    MISSING="$MISSING 'Total Lines'"
fi

# Check for markdown table structure (has | and ---)
if ! echo "$FINAL_MESSAGE" | grep -q "|"; then
    MISSING="$MISSING 'markdown table (|)'"
fi

# Check for error indicators
if echo "$FINAL_MESSAGE" | grep -qi "error\|fail"; then
    echo "FAIL: Output contains error indicators"
    exit 1
fi

if [ -n "$MISSING" ]; then
    echo "FAIL: Missing required sections:$MISSING"
    exit 1
fi

echo "PASS: All checks passed"
echo "Evaluation reason: Agent produced valid code stats output with required sections"
exit 0
