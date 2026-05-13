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
    │   └── top-extensions.yaml      # Test: find top extensions
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
Create a skill evaluation for /Users/zhaoping/work/skill-up/examples/code-stats using skill-creator.
EOF
)"
```

### With skill-up (Go framework)

```bash
# Build and run
cd /Users/zhaoping/work/skill-up
make build
./bin/skill-up run ./examples/code-stats/evals/eval.yaml
```

## Dual Format Support

| Tool | Config File | Format |
|------|-------------|--------|
| skill-creator | `evals/evals.json` | JSON with `skill_name`, `evals[]` array |
| skill-up | `evals/eval.yaml` + `cases/*.yaml` | YAML with schema v1alpha1 |

For `skill-up`, case files in `evals/eval.yaml` stay relative to `eval.yaml`, while fixture-style references such as `repo_fixture` and `script_path` are relative to the directory containing `SKILL.md`, so they should be written as `evals/fixtures/...`.

## Test Cases

1. **analyze-directory**: Analyze the evals directory and report statistics
2. **top-extensions**: Find top file extensions by line count

## Evaluation Script

`check-stats.sh` validates:
- Exit code is 0
- Output contains "Files by Extension"
- Output contains "Total Files" and "Total Lines"
- Markdown table format with `|` characters
- No error indicators
