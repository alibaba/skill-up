# text-match-rules

## Test Objectives

Verify `RuleBasedJudge` text-matching rules and exit_code rules, as well as the priority logic between success and failure rules.

## Features Covered

- `output_contains.all`: output must contain all specified keywords
- `output_contains.any`: output must contain at least one specified keyword
- `exit_code`: process exit code check
- success rules use AND logic: all success rules must pass for a PASS result
- failure rule priority: a matching failure rule immediately returns FAIL without evaluating success rules
- `NewResult()` Summary statistics calculation

## Included Files

| File | Purpose |
|------|---------|
| `SKILL.md` | Simulated skill: code review assistant |
| `evals/eval.yaml` | Eval config, judge.type=rule_based |
| `evals/cases/find-null-bug.yaml` | PASS case: output contains "null"+"bug", exit_code=0 |
| `evals/cases/miss-boundary-check.yaml` | FAIL case: output is missing required keywords |
| `fixtures/judge-inputs/find-null-bug.json` | Pre-built judge.Input (with transcript + FinalMessage) |
| `fixtures/judge-inputs/miss-boundary-check.json` | Pre-built judge.Input (missing keywords) |
| `fixtures/expected/find-null-bug-grading.json` | Expected PASS grading result |
| `fixtures/expected/miss-boundary-check-grading.json` | Expected FAIL grading result |

## Prerequisites

Build the binary from the project root:

```bash
cd /path/to/skill-up   # replace with actual project root
make build
```

## How to Test

### 1. Validate only the Judge module (CLI command)

The judge-inputs JSON files are self-contained, including judge.Input data + expect/judge config, ready for direct use with the `debug judge` command.

**Case A: find-null-bug (expected PASS)**

```bash
cd e2e/testdata/text-match-rules
../../../bin/skill-up debug judge fixtures/judge-inputs/find-null-bug.json
```

Expected output:
```
[expect] PASS — all pre-checks passed
[judge]  PASS — 3/3 assertions passed (pass_rate: 100.0%)
[output] grading.json
```

Generated `grading.json` contents: status is "PASS", all 3 assertions passed (output_contains.all + output_contains.any + exit_code), summary.pass_rate is 1.0.

**Case B: miss-boundary-check (expected FAIL)**

```bash
../../../bin/skill-up debug judge fixtures/judge-inputs/miss-boundary-check.json
```

Expected output:
```
[expect] PASS — all pre-checks passed
[judge]  FAIL — 1/3 assertions passed (pass_rate: 33.3%)
[output] grading.json
```

Generated `grading.json` contents: status is "FAIL", output_contains.all assertion fails (output does not contain the "null" keyword), output_contains.any assertion fails (output contains none of "severe"/"critical"/"high-risk"), exit_code assertion passes. summary.pass_rate is 0.33.

### 2. Load fixtures in Go test code

```go
// Load judge-inputs JSON (ignoring expect/judge config fields)
data, _ := os.ReadFile("fixtures/judge-inputs/find-null-bug.json")
var input judge.Input
json.Unmarshal(data, &input)

// Load eval.yaml to build JudgeConfig
cfg, _ := config.LoadEval("evals/eval.yaml")
j := judge.NewRuleBasedJudge(cfg.Judge)
result, _ := j.Evaluate(ctx, input)

// Compare against expected
expectedData, _ := os.ReadFile("fixtures/expected/find-null-bug-grading.json")
```

### 3. CLI end-to-end test (full pipeline)

```bash
../../../bin/skill-up run --config evals/eval.yaml
```

Expected: runs the complete Engine → Expect → Judge → Report pipeline and generates evaluation artifacts.

## Why passing means the feature is complete

- `find-null-bug` passing means output_contains.all + exit_code rules correctly match positive output
- `miss-boundary-check` passing means rules correctly reject non-compliant output
- The two cases together cover both directions of RuleBasedJudge behavior, plus correct Summary calculation
