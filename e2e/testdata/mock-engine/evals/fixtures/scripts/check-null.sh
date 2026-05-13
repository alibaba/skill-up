#!/bin/bash
# check-null.sh - Script judge that checks if the output mentions "null"
# Exit 0 = PASS, non-0 = FAIL

if echo "$EVAL_FINAL_MESSAGE" | grep -qi "null"; then
  echo "Output correctly identifies null pointer issue"
  exit 0
else
  echo "Output does not mention null pointer issue"
  exit 1
fi
