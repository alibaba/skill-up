# full-report-pipeline

## Test Objectives

Verify the complete report module pipeline: generating a full set of JSON/JUnit/HTML/Anthropic report artifacts from CaseResults covering all four statuses (PASS/FAIL/SKIP/ERROR).

## Features Covered

- `JSONReporter.Write()` → JSON report completeness
- `JUnitReporter.Write()` → XML testsuites structure correctness
- `HTMLReporter.Write()` → review.html rendering
- `ConvertToAnthropicGrading()` → grading.json Anthropic-compatible format
- `ComputeAnthropicBenchmark()` → benchmark.json statistical calculations
- `GenerateBenchmarkMD()` → benchmark.md Markdown table
- `IterationWorkspace` → iteration directory structure creation and file writing
- nil Grading safe handling (ERROR case Grading field is nil)
- Correct mapping and statistics for all four Status values

## Included Files

| File | Purpose |
|------|---------|
| `SKILL.md` | Simulated skill: code quality analyzer |
| `evals/eval.yaml` | Eval config, report.formats=[json,junit,html] |
| `evals/cases/pass-case.yaml` | PASS case: successfully identified bug |
| `evals/cases/fail-case.yaml` | FAIL case: missed critical issue |
| `evals/cases/skip-case.yaml` | SKIP case: precondition not met |
| `evals/cases/error-case.yaml` | ERROR case: engine execution timed out |
| `fixtures/report-input.json` | Pre-built report.Input (4 CaseResults) |
| `fixtures/expected/grading-pass.json` | AnthropicGrading for PASS case |
| `fixtures/expected/grading-fail.json` | AnthropicGrading for FAIL case |
| `fixtures/expected/benchmark.json` | AnthropicBenchmark statistics |
| `fixtures/expected/eval-metadata.json` | EvalMetadata example |

## Prerequisites

Build the binary from the project root:

```bash
cd /path/to/skill-up   # replace with actual project root
make build
```

## How to Test

### 1. Validate only the Report module (CLI command)

`fixtures/report-input.json` is a pre-built `report.Input` JSON containing 4 CaseResults (PASS/FAIL/SKIP/ERROR), ready for direct use with the `debug report` command.

**Generate JSON format report**

```bash
cd e2e/testdata/full-report-pipeline
../../../bin/skill-up debug report fixtures/report-input.json --format json
```

Expected output:
```
[report] json format — 4 cases (pass_rate: 25%)
[output] result.json
```

Generated `result.json` contents:
- Contains 4 case_results with PASS/FAIL/SKIP/ERROR statuses
- skill_name: "code-quality-analyzer"
- Overall pass_rate: 0.25 (25%, only 1 PASS)
- ERROR case grading is null, but error field has a value

**Generate JUnit XML format report**

```bash
../../../bin/skill-up debug report fixtures/report-input.json --format junit
```

Expected output:
```
[report] junit format — 4 cases (pass_rate: 25%)
[output] report.xml
```

Generated `report.xml` contents:
- `<testsuites>` contains 1 `<testsuite>` with tests="4", failures="1", errors="1", skipped="1"
- PASS case: `<testcase>` with no child elements
- FAIL case: `<testcase>` with `<failure>` element
- SKIP case: `<testcase>` with `<skipped>` element
- ERROR case: `<testcase>` with `<error>` element

**Generate HTML format report**

```bash
../../../bin/skill-up debug report fixtures/report-input.json --format html
```

Expected output:
```
[report] html format — 4 cases (pass_rate: 25%)
[output] report.html
```

Generated `report.html` contents:
- HTML report openable in a browser
- Includes detailed info for all 4 cases plus overall statistics
- Each case shows status, duration, and assertion details

### 2. Validate Anthropic-compatible format

Anthropic-compatible format validation requires Go test code:

```go
// Load report-input.json
data, _ := os.ReadFile("fixtures/report-input.json")
var input report.Input
json.Unmarshal(data, &input)

// Generate AnthropicGrading for PASS case Grading
grading := report.ConvertToAnthropicGrading(input.CaseResults[0].Grading)

// Compare against expected output
expected, _ := os.ReadFile("fixtures/expected/grading-pass.json")
// grading should match expected

// Verify nil Grading safe handling (ERROR case)
errorGrading := report.ConvertToAnthropicGrading(input.CaseResults[3].Grading)
// Should return empty expectations list, not panic
```

### 3. CLI end-to-end test

```bash
../../../bin/skill-up run --config evals/eval.yaml
```

Expected: runs the complete pipeline and generates the full artifact set: grading.json + benchmark.json + benchmark.md + review.html.

## Why passing means the feature is complete

- Full coverage of all 4 statuses ensures the report module correctly handles every possible evaluation result
- Three format outputs verify completeness of report generation
- Anthropic-compatible format verifies interoperability with external tools (eval-viewer)
- nil Grading handling verifies safety in edge cases
