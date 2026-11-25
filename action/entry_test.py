#!/usr/bin/env python3
"""Regression tests for Docker Action exit cleanup."""

import os
import pathlib
import shutil
import stat
import subprocess
import tempfile
import unittest


class EntrypointCleanupTest(unittest.TestCase):
    def run_entry(self, exit_code, fail_chown=""):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = pathlib.Path(temporary.name)
        home = root / "home with spaces"
        workspace = root / "workspace"
        commands = root / "commands"
        for directory in (home, workspace, commands):
            directory.mkdir()
        shutil.copyfile(pathlib.Path(__file__).with_name("entry.sh"), root / "entry.sh")
        (root / "main.py").write_text(
            "import os, pathlib, sys\n"
            "state = pathlib.Path(os.environ['HOME']) / '.qodersec' / 'state'\n"
            "state.mkdir(parents=True)\n"
            "token = state / 'token'\n"
            "token.write_text('fixture')\n"
            "token.chmod(0o600)\n"
            "state.chmod(0o000)\n"
            "report = pathlib.Path(os.environ['GITHUB_WORKSPACE']) / 'skill-up-workspace'\n"
            "report.mkdir()\n"
            "sys.exit(int(os.environ['TEST_EXIT_CODE']))\n"
        )
        # GNU stat is unavailable on macOS. Stub only ownership commands;
        # exercise the actual shell trap, engine exit, and recursive chmod.
        (commands / "stat").write_text("#!/bin/sh\necho 1234:5678\n")
        (commands / "chown").write_text(
            '#!/bin/sh\nprintf "%s\\n" "$*" >> "$TEST_CHOWN_LOG"\n'
            'for argument do last="$argument"; done\n'
            '[ "$last" != "$TEST_FAIL_CHOWN" ]\n'
        )
        for command in commands.iterdir():
            command.chmod(0o755)
        env = {
            **os.environ,
            "HOME": str(home),
            "GITHUB_WORKSPACE": str(workspace),
            "PATH": f"{commands}:/usr/local/bin:/usr/bin:{os.environ['PATH']}",
            "TEST_EXIT_CODE": str(exit_code),
            "TEST_CHOWN_LOG": str(root / "chown.log"),
            "TEST_FAIL_CHOWN": (
                str(home) if fail_chown == "home" else
                str(workspace / "skill-up-workspace") if fail_chown == "report" else ""
            ),
        }
        result = subprocess.run(
            ["bash", str(root / "entry.sh")], env=env,
            capture_output=True, text=True, check=False,
        )
        return result, home, root / "chown.log"

    def test_repairs_private_home_on_success_and_engine_failure(self):
        for exit_code in (0, 7):
            with self.subTest(exit_code=exit_code):
                result, home, log = self.run_entry(exit_code)
                self.assertEqual(result.returncode, exit_code, result.stderr)
                state = home / ".qodersec" / "state"
                self.assertEqual(stat.S_IMODE(state.stat().st_mode), 0o700)
                self.assertEqual(stat.S_IMODE((state / "token").stat().st_mode), 0o600)
                self.assertEqual((state / "token").read_text(), "fixture")
                self.assertIn(f"-R 1234:5678 {home}", log.read_text().splitlines())

    def test_home_cleanup_failure_is_reported_without_masking_engine_failure(self):
        for exit_code in (0, 7):
            with self.subTest(exit_code=exit_code):
                result, home, _ = self.run_entry(exit_code, fail_chown="home")
                # Restore the fixture so TemporaryDirectory can remove it.
                (home / ".qodersec" / "state").chmod(0o700)
                self.assertEqual(result.returncode, exit_code or 1)
                self.assertIn("failed to restore home ownership", result.stderr)

    def test_report_cleanup_failure_does_not_skip_home_cleanup(self):
        result, home, _ = self.run_entry(0, fail_chown="report")
        self.assertEqual(result.returncode, 1)
        self.assertIn("failed to restore report ownership", result.stderr)
        self.assertEqual(stat.S_IMODE((home / ".qodersec" / "state").stat().st_mode), 0o700)


if __name__ == "__main__":
    unittest.main()
