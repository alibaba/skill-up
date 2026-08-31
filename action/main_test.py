#!/usr/bin/env python3
"""Characterization tests for the public GitHub Action adapter."""

import importlib.util
import pathlib
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock


MODULE_PATH = pathlib.Path(__file__).with_name("main.py")
SPEC = importlib.util.spec_from_file_location("skill_up_action_main", MODULE_PATH)
ACTION = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ACTION)


class ActionConfigurationContractTest(unittest.TestCase):
    def test_main_composes_codex_argv_and_environment(self):
        inputs = SimpleNamespace(
            engine="codex",
            model="qwen3.6-plus",
            provider="dashscope",
            api_key="secret",
            base_url="",
            open_sandbox_api_key="",
            skill_target="evals/eval.yaml",
            skill_up_version="0.7.0",
            skill_up_command="skill-up run",
            parallelism="",
            agent_install_command="",
        )
        final_run = mock.Mock(return_value=SimpleNamespace(returncode=0))

        with tempfile.TemporaryDirectory() as workspace:
            with (
                mock.patch.object(ACTION, "parse_inputs", return_value=inputs),
                mock.patch.object(ACTION.shutil, "which", return_value="/usr/bin/skill-up"),
                mock.patch.object(ACTION, "_run"),
                mock.patch.object(ACTION, "get_skill_up_version", return_value=(0, 7, 0)),
                mock.patch.object(ACTION, "command_supports_explicit_provider", return_value=True),
                mock.patch.object(ACTION.subprocess, "run", final_run),
                mock.patch.dict(ACTION.os.environ, {"GITHUB_WORKSPACE": workspace}, clear=True),
                mock.patch("builtins.print"),
                self.assertRaises(SystemExit) as exit_context,
            ):
                ACTION.main()

        self.assertEqual(exit_context.exception.code, 0)
        final_run.assert_called_once()
        argv = final_run.call_args.args[0]
        run_env = final_run.call_args.kwargs["env"]

        engine_flag_index = argv.index("--engine")
        self.assertEqual(
            argv[engine_flag_index:engine_flag_index + 6],
            ["--engine", "codex", "--provider", "dashscope", "--model", "qwen3.6-plus"],
        )
        self.assertEqual(run_env["OPENAI_MODEL"], "qwen3.6-plus")
        self.assertEqual(run_env["OPENAI_API_KEY"], "secret")
        self.assertEqual(
            run_env["OPENAI_BASE_URL"],
            "https://dashscope.aliyuncs.com/compatible-mode/v1",
        )
        self.assertEqual(run_env["DASHSCOPE_API_KEY"], "secret")
        self.assertEqual(
            run_env["DASHSCOPE_BASE_URL"],
            "https://dashscope.aliyuncs.com/compatible-mode/v1",
        )

    def test_protocol_selects_provider_endpoint(self):
        cases = [
            ("claude_code", "dashscope", "", "https://dashscope.aliyuncs.com/apps/anthropic"),
            ("codex", "dashscope", "", "https://dashscope.aliyuncs.com/compatible-mode/v1"),
            ("qwen_code", "dashscope", "", "https://dashscope.aliyuncs.com/compatible-mode/v1"),
            ("qodercli", "dashscope", "", ""),
            ("", "dashscope", "", ""),
            ("claude_code", "dashscope", "https://gateway.example/v1", "https://gateway.example/v1"),
        ]

        for engine, provider, explicit_url, expected in cases:
            with self.subTest(engine=engine, provider=provider, explicit_url=explicit_url):
                self.assertEqual(
                    ACTION.resolve_base_url(engine, provider, explicit_url),
                    expected,
                )

    def test_model_ref_preserves_historical_action_translation(self):
        cases = [
            ("codex", "dashscope", "qwen3.6-plus", "dashscope/qwen3.6-plus"),
            ("codex", "dashscope", "org/model", "org/model"),
            ("claude_code", "dashscope", "qwen3.6-plus", "qwen3.6-plus"),
            ("qodercli", "qoder", "auto", "auto"),
            ("qwen_code", "dashscope", "qwen3.6-plus", "qwen3.6-plus"),
            ("", "dashscope", "qwen3.6-plus", "qwen3.6-plus"),
            ("codex", "dashscope", "", ""),
        ]

        for engine, provider, model, expected in cases:
            with self.subTest(engine=engine, provider=provider, model=model):
                self.assertEqual(
                    ACTION.compose_model_ref(provider, model, engine),
                    expected,
                )

    def test_explicit_engine_routes_auth_without_cli_api_key(self):
        argv = ACTION.build_skill_up_argv(
            "skill-up run",
            "codex",
            "qwen3.6-plus",
            "dashscope",
            "",
            "/tmp/reports",
            "evals/eval.yaml",
            api_key="secret",
        )

        self.assertEqual(argv[:6], [
            "skill-up", "run", "--engine", "codex",
            "--model", "dashscope/qwen3.6-plus",
        ])
        self.assertNotIn("--api-key", argv)
        self.assertEqual(
            ACTION.engine_env("codex", "secret", "https://gateway.example/v1"),
            {
                "OPENAI_API_KEY": "secret",
                "OPENAI_BASE_URL": "https://gateway.example/v1",
            },
        )

    def test_new_cli_receives_separate_provider_and_opaque_model(self):
        argv = ACTION.build_skill_up_argv(
            "skill-up run",
            "codex",
            "org/model",
            "dashscope",
            "",
            "/tmp/reports",
            "evals/eval.yaml",
            explicit_provider_supported=True,
        )

        self.assertEqual(argv[:8], [
            "skill-up", "run", "--engine", "codex",
            "--provider", "dashscope", "--model", "org/model",
        ])

    def test_old_cli_keeps_legacy_provider_model_translation(self):
        argv = ACTION.build_skill_up_argv(
            "skill-up run",
            "codex",
            "qwen3.6-plus",
            "dashscope",
            "",
            "/tmp/reports",
            "evals/eval.yaml",
            explicit_provider_supported=False,
        )

        self.assertEqual(argv[:6], [
            "skill-up", "run", "--engine", "codex",
            "--model", "dashscope/qwen3.6-plus",
        ])

    def test_detects_explicit_provider_from_run_help(self):
        completed = SimpleNamespace(stdout="Flags:\n  --provider string\n")
        with mock.patch.object(ACTION.subprocess, "run", return_value=completed) as run:
            self.assertTrue(ACTION.command_supports_explicit_provider("skill-up run"))
        run.assert_called_once_with(
            ["skill-up", "run", "--help"],
            env=ACTION.os.environ,
            capture_output=True,
            text=True,
            check=True,
        )

    def test_provider_reference_in_other_help_text_is_not_feature_support(self):
        completed = SimpleNamespace(stdout="  --model string  opaque with --provider\n")
        with mock.patch.object(ACTION.subprocess, "run", return_value=completed):
            self.assertFalse(ACTION.command_supports_explicit_provider("skill-up run"))

    def test_empty_engine_delegates_to_eval_yaml(self):
        argv = ACTION.build_skill_up_argv(
            "skill-up run",
            "",
            "org/model",
            "dashscope",
            "",
            "/tmp/reports",
            "evals/eval.yaml",
            api_key="secret",
        )

        self.assertNotIn("--engine", argv)
        self.assertEqual(argv[2:6], ["--api-key", "secret", "--model", "org/model"])

    def test_absent_credentials_leave_agent_login_untouched(self):
        for engine in ACTION.ENGINE_PROTOCOL:
            with self.subTest(engine=engine):
                self.assertEqual(ACTION.engine_env(engine, "", ""), {})
        self.assertEqual(ACTION.provider_env("dashscope", "", ""), {})
        self.assertEqual(ACTION.unified_base_url_env("", ""), {})

    def test_empty_engine_exports_both_protocol_endpoints(self):
        self.assertEqual(
            ACTION.unified_base_url_env("dashscope", ""),
            {
                "OPENAI_BASE_URL": "https://dashscope.aliyuncs.com/compatible-mode/v1",
                "ANTHROPIC_BASE_URL": "https://dashscope.aliyuncs.com/apps/anthropic",
            },
        )

    def test_unknown_engine_fails_fast(self):
        with self.assertRaisesRegex(ValueError, "unknown engine"):
            ACTION.resolve_base_url("unknown", "dashscope", "")


if __name__ == "__main__":
    unittest.main()
