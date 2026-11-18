#!/usr/bin/env python3
"""Tests for summarize.py, run with: python3 -m unittest scripts/routing-canary/test_summarize.py"""

from __future__ import annotations

import contextlib
import importlib.util
import io
import json
import os
import subprocess
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

    def test_opencode_nested_tool_event_is_reported(self) -> None:
        """OpenCode keeps the tool name in `part.tool` and the arguments in `part.state.input`."""
        write_json(self.iteration / "result.json", {"case_results": [{"case_id": "competing", "status": "PASS"}]})
        write_transcript(
            self.iteration / "competing",
            [
                {
                    "type": "tool_use",
                    "part": {
                        "type": "tool",
                        "tool": "skill",
                        "state": {"status": "completed", "input": {"name": "literary-translation"}},
                    },
                },
                {
                    "type": "tool_use",
                    "part": {
                        "type": "tool",
                        "tool": "bash",
                        "state": {"status": "completed", "input": {"command": "printf 'translated' > .translation-done"}},
                    },
                },
            ],
        )
        write_json(self.iteration / "competing/with_skill/grading.json", {"summary": {"failed": 0}})

        text = render(self.root)
        competing = row(text, "competing")
        self.assertIn("| skill |", competing)
        self.assertIn("| .translation-done |", competing)

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


class EngineNameValidationTest(unittest.TestCase):
    """`run.sh` must reject a name by string equality, before touching any path.

    A name reaches `out/<engine>/` and the temporary workspace, so a pattern
    (`co*`) or a value with an embedded newline (`grep -x` reads it as two
    patterns) would otherwise be able to select a directory outside `out/`.
    """

    def run_script(
        self, *args: str, cwd: Path, env: dict[str, str] | None = None
    ) -> subprocess.CompletedProcess:
        return subprocess.run(
            ["bash", str(HERE / "run.sh"), *args],
            cwd=cwd,
            capture_output=True,
            text=True,
            timeout=120,
            env=env,
        )

    def test_rejects_unknown_pattern_and_multiline_names(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            victim = cwd / "victim"
            victim.mkdir()

            # Point mktemp at a directory of its own: a rejected name must not
            # leave a temporary workspace behind either.
            scratch = cwd / "tmp"
            scratch.mkdir()
            env = {**os.environ, "TMPDIR": str(scratch)}

            names = ["evil", "co*", "claude*", "../victim", "../victim\ncodex", ""]
            for name in names:
                with self.subTest(name=name):
                    result = self.run_script(name, cwd=cwd, env=env)
                    self.assertEqual(result.returncode, 1, result.stderr)
                    self.assertIn("unsupported engine", result.stderr)
                    self.assertIn("supported: claude_code codex opencode", result.stderr)
                    # Nothing ran: no build output, no artifacts, no deletion.
                    self.assertEqual(sorted(p.name for p in cwd.iterdir()), ["tmp", "victim"], result.stdout)
                    self.assertEqual(list(scratch.iterdir()), [], "a rejected name left a temp dir")

            self.assertTrue(victim.is_dir())

    def test_accepts_each_supported_name_syntax(self) -> None:
        # Reaching the build step proves the name passed validation. The build
        # itself is stubbed out so the test does not need Go, and a failing
        # stub stops the run before the fixture is copied.
        for name in ("claude_code", "codex", "opencode"):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as tmp:
                cwd = Path(tmp)
                fake_bin = cwd / "bin"
                fake_bin.mkdir()
                (fake_bin / "go").write_text("#!/bin/sh\nexit 1\n")
                (fake_bin / "go").chmod(0o755)

                result = self.run_script(
                    name,
                    cwd=cwd,
                    env={**os.environ, "PATH": f"{fake_bin}:{os.environ.get('PATH', '')}"},
                )
                self.assertNotIn("unsupported engine", result.stderr)
                self.assertIn("building skill-up", result.stdout)


if __name__ == "__main__":
    unittest.main()
