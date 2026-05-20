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

for path in case_files:
    text = path.read_text(encoding="utf-8")
    match = cjk_pattern.search(text)
    if match:
        line_no = text[: match.start()].count("\n") + 1
        failures.append(f"{path}:{line_no} contains Chinese/CJK characters.")

if failures:
    print("English-only language check failed:")
    for failure in failures:
        print(f"- {failure}")
    sys.exit(1)

print("PASS: final response is English-like, and generated eval case YAML files contain no Chinese/CJK characters.")
PY
