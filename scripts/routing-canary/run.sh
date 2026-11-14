#!/usr/bin/env bash
# Run the routing canary (#166) against one or more engines and keep the
# artifacts for inspection.
#
#   scripts/routing-canary/run.sh                 # all engines
#   scripts/routing-canary/run.sh claude_code     # one engine
#   ITERATIONS=3 scripts/routing-canary/run.sh    # repeat for stability
#   CANARY_MODEL=opencode/deepseek-v4.1-flash-free \
#     scripts/routing-canary/run.sh opencode      # pin the engine model
#
# Artifacts are written to ./out/<engine>/ (relative to the current directory).
# The script exits nonzero if any engine failed to produce a passing run, so a
# missing measurement cannot look like a clean result.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
example="$root/examples/routing-canary"
work="$(mktemp -d "${TMPDIR:-/tmp}/routing-canary.XXXXXX")"
artifacts="${OUT:-out}"
iterations="${ITERATIONS:-1}"
model="${CANARY_MODEL:-}"
failed=""

if [ "$#" -gt 0 ]; then
  engines=("$@")
else
  engines=(claude_code codex opencode)
fi

cleanup() {
  rm -rf "$work"
}
trap cleanup EXIT

echo "building skill-up from $root"
(cd "$root" && go build -o "$work/skill-up" ./cmd/skill-up)

for engine in "${engines[@]}"; do
  echo
  echo "== $engine"
  fixture="$work/$engine/fixture"
  mkdir -p "$work/$engine"
  cp -R "$example" "$fixture"
  rm -rf "$fixture/out"

  model_args=()
  if [ -n "$model" ]; then
    model_args=(--model "$model")
  fi

  if ! "$work/skill-up" run "$fixture/evals/eval.yaml" \
    --engine "$engine" \
    "${model_args[@]}" \
    --output-dir "$work/$engine/reports" \
    --no-delete \
    --iteration "$iterations"; then
    echo "!! $engine did not finish with a passing result"
    failed="$failed $engine"
  fi

  # Replace the previous run for this engine instead of overlaying it: an older
  # iteration-2/ would otherwise survive a later ITERATIONS=1 run and be read as
  # part of the latest result.
  rm -rf "${artifacts:?}/$engine"
  mkdir -p "$artifacts/$engine"
  if [ -d "$work/$engine/reports" ]; then
    cp -R "$work/$engine/reports/." "$artifacts/$engine/"
    if ! "$root/scripts/routing-canary/summarize.py" "$artifacts/$engine"; then
      echo "!! $engine summary failed"
      failed="$failed $engine-summary"
    fi
  else
    echo "!! $engine produced no artifacts"
    failed="$failed $engine-artifacts"
  fi
done

echo
echo "artifacts: $(cd "$artifacts" && pwd)"
if [ -n "$failed" ]; then
  echo "FAILED:$failed"
  exit 1
fi
