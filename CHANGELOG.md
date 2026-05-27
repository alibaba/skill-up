# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.3] - 2026-05-27

### Added
- `engine.kwargs` (CLI: `--engine-kwarg key=value`, alias `--ek`): an
  agent-specific switch map mirroring `environment.kwargs`/`--runtime-kwarg`.
  Each agent reads only the keys it understands; unknown keys are silently
  ignored. First key: `bypass_sandbox` — `codex` honours it by forcing
  `--dangerously-bypass-approvals-and-sandbox` and skipping its Landlock-based
  `linux-sandbox` wrapper, useful on CI runners whose kernel/seccomp profile
  does not allow Landlock setup (typical Aone CI symptom:
  `error running landlock: Sandbox(LandlockRestrict)`, exit 101 on every
  shell call). `claude_code` and `qodercli` accept the key but no-op.

## [0.2.2] - 2026-05-26

### Added
- `docker` runtime: a third `environment.type` alongside `none` and
  `opensandbox`. Each eval runs inside a local Docker container —
  container-level filesystem/process/network isolation, reproducible via
  pinned images, no external sandbox service dependency. Implemented in
  `internal/runtime/docker.go` against the existing Runtime interface;
  `Create` rolls half-created containers back with `docker rm -f` on any
  failure, Upload/Download use `docker cp`. Closes #32.
- `judge.timeout_seconds` now also bounds `agent_judge`, not just the
  script judge. `0` keeps the previous "no judge-level deadline" semantics
  (parent ctx still applies); positive values cap a single LLM-based
  judge call short of the case timeout.

### Changed
- Case timeout is now a strict budget for **agent + judge as one unit**.
  The salvage detour that re-ran the judge with `context.WithoutCancel`
  when the agent run hit `context.DeadlineExceeded` (gated on
  `>=16` chars of recoverable text) has been removed — it quietly broke
  the contract that the case timeout bounds a case. `handleExecutionResult`
  now ERRORs on any execution error; if the agent's context expired, the
  judge does not run. (`AgentJudge.canRecoverAgentJudgeResult`, a
  separate salvage that reparses JSON from a non-cancelled agent's
  `FinalMessage`, is unaffected.)
- Timeout errors now name the knob and value: `withCaseTimeout` returns a
  `(source, seconds)` pair that `annotateCaseTimeoutError` formats into
  `"… (case timeout 60s via cases.defaults.timeout_seconds)"`, so users
  know which YAML field to tune. Symmetric annotation in
  `agent_judge.annotateTimeoutError` for `judge.timeout_seconds` hits.

### Fixed
- `codex` engine: empty `ModelProvider` plus a non-empty `BaseURL` now
  synthesises the same `model_providers.skill-up-openai` override that
  `ModelProvider="openai"` already produced, with `wire_api="chat"`.
  Previously the empty-provider path emitted only `-c openai_base_url=...`,
  which codex silently ignores — it kept talking to `api.openai.com` under
  the bundled provider with `wire_api="responses"`, so any
  `/chat/completions`-only upstream (idealab, dashscope OpenAI-compat, …)
  returned 400 from `codex_api::endpoint::responses`. The
  `--model <provider>/<name>` path was unaffected; this aligns the
  empty-provider path with it.
- `eval.yaml` defaults: `LoadEvalConfig` now fills the documented defaults
  (`timeout_seconds=300`, `max_turns=10`, `parallelism=1`,
  `report.formats=[json]`) after unmarshal, sourced from
  `DefaultEvalConfig()` so `defaults.yaml` stays the single source of
  truth. Previously these only kicked in via the "no eval.yaml" code path
  — with an eval.yaml present, an omitted `timeout_seconds` resolved to 0
  and `withCaseTimeout` treated it as "no deadline", the opposite of what
  the docs promised. `timeout_seconds=0` is now "not configured, use
  default"; pass `-1` to opt out of the case-level deadline.
- Runtime non-zero exit: surface stdout alongside stderr in the warn /
  error log. `logNonZeroStderr` alone left users staring at an exit code
  with no context when the failing tool (build, test runner, package
  manager) wrote diagnostics to stdout. Wired into `NoneRuntime.Exec`,
  `DockerRuntime.classifyExecResult`, and `OpenSandboxRuntime.Exec`
  (non-zero, context-killed, and SDK-failure branches).
- Case-timeout label no longer mislabels child or parent deadlines.
  `annotateCaseTimeoutError` previously stamped
  `"(case timeout … via cases.defaults.timeout_seconds)"` on any error
  whose chain contained `context.DeadlineExceeded`, so a tighter
  `judge.timeout_seconds` firing — or a parent caller's
  `context.WithTimeout` propagating through — pointed users at the
  wrong YAML knob. The annotation now requires both the case-level
  attempt context to have hit `DeadlineExceeded` *and* the parent
  context to still be alive at return time.

## [0.2.1] - 2026-05-22

### Fixed
- `skill-up run --format json` now generates `report.json` in the iteration
  output directory. Previously the `"json"` format was silently skipped
  because `result.json` is always written unconditionally; this made
  `--format json` a no-op and was inconsistent with `skill-up report --format json`
  which correctly produced `report.json`.

## [0.2.0] - 2026-05-21

### Changed
- `kind: SkillEvalConfig` renamed to `kind: SkillUpConfig` in all user config files.
  Existing configs using the old kind value must be updated.
- `$SKILL_EVAL_CONFIG` environment variable renamed to `$SKILL_UP_CONFIG`.
  Scripts and CI pipelines that set `SKILL_EVAL_CONFIG` must switch to `SKILL_UP_CONFIG`.

## [0.1.2] - 2026-05-21

### Added
- `skill-upper`: a bundled meta-skill under `skills/skill-upper/` that
  drives `skill-up` to scaffold and run evals for a target skill end-to-end.
- `--runtime` CLI flag on `skill-up run` overrides `environment.type`
  (`none` / `opensandbox`) without editing `eval.yaml`. The override path
  also rejects `--runtime none` when `network_policy` is configured, so
  network isolation cannot be silently dropped.
- `network_policy` for sandbox egress isolation: `deny_all` blocks all
  egress; `allow_declared` plus `environment.allowed_egress` permits only
  the listed FQDN / wildcard domains. Validated against the runtime type
  (rejected on `environment.type: none`) and against URL-shaped entries.
- Mocked MCP servers (`mode: mocked`): provisioned as generated stdio
  servers via the existing agent MCP install path. Supports the built-in
  `filesystem` mock and arbitrary `tool_responses` via `config_ref`.
- `userconfig.LoadFile(path)` helper for callers that need to read and
  validate a single config file without consulting discovery layers.
- Coverage badge generation in CI and README badge links.

### Changed
- `skill-up init`: `--config <path>` now names a **source** file to read
  rather than a write target. `init` validates the source as a skill-up
  config and writes its raw bytes (comments preserved) to the target
  selected by `--local` (or the default XDG path). `--local` and
  `--config` are no longer mutually exclusive. Without `--config`, `init`
  still writes the commented template as before. This aligns `--config`
  with the `run`/`validate` discovery chain semantics
  (`embed < user < project < --config`).
- OpenSandbox directory uploads now use the SDK's batch `UploadFiles` in
  chunks of 128 files (one multipart request per chunk) instead of N
  concurrent single-file uploads. `file_transfer_parallelism` now only
  governs directory **download**.
- OpenSandbox SDK upgraded to v1.0.1.
- Default eval workspace is now placed **alongside** the skill directory
  (`<skill>-workspace/` as a sibling of `<skill>/`) instead of inside it,
  matching the Anthropic eval layout convention. Docs and `--output-dir`
  default updated accordingly.

### Fixed
- `context.git.checkout` is now actually honored by the fixture loader.
  Previously the field parsed but was never read, so cases ran on the
  fixture's current branch. The loader now runs `git switch` (or creates
  the branch on an unborn HEAD) before inline `context.files` and
  `apply_diff`; a missing branch in a committed fixture fails loudly,
  and branch names are validated against shell injection.
- `skill-up run` credential resolver now reads the documented
  `~/.skill-up/credentials.yaml`. Previously the run path passed an
  empty config path, so only `--api-key`, env vars, and a CWD `.env`
  were consulted.
- Post-run session lookup, stdout download, and last-message resolution
  (claude_code / codex / qodercli) no longer inherit the canceled run
  context, so a timed-out eval case no longer produces two bursts of
  misleading "command exited with code -1: ..." log lines for the
  cleanup steps. A fresh 30s `sessionCleanupTimeout` is used instead.
- `runtime/none`: when a command is killed because its context was
  canceled or timed out, the log now reads "command killed by context"
  instead of pretending to be a real `-1` exit with the full bootstrap
  script attached.
- `runtime/opensandbox`: when the OpenSandbox SDK call itself fails
  (network error, nil execution, timeout before a remote exit code is
  reported), the log now identifies it as an SDK-level failure and
  skips the misleading script dump. Genuine remote non-zero exits keep
  the existing log.

## [0.1.1] - 2026-05-15

### Added
- `install.sh` one-line installer that downloads the matching release archive
  for the host OS/arch and installs the `skill-up` binary.

### Fixed
- Codex agent now honors `OPENAI_BASE_URL` when `provider=openai`, emitting a
  full `model_provider` override so traffic routes to the configured endpoint
  instead of always dialing `api.openai.com`.
- Skip the nvm bootstrap step when the `claude` / `codex` CLI is already
  available on `PATH`.

## [0.1.0]

### Added
- Initial public release of **skill-up**. This baseline establishes the full
  project and delivers the end-to-end capability to declare eval environments,
  run cases and emit structured reports as described in [README.md](README.md).

[0.2.1]: https://github.com/alibaba/skill-up/releases/tag/v0.2.1
[0.2.0]: https://github.com/alibaba/skill-up/releases/tag/v0.2.0
[0.1.2]: https://github.com/alibaba/skill-up/releases/tag/v0.1.2
[0.1.1]: https://github.com/alibaba/skill-up/releases/tag/v0.1.1
[0.1.0]: https://github.com/alibaba/skill-up/releases/tag/v0.1.0
