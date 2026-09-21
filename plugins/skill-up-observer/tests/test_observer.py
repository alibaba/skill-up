from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path
import tempfile
import threading
import unittest
from unittest import mock


SCRIPT = Path(__file__).parents[1] / "scripts" / "observer.py"
SPEC = importlib.util.spec_from_file_location("skill_up_observer", SCRIPT)
observer = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(observer)


class ObserverTest(unittest.TestCase):
    def test_cross_host_observation_fixtures_share_the_contract(self) -> None:
        fixtures = SCRIPT.parents[1] / "tests" / "fixtures"
        for name in ("codex-observation.json", "dsh-observation.json"):
            observation = json.loads((fixtures / name).read_text(encoding="utf-8"))
            observer.validate_observation(observation)

    def test_plugin_exposes_only_skill_upper(self) -> None:
        manifest = json.loads((SCRIPT.parents[1] / ".codex-plugin" / "plugin.json").read_text())
        self.assertEqual(manifest["skills"], "./skills/")
        skill_files = list((SCRIPT.parents[1] / "skills").rglob("SKILL.md"))
        self.assertEqual([path.parent.name for path in skill_files], ["skill-upper"])

    def test_bundled_skill_upper_matches_canonical_skill(self) -> None:
        plugin_skill = SCRIPT.parents[1] / "skills" / "skill-upper"
        canonical_skill = SCRIPT.parents[3] / "skills" / "skill-upper"
        bundled_files = {
            path.relative_to(plugin_skill)
            for path in plugin_skill.rglob("*")
            if path.is_file()
        }
        canonical_files = {
            path.relative_to(canonical_skill)
            for path in canonical_skill.rglob("*")
            if path.is_file() and "evals" not in path.relative_to(canonical_skill).parts
        }
        self.assertEqual(bundled_files, canonical_files)
        for relative_path in bundled_files:
            self.assertEqual(
                (plugin_skill / relative_path).read_bytes(),
                (canonical_skill / relative_path).read_bytes(),
                relative_path,
            )

    def test_codex_default_hook_manifest_exists(self) -> None:
        manifest = json.loads((SCRIPT.parents[1] / "hooks" / "hooks.json").read_text())
        self.assertIn("UserPromptSubmit", manifest["hooks"])
        self.assertIn("Stop", manifest["hooks"])

    def test_codex_mcp_uses_relative_plugin_path(self) -> None:
        manifest = json.loads((SCRIPT.parents[1] / ".mcp.json").read_text())
        args = manifest["mcpServers"]["skill_up_observer"]["args"]
        self.assertEqual(args[0], "scripts/observer.py")

    def test_development_data_override_takes_precedence(self) -> None:
        with mock.patch.dict(
            os.environ,
            {"SKILL_UP_OBSERVER_DATA": "/tmp/observer-override", "PLUGIN_DATA": "/tmp/plugin-data"},
        ):
            self.assertEqual(observer.data_dir(), Path("/tmp/observer-override").resolve())

    def test_default_data_dir_is_shared_between_hooks_and_mcp(self) -> None:
        with mock.patch.dict(
            os.environ,
            {"CODEX_HOME": "/tmp/codex-home", "PLUGIN_DATA": "/tmp/hook-only-plugin-data"},
            clear=True,
        ):
            self.assertEqual(
                observer.data_dir(),
                Path("/tmp/codex-home").resolve() / "plugin-data" / observer.OBSERVER_SKILL_NAME,
            )

    def test_redacts_prefixed_environment_credentials(self) -> None:
        source = "OPENAI_API_KEY=plain-secret GITHUB_TOKEN:another-secret MY_PASSWORD=hunter2"
        redacted, categories = observer.redact(source)
        self.assertNotIn("plain-secret", redacted)
        self.assertNotIn("another-secret", redacted)
        self.assertNotIn("hunter2", redacted)
        self.assertIn("secret_assignment", categories)

    def test_redacts_complete_quoted_secret_assignments(self) -> None:
        source = 'PASSWORD="correct horse battery staple" TOKEN=\'alpha beta gamma\''
        redacted, categories = observer.redact(source)
        self.assertEqual(redacted, "[REDACTED] [REDACTED]")
        self.assertIn("secret_assignment", categories)

    def test_explicit_hook_lifecycle_is_redacted_and_deduplicated(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            prompt = {
                "session_id": "session-1",
                "turn_id": "turn-1",
                "hook_event_name": "UserPromptSubmit",
                "model": "gpt-test",
                "prompt": "Use $demo-skill with OPENAI_API_KEY=plain-secret",
            }
            observer.handle_hook(prompt, root)
            stop = {
                "session_id": "session-1",
                "turn_id": "turn-1",
                "hook_event_name": "Stop",
                "last_assistant_message": "done with sk-abcdefghijklmnopqrstuvwxyz",
            }
            result = observer.handle_hook(stop, root)
            self.assertIsNotNone(result)
            self.assertEqual(result["skill"]["name"], "demo-skill")
            self.assertEqual(result["host"]["version"], "gpt-test")
            encoded = json.dumps(result)
            self.assertNotIn("plain-secret", encoded)
            self.assertNotIn("sk-abc", encoded)
            self.assertEqual(len(observer.list_observations(root)), 1)

            observer.handle_hook(prompt, root)
            duplicate = observer.handle_hook(stop, root)
            self.assertEqual(duplicate["id"], result["id"])
            self.assertEqual(len(observer.list_observations(root)), 1)

    def test_skill_upper_is_not_captured_as_the_target_skill(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            observer.handle_hook(
                {
                    "session_id": "session-control-skill",
                    "turn_id": "turn-1",
                    "hook_event_name": "UserPromptSubmit",
                    "prompt": "Use $skill-upper to record feedback for $demo-skill",
                },
                root,
            )
            result = observer.handle_hook(
                {
                    "session_id": "session-control-skill",
                    "turn_id": "turn-1",
                    "hook_event_name": "Stop",
                },
                root,
            )
            self.assertEqual(result["skill"]["name"], "demo-skill")

    def test_concurrent_duplicate_observation_writes_are_atomic(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            observer.handle_hook(
                {
                    "session_id": "session-atomic",
                    "turn_id": "turn-1",
                    "hook_event_name": "UserPromptSubmit",
                    "prompt": "Use $demo-skill",
                },
                root,
            )
            observation = observer.handle_hook(
                {"session_id": "session-atomic", "turn_id": "turn-1", "hook_event_name": "Stop"}, root
            )
            path = root / f"{observation['id']}.json"
            path.unlink()
            results: list[bool] = []

            def save() -> None:
                results.append(observer.save_observation(root, observation))

            threads = [threading.Thread(target=save) for _ in range(12)]
            for thread in threads:
                thread.start()
            for thread in threads:
                thread.join()
            self.assertEqual(results.count(True), 1)
            self.assertEqual(observer.get_observation(root, observation["id"]), observation)

    def test_unattributed_turn_is_discarded(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            observer.handle_hook(
                {"session_id": "session-2", "turn_id": "turn-1", "hook_event_name": "UserPromptSubmit", "prompt": "Help me"},
                root,
            )
            result = observer.handle_hook(
                {"session_id": "session-2", "turn_id": "turn-1", "hook_event_name": "Stop"}, root
            )
            self.assertIsNone(result)
            self.assertEqual(observer.list_observations(root), [])

    def test_concurrent_markers_are_serialized(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            observer.handle_hook(
                {"session_id": "session-3", "turn_id": "turn-1", "hook_event_name": "UserPromptSubmit", "prompt": "Help me"},
                root,
            )
            observer.handle_hook(
                {
                    "session_id": "session-3",
                    "turn_id": "turn-1",
                    "hook_event_name": "PostToolUse",
                    "tool_name": "mcp__skill_up_observer__mark_skill_invocation",
                    "tool_input": {"skill_name": "demo-skill"},
                },
                root,
            )
            errors: list[Exception] = []

            def attach() -> None:
                try:
                    observer.handle_hook(
                        {
                            "session_id": "session-3",
                            "turn_id": "turn-1",
                            "hook_event_name": "PostToolUse",
                            "tool_name": "mcp__skill_up_observer__attach_skill_evidence",
                            "tool_input": {"kind": "test", "summary": "evidence"},
                        },
                        root,
                    )
                except Exception as error:  # pragma: no cover - assertion reports details.
                    errors.append(error)

            threads = [threading.Thread(target=attach) for _ in range(12)]
            for thread in threads:
                thread.start()
            for thread in threads:
                thread.join()
            self.assertEqual(errors, [])
            result = observer.handle_hook(
                {"session_id": "session-3", "turn_id": "turn-1", "hook_event_name": "Interrupt"}, root
            )
            self.assertEqual(len(result["evidence"]), 12)

    def test_review_and_write_candidate_case(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "data"
            skill = Path(temporary) / "skill"
            (skill / "evals" / "cases").mkdir(parents=True)
            (skill / "SKILL.md").write_text("---\nname: demo-skill\n---\n", encoding="utf-8")
            (skill / "evals" / "eval.yaml").write_text(
                "schema_version: v1alpha1\nenvironment:\n  type: none\nengine:\n  name: codex\ncases:\n  files: []\n",
                encoding="utf-8",
            )
            observer.handle_hook(
                {
                    "session_id": "session-4",
                    "turn_id": "turn-1",
                    "hook_event_name": "UserPromptSubmit",
                    "prompt": "Use $demo-skill",
                },
                root,
            )
            observation = observer.handle_hook(
                {"session_id": "session-4", "turn_id": "turn-1", "hook_event_name": "Stop"}, root
            )
            with self.assertRaises(PermissionError):
                observer.write_candidate_case(root, observation, str(skill))
            observer.set_review(root, observation["id"], "approved")
            with mock.patch.object(observer.shutil, "which", return_value=None):
                result = observer.write_candidate_case(root, observer.get_observation(root, observation["id"]), str(skill))
            self.assertEqual(result["validation"], "skipped (skill-up is not installed)")
            self.assertTrue(Path(result["case_path"]).is_file())
            eval_text = (skill / "evals" / "eval.yaml").read_text(encoding="utf-8")
            self.assertIn(Path(result["case_path"]).name, eval_text)

    def test_reference_only_observation_is_rejected(self) -> None:
        observation = {
            "schema_version": "v1alpha1",
            "id": "obs_0123456789abcdef01234567",
            "host": {"name": "codex"},
            "skill": {"name": "demo-skill"},
            "attribution": {"method": "explicit", "confidence": 1},
            "input": {"ref": "somewhere"},
            "outcome": {"status": "completed"},
            "correlation": {"session_id": "session"},
            "timing": {"observed_at": "2026-09-18T00:00:00Z"},
            "privacy": {"storage": "local", "consent": "test"},
            "review": {"status": "candidate"},
        }
        with self.assertRaisesRegex(ValueError, "input.text"):
            observer.validate_observation(observation)

    def test_append_case_reference_preserves_comments(self) -> None:
        original = "cases:\n  # Keep this comment.\n  files:\n    - evals/cases/existing.yaml\nreports:\n  json: true\n"
        updated = observer.append_case_reference(original, "evals/cases/new.yaml")
        self.assertIn("# Keep this comment.", updated)
        self.assertIn("    - evals/cases/new.yaml\n", updated)
        self.assertTrue(updated.endswith("reports:\n  json: true\n"))

    def test_failed_skill_up_validation_rolls_back_both_files(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "data"
            skill = Path(temporary) / "skill"
            (skill / "evals" / "cases").mkdir(parents=True)
            (skill / "SKILL.md").write_text("---\nname: demo-skill\n---\n", encoding="utf-8")
            eval_path = skill / "evals" / "eval.yaml"
            original = "schema_version: v1alpha1\ncases:\n  files: []\n"
            eval_path.write_text(original, encoding="utf-8")
            observer.handle_hook(
                {
                    "session_id": "session-rollback",
                    "hook_event_name": "UserPromptSubmit",
                    "prompt": "Use $demo-skill",
                },
                root,
            )
            observation = observer.handle_hook(
                {"session_id": "session-rollback", "hook_event_name": "Stop"}, root
            )
            observer.set_review(root, observation["id"], "approved")
            failed = mock.Mock(returncode=1, stderr="invalid suite", stdout="")
            with mock.patch.object(observer.shutil, "which", return_value="/fake/skill-up"), mock.patch.object(
                observer.subprocess, "run", return_value=failed
            ):
                with self.assertRaisesRegex(ValueError, "invalid suite"):
                    observer.write_candidate_case(
                        root, observer.get_observation(root, observation["id"]), str(skill)
                    )
            self.assertEqual(eval_path.read_text(encoding="utf-8"), original)
            self.assertEqual(list((skill / "evals" / "cases").iterdir()), [])

    def test_case_write_rejects_directory_symlink_escape(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "data"
            skill = Path(temporary) / "skill"
            outside = Path(temporary) / "outside"
            (skill / "evals").mkdir(parents=True)
            outside.mkdir()
            (skill / "evals" / "cases").symlink_to(outside, target_is_directory=True)
            (skill / "SKILL.md").write_text("---\nname: demo-skill\n---\n", encoding="utf-8")
            (skill / "evals" / "eval.yaml").write_text(
                "schema_version: v1alpha1\ncases:\n  files: []\n", encoding="utf-8"
            )
            observer.handle_hook(
                {
                    "session_id": "session-escape",
                    "hook_event_name": "UserPromptSubmit",
                    "prompt": "Use $demo-skill",
                },
                root,
            )
            observation = observer.handle_hook(
                {"session_id": "session-escape", "hook_event_name": "Stop"}, root
            )
            observer.set_review(root, observation["id"], "approved")
            with self.assertRaisesRegex(ValueError, "cases directory"):
                observer.write_candidate_case(
                    root, observer.get_observation(root, observation["id"]), str(skill)
                )
            self.assertEqual(list(outside.iterdir()), [])

    def test_mcp_lists_marker_and_review_tools(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            response = observer.handle_rpc({"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, Path(temporary))
            names = {tool["name"] for tool in response["result"]["tools"]}
            self.assertIn("mark_skill_invocation", names)
            self.assertIn("review_skill_observation", names)
            self.assertIn("write_observation_case", names)


if __name__ == "__main__":
    unittest.main()
