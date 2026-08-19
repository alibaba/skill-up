# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Failed `agent_judge` criteria can now include an optional, structured
  AI-generated diagnosis with a likely failure attribution, confidence,
  attribution evidence, and an actionable improvement suggestion. Standard
  judge context includes a bounded, hashed, pre-execution snapshot of the
  evaluated Skill source, while benchmark baselines and engines without
  trustworthy Skill-use evidence are prevented from producing unsupported
  Skill-specific blame.

### Fixed
- `agent_judge` now uses stable criterion IDs and a strict JSON response
  contract, rejects malformed or semantically incomplete judge output, and
  always derives report labels and ordering from configured criteria.
- `agent_judge` now makes one bounded output-correction attempt after strict
  decoding or semantic validation fails. Both raw responses are preserved,
  retry engine artifacts are isolated under `outputs/judge/run/retry/`, and
  runtime-backed artifacts are snapshotted before a retry can overwrite them.
  Independent correction runs begin at turn one, while aggregate Judge duration
  and usage metrics cover the complete correction flow without changing report
  schemas.
- HTML and Markdown reports now identify the top-level duration as evaluation
  wall time and show per-case tested-agent execution time plus input, output,
  and total token usage. Benchmark cases include compact with-Skill,
  without-Skill, and delta annotations beside the case heading. Agent-judge
  time and tokens are reported separately and included in an explicit overall
  token total.
- Multi-iteration reports now record each iteration's own start time and wall
  time instead of reusing the command start time and accumulating the duration
  of all preceding iterations.
- Codex JSONL parsing now accepts records up to a configurable 16 MiB default
  while rejecting oversized records explicitly. The stdout stream is written to
  a bounded-download artifact instead of being buffered without limit in host
  memory, and the `--output-last-message` artifact remains the authoritative
  final response, so a large tool result cannot leave an earlier progress update
  to be graded as the answer.

## [0.9.0] - 2026-08-12

### Fixed
- Multi-turn evaluation now resumes the CLI session it started. Session
  transcripts are located without reconstructing a CLI's project-directory
  naming rule: directory names and the workspace path are compared in a shared
  canonical form, falling back to the working directory a transcript records.
  Workspace paths containing underscores, dots, spaces or non-ASCII characters
  no longer break resume — previously such paths (macOS default `TMPDIR` among
  them) made every turn start a brand-new session and lose all earlier context.
- Session lookup no longer mistakes a sub-agent transcript for the main session.
  A Skill or Task call writes `<sessionID>/subagents/agent-*.jsonl` into the same
  project tree, and those file names are not resumable session ids.
- Session lookup no longer resumes or grades a transcript belonging to another
  workspace. The CLIs collapse punctuation when naming a project directory, so
  workspaces whose paths differ only in punctuation share one directory.
  Candidates are resolved against the working directory a transcript records,
  matched as the JSON fragment it is written as, so a Windows path
  (`C:\\Users\\...`) is recognised and a path that merely appears in the
  conversation is not mistaken for identity. When a neighbouring transcript proves
  the directory is shared and none can be attributed to this workspace, the lookup
  reports no session instead of guessing. Markers are encoded the way the CLIs
  write them (no HTML escaping, so a path containing `&`, `<` or `>` matches), and
  Windows comparisons fold case because the same directory may reach skill-up
  spelled differently from how the CLI recorded it.
- Per-turn responses in multi-turn evaluation are now the answer that closed each
  turn. Turn boundaries are derived from the transcript each turn appends instead
  of from session-file turn numbers, which count tool results and injected Skill
  bodies as user events and therefore advance several times within one turn.
- Multi-turn evaluation warns when an engine reports no session id and later
  turns cannot resume, instead of silently degrading to independent sessions.

### Changed
- Assistant messages in a parsed transcript carry only the text the assistant
  produced. A tool call is represented by its own `tool_call` entry and no longer
  also appears as a `[tool: Name]` placeholder in assistant content, so such a
  placeholder can no longer be graded as a turn's answer.

## [0.8.0] - 2026-08-07

### Added
- Eval-level default expect checks under `cases.defaults.expect`, with
  append-and-deduplicate semantics for list checks and case-level overrides for
  `exit_code` and `golden_file`.
- Multi-iteration `skill-up run --iteration N` now prints a per-case
  stability summary to highlight flaky PASS/FAIL/ERROR/SKIP outcomes.
- Skill references now support explicit doublestar `include` and `exclude`
  patterns for both run-agent and judge Skill installation.
- Markdown report generation through `skill-up report --format markdown`,
  producing a concise `report.md` suitable for CI logs and pull request
  comments.
- A `--baseline` flag for `skill-up run` that enables benchmark comparison for
  an individual invocation without changing `eval.yaml`.

### Fixed
- Claude Code session lookup now normalizes Windows workspace paths to the
  project-directory key used by the CLI, restoring token usage in reports.
- Aggregate report statistics now exclude `without_skill` baseline runs, so
  pass rates and status totals describe the evaluated Skill rather than mixing
  benchmark control results into the headline metrics.

### Fixed
- QoderCLI runs now enable token-usage exposure and parse usage from JSON
  stdout, allowing `input_tokens` and `output_tokens` to be populated even when
  the runtime cannot read Qoder's private session directory.

## [0.7.0] - 2026-07-16

### Added
- Per-case mocked MCP response overrides (SUP-0003): a case file may now declare
  its own `mcp.servers` to vary mocked fixtures while keeping the same MCP
  server and tool names. Servers merge with eval-level `mcp.servers` by `name`
  (whole-entry replacement; unmatched names are appended), and a case without
  `mcp` inherits the eval-level config unchanged. Case-level servers must use
  `mode: mocked` in this MVP, non-built-in mocked servers must provide
  `config_ref`, `config_ref` stays relative to the Skill directory, and each
  case provisions its own mocked server so fixtures remain isolated under
  `cases.parallelism > 1`.

## [0.6.0] - 2026-07-14

### Added
- Judge-specific Skills for `agent_judge` via `judge.skills`. These Skills are
  installed only into the judge agent and can provide reusable grading rubrics,
  evidence rules, and domain-specific constraints without leaking into the run
  agent or benchmark baseline.
- Judge Skill metadata in JSON, JUnit, and HTML reports, including the source,
  path, target, and callable Skill name used for the evaluation.

### Fixed
- Judge prompts now identify installed Skills by their callable names and
  explicitly require invoking each Skill tool and reading its body before
  grading. Source paths remain only as an internal fallback when no name is
  available.

## [0.5.0] - 2026-07-09

### Added
- Configurable prompt delivery for `agent_judge` through `judge.context`.
  Supports `minimal` and `standard` context profiles, per-field delivery
  modes, inline-size limits, generated-file indexing, and attachment file
  references. Reports include `judge_context` metadata describing how review
  materials and prompts were delivered.
- Built-in CLI agents now route large prompts through runtime-readable files
  and stdin when they exceed `SKILL_UP_PROMPT_INLINE_MAX_BYTES` (default
  `32768` bytes), while shorter prompts remain inline.

### Changed
- `agent_judge` now defaults to materialized review context (`profile:
  standard`) instead of inlining the full transcript and workspace diff into
  the judge prompt. Large artifacts are delivered through runtime-readable
  files, while short final messages remain inline. Evals that require legacy
  inline transcript delivery can set `judge.context.transcript: include`.

The `v0.5.0` release tag is available at
[GitHub](https://github.com/alibaba/skill-up/releases/tag/v0.5.0).

## [0.4.0] - 2026-07-07

### Added
- **Multi-turn conversation evaluation** (`input.turns`): define sequential
  user turns with per-turn `post_condition` gates (`must_contain_all`,
  `must_contain_any`, `must_not_contain`, `on_fail: fail|skip_remaining`) and
  `capture` for regex-based variable extraction between turns.
- Per-turn judge assertions: `turn_response_contains`,
  `turn_response_not_contains`, `tool_called_in_turn`,
  `tool_not_called_in_turn` — all scoped by turn number.
- Session resumption support for `claude_code` (`--resume`), `qodercli`
  (`-r <session-id>`), and `codex` (`codex resume <thread-id>`) engines;
  unsupported engines fall back to batch mode.
- `TurnResults` field in JSON, HTML, and JUnit reports (`turn_results` in
  `result.json`; turn-scoped failure details in JUnit XML).

### Changed
- `skill-up run` now validates only the cases left after
  `--include-case-name` / `--exclude-case-name` filters (plus the eval-level
  config), so an invalid case that is filtered out no longer blocks a filtered
  run — a shared eval can hold a quick `smoke` subset alongside heavier cases.
  `skill-up validate` is unchanged and still validates the whole suite (every
  case) regardless of filters.

### Added
- Config validation now rejects duplicate case IDs and duplicate `cases.files`
  references. Since reports and artifacts are keyed by case ID and one case runs
  per `cases.files` entry, a collision would silently overwrite results or
  double-run a case. `skill-up validate` (and the preflight in `skill-up run`)
  fails with the conflicting ID and the offending source files. Covers both
  explicit `id:` fields and filename-derived IDs, and collapses
  differently-spelled paths (e.g. `cases/a.yaml` vs `./cases/a.yaml`).

### Changed
- The `none` runtime now terminates a canceled or timed-out command's whole
  process group gracefully before force-killing it. On Unix it sends `SIGTERM`
  to the group first, giving children ~1s (`noneExecKillGrace`) to run trap
  handlers, release locks, and exit, then escalates to `SIGKILL` if the group
  is still alive. The grace fits inside the existing 2s `WaitDelay`, so a
  timed-out `Exec` still returns within its deadline and keeps the
  `context.DeadlineExceeded` / exit-code `-1` contract. Previously the group
  got an immediate `SIGKILL`, so a helper holding a lock could leave stale
  state and stall a later command. No-op on non-Unix platforms.

### Fixed
- Skill installation preserves source file permissions, notably the executable
  bit on scripts. The `none` runtime's per-file upload previously wrote every
  file with a fixed `0600` mode, so skills shipping runnable helper scripts
  installed non-executable and failed without a chmod workaround. The
  file-transfer contract now documents permission preservation, matching the
  `opensandbox` and `docker` runtimes which already carried the mode across.

## [0.3.0] - 2026-07-03

### Added
- Built-in `qwen_code` engine (aliases `qwen-code`, `qwen`), backed by the
  [Qwen Code](https://github.com/QwenLM/qwen-code) CLI (`@qwen-code/qwen-code`).
  Installed on demand via npm (Node.js bootstrapped automatically) and run
  non-interactively with `qwen --yolo -p <instruction>`. Authenticates against
  any OpenAI-compatible endpoint through `OPENAI_API_KEY` / `OPENAI_BASE_URL`,
  with `engine.model.name` / `--model` forwarded as both the `-m` flag and
  `OPENAI_MODEL`; MCP servers install via `qwen mcp add`.
- Custom Engine `http` transport (`engine.custom.transport: http`): calls a
  remote (or local) HTTP agent service, POSTing the `SessionInput` and parsing
  the `SessionResult` from the response. Renders `${...}` references in
  `http.url` / `http.headers` / `http.request_body`; a `request_body` field
  whose value is exactly `${session_input}`, `${messages}`, or `${kwargs}` is
  injected as a JSON structure rather than a string. A non-2xx status is treated
  as an engine execution error, and the API key is rejected in the URL (use a
  header or the request body instead) and masked in responses.
- Custom Engine `http` transport multipart file upload (`engine.custom.http.files`):
  when files are declared the request becomes `multipart/form-data` — the JSON
  body is sent as the `payload` field and each matched workspace file as a
  `files` part (the part filename is the workspace-relative path). Paths may be
  exact or doublestar globs (`*`, `**`), are confined to the workspace, and only
  regular files are uploaded; `required: true` (the default) errors when an
  entry matches nothing, `required: false` skips it.
- Custom Engine: download `artifacts.files[].url` artifacts into the report. A
  result that declares a downloadable `url` is fetched (http/https only) and
  written into the case artifact directory under `name`. The download is
  best-effort and bounded (256 MB cap, request timeout): a non-2xx status,
  transport error, non-http(s) scheme, or over-cap body is logged and skipped
  without failing the run. A URL embedding the configured API key is refused, and
  logged errors are scrubbed of the configured `api_key` / `engine.custom.env`
  secrets.

## [0.2.4] - 2026-06-03

### Added
- `collect_artifacts`: glob patterns (doublestar syntax — `*` within a
  path segment, `**` across directories) that select workspace files to
  download as run artifacts. Configurable at `cases.defaults.collect_artifacts`
  (all cases) and per-case `collect_artifacts` (merged as a de-duplicated
  union). Matches are downloaded — preserving their workspace-relative path —
  to `<output-dir>/<case>/<config>/outputs/workspace/`, **regardless of whether
  the agent succeeded, failed, or timed out**. Collection is read-only and
  orthogonal to `report.artifacts` (artifact types) and the `agent_judge`
  workspace diff.
- Custom Engine with `local` transport (`engine.custom` in `eval.yaml`):
  plug in any agent executor as a user-provided command without modifying
  `skill-up`. The framework runs the command inside the current runtime via
  `runtime.Exec`, passes a standard `SessionInput` (JSON), and parses a
  standard `SessionResult`. Supports `session_result` and `text` response
  formats, structured file artifacts, `env` / `kwargs` forwarding, and
  `${VAR}` / `${VAR:-default}` / `${VAR?msg}` env references resolved at
  load time. API keys and custom env values are masked in reports.
  `http` transport config is parsed and validated but returns an explicit
  "not yet implemented" error — HTTP lands in a follow-up.
- `runtime.Runtime.MergeEnv(env)`: new interface method that lets
  setup-time callers (notably each agent's `Install`) seed the runtime's
  persistent env baseline. Subsequent `Exec` calls inherit these vars
  unless overridden by `opts.Env`. Implementations are intended for
  one-time setup; concurrent `MergeEnv`/`Exec` is not safe.
- **First-class Windows support** for the CLI, the `none` runtime, and the
  script judge. Native Windows builds run all unit tests, the script judge
  routes `.ps1`/`.cmd`/`.bat` directly and `.sh` through Git Bash when
  available, and CI gains a `windows-latest` build/test matrix plus a
  dedicated `E2E (none runtime, Windows)` contract job.
  See [Windows Support](docs/guide/windows.md) for the full guide.
- `SKILL_UP_BASH` environment variable: explicit path to a `bash`
  executable for skill-up's `none` runtime to use. Honored on every
  platform (read once at startup, takes precedence over `PATH`).
- PowerShell tooling under `scripts/windows/`: `hooks.ps1`,
  `lint-tools.ps1`, and `verify.ps1` mirror the Makefile targets for
  contributors on Windows; `examples/judge-debug-eval.ps1` provides a
  runnable PowerShell script-judge example.
- `.gitattributes` pins line endings (LF for `*.sh`, CRLF for `*.ps1` /
  `*.cmd` / `*.bat`) so Git checkout on Windows does not break scripts.

### Changed
- Agent layer no longer injects PATH into the env map. Each agent's
  `Install` now probes the target shell for the resolved PATH (a
  per-agent `printf '%s' "$HOME/..."` command) and merges the literal
  result into the runtime's env baseline via `runtime.MergeEnv`.
  Runtimes treat env keys uniformly — no key is special-cased anywhere.
- `NoneRuntime` no longer expands `$VAR` / `${VAR}` in user-provided env
  values (`environment.env` / `opts.Env`). Values forward literally,
  matching docker and opensandbox. If your `eval.yaml` relied on
  host-side expansion (e.g. `MY_VAR: "$HOME/foo"`), switch to a literal
  value or `export` it inside a `setup_steps` step.
- `docker` / `opensandbox`: the same "values forward literally" rule
  now applies — previously the docker runtime silently dropped
  `environment.env.PATH` values containing `$` (the alternative was
  passing them to `docker create --env` literally, which would have
  broken container startup because the entrypoint's PATH lookup can't
  resolve directories named `$HOME` / `$PATH`). After this PR no key
  is special-cased, so passing such a value will now make the
  container/sandbox fail to start; use a literal PATH or move the
  manipulation into a `setup_steps` `export`.
- `environment.type: none`: the framework no longer force-prepends
  `$HOME/.local/bin:$HOME/.nvm/current/bin:$PATH` to agent commands.
  Because `ag.Install` is skipped for type=none, the new probe doesn't
  run either; the host shell's PATH is used as-is. Add the dirs to
  your shell rc if `claude` / `codex` / `qodercli` isn't already on it.
- Non-built-in `engine.name` values with an `engine.custom` block now
  dispatch to `CustomAgent`; without the block the error is
  `unsupported agent "x": missing engine.custom`.
- Agent CLIs (Claude Code, Codex, Qoder CLI) now hard-fail on Windows
  hosts without a discoverable bash, with a clear error pointing at
  Git Bash or `SKILL_UP_BASH`. Previously the cmd.exe fallback would
  accept agent commands but leak shell metacharacters from instructions
  into the host shell.
- `internal/platform` centralizes host shell, quoter, and bash discovery
  behind a single `platform.Host()` (cached for the process lifetime).
  Replaces the previous ad-hoc platform branching in `NoneRuntime.Exec`
  and the script-judge planner.
- `Runtime.TargetGOOS() string` is now a required interface method so
  future runtimes get a compile-time error rather than silently
  defaulting to `"linux"`.

### Fixed
- Skill name in reports (`result.json`, benchmark, JUnit, HTML) now
  reads the `name:` field from SKILL.md frontmatter instead of using
  the directory basename. Falls back to basename when SKILL.md is
  missing, has no frontmatter, or declares no name.

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

[0.9.0]: https://github.com/alibaba/skill-up/releases/tag/v0.9.0
[0.8.0]: https://github.com/alibaba/skill-up/releases/tag/v0.8.0
[0.7.0]: https://github.com/alibaba/skill-up/releases/tag/v0.7.0
[0.6.0]: https://github.com/alibaba/skill-up/releases/tag/v0.6.0
[0.5.0]: https://github.com/alibaba/skill-up/releases/tag/v0.5.0
[0.4.0]: https://github.com/alibaba/skill-up/releases/tag/v0.4.0
[0.3.0]: https://github.com/alibaba/skill-up/releases/tag/v0.3.0
[0.2.4]: https://github.com/alibaba/skill-up/releases/tag/v0.2.4
[0.2.3]: https://github.com/alibaba/skill-up/releases/tag/v0.2.3
[0.2.2]: https://github.com/alibaba/skill-up/releases/tag/v0.2.2
[0.2.1]: https://github.com/alibaba/skill-up/releases/tag/v0.2.1
[0.2.0]: https://github.com/alibaba/skill-up/releases/tag/v0.2.0
[0.1.2]: https://github.com/alibaba/skill-up/releases/tag/v0.1.2
[0.1.1]: https://github.com/alibaba/skill-up/releases/tag/v0.1.1
[0.1.0]: https://github.com/alibaba/skill-up/releases/tag/v0.1.0
