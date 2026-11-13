#!/usr/bin/env python3
"""Tests for summarize.py, run with: python3 -m unittest scripts/routing-canary/test_summarize.py"""

from __future__ import annotations

import contextlib
import importlib.util
import io
import json
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("routing_summarize", HERE / "summarize.py")
summarize = importlib.util.module_from_spec(spec)
assert spec.loader is not None
spec.loader.exec_module(summarize)


def write_json(path: Path, payload) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload))


def write_transcript(case_dir: Path, events) -> None:
    run_dir = case_dir / "with_skill/outputs/agent/run"
    run_dir.mkdir(parents=True, exist_ok=True)
    (run_dir / "run.jsonl").write_text("\n".join(json.dumps(event) for event in events) + "\n")


def render(root: Path) -> str:
    out = io.StringIO()
    with contextlib.redirect_stdout(out):
        summarize.main(["summarize.py", str(root)])
    return out.getvalue()


def row(text: str, case: str) -> str:
    for line in text.splitlines():
        if line.startswith("|") and f"| {case} |" in line:
            return line
    raise AssertionError(f"no row for {case} in:\n{text}")


class SummarizeFixtureTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.iteration = self.root / "iteration-1"

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def test_case_without_transcript_is_kept_with_execution_status(self) -> None:
        write_json(
            self.iteration / "result.json",
            {
                "case_results": [
                    {"case_id": "relevant", "status": "PASS"},
                    {"case_id": "timed_out", "status": "ERROR"},
                ]
            },
        )
        write_json(self.iteration / "relevant/with_skill/grading.json", {"summary": {"failed": 0}})
        write_transcript(
            self.iteration / "relevant",
            [{"type": "tool_use", "name": "exec_command", "input": {"cmd": "cat .codex/skills/skill/SKILL.md"}}],
        )

        text = render(self.root)
        missing = row(text, "timed_out")
        self.assertIn("| ERROR |", missing)
        self.assertIn("| missing |", missing)
        self.assertIn("| PASS |", row(text, "relevant"))

    def test_reference_without_a_read_is_not_reported_as_a_skill_call(self) -> None:
        write_json(self.iteration / "result.json", {"case_results": [{"case_id": "listing", "status": "PASS"}]})
        write_transcript(
            self.iteration / "listing",
            [{"type": "tool_use", "name": "exec_command", "input": {"cmd": "ls .codex/skills/skill/SKILL.md"}}],
        )

        text = render(self.root)
        listing = row(text, "listing")
        self.assertEqual(
            "| iteration-1 | listing | PASS | - | present | - | yes | - | no |",
            listing,
        )

    def test_transcript_without_result_json_still_renders(self) -> None:
        write_transcript(
            self.iteration / "unrelated",
            [
                {"type": "tool_use", "name": "Bash", "input": {"command": "true"}},
                {"type": "tool_result", "content": "CANARY-ROUTING-OK"},
            ],
        )
        write_json(self.iteration / "unrelated/with_skill/grading.json", {"summary": {"failed": 1}})

        text = render(self.root)
        unrelated = row(text, "unrelated")
        self.assertIn("| FAIL |", unrelated)
        self.assertIn("| present |", unrelated)
        self.assertIn("| - |", unrelated)


if __name__ == "__main__":
    unittest.main()
