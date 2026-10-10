#!/usr/bin/env bash
set -euo pipefail
python3 - <<'PYCODE'
from pathlib import Path
import re
import subprocess
import os
import json
import shlex
def shell_expansion_positions(command, include_globs=False):
    """Inspect shell quoting before shlex discards literal/escaped markers."""
    quote = None
    escaped = False
    comment = False
    word_start = True
    substitutions = []
    skip_parenthesis = False
    for index, character in enumerate(command):
        if skip_parenthesis:
            skip_parenthesis = False
            continue
        if comment:
            if character == "\n":
                comment = False
                word_start = True
            continue
        if escaped:
            escaped = False
            if character != "\n":
                word_start = False
            continue
        if character == "#" and quote is None and word_start:
            comment = True
            continue
        if quote != "'" and character == "$":
            yield index
            if command[index + 1:index + 2] == "(":
                substitutions.append([quote, 1, ")"])
                quote = None
                word_start = True
                skip_parenthesis = True
                continue
        elif quote != "'" and character == "`":
            yield index
            if substitutions and substitutions[-1][2] == "`":
                quote = substitutions.pop()[0]
                word_start = False
            else:
                substitutions.append([quote, 0, "`"])
                quote = None
                word_start = True
            continue
        elif include_globs and quote is None and character in "*?[":
            yield index
        if quote is None and substitutions and substitutions[-1][2] == ")":
            if character == "(":
                substitutions[-1][1] += 1
            elif character == ")":
                substitutions[-1][1] -= 1
                if substitutions[-1][1] == 0:
                    quote = substitutions.pop()[0]
                    word_start = False
                    continue
        if character == "\\" and quote != "'":
            escaped = True
        elif character == "'" and quote != '"':
            quote = None if quote == "'" else "'"
        elif character == '"' and quote != "'":
            quote = None if quote == '"' else '"'
        word_start = quote is None and (character.isspace() or character in ";&|<>()")

def active_shell_expansion(command, include_globs=False):
    return next(shell_expansion_positions(command, include_globs), None) is not None

def standalone_shell_wrapper(command):
    """Keep compound commands from becoming discarded wrapper operands."""
    quote = None
    escaped = False
    for character in command:
        if escaped:
            escaped = False
            continue
        if character == "\\" and quote != "'":
            escaped = True
        elif character == "'" and quote != '"':
            quote = None if quote == "'" else "'"
        elif character == '"' and quote != "'":
            quote = None if quote == '"' else '"'
        elif character in "$`" and quote != "'":
            return False
        elif quote is None and character in ";&|()<>\n":
            return False
    return True

def shell_line_continues(line, quote=None):
    escaped = False
    for index, character in enumerate(line):
        if escaped:
            escaped = False
        elif character == "\\" and quote != "'":
            escaped = True
        elif character in "'\"":
            if quote is None:
                quote = character
            elif quote == character:
                quote = None
        elif quote is None and character == "#" and (index == 0 or line[index - 1].isspace() or line[index - 1] in ";&|()<>"):
            return False
    return escaped

def heredoc_marker(line, quote=None):
    escaped = False
    index = 0
    while index < len(line):
        character = line[index]
        if escaped:
            escaped = False
        elif character == "\\" and quote != "'":
            escaped = True
        elif character in "'\"":
            if quote is None:
                quote = character
            elif quote == character:
                quote = None
        elif quote is None:
            if character == "#" and (index == 0 or line[index - 1].isspace() or line[index - 1] in ";&|()<>"):
                break
            if line.startswith("<<<", index):
                index += 3
                continue
            if line.startswith("<<", index):
                cursor = index + (3 if line.startswith("<<-", index) else 2)
                while cursor < len(line) and line[cursor].isspace():
                    cursor += 1
                start = cursor
                marker_quote = None
                quoted = False
                decoded = []
                while cursor < len(line):
                    current = line[cursor]
                    assert not (marker_quote is None and current == "$" and line[cursor + 1:cursor + 2] in {"\"", "'"}), "Unsupported shell quoting in heredoc delimiter"
                    if current == "\\" and marker_quote != "'":
                        quoted = True
                        assert cursor + 1 < len(line), "Incomplete heredoc delimiter escape"
                        following = line[cursor + 1]
                        if marker_quote is None or following in '$`"\\':
                            decoded.append(following)
                        else:
                            decoded.extend((current, following))
                        cursor += 2
                        continue
                    if current in "'\"":
                        if marker_quote is None:
                            marker_quote = current
                            quoted = True
                        elif marker_quote == current:
                            marker_quote = None
                        else:
                            decoded.append(current)
                    elif marker_quote is None and (current.isspace() or current in ";&|()<>"):
                        break
                    else:
                        decoded.append(current)
                    cursor += 1
                assert cursor > start and marker_quote is None and cursor <= len(line), "Unsupported heredoc delimiter outside the literal authoring profile"
                marker = "".join(decoded)
                return (index, cursor, marker, quoted, line.startswith("<<-", index)), quote
        index += 1
    return None, quote

path = os.environ.get("EVAL_TRANSCRIPT_PATH")
assert path, "Missing authoring transcript"
for message in json.loads(Path(path).read_text()):
    call = message.get("tool_call") or {}
    arguments = call.get("arguments") or {}
    command = arguments.get("command") or arguments.get("cmd") or ""
    for _ in range(4):
        if not re.match(r"^\s*(?:[^\s]*/)?(?:sh|bash|zsh|dash)\s+-(?:c|lc)\s", command):
            break
        assert standalone_shell_wrapper(command), "Unsupported compound shell wrapper"
        wrapper = shlex.split(command)
        if len(wrapper) >= 3 and Path(wrapper[0]).name in {"sh", "bash", "zsh", "dash"} and wrapper[1] in {"-c", "-lc"}:
            command = wrapper[2]
        else:
            break
    # Only quoted heredocs are inert data; require quoting before skipping bodies.
    executed = []
    delimiter = None
    quote_state = None
    lines = iter(command.splitlines())
    for line in lines:
        if delimiter is not None:
            if (line.lstrip("\t") if strip_tabs else line) == delimiter:
                delimiter = None
            continue
        while shell_line_continues(line, quote_state):
            continuation = next(lines, None)
            assert continuation is not None, "Incomplete shell line continuation"
            line = line[:-1] + continuation
        match, next_quote_state = heredoc_marker(line, quote_state)
        if match and re.match(r"\s*(?:/[^\s]+/)?(?:cat|tee|printf)\b", line):
            start, end, delimiter, quoted, strip_tabs = match
            assert quoted, "Author used an unquoted heredoc outside the literal authoring profile"
            line = line[:start] + line[end:]
            other, next_quote_state = heredoc_marker(line, quote_state)
            assert other is None, "Multiple heredocs outside the literal authoring profile"
        quote_state = next_quote_state
        executed.append(line)
    command = "\n".join(executed)
    assert not active_shell_expansion(command), "Authoring used expanded skill-up arguments or dynamic shell dispatch"
    lexer = shlex.shlex(command.replace("\n", "\n;"), posix=True, punctuation_chars=";&|()<>")
    lexer.whitespace_split = True
    segments = [[]]
    for token in lexer:
        if token in {";", "&&", "||", "|", "&", "(", ")"}:
            segments.append([])
        else:
            segments[-1].append(token)
    for segment in segments:
        invocation = []
        index = 0
        while index < len(segment):
            if segment[index] in {">", ">>", "<", "<<", "<<-", ">&", "<&"}:
                if invocation and invocation[-1].isdigit():
                    invocation.pop()
                index += 2
            else:
                invocation.append(segment[index])
                index += 1
        while invocation and (invocation[0] in {"if", "then", "elif", "else", "while", "until", "do", "!", "{"} or re.match(r"^[A-Za-z_][A-Za-z_0-9]*=", invocation[0])):
            invocation = invocation[1:]
        if not invocation:
            continue
        executable = Path(invocation[0]).name
        if executable == "command" and len(invocation) >= 3 and invocation[1] in {"-v", "-V"}:
            continue
        assert not re.fullmatch(r"python[0-9.]*|node|ruby|perl|awk|gawk|mawk|nawk", executable), "Authoring used unsupported interpreter dispatch"
        allowed = {"cat", "tee", "printf", "echo", "mkdir", "touch", "cp", "mv", "rm", "chmod", "cd", "pwd", "ls", "head", "tail", "rg", "grep", "wc", "stat", "find", "sed", "sort", "skill-up", "true", "false", ":", "test", "[", "fi", "done", "}"}
        assert executable in allowed, "Authoring used unsupported command dispatch"
        if executable == "find":
            assert not {"-exec", "-execdir", "-ok", "-okdir"}.intersection(invocation[1:]), "Authoring used unsupported find dispatch"
        if executable == "rg":
            assert not any(arg in {"--pre", "--hostname-bin"} or arg.startswith(("--pre=", "--hostname-bin=")) for arg in invocation[1:]), "Authoring used unsupported search dispatch"
        if executable == "sed":
            assert len(invocation) >= 4 and invocation[1] == "-n" and re.fullmatch(r"[0-9]+(?:,[0-9]+)?p", invocation[2]) and all(not arg.startswith("-") for arg in invocation[3:]), "Authoring used unsupported sed dispatch"
        if executable == "sort":
            assert all(not arg.startswith("-") or arg == "--" or re.fullmatch(r"-[rnu]+", arg) for arg in invocation[1:]), "Authoring used unsupported sort dispatch"
        if executable == "skill-up":
            args = invocation[1:]
            while args and (args[0] == "--config" or args[0].startswith("--config=")):
                args = args[2:] if args[0] == "--config" else args[1:]
            assert not args or args[0] != "run", "Authoring launched an evaluation"
            assert args and args[0] in {"validate", "list-cases", "help", "--help", "-h", "--version"}, "Authoring used unsupported CLI dispatch"
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
import yaml
case_nodes = yaml.compose(case_text)
def is_explanatory_comment(comment):
    return bool(re.search(r"[A-Za-z]{3,}", comment)) and not comment.startswith("-") and not re.match(r"^[A-Za-z0-9_.-]+:\s*", comment)

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
    assert any(is_explanatory_comment(comment) for comment in comments), "New case missing field-leading comment: " + ".".join(field_path)
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
