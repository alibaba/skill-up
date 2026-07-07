---
title: agent_judge Judge-Specific Skill Support
authors:
  - "kongtang"
creation-date: 2026-07-07
last-updated: 2026-07-07
status: draft
---

# SUP-0002: agent_judge Judge-Specific Skill Support

Language: English | [中文](zh/0002-agent-judge-specific-skill.md)

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Requirements](#requirements)
- [Proposal](#proposal)
  - [User Scenario Quick Reference](#user-scenario-quick-reference)
  - [Notes, Constraints, and Caveats](#notes-constraints-and-caveats)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
  - [Configuration Schema](#configuration-schema)
  - [Configuration Merge Semantics](#configuration-merge-semantics)
  - [Path Resolution and Install Target](#path-resolution-and-install-target)
  - [Agent Installation Adaptation and Progressive Loading](#agent-installation-adaptation-and-progressive-loading)
  - [Mandatory Use Semantics](#mandatory-use-semantics)
  - [Evaluator Execution Flow](#evaluator-execution-flow)
  - [Isolation Semantics](#isolation-semantics)
  - [Report Metadata](#report-metadata)
  - [Documentation and Template Updates](#documentation-and-template-updates)
- [Test Plan](#test-plan)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Infrastructure Needed](#infrastructure-needed)
- [Upgrade & Migration Strategy](#upgrade--migration-strategy)
<!-- /toc -->

## Summary

`agent_judge` can currently describe grading standards only through `judge.criteria`; it cannot install a Skill dedicated to the judge agent. For evaluations that need domain knowledge, reusable rubrics, strict output formats, or long-lived grading guidance, this forces authors to place large amounts of judging logic into every case YAML file. It also bypasses each Agent Engine's native Skill progressive-loading mechanism, increasing the risk of filling the context window with large rubric documents.

This proposal, based on [GitHub issue #134](https://github.com/alibaba/skill-up/issues/134), adds a `judge.skills` configuration so `agent_judge` can install one or more judge-specific Skills through the selected Agent adapter's native Skill installation path. The feature keeps judge Skills isolated from the Skill under test, preserves existing `judge.criteria`, benchmark `with_skill` / `without_skill`, and Agent execution semantics, and records the configured judge Skill information in reports.

## Motivation

`agent_judge` is useful when semantic understanding is required, but today's configuration surface is limited to natural-language criteria, model selection, pass threshold, and timeout. Eval authors commonly run into these problems:

1. **Rubrics are too long for YAML**: domain rules, style guides, scoring details, and negative examples are awkward to maintain inline.
2. **Judging logic needs reuse**: when many evals or cases share the same rubric, duplicated criteria drift over time.
3. **The judge agent needs different context**: the run agent should install the Skill under test, while the judge agent may need a separate Skill that teaches it how to grade.
4. **Judge prompts should evolve independently**: a judge Skill can version and refine its rubric without editing every case file.
5. **Isolation matters**: judge helper Skills must not leak into the run agent, especially in benchmark mode where they would pollute the measured capability.
6. **Progressive loading is the point**: the goal is not to concatenate `SKILL.md`, `references/`, and `assets/` into the judge prompt. The goal is to rely on the Agent's own Skill discovery, selection, and on-demand loading behavior so long rubrics enter context only when needed.
7. **Reports need auditability**: results should say which judge Skills were configured, otherwise reviewers cannot understand the grading basis or reproduce the judge environment.

The current code path exposes the gap:

- `config.JudgeConfig` contains fields such as `type`, `model`, `criteria`, `pass_threshold`, and `timeout_seconds`, but no judge-level Skill reference.
- `defaultEvaluator.setupCaseEnvironment()` installs `evalCfg.Skills` only for the main run agent.
- `agent_judge` later creates or resolves a judge agent in `resolveJudgeAgent()`, wraps it through `judge.NewJudge()`, and `AgentJudge.Evaluate()` calls `judgeAgent.Run()`.
- There is no judge-phase step that calls `judgeAgent.InstallSkill(...)`.

### Goals

1. **Support judge-level Skill configuration**: add a `skills` field to `JudgeConfig`, reusing existing `SkillRef` semantics.
2. **Apply only to `agent_judge`**: `judge.skills` is meaningful only when `judge.type: agent_judge`.
3. **Install into the judge agent**: install `judge.skills` into the judge agent runtime before `AgentJudge.Evaluate()` calls `Run()`.
4. **Keep run and judge agents isolated**: `judge.skills` is not installed into the main run agent, and `eval.skills` is not automatically installed into the judge agent.
5. **Preserve benchmark semantics**: `judge.skills` is installed for both `with_skill` and `without_skill` because it is grading tooling, not the Skill under test.
6. **Remain backward compatible**: existing `agent_judge` configurations behave the same when `judge.skills` is absent.
7. **Preserve native Agent Skill mechanisms**: different Agent Engines may have different Skill directories, manifests, indexes, and discovery mechanisms. skill-up should trigger installation through the Agent adapter's `InstallSkill` abstraction, not inject Skill documents into prompts.
8. **Require judge Skill usage**: when users configure `judge.skills` for `agent_judge`, the judge prompt must explicitly instruct the judge agent to use the installed Skills as authoritative grading guidance. Installation is necessary but not sufficient.
9. **Make reports auditable**: report the judge Skill metadata used for each judged result.
10. **Cover tests and docs**: add coverage for config loading, validation, merging, installation isolation, mandatory usage prompt behavior, report metadata, and documentation examples.

### Non-Goals

1. **No new Skill package manager**: this proposal reuses `SkillRef` / `runtime.SkillConfig` and does not design registry download, version locking, or dependency resolution.
2. **No change to main run Skill installation**: `eval.skills` continues to mean Skills needed by the run agent or the Skill under test.
3. **No rewrite of the `AgentJudge` scoring protocol**: this phase continues to use criteria-driven JSON result parsing.
4. **Judge Skills do not define the criteria list by themselves**: Skills may provide detailed rubrics and constraints, but structured scoring dimensions still come from `judge.criteria`.
5. **No Skill installation for non-`agent_judge` judges**: `rule_based` and `script` judges do not read `judge.skills`.
6. **No prompt-concatenation fallback**: if an Agent adapter cannot install Skills, this proposal does not allow reading Skill files and appending their contents to the judge prompt as a substitute.

## Requirements

### Must Have

| ID  | Requirement | Acceptance Criteria |
| --- | --- | --- |
| R1 | `JudgeConfig` supports `skills` | YAML can declare a `skills` array under `judge:` and load it into config |
| R2 | Only `agent_judge` can use it | `rule_based` / `script` with `judge.skills` fails validation with a clear error |
| R3 | Install before judging | Configured judge Skills are installed before `AgentJudge.Evaluate()` invokes the judge agent |
| R4 | Installation isolation | The run agent receives only `eval.skills`; the judge agent receives `judge.skills` |
| R5 | Benchmark does not suppress judge Skills | `without_skill` skips `eval.skills` but still installs `judge.skills` |
| R6 | Consistent path resolution | Local judge Skill paths resolve relative to the Skill root, consistent with `eval.skills` |
| R7 | Backward compatibility | Existing configs require no changes and existing `judge.criteria` behavior remains |
| R8 | Native Skill progressive loading | Implementation calls the judge Agent adapter's `InstallSkill`; it must not read Skill docs and concatenate them into the judge prompt |
| R9 | Report judge Skills | JSON/HTML reports show the judge Skill list; JUnit exposes it at least through properties |
| R10 | Mandatory judge Skill use | When `judge.skills` is non-empty, the prompt sent by `AgentJudge` must include a mandatory-use instruction and Skill identifiers |

### Should Have

| ID  | Requirement | Acceptance Criteria |
| --- | --- | --- |
| S1 | Multiple judge Skills | `judge.skills` can declare multiple Skills, installed in configuration order |
| S2 | Case-level override | Case-level `judge.skills` can override eval-level judge configuration |
| S3 | Clear diagnostics | Installation failures include the judge Skill path and judge-phase context |
| S4 | Documentation updates | English/Chinese writing-evals docs and skill-upper references include examples |
| S5 | Document Agent differences | Docs explain that judge Skill installation depends on each adapter's Skill support and does not guarantee identical behavior across Agents |

### Nice to Have

| ID  | Requirement | Acceptance Criteria |
| --- | --- | --- |
| N1 | Richer report metadata | Reports may include judge Skill digest, target, and install status without exposing sensitive absolute paths |
| N2 | Future skill-only judge compatibility | Future work can allow a judge Skill to provide default criteria without breaking this design |

## Proposal

Add `judge.skills`, using the same plural form as top-level `skills`:

```yaml
judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
  skills:
    - source: local_path
      path: evals/fixtures/judge-skill
  criteria:
    - "The answer is correct according to the rubric in the installed judge Skill"
```

High-level flow:

1. Parse `judge.skills` from `eval.yaml` and case YAML.
2. Validate that `judge.skills` is used only with `agent_judge`.
3. Execute the case as usual: prepare runtime, install the run agent, install MCP, and install `eval.skills` for the run agent.
4. Run the main agent and collect `SessionResult`, workspace diff, transcript, and other judge inputs.
5. Enter the judge phase and resolve or create the judge agent.
6. Install merged `judge.skills` into the judge agent through that Agent adapter's `InstallSkill`.
7. When building the judge prompt, if `judge.skills` is non-empty, add an instruction that requires the judge agent to use the installed judge Skills. Do not grade as ordinary criteria-only `agent_judge`.
8. Run the judge agent and parse the structured JSON grading result.
9. Write judge Skill metadata into reports.

```
Case Runtime

setupCaseEnvironment
  - install run agent
  - install MCP
  - install eval.skills --------------+
                                      |
                                Main Run Agent
                                runs Skill under test
                                      |
                            transcript/diff/output
                                      |
judge phase                          |
  - resolve judge agent              |
  - install judge.skills -----+      |
  - AgentJudge.Evaluate       |      |
                              v      v
                         Judge Agent
                         grades with installed
                         judge Skill + criteria
```

### User Scenario Quick Reference

#### Scenario 1: Reusable Domain Rubric

```yaml
schema_version: v1alpha1

skills:
  - source: local_path
    path: .

judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
  skills:
    - source: local_path
      path: evals/fixtures/sql-judge-skill
  criteria:
    - "The SQL change satisfies the safety and compatibility rules defined by the judge Skill"
    - "The grading decision cites concrete evidence rather than generic opinions"
```

The run agent installs the Skill under test. The judge agent installs `sql-judge-skill`, which contains database review rules, counterexamples, and output requirements.

#### Scenario 2: Case-Level Judge Skill Override

```yaml
# evals/eval.yaml
judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
  skills:
    - source: local_path
      path: evals/fixtures/default-judge-skill
  criteria:
    - "Grade output quality according to the default judge Skill"
```

```yaml
# evals/cases/security-review.yaml
judge:
  type: agent_judge
  skills:
    - source: local_path
      path: evals/fixtures/security-judge-skill
  criteria:
    - "Grade whether the answer identifies high-risk issues according to the security judge Skill"
```

When a case declares its own `judge.type`, the case-level judge config is treated as a complete judge strategy: `skills` and `criteria` come from the case, while `model`, `pass_threshold`, and `timeout_seconds` can still inherit from global defaults.

#### Scenario 3: Stable Grading Tooling in Benchmark Mode

```yaml
benchmark:
  enabled: true

skills:
  - source: local_path
    path: .

judge:
  type: agent_judge
  model: anthropic/claude-sonnet-4-6
  skills:
    - source: local_path
      path: evals/fixtures/judge-rubric
  criteria:
    - "Grade whether the output satisfies the acceptance criteria according to judge-rubric"
```

Benchmark execution:

- `with_skill`: the run agent installs `eval.skills`, and the judge agent installs `judge.skills`.
- `without_skill`: the run agent skips `eval.skills`, and the judge agent still installs `judge.skills`.

This compares the effect of the Skill under test, not whether the grading tool exists.

### Notes, Constraints, and Caveats

1. **`criteria` remains required**: in this phase, `judge.criteria` still defines structured scoring dimensions. A judge Skill may contain long rubrics, but YAML keeps at least one criterion so result count and report structure remain deterministic.
2. **Reuse `SkillRef`**: `judge.skills` uses `source`, `path`, and `target`; there is no parallel singular `judge.skill` syntax.
3. **Must rely on Agent Skill mechanisms**: judge Skills are valuable because Agents can discover and load them on demand. Implementation must not read an entire Skill directory into `criteria` or the judge prompt.
4. **Agent installation methods may differ**: Claude Code, Codex, Qoder CLI, and custom Agents may use different Skill locations or indexes. skill-up hands the same `runtime.SkillConfig` to the relevant adapter.
5. **Local paths first**: phase one uses the existing local Skill installation capability. If top-level `skills` later supports registries, `judge.skills` can reuse that path.
6. **Installation failure is ERROR**: failed judge Skill installation means the judge environment is not ready; mark the case as ERROR, not FAIL.
7. **No silent fallback**: if `judge.skills` is configured but cannot be installed, do not continue with an unskilled judge agent.

### Risks and Mitigations

| Risk | Impact | Probability | Mitigation |
| --- | --- | --- | --- |
| Judge Skill accidentally installs into the run agent | Benchmark results are polluted | Medium | Installation code receives only `judgeAgent`; tests record run/judge installs separately |
| `without_skill` skips judge Skills | Baseline cannot be graded by the same rubric | Medium | Judge Skill installation does not depend on `configName` |
| Case/global merge semantics are unclear | Authors cannot predict which judge Skill is used | Medium | Reuse existing full override semantics for case-level `judge.type` and document them |
| `criteria` conflicts with judge Skill rubric | Grading becomes unstable | Medium | Docs recommend stable criteria dimensions and detailed rubrics in Skills |
| Skill path escapes the Skill root | Unexpected local files may be read | Low | Reuse or strengthen path validation so resolved relative paths remain under the Skill root |
| Prompt-concatenation fallback for compatibility | Loses progressive loading and can exhaust context | Medium | Explicitly forbid prompt-concatenation fallback; unsupported adapters return ERROR |
| Skill discovery differs by Agent | Same judge Skill may behave differently across engines | Medium | Keep adapter-level tests and record engine plus judge Skill metadata in reports |
| Reports omit judge Skill info | Grading basis is not auditable | Medium | Add judge Skill metadata to EvalResult/report generation |
| Judge Skill is installed but not used | Grading still follows ordinary criteria, ignoring the user-defined rubric | Medium | `AgentJudge` prompt must require use of installed judge Skills; unit tests assert the prompt and fixtures verify behavior |

## Design Details

### Configuration Schema

Extend `JudgeConfig` in `internal/config/schema.go`:

```go
// JudgeConfig describes the evaluation strategy.
type JudgeConfig struct {
    Type       string     `json:"type"                     yaml:"type"`
    ScriptPath string     `json:"script_path,omitempty"    yaml:"script_path,omitempty"`
    Model      string     `json:"model,omitempty"          yaml:"model,omitempty"`
    Criteria   []string   `json:"criteria,omitempty"       yaml:"criteria,omitempty"`
    Skills     []SkillRef `json:"skills,omitempty"         yaml:"skills,omitempty"`

    PassThreshold  *float64 `json:"pass_threshold,omitempty"  yaml:"pass_threshold,omitempty"`
    TimeoutSeconds *int     `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
    Success        []Rule   `json:"success,omitempty"         yaml:"success,omitempty"`
    Failure        []Rule   `json:"failure,omitempty"         yaml:"failure,omitempty"`
}
```

Validation rules:

1. If `judge.skills` is non-empty, `judge.type` must be `agent_judge`.
2. Each `judge.skills[*].source` should currently be `local_path` or another source already supported by top-level `skills`.
3. For `source: local_path`, `path` is required and must not be blank.
4. `target` is optional and follows top-level `skills[*].target` semantics.
5. `agent_judge` still requires `model` and at least one `criteria` entry unless a later proposal changes the `AgentJudge` protocol.
6. Checks that depend on inherited judge defaults, especially the required `agent_judge` `model`, must run against the effective judge config after `judge.MergeJudgeConfig(global, caseLevel)`. Raw case validation may continue to check case-local constraints, but it must not reject a case-level `agent_judge` only because `model` is inherited from the global judge config.

### Configuration Merge Semantics

Reuse the current `judge.MergeJudgeConfig(global, caseLevel)` behavior:

- If a case does not declare `judge.type`, use the global judge config, including global `judge.skills`.
- If a case declares `judge.type`, treat the case judge config as a full override. `skills`, `criteria`, `success`, `failure`, and `script_path` come from the case.
- `model`, `pass_threshold`, and `timeout_seconds` may continue inheriting from global config because current logic already does this.

No implicit append behavior is added for the new field. A judge Skill usually represents a complete grading context; silently merging global and case-level Skills can create rubric conflicts. If an author needs multiple Skills, the case-level `judge.skills` should explicitly list all of them.

### Path Resolution and Install Target

Local judge Skill path resolution matches top-level `skills`:

```go
skillSourceDir := e.loader.SkillDir()
skillSource := filepath.Join(skillSourceDir, judgeSkillRef.Path)
skillCfg := runtime.SkillConfig{
    Source: skillSource,
    Target: judgeSkillRef.Target,
}
```

Implementation should extract a small shared helper, for example:

```go
func resolveSkillConfig(skillDir string, ref config.SkillRef) runtime.SkillConfig {
    return runtime.SkillConfig{
        Source: filepath.Join(skillDir, ref.Path),
        Target: ref.Target,
    }
}
```

Notes:

- Relative paths are resolved from the Skill root, not the current working directory or `eval.yaml` directory.
- Installation order follows the `judge.skills` array order.
- Error messages should include `judge.skills[i].path`.

### Agent Installation Adaptation and Progressive Loading

Different Agents may have different Skill conventions:

- Claude Code may have its own Skill directory and indexing rules.
- Codex may have its own Skill search, resource discovery, and loading conventions.
- Qoder CLI may use a different install command, target directory, or manifest handling.
- Custom Agents may implement installation through `InstallSkillCmd` or their own protocol.

The evaluator should not understand each Agent's internal Skill layout. It does only three things:

1. Resolve `runtime.SkillConfig{Source, Target}` from config.
2. Call `InstallSkill(ctx, rt, skillCfg)` on the final judge agent at the correct phase.
3. Record judge Skill metadata for reports and diagnostics.

The adapter owns the concrete installation method:

```go
type Agent interface {
    Run(ctx context.Context, rt Runtime, opts ExecOptions, messages []Message) (*SessionResult, error)
    Install(ctx context.Context, rt Runtime) error
    InstallMCP(ctx context.Context, rt Runtime, cfg runtime.MCPConfig) error
    InstallSkill(ctx context.Context, rt Runtime, skillCfg runtime.SkillConfig) error
    CheckCredentials(ctx context.Context) error
    Name() string
}
```

Constraints:

- `InstallSkill` should use the Agent's native Skill installation path: directory layout, index files, manifest handling, cache refresh, or CLI install entrypoint.
- The evaluator must not read judge Skill `SKILL.md`, `references/`, or `assets/`, and must not insert these file contents into the judge prompt.
- If an adapter cannot install Skills, it returns a clear error. When `judge.skills` is configured, do not silently fall back to appending Skill docs to the prompt.
- Custom Agents continue to use the existing `InstallSkillCmd` capability. If no installation capability exists but `judge.skills` is configured, the judge phase fails with ERROR.

This preserves the core value of Skills: the Agent can progressively load relevant instructions and resources during judging instead of consuming the whole context window up front.

### Mandatory Use Semantics

Installing a judge Skill does not guarantee `agent_judge` will use it. To ensure user-defined judge Skills actually participate in grading, `AgentJudge` prompt construction must add a mandatory-use block when `judge.skills` is non-empty, for example:

```text
You MUST use the installed judge Skill(s) listed below as the authoritative
grading rubric before evaluating the case. Do not grade this case using only
the inline criteria. The inline criteria identify the result dimensions, while
the judge Skill(s) define the detailed rubric, constraints, and evidence rules.

Installed judge Skill(s):
- evals/fixtures/judge-skill
```

This block references only Skill identifiers and configured paths; it does not expand Skill file contents. Its purpose is to trigger the Agent's Skill selection mechanism and make grading priority explicit:

1. `judge.skills` provides detailed rubrics, constraints, style guides, and evidence requirements.
2. `judge.criteria` provides structured result dimensions and determines the result count.
3. If they conflict, the prompt should state that the judge Skill rubric is authoritative unless a criterion defines a more specific case-level acceptance condition.

Implementation requirements:

- `AgentJudge.buildPrompt()` or an equivalent function receives resolved judge Skill metadata.
- When `judge.skills` is non-empty, the prompt must include a mandatory-use instruction and a stable identifier for every Skill.
- When `judge.skills` is empty, prompt behavior remains criteria-only for backward compatibility.
- The evaluator does not have to prove which internal file the Agent loaded. It must make the observable input require Skill use, and integration fixtures should prove that a Skill-capable Agent uses the Skill under that instruction.
- Reports should distinguish "configured/installed judge skills" from any future "agent-acknowledged used judge skills". This proposal requires at least the former.

### Evaluator Execution Flow

Add judge Skill installation near `defaultEvaluator.newJudgeForCase()`:

```go
func (e *defaultEvaluator) newJudgeForCase(
    ctx context.Context,
    rt runtime.Runtime,
    judgeCfg config.JudgeConfig,
    runAgent agent.Agent,
) (judge.Judge, error) {
    judgeCfg = resolveJudgeScriptPath(e.judgeScriptBaseDir(), judgeCfg)

    judgeAgent, err := e.resolveJudgeAgent(ctx, judgeCfg, runAgent)
    if err != nil {
        return nil, err
    }

    if err := e.installJudgeSkills(ctx, rt, judgeCfg, judgeAgent); err != nil {
        return nil, err
    }

    j, err := judge.NewJudge(judgeCfg, judgeAgent, rt)
    if err != nil {
        return nil, fmt.Errorf("failed to create judge: %w", err)
    }
    return j, nil
}
```

Core `installJudgeSkills` behavior:

```go
func (e *defaultEvaluator) installJudgeSkills(
    ctx context.Context,
    rt runtime.Runtime,
    judgeCfg config.JudgeConfig,
    judgeAgent agent.Agent,
) error {
    if judgeCfg.Type != "agent_judge" || len(judgeCfg.Skills) == 0 {
        return nil
    }
    if e.loader == nil {
        return errors.New("judge.skills requires a loader to resolve local paths")
    }

    skillDir := e.loader.SkillDir()
    for i, ref := range judgeCfg.Skills {
        skillCfg := resolveSkillConfig(skillDir, ref)
        if err := judgeAgent.InstallSkill(ctx, rt, skillCfg); err != nil {
            return fmt.Errorf("failed to install judge skill judge.skills[%d].path=%q: %w", i, ref.Path, err)
        }
        logging.DebugContextf(ctx, "Evaluator: judge skill installed: %s", filepath.Base(skillCfg.Source))
    }
    return nil
}
```

The installation point is after `resolveJudgeAgent()` and before `judge.NewJudge()` because:

1. The final judge agent is known, so the Skill is not installed into the run agent by mistake.
2. The runtime exists and the main run output/workspace diff has already been prepared.
3. Installation failures can return a clear ERROR before `AgentJudge.Evaluate()` starts.

### Isolation Semantics

| Configuration | Install into run agent | Install into judge agent |
| --- | --- | --- |
| `eval.skills` + `with_skill` | yes | no |
| `eval.skills` + `without_skill` | no | no |
| `judge.skills` + `with_skill` | no | yes |
| `judge.skills` + `without_skill` | no | yes |

If the current implementation ever reuses the `runAgent` instance as the judge agent, installation should still follow logical role boundaries: `eval.skills` installs before the main run, and `judge.skills` installs before judging. For built-in engines, prefer a distinct judge agent for `agent_judge`. If reuse is unavoidable, docs and tests must ensure judge Skill installation cannot affect the already-completed main run phase.

### Report Metadata

Reports should record the judge Skills actually configured for the judging run. Add a non-sensitive structure to case result or grading metadata:

```go
// JudgeSkillInfo describes a judge Skill used during agent_judge evaluation.
type JudgeSkillInfo struct {
    Source string `json:"source,omitempty"` // e.g. local_path
    Path   string `json:"path,omitempty"`   // config path, relative when configured that way
    Target string `json:"target,omitempty"`
    Name   string `json:"name,omitempty"`   // derived from path basename or Skill metadata
}
```

Report expectations:

- JSON report: include `judge_skills` or an equivalent field under each case/config result.
- HTML report: show judge Skill name, configured path, and target in case details.
- JUnit report: expose basic fields through testcase properties such as `judge.skills.count` and `judge.skills.<n>.path`.
- Anthropic `grading.json`: if no compatible extension point exists, it can omit these official grading fields, but skill-up's JSON/HTML reports must retain the metadata.

Security and privacy:

- Record configured relative `path` by default, not expanded local absolute paths.
- Do not record Skill file contents.
- Future digests may help reproduction, but large file contents should not be embedded in reports.

### Documentation and Template Updates

Update:

- `docs/guide/writing-evals.md`: add a `skills` example to the `judge: agent_judge` section and explain isolation.
- `docs/zh/guide/writing-evals.md`: add the Chinese equivalent.
- `skills/skill-upper/references/eval-yaml.md`: document `judge.skills`.
- `skills/skill-upper/references/judge-types.md`: explain when to use judge Skills versus inline criteria.
- Agent or custom-engine docs: explain that judge Skill installation depends on adapter `InstallSkill` capability and will not degrade to prompt concatenation.
- Templates with `agent_judge` examples may include commented examples, but should not enable judge Skills by default because they increase cost and complexity.

## Test Plan

### Unit Tests

1. **Config loading**
   - Eval-level `judge.skills` deserializes into `JudgeConfig.Skills`.
   - Case-level `judge.skills` deserializes.
   - Multiple Skills preserve configuration order.

2. **Config validation**
   - `judge.type: agent_judge` + `judge.skills` passes.
   - `judge.type: rule_based` + `judge.skills` fails.
   - `judge.type: script` + `judge.skills` fails.
   - Blank `judge.skills[*].path` fails.
   - Missing `criteria` for `agent_judge` keeps the existing failure behavior.

3. **Config merge**
   - A case without `judge.type` inherits global `judge.skills`.
   - A case with its own `judge.type` and `judge.skills` does not append global `judge.skills`.
   - A case with `judge.type` but no `model` still inherits global `model`.

4. **Evaluator installation behavior**
   - Mock run agent receives only `eval.skills`.
   - Mock judge agent receives only `judge.skills`.
   - `without_skill` skips `eval.skills` but installs `judge.skills`.
   - Judge Skill install failure marks the case ERROR and does not invoke judge agent `Run()`.
   - Unsupported adapter + configured `judge.skills` returns ERROR and does not concatenate Skill docs into the prompt.

5. **Mandatory-use prompt**
   - Non-empty `judge.skills` makes the `AgentJudge` prompt include a mandatory-use instruction.
   - The prompt includes a stable identifier or configured path for each judge Skill.
   - The prompt does not include judge Skill file contents.
   - Empty `judge.skills` preserves existing criteria-only behavior.

6. **Path resolution**
   - Relative paths resolve from the Skill root.
   - `target` passes through to `runtime.SkillConfig.Target`.

7. **Report metadata**
   - JSON report contains the judge Skill list.
   - HTML report displays judge Skill name and configured path.
   - JUnit properties include judge Skill count and path.
   - Reports do not contain Skill file contents or local absolute paths.

### Integration / E2E-Style Fixture

Add a fixture such as:

```text
e2e/testdata/agent-judge-skill/
  SKILL.md
  evals/
    eval.yaml
    cases/
      uses-judge-skill.yaml
    fixtures/
      judge-skill/
        SKILL.md
```

Use a controlled mock/custom agent to verify:

- The run agent does not read the judge Skill.
- The judge prompt explicitly requires use of the installed judge Skill.
- The judge agent reads the judge Skill through its Skill mechanism, not from prompt body text.
- Missing judge Skill fails or errors the same case, proving installation matters.
- The mock/custom agent relies on its own Skill discovery mechanism; tests do not pass because `SKILL.md` was appended to the prompt.
- Generated reports contain judge Skill metadata.

## Drawbacks

1. **Configuration surface grows**: `judge.skills` needs clear docs explaining how it differs from top-level `skills`.
2. **Reviewers must inspect Skill files**: rubrics move from YAML into Skill files, so review requires looking at both.
3. **Additional install cost**: each `agent_judge` may perform one or more extra Skill installs.
4. **`criteria` still remains**: this phase does not fully implement "Skill defines all scoring dimensions", so users may still need a short criterion.
5. **Cross-Agent behavior may differ**: Skill discovery and loading are inherently Agent-specific.
6. **Reports need extension work**: JSON/HTML/JUnit report paths need to carry judge Skill metadata.

## Alternatives

### Alternative A: Singular `judge.skill`

```yaml
judge:
  type: agent_judge
  skill:
    source: local_path
    path: evals/fixtures/judge-skill
```

This is concise for one Skill, but top-level config already uses `skills`. Adding a singular form creates parallel semantics and a future migration when multiple judge Skills are needed. Not chosen.

### Alternative B: Put Judge Skill in Top-Level `skills`

```yaml
skills:
  - source: local_path
    path: .
  - source: local_path
    path: evals/fixtures/judge-skill
```

This avoids schema changes, but installs the judge Skill into the run agent, polluting the measured capability and breaking benchmark interpretation. Not chosen.

### Alternative C: External Markdown Criteria File

```yaml
judge:
  type: agent_judge
  criteria_file: evals/fixtures/rubric.md
```

This is simpler, but it cannot use Agent Skill conventions, directory structure, resources, installation, or progressive loading. If implemented by reading Markdown and appending it to the prompt, it places the long rubric into context all at once, which contradicts the core motivation for using Skills. Not chosen.

### Alternative C2: Prompt-Concatenation Fallback on Install Failure

This appears compatible, but degrades Skill behavior into an ordinary long prompt:

- It loses on-demand loading.
- It can fill context with `references/` and asset descriptions.
- It cannot validate that the judge Skill works through the real Agent Skill mechanism.
- Reports cannot cleanly distinguish "installed Skill" from "concatenated documents".

This fallback is explicitly not chosen.

### Alternative D: Let Judge Skill Replace `criteria`

```yaml
judge:
  type: agent_judge
  skills:
    - source: local_path
      path: evals/fixtures/judge-skill
```

This is a useful future direction, but it requires changing the `AgentJudge` prompt and JSON result parser because current report structure validates the number of results against `criteria`. This proposal keeps `criteria` required to reduce implementation risk.

## Infrastructure Needed

No external service or third-party dependency is required. Implementation reuses existing config loading, runtime Skill installation, Agent interfaces, and test infrastructure.

Useful local test assets:

- A judge Skill fixture.
- A mock judge agent that records `InstallSkill` calls.
- A custom/mock agent fixture for integration-style verification.

## Upgrade & Migration Strategy

This change is backward compatible:

- Existing `agent_judge` configs require no changes.
- Behavior is unchanged when `judge.skills` is absent.
- Top-level `skills` semantics remain unchanged.
- Benchmark output semantics remain unchanged; the judge side can now have stable rubric tooling.

Recommended migration:

1. Move long judge rubrics into `evals/fixtures/<name>-judge-skill/SKILL.md`.
2. Reference that path from `judge.skills`.
3. Reduce `judge.criteria` to stable scoring dimensions, for example "judge according to the installed judge Skill's safety rules".
4. Run `skill-up validate` and a small eval to confirm the judge agent can discover and use the judge Skill.
