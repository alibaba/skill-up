# expect-short-circuit

## Test Objective

Verify the short-circuit semantics of the Expect pre-check layer: when an Expect check fails, the Judge is never called; failure information is converted directly into AssertionResults and written to grading.json.

## Features Covered

- All four check types of `CheckExpect()`: must_contain, must_not_contain, exit_code, files_exist
- Short-circuit semantics: Expect fails → Judge.Evaluate() is not called (saves LLM tokens)
- `ExpectResult.ToAssertionResults()` conversion: ExpectFailure → AssertionResult
- Aggregation behavior when multiple failures occur simultaneously

## Files

| File | Purpose |
|------|---------|
| `SKILL.md` | Simulated skill: quick calculator |
| `evals/eval.yaml` | Eval configuration |
| `evals/cases/basic-arithmetic.yaml` | Case: must_contain check fails |
| `evals/cases/exit-code-mismatch.yaml` | Case: exit_code mismatch |
| `fixtures/judge-inputs/basic-arithmetic.json` | Output missing must_contain keywords |
| `fixtures/judge-inputs/exit-code-mismatch.json` | exit_code=1 but expect requires 0 |
| `fixtures/expected/basic-arithmetic-grading.json` | FAIL; assertions come from expect layer |
| `fixtures/expected/exit-code-mismatch-grading.json` | FAIL; exit_code mismatch |

## Prerequisites

Build the binary from the project root:

```bash
cd /path/to/skill-up   # replace with the actual project root
make build
```

## How to Test

### 1. Validate Expect + Judge module only (CLI command)

**Case A: basic-arithmetic (Expect fails — must_contain + must_not_contain)**

```bash
cd e2e/testdata/expect-short-circuit
../../../bin/skill-up debug judge fixtures/judge-inputs/basic-arithmetic.json
```

Expected output:
```
[expect] FAIL — 2 checks failed, judge skipped
[judge]  FAIL — 0/2 assertions passed (pass_rate: 0.0%)
[output] grading.json
```

Generated `grading.json`:
- status: "FAIL"
- 2 assertions all failed (from the expect layer, not judge):
  - `expect.must_contain` — output missing "calculation result" keyword
  - `expect.must_not_contain` — output contains forbidden word "error"
- **Key verification point**: Judge was never called (short-circuit); all assertions have `expect.` prefix

**Case B: exit-code-mismatch (Expect fails — exit_code + files_exist)**

```bash
../../../bin/skill-up debug judge fixtures/judge-inputs/exit-code-mismatch.json
```

Expected output:
```
[expect] FAIL — 2 checks failed, judge skipped
[judge]  FAIL — 0/2 assertions passed (pass_rate: 0.0%)
[output] grading.json
```

Generated `grading.json`:
- status: "FAIL"
- 2 assertions all failed:
  - `expect.exit_code` — expected exit_code=0, actual is 1
  - `expect.files_exist` — "output.txt" does not exist
- **Key verification point**: Judge was never called; multiple Expect failures are aggregated simultaneously

### 2. Verifying short-circuit semantics in Go test code

```go
// Load fixtures and construct mockJudge
data, _ := os.ReadFile("fixtures/judge-inputs/basic-arithmetic.json")
var debugInput judgeDebugInput
json.Unmarshal(data, &debugInput)

input := debugInput.toJudgeInput()
mockJudge := &countingJudge{} // tracks whether it was called

result, _ := runEvalPipeline(ctx, debugInput.Expect, mockJudge, input)
assert.Equal(t, judge.StatusFail, result.Status)
assert.Equal(t, 0, mockJudge.callCount) // Judge was not called!
```

### 3. CLI end-to-end test

```bash
../../../bin/skill-up run --config evals/eval.yaml
```

Expected: executes the full pipeline; cases where Expect fails are directly marked FAIL without invoking the Judge.

## Why passing means the feature is complete

- basic-arithmetic passing proves must_contain checks can correctly detect missing keywords
- exit-code-mismatch passing proves exit_code checks can correctly detect exit code mismatches
- Together, both cases verify the short-circuit semantics, ensuring the expensive Judge is not called when pre-checks fail
