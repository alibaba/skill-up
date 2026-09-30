# Code Stats Skill

A real evaluable skill that analyzes code files and reports statistics. Works with both **skill-creator** and **skill-up**.

## Structure

```
code-stats/
├── SKILL.md                         # Skill definition with instructions
└── evals/
    ├── eval.yaml                    # skill-up config (YAML format)
    ├── evals.json                   # skill-creator format
    ├── cases/
    │   ├── analyze-directory.yaml   # Test: analyze directory
    │   ├── top-extensions.yaml      # Test: find top extensions
    │   ├── current-directory-format.yaml
    │   └── exclude-dependencies.yaml
    └── fixtures/
        ├── scripts/
        │   └── check-stats.sh       # Evaluation script
        └── repos/
            └── sample-project/       # Test repository
                ├── main.go
                ├── util.go
                └── README.md
```

## How to Evaluate

### With skill-creator

```bash
# Run evaluation using skill-creator workflow
claude -p "$(cat <<'EOF'
Create a skill evaluation for ./examples/code-stats using skill-creator.
EOF
)"
```

### With skill-up (Go framework)

The YAML suite uses OpenCode with `dashscope/qwen3.8-max` at
`https://dashscope.aliyuncs.com/compatible-mode/v1`. Install OpenCode and inject
`DASHSCOPE_API_KEY` through your environment or secret manager; keep it out of YAML.
The endpoint and model must be available to that key.

```bash
# From the repository root; DASHSCOPE_API_KEY must already be injected.
make build
./bin/skill-up validate ./examples/code-stats/evals/eval.yaml
./bin/skill-up run ./examples/code-stats/evals/eval.yaml \
  --include-case-name analyze-directory --parallelism 1 \
  --format json --format html --format junit
```

`analyze-directory` reuses `check-stats.sh` as the `format` script judge and adds
an independent `relevance` Agent judge. The other cases retain their singular
script judges. The evaluated agent runs once per configuration; semantic review
uses a separate Agent session over the same completed output. `all_required`
requires both judges to pass. Benchmark mode also runs a separate `without_skill`
configuration; that is a baseline evaluation, not a second execution for judging.

The prompt limits the project to `main.go`, `util.go`, and `README.md`, and gates
check 3 files and 20 lines. This excludes OpenCode's installed Skill and generated
configuration from project statistics. The reused script checks report formatting;
the semantic rubric checks that an actual, complete report answers the request.

## Verified behavior

The selected case was exercised with real OpenCode 1.14.24 / Qwen3.8-Max calls
on 2026-09-30: both judges passed with the Skill. A controlled failure in a scratch
copy produced script FAIL, semantic PASS and aggregate FAIL; the semantic judge
could not see a file written by the script judge. The baseline failed the exact
output-format gates, so both judges were skipped.

See the [verification report and reproduction steps](../../docs/design/multi-judge.md#live-qwen-verification)
for evidence boundaries and the negative-test procedure. This does not claim a
live-model run of all four cases.

## Dual Format Support

| Tool | Config File | Format |
|------|-------------|--------|
| skill-creator | `evals/evals.json` | JSON with `skill_name`, `evals[]` array |
| skill-up | `evals/eval.yaml` + `cases/*.yaml` | YAML with schema v1alpha1 |

For `skill-up`, case file paths, `repo_fixture`, and `script_path` are relative to the Skill root (the directory containing `SKILL.md`). Use `evals/cases/...` and `evals/fixtures/...` as in this example.

## Test Cases

1. **analyze-directory**: Check the fixed project with script and semantic judges
2. **top-extensions**: Find top file extensions by line count
3. **current-directory-format**: Check the required report sections
4. **exclude-dependencies**: Verify dependency-directory exclusions

## Evaluation Script

`check-stats.sh` validates:
- Agent exit code is recorded as a warning if nonzero; script exit code determines its verdict
- Output contains "Files by Extension"
- Output contains "Total Files" and "Total Lines"
- Markdown table format with `|` characters
- No error indicators
