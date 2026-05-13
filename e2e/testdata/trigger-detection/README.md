# trigger-detection

## Test Objectives

Verify trigger-test (`trigger_test` tag) scenarios: whether the agent can correctly detect user emotions and activate the corresponding skill.

## Features Covered

- Test cases tagged with `tag: trigger_test`
- Emotional keyword trigger detection
- `output_contains.any` rule: output must contain at least one expected keyword
- Expect layer `must_contain` check
- Case-level judge config override (the judge in case.yaml overrides the global judge in eval.yaml)
- Anthropic-compatible evals.json format

## Included Files

| File | Purpose |
|------|---------|
| `SKILL.md` | Simulated skill: mood booster assistant |
| `evals/eval.yaml` | Eval config |
| `evals/cases/detect-boredom.yaml` | trigger_test: user says "I'm so bored" |
| `evals/cases/detect-frustration.yaml` | trigger_test: user says "I'm so frustrated" |
| `evals/evals.json` | Anthropic-compatible format |
| `fixtures/judge-inputs/detect-boredom.json` | Transcript where agent correctly triggers the skill |
| `fixtures/judge-inputs/detect-frustration.json` | Transcript where agent correctly triggers the skill |
| `fixtures/expected/detect-boredom-grading.json` | Expected PASS |

## Prerequisites

Build the binary from the project root:

```bash
cd /path/to/skill-up   # replace with actual project root
make build
```

## How to Test

### 1. Validate only the Judge module (CLI command)

The judge-inputs JSON files include case-level judge config (overriding global config) and are ready for direct use with the `debug judge` command.

**Case A: detect-boredom (expected PASS — user says "I'm so bored")**

```bash
cd e2e/testdata/trigger-detection
../../../bin/skill-up debug judge fixtures/judge-inputs/detect-boredom.json
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
  - `output_contains` — passed, because output contains "joke" and "😄"
- evidence: "all output_contains checks passed"
- Verifies that the agent correctly detects the "boredom" emotion and activates the mood-booster skill

**Case B: detect-frustration (expected PASS — user says "I'm so frustrated")**

```bash
../../../bin/skill-up debug judge fixtures/judge-inputs/detect-frustration.json
```

Expected output:
```
[expect] PASS — all pre-checks passed
[judge]  PASS — 1/1 assertions passed (pass_rate: 100.0%)
[output] grading.json
```

Generated `grading.json` contents:
- status: "PASS"
- Output contains "joke" and "keep going" keywords
- Verifies the combined comfort + joke response for strong negative emotions

### 2. Verify case-level judge config override in Go test code

```go
// Load global config and case config
eval, _ := config.LoadEval("evals/eval.yaml")
caseConfig, _ := config.LoadCase("evals/cases/detect-boredom.yaml")

// MergeJudgeConfig: case-level judge overrides global judge
merged := judge.MergeJudgeConfig(eval.Judge, caseConfig.Judge)
// merged.Type should be "rule_based"
// merged.Success should contain output_contains.any rule

j := judge.NewRuleBasedJudge(merged)

// Load judge-inputs
data, _ := os.ReadFile("fixtures/judge-inputs/detect-boredom.json")
var input judge.Input
json.Unmarshal(data, &input)

result, _ := j.Evaluate(ctx, input)
assert.Equal(t, judge.StatusPass, result.Status)
```

### 3. CLI end-to-end test

```bash
../../../bin/skill-up run --config evals/eval.yaml
```

Expected: runs the complete pipeline and verifies whether the agent correctly activates the mood-booster skill.

## Why passing means the feature is complete

- Two trigger_test cases cover different emotional trigger words
- output_contains.any rules verify that agent output matches the skill's expected behavior
- Case-level judge config override verifies the config merging logic
- evals.json verifies compatibility with Anthropic evaluation tools
