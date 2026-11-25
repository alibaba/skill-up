#!/usr/bin/env python3
"""Summarize the native Skill evidence in routing-canary transcript artifacts.

Usage: summarize.py out/<engine> [out/<engine> ...]

The script does not depend on a single engine transcript schema. It walks each
JSON line and records tool/function calls by name, references to a SKILL.md path
in a tool-call payload, and the presence of an injected Skill catalog, so the
same summary works for claude_code, codex and opencode artifacts.

A reference is not a confirmed read: a directory listing, or a `cat` that failed,
also puts "SKILL.md" in the payload. Confirming a read would require correlating
the call with its result, which is engine specific; the column is labelled
accordingly instead of claiming more than the transcript shows.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

MARKERS = (".canary-relevant", ".translation-done")


def tool_calls(obj, out):
    """Collect (name, payload) pairs from any transcript shape.

    Engines do not agree on where a call keeps its name and its arguments.
    OpenCode nests them in `part.tool` and `part.state.input`, while the
    Claude Code and Codex transcripts put `name`/`input` on one object, so
    read the name and the payload from both layouts.
    """
    if isinstance(obj, dict):
        name = obj.get("name") or obj.get("tool")
        payload = obj.get("input") or obj.get("arguments")
        part = obj.get("part")
        if isinstance(part, dict):
            name = name or part.get("tool") or part.get("name")
            state = part.get("state")
            if isinstance(state, dict):
                payload = payload or state.get("input") or state.get("arguments")
        elif isinstance(obj.get("state"), dict):
            # A bare `part`-like object passed in from a caller.
            state = obj["state"]
            payload = payload or state.get("input") or state.get("arguments")
        if isinstance(name, str) and payload is not None:
            out.append((name, payload if isinstance(payload, str) else json.dumps(payload)))
        for value in obj.values():
            tool_calls(value, out)
    elif isinstance(obj, list):
        for value in obj:
            tool_calls(value, out)


def message_texts(obj, out, role=None):
    """Collect (role, text) pairs so catalog detection can ignore assistant text.

    The injected catalog rides in a `system` or `developer` message (Codex sends
    it as a developer message); an assistant sentence like "There are no
    available skills." must not count as a catalog.
    """
    if isinstance(obj, dict):
        next_role = role
        role_value = obj.get("role")
        if isinstance(role_value, str):
            next_role = role_value.lower()
        for key, value in obj.items():
            if key in ("text", "content", "output_text", "input_text") and isinstance(value, str):
                out.append((next_role, value))
            else:
                message_texts(value, out, next_role)
    elif isinstance(obj, list):
        for value in obj:
            message_texts(value, out, role)


def is_error_event(obj) -> bool:
    """Whether a transcript record reports that the run itself failed.

    The engines report that in their own shape, and the adapters already read
    these two: OpenCode writes a `type: "error"` event, and the Claude Code
    stream ends with a `result` event flagged `is_error`. A failed run never
    reached the Skill evidence it did not show, so a signal it did not produce
    is unknown rather than absent.
    """
    if not isinstance(obj, dict):
        return False
    if obj.get("type") == "error":
        return True
    return obj.get("type") == "result" and obj.get("is_error") is True


def summarize_transcript(path: Path) -> dict:
    calls: list[tuple[str, str]] = []
    texts: list[tuple[str | None, str]] = []
    records = 0
    unparseable = 0
    failed = False
    for line in path.read_text(errors="replace").splitlines():
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            unparseable += 1
            continue
        # Only a JSON object or an array is a record. A line that decodes to
        # `null` or to a scalar carries nothing about the run, so it must not
        # make the transcript look complete.
        if isinstance(event, (dict, list)):
            records += 1
        if is_error_event(event):
            failed = True
        tool_calls(event, calls)
        message_texts(event, texts)

    skill_calls = sorted({name for name, _ in calls if "skill" in name.lower()})
    skill_file_referenced = any("SKILL.md" in payload for _, payload in calls)
    markers = sorted({marker for marker in MARKERS if any(marker in payload for _, payload in calls)})
    catalog = any(
        role in ("system", "developer")
        and ("<skills_instructions>" in text or "available skills" in text.lower())
        for role, text in texts
    )
    # An artifact with no usable record says nothing about the Skill: the
    # OpenCode adapter can leave an empty stdout file even when the run failed.
    # Report the evidence as unknown rather than as an observed absence.
    if records == 0:
        return {
            "events": 0,
            "state": "empty" if unparseable == 0 else "unparseable",
            "skill_tool_calls": [],
            "skill_file_referenced": None,
            "markers": [],
            "catalog_injected": None,
        }
    # A line that did not decode can hold the evidence that was in it, and a
    # run that failed never reached the evidence it did not show: in both
    # cases an unobserved signal is unknown, not absent.
    unknown = None if (unparseable or failed) else False
    if failed:
        state = "failed"
    elif unparseable:
        state = "incomplete"
    else:
        state = "present"
    return {
        "events": len(texts),
        "state": state,
        "skill_tool_calls": skill_calls,
        "skill_file_referenced": skill_file_referenced if skill_file_referenced else unknown,
        "markers": markers,
        "catalog_injected": catalog if catalog else unknown,
    }


def graded_result(case_dir: Path) -> str:
    grading = case_dir / "with_skill" / "grading.json"
    if not grading.exists():
        return "-"
    try:
        data = json.loads(grading.read_text())
    except json.JSONDecodeError:
        return "-"
    for key in ("passed", "pass", "success"):
        if key in data:
            return "PASS" if data[key] else "FAIL"
    summary = data.get("summary")
    if isinstance(summary, dict) and "failed" in summary:
        return "PASS" if summary["failed"] == 0 else "FAIL"
    return "-"


def execution_status(iteration: Path) -> dict[str, str]:
    """Read case execution statuses from the iteration result.json."""
    path = iteration / "result.json"
    if not path.exists():
        return {}
    try:
        data = json.loads(path.read_text())
    except json.JSONDecodeError:
        return {}
    statuses = {}
    for case in data.get("case_results") or []:
        if not isinstance(case, dict):
            continue
        case_id = case.get("case_id") or case.get("caseId") or case.get("id")
        status = case.get("status") or case.get("result")
        if isinstance(case_id, str) and isinstance(status, str):
            statuses[case_id] = status.upper()
    return statuses


def summarize_case(case_dir: Path, status: str) -> dict:
    transcripts = sorted(case_dir.glob("with_skill/outputs/agent/run/*.jsonl"))
    if not transcripts:
        return {
            "execution": status,
            "graded": graded_result(case_dir),
            "transcript": "missing",
            "skill_tool_calls": [],
            "skill_file_referenced": None,
            "markers": [],
            "catalog_injected": None,
        }
    info = summarize_transcript(transcripts[0])
    # A run can fail without leaving a structured error event: an agent that
    # times out after writing a valid text event is the common shape. The
    # transcript then looks complete, but the run never got far enough to show
    # what it did not show — and `result.json` already knows it failed. Use the
    # execution status as the fallback signal, so an unobserved signal reads as
    # unknown instead of absent. Positive observations stay: the run did reach
    # those before it failed.
    if status.upper() == "ERROR":
        if not info["skill_file_referenced"]:
            info["skill_file_referenced"] = None
        if not info["catalog_injected"]:
            info["catalog_injected"] = None
    info["execution"] = status
    info["graded"] = graded_result(case_dir)
    info["transcript"] = info.pop("state")
    return info


def cell(values, empty="-") -> str:
    if values is None:
        return "-"
    if isinstance(values, list):
        return ", ".join(values) or empty
    return "yes" if values else "no"


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        print(__doc__, file=sys.stderr)
        return 2
    for root in map(Path, argv[1:]):
        print(f"\n## {root}")
        print(
            "| iteration | case | execution | graded | transcript | skill tool call | "
            "skill file reference | markers | catalog |"
        )
        print("| --- | --- | --- | --- | --- | --- | --- | --- | --- |")
        for iteration in sorted(root.glob("iteration-*")):
            statuses = execution_status(iteration)
            case_dirs = {p.name: p for p in iteration.iterdir() if p.is_dir()}
            for name in sorted(set(case_dirs) | set(statuses)):
                case_dir = case_dirs.get(name)
                if case_dir is None:
                    info = {
                        "execution": statuses[name],
                        "graded": "-",
                        "transcript": "missing",
                        "skill_tool_calls": [],
                        "skill_file_referenced": None,
                        "markers": [],
                        "catalog_injected": None,
                    }
                else:
                    info = summarize_case(case_dir, statuses.get(name, "-"))
                print(
                    f"| {iteration.name} | {name} | {info['execution']} | {info['graded']} | "
                    f"{info['transcript']} | {cell(info['skill_tool_calls'])} | "
                    f"{cell(info['skill_file_referenced'])} | {cell(info['markers'])} | "
                    f"{cell(info['catalog_injected'])} |"
                )
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
