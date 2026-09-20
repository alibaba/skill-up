#!/usr/bin/env bash
set -euo pipefail

input_file="$1"
if grep -q '"session_id":"session-1"' "$input_file"; then
  printf '%s\n' '{"exit_code":0,"final_message":"used token abc"}'
else
  printf '%s\n' '{"exit_code":0,"session_id":"session-1","final_message":"token=abc"}'
fi
