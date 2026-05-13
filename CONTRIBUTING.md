# Contributing

This document explains the directory layout of the **skill-up** repository and the responsibilities of each directory in the overall architecture, helping you locate code and submit changes.

## How to Contribute

We welcome bug reports, feature requests, documentation improvements, and code contributions. The general workflow is:

1. **Open an issue first** for non-trivial changes (new features, behavior changes, breaking refactors), so we can align on direction before you invest time. Typo fixes and small bug fixes can go directly to a PR.
2. **Fork** this repository and clone your fork locally.
3. Create a topic branch from the latest `main`. Suggested naming:
   - `feat/<short-description>` for new features
   - `fix/<short-description>` for bug fixes
   - `docs/<short-description>` for documentation-only changes
4. Make your changes. Before committing, run:
   ```bash
   make fmt       # format Go files
   make verify    # fmt-check + vet + revive + golangci-lint
   make test      # unit tests with race detector
   # If you touched anything under e2e/ or internal/runner/, also run:
   make e2e
   ```
5. Commit using **Conventional Commits** (enforced by `.githooks/commit-msg`). See the *Commit Message* section below for the allowed types and examples.
6. Push your branch to your fork and open a Pull Request against `main`. Fill out the PR template, link any related issues, and describe the user-visible impact.
7. Update [`CHANGELOG.md`](CHANGELOG.md) in the same PR if your change is user-visible.
8. CI must pass (`make verify` + `make test`) before a maintainer reviews. Address review feedback by pushing additional commits to the same branch; we squash on merge by default.

### Sign-off / CLA

This project does not currently require a CLA or DCO sign-off. By submitting a Pull Request, you agree to license your contribution under the project's [Apache-2.0 License](LICENSE).

### Reporting security issues

Do **not** file public issues for security vulnerabilities. Follow the disclosure process described in [`SECURITY.md`](SECURITY.md).

## Directory Overview

```text
skill-up/
├── cmd/skill-up/          # CLI executable entry point (main)
├── internal/                # Implementation code used only by this repo (no public stable API)
│   ├── cli/                 # Cobra subcommands: run / validate / list-cases / report
│   ├── config/              # Loading, schema, and validation of eval.yaml and case YAMLs
│   ├── credential/          # Model credential resolution (CLI / env vars / user config)
│   ├── runtime/             # Eval runtime: none / opensandbox
│   ├── agent/               # Agent Engine adapters (claude_code, codex, custom)
│   ├── judge/               # Evaluators: rule_based, agent_judge, script
│   ├── report/              # Report output: JSON, JUnit, HTML
│   ├── runner/              # End-to-end orchestration (config → environment → cases → report)
│   ├── mcp/                 # MCP Server configuration (mocked / real)
│   └── skill/               # Install Skill into each Engine's conventional directory (excluding evals/)
├── pkg/transcript/          # Reusable transcript parsing and helpers (publicly importable package)
├── docs/                    # VitePress documentation site (guide/, zh/, user-manual/, .vitepress/, public/)
├── .githooks/               # Git hooks (commit message, pre-commit checks); see "Engineering Constraints" below
├── .github/workflows/       # CI (build & test) and Release (GoReleaser)
├── Makefile                 # Common build and check commands
├── .golangci.yml            # golangci-lint rules
├── .editorconfig            # Basic editor conventions (indentation, line endings, etc.)
├── .goreleaser.yaml         # Configuration for releasing binaries and archives
├── go.mod / go.sum          # Go module and dependency lockfile
├── README.md                # Project introduction and quick start
└── LICENSE                  # License (Apache 2.0)
```

## Directory Descriptions

### `cmd/skill-up/`

- **Meaning**: The `main` package of the CLI program; responsible for starting the process and invoking `internal/cli`.
- **Convention**: Keep it minimal; business logic belongs in `internal/`.

### `internal/cli/`

- **Meaning**: Cobra-based command-line interface that defines the root command, subcommands, flags, and help text.
- **Relation to design**: Corresponds to the CLI in the design doc (`validate`, `list-cases`, `run`, `report`).

### `internal/config/`

- **Meaning**: Reads `evals/eval.yaml` and `cases/*.yaml`, performs schema alignment, default values, and reference checks; `cases.files` is relative to the directory containing `SKILL.md`, and fixture-style references are also relative to that directory.
- **Relation to design**: Corresponds to the "Config Loader" and the `v1alpha1` configuration model.

### `internal/credential/`

- **Meaning**: Resolves API keys and similar credentials by priority (CLI flags, env vars, `~/.skill-up/credentials.yaml`, etc.).
- **Relation to design**: Corresponds to credential management in the CLI design.

### `internal/runtime/`

- **Meaning**: Abstracts "the environment required for one evaluation": lifecycle and workspace for a local temp directory or remote sandbox.
- **Relation to design**: Corresponds to `environment.type` (`none` / `opensandbox`).

### `internal/agent/`

- **Meaning**: Invokes each Agent Engine, turning prompts and workspace constraints into a session execution and collecting a contract-compliant `SessionResult` (with transcript).
- **Relation to design**: Corresponds to the "Engine Adapter" and the custom Engine contract.

### `internal/judge/`

- **Meaning**: Performs evaluation on top of Engine output: `rule_based`, `agent_judge`, `script`, producing results aligned with `grading.json`.
- **Relation to design**: Corresponds to the "Judge Runner" and the three evaluation types.

### `internal/report/`

- **Meaning**: Aggregates case results and generates JSON, JUnit, HTML, and other reports.
- **Relation to design**: Corresponds to the "Report Generator" and `report.formats`.

### `internal/runner/`

- **Meaning**: Stitches together config loading, runtime preparation, Skill installation, MCP, per-case execution, and reporting; one of the main orchestration entry points for the CLI `run` command (the final implementation is authoritative).
- **Relation to design**: Corresponds to the orchestration and case loop in the overall execution flow.

### `internal/mcp/`

- **Meaning**: Prepares MCP (mock or real) according to configuration, used by Skills that depend on MCP during evaluation.
- **Relation to design**: Corresponds to the "MCP Provisioner" in the flow.

### `internal/skill/`

- **Meaning**: Copies Skill files to the installation path according to each Engine's convention, ensuring `evals/` is excluded.
- **Relation to design**: Corresponds to the "Skill Installer".

### `pkg/transcript/`

- **Meaning**: A **publicly reusable** toolkit for transcript parsing and querying; other Go projects that need to be compatible with the same format may depend on this package.
- **Difference from `internal`**: `internal/` does not expose a stable API; packages in `pkg/` are intended to be importable, versionable library boundaries.

### `docs/`

- **Meaning**: VitePress-powered documentation site (English `guide/`, Chinese `zh/guide/`, legacy `user-manual/`); built and deployed to GitHub Pages by `.github/workflows/docs.yml`. Not part of `go build`.
- **Maintenance advice**: User-visible behavior changes should be reflected in the relevant pages under `docs/guide/` and `docs/zh/guide/` (and the legacy `docs/user-manual/` where applicable) in the same commit.

### `.github/workflows/`

- **Meaning**: Continuous integration (e.g. `go test`, `go vet`, build) and tag-triggered release pipelines.
- **Convention**: When changing release behavior, also check `.goreleaser.yaml`.

## Engineering Constraints

### Commit Message (Conventional Commits)

**Requirement**: The first line (subject) must conform to [Conventional Commits](https://www.conventionalcommits.org/), in the form:

```text
<type>(<optional-scope>): <description>
```

**Allowed types**: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`.

**Examples**:

- `feat(cli): add validate command`
- `fix(config): correct default timeout`
- `docs: update CONTRIBUTING`

**Exceptions**: Git-generated `Merge …` and `Revert …` commits are not validated.

The repository uses `.githooks/commit-msg` to reject non-conforming commits locally; run `make hooks` once after cloning to enable it (see below).

### Code Formatting and Static Analysis

| Tool            | Purpose                                                              |
| --------------- | -------------------------------------------------------------------- |
| `gofmt`         | Official Go formatting; `make fmt` formats automatically, `make fmt-check` only checks without modifying |
| `go vet`        | Standard static analysis                                             |
| `golangci-lint` | Aggregates multiple linters (see `.golangci.yml` at the repo root)   |

**Local checks aligned with CI**:

```bash
make verify    # fmt-check + vet + golangci-lint (matches CI gating)
```

Install **golangci-lint** (the version must match CI; currently CI uses **v2.11.4**, see `GOLANGCI_LINT_VERSION` in the `Makefile`):

- [Installation guide](https://golangci-lint.run/welcome/install/) (Homebrew, binary, `go install`, etc.)
- Example with `go install`: `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4`
- Or run `make lint-tools` to let the Makefile install the tool at the pinned version into `.tools/bin/`.

### Git Hooks (Recommended)

Register this repository's hooks directory with Git once:

```bash
make hooks
```

After enabling:

- **pre-commit**: runs `make verify` (format, `go vet`, golangci-lint)
- **commit-msg**: validates that the first line conforms to Conventional Commits

If `golangci-lint` is not installed, `pre-commit` will fail; install it before committing, or rely on CI to surface issues for fixing later.

## Local Development Tips

- Build and check: `make build`, `make test`, `make fmt`, `make verify` (see `Makefile` for details).
- The module path is `github.com/alibaba/skill-up`; import paths must match.

Contributions via Issues and Pull Requests are welcome. Please make sure `make verify` and tests pass locally or in CI before merging.
