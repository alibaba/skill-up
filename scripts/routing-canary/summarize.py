#!/usr/bin/env python3
"""Summarize the native Skill evidence in routing-canary transcript artifacts.

Usage: summarize.py out/<engine> [out/<engine> ...]

The script does not depend on a single engine transcript schema. It walks each
JSON line and records tool/function calls by name, reads of a SKILL.md file, and
the presence of an injected Skill catalog, so the same summary works for
claude_code, codex and opencode artifacts.
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
    skill_reads = sorted(
        {
            "SKILL.md"
            for name, payload in calls
            if "SKILL.md" in payload and name.lower() not in ("skill",)
        }
    )
    reads_bodies = any("SKILL.md" in payload for _, payload in calls)
    markers = sorted({marker for marker in MARKERS if any(marker in payload for _, payload in calls)})
    catalog = any("<skills_instructions>" in text or "available skills" in text.lower() for text in texts)
    return {
        "events": len(texts),
        "skill_tool_calls": skill_calls,
        "reads_skill_md": reads_bodies,
        "markers": markers,
        "catalog_injected": catalog,
    }


def result_of(case_dir: Path) -> str:
    grading = case_dir / "with_skill" / "grading.json"
    if not grading.exists():
        return "?"
    try:
        data = json.loads(grading.read_text())
    except json.JSONDecodeError:
        return "?"
    for key in ("passed", "pass", "success"):
        if key in data:
            return "PASS" if data[key] else "FAIL"
    summary = data.get("summary")
    if isinstance(summary, dict) and "failed" in summary:
        return "PASS" if summary["failed"] == 0 else "FAIL"
    return "?"


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        print(__doc__, file=sys.stderr)
        return 2
    for root in map(Path, argv[1:]):
        print(f"\n## {root}")
        print("| iteration | case | result | skill tool call | reads SKILL.md | markers | catalog |")
        print("| --- | --- | --- | --- | --- | --- | --- |")
        for iteration in sorted(root.glob("iteration-*")):
            for case_dir in sorted(p for p in iteration.iterdir() if p.is_dir()):
                transcripts = sorted(case_dir.glob("with_skill/outputs/agent/run/*.jsonl"))
                if not transcripts:
                    continue
                info = summarize_transcript(transcripts[0])
                print(
                    f"| {iteration.name} | {case_dir.name} | {result_of(case_dir)} | "
                    f"{', '.join(info['skill_tool_calls']) or '-'} | "
                    f"{'yes' if info['reads_skill_md'] else 'no'} | "
                    f"{', '.join(info['markers']) or '-'} | "
                    f"{'yes' if info['catalog_injected'] else 'no'} |"
                )
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
