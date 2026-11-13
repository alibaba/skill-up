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

MARKERS = (".canary-relevant", ".canary-decoy")


def tool_calls(obj, out):
    """Collect (name, payload) pairs from any transcript shape."""
    if isinstance(obj, dict):
        name = obj.get("name") or obj.get("tool")
        payload = obj.get("input") or obj.get("arguments")
        if isinstance(name, str) and payload is not None:
            out.append((name, payload if isinstance(payload, str) else json.dumps(payload)))
        for value in obj.values():
            tool_calls(value, out)
    elif isinstance(obj, list):
        for value in obj:
            tool_calls(value, out)


def text_blobs(obj, out):
    if isinstance(obj, dict):
        for key, value in obj.items():
            if key in ("text", "content", "output_text", "input_text") and isinstance(value, str):
                out.append(value)
            else:
                text_blobs(value, out)
    elif isinstance(obj, list):
        for value in obj:
            text_blobs(value, out)


def summarize_transcript(path: Path) -> dict:
    calls: list[tuple[str, str]] = []
    texts: list[str] = []
    for line in path.read_text(errors="replace").splitlines():
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        tool_calls(event, calls)
        text_blobs(event, texts)

    skill_calls = sorted({name for name, _ in calls if "skill" in name.lower()})
    skill_file_referenced = any("SKILL.md" in payload for _, payload in calls)
    markers = sorted({marker for marker in MARKERS if any(marker in payload for _, payload in calls)})
    catalog = any("<skills_instructions>" in text or "available skills" in text.lower() for text in texts)
    return {
        "events": len(texts),
        "skill_tool_calls": skill_calls,
        "skill_file_referenced": skill_file_referenced,
        "markers": markers,
        "catalog_injected": catalog,
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
    info["execution"] = status
    info["graded"] = graded_result(case_dir)
    info["transcript"] = "present"
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
