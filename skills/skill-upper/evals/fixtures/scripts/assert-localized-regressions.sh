#!/usr/bin/env bash
set -euo pipefail
python3 - <<'PYCODE'
from pathlib import Path
import re
import subprocess
import os
import json
import shlex
import yaml
path = os.environ.get("EVAL_TRANSCRIPT_PATH")
assert path, "Missing authoring transcript"
for message in json.loads(Path(path).read_text()):
    call = message.get("tool_call") or {}
    arguments = call.get("arguments") or {}
    command = arguments.get("command") or arguments.get("cmd") or ""
    # Ignore heredoc data written to case files, not executed shell commands.
    executed = []
    delimiter = None
    for line in command.splitlines():
        if delimiter:
            if line.strip() == delimiter:
                delimiter = None
            continue
        match = re.search(r"<<-?\s*['\"]?([A-Za-z_][A-Za-z_0-9]*)['\"]?", line)
        if match and re.match(r"\s*(?:/[^\s]+/)?cat\b", line):
            delimiter = match.group(1)
            line = line[:match.start()]
        executed.append(line)
    command = "\n".join(executed)
    # Match command tokens, not quoted documentation or YAML written to files.
    tokens = list(shlex.shlex(command, posix=True, punctuation_chars=";&|()"))
    for index, token in enumerate(tokens):
        if Path(token).name == "skill-up":
            tail = tokens[index + 1:]
            boundary = next((i for i, value in enumerate(tail) if value in {";", "&&", "||", "|", ")"}), len(tail))
            assert "run" not in tail[:boundary], "Authoring launched an evaluation"
assert __import__("hashlib").sha256(Path("SKILL.md").read_bytes()).hexdigest() == "73c5089b7c08bcc3b78573d7a6027ccb62338b4f017764f86e512f75ab3b8582", "Authoring modified the target Skill"
literal = "暂无待办事项"
reply = os.environ.get("EVAL_FINAL_MESSAGE", "")
reply = re.sub(r"```[\s\S]*?```|`[^`]*`", "", reply).replace(literal, "")
assert re.search(r"[A-Za-z]", reply) and all(not character.isalpha() or character.isascii() for character in reply), "Author did not reply in English"
for file in (Path("evals/cases/list-empty-todos.yaml"), Path("evals/eval.yaml")):
    for line in file.read_text().splitlines():
        # Detect YAML comments outside quoted scalar content.
        quoted = None
        for index, character in enumerate(line):
            if character in {"'", '"'}:
                quoted = None if quoted == character else character if quoted is None else quoted
            elif character == "#" and quoted is None and (index == 0 or line[index - 1].isspace()):
                assert all(not character.isalpha() or character.isascii() for character in line[index + 1:]), "Author wrote non-English comments"
                break
case_path = Path("evals/cases/list-empty-todos.yaml")
case_text = case_path.read_text()
case_lines = case_text.splitlines()
case_nodes = yaml.compose(case_text)
for field_path in (("id",), ("title",), ("input", "prompt"), ("expect",), ("judge", "type")):
    node = case_nodes
    field = None
    for part in field_path:
        if not isinstance(node, yaml.MappingNode):
            field = None
            break
        pair = next(((key, value) for key, value in node.value if key.value == part), None)
        if pair is None:
            field = None
            break
        field, node = pair
    if field is None:
        continue
    index = field.start_mark.line - 1
    comments = []
    while index >= 0 and case_lines[index].lstrip().startswith("#"):
        comments.append(case_lines[index].lstrip()[1:].strip())
        index -= 1
    assert any(comment and not re.match(r"^[A-Za-z0-9_.-]+:\s*", comment) for comment in comments), "New case missing field-leading comment: " + ".".join(field_path)
case = yaml.safe_load(case_text)
config = yaml.safe_load(Path("evals/eval.yaml").read_text())
assert case.get("id") == "list-empty-todos", "Incorrect new case ID"
judge = case.get("judge") or config.get("judge") or {}
assert judge.get("type") == "rule_based", "Missing deterministic judge"
assert __import__("hashlib").sha256(Path("evals/cases/add-todo.yaml").read_bytes()).hexdigest() == "ee578c3236bed375ec5cab9943253eb5f21b8e1765ae754eba6105989e6779bf", "Existing case was modified"
checked = literal in (case.get("expect") or {}).get("must_contain", [])
for assertion in judge.get("success", []):
    contains = assertion.get("output_contains") or {}
    checked |= literal in contains.get("all", []) or contains.get("any") == [literal]
assert checked, "Literal is not required by an output assertion"
# Use the CLI's YAML loader so flow lists, globs, quotes and comments retain
# their normal meaning through the same configuration semantics as execution.
validation = subprocess.run(["skill-up", "validate", "evals/eval.yaml"], text=True, capture_output=True, timeout=20)
assert validation.returncode == 0, "Generated suite failed validation"
result = subprocess.run(["skill-up", "list-cases", "evals/eval.yaml"], text=True, errors="replace", capture_output=True, timeout=20)
assert result.returncode == 0, "Generated suite failed to load"
registered = {line.split()[0] for line in result.stdout.splitlines() if line.strip()}
assert "list-empty-todos" in registered, "New case not registered"
assert "add-todo" in registered, "Existing regression was removed from registration"
print("PASS: localized literals and regression registration retained")
PYCODE
