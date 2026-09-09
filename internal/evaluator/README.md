# evaluator

Package `evaluator` provides the evaluation orchestrator for skill-up.

## Table of Contents

- [Module Structure](#module-structure)
- [Core Interfaces](#core-interfaces)
- [EvalResult Type](#evalresult-type)
- [Concurrency Control](#concurrency-control)
- [Runtime Environment](#runtime-environment)
- [Artifact Directory Layout](#artifact-directory-layout)
- [Orchestration Flow](#orchestration-flow)
- [Dependencies](#dependencies)

---

## Module Structure

```
internal/evaluator/
├── evaluator.go   — Evaluator orchestrator + interface definitions + EvalResult type
├── fixtures.go    — FixtureUploader interface + repo / applyDiff upload implementations
└── README.md      — This document
```

**Responsibility split**:

| File | Responsibility |
|---|---|
| `evaluator.go` | Case orchestration, agent execution, expect pre-checks, judge coordination, runtime preparation, session download |
| `fixtures.go` | Fixture resource upload (repo_fixture / apply_diff) |

---

## Core Interfaces

### Evaluator

The main orchestration interface that coordinates the entire evaluation flow:

```go
type Evaluator interface {
    EvaluateAll(ctx context.Context, cases []*config.CaseConfig) ([]EvalResult, error)
}
```

### FixtureUploader

The fixture resource upload interface (`fixtures.go`):

```go
type FixtureUploader interface {
    Upload(ctx context.Context, rt runtime.Runtime, caseCfg *config.CaseConfig, skillDir, fixtureBaseDir string) error
}
```

Implementations:
- `repoFixtureUploader` — uploads a repository template directory
- `applyDiffUploader` — applies a git diff patch

---

## EvalResult Type

The complete evaluation result for a single case:

```go
type EvalResult struct {
    CaseID        string             // Unique case identifier
    CaseName      string             // Case name
    Configuration string             // "with_skill" | "without_skill"
    Status        judge.Status       // PASS | FAIL | SKIP | ERROR
    Prompt        string             // Input prompt
    FinalMessage  string             // Final agent reply
    ExitCode      int                // Agent process exit code
    DurationMs    int64              // Execution duration (milliseconds)
    TurnsExecuted int                // Actual number of turns executed
    TurnsTotal    int                // Planned total number of turns
    InputTokens   int                // Number of input tokens
    OutputTokens  int                // Number of output tokens
    Grading       *judge.Result      // Valid judge result (nil when skipped or failed)
    JudgeSession  *agent.SessionResult // Separate agent_judge session, including failures
    ExpectResult  *judge.ExpectResult // Expect pre-check result (nil means no expect)
    Error         error              // Execution error
}
```

**Deprecated type**: `CaseResult` is deprecated; use `EvalResult` instead.

---

## Concurrency Control

Concurrency for all cases is governed by a **single global concurrency setting**; with_skill and without_skill do not run in independent parallel pools.

```
┌─────────────────────────────────────────────────────┐
│  Evaluator.EvaluateAll                              │
│                                                     │
│  cases × configurations (with_skill + without_skill)│
│  └── all tasks placed into a unified worker pool    │
│       └── controlled by a semaphore, at most         │
│           Concurrency tasks run simultaneously      │
│                                                     │
│  Example: Concurrency=3, 4 cases, withBaseline=true │
│  → at most 3 tasks run in parallel                  │
│  → each case yields up to 2 tasks (with/without)    │
│  → up to 8 tasks in total                           │
└─────────────────────────────────────────────────────┘
```

---

## Runtime Environment

Evaluator creates different Runtime environments for different cases and for with/without skill variants:

```
${runtime.workspace}  (with_skill variant)
├── .../                   # File contents from repo fixtures
├── fixtures/              # Dependency files
├── .claude/skills/        # Installed skill
└── [agent workspace]

${runtime.workspace}  (without_skill variant)
├── .../                   # File contents from repo fixtures
├── fixtures/              # Dependency files
└── [agent workspace]
```

**Differences**:
- The `without_skill` variant's runtime contains no skills
- The `with_skill` variant's runtime contains every skill defined under `skills`

### Fixtures

Environment-dependent data; the fixture files required vary by case:

- `repo_fixtures` — upload files under the target directory into the workspace (i.e. the agent's cwd)
- `judge.script_path` — uploaded before the judge runs, providing data the judge depends on

---

## Artifact Directory Layout

After completion, Evaluator creates the following directory layout locally and saves runtime files via `runtime.DownloadDir`:

```
${skill_dir}/${skill-name-workspace}/iteration-x/case_name/
├── with_skill/
│   └── outputs/          # Agent execution artifacts
└── without_skill/
    └── outputs/          # Agent execution artifacts
```

Artifacts include:
- `stdout.txt` — standard output
- `*.jsonl` — trace files

For `agent_judge`, artifacts live under `outputs/judge/run/`. The first engine
artifact paths remain unchanged and `raw-response-attempt-1.txt` is always
written. When strict output correction is needed, the directory also contains
`raw-response-attempt-2.txt` and a `retry/` subdirectory for the second engine
invocation. Evaluator preserves this layout on both successful corrections and
final Judge errors.

---

## Orchestration Flow

### EvaluateAll Flow

```
EvaluateAll
├── Expand all tasks (case × configuration)
│   ├── withBaseline=false → each case yields 1 task (with_skill)
│   └── withBaseline=true  → each case yields 2 tasks (with_skill + without_skill)
│
├── Unified concurrent execution (semaphore controls Concurrency)
│   └── each task → executeCase (retry wrapper with exponential backoff)
│                        └── executeCaseOnce
│
└── Return []EvalResult
```

### executeCaseOnce (single-case execution pipeline)

```
executeCaseOnce(caseCfg, configName, overrideRT, overrideAgent)
│
├── 1. prepareRuntimeForCase          ← runtime preparation
│   ├── runtime.NewRuntime + Create
│   ├── setupCaseEnvironment
│   │   ├── execute setup_steps
│   │   ├── ag.Install (isolated runtimes only)
│   │   ├── agent.Preflight (binary check + static version observation)
│   │   ├── ag.InstallMCP
│   │   ├── ag.InstallSkill (with_skill only)
│   │   └── fixtureRegistry.UploadAll
│   └── defer rt.Close
│
├── 2. ag.Run(prompt)                 ← agent execution
│   └── → sessionResult
│
├── 3. processSessionResult           ← result collection
│   ├── extract FinalMessage / DurationMs / Turns
│   └── download session file (if any)
│
├── 4. resolveExpectConfig + CheckExpect  ← expect pre-check
│   ├── failure → grading = NewResult + StatusFail → return immediately
│   └── pass → continue
│
├── 5. judge.NewJudge + j.Evaluate    ← judge scoring
│   └── → grading
│
└── 6. assemble EvalResult → return
```

---

## Dependencies

```
runner.go
  └── evaluator.NewEvaluator → Evaluator
          └── defaultEvaluator
                  ├── SkillName / SkillDir / Iteration
                  ├── evalCfg  *config.EvalConfig
                  ├── loader   *config.Loader
                  ├── resolver *credential.Resolver
                  ├── ag       agent.Agent
                  └── fixtures *fixtureRegistry

  External interfaces evaluator depends on:
  ├── agent.Agent     — Run / InstallMCP / InstallSkill
  ├── runtime.Runtime — Create / Close / Exec / UploadFile / UploadDir / DownloadFile
  └── judge.Judge     — Evaluate
```
