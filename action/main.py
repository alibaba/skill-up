#!/usr/bin/python3
# -*- coding: UTF-8 -*-
"""skill-up GitHub Action core logic (public github.com / cloud runner).

Adapted from the internal Aone CI component. Differences from the internal
build:

  * No internal-config fetch (skill-up-ali / code.alibaba-inc.com): unreachable
    from github.com hosted runners, so it is removed entirely.
  * No ghproxy: raw.githubusercontent.com / GitHub Releases are directly
    reachable on github.com, so skill-up installs without a proxy.
  * Directory conventions follow GitHub: the eval target is resolved against
    ``$GITHUB_WORKSPACE`` (the caller's checked-out repo); reports are written
    to ``$GITHUB_WORKSPACE/skill-up-workspace``.
  * Only public model endpoints are routable: the ``provider`` table keeps the
    public ``dashscope`` entry; internal gateways (idealab/routify) are dropped.
    When nothing is given, the agent CLI falls back to its own public default
    (api.anthropic.com / api.openai.com).
  * Results are also exported to ``$GITHUB_OUTPUT`` for downstream steps.

The agent engines (claude / codex / qodercli / qwen) and skill-up are baked into the
Docker image (see Dockerfile), so the runtime install paths are skipped when the
binaries are already present.
"""

import argparse
import hashlib
import os
import shlex
import shutil
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request


# provider -> per-protocol (openai / anthropic) base_url.
# Only public-reachable providers from github.com hosted runners are kept.
# Internal gateways (idealab / routify) are intentionally dropped — selecting
# them on a cloud runner would silently fail with a connection error.
PROVIDERS = {
    "dashscope": {
        "openai": "https://dashscope.aliyuncs.com/compatible-mode/v1",
        "anthropic": "https://dashscope.aliyuncs.com/apps/anthropic",
    },
}

# engine -> model API protocol. qodercli does not use base-url.
ENGINE_PROTOCOL = {
    "claude_code": "anthropic",
    "codex": "openai",
    "qodercli": None,
    "qwen_code": "openai",
}

# engine -> built-in default base-url. Empty on the public build: when no
# base-url / provider is supplied, let the agent CLI use its own public default
# endpoint (api.anthropic.com / api.openai.com) rather than an internal gateway.
ENGINE_DEFAULT_BASE_URL = {}

SKILL_UP_INSTALL_URL = (
    "https://raw.githubusercontent.com/alibaba/skill-up/"
    "5ac7ce0467a164d07aacbbf7052bcffda68a446b/install.sh"
)
SKILL_UP_INSTALL_SHA256 = (
    "2b7fbea303dc8b6feb09db6f9cc7307fb9263ff6cc4b0c68cdfe9c2897d109ef"
)


def _provider_env_prefix(provider):
    """Provider name -> env var prefix (uppercase, ``-`` -> ``_``)."""
    return provider.upper().replace("-", "_")


def resolve_base_url(engine, provider, base_url):
    """Resolve base-url by priority: explicit base-url > provider table > default.

    qodercli has no base-url (always ""). engine="" passes the explicit base-url
    through verbatim (empty stays empty; the caller's unified_base_url_env then
    fills both protocol slots from the provider table). Unknown engine raises
    ValueError (fail-fast).
    """
    if engine == "":
        return base_url or ""
    if engine not in ENGINE_PROTOCOL:
        raise ValueError(
            "unknown engine: " + repr(engine) + " (expected one of "
            + ", ".join(sorted(ENGINE_PROTOCOL)) + ")")
    if base_url:
        return base_url
    protocol = ENGINE_PROTOCOL[engine]
    if protocol is None:
        return ""
    if provider:
        mapping = PROVIDERS.get(provider, {})
        if protocol in mapping:
            return mapping[protocol]
    return ENGINE_DEFAULT_BASE_URL.get(engine, "")


def engine_env(engine, api_key, base_url):
    """Route the unified api-key / base-url into engine-scoped env vars.

    These are read by the agent CLI itself (claude / codex / qodercli / qwen). engine=""
    returns {} — the caller takes the unified_base_url_env + ``skill-up --api-key``
    pass-through path instead.
    """
    env = {}
    if engine == "":
        return env
    if engine == "claude_code":
        if api_key:
            env["ANTHROPIC_API_KEY"] = api_key
            env["ANTHROPIC_AUTH_TOKEN"] = api_key
        if base_url:
            env["ANTHROPIC_BASE_URL"] = base_url
    elif engine in ("codex", "qwen_code"):
        # Both speak the OpenAI wire protocol and read OPENAI_API_KEY /
        # OPENAI_BASE_URL (qwen_code is a Gemini CLI fork talking to any
        # OpenAI-compatible endpoint; see internal/agent/qwen_code.go).
        if api_key:
            env["OPENAI_API_KEY"] = api_key
        if base_url:
            env["OPENAI_BASE_URL"] = base_url
    elif engine == "qodercli":
        if api_key:
            env["QODER_PERSONAL_ACCESS_TOKEN"] = api_key
    return env


def engine_model_env(engine, model):
    """Pin the engine-scoped MODEL env when an explicit ``model`` override is set.

    ``skill-up run --model`` sets the model for the primary case engine, but an
    ``agent_judge`` is a *separate* claude_code / codex invocation that reads the
    engine-scoped ``ANTHROPIC_MODEL`` / ``OPENAI_MODEL`` env instead. Without
    pinning that too, judges fall back to the engine's built-in default model
    (e.g. claude-sonnet-*), which a custom endpoint (dashscope, a gateway, ...)
    may not support — surfacing as a 400 "model not found" on the judge call
    while the case agent itself ran fine. Returns {} when model is empty or the
    engine has no model-env convention (qodercli).
    """
    if not model:
        return {}
    if engine == "claude_code":
        return {"ANTHROPIC_MODEL": model}
    if engine in ("codex", "qwen_code"):
        return {"OPENAI_MODEL": model}
    return {}


def unified_base_url_env(provider, base_url):
    """engine="" : fill only protocol-scoped OPENAI_BASE_URL / ANTHROPIC_BASE_URL.

    Explicit base-url sets both slots to the same URL; provider-only fills each
    from the PROVIDERS table. Neither set -> {} (agent CLI uses its own default).
    ``<PROVIDER>_BASE_URL`` is intentionally NOT set — that slot is protocol-
    orthogonal and a single value cannot carry both openai and anthropic
    endpoints; with engine unknown the component cannot pick which protocol it
    serves, so filling it risks wrong-protocol routing.
    """
    env = {}
    if base_url:
        env["OPENAI_BASE_URL"] = base_url
        env["ANTHROPIC_BASE_URL"] = base_url
        return env
    if provider:
        mapping = PROVIDERS.get(provider, {})
        if "openai" in mapping:
            env["OPENAI_BASE_URL"] = mapping["openai"]
        if "anthropic" in mapping:
            env["ANTHROPIC_BASE_URL"] = mapping["anthropic"]
    return env


def provider_env(provider, api_key, base_url):
    """skill-up resolver reads ``<PROVIDER>_BASE_URL`` / ``<PROVIDER>_API_KEY``
    to populate params.BaseURL / params.APIKey (provider-scoped scope).

    Critical for codex running ``--model <provider>/<name>``: skill-up only emits
    the ``-c model_providers.<provider>.base_url=...`` config when it sees a
    non-empty BaseURL, otherwise it falls back to codex's built-in openai
    provider and the base-url is lost. Returns {} when provider is empty.
    """
    if not provider:
        return {}
    prefix = _provider_env_prefix(provider)
    env = {}
    if api_key:
        env[prefix + "_API_KEY"] = api_key
    if base_url:
        env[prefix + "_BASE_URL"] = base_url
    return env


def parse_skill_up_version(stdout):
    """Extract the (MAJOR, MINOR, PATCH) tuple from ``skill-up --version``.

    Output looks like ``skill-up version 0.2.3``. Returns None when unparseable
    (treated as "unknown version, conservatively skip new flags").
    """
    if not stdout:
        return None
    line = stdout.strip().splitlines()[0] if stdout.strip() else ""
    if not line:
        return None
    raw = line.split()[-1].lstrip("v")
    parts = raw.split(".")[:3]
    try:
        nums = [int(p) for p in parts]
    except ValueError:
        return None
    while len(nums) < 3:
        nums.append(0)
    return tuple(nums)


def version_supports_bypass_sandbox(version_tuple):
    """``engine.kwargs.bypass_sandbox`` was introduced by skill-up 0.2.3.

    Older CLIs reject ``--engine-kwarg`` as an unknown flag, so treat unknown
    version as "do not pass".
    """
    if not version_tuple:
        return False
    return version_tuple >= (0, 2, 3)


def get_skill_up_version(env=None):
    """Run ``skill-up --version`` and return the parsed tuple (None if unknown)."""
    run_env = os.environ if not env else {**os.environ, **env}
    try:
        result = subprocess.run(
            ["skill-up", "--version"],
            env=run_env,
            capture_output=True,
            text=True,
            check=True,
        )
    except (subprocess.CalledProcessError, FileNotFoundError):
        return None
    return parse_skill_up_version(result.stdout)


def validate_parallelism(value):
    """Validate parallelism: empty is fine (omit flag); else 1-256 integer."""
    if value is None or value == "":
        return ""
    if not value.isdigit():
        raise ValueError("parallelism must be a positive integer between 1 and 256")
    n = int(value)
    if n < 1 or n > 256:
        raise ValueError("parallelism must be a positive integer between 1 and 256")
    return str(n)


def compose_model_ref(provider, model, engine):
    """Fold (provider, model) into the ``--model`` value skill-up accepts.

    Only prefix ``provider/`` for codex — it needs that to make skill-up emit the
    ``-c model_providers`` config, otherwise it falls back to the built-in openai
    provider and 401s. Other engines get the bare name.
    """
    if not model:
        return ""
    if engine == "codex" and provider and "/" not in model:
        return provider + "/" + model
    return model


def mask_argv_for_log(argv):
    """Mask the value after ``--api-key`` to ``***`` for log output."""
    masked = list(argv)
    for i, arg in enumerate(masked):
        if arg == "--api-key" and i + 1 < len(masked):
            masked[i + 1] = "***"
    return masked


def build_skill_up_argv(skill_up_command, engine, model, provider, parallelism,
                        output_dir, skill_target, api_key="",
                        bypass_codex_sandbox=False):
    """Assemble the skill-up argv. skill-up-command must start with ``skill-up``.

    engine="" : do not append --engine (let eval.yaml declare it) and pass the
    credential via ``--api-key`` directly. bypass_codex_sandbox=True appends
    ``--engine-kwarg bypass_sandbox=true`` (skill-up >= 0.2.3) so codex skips its
    Landlock sandbox on restricted runner images.
    """
    parts = shlex.split(skill_up_command)
    if not parts or parts[0] != "skill-up":
        raise ValueError("skill-up-command must start with 'skill-up'")
    argv = list(parts)
    if engine:
        argv += ["--engine", engine]
    elif api_key:
        argv += ["--api-key", api_key]
    model_ref = compose_model_ref(provider, model, engine)
    if model_ref:
        argv += ["--model", model_ref]
    norm = validate_parallelism(parallelism)
    if norm:
        argv += ["--parallelism", norm]
    if bypass_codex_sandbox:
        argv += ["--engine-kwarg", "bypass_sandbox=true"]
    argv += ["--output-dir", output_dir,
             "--format", "json", "--format", "junit", "--format", "html",
             skill_target]
    return argv


def parse_inputs(argv=None):
    parser = argparse.ArgumentParser(description="skill-up GitHub Action")
    parser.add_argument("--engine", default="")
    parser.add_argument("--model", default="")
    parser.add_argument("--provider", default="")
    parser.add_argument("--api-key", default="")
    parser.add_argument("--base-url", default="")
    parser.add_argument("--open-sandbox-api-key", default="")
    parser.add_argument("--skill-target", default=".")
    parser.add_argument("--skill-up-version", default="0.7.0")
    parser.add_argument("--skill-up-command", default="skill-up run")
    parser.add_argument("--parallelism", default="")
    parser.add_argument("--agent-install-command", default="")
    return parser.parse_args(argv)


def _run(script, env=None, cwd=None, check=True):
    """Execute a bash snippet."""
    run_env = os.environ if not env else {**os.environ, **env}
    result = subprocess.run(["bash", "-c", script], env=run_env, cwd=cwd)
    if check and result.returncode != 0:
        sys.exit(result.returncode)
    return result.returncode


def install_skill_up(bin_dir, version):
    """Install skill-up from the official install.sh (GitHub Releases).

    No proxy: raw.githubusercontent.com / GitHub Releases are directly reachable
    on github.com hosted runners.
    """
    os.makedirs(bin_dir, exist_ok=True)
    install_env = {"INSTALL_DIR": bin_dir}
    if version and version != "latest":
        install_env["SKILL_UP_VERSION"] = version

    try:
        with urllib.request.urlopen(SKILL_UP_INSTALL_URL, timeout=120) as response:
            installer = response.read()
    except (urllib.error.URLError, TimeoutError, OSError) as exc:
        print(
            f"error: failed to download skill-up installer from "
            f"{SKILL_UP_INSTALL_URL}: {exc}",
            file=sys.stderr,
        )
        sys.exit(1)
    actual_sha256 = hashlib.sha256(installer).hexdigest()
    if actual_sha256 != SKILL_UP_INSTALL_SHA256:
        print(
            "error: skill-up installer checksum mismatch "
            f"(expected {SKILL_UP_INSTALL_SHA256}, got {actual_sha256})",
            file=sys.stderr,
        )
        sys.exit(1)

    installer_path = ""
    try:
        with tempfile.NamedTemporaryFile(delete=False) as installer_file:
            installer_file.write(installer)
            installer_path = installer_file.name
        _run("bash " + shlex.quote(installer_path), env=install_env)
    finally:
        if installer_path:
            os.unlink(installer_path)


def _set_output(name, value):
    """Append name=value to $GITHUB_OUTPUT when running inside Actions."""
    out = os.environ.get("GITHUB_OUTPUT")
    if not out:
        return
    try:
        with open(out, "a") as f:
            f.write(f"{name}={value}\n")
    except OSError:
        pass


def main():
    inputs = parse_inputs()

    # The caller's repo is checked out into $GITHUB_WORKSPACE; for a Docker
    # container action that path is the working dir (/github/workspace).
    workspace = os.environ.get("GITHUB_WORKSPACE", os.getcwd())
    target_dir = workspace
    # Reports go to a sibling workspace dir (skill-up's <skill>-workspace
    # convention) so they don't pollute the evaluated repo's tree.
    output_dir = os.path.join(workspace, "skill-up-workspace")
    tool_bin = os.path.join(os.path.expanduser("~"), ".skill-up-tools", "bin")

    try:
        base_url = resolve_base_url(inputs.engine, inputs.provider, inputs.base_url)
    except ValueError as exc:
        print(f"error: {exc}", file=sys.stderr)
        sys.exit(2)
    print(f"engine={inputs.engine or '<eval.yaml>'} "
          f"provider={inputs.provider or '<none>'} "
          f"base-url={base_url or '<agent default>'}")

    # 1. Install skill-up (skipped when the image already ships it).
    if shutil.which("skill-up"):
        print(f"skill-up already available at {shutil.which('skill-up')}, skipping install.")
        if inputs.skill_up_version not in ("latest", ""):
            print(f"Note: --skill-up-version={inputs.skill_up_version} ignored "
                  "(using pre-installed version).")
        path_env = {}
    else:
        install_skill_up(tool_bin, inputs.skill_up_version)
        path_env = {"PATH": tool_bin + os.pathsep + os.environ.get("PATH", "")}
    _run("skill-up --version", env=path_env)
    skill_up_version = get_skill_up_version(env=path_env)
    bypass_codex_sandbox = version_supports_bypass_sandbox(skill_up_version)
    if bypass_codex_sandbox:
        print("skill-up >= 0.2.3 detected; will pass --engine-kwarg "
              "bypass_sandbox=true to work around Landlock-restricted images.")

    # 2. Optional custom install command (for images missing a target CLI).
    if inputs.agent_install_command:
        print("Running custom install command from action input.")
        _run(inputs.agent_install_command, env=path_env)

    # 3. Engine availability preflight (fail-fast).
    engine_check = {
        "codex": "command -v codex >/dev/null && codex --version",
        "claude_code": "command -v claude >/dev/null",
        "qodercli": "command -v qodercli >/dev/null",
    }.get(inputs.engine)
    if engine_check:
        _run(engine_check, env=path_env)

    # 4. Assemble and run skill-up.
    os.makedirs(output_dir, exist_ok=True)
    try:
        argv = build_skill_up_argv(inputs.skill_up_command, inputs.engine,
                                   inputs.model, inputs.provider,
                                   inputs.parallelism,
                                   output_dir, inputs.skill_target,
                                   api_key=inputs.api_key,
                                   bypass_codex_sandbox=bypass_codex_sandbox)
    except ValueError as exc:
        print(f"error: {exc}", file=sys.stderr)
        sys.exit(2)

    run_env = dict(os.environ)
    run_env.update(path_env)
    run_env["IS_SANDBOX"] = "1"
    if inputs.open_sandbox_api_key:
        run_env["OPENSANDBOX_API_KEY"] = inputs.open_sandbox_api_key
    if inputs.engine:
        run_env.update(engine_env(inputs.engine, inputs.api_key, base_url))
        run_env.update(engine_model_env(inputs.engine, inputs.model))
        run_env.update(provider_env(inputs.provider, inputs.api_key, base_url))
    else:
        run_env.update(unified_base_url_env(inputs.provider, inputs.base_url))

    print("Running: " + " ".join(shlex.quote(a) for a in mask_argv_for_log(argv)),
          flush=True)
    result = subprocess.run(argv, env=run_env, cwd=target_dir)
    exit_code = result.returncode
    try:
        with open(os.path.join(output_dir, "skill-up-exit-code"), "w") as f:
            f.write(str(exit_code))
    except OSError:
        pass
    print(f"skill-up exit code: {exit_code}")

    _set_output("exit-code", exit_code)
    # Emit a path relative to GITHUB_WORKSPACE, not the in-container absolute
    # path (/github/workspace/...). Downstream steps run on the host runner where
    # that absolute path doesn't exist; a workspace-relative path resolves on both.
    try:
        report_dir_out = os.path.relpath(output_dir, workspace)
    except ValueError:
        report_dir_out = output_dir
    _set_output("report-dir", report_dir_out)

    sys.exit(exit_code)


if __name__ == "__main__":
    main()
