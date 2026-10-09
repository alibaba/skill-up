#!/usr/bin/env bash
set -euo pipefail
python3 - <<'PYCODE'
from pathlib import Path
import os
import json
import shlex
import re
path = os.environ.get("EVAL_TRANSCRIPT_PATH")
assert path, "Missing authoring transcript"
messages = json.loads(Path(path).read_text())
read_commands = {"pwd", "ls", "cat", "head", "tail", "rg", "grep", "wc", "stat", "file", "which", "realpath", "readlink", "uname", "find", "echo", "sed", "sort"}
for message in messages:
    call = message.get("tool_call") or {}
    name = call.get("name", "").lower()
    assert not any(word in name for word in ("write", "edit", "patch", "install", "delete", "remove", "move")), "Planning used a mutating tool"
    arguments = call.get("arguments") or {}
    command = arguments.get("command") or arguments.get("cmd")
    if not command:
        continue
    for _ in range(4):
        if not re.match(r"^\s*(?:[^\s]*/)?(?:sh|bash|zsh|dash)\s+-(?:c|lc)\s", command):
            break
        wrapper = shlex.split(command)
        if len(wrapper) == 3 and Path(wrapper[0]).name in {"sh", "bash", "zsh", "dash"} and wrapper[1] in {"-c", "-lc"}:
            command = wrapper[2]
        else:
            break
    # Preserve quoted/escaped punctuation as argument data through shlex.
    punctuation = {char: chr(0xE100 + index) for index, char in enumerate(";&|<>()")}
    protected = []
    quote = None
    escaped = False
    for index, character in enumerate(command):
        if escaped:
            protected.append(punctuation.get(character, character))
            escaped = False
            continue
        if character == "$" and command[index + 1:index + 2] == "(" and quote != "'":
            raise AssertionError("Planning used command substitution")
        protected.append(punctuation.get(character, character) if quote else character)
        if character == "\\" and quote != "'":
            escaped = True
        elif character == "'" and quote != '"':
            quote = None if quote == "'" else "'"
        elif character == '"' and quote != "'":
            quote = None if quote == '"' else '"'
        elif character == "`" and quote != "'":
            raise AssertionError("Planning used command substitution")
    command = "".join(protected)
    # Discarding stderr or merging it into stdout does not write fixture files.
    command = re.sub(r"(?<!\S)2\s*>\s*(?:/dev/null|&1)(?=\s|[;|&]|$)", "", command)
    lexer = shlex.shlex(command.replace("\n", ";"), posix=True, punctuation_chars=";&|<>()")
    tokens = list(lexer)
    assert not any(set(token) <= set(";&|<>()") and token not in {";", "&&", "||", "|", "&"} for token in tokens), "Planning used unsupported shell operators"
    assert not any(token in {">", ">>", "<", "<<", "(", ")"} for token in tokens), "Planning used redirection or a subshell"
    segments = [[]]
    for token in tokens:
        if token in {";", "&&", "||", "|", "&"}:
            segments.append([])
        else:
            for character, marker in punctuation.items():
                token = token.replace(marker, character)
            segments[-1].append(token)
    for segment in segments:
        if not segment:
            continue
        executable = Path(segment[0]).name
        if executable == "command":
            assert len(segment) >= 3 and segment[1] in {"-v", "-V"}, "Planning executed a command instead of querying its path"
        elif executable == "skill-up":
            assert len(segment) >= 2 and segment[1] in {"--help", "--version", "validate", "list-cases"}, "Planning executed a mutating CLI operation"
        else:
            assert executable in read_commands, "Planning executed a command outside the requested read-only profile"
        if executable == "sort":
            assert all(not arg.startswith("-") or arg == "--" or re.fullmatch(r"-[rnu]+", arg) for arg in segment[1:]), "Planning used sort outside the read-only argument profile"
        if executable == "sed":
            assert len(segment) >= 4 and segment[1] == "-n" and re.fullmatch(r"[0-9]+(?:,[0-9]+)?p", segment[2]), "Planning used sed outside line-range printing"
            assert all(not arg.startswith("-") for arg in segment[3:]), "Planning used unsupported sed options"
        if executable == "find":
            assert not any("$" in arg for arg in segment[1:]), "Planning used shell expansion in find arguments"
            forbidden = {"-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls"}
            assert not forbidden.intersection(segment[1:]), "Planning used a mutating find action"
        if executable == "rg":
            assert not any(arg in {"--pre", "--hostname-bin"} or arg.startswith(("--pre=", "--hostname-bin=")) for arg in segment[1:]), "Planning used a search option that executes a subprocess"
        if executable == "file":
            assert not any("$" in arg for arg in segment[1:]), "Planning used shell expansion in file arguments"
            # Full safe option spellings only: GNU getopt_long also accepts
            # abbreviations such as --comp for the writing --compile mode.
            safe_options = {"--brief", "--mime", "--mime-type", "--mime-encoding", "--dereference", "--no-dereference", "--help", "--version"}
            for arg in segment[1:]:
                if arg == "--":
                    break  # Remaining arguments are filenames.
                assert not arg.startswith("-") or arg in safe_options or re.fullmatch(r"-[biLhv]+", arg), "Planning used file outside the read-only argument profile"
# Host/runtime metadata is installed before the agent runs, outside the fixture.
managed = {".git", ".qoder", ".codex", ".claude", ".agents", ".opencode", ".qwen"}
entries = {str(p) for p in Path(".").rglob("*") if p.parts[0] not in managed}
assert entries == {"SKILL.md"}, f"Planning changed fixture contents: {sorted(entries - {'SKILL.md'})}"
assert __import__("hashlib").sha256(Path("SKILL.md").read_bytes()).hexdigest() == "d33f46a33b55ee9d4074c6b19dc993118a0d1318e3ce77eb78ca84dfcd798a79", "Planning modified the target Skill"
text = os.environ.get("EVAL_FINAL_MESSAGE", "").lower()
assert "case" in text and ("plan" in text or "evaluat" in text), "Missing evaluation plan"
print("PASS: plan supplied without creating evals")
PYCODE
