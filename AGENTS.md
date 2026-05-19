# AGENTS.md

> This file follows the [agents.md](https://agents.md) convention and provides
> machine-readable guidance for AI coding agents (Qoder, Claude Code, Codex,
> Cursor, etc.) working in this repository. For a human-oriented overview,
> see [`README.md`](README.md) and [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Project overview

**skill-up** is a CLI evaluation framework for Agent Skill developers, written
in Go. It loads declarative eval configs (`evals/eval.yaml` + `cases/*.yaml`),
provisions an isolated workspace, invokes an Agent Engine (Qoder CLI / Claude
Code / Codex), runs Judges (`rule_based` / `script` / `agent_judge`), and emits
structured reports (JSON / JUnit / HTML / Anthropic `grading.json`).

- Module path: `github.com/alibaba/skill-up`
- Entry point: [`cmd/skill-up/main.go`](cmd/skill-up/main.go)
- Go version: **1.25+** (see [`go.mod`](go.mod))
- License: Apache-2.0

## Setup commands

```bash
# Fetch dependencies (only needed after cloning or go.mod changes)
go mod download

# Install git hooks (pre-commit + commit-msg). Idempotent; auto-runs on first build.
make hooks

# Install pinned lint tools into .tools/bin/ (golangci-lint v2.11.4, revive v1.10.0)
make lint-tools
```

If you are in mainland China and `go install` is slow, set
`GOPROXY=https://goproxy.cn,direct` before running the commands above.

## Build & run

```bash
# Standard build with version injection via ldflags -> bin/skill-up
make build

# Fast iteration build (no version injection)
go build -o bin/skill-up ./cmd/skill-up

# Run the CLI
./bin/skill-up --help
./bin/skill-up run ./evals/eval.yaml
./bin/skill-up validate ./evals/eval.yaml
```

## Testing

```bash
# Unit tests (race detector enabled — always use this)
make test
# equivalent: go test -race ./...

# Run a single test
go test -race -run TestFoo ./internal/config/

# End-to-end tests (build-tag gated, slower)
make e2e
# equivalent: go test -tags e2e -v ./e2e
```

Rules for agents:

- **Always run `make test` before declaring a change complete.** New code
  must not break any existing tests.
- Add or update tests alongside any behavior change. Table-driven tests are
  the dominant pattern in this repo — follow it.
- Avoid network-dependent tests; use fixtures under `internal/*/testdata/`
  or `e2e/testdata/` for inputs.
- E2E tests live in [`e2e/`](e2e/) and are gated by the `e2e` build tag;
  do not add them to regular unit packages.

## Code style & static analysis

The CI gate (and `pre-commit` hook) is `make verify`, which chains:

```
fmt-check   -> gofmt must be clean (run `make fmt` to auto-fix)
vet         -> go vet ./...
revive      -> revive -config revive.toml ./... (incremental linter)
lint        -> golangci-lint run ./... (config: .golangci.yml)
```

Agents MUST:

- Run `make fmt` before committing Go files.
- Run `make verify` before declaring a change complete. If any step fails,
  fix the findings — do not disable lints to pass CI.
- Keep the pinned tool versions consistent between [`Makefile`](Makefile)
  (`GOLANGCI_LINT_VERSION`, `REVIVE_VERSION`), CI workflows under
  [`.github/workflows/`](.github/workflows), and documentation.
- Not introduce new abstractions, helpers, or files unless the task
  actually requires them. Bug fixes should stay local.

Language & comments:

- Public (exported) identifiers need Go doc comments starting with the name.
- Inline comments are sparse — only explain non-obvious intent.
- Comment and documentation language: **English** for code-level artifacts;
  user-facing docs may be bilingual (`README.md` + `README.zh.md`).

## Repository layout

```
cmd/skill-up/       CLI main (keep minimal; delegate to internal/cli)
internal/           Private implementation — never import from outside the module
  cli/              Cobra subcommands (run / validate / list-cases / report / import / debug)
  config/           eval.yaml + cases/*.yaml loader, schema, validator (v1alpha1)
  credential/       API key resolution (flags / env / ~/.skill-up/credentials.yaml)
  runtime/          Workspace runtime: none / opensandbox
  agent/            Agent Engine adapters (qoder_cli / claude_code / codex)
  mcp/              MCP provisioner (mock / real)
  skill/            Install Skill files into Engine's conventional path (excluding evals/)
  evaluator/        Evaluator: iterates cases, calls agent.Run, returns CaseResult
  judge/            Judges: rule_based, script, agent_judge
  report/           Report generators: JSON / JUnit / HTML / Anthropic grading & benchmark
  runner/           End-to-end orchestration for `skill-up run`
  logging/ observability/ shellquote/ ui/    Cross-cutting utilities
pkg/                Publicly importable APIs (semver-stable; change with care)
  skillup/          Embeddable evaluation API
  transcript/       Transcript parsing helpers
skill-upper/        Distributable Agent Skill that guides AI agents through the
                    skill-up eval workflow (scaffolding, running, interpreting).
                    Contains SKILL.md (English), SKILL.zh.md (Chinese), assets/
                    templates, references/, and its own evals/ suite. Not part of
                    `go build`; consumed by Agent Engines (Cursor, Claude Code,
                    Qoder, etc.) at runtime.
e2e/                End-to-end tests (build-tag gated) + testdata/
examples/           Example fixtures and debug inputs
docs/               Design docs, user manuals, and the VitePress site
                    (built & deployed to GitHub Pages by .github/workflows/docs.yml)
```

### Boundary rules

- `internal/` is **private**. Never import it from outside this module; never
  re-export its types through `pkg/` without a conscious API-stability review.
- `pkg/skillup/` and `pkg/transcript/` are **public, semver-stable**.
  Breaking changes here require a new module major version.
- CLI glue belongs in `internal/cli/`; business logic belongs in the
  corresponding `internal/<domain>/` package.

## Configuration schema

- Schema version: `v1alpha1` (declared in each YAML's `schema_version`).
- `eval.yaml` -> [`internal/config.EvalConfig`](internal/config/schema.go)
- `cases/*.yaml` -> [`internal/config.CaseConfig`](internal/config/schema.go)
- Path resolution: `cases.files` and fixture-style references are resolved
  **relative to the directory that contains `SKILL.md`**, not the current
  working directory.

When changing the schema: update `schema.go`, `validator.go`, their tests,
`defaults.yaml`, and any affected example under `examples/` or `e2e/testdata/`
in the same commit.

## Commit & PR guidelines

- **Conventional Commits** are enforced by `.githooks/commit-msg`. Subject
  format: `<type>(<scope>): <description>`.
  Allowed types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`,
  `build`, `ci`, `chore`, `revert`.
  Examples:
  - `feat(cli): add validate command`
  - `fix(config): correct default timeout`
  - `docs: update AGENTS.md`
- Keep commits focused; prefer multiple small commits over one sweeping change.
- Do **not** commit:
  - Real API keys, tokens, or credentials (see `internal/credential/README.md`).
  - Local caches (`.cache/`, `.tools/bin/`), build outputs (`bin/`, `dist/`),
    or personal editor configs. These are in `.gitignore` — keep it that way.
- Before opening a PR:
  1. `make fmt`
  2. `make verify`
  3. `make test`
  4. If you touched anything under `e2e/` or `internal/runner/`, also run `make e2e`.
  5. Update `CHANGELOG.md` if the change is user-visible.

## Security considerations

- Credentials resolve in this priority order: CLI flag -> environment variable
  -> `~/.skill-up/credentials.yaml`. Never log raw credential values; redact
  them via the helpers in `internal/credential/`.
- External processes (Agent Engines, MCP servers, scripts) run in the selected
  runtime (`none` or `opensandbox`). When adding new shell execution paths,
  use [`internal/shellquote`](internal/shellquote/shellquote.go) — never
  concatenate untrusted strings into a shell command.
- `gosec` and `noctx` are enabled in `.golangci.yml`; fix findings rather than
  silencing them.

## Release

- Releases are driven by [GoReleaser](.goreleaser.yaml), triggered by pushing
  a `v*` tag. CI workflow: [`.github/workflows/release.yml`](.github/workflows/release.yml).
- Version is injected at build time via `-ldflags "-X main.version=<tag>"`.
  The fallback `var version = "dev"` lives in `cmd/skill-up/main.go`.
- Do not create tags or invoke `goreleaser release` automatically — release
  cadence is owned by maintainers.

## What agents should NOT do

- Do not modify `go.mod` / `go.sum` except via `go get` or `make tidy`.
- Do not add new top-level directories without updating this file,
  `CONTRIBUTING.md`, and the `Project Structure` section of `README.md`.
- Do not disable or weaken existing lint rules, tests, or git hooks to
  make a change pass.
- Do not introduce dependencies on `internal/*` from outside the module,
  or on GPL / network-only packages.
- Do not generate large refactors, planning docs, or analysis documents
  unless the task explicitly asks for them.
