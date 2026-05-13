#!/bin/bash
# check-transform.sh — ScriptJudge evaluation script
#
# Contract:
#   - Working directory: case workspace root
#   - Environment variables: EVAL_TRANSCRIPT_PATH, EVAL_FINAL_MESSAGE, EVAL_EXIT_CODE
#   - Exit code 0 → PASS, non-0 → FAIL
#   - stdout → evidence, stderr → debug info
#
# Check logic:
#   1. output.json file exists
#   2. output.json is valid JSON
#   3. output.json contains 3 records
#   4. Each record contains name, age, city, email fields

set -euo pipefail

# Debug info goes to stderr
echo "check-transform.sh: working dir = $(pwd)" >&2
echo "check-transform.sh: EVAL_EXIT_CODE = ${EVAL_EXIT_CODE:-unset}" >&2

# Check 1: output.json exists
if [ ! -f "output.json" ]; then
    echo "FAIL: output.json does not exist"
    exit 1
fi

# Check 2: valid JSON (uses python3, available on macOS)
if ! python3 -c "import json; json.load(open('output.json'))" 2>/dev/null; then
    echo "FAIL: output.json is not valid JSON"
    exit 1
fi

# Check 3: contains 3 records
RECORD_COUNT=$(python3 -c "import json; data=json.load(open('output.json')); print(len(data))")
if [ "$RECORD_COUNT" != "3" ]; then
    echo "FAIL: expected 3 records, got $RECORD_COUNT"
    exit 1
fi

# Check 4: each record contains required fields
MISSING=$(python3 -c "
import json
data = json.load(open('output.json'))
required = {'name', 'age', 'city', 'email'}
for i, record in enumerate(data):
    missing = required - set(record.keys())
    if missing:
        print(f'record {i}: missing fields {missing}')
" 2>/dev/null)

if [ -n "$MISSING" ]; then
    echo "FAIL: $MISSING"
    exit 1
fi

# All checks passed
echo "All checks passed: output.json contains 3 valid records with all required fields"
exit 0
