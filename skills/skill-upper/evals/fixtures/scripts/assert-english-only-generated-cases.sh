#!/usr/bin/env bash
set -euo pipefail

python3 - <<'PY'
import os
import pathlib
import re
import sys

cjk_pattern = re.compile(r"[\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff\u3000-\u303f\uff00-\uffef]")
ascii_word_pattern = re.compile(r"[A-Za-z]{3,}")

failures = []

final_message = os.environ.get("EVAL_FINAL_MESSAGE", "")
if not final_message.strip():
    failures.append("Final response is empty.")
elif cjk_pattern.search(final_message):
    failures.append("Final response contains Chinese/CJK characters.")
elif len(ascii_word_pattern.findall(final_message)) < 5:
    failures.append("Final response does not contain enough English words.")

case_dir = pathlib.Path("evals/cases")
case_files = sorted(case_dir.glob("*.yaml")) if case_dir.exists() else []
if not case_files:
    failures.append("No generated eval case YAML files were found under evals/cases/.")

generated_yaml_files = []
eval_yaml = pathlib.Path("evals/eval.yaml")
if not eval_yaml.exists():
    failures.append("Generated evals/eval.yaml was not found.")
else:
    generated_yaml_files.append(eval_yaml)
generated_yaml_files.extend(case_files)

for path in generated_yaml_files:
    text = path.read_text(encoding="utf-8")
    match = cjk_pattern.search(text)
    if match:
        line_no = text[: match.start()].count("\n") + 1
        failures.append(f"{path}:{line_no} contains Chinese/CJK characters.")

def file_lines(path):
    return path.read_text(encoding="utf-8").splitlines()

def is_explanatory_comment(line):
    stripped = line.lstrip()
    if not stripped.startswith("#"):
        return False
    content = stripped[1:].strip()
    if not content:
        return False
    if content.startswith("-"):
        return False
    return not re.match(r"^[A-Za-z0-9_.-]+:\s*", content)

def has_adjacent_explanatory_comment(lines, index, stop_index=0):
    cursor = index - 1
    found = False
    while cursor >= stop_index:
        line = lines[cursor]
        if not line.strip():
            return found
        if not line.lstrip().startswith("#"):
            return found
        found = is_explanatory_comment(line) or found
        cursor -= 1
    return found

def has_comment_before(lines, field_line):
    for index, line in enumerate(lines):
        if line.lstrip().startswith("#"):
            continue
        if line.lstrip().startswith(field_line):
            return has_adjacent_explanatory_comment(lines, index)
    return False

def has_real_field(lines, field_line):
    return any(
        not line.lstrip().startswith("#") and line.lstrip().startswith(field_line)
        for line in lines
    )

def has_nested_comment_before(lines, parent, child):
    for index, line in enumerate(lines):
        if line.lstrip().startswith("#"):
            continue
        if line.startswith(parent):
            for child_index in range(index + 1, min(len(lines), index + 8)):
                if lines[child_index].lstrip().startswith("#"):
                    continue
                if lines[child_index].startswith(child):
                    return has_adjacent_explanatory_comment(lines, child_index, index + 1)
    return False

if eval_yaml.exists():
    lines = file_lines(eval_yaml)
    eval_checks = [
        ("schema_version", has_comment_before(lines, "schema_version:")),
        ("environment.type", has_nested_comment_before(lines, "environment:", "  type:")),
        ("engine.name", has_nested_comment_before(lines, "engine:", "  name:")),
        ("cases.files", has_nested_comment_before(lines, "cases:", "  files:")),
        ("report.formats", has_nested_comment_before(lines, "report:", "  formats:")),
    ]
    if has_real_field(lines, "expect:"):
        eval_checks.append(("expect", has_comment_before(lines, "expect:")))
    if has_real_field(lines, "judge:"):
        eval_checks.append(("judge", has_comment_before(lines, "judge:")))
    for field, passed in eval_checks:
        if not passed:
            failures.append(f"evals/eval.yaml is missing a field-leading comment for {field}.")

for path in case_files:
    lines = file_lines(path)
    case_checks = [
        ("id", has_comment_before(lines, "id:")),
        ("title", has_comment_before(lines, "title:")),
        ("input.prompt", has_nested_comment_before(lines, "input:", "  prompt:")),
        ("judge.type", has_nested_comment_before(lines, "judge:", "  type:")),
    ]
    if has_real_field(lines, "expect:"):
        case_checks.append(("expect", has_comment_before(lines, "expect:")))
    for field, passed in case_checks:
        if not passed:
            failures.append(f"{path} is missing a field-leading comment for {field}.")

if failures:
    print("English-only language check failed:")
    for failure in failures:
        print(f"- {failure}")
    sys.exit(1)

print("PASS: final response is English-like, generated YAML is English-only, and key fields have comments.")
PY
