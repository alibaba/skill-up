#!/usr/bin/env python3
"""Bridge skill-up's stateful Custom Engine contract to DSH's ACP profile."""

from __future__ import annotations

import argparse
import base64
import json
import os
import queue
import re
import signal
import subprocess
import sys
import threading
import time
import uuid
from pathlib import Path
from typing import Any


def yaml_string(value: str) -> str:
    """Return a JSON string, which is also a valid YAML scalar."""
    return json.dumps(value, ensure_ascii=False)


def safe_segment(value: str, fallback: str) -> str:
    value = re.sub(r"[^A-Za-z0-9._-]+", "-", value).strip("-.")
    return value or fallback


def output_text(value: str | bytes | None) -> str:
    if value is None:
        return ""
    if isinstance(value, bytes):
        return value.decode("utf-8", errors="replace")
    return value


def redact_secrets(value: str) -> str:
    for name in ("DSH_API_KEY", "DASHSCOPE_EVAL_API_KEY"):
        secret = os.environ.get(name, "")
        if secret:
            value = value.replace(secret, "[REDACTED]")
    return value


def redact_data(value: Any) -> Any:
    if isinstance(value, str):
        return redact_secrets(value)
    if isinstance(value, list):
        return [redact_data(item) for item in value]
    if isinstance(value, dict):
        return {
            redact_secrets(key) if isinstance(key, str) else key: redact_data(item)
            for key, item in value.items()
        }
    return value


def sanitize_run_artifacts(root: Path) -> None:
    secrets = {
        value.encode()
        for name in ("DSH_API_KEY", "DASHSCOPE_EVAL_API_KEY")
        if (value := os.environ.get(name, ""))
    }
    if not secrets:
        return
    for path in root.rglob("*"):
        if not path.is_file():
            continue
        content = path.read_bytes()
        sanitized = content
        for secret in secrets:
            sanitized = sanitized.replace(secret, b"[REDACTED]")
        if sanitized != content:
            path.write_bytes(sanitized)


def terminate_process_tree(process: subprocess.Popen[str]) -> None:
    if os.name == "nt":
        subprocess.run(
            ["taskkill", "/PID", str(process.pid), "/T", "/F"],
            capture_output=True,
            check=False,
        )
        return
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        return


def run_dsh_acp(
    command: list[str],
    workspace: Path,
    env: dict[str, str],
    timeout: float,
    prompt: str,
    session_id: str,
) -> tuple[int, str, str, bool, str, list[dict[str, Any]]]:
    process_options: dict[str, Any] = {}
    if os.name == "nt":
        process_options["creationflags"] = subprocess.CREATE_NEW_PROCESS_GROUP
    else:
        process_options["start_new_session"] = True
    process = subprocess.Popen(
        command,
        cwd=workspace,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        stdin=subprocess.PIPE,
        text=True,
        bufsize=1,
        **process_options,
    )
    assert process.stdin is not None
    assert process.stdout is not None
    assert process.stderr is not None

    stdout_lines: queue.Queue[str | None] = queue.Queue()
    stderr_lines: list[str] = []
    protocol_frames: list[dict[str, Any]] = []

    def read_stdout() -> None:
        for line in process.stdout:
            stdout_lines.put(line)
        stdout_lines.put(None)

    def read_stderr() -> None:
        stderr_lines.extend(process.stderr)

    stdout_thread = threading.Thread(target=read_stdout, daemon=True)
    stderr_thread = threading.Thread(target=read_stderr, daemon=True)
    stdout_thread.start()
    stderr_thread.start()
    deadline = time.monotonic() + timeout

    def write_frame(frame: dict[str, Any]) -> None:
        protocol_frames.append({"direction": "client", "frame": redact_data(frame)})
        process.stdin.write(json.dumps(frame, ensure_ascii=False) + "\n")
        process.stdin.flush()

    def reply_to_permission(frame: dict[str, Any]) -> None:
        options = (frame.get("params") or {}).get("options") or []
        selected = next(
            (
                option
                for kind in ("allow_always", "allow_once")
                for option in options
                if option.get("kind") == kind
            ),
            None,
        )
        outcome: dict[str, Any]
        if selected is None:
            outcome = {"outcome": "cancelled"}
        else:
            outcome = {
                "outcome": "selected",
                "optionId": selected["optionId"],
            }
        write_frame(
            {
                "jsonrpc": "2.0",
                "id": frame["id"],
                "result": {"outcome": outcome},
            }
        )

    assistant_messages: dict[str, list[str]] = {}
    assistant_message_order: list[str] = []

    def request(request_id: int, method: str, params: dict[str, Any]) -> dict[str, Any]:
        write_frame(
            {
                "jsonrpc": "2.0",
                "id": request_id,
                "method": method,
                "params": params,
            }
        )
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError(f"ACP request {method} timed out")
            try:
                line = stdout_lines.get(timeout=remaining)
            except queue.Empty as exc:
                raise TimeoutError(f"ACP request {method} timed out") from exc
            if line is None:
                raise RuntimeError(
                    f"DSH ACP exited before responding to {method} (exit={process.poll()})"
                )
            try:
                frame = json.loads(line)
            except json.JSONDecodeError as exc:
                raise RuntimeError(f"DSH ACP emitted non-JSON stdout: {line.strip()}") from exc
            if not isinstance(frame, dict):
                continue
            protocol_frames.append({"direction": "agent", "frame": redact_data(frame)})
            if frame.get("method") == "session/request_permission" and "id" in frame:
                reply_to_permission(frame)
                continue
            if frame.get("method") == "session/update":
                update = (frame.get("params") or {}).get("update") or {}
                content = update.get("content") or {}
                if (
                    update.get("sessionUpdate") == "agent_message_chunk"
                    and content.get("type") == "text"
                ):
                    message_id = str(update.get("messageId") or "unidentified")
                    if message_id not in assistant_messages:
                        assistant_messages[message_id] = []
                        assistant_message_order.append(message_id)
                    assistant_messages[message_id].append(
                        str(content.get("text") or "")
                    )
                continue
            if frame.get("id") != request_id:
                continue
            if frame.get("error") is not None:
                error = frame["error"]
                message = error.get("message") if isinstance(error, dict) else error
                data = error.get("data") if isinstance(error, dict) else None
                details = data.get("details") if isinstance(data, dict) else None
                if details and details not in str(message):
                    message = f"{message}: {details}"
                raise RuntimeError(f"DSH ACP {method} failed: {message}")
            result = frame.get("result")
            return result if isinstance(result, dict) else {}

    request_id = 0

    def rpc(method: str, params: dict[str, Any]) -> dict[str, Any]:
        nonlocal request_id
        request_id += 1
        return request(request_id, method, params)

    def open_session(method: str, params: dict[str, Any]) -> dict[str, Any]:
        delay = 0.1
        ready_deadline = min(deadline, time.monotonic() + 5)
        while True:
            try:
                return rpc(method, params)
            except RuntimeError as exc:
                if (
                    "no adapter registered for provider" not in str(exc)
                    or time.monotonic() + delay >= ready_deadline
                ):
                    raise
                time.sleep(delay)
                delay = min(delay * 2, 1.0)

    active_session_id = session_id

    def final_assistant_message() -> str:
        if not assistant_message_order:
            return ""
        return "".join(assistant_messages[assistant_message_order[-1]])

    try:
        rpc(
            "initialize",
            {
                "protocolVersion": 1,
                "clientCapabilities": {},
                "clientInfo": {"name": "skill-up-dsh-runner", "version": "1"},
            },
        )
        if active_session_id:
            open_session(
                "session/resume",
                {
                    "sessionId": active_session_id,
                    "cwd": str(workspace),
                    "mcpServers": [],
                },
            )
        else:
            created = open_session(
                "session/new",
                {"cwd": str(workspace), "mcpServers": []},
            )
            active_session_id = str(created.get("sessionId") or "")
            if not active_session_id:
                raise RuntimeError("DSH ACP session/new returned no sessionId")
        rpc(
            "session/prompt",
            {
                "sessionId": active_session_id,
                "prompt": [{"type": "text", "text": prompt}],
            },
        )
        rpc("session/close", {"sessionId": active_session_id})
        process.stdin.close()
        remaining = max(0.1, deadline - time.monotonic())
        process.wait(timeout=remaining)
        stdout_thread.join(timeout=1)
        stderr_thread.join(timeout=1)
        return (
            process.returncode,
            final_assistant_message(),
            "".join(stderr_lines),
            False,
            active_session_id,
            protocol_frames,
        )
    except (TimeoutError, subprocess.TimeoutExpired) as exc:
        terminate_process_tree(process)
        process.wait()
        stderr_lines.append(f"\n{exc}")
        stdout_thread.join(timeout=1)
        stderr_thread.join(timeout=1)
        return (
            124,
            final_assistant_message(),
            "".join(stderr_lines),
            True,
            active_session_id,
            protocol_frames,
        )
    except Exception as exc:  # noqa: BLE001 - transport errors belong in SessionResult
        terminate_process_tree(process)
        process.wait()
        stderr_lines.append(f"\n{exc}")
        stdout_thread.join(timeout=1)
        stderr_thread.join(timeout=1)
        return (
            1,
            final_assistant_message(),
            "".join(stderr_lines),
            False,
            active_session_id,
            protocol_frames,
        )


def encode_session_ref(run_id: str, session_id: str) -> str:
    payload = json.dumps({"run": run_id, "session": session_id}, separators=(",", ":"))
    token = base64.urlsafe_b64encode(payload.encode()).decode().rstrip("=")
    return f"dsh1.{token}"


def decode_session_ref(value: str) -> tuple[str, str]:
    if not value.startswith("dsh1."):
        raise ValueError("unsupported DSH session reference")
    token = value.removeprefix("dsh1.")
    token += "=" * (-len(token) % 4)
    payload = json.loads(base64.urlsafe_b64decode(token).decode())
    run_id = str(payload.get("run") or "")
    session_id = str(payload.get("session") or "")
    if safe_segment(run_id, "") != run_id or not session_id:
        raise ValueError("invalid DSH session reference")
    return run_id, session_id


def write_dsh_config(home: Path, session_root: Path, skills_dir: Path) -> None:
    model = os.environ.get("DSH_MODEL", "qwen3.8-max")
    base_url = os.environ.get(
        "DSH_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"
    )
    patch_lines = [
        "- id: agent-default-model",
        "  config:",
        "    provider: skill-up-openai",
        f"    model: {yaml_string(model)}",
        "- id: acp",
        "  config:",
        "    provider: skill-up-openai",
        f"    model: {yaml_string(model)}",
        "- id: session-persistence-jsonl",
        "  config:",
        f"    root: {yaml_string(str(session_root))}",
        "    compression: none",
        "    packChunks: false",
        "- id: session-title-llm",
        "  disabled: true",
    ]
    if skills_dir.is_dir():
        patch_lines.extend(
            [
                "- id: skill-filesystem",
                "  config:",
                "    includeDefaultRoots: false",
                "    customSkillDirs:",
                f"      - {yaml_string(str(skills_dir))}",
            ]
        )
    (home / "cordis.patch.yml").write_text("\n".join(patch_lines) + "\n")

    settings = "\n".join(
        [
            "llm-pi-ai:",
            "  providers:",
            "    skill-up-openai:",
            "      apiKeyEnv: DSH_API_KEY",
            "      api: openai-completions",
            f"      baseURL: {yaml_string(base_url)}",
            "      models:",
            f"        - id: {yaml_string(model)}",
            "",
        ]
    )
    (home / "settings.yaml").write_text(settings)


def transcript(messages: list[dict[str, Any]], final_message: str) -> list[dict[str, Any]]:
    result = []
    current_turn = 1
    for message in messages:
        role = str(message.get("role", "user"))
        if role == "user" and result:
            current_turn += 1
        result.append(
            {
                "role": role,
                "content": redact_secrets(str(message.get("content", ""))),
                "turn": current_turn,
            }
        )
    result.append({"role": "assistant", "content": final_message, "turn": current_turn})
    return result


def content_text(blocks: Any) -> str:
    if not isinstance(blocks, list):
        return ""
    parts = []
    for block in blocks:
        if not isinstance(block, dict):
            continue
        if block.get("type") == "text" and block.get("text"):
            parts.append(redact_secrets(str(block["text"])))
        elif block.get("type") == "tool-result":
            nested = content_text(block.get("content"))
            if nested:
                parts.append(nested)
    return "\n".join(parts)


def parse_arguments(raw: Any) -> dict[str, Any]:
    if isinstance(raw, dict):
        return redact_data(raw)
    if not isinstance(raw, str) or not raw:
        return {}
    try:
        parsed = json.loads(raw)
    except json.JSONDecodeError:
        return {"_raw": redact_secrets(raw)}
    value = parsed if isinstance(parsed, dict) else {"_raw": raw}
    return redact_data(value)


def read_session(
    session_file: Path | None,
    input_messages: list[dict[str, Any]],
    final_message: str,
) -> tuple[str, str, list[dict[str, Any]], int, int, int]:
    if session_file is None:
        fallback = transcript(input_messages, final_message)
        return "", final_message, fallback, 0, 0, 1

    session_id = session_file.stem
    records: list[dict[str, Any]] = []
    latest_turn = 1
    for line in session_file.read_text().splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not isinstance(event, dict):
            continue
        if event.get("type") == "session":
            session_id = str(event.get("id") or session_id)
        data = event.get("data")
        if isinstance(data, dict):
            latest_turn = max(latest_turn, int(data.get("turn") or 1))
        records.append(event)

    events: list[dict[str, Any]] = []
    input_tokens = 0
    output_tokens = 0
    for event in records:
        data = event.get("data")
        if not isinstance(data, dict):
            continue
        turn = int(data.get("turn") or 1)
        if turn != latest_turn:
            continue
        if event.get("type") in ("assistant/message", "assistant/attempt"):
            usage = data.get("usage") or {}
            message = data.get("message") or {}
            blocks = message.get("content") if isinstance(message, dict) else []
            if event.get("type") == "assistant/attempt":
                blocks = []
                for stream_item in data.get("stream") or []:
                    if not isinstance(stream_item, dict) or stream_item.get("type") != "chunk":
                        continue
                    chunk = stream_item.get("chunk") or {}
                    if not isinstance(chunk, dict):
                        continue
                    if chunk.get("type") == "usage":
                        usage = chunk.get("usage") or usage
                    elif chunk.get("type") == "block-end" and isinstance(
                        chunk.get("block"), dict
                    ):
                        blocks.append(chunk["block"])
            if isinstance(usage, dict):
                input_tokens += sum(
                    int(usage.get(key) or 0)
                    for key in ("inputTokens", "cacheReadTokens", "cacheWriteTokens")
                )
                output_tokens += int(usage.get("outputTokens") or 0)
            for block in blocks if isinstance(blocks, list) else []:
                if not isinstance(block, dict):
                    continue
                if block.get("type") == "text" and block.get("text"):
                    events.append(
                        {
                            "role": "assistant",
                            "content": redact_secrets(str(block["text"])),
                            "turn": 1,
                        }
                    )
                elif block.get("type") == "tool-call":
                    events.append(
                        {
                            "role": "tool_call",
                            "turn": 1,
                            "tool_call": {
                                "id": str(block.get("id") or ""),
                                "name": str(block.get("name") or ""),
                                "arguments": parse_arguments(block.get("arguments")),
                            },
                        }
                    )
        elif event.get("type") == "tool/result":
            message = data.get("message") or {}
            blocks = message.get("content") if isinstance(message, dict) else []
            for block in blocks if isinstance(blocks, list) else []:
                if not isinstance(block, dict) or block.get("type") != "tool-result":
                    continue
                events.append(
                    {
                        "role": "tool_result",
                        "turn": 1,
                        "content": content_text(block.get("content")),
                        "tool_result": {
                            "call_id": str(block.get("toolCallId") or ""),
                            "status": (
                                "error"
                                if block.get("isError") is True or data.get("error")
                                else "success"
                            ),
                            "content": content_text(block.get("content")),
                        },
                    }
                )

    normalized_inputs = []
    current_turn = 1
    for message in input_messages:
        role = str(message.get("role", "user"))
        if role == "user" and normalized_inputs:
            current_turn += 1
        normalized_inputs.append(
            {
                "role": role,
                "content": redact_secrets(str(message.get("content", ""))),
                "turn": current_turn,
            }
        )
    if not final_message:
        final_message = next(
            (
                str(item.get("content") or "")
                for item in reversed(events)
                if item.get("role") == "assistant" and item.get("content")
            ),
            "",
        )
    combined = normalized_inputs + events
    if not any(
        item.get("role") == "assistant" and item.get("content", "").strip() == final_message
        for item in combined
    ):
        combined.append({"role": "assistant", "content": final_message, "turn": 1})
    return session_id, final_message, combined, input_tokens, output_tokens, 1


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()

    request = json.loads(Path(args.input).read_text())
    workspace = Path(request["workspace"]).resolve()
    run_root = workspace / ".skill-up-dsh" / "runs"
    run_root.mkdir(parents=True, exist_ok=True)
    session_ref = str(request.get("session_id") or "")
    dsh_session_id = ""
    if session_ref:
        run_id, dsh_session_id = decode_session_ref(session_ref)
    else:
        run_id = "-".join(
            [
                safe_segment(str(request.get("case_id", "case")), "case"),
                safe_segment(str(request.get("variant", "run")), "run"),
                uuid.uuid4().hex[:12],
            ]
        )
    home = run_root / run_id
    session_root = home / "sessions"
    home.mkdir(parents=True, exist_ok=True)
    session_root.mkdir(exist_ok=True)
    skills_dir = Path(os.environ.get("DSH_SKILLS_DIR", workspace / ".dsh-skills"))
    write_dsh_config(home, session_root, skills_dir)

    messages = request.get("messages") or []
    if len(messages) != 1 or messages[0].get("role") != "user":
        raise ValueError("stateful DSH runner expects exactly one user message per invocation")
    prompt = str(messages[0].get("content", ""))
    timeout = float(request.get("timeout_seconds") or 300)
    timeout_slack = min(5.0, max(0.5, timeout * 0.1))
    dsh_timeout = max(0.1, timeout - timeout_slack)
    env = os.environ.copy()
    env["DSH_HOME"] = str(home)
    env.setdefault("DSH_TELEMETRY_MODE", "DISABLED")
    env.setdefault("DSH_PERMISSION_MODE", "danger-full-access")

    started = time.monotonic()
    (
        exit_code,
        acp_message,
        raw_stderr,
        timed_out,
        dsh_session_id,
        protocol_frames,
    ) = run_dsh_acp(
        [env.get("DSH_BIN", "dsh"), "--profile", "acp"],
        workspace,
        env,
        dsh_timeout,
        prompt,
        dsh_session_id,
    )
    final_message = redact_secrets(output_text(acp_message).strip())
    stderr = redact_secrets(output_text(raw_stderr))
    if exit_code != 0 and not stderr.strip():
        stderr = final_message
    if timed_out:
        stderr += f"\nDSH timed out after {dsh_timeout:g}s"

    duration_ms = int((time.monotonic() - started) * 1000)
    invocation_id = uuid.uuid4().hex[:12]
    stderr_path = home / f"stderr-{invocation_id}.log"
    protocol_path = home / f"acp-{invocation_id}.jsonl"
    stderr_path.write_text(stderr)
    protocol_path.write_text(
        "".join(
            json.dumps(redact_data(frame), ensure_ascii=False) + "\n"
            for frame in protocol_frames
        )
    )
    sanitize_run_artifacts(home)
    session_files = sorted(session_root.rglob("*.jsonl"))
    session_file = next(
        (path for path in session_files if path.parent.name == dsh_session_id),
        None,
    )
    artifact_files = [
        home / "cordis.patch.yml",
        home / "settings.yaml",
        stderr_path,
        protocol_path,
        *([session_file] if session_file is not None else []),
    ]
    generated_files = [
        str(path.relative_to(workspace)) for path in artifact_files if path.is_file()
    ]
    (
        parsed_session_id,
        final_message,
        run_transcript,
        input_tokens,
        output_tokens,
        turns,
    ) = read_session(session_file, messages, final_message)
    dsh_session_id = parsed_session_id or dsh_session_id
    continued_session_id = (
        encode_session_ref(run_id, dsh_session_id) if dsh_session_id else ""
    )

    result = {
        "engine": "deepseek-harness",
        "model": os.environ.get("DSH_MODEL", "qwen3.8-max"),
        "session_id": continued_session_id,
        "exit_code": exit_code,
        "duration_ms": duration_ms,
        "turns": turns,
        "input_tokens": input_tokens,
        "output_tokens": output_tokens,
        "final_message": final_message,
        "stderr": stderr if exit_code else "",
        "transcript": run_transcript,
        "artifacts": {
            "generated_files": generated_files,
            "logs": stderr,
        },
    }
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    # The wrapper completed its transport contract once SessionResult was
    # written. DSH failures stay non-zero in the structured result so skill-up
    # can report their redacted diagnostics instead of replacing them with an
    # empty command-level error.
    return 0


if __name__ == "__main__":
    sys.exit(main())
