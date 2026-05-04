#!/usr/bin/env python3
"""Unit tests for openai-sdk-compat-smoke.py helpers."""

from __future__ import annotations

import importlib.util
import unittest
from pathlib import Path
from types import SimpleNamespace


def load_smoke_module():
    script = Path(__file__).with_name("openai-sdk-compat-smoke.py")
    spec = importlib.util.spec_from_file_location("openai_sdk_compat_smoke", script)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load {script}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


smoke = load_smoke_module()


class SmokeHelperTests(unittest.TestCase):
    def test_extract_output_text_prefers_direct_field(self) -> None:
        response = SimpleNamespace(output_text="direct", model_extra={"output_text": "extra"})
        self.assertEqual("direct", smoke.extract_output_text(response))

    def test_extract_output_text_reads_model_extra(self) -> None:
        response = SimpleNamespace(output_text="", model_extra={"output_text": "from-extra"})
        self.assertEqual("from-extra", smoke.extract_output_text(response))

    def test_extract_output_text_reads_pydantic_extra(self) -> None:
        response = SimpleNamespace(__pydantic_extra__={"output_text": "from-pydantic-extra"})
        self.assertEqual("from-pydantic-extra", smoke.extract_output_text(response))

    def test_extract_output_text_joins_nested_content_text(self) -> None:
        response = SimpleNamespace(
            output=[
                SimpleNamespace(content=[SimpleNamespace(text="hello"), SimpleNamespace(text=" ")]),
                SimpleNamespace(content=[SimpleNamespace(text="world")]),
            ]
        )
        self.assertEqual("hello world", smoke.extract_output_text(response))

    def test_assert_response_rejects_blank_output(self) -> None:
        response = SimpleNamespace(id="resp_test", output_text=" ")
        with self.assertRaisesRegex(smoke.SmokeFailure, "missing output text"):
            smoke.assert_response(response, "responses.create")

    def test_normalize_upstream_origin_rejects_userinfo(self) -> None:
        with self.assertRaisesRegex(smoke.SmokeFailure, "must not include credentials"):
            smoke.normalize_upstream_origin("https://user:pass@example.com")


if __name__ == "__main__":
    unittest.main()
