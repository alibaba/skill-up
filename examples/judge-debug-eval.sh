#!/bin/sh
# Evaluation script: check whether the Agent output identified a bug and provided a fix suggestion
if echo "$EVAL_FINAL_MESSAGE" | grep -q "bug"; then
  if echo "$EVAL_FINAL_MESSAGE" | grep -q "fix\|nil"; then
    echo "Agent identified the bug AND provided a fix"
    exit 0
  else
    echo "Agent identified the bug but did NOT provide a fix"
    exit 1
  fi
else
  echo "Agent failed to identify any bug"
  exit 1
fi
