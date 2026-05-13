# script-judge-eval

## Test Objectives

Verify the complete ScriptJudge flow: from environment variable injection and script execution to result parsing. ScriptJudge evaluates agent output by running a user-provided bash script; the script's exit code determines PASS/FAIL.

## Features Covered

- `ScriptJudge.Evaluate()` complete execution flow
- Script environment variable injection: `EVAL_TRANSCRIPT_PATH`, `EVAL_FINAL_MESSAGE`, `EVAL_EXIT_CODE`
- Script working directory set to the case workspace
- Exit code 0 → PASS, non-0 → FAIL
- stdout as evidence, stderr as debug info
- Script timeout handling

## Included Files

| File | Purpose |
|------|---------|
| `SKILL.md` | Simulated skill: data transformer |
| `evals/eval.yaml` | Eval config, judge.type=script |
| `evals/cases/csv-to-json.yaml` | Case: convert CSV to JSON |
| `fixtures/scripts/check-transform.sh` | Judge script (checks output file format and content) |
| `fixtures/workspace/input.csv` | Input CSV file |
| `fixtures/workspace/output.json` | Simulated agent-generated JSON output |
| `fixtures/judge-inputs/csv-to-json.json` | Pre-built judge.Input |
| `fixtures/expected/csv-to-json-grading.json` | Expected PASS grading result |

## Prerequisites

Build the binary from the project root:

```bash
cd /path/to/skill-up   # replace with actual project root
make build
```

## How to Test

### 1. Validate only the ScriptJudge module (CLI command)

ScriptJudge requires a real workspace and an executable script. The working directory during script execution is the workspace, so `script_path` must use an absolute path.

```bash
cd e2e/testdata/script-judge-eval

# Create a temporary workspace and copy files
TMP_WS=$(mktemp -d)
cp -r fixtures/workspace/* "$TMP_WS/"

# Get the absolute path of the script (ScriptJudge executes in the workspace directory; relative paths cannot be resolved)
SCRIPT_ABS=$(pwd)/fixtures/scripts/check-transform.sh

# Substitute workspace_path and script_path with absolute paths
sed "s|__WORKSPACE__|$TMP_WS|" fixtures/judge-inputs/csv-to-json.json \
  | sed "s|fixtures/scripts/check-transform.sh|$SCRIPT_ABS|" \
  > /tmp/judge-input-script.json
../../../bin/skill-up debug judge /tmp/judge-input-script.json
```

Expected output:
```
[expect] PASS — all pre-checks passed
[judge]  PASS — 1/1 assertions passed (pass_rate: 100.0%)
[output] grading.json
```

Generated `grading.json` contents:
- status: "PASS"
- 1 assertion:
  - `script: <script_path>` — passed
  - evidence: contains "All checks passed" message
- summary: passed=1, failed=0, pass_rate=1.0

> **Note**: stderr will output `warning: ScriptJudge.TranscriptPath is empty` — this is normal debug mode behavior and does not affect test results.

### 2. Test directly in Go test code

```go
workspace := t.TempDir()
copyDir("fixtures/workspace", workspace)

scriptPath := filepath.Join(testdataDir, "fixtures/scripts/check-transform.sh")
j := &judge.ScriptJudge{ScriptPath: scriptPath}

input := judge.Input{
    CaseID:        "csv-to-json",
    WorkspacePath: workspace,
    FinalMessage:  "Converted input.csv to JSON format and wrote output.json",
    ExitCode:      0,
}
result, _ := j.Evaluate(ctx, input)
assert.Equal(t, judge.StatusPass, result.Status)
assert.Contains(t, result.AssertionResults[0].Evidence, "All checks passed")
```

### 3. CLI end-to-end test

```bash
../../../bin/skill-up run --config evals/eval.yaml
```

Expected: runs the complete pipeline; ScriptJudge executes the script and determines PASS/FAIL based on exit code.

## Why passing means the feature is complete

- Script correctly received environment variables and executed in the correct working directory
- Script can inspect agent-generated file contents and formats
- Script exit code is correctly mapped to judge.Status
- Evidence correctly captures the script's stdout output
