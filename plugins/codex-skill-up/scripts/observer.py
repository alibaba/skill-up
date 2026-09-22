#!/usr/bin/env python3
"""Self-contained observation runtime for the Codex skill-up plugin."""

from __future__ import annotations

import argparse
import contextlib
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time
from typing import Any, Iterator


SCHEMA_VERSION = "v1alpha1"
PLUGIN_NAME = "codex-skill-up"
OBSERVATION_DATA_NAME = "skill-up-observer"
CAPTURE_CONTROL_SKILLS = {PLUGIN_NAME, OBSERVATION_DATA_NAME, "skill-upper"}
SKILL_NAME_RE = re.compile(r"^[a-z0-9][a-z0-9_-]{0,63}$")
OBSERVATION_ID_RE = re.compile(r"^obs_[a-f0-9]{24}$")
EXPLICIT_SKILL_RE = re.compile(r"(?:^|\s)\$([A-Za-z0-9][A-Za-z0-9_-]*)")
FEEDBACK_SENTIMENTS = {"", "positive", "negative", "mixed", "neutral"}

REDACTION_RULES = (
    ("bearer_token", re.compile(r"\bbearer\s+[a-z0-9._~+/=-]{12,}", re.IGNORECASE)),
    ("jwt", re.compile(r"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b")),
    ("provider_key", re.compile(r"\b(?:sk-[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9_]{12,}|AIza[A-Za-z0-9_-]{20,})\b")),
    ("aws_access_key", re.compile(r"\b(?:AKIA|ASIA)[A-Z0-9]{16}\b")),
    (
        "secret_assignment",
        re.compile(
            r"\b(?:[A-Za-z_][A-Za-z0-9_]*_)?(?:api[_-]?key|token|secret|password|passwd|pwd)"
            r"\s*[:=]\s*(?:\"(?:\\.|[^\"\\\r\n])*\"|'(?:\\.|[^'\\\r\n])*'|[^\s,;'\"`]+)",
            re.IGNORECASE,
        ),
    ),
)


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")


def data_dir() -> Path:
    configured = os.environ.get("SKILL_UP_OBSERVER_DATA")
    if configured:
        return Path(configured).expanduser().resolve()
    codex_home = Path(os.environ.get("CODEX_HOME", Path.home() / ".codex"))
    return codex_home.expanduser().resolve() / "plugin-data" / OBSERVATION_DATA_NAME


def ensure_private_dir(path: Path) -> None:
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    with contextlib.suppress(OSError):
        path.chmod(0o700)


def redact(value: Any) -> tuple[str, list[str]]:
    text = "" if value is None else str(value)
    categories: set[str] = set()
    for name, pattern in REDACTION_RULES:
        if pattern.search(text):
            text = pattern.sub("[REDACTED]", text)
            categories.add(name)
    return text, sorted(categories)


def merge_strings(*groups: list[str]) -> list[str]:
    return list(dict.fromkeys(item for group in groups for item in group))


def atomic_write_json(path: Path, value: dict[str, Any]) -> None:
    ensure_private_dir(path.parent)
    fd, temporary = tempfile.mkstemp(prefix=".observer-", suffix=".json", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(value, stream, ensure_ascii=False, indent=2)
            stream.write("\n")
        os.replace(temporary, path)
    finally:
        with contextlib.suppress(FileNotFoundError):
            os.unlink(temporary)


def atomic_write_text(path: Path, value: str) -> None:
    mode = path.stat().st_mode & 0o777
    fd, temporary = tempfile.mkstemp(prefix=".observer-", suffix=path.suffix, dir=path.parent)
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            stream.write(value)
        os.replace(temporary, path)
    finally:
        with contextlib.suppress(FileNotFoundError):
            os.unlink(temporary)


@contextlib.contextmanager
def file_lock(path: Path, timeout: float = 2.0) -> Iterator[None]:
    ensure_private_dir(path.parent)
    deadline = time.monotonic() + timeout
    while True:
        try:
            path.mkdir(mode=0o700)
            break
        except FileExistsError:
            if time.monotonic() >= deadline:
                raise TimeoutError(f"timed out acquiring {path}; remove it if no observer process is active")
            time.sleep(0.01)
    try:
        yield
    finally:
        with contextlib.suppress(FileNotFoundError):
            path.rmdir()


def draft_path(root: Path, session_id: str, turn_id: str) -> Path:
    digest = hashlib.sha256(f"{session_id}\0{turn_id}".encode()).hexdigest()[:32]
    return root / ".drafts" / f"{digest}.json"


def load_json(path: Path) -> dict[str, Any] | None:
    try:
        with path.open(encoding="utf-8") as stream:
            value = json.load(stream)
    except FileNotFoundError:
        return None
    if not isinstance(value, dict):
        raise ValueError(f"{path} must contain a JSON object")
    return value


def tool_arguments(payload: dict[str, Any]) -> dict[str, Any]:
    value = payload.get("tool_input") or {}
    if isinstance(value, str):
        value = json.loads(value)
    if not isinstance(value, dict):
        raise ValueError("tool_input must be an object")
    return value


def short_tool_name(name: str) -> str:
    return name.rsplit("__", 1)[-1]


def apply_marker(draft: dict[str, Any], tool_name: str, arguments: dict[str, Any]) -> None:
    if tool_name == "mark_skill_invocation":
        name = str(arguments.get("skill_name", "")).strip().lower()
        if not SKILL_NAME_RE.fullmatch(name):
            raise ValueError("skill_name must use 1-64 lowercase letters, digits, underscores, or hyphens")
        version, found = redact(str(arguments.get("skill_version", "")).strip())
        draft["skill"] = {"name": name}
        if version:
            draft["skill"]["version"] = version
        draft["attribution"] = {
            "method": "instrumented",
            "confidence": 1,
            "evidence": ["Skill invocation marked through the observer MCP tool"],
        }
        draft["redactions"] = merge_strings(draft.get("redactions", []), found)
        return

    if tool_name == "attach_skill_evidence":
        kind, r0 = redact(str(arguments.get("kind", "")).strip())
        if not kind:
            raise ValueError("kind is required")
        reference, r1 = redact(arguments.get("reference", ""))
        summary, r2 = redact(arguments.get("summary", ""))
        evidence = {"kind": kind}
        if reference:
            evidence["ref"] = reference
        if summary:
            evidence["summary"] = summary
        draft.setdefault("evidence", []).append(evidence)
        draft["redactions"] = merge_strings(draft.get("redactions", []), r0, r1, r2)
        return

    if tool_name == "record_skill_feedback":
        sentiment = str(arguments.get("sentiment", ""))
        if sentiment not in FEEDBACK_SENTIMENTS:
            raise ValueError("sentiment must be positive, negative, mixed, neutral, or empty")
        comment, found = redact(arguments.get("comment", ""))
        feedback: dict[str, str] = {}
        if sentiment:
            feedback["sentiment"] = sentiment
        if comment:
            feedback["comment"] = comment
        draft["feedback"] = feedback
        draft["redactions"] = merge_strings(draft.get("redactions", []), found)
        return

    raise ValueError(f"unsupported observer tool {tool_name!r}")


def assign_observation_id(observation: dict[str, Any]) -> str:
    material = "\0".join(
        (
            observation["host"]["name"],
            observation["skill"]["name"],
            observation["skill"].get("version", ""),
            observation["attribution"]["method"],
            observation["input"]["text"],
            observation["outcome"]["status"],
            observation["outcome"].get("final_message", ""),
            observation["correlation"]["session_id"],
            observation["correlation"].get("turn_id", ""),
        )
    )
    return "obs_" + hashlib.sha256(material.encode()).hexdigest()[:24]


def validate_observation(observation: dict[str, Any]) -> None:
    if observation.get("schema_version") != SCHEMA_VERSION:
        raise ValueError("schema_version must be v1alpha1")
    if not OBSERVATION_ID_RE.fullmatch(str(observation.get("id", ""))):
        raise ValueError("invalid observation id")
    if observation.get("host", {}).get("name") not in {"codex", "dsh"}:
        raise ValueError("host.name must be codex or dsh")
    if not SKILL_NAME_RE.fullmatch(str(observation.get("skill", {}).get("name", ""))):
        raise ValueError("invalid skill.name")
    if observation.get("attribution", {}).get("method") not in {"explicit", "instrumented"}:
        raise ValueError("attribution.method must be explicit or instrumented")
    if not str(observation.get("input", {}).get("text", "")):
        raise ValueError("input.text is required for replayable candidate cases")
    if observation.get("outcome", {}).get("status") not in {"completed", "interrupted"}:
        raise ValueError("outcome.status must be completed or interrupted")
    if not str(observation.get("correlation", {}).get("session_id", "")):
        raise ValueError("correlation.session_id is required")
    if observation.get("privacy", {}).get("storage") != "local":
        raise ValueError("privacy.storage must be local")
    if observation.get("review", {}).get("status") not in {"candidate", "approved", "rejected"}:
        raise ValueError("invalid review.status")
    feedback = observation.get("feedback")
    if feedback and feedback.get("sentiment", "") not in FEEDBACK_SENTIMENTS:
        raise ValueError("invalid feedback.sentiment")


def save_observation(root: Path, observation: dict[str, Any]) -> bool:
    validate_observation(observation)
    ensure_private_dir(root)
    path = root / f"{observation['id']}.json"
    with file_lock(Path(str(path) + ".lock")):
        if path.exists():
            return False
        atomic_write_json(path, observation)
        return True


def handle_hook(payload: dict[str, Any], root: Path | None = None) -> dict[str, Any] | None:
    root = root or data_dir()
    session_id = str(payload.get("session_id", ""))
    turn_id = str(payload.get("turn_id", ""))
    event = str(payload.get("hook_event_name", ""))
    if not session_id:
        raise ValueError("hook payload session_id is required")
    path = draft_path(root, session_id, turn_id)

    with file_lock(Path(str(path) + ".lock")):
        if event == "UserPromptSubmit":
            prompt, redactions = redact(payload.get("prompt", ""))
            draft: dict[str, Any] = {
                "session_id": session_id,
                "turn_id": turn_id,
                "model": str(payload.get("model", "")),
                "prompt": prompt,
                "skill": {},
                "attribution": {},
                "redactions": redactions,
                "observed_at": utc_now(),
            }
            for match in EXPLICIT_SKILL_RE.finditer(prompt):
                name = match.group(1).lower()
                if name not in CAPTURE_CONTROL_SKILLS and SKILL_NAME_RE.fullmatch(name):
                    draft["skill"] = {"name": name}
                    draft["attribution"] = {
                        "method": "explicit",
                        "confidence": 1,
                        "evidence": [f"user prompt explicitly referenced ${name}"],
                    }
                    break
            atomic_write_json(path, draft)
            return None

        if event == "PostToolUse":
            draft = load_json(path) or {
                "session_id": session_id,
                "turn_id": turn_id,
                "model": str(payload.get("model", "")),
                "prompt": "",
                "skill": {},
                "attribution": {},
                "redactions": [],
                "observed_at": utc_now(),
            }
            apply_marker(draft, short_tool_name(str(payload.get("tool_name", ""))), tool_arguments(payload))
            atomic_write_json(path, draft)
            return None

        if event not in {"Stop", "Interrupt"}:
            raise ValueError(f"unsupported hook event {event!r}")

        draft = load_json(path)
        with contextlib.suppress(FileNotFoundError):
            path.unlink()
        if not draft or not draft.get("skill", {}).get("name"):
            return None
        if draft.get("attribution", {}).get("method") not in {"explicit", "instrumented"}:
            return None
        if not draft.get("prompt"):
            return None

        message, redactions = redact(payload.get("last_assistant_message", ""))
        observation: dict[str, Any] = {
            "schema_version": SCHEMA_VERSION,
            "id": "",
            "host": {"name": "codex", "adapter_version": "v1alpha1"},
            "skill": draft["skill"],
            "attribution": draft["attribution"],
            "input": {"text": draft["prompt"]},
            "outcome": {"status": "completed" if event == "Stop" else "interrupted"},
            "correlation": {"session_id": session_id},
            "timing": {"observed_at": draft["observed_at"], "completed_at": utc_now()},
            "privacy": {
                "storage": "local",
                "consent": "codex_plugin_enabled_and_hooks_trusted",
                "redactions": merge_strings(draft.get("redactions", []), redactions),
            },
            "review": {"status": "candidate"},
        }
        if draft.get("model"):
            observation["host"]["version"] = draft["model"]
        if message:
            observation["outcome"]["final_message"] = message
        if turn_id:
            observation["correlation"]["turn_id"] = turn_id
        if draft.get("evidence"):
            observation["evidence"] = draft["evidence"]
        if draft.get("feedback"):
            observation["feedback"] = draft["feedback"]
        observation["id"] = assign_observation_id(observation)
        save_observation(root, observation)
        return observation


def safe_observation_path(root: Path, observation_id: str) -> Path:
    if not OBSERVATION_ID_RE.fullmatch(observation_id):
        raise ValueError("invalid observation id")
    return root / f"{observation_id}.json"


def get_observation(root: Path, observation_id: str) -> dict[str, Any]:
    observation = load_json(safe_observation_path(root, observation_id))
    if observation is None:
        raise FileNotFoundError(f"observation {observation_id} does not exist")
    validate_observation(observation)
    return observation


def list_observations(root: Path) -> list[dict[str, Any]]:
    if not root.exists():
        return []
    observations = []
    for path in root.glob("obs_*.json"):
        value = load_json(path)
        if value is not None:
            validate_observation(value)
            observations.append(value)
    observations.sort(key=lambda item: item["timing"]["observed_at"], reverse=True)
    return observations


def set_review(root: Path, observation_id: str, status: str) -> dict[str, Any]:
    if status not in {"approved", "rejected"}:
        raise ValueError("status must be approved or rejected")
    path = safe_observation_path(root, observation_id)
    with file_lock(Path(str(path) + ".lock")):
        observation = get_observation(root, observation_id)
        observation["review"] = {"status": status, "reviewed_at": utc_now()}
        atomic_write_json(path, observation)
    return observation


def yaml_string(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


def candidate_case(observation: dict[str, Any]) -> tuple[str, str]:
    validate_observation(observation)
    skill_name = observation["skill"]["name"]
    base = re.sub(r"[^a-z0-9-]+", "-", skill_name.lower().replace("_", "-")).strip("-") or "skill"
    case_id = f"observed-{base}-{observation['id'].removeprefix('obs_')[:8]}"
    description = (
        f"Candidate regression case from local observation {observation['id']}. "
        "Review the prompt and add concrete expectations before relying on it as a release gate."
    )
    feedback = observation.get("feedback", {}).get("comment", "")
    if feedback:
        description += f" User feedback: {feedback}"
    content = "\n".join(
        (
            f"id: {yaml_string(case_id)}",
            f"title: {yaml_string(f'Observed {skill_name} interaction')}",
            f"description: {yaml_string(description)}",
            'tag: "functional_test"',
            "input:",
            f"  prompt: {yaml_string(observation['input']['text'])}",
            "",
        )
    )
    return content, case_id


def append_case_reference(eval_text: str, relative_path: str) -> str:
    lines = eval_text.splitlines(keepends=True)
    cases_index = next((i for i, line in enumerate(lines) if re.match(r"^cases:\s*(?:#.*)?(?:\r?\n)?$", line)), None)
    if cases_index is None:
        raise ValueError("eval.yaml must contain a top-level cases mapping")
    end = len(lines)
    for i in range(cases_index + 1, len(lines)):
        stripped = lines[i].strip()
        if stripped and not stripped.startswith("#") and len(lines[i]) - len(lines[i].lstrip()) == 0:
            end = i
            break
    files_index = None
    files_indent = 0
    inline_value = ""
    for i in range(cases_index + 1, end):
        match = re.match(r"^(\s+)files:\s*(.*?)\s*(?:#.*)?(?:\r?\n)?$", lines[i])
        if match:
            files_index = i
            files_indent = len(match.group(1))
            inline_value = match.group(2)
            break
    if files_index is None:
        raise ValueError("eval.yaml cases must contain files")
    reference_line = " " * (files_indent + 2) + f"- {relative_path}\n"
    if inline_value == "[]":
        newline = "\r\n" if lines[files_index].endswith("\r\n") else "\n"
        lines[files_index] = " " * files_indent + "files:" + newline
        lines.insert(files_index + 1, reference_line.replace("\n", newline))
        return "".join(lines)
    if inline_value:
        raise ValueError("flow-style non-empty cases.files is not supported; use a block sequence")
    insert_at = end
    for i in range(files_index + 1, end):
        stripped = lines[i].strip()
        indent = len(lines[i]) - len(lines[i].lstrip())
        if stripped and not stripped.startswith("#") and indent <= files_indent:
            insert_at = i
            break
        if stripped.startswith("-") and stripped[1:].strip().strip("'\"") == relative_path:
            raise FileExistsError(f"eval.yaml already references {relative_path}")
    lines.insert(insert_at, reference_line)
    return "".join(lines)


def require_within(root: Path, path: Path, label: str) -> Path:
    resolved = path.resolve(strict=True)
    try:
        resolved.relative_to(root)
    except ValueError as error:
        raise ValueError(f"{label} must resolve inside the Skill root") from error
    return resolved


def write_candidate_case(root: Path, observation: dict[str, Any], skill_root: str) -> dict[str, Any]:
    if observation["review"]["status"] != "approved":
        raise PermissionError("observation must be approved before writing a case")
    skill = Path(skill_root).expanduser().resolve()
    if not skill.is_dir():
        raise FileNotFoundError("skill root must be a directory")
    if not (skill / "SKILL.md").is_file():
        raise FileNotFoundError("skill root must contain SKILL.md")
    require_within(skill, skill / "SKILL.md", "SKILL.md")
    evals_dir = require_within(skill, skill / "evals", "evals directory")
    if not (evals_dir / "eval.yaml").is_file():
        raise FileNotFoundError("skill root must contain evals/eval.yaml")
    eval_path = require_within(skill, evals_dir / "eval.yaml", "eval.yaml")

    with file_lock(evals_dir / ".skill-up-observer.lock", timeout=30):
        case_text, case_id = candidate_case(observation)
        relative_path = f"evals/cases/{case_id}.yaml"
        cases_dir = evals_dir / "cases"
        cases_dir.mkdir(mode=0o755, parents=True, exist_ok=True)
        cases_dir = require_within(skill, cases_dir, "cases directory")
        case_path = cases_dir / f"{case_id}.yaml"
        eval_original = eval_path.read_text(encoding="utf-8")
        eval_updated = append_case_reference(eval_original, relative_path)
        try:
            fd = os.open(case_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        except FileExistsError as error:
            raise FileExistsError(f"candidate case already exists: {case_path}") from error
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as stream:
                stream.write(case_text)
            atomic_write_text(eval_path, eval_updated)
            validator = shutil.which("skill-up")
            validation = "skipped (skill-up is not installed)"
            if validator:
                result = subprocess.run(
                    [validator, "validate", str(eval_path)],
                    cwd=skill,
                    check=False,
                    capture_output=True,
                    text=True,
                    timeout=60,
                )
                if result.returncode != 0:
                    raise ValueError(f"skill-up validate failed: {(result.stderr or result.stdout).strip()}")
                validation = "passed"
        except Exception:
            atomic_write_text(eval_path, eval_original)
            with contextlib.suppress(FileNotFoundError):
                case_path.unlink()
            raise
    return {"case_path": str(case_path), "eval_path": str(eval_path), "validation": validation}


TOOLS = [
    {
        "name": "mark_skill_invocation",
        "description": "Declare the Skill responsible for the current interaction.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "properties": {"skill_name": {"type": "string"}, "skill_version": {"type": "string"}},
            "required": ["skill_name"],
        },
    },
    {
        "name": "attach_skill_evidence",
        "description": "Attach a local reference or summary to the current Skill interaction.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "properties": {
                "kind": {"type": "string"},
                "reference": {"type": "string"},
                "summary": {"type": "string"},
            },
            "required": ["kind"],
        },
    },
    {
        "name": "record_skill_feedback",
        "description": "Record explicit user feedback for the current Skill interaction.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "properties": {
                "sentiment": {"type": "string", "enum": ["positive", "negative", "mixed", "neutral"]},
                "comment": {"type": "string"},
            },
        },
    },
    {
        "name": "list_skill_observations",
        "description": "List locally stored Skill observations.",
        "inputSchema": {"type": "object", "additionalProperties": False},
    },
    {
        "name": "get_skill_observation",
        "description": "Read one local Skill observation.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "properties": {"observation_id": {"type": "string"}},
            "required": ["observation_id"],
        },
    },
    {
        "name": "review_skill_observation",
        "description": "Approve or reject one observation after explicit user review.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "properties": {
                "observation_id": {"type": "string"},
                "status": {"type": "string", "enum": ["approved", "rejected"]},
            },
            "required": ["observation_id", "status"],
        },
    },
    {
        "name": "preview_observation_case",
        "description": "Preview a candidate regression case without writing files.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "properties": {"observation_id": {"type": "string"}},
            "required": ["observation_id"],
        },
    },
    {
        "name": "write_observation_case",
        "description": "Write an approved observation as a candidate case and update eval.yaml.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "properties": {"observation_id": {"type": "string"}, "skill_root": {"type": "string"}},
            "required": ["observation_id", "skill_root"],
        },
    },
]


def call_tool(name: str, arguments: dict[str, Any], root: Path) -> Any:
    if name in {"mark_skill_invocation", "attach_skill_evidence", "record_skill_feedback"}:
        test_draft: dict[str, Any] = {"redactions": []}
        apply_marker(test_draft, name, arguments)
        return {"accepted": True, "tool": name, "note": "The matching PostToolUse hook persists this marker."}
    if name == "list_skill_observations":
        return {
            "observations": [
                {
                    "id": item["id"],
                    "skill": item["skill"]["name"],
                    "review": item["review"]["status"],
                    "outcome": item["outcome"]["status"],
                    "observed_at": item["timing"]["observed_at"],
                }
                for item in list_observations(root)
            ]
        }
    observation_id = str(arguments.get("observation_id", ""))
    if name == "get_skill_observation":
        return get_observation(root, observation_id)
    if name == "review_skill_observation":
        return set_review(root, observation_id, str(arguments.get("status", "")))
    if name == "preview_observation_case":
        case_text, case_id = candidate_case(get_observation(root, observation_id))
        return {"case_id": case_id, "yaml": case_text}
    if name == "write_observation_case":
        return write_candidate_case(root, get_observation(root, observation_id), str(arguments.get("skill_root", "")))
    raise ValueError(f"unknown observer tool {name!r}")


def rpc_result(request_id: Any, result: Any) -> dict[str, Any]:
    return {"jsonrpc": "2.0", "id": request_id, "result": result}


def handle_rpc(request: dict[str, Any], root: Path) -> dict[str, Any] | None:
    if "id" not in request:
        return None
    request_id = request["id"]
    method = request.get("method")
    if method == "initialize":
        requested = request.get("params", {}).get("protocolVersion") or "2025-06-18"
        return rpc_result(
            request_id,
            {
                "protocolVersion": requested,
                "capabilities": {"tools": {"listChanged": False}},
                "serverInfo": {"name": PLUGIN_NAME, "version": "0.1.0"},
            },
        )
    if method == "ping":
        return rpc_result(request_id, {})
    if method == "tools/list":
        return rpc_result(request_id, {"tools": TOOLS})
    if method == "tools/call":
        params = request.get("params") or {}
        try:
            value = call_tool(str(params.get("name", "")), params.get("arguments") or {}, root)
            text = json.dumps(value, ensure_ascii=False, indent=2)
            return rpc_result(
                request_id,
                {"content": [{"type": "text", "text": text}], "structuredContent": value},
            )
        except Exception as error:  # MCP tool errors are returned as tool results.
            return rpc_result(
                request_id,
                {"content": [{"type": "text", "text": str(error)}], "isError": True},
            )
    return {"jsonrpc": "2.0", "id": request_id, "error": {"code": -32601, "message": "method not found"}}


def serve_mcp(root: Path | None = None) -> None:
    root = root or data_dir()
    for line in sys.stdin:
        try:
            request = json.loads(line)
            response = handle_rpc(request, root)
        except json.JSONDecodeError:
            response = {"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": "parse error"}}
        if response is not None:
            print(json.dumps(response, ensure_ascii=False), flush=True)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    subparsers.add_parser("ingest-hook", help=argparse.SUPPRESS)
    subparsers.add_parser("mcp", help=argparse.SUPPRESS)
    args = parser.parse_args()
    try:
        if args.command == "mcp":
            serve_mcp()
            return 0
        payload = json.load(sys.stdin)
        handle_hook(payload)
        print("{}")
        return 0
    except Exception as error:
        print(f"codex-skill-up: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
