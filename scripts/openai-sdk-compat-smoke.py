#!/usr/bin/env python3
"""Opt-in OpenAI Python SDK compatibility smoke for one-llm-router.

The script starts a temporary router process, commits a first API-key
account through the setup API, then points the official Python SDK at the
router's OpenAI-compatible `/v1` data plane.
"""

from __future__ import annotations

import io
import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

REQUIRED_ENV = (
    "LIVE_UPSTREAM_SMOKE",
    "LIVE_OPENAI_API_KEY",
    "LIVE_OPENAI_BASE_URL",
    "LIVE_OPENAI_MODEL",
)


class SmokeFailure(RuntimeError):
    pass


def env_required(key: str) -> str:
    value = os.environ.get(key, "").strip()
    if not value:
        raise SmokeFailure(f"{key} is required")
    return value


def env_optional(key: str) -> str | None:
    value = os.environ.get(key, "").strip()
    return value or None


def normalize_upstream_origin(raw: str) -> str:
    value = raw.strip()
    if "://" not in value:
        value = f"http://{value}"
    parsed = urllib.parse.urlparse(value)
    if parsed.scheme not in ("http", "https") or not parsed.netloc:
        raise SmokeFailure("LIVE_OPENAI_BASE_URL must be an http(s) origin")
    if parsed.username or parsed.password:
        raise SmokeFailure("LIVE_OPENAI_BASE_URL must not include credentials")
    if parsed.path not in ("", "/") or parsed.params or parsed.query or parsed.fragment:
        raise SmokeFailure("LIVE_OPENAI_BASE_URL must be an origin, not a /v1 path")
    return urllib.parse.urlunparse((parsed.scheme, parsed.netloc, "", "", "", ""))


def find_free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def http_json(
    method: str,
    url: str,
    payload: dict[str, Any] | None = None,
    timeout: float = 10.0,
) -> tuple[int, dict[str, Any]]:
    data = None
    headers = {"accept": "application/json"}
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["content-type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = resp.read().decode("utf-8")
            return resp.status, json.loads(body) if body else {}
    except urllib.error.HTTPError as err:
        body = err.read().decode("utf-8")
        try:
            parsed = json.loads(body) if body else {}
        except json.JSONDecodeError:
            parsed = {"raw": body}
        return err.code, parsed


def wait_for_router(base_url: str, timeout_s: float = 10.0) -> None:
    deadline = time.time() + timeout_s
    last_error: Exception | None = None
    while time.time() < deadline:
        try:
            status, _ = http_json("GET", f"{base_url}/api/setup/status", timeout=1.0)
            if status < 500:
                return
        except Exception as err:  # noqa: BLE001 - report the last readiness error.
            last_error = err
        time.sleep(0.2)
    raise SmokeFailure(f"router did not become ready: {last_error}")


def commit_api_key_setup(base_url: str, upstream_origin: str, upstream_api_key: str) -> None:
    status, body = http_json(
        "POST",
        f"{base_url}/api/setup/commit",
        {
            "db": {"driver": "sqlite3", "url": "router.db"},
            "plugins": {
                "admin_auth": {"enabled": False},
                "client_keys": {"enabled": False},
            },
            "first_account": {
                "name": "openai-sdk-compat",
                "provider": "openai",
                "api_key": upstream_api_key,
                "base_url": upstream_origin,
            },
            "runtime": {
                "log_client_request_body": False,
                "log_upstream_request_body": False,
                "log_upstream_response_body": False,
                "log_retention_days": 30,
                "log_level": "info",
            },
        },
    )
    if status != 200 or body.get("code") != 0:
        raise SmokeFailure(f"setup commit failed: status={status} body={body}")


def extract_output_text(response: Any) -> str:
    output_text = getattr(response, "output_text", None)
    if isinstance(output_text, str) and output_text.strip():
        return output_text

    extra = getattr(response, "model_extra", None) or getattr(response, "__pydantic_extra__", None)
    if isinstance(extra, dict):
        raw_output_text = extra.get("output_text")
        if isinstance(raw_output_text, str):
            return raw_output_text

    parts: list[str] = []
    for item in getattr(response, "output", []) or []:
        for content in getattr(item, "content", []) or []:
            text = getattr(content, "text", None)
            if isinstance(text, str):
                parts.append(text)
    return "".join(parts)


def assert_response(response: Any, case_name: str) -> None:
    response_id = getattr(response, "id", None)
    if not isinstance(response_id, str) or not response_id:
        raise SmokeFailure(f"{case_name}: SDK response missing id")
    if not extract_output_text(response).strip():
        raise SmokeFailure(f"{case_name}: SDK response missing output text")


def run_sdk_cases(router_url: str, model: str) -> int:
    from openai import OpenAI

    client = OpenAI(
        api_key="sdk-client-placeholder",
        base_url=f"{router_url}/v1",
        max_retries=0,
        timeout=60.0,
    )

    response = client.responses.create(
        model=model,
        input="Reply with the single word pong.",
        max_output_tokens=16,
    )
    assert_response(response, "sdk.text_input_string")
    print("[sdk-compat] sdk.text_input_string ok")
    completed = 1

    response = client.responses.create(
        model=model,
        input=[
            {
                "role": "user",
                "content": [{"type": "input_text", "text": "Reply with the single word pong."}],
            }
        ],
        max_output_tokens=16,
    )
    assert_response(response, "sdk.text_input_list")
    print("[sdk-compat] sdk.text_input_list ok")
    completed += 1

    stream = client.responses.create(
        model=model,
        input="Reply with the single word pong.",
        max_output_tokens=16,
        stream=True,
    )
    seen_delta = False
    seen_completed = False
    for event in stream:
        event_type = getattr(event, "type", "")
        if event_type == "response.output_text.delta":
            seen_delta = True
        if event_type == "response.completed":
            seen_completed = True
    if not seen_delta:
        raise SmokeFailure("sdk.streaming: no response.output_text.delta event")
    if not seen_completed:
        raise SmokeFailure("sdk.streaming: no response.completed event")
    print("[sdk-compat] sdk.streaming ok")
    completed += 1

    models = client.models.list()
    if not hasattr(models, "data"):
        raise SmokeFailure("sdk.models.list: SDK response missing data")
    print("[sdk-compat] sdk.models.list ok")
    completed += 1

    chat_model = env_optional("LIVE_OPENAI_CHAT_MODEL") or model
    chat = client.chat.completions.create(
        model=chat_model,
        messages=[{"role": "user", "content": "Reply with the single word pong."}],
        max_completion_tokens=16,
    )
    if not getattr(chat, "id", None):
        raise SmokeFailure("sdk.chat.completions.create: response missing id")
    if not getattr(chat, "choices", None):
        raise SmokeFailure("sdk.chat.completions.create: response missing choices")
    first_choice = chat.choices[0]
    content = getattr(getattr(first_choice, "message", None), "content", None)
    if not isinstance(content, str) or not content.strip():
        raise SmokeFailure("sdk.chat.completions.create: response missing message content")
    print("[sdk-compat] sdk.chat.completions.create ok")
    completed += 1

    chat_stream = client.chat.completions.create(
        model=chat_model,
        messages=[{"role": "user", "content": "Reply with the single word pong."}],
        max_completion_tokens=16,
        stream=True,
    )
    seen_chunk_id = False
    seen_delta = False
    for chunk in chat_stream:
        if getattr(chunk, "id", None):
            seen_chunk_id = True
        for choice in getattr(chunk, "choices", []) or []:
            delta = getattr(choice, "delta", None)
            content = getattr(delta, "content", None)
            if isinstance(content, str) and content:
                seen_delta = True
    if not seen_chunk_id:
        raise SmokeFailure("sdk.chat.completions.stream: no chunk id")
    if not seen_delta:
        raise SmokeFailure("sdk.chat.completions.stream: no delta content")
    print("[sdk-compat] sdk.chat.completions.stream ok")
    completed += 1

    if env_optional("LIVE_OPENAI_CHAT_MODEL") is None:
        print(
            "[sdk-compat] sdk.chat.completions model defaulted to LIVE_OPENAI_MODEL "
            "(set LIVE_OPENAI_CHAT_MODEL to override)"
        )

    return completed


def assert_unsupported_audio(router_url: str) -> None:
    from openai import APIStatusError, OpenAI

    client = OpenAI(
        api_key="sdk-client-placeholder",
        base_url=f"{router_url}/v1",
        max_retries=0,
        timeout=60.0,
    )
    audio = io.BytesIO(b"not-a-real-audio-file")
    audio.name = "sample.wav"
    try:
        client.audio.transcriptions.create(model="whisper-1", file=audio)
    except APIStatusError as exc:
        status = exc.status_code
        try:
            body = exc.response.json()
        except Exception:
            body = {}
    else:
        raise SmokeFailure("sdk.audio.transcriptions.create unexpectedly succeeded")

    error = body.get("error", {}) if isinstance(body, dict) else {}
    if (
        status != 404
        or error.get("code") != "unsupported_endpoint"
        or error.get("type") != "router_error"
    ):
        raise SmokeFailure(f"unsupported audio contract drifted: status={status} body={body}")
    print("[sdk-compat] sdk.audio.transcriptions.unsupported ok")


def assert_admin_usage_observed(router_url: str, min_request_count: int) -> None:
    deadline = time.time() + 10.0
    last_body: dict[str, Any] | None = None
    while time.time() < deadline:
        status, body = http_json("GET", f"{router_url}/api/admin/usage")
        last_body = body
        data = body.get("data", {}) if isinstance(body, dict) else {}
        if status == 200 and body.get("code") == 0 and data.get("request_count", 0) >= min_request_count:
            print("[sdk-compat] router.admin_usage ok")
            return
        time.sleep(0.25)
    raise SmokeFailure(f"/api/admin/usage did not observe SDK traffic: {last_body}")


def assert_dashboard_observed(router_url: str, min_request_count: int) -> None:
    deadline = time.time() + 10.0
    last_body: dict[str, Any] | None = None
    while time.time() < deadline:
        status, body = http_json("GET", f"{router_url}/api/admin/dashboard?range=1h")
        last_body = body
        cards = body.get("data", {}).get("cards", {}) if status == 200 else {}
        total = cards.get("requests", {}).get("total", 0)
        ttft_samples = cards.get("ttft", {}).get("sample_count", 0)
        if body.get("code") == 0 and total >= min_request_count and ttft_samples >= 1:
            return
        time.sleep(0.25)
    raise SmokeFailure(f"dashboard did not observe SDK traffic: {last_body}")


def scrub(text: str) -> str:
    for key in ("LIVE_OPENAI_API_KEY", "LIVE_OPENAI_BASE_URL"):
        secret = os.environ.get(key, "")
        if secret:
            text = text.replace(secret, "<redacted>")
    return text


def tail(path: Path, lines: int = 80) -> str:
    try:
        content = path.read_text(encoding="utf-8", errors="replace").splitlines()
    except FileNotFoundError:
        return ""
    return "\n".join(content[-lines:])


def main() -> int:
    if env_required("LIVE_UPSTREAM_SMOKE") != "1":
        raise SmokeFailure("LIVE_UPSTREAM_SMOKE=1 is required")
    for key in REQUIRED_ENV[1:]:
        env_required(key)

    upstream_origin = normalize_upstream_origin(env_required("LIVE_OPENAI_BASE_URL"))
    upstream_api_key = env_required("LIVE_OPENAI_API_KEY")
    model = env_required("LIVE_OPENAI_MODEL")
    router_bin = Path(os.environ.get("ROUTER_BINARY", "./one-llm-router")).resolve()
    if not router_bin.is_file():
        raise SmokeFailure(f"router binary not found: {router_bin}")

    with tempfile.TemporaryDirectory(prefix="one-llm-router-sdk-compat.") as runtime_dir:
        runtime_path = Path(runtime_dir)
        log_path = runtime_path / "router.log"
        port = find_free_port()
        router_url = f"http://127.0.0.1:{port}"

        env = os.environ.copy()
        for key in list(env):
            if key.startswith("ROUTER_"):
                del env[key]
        env["ROUTER_LISTEN_ADDR"] = f"127.0.0.1:{port}"

        with log_path.open("w", encoding="utf-8") as log_file:
            proc = subprocess.Popen(
                [str(router_bin), "-config", "./config.json"],
                cwd=runtime_path,
                env=env,
                stdout=log_file,
                stderr=subprocess.STDOUT,
                text=True,
            )
            try:
                wait_for_router(router_url)
                commit_api_key_setup(router_url, upstream_origin, upstream_api_key)
                completed = run_sdk_cases(router_url, model)
                assert_unsupported_audio(router_url)
                assert_admin_usage_observed(router_url, completed)
                assert_dashboard_observed(router_url, completed)
                print("[sdk-compat] dashboard observed SDK traffic")
                return 0
            except Exception:
                proc.poll()
                print("[sdk-compat] router log tail:", file=sys.stderr)
                print(scrub(tail(log_path)), file=sys.stderr)
                raise
            finally:
                if proc.poll() is None:
                    proc.terminate()
                    try:
                        proc.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        proc.kill()
                        proc.wait(timeout=3)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except SmokeFailure as err:
        print(f"[sdk-compat] failed: {err}", file=sys.stderr)
        raise SystemExit(1)
    except Exception as err:  # noqa: BLE001 - keep CI output compact and scrubbed.
        print(f"[sdk-compat] failed: {scrub(str(err))}", file=sys.stderr)
        raise SystemExit(1)
