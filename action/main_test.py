#!/usr/bin/env python3
"""Characterization tests for the public GitHub Action adapter."""

import importlib.util
import pathlib
import unittest


MODULE_PATH = pathlib.Path(__file__).with_name("main.py")
SPEC = importlib.util.spec_from_file_location("skill_up_action_main", MODULE_PATH)
ACTION = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ACTION)


class ActionConfigurationContractTest(unittest.TestCase):
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
