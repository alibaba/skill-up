#!/usr/bin/env bash
# Run the routing canary (#166) against one or more engines and keep the
# artifacts for inspection.
#
#   scripts/routing-canary/run.sh                 # all engines
#   scripts/routing-canary/run.sh claude_code     # one engine
#   ITERATIONS=3 scripts/routing-canary/run.sh    # repeat for stability
#
# Artifacts are written to ./out/<engine>/ (relative to the current directory).
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
example="$root/examples/routing-canary"
work="$(mktemp -d "${TMPDIR:-/tmp}/routing-canary.XXXXXX")"
artifacts="${OUT:-out}"
iterations="${ITERATIONS:-1}"

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

  if ! "$work/skill-up" run "$fixture/evals/eval.yaml" \
    --engine "$engine" \
    --output-dir "$work/$engine/reports" \
    --no-delete \
    --iteration "$iterations"; then
    echo "!! $engine did not complete; its artifacts (if any) are kept under $artifacts/$engine"
  fi

  mkdir -p "$artifacts/$engine"
  if [ -d "$work/$engine/reports" ]; then
    cp -R "$work/$engine/reports/." "$artifacts/$engine/"
    "$root/scripts/routing-canary/summarize.py" "$artifacts/$engine" || true
  fi
done

echo
echo "artifacts: $(cd "$artifacts" && pwd)"
