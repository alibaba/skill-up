# file-and-tool-rules

## Test Objectives

Verify `RuleBasedJudge` filesystem checks (`files_exist` / `files_not_exist`), MCP tool-call checks (`tool_called` with partial argument matching), and `golden_file` comparison.

## Features Covered

- `files_exist`: checks that the agent generated the expected files in the workspace
- `files_not_exist`: checks that the agent did not generate files that should not exist (e.g. temp files)
- `tool_called`: checks whether the transcript contains the specified tool calls
- `tool_called` partial argument matching: validates only the specified argument subset, ignoring the rest
- `golden_file` (expect layer): compares agent output character-by-character against a baseline file

## Included Files

| File | Purpose |
|------|---------|
| `SKILL.md` | Simulated skill: file organizer assistant |
| `evals/eval.yaml` | Eval config, judge.type=rule_based |
| `evals/cases/organize-project.yaml` | Case: verify file generation + tool calls |
| `fixtures/workspace/report.md` | Simulated agent-generated review report |
| `fixtures/workspace/summary.json` | Simulated agent-generated summary file |
| `fixtures/golden/expected-report.md` | Golden file baseline (referenced by `evals/fixtures/golden/...`) |
| `fixtures/judge-inputs/organize-project.json` | Pre-built judge.Input |
| `fixtures/expected/organize-project-grading.json` | Expected output |

## Prerequisites

Build the binary from the project root:

```bash
cd /path/to/skill-up   # replace with actual project root
make build
```

## How to Test

### 1. Validate only the Judge module (CLI command)

Note: `files_exist/files_not_exist` checks use a workspace; `golden_file` uses a path relative to `SKILL.md`, so both the workspace must be prepared and `skill_dir` must be set in the debug input before running.

**Prepare the workspace and run**

```bash
cd e2e/testdata/file-and-tool-rules

# Create a temporary workspace and copy files
TMP_WS=$(mktemp -d)
cp -r fixtures/workspace/* "$TMP_WS/"

# Substitute workspace_path / skill_dir placeholders in the JSON and run
SKILL_DIR=$(pwd)
sed "s|__WORKSPACE__|$TMP_WS|" fixtures/judge-inputs/organize-project.json \
  | sed "s|__SKILL_DIR__|$SKILL_DIR|" \
  > /tmp/judge-input.json
../../../bin/skill-up debug judge /tmp/judge-input.json
```

Expected output:
```
[expect] PASS — all pre-checks passed
[judge]  PASS — 3/3 assertions passed (pass_rate: 100.0%)
[output] grading.json
```

Generated `grading.json` contents:
- status: "PASS"
- 3 assertions all passed:
  - `files_exist: [report.md summary.json]` — both files exist in workspace
  - `files_not_exist: [temp.log .cache]` — forbidden files do not exist
  - `tool_called: read_file (with args)` — transcript contains matching tool call

### 2. Load fixtures in Go test code

```go
// Copy workspace to a temporary directory
workspace := t.TempDir()
copyDir("fixtures/workspace", workspace)

// Load judge-inputs JSON and set workspace_path / skill_dir
data, _ := os.ReadFile("fixtures/judge-inputs/organize-project.json")
var input judge.Input
json.Unmarshal(data, &input)
input.WorkspacePath = workspace
input.SkillDir = testdataDir

// Build RuleBasedJudge and run
cfg, _ := config.LoadEval("evals/eval.yaml")
j := judge.NewRuleBasedJudge(cfg.Judge)
result, _ := j.Evaluate(ctx, input)
// result.Status should be "PASS"
```

### 3. Expect layer golden_file test

```bash
# golden_file comparison must be verified through Go test code
# CheckExpect will compare FinalMessage against evals/fixtures/golden/expected-report.md character by character
```

### 4. CLI end-to-end test

```bash
../../../bin/skill-up run --config evals/eval.yaml
```

Expected: runs the complete Engine → Expect → Judge → Report pipeline.

## Why passing means the feature is complete

- files_exist/files_not_exist passing means the judge can correctly check file state in the workspace
- tool_called passing means the judge can correctly extract tool calls from the transcript and perform partial argument matching
- golden_file passing means the expect layer can correctly resolve the skill root path and perform character-by-character content comparison
