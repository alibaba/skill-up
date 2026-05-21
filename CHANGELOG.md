# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

[0.2.0]: https://github.com/alibaba/skill-up/releases/tag/v0.2.0
[0.1.2]: https://github.com/alibaba/skill-up/releases/tag/v0.1.2
[0.1.1]: https://github.com/alibaba/skill-up/releases/tag/v0.1.1
[0.1.0]: https://github.com/alibaba/skill-up/releases/tag/v0.1.0
