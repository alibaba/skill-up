# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

[0.1.1]: https://github.com/alibaba/skill-up/releases/tag/v0.1.1
[0.1.0]: https://github.com/alibaba/skill-up/releases/tag/v0.1.0
