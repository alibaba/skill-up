---
title: Multi-Turn Conversation Evaluation Support
authors:
  - "kongtang"
creation-date: 2026-05-19
last-updated: 2026-05-19
status: provisional
---

# SUP-0001: Multi-Turn Conversation Evaluation Support

Language: English | [中文](zh/0001-multi-turn-conversation-eval.md)

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Requirements](#requirements)
- [Proposal](#proposal)
  - [User Scenario Quick Reference](#user-scenario-quick-reference)
  - [Notes/Constraints/Caveats](#notesconstraintscaveats)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
  - [Schema Changes](#schema-changes)
  - [Evaluator Multi-Turn Execution Engine](#evaluator-multi-turn-execution-engine)
  - [Agent Interface Extension](#agent-interface-extension)
  - [Judge Per-Turn Assertions](#judge-per-turn-assertions)
  - [Reliability Mechanisms](#reliability-mechanisms)
- [Test Plan](#test-plan)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
  - [Alternative D: Scenario-Driven ChatterAgent Mode (Future Extension)](#alternative-d-scenario-driven-chatteragent-mode-future-extension)
- [Infrastructure Needed](#infrastructure-needed)
- [Upgrade & Migration Strategy](#upgrade--migration-strategy)
<!-- /toc -->

## Summary

Although skill-up currently defines `input.turns` and `Turn` (including `PostCondition`) at the schema level, the evaluator in practice concatenates all turns into a single instruction and sends it to the Agent Engine in one shot. **There is no actual turn-by-turn interaction, intermediate assertions, or conditional branching — the core mechanisms of multi-turn conversation evaluation are missing.** This proposal designs and implements full multi-turn conversation evaluation capabilities, enabling skill-up to verify phase gating, double confirmation, information clarification, iterative refinement, and cross-turn state reference — Skill behaviors that can only be demonstrated through multi-turn interactions.

## Motivation

Many Agent Skills' core value can only be demonstrated through multi-turn interactions; single-turn tests cannot cover them. Specific problems include:

1. **Phase gating cannot be verified**: For workflow Skills like SDD-RIPER, one needs to first start a task normally and then attempt to skip a phase, verifying whether the Skill's "guardrails" are effective
2. **Double confirmation flows are untestable**: Dangerous operations (file deletion, production deployment) have "ask → confirm → execute" and "ask → reject → cancel" paths that require at least two turns of interaction
3. **Information clarification behavior is missing**: When parameters are incomplete, the Skill should ask clarifying questions rather than guess. This requires "clarify → provide → execute" multi-turn verification
4. **Iterative refinement cannot be evaluated**: Code generation Skills need incremental modifications based on previous output, which single-turn tests cannot simulate
5. **Cross-turn state reference is missing**: Creating a resource in the first turn and operating on it in the second turn requires verifying that the Skill correctly maintains context

**Specific problems in the current codebase**:
- In `internal/evaluator/evaluator.go`, `buildCaseMessages()` builds all turns into messages and passes them to `agent.Run()` in one shot
- In `internal/agent/agent.go`, `BuildInstructionFromMessages()` concatenates all user messages into a single string
- All Agent implementations (claude_code, codex, qodercli) call `BuildInstructionFromMessages()` for one-shot execution
- `PostCondition` is defined in the schema but has no checking logic in the evaluator
- The `rule_based` Judge only supports global assertions, not per-turn assertions

### Goals

1. **Turn-by-turn execution**: The evaluator invokes the Agent for each turn, checks `post_condition` after each turn completes, then decides whether to proceed to the next turn
2. **Session continuity**: Multi-turn interactions within the same eval case share the Agent session context, rather than starting a new session for each turn
3. **Intermediate assertions**: `post_condition` executes after each turn completes, supporting `skip_remaining` (skip subsequent turns) and `fail` (immediate failure)
4. **Per-turn Judge assertions**: The rule_based Judge adds `turn_response_contains` / `turn_response_not_contains` rules
5. **Dynamic value capture**: `capture` extracts values from a turn's output for use in subsequent turns' prompts via template variables
6. **Backward compatibility**: The single-turn `input.prompt` mode is unaffected; existing cases require no modifications

### Non-Goals

1. **Agent Engine protocol modification**: No changes to the underlying communication protocols of claude_code / codex / qodercli; multi-turn is achieved through existing session resume mechanisms like `--resume`, `codex exec resume`, and qodercli `-r <session-id>`
2. **Parallel turn execution**: Turns are strictly sequential; parallelism is not supported
3. **Automated conversation tree/branch testing**: This phase only supports linear multi-turn sequences, not conditional branches forming conversation trees
4. **Agent-side streaming real-time assertions**: Assertions execute only after each turn completes, not during streaming output
5. **Dynamic content generation**: All turns' content must be pre-defined in YAML; runtime generation by LLM or script is not supported (`capture` + `{{variable}}` template variables provide limited dynamic value referencing, but the prompt structure itself is deterministic)
6. **Scenario-driven simulated-user mode**: A ChatterAgent-style mode that adaptively generates user turns from an objective is valuable for open-ended business workflows, but is intentionally deferred to a later proposal so this phase can deliver deterministic, reproducible scripted-turn evaluation first

## Requirements

### Must Have

| ID  | Requirement               | Acceptance Criteria                                                                                                               |
| --- | ------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| R1  | Turn-by-turn execution    | The evaluator invokes the Agent separately for each turn in `input.turns`, collecting the response after each turn                |
| R2  | Session continuity        | Multi-turn interactions within the same case share the Agent session context; the Agent can reference content from previous turns |
| R3  | post_condition check      | `post_condition` is evaluated after each turn; `on_fail: skip_remaining` skips subsequent turns                                   |
| R4  | Per-turn Judge assertions | `turn_response_contains` / `turn_response_not_contains` assertions support specifying the turn number                             |
| R5  | Backward compatibility    | Existing `input.prompt` single-turn cases are unaffected and require no modifications                                             |
| R6  | Transcript completeness   | The complete transcript of multi-turn interactions records all turns, with each message annotated with its turn number            |

### Should Have

| ID  | Requirement                    | Acceptance Criteria                                                                                                       |
| --- | ------------------------------ | ------------------------------------------------------------------------------------------------------------------------- |
| S1  | Capture value extraction       | Extract values from a turn's response via regex or JSONPath, available as template variables in subsequent turns' prompts |
| S2  | Per-turn timeout               | Each turn can have its own timeout, independent of the case-level timeout                                                 |
| S3  | Per-turn tool_called assertion | `tool_called_in_turn` assertion can specify which turn to check for tool invocations                                      |

### Nice to Have

| ID  | Requirement               | Acceptance Criteria                                   |
| --- | ------------------------- | ----------------------------------------------------- |
| N1  | Retry mechanism extension | `retry_on` adds a new `turn_precondition_fail` option |
| N2  | Per-turn agent_judge      | Use LLM-as-Judge to evaluate a specific turn's output |

## Proposal

### User Scenario Quick Reference

Before diving into the technical design, here are three typical scenarios demonstrating how multi-turn conversation evaluation is configured in practice, helping readers quickly build intuition.

---

#### Scenario 1: Phase Gating — Skill Should Reject When User Attempts to Skip a Phase

**Test objective**: The SDD-RIPER workflow Skill should reject and guide the user to follow the correct order when the user requests to skip the Research phase.

```yaml
# cases/phase-gate.yaml
id: phase-gate-enforcement
title: Skill should reject when user attempts to skip a phase

input:
  turns:
    # Turn 1: Start the task normally; Agent should enter the Research phase
    - role: user
      content: "sdd_bootstrap: task=implement user login"
      post_condition:
        must_contain_any: ["Research", "analyze", "understand requirements"]
        on_fail: skip_remaining   # Agent didn't enter Research → skip subsequent turns (scenario doesn't apply)

    # Turn 2: Attempt to skip; Agent should reject
    - role: user
      content: "Skip the Research phase and write the code directly"

judge:
  type: rule_based
  success:
    - turn_response_contains:      # Assert turn 2 response contains rejection keywords
        turn: 2
        contains_any: ["need to complete first", "cannot skip", "execute in order"]
  failure:
    - turn_response_contains:      # Code appears in turn 2 → gating failed
        turn: 2
        contains_any: ["```python", "```java", "def ", "class "]
```

**Key points**:
- `post_condition` checks after turn 1 whether the Agent entered the expected phase; if not, subsequent turns are skipped
- `turn_response_contains` asserts specifically on the Agent's turn 2 response
- `judge.failure` is optional and is used for explicit negative evidence. It is evaluated before `judge.success`; if any failure rule matches, the case fails immediately. If no failure rule matches, all success rules must pass. Therefore, when a response matches neither the success nor failure criteria, the case fails because the required success evidence is missing.

---

#### Scenario 2: Double Confirmation — Confirm/Reject Paths for Dangerous Operations

**Test objective**: The file deletion Skill should ask for confirmation before executing; only execute after user confirms.

```yaml
# cases/delete-confirm.yaml
id: delete-with-confirmation
title: File deletion requires double confirmation

input:
  turns:
    # Turn 1: Issue a delete request
    - role: user
      content: "Delete all log files under /tmp/data/"
      post_condition:
        must_contain_any: ["confirm", "sure", "proceed", "delete"]
        on_fail: fail              # Agent deleted without asking → test fails

    # Turn 2: User confirms
    - role: user
      content: "Yes, confirm deletion"

judge:
  type: rule_based
  success:
    - turn_response_contains:
        turn: 1
        contains_any: ["confirm", "sure", "proceed"]   # Turn 1 should ask for confirmation
    - turn_response_contains:
        turn: 2
        contains_any: ["deleted", "done", "removed"]   # Turn 2 should execute deletion
```

**Key points**:
- `on_fail: fail` means if the Agent doesn't ask for confirmation in turn 1, the entire case is immediately marked as failed
- Two `turn_response_contains` assert behaviors in different turns respectively

---

#### Scenario 3: Cross-Turn State Reference — Resource ID Created in Turn 1 Used in Turn 2

**Test objective**: After the Agent creates a database table, the user references that table name to insert data; the Agent should correctly reference it.

```yaml
# cases/cross-turn-reference.yaml
id: cross-turn-table-reference
title: Cross-turn reference — operate using the table name created in the previous turn

input:
  turns:
    # Turn 1: Create table
    - role: user
      content: "Create a users table with id, name, and email fields"
      post_condition:
        must_contain_any: ["CREATE TABLE", "create table"]
        on_fail: fail
      capture:
        - variable: table_name              # Extract table name from Agent response
          pattern: "(?i)CREATE TABLE\\s+(?P<value>\\w+)"

    # Turn 2: Use {{table_name}} to reference the extracted table name from the previous turn
    - role: user
      content: "Insert a test record into the {{table_name}} table"

judge:
  type: rule_based
  success:
    - turn_response_contains:
        turn: 2
        contains_any: ["INSERT INTO"]
```

**Key points**:
- `capture` extracts a value from the Agent response via regex and stores it in the variable `table_name`
- Turn 2's `content` uses the `{{table_name}}` template syntax to reference this value, which is automatically replaced at runtime with the actually extracted table name
- This means the eval case doesn't need to know in advance what name the Agent will give the table

---

> **Summary**: The core configuration pattern of multi-turn conversation evaluation is `input.turns` (define prompts per turn) + `post_condition` (inter-turn assertions) + `capture`/`{{variable}}` (cross-turn value passing) + `turn_response_contains` (per-turn Judge assertions). All turns' content is pre-defined static text (with template variable substitution), ensuring fully reproducible evaluation results.

### Core Approach

Change the evaluator's case execution mode from "send all messages in one shot" to "iterative turn-by-turn execution." For each turn:

1. Build the current turn's user message (`content` field + `{{variable}}` template substitution)
2. Invoke the Agent (using session resume to maintain context)
3. Collect the Agent response
4. Execute `post_condition` check
5. If passed, optionally execute `capture` to extract values
6. Inject extracted values into the next turn's prompt template
7. Proceed to the next turn or terminate

```
┌─────────────────────────────────────────────────┐
│                  Case Execution                  │
│                                                  │
│   Turn 1          Turn 2          Turn N         │
│  ┌──────┐       ┌──────┐       ┌──────┐         │
│  │Prompt│──────▶│Prompt│──────▶│Prompt│         │
│  └──┬───┘       └──┬───┘       └──┬───┘         │
│     │              │              │              │
│     ▼              ▼              ▼              │
│  ┌──────┐       ┌──────┐       ┌──────┐         │
│  │Agent │       │Agent │       │Agent │         │
│  │ Run  │       │Resume│       │Resume│         │
│  └──┬───┘       └──┬───┘       └──┬───┘         │
│     │              │              │              │
│     ▼              ▼              ▼              │
│  ┌──────┐       ┌──────┐       ┌──────┐         │
│  │Post  │       │Post  │       │ (no  │         │
│  │Cond  │       │Cond  │       │check)│         │
│  └──┬───┘       └──┬───┘       └──┬───┘         │
│     │              │              │              │
│     ▼              ▼              ▼              │
│  Capture?       Capture?       ──────────┐      │
│     │              │                      │      │
│     ▼              ▼                      ▼      │
│  ┌───────────────────────────────────────────┐   │
│  │       Judge (global + per-turn assertions) │   │
│  └───────────────────────────────────────────┘   │
└─────────────────────────────────────────────────┘
```

### Agent Session Resume Mechanism

The key challenge of multi-turn evaluation is how to maintain the Agent's session context across multiple invocations. Survey of session resume capabilities across Agent Engines:

| Engine      | Resume Method                    | Programmatic Command                                    | Verification Status                                                                                       |
| ----------- | -------------------------------- | ------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| claude_code | `--resume <session-id>` + `-p`   | `claude --resume <id> -p "follow-up"`                   | ✅ Confirmed by [official docs](https://code.claude.com/docs/en/cli-reference)                             |
| codex       | `codex exec resume <session-id>` | `codex exec resume <id> "follow-up"`                    | ✅ Confirmed by [official docs](https://developers.openai.com/codex/cli/features) for non-interactive mode |
| qodercli    | `-r <session-id>` + `-p`         | `qodercli -r <id> -p "follow-up" --output-format=json` | ✅ Supported by latest qodercli non-interactive mode; parse `sessionId` from JSON output                   |

**Session ID Sources**:

| Engine      | Session ID Generation                  | Session ID Storage                                                                              |
| ----------- | -------------------------------------- | ----------------------------------------------------------------------------------------------- |
| claude_code | `uuid.New()` passed via `--session-id` | `claudePrintJSONResult.SessionID` field, parsed from JSON output                                |
| codex       | Auto-generated by codex CLI            | Session file correlated with the current case's initial `Run`; never the global "most recent" file |
| qodercli    | Auto-generated by qodercli             | `sessionId` field in `--output-format=json` output from the initial `qodercli -p` run           |

**Key Design Decisions**:

1. **Session ID retrieval**: claude_code extracts from the `session_id` field in JSON output; qodercli extracts from the `sessionId` field in `--output-format=json`; codex must correlate the session file with the current case's initial `Run` (for example by parsing an emitted session ID, using a case-isolated session directory, or serializing the initial Run + extraction window). It must not pick the global most recent file under `~/.codex/sessions/` because skill-up can execute cases concurrently.
2. **Agent interface extension**: A new optional interface `SessionResumer` (with `RunTurn` method) is added; the evaluator checks capability via type assertion. claude_code, codex, and qodercli should all implement this interface. Engines that don't support it, such as custom or legacy adapters, fall back to single-shot concatenation mode.
3. **Priority**: Phase 1 implements the shared evaluator engine and claude_code/qodercli adapters (both expose parseable session IDs in JSON output); Phase 4 implements codex after its session-file correlation strategy is finalized.

### Notes/Constraints/Caveats

1. **Agent Engine dependency**: Session resume depends on each Agent CLI's resume capability (`--resume`, `codex exec resume`, `-r`). The three built-in engines targeted by this proposal support real multi-turn resume; custom or legacy engines that do not implement `SessionResumer` fall back to "concatenate all turns and send in one shot" mode (existing behavior), with a notation in the report
2. **Model randomness**: Agent responses to the same prompt may vary; `post_condition` matching should use loose mode (`must_contain_any` rather than exact match)
3. **Cost control**: Token consumption in multi-turn interactions is significantly higher than single-turn. Case designs should limit the number of turns (recommended 2-5 turns primarily)

### Risks and Mitigations

| Risk                                                          | Impact                                                | Probability | Mitigation                                                                                             |
| ------------------------------------------------------------- | ----------------------------------------------------- | ----------- | ------------------------------------------------------------------------------------------------------ |
| Custom/legacy Agent Engine doesn't support session resume     | Multi-turn tests degrade to single-shot concatenation | Low         | Detect Engine capability before execution; explicitly annotate execution mode in reports               |
| Overly strict post_condition matching causing excessive SKIPs | Low evaluation effectiveness                          | Medium      | Provide `must_contain_any` (OR semantics); support regex matching; guide users to use loose conditions |
| Multi-turn token consumption triggering rate limits           | Evaluation gets throttled                             | Medium      | Add configurable `turn_delay` between turns; documentation recommends limiting turn count              |
| Session resume failure causing context loss                   | Semantic discontinuity in subsequent turns            | Low         | Mark as ERROR on resume failure with diagnostic info; no silent fallback                               |

## Design Details

### Schema Changes

#### 1. Turn Struct Extension

Existing `Turn` definition (`internal/config/schema.go`):

```go
type Turn struct {
    Role          string         `yaml:"role"`
    Content       string         `yaml:"content"`
    PostCondition *PostCondition `yaml:"post_condition,omitempty"`
}

type PostCondition struct {
    MustContainAny []string `yaml:"must_contain_any,omitempty"`
    OnFail         string   `yaml:"on_fail,omitempty"`
}
```

Extended version:

```go
// Turn is a single conversation turn in a multi-turn evaluation case.
type Turn struct {
    Role           string         `yaml:"role"`                       // user (required)
    Content        string         `yaml:"content"`                    // prompt text, supports {{variable}} template
    PostCondition  *PostCondition `yaml:"post_condition,omitempty"`
    Capture        []CaptureRule  `yaml:"capture,omitempty"`
    TimeoutSeconds int            `yaml:"timeout_seconds,omitempty"`  // per-turn timeout override
}

// PostCondition checks the agent response after a turn completes.
type PostCondition struct {
    MustContainAny []string `yaml:"must_contain_any,omitempty"` // OR: at least one must match
    MustContainAll []string `yaml:"must_contain_all,omitempty"` // AND: all must match
    MustNotContain []string `yaml:"must_not_contain,omitempty"` // NONE: none should match
    OnFail         string   `yaml:"on_fail,omitempty"`          // skip_remaining | fail (default: fail)
}

// CaptureRule extracts a value from the agent response for use in subsequent turns.
type CaptureRule struct {
    Variable string `yaml:"variable"`           // template variable name (e.g. "plan_id")
    Pattern  string `yaml:"pattern,omitempty"`   // regex with named group (?P<value>...)
    JSONPath string `yaml:"jsonpath,omitempty"`  // JSONPath expression (e.g. "$.transcript.tool_results[0].call_id")
}
```

#### 2. Rule Extension (Per-Turn Assertions)

Extending the existing `Rule` definition with new assertion types:

```go
type Rule struct {
    // Existing fields
    OutputContains *OutputContainsRule `json:"output_contains,omitempty" yaml:"output_contains,omitempty"`
    ExitCode       *int                `json:"exit_code,omitempty"       yaml:"exit_code,omitempty"`
    ToolCalled     *ToolCalledRule     `json:"tool_called,omitempty"     yaml:"tool_called,omitempty"`
    FilesExist     []string            `json:"files_exist,omitempty"     yaml:"files_exist,omitempty"`
    FilesNotExist  []string            `json:"files_not_exist,omitempty" yaml:"files_not_exist,omitempty"`

    // New: per-turn assertions
    TurnResponseContains    *TurnResponseContainsRule    `json:"turn_response_contains,omitempty"     yaml:"turn_response_contains,omitempty"`
    TurnResponseNotContains *TurnResponseNotContainsRule `json:"turn_response_not_contains,omitempty" yaml:"turn_response_not_contains,omitempty"`
    ToolCalledInTurn        *ToolCalledInTurnRule        `json:"tool_called_in_turn,omitempty"        yaml:"tool_called_in_turn,omitempty"`
    ToolNotCalledInTurn     *ToolNotCalledInTurnRule     `json:"tool_not_called_in_turn,omitempty"    yaml:"tool_not_called_in_turn,omitempty"`
}

// TurnResponseContainsRule checks if a specific turn's response contains expected text.
type TurnResponseContainsRule struct {
    Turn        int      `json:"turn"                 yaml:"turn"`                    // 1-indexed turn number
    ContainsAll []string `json:"contains_all,omitempty" yaml:"contains_all,omitempty"` // AND semantics
    ContainsAny []string `json:"contains_any,omitempty" yaml:"contains_any,omitempty"` // OR semantics
}

// TurnResponseNotContainsRule checks if a specific turn's response does NOT contain text.
type TurnResponseNotContainsRule struct {
    Turn        int      `json:"turn"             yaml:"turn"`         // 1-indexed turn number
    NotContains []string `json:"not_contains"     yaml:"not_contains"` // none should match
}

// ToolCalledInTurnRule checks if a tool was called in a specific turn.
type ToolCalledInTurnRule struct {
    Turn int            `json:"turn"           yaml:"turn"`
    Name string         `json:"name"           yaml:"name"`
    Args map[string]any `json:"args,omitempty" yaml:"args,omitempty"`
}

// ToolNotCalledInTurnRule checks that a tool was NOT called in a specific turn.
type ToolNotCalledInTurnRule struct {
    Turn int    `json:"turn" yaml:"turn"`
    Name string `json:"name" yaml:"name"`
}
```

#### 3. YAML Configuration Example

> For more complete user scenarios, see the [User Scenario Quick Reference](#user-scenario-quick-reference) section above. This example demonstrates the combined usage of all schema fields.

```yaml
id: clarification-and-execute
title: Skill should ask for clarification when parameters are incomplete

input:
  turns:
    # Turn 1: Deliberately provide incomplete parameters; expect Agent to ask for clarification
    - role: user
      content: "Deploy the service for me"
      post_condition:
        must_contain_any: ["which environment", "which service", "please specify", "need to know"]
        on_fail: fail
      capture:
        - variable: clarification_question
          pattern: "(?P<value>[^.?]+[?])"

    # Turn 2: After providing parameters, Agent should execute deployment
    - role: user
      content: "Deploy order-service to staging"
      post_condition:
        must_contain_all: ["order-service", "staging"]
        on_fail: fail
      timeout_seconds: 120

    # Turn 3: Confirm deployment result
    - role: user
      content: "What's the deployment result?"

judge:
  type: rule_based
  success:
    - turn_response_contains:
        turn: 1
        contains_any: ["which", "please specify", "need"]
    - turn_response_contains:
        turn: 2
        contains_any: ["deploy", "staging"]
    - turn_response_not_contains:
        turn: 1
        not_contains: ["deployed", "deploy completed"]
    - tool_called_in_turn:
        turn: 2
        name: deploy
```

### Evaluator Multi-Turn Execution Engine

#### Core Change: `executeCaseOnce` Branching

In `internal/evaluator/evaluator.go`, the `executeCaseOnce` method needs to branch based on input type:

The existing method signature remains unchanged; a branch is inserted before the `agent.Run` call:

```go
func (e *defaultEvaluator) executeCaseOnce(ctx context.Context, caseCfg *config.CaseConfig,
    configName string, overrideRT runtime.Runtime, overrideAgent agent.Agent) EvalResult {

    // ── Existing code below, unchanged ──
    // startTime, prompt/turnsTotal, result initialization, runtime preparation, judge config merging ...

    // ── New branch: multi-turn execution path ──
    if len(caseCfg.Input.Turns) > 1 {
        return e.executeMultiTurn(ctx, caseCfg, configName, rt, runAgent, judgeCfg, startTime)
    }

    // ── Existing single-turn execution logic below, completely unchanged ──
    // messages := buildCaseMessages(caseCfg)
    // sessionResult, execErr := runAgent.Run(...)
    // return e.evaluateCaseSession(...)
}
```

**Key note**: The modification strategy here is **minimally invasive** — inserting an `if` branch before the `runAgent.Run()` call in the existing `executeCaseOnce` method, taking the multi-turn path only when `input.turns` has more than one element. All existing single-turn logic (environment setup, artifact collection, expect pre-check, judge evaluation, etc.) remains completely unmodified.

#### Multi-Turn Execution Core Logic

```go
// TurnResult holds the result of a single turn execution.
type TurnResult struct {
    TurnNumber    int                       // 1-indexed
    Content       string                    // the user message sent in this turn
    Response      string                    // agent response text
    Transcript    transcript.Transcript     // this turn's transcript
    SessionResult *agent.SessionResult      // full session result
    Status        TurnStatus                // completed, skipped, failed, error
    SkipReason    string                    // populated when status is skipped
    CapturedVars  map[string]string         // variables captured from this turn
}

type TurnStatus string

const (
    TurnCompleted TurnStatus = "completed"
    TurnSkipped   TurnStatus = "skipped"
    TurnFailed    TurnStatus = "failed"
    TurnError     TurnStatus = "error"
)

func (e *defaultEvaluator) executeMultiTurn(
    ctx context.Context,
    caseCfg *config.CaseConfig,
    configName string,
    rt runtime.Runtime,
    runAgent agent.Agent,
    judgeCfg config.JudgeConfig,
    startTime time.Time,
) EvalResult {
    turnsTotal := len(caseCfg.Input.Turns)

    // Check if the Agent supports session resume
    resumer, supportsResume := runAgent.(agent.SessionResumer)
    if !supportsResume {
        logging.WarnContextf(ctx, "Agent %s does not implement SessionResumer; "+
            "falling back to single-shot execution for multi-turn case %s", runAgent.Name(), caseCfg.ID)
        return e.executeMultiTurnFallback(ctx, caseCfg, configName, rt, runAgent)
    }

    turnResults := e.executeTurnsSequentially(ctx, caseCfg, rt, runAgent, resumer)
    return e.finalizeMultiTurnResult(ctx, caseCfg, configName, rt, judgeCfg, turnResults, startTime)
}

// executeTurnsSequentially runs each turn in sequence, checking post-conditions
// and capturing values between turns.
func (e *defaultEvaluator) executeTurnsSequentially(
    ctx context.Context,
    caseCfg *config.CaseConfig,
    rt runtime.Runtime,
    runAgent agent.Agent,
    resumer agent.SessionResumer,
) []TurnResult {
    turnsTotal := len(caseCfg.Input.Turns)
    capturedVars := make(map[string]string)
    turnResults := make([]TurnResult, 0, turnsTotal)
    var sessionID string
    var codexSessionSnapshotBeforeRun SessionFileSnapshot

    for i, turn := range caseCfg.Input.Turns {
        turnNum := i + 1

        // 1. Template variable substitution
        content, renderErr := renderTemplate(turn.Content, capturedVars)
        if renderErr != nil {
            turnResults = append(turnResults, TurnResult{
                TurnNumber:  turnNum,
                Content:     turn.Content,
                Status:      TurnError,
                SkipReason:  renderErr.Error(),
                CapturedVars: map[string]string{},
            })
            return turnResults
        }

        // 2. Build this turn's message
        message := transcript.Message{
            Role:    transcript.RoleUser,
            Content: content,
            Turn:    turnNum,
        }

        // 3. Set per-turn timeout + invoke Agent
        sessionResult, execErr := func() (*agent.SessionResult, error) {
            turnCtx := ctx
            if turn.TimeoutSeconds > 0 {
                var cancel context.CancelFunc
                turnCtx, cancel = context.WithTimeout(ctx, time.Duration(turn.TimeoutSeconds)*time.Second)
                defer cancel() // cancel executes when the closure returns, not when the outer function ends
            }

            // First turn uses Run to start a new session; subsequent turns use RunTurn to resume
            if turnNum == 1 {
                if runAgent.Name() == "codex" {
                    codexSessionSnapshotBeforeRun = snapshotCodexSessions(turnCtx, rt)
                }
                sr, err := runAgent.Run(turnCtx, rt, agent.ExecOptions{},
                    []transcript.Message{message})
                if sr != nil {
                    sessionID = extractSessionID(turnCtx, rt, runAgent, sr, codexSessionSnapshotBeforeRun)
                }
                return sr, err
            }
            return resumer.RunTurn(turnCtx, rt, agent.ExecOptions{},
                message, sessionID)
        }()

        // 5. Collect this turn's result
        turnResult := TurnResult{
            TurnNumber:   turnNum,
            Content:      content, // record the actual content sent
            CapturedVars: make(map[string]string),
        }
        if sessionResult != nil {
            turnResult.Response = sessionResult.FinalMessage
            turnResult.Transcript = sessionResult.Transcript
            turnResult.SessionResult = sessionResult
        }
        if execErr != nil {
            turnResult.Status = TurnError
            turnResult.SkipReason = execErr.Error()
            turnResults = append(turnResults, turnResult)
            return turnResults // Execution error, terminate subsequent turns
        }
        turnResult.Status = TurnCompleted

        // 6. Execute post_condition check
        if turn.PostCondition != nil {
            passed, reason := checkPostCondition(turn.PostCondition, turnResult.Response)
            if !passed {
                if turn.PostCondition.OnFail == "skip_remaining" {
                    turnResult.Status = TurnSkipped
                    turnResult.SkipReason = reason
                    turnResults = append(turnResults, turnResult)
                    // Mark subsequent turns as skipped
                    for j := turnNum; j < turnsTotal; j++ {
                        turnResults = append(turnResults, TurnResult{
                            TurnNumber: j + 1,
                            Status:     TurnSkipped,
                            SkipReason: fmt.Sprintf("skipped: turn %d post_condition failed", turnNum),
                        })
                    }
                    return turnResults
                }
                // default: "fail"
                turnResult.Status = TurnFailed
                turnResult.SkipReason = reason
                turnResults = append(turnResults, turnResult)
                return turnResults
            }
        }

        // 7. Execute capture
        for _, cap := range turn.Capture {
            value, captureErr := extractCapturedValue(cap, turnResult.Response, sessionResult)
            if captureErr != nil {
                turnResult.Status = TurnError
                turnResult.SkipReason = captureErr.Error()
                turnResults = append(turnResults, turnResult)
                return turnResults
            }
            capturedVars[cap.Variable] = value
            turnResult.CapturedVars[cap.Variable] = value
        }

        turnResults = append(turnResults, turnResult)
    }
    return turnResults
}

// finalizeMultiTurnResult constructs the EvalResult from turn results and runs the judge.
func (e *defaultEvaluator) finalizeMultiTurnResult(
    ctx context.Context,
    caseCfg *config.CaseConfig,
    configName string,
    rt runtime.Runtime,
    judgeCfg config.JudgeConfig,
    turnResults []TurnResult,
    startTime time.Time,
) EvalResult {
    turnsTotal := len(caseCfg.Input.Turns)
    turnsExecuted := countExecutedTurns(turnResults)

    // Merge transcripts from all turns
    var fullTranscript transcript.Transcript
    var lastSessionResult *agent.SessionResult
    for _, tr := range turnResults {
        fullTranscript = append(fullTranscript, tr.Transcript...)
        if tr.SessionResult != nil {
            lastSessionResult = tr.SessionResult
        }
    }

    result := EvalResult{
        CaseID:        caseCfg.ID,
        CaseName:      caseCfg.Title,
        Prompt:        caseCfg.Input.Turns[0].Content,
        SessionResult: lastSessionResult,
        TurnsTotal:    turnsTotal,
        Configuration: configName,
    }
    if result.SessionResult == nil {
        result.SessionResult = &agent.SessionResult{}
    }
    result.SessionResult.Transcript = fullTranscript
    result.SessionResult.Turns = turnsExecuted

    // Check terminal turn states before running the judge.
    // Execution errors are infrastructure errors and must not be reported as SKIP.
    if err := firstTurnError(turnResults); err != nil {
        result.Status = judge.StatusError
        result.Error = err
        return result
    }
    if hasFailedTurn(turnResults) {
        result.Status = judge.StatusFail
        return result
    }
    if allSkipped(turnResults) {
        result.Status = judge.StatusSkip
        return result
    }

    // Execute Judge evaluation (reuses the existing evaluateCaseSession flow)
    //
    // The only difference between multi-turn and single-turn execution is that
    // judgeInput carries TurnResults, enabling per-turn assertions
    // (turn_response_contains, etc.) to work.
    // The rest of the flow (expect pre-check → judge → grading) is identical.
    judgeInput := judge.Input{
        CaseID:         caseCfg.ID,
        Transcript:     fullTranscript,
        FinalMessage:   lastFinalMessage(turnResults),
        ExitCode:       lastExitCode(turnResults),
        WorkspacePath:  rt.Workspace(),
        SkillDir:       e.skillDir,
        TurnsExecuted:  turnsExecuted,
        TurnsTotal:     turnsTotal,
        TurnResults:    toJudgeTurnResults(turnResults),
        WorkspaceDiff:  sessionWorkspaceDiff(lastSessionResult),
        GeneratedFiles: sessionGeneratedFiles(lastSessionResult),
        SessionResult:  lastSessionResult,
    }

    if failed := e.runExpectPreCheck(ctx, caseCfg, configName, judgeInput, turnsTotal, &result); failed {
        return result
    }

    var expectAssertions []judge.AssertionResult
    if result.ExpectResult != nil {
        expectAssertions = result.ExpectResult.ToAssertionResults()
    }

    finalResult := e.runJudgePhase(ctx, rt, caseCfg, configName, judgeCfg, turnsTotal, nil, judgeInput, &result)
    if len(expectAssertions) > 0 && finalResult.Grading != nil {
        finalResult.Grading.AssertionResults = append(expectAssertions, finalResult.Grading.AssertionResults...)
        finalResult.Grading.Summary.Passed += len(expectAssertions)
        finalResult.Grading.Summary.Total += len(expectAssertions)
        if finalResult.Grading.Summary.Total > 0 {
            finalResult.Grading.Summary.PassRate = float64(finalResult.Grading.Summary.Passed) / float64(finalResult.Grading.Summary.Total)
        }
    }

    return finalResult
}
```

#### post_condition Check Implementation

```go
// checkPostCondition evaluates a post-condition against the agent response.
// Returns (passed bool, reason string).
func checkPostCondition(pc *config.PostCondition, response string) (bool, string) {
    lower := strings.ToLower(response)

    // must_contain_all: all must match
    for _, keyword := range pc.MustContainAll {
        if !strings.Contains(lower, strings.ToLower(keyword)) {
            return false, fmt.Sprintf("response missing required keyword: %q", keyword)
        }
    }

    // must_contain_any: at least one must match
    if len(pc.MustContainAny) > 0 {
        found := false
        for _, keyword := range pc.MustContainAny {
            if strings.Contains(lower, strings.ToLower(keyword)) {
                found = true
                break
            }
        }
        if !found {
            return false, fmt.Sprintf("response missing any of: %v", pc.MustContainAny)
        }
    }

    // must_not_contain: none should match
    for _, keyword := range pc.MustNotContain {
        if strings.Contains(lower, strings.ToLower(keyword)) {
            return false, fmt.Sprintf("response unexpectedly contains: %q", keyword)
        }
    }

    return true, ""
}
```

#### Template Rendering Implementation

```go
// renderTemplate replaces {{variable}} placeholders in content with captured values.
// Uses simple string replacement rather than text/template to avoid complexity
// and security risks (no function calls, no control flow).
func renderTemplate(content string, vars map[string]string) (string, error) {
    result := content
    for name, value := range vars {
        result = strings.ReplaceAll(result, "{{"+name+"}}", value)
    }
    unresolved := regexp.MustCompile(`\{\{[a-zA-Z_][a-zA-Z0-9_]*\}\}`).FindAllString(result, -1)
    if len(unresolved) > 0 {
        return "", fmt.Errorf("unresolved template variables: %v", unresolved)
    }
    return result, nil
}
```

#### Capture Value Extraction Implementation

```go
// extractCapturedValue extracts a value from the agent response using the configured method.
// Returns an error when extraction fails so unresolved variables cannot silently
// leak into subsequent turns.
func extractCapturedValue(rule config.CaptureRule, response string, sr *agent.SessionResult) (string, error) {
    // Prefer regex extraction
    if rule.Pattern != "" {
        return extractByRegex(rule.Pattern, response)
    }
    // JSONPath extraction: structure TurnResult as JSON then query
    if rule.JSONPath != "" && sr != nil {
        return extractByJSONPath(rule.JSONPath, response, sr)
    }
    return "", fmt.Errorf("capture %q has no extraction method or no session result", rule.Variable)
}

// extractByRegex extracts a value using a regex with a named group (?P<value>...).
func extractByRegex(pattern, text string) (string, error) {
    re, err := regexp.Compile(pattern)
    if err != nil {
        return "", fmt.Errorf("invalid capture regex: %w", err)
    }
    match := re.FindStringSubmatch(text)
    if match == nil {
        return "", fmt.Errorf("capture regex did not match")
    }
    // Look for the named group "value"
    for i, name := range re.SubexpNames() {
        if name == "value" && i < len(match) {
            if match[i] == "" {
                return "", fmt.Errorf("capture regex matched empty value")
            }
            return match[i], nil
        }
    }
    // Fallback: return the first capturing group
    if len(match) > 1 {
        if match[1] == "" {
            return "", fmt.Errorf("capture regex matched empty value")
        }
        return match[1], nil
    }
    return "", fmt.Errorf("capture regex has no capturing group")
}

// extractByJSONPath extracts a value from the session result using a JSONPath expression.
// The root object $ is a JSON representation of the turn result:
//   {
//     "response": "...",
//     "transcript": { "tool_calls": [...], "tool_results": [...] }
//   }
func extractByJSONPath(path, response string, sr *agent.SessionResult) (string, error) {
    // Build a queryable JSON object from the turn data
    turnData := map[string]any{
        "response": response,
        "transcript": map[string]any{
            "tool_calls":   transcriptToolCalls(sr.Transcript),
            "tool_results": transcriptToolResults(sr.Transcript),
        },
    }
    jsonBytes, err := json.Marshal(turnData)
    if err != nil {
        return "", fmt.Errorf("marshal turn data for JSONPath: %w", err)
    }

    var data any
    if err := json.Unmarshal(jsonBytes, &data); err != nil {
        return "", fmt.Errorf("unmarshal turn data for JSONPath: %w", err)
    }

    // Uses the github.com/PaesslerAG/jsonpath library to query JSON data.
    // Requires adding a new dependency in go.mod: go get github.com/PaesslerAG/jsonpath
    // import "github.com/PaesslerAG/jsonpath"
    result, err := jsonpath.Get(path, data)
    if err != nil {
        return "", fmt.Errorf("JSONPath capture did not match %q: %w", path, err)
    }
    value := fmt.Sprintf("%v", result)
    if value == "" || value == "<nil>" {
        return "", fmt.Errorf("JSONPath capture returned empty value for %q", path)
    }
    return value, nil
}

// transcriptToolCalls extracts tool call info from a transcript.
func transcriptToolCalls(tr transcript.Transcript) []map[string]any {
    var calls []map[string]any
    for _, msg := range tr {
        if msg.Role == transcript.RoleToolCall && msg.ToolCall != nil {
            calls = append(calls, map[string]any{
                "id":        msg.ToolCall.ID,
                "name":      msg.ToolCall.Name,
                "arguments": msg.ToolCall.Arguments,
            })
        }
    }
    return calls
}

// transcriptToolResults extracts tool result info from a transcript.
func transcriptToolResults(tr transcript.Transcript) []map[string]any {
    var results []map[string]any
    for _, msg := range tr {
        if msg.Role == transcript.RoleToolResult && msg.ToolResult != nil {
            results = append(results, map[string]any{
                "call_id": msg.ToolResult.CallID,
                "status":  msg.ToolResult.Status,
                "content": msg.ToolResult.Content,
            })
        }
    }
    return results
}
```

#### Helper Function Implementation

```go
// countExecutedTurns counts the number of turns that were actually executed (not skipped).
func countExecutedTurns(turnResults []TurnResult) int {
    count := 0
    for _, tr := range turnResults {
        if tr.Status == TurnCompleted || tr.Status == TurnFailed || tr.Status == TurnError {
            count++
        }
    }
    return count
}

// firstTurnError returns the first hard execution error from turn execution.
func firstTurnError(turnResults []TurnResult) error {
    for _, tr := range turnResults {
        if tr.Status == TurnError {
            return fmt.Errorf("turn %d error: %s", tr.TurnNumber, tr.SkipReason)
        }
    }
    return nil
}

// hasFailedTurn returns true if any turn has TurnFailed status.
func hasFailedTurn(turnResults []TurnResult) bool {
    for _, tr := range turnResults {
        if tr.Status == TurnFailed {
            return true
        }
    }
    return false
}

// allSkipped returns true if all turns are skipped (no completed turns).
func allSkipped(turnResults []TurnResult) bool {
    for _, tr := range turnResults {
        if tr.Status == TurnCompleted {
            return false
        }
    }
    return true
}

// lastFinalMessage returns the FinalMessage from the last completed turn.
func lastFinalMessage(turnResults []TurnResult) string {
    for i := len(turnResults) - 1; i >= 0; i-- {
        if turnResults[i].Status == TurnCompleted && turnResults[i].Response != "" {
            return turnResults[i].Response
        }
    }
    return ""
}

// lastExitCode returns the ExitCode from the last turn that has a SessionResult.
func lastExitCode(turnResults []TurnResult) int {
    for i := len(turnResults) - 1; i >= 0; i-- {
        if turnResults[i].SessionResult != nil {
            return turnResults[i].SessionResult.ExitCode
        }
    }
    return 0
}

// toJudgeTurnResults converts evaluator TurnResults to judge-visible TurnResults.
func toJudgeTurnResults(turnResults []TurnResult) []judge.TurnResult {
    results := make([]judge.TurnResult, len(turnResults))
    for i, tr := range turnResults {
        results[i] = judge.TurnResult{
            TurnNumber: tr.TurnNumber,
            Response:   tr.Response,
            Transcript: tr.Transcript,
            Status:     string(tr.Status),
        }
    }
    return results
}
```

### Agent Interface Extension

#### New Optional `SessionResumer` Interface

In `internal/agent/agent.go`, **without modifying the existing `Agent` interface**, a new optional interface is added. Agent implementations voluntarily implement it via Go interface composition; the evaluator checks capability via type assertion:

```go
// SessionResumer is an optional interface that Agent implementations may satisfy
// to support multi-turn session resume. The evaluator checks for this interface
// via type assertion before attempting multi-turn execution.
type SessionResumer interface {
    // RunTurn resumes an existing session and sends a follow-up message.
    // sessionID is the session identifier returned by the initial Run call.
    RunTurn(ctx context.Context, rt Runtime, opts ExecOptions, message transcript.Message, sessionID string) (*SessionResult, error)
}
```

Capability check in the evaluator:

```go
resumer, supportsResume := runAgent.(agent.SessionResumer)
if !supportsResume && len(caseCfg.Input.Turns) > 1 {
    // Fall back to single-shot concatenation mode
    logging.WarnContextf(ctx, "Agent %s does not implement SessionResumer; "+
        "falling back to single-shot execution for multi-turn case %s", runAgent.Name(), caseCfg.ID)
}
```

Advantages of this design:
- **Zero breakage**: The existing `Agent` interface is unchanged; all existing implementations compile without modification
- **Incremental adoption**: Only Agents that implement `SessionResumer` take the multi-turn path
- **Idiomatic Go**: Consistent with optional interface patterns in the standard library (e.g., `io.ReadCloser`, `io.WriterTo`)

#### Claude Code Implementation

The claude code CLI already supports `--resume <session-id>` combined with `-p` (print mode) for programmatic session resume. In the current codebase, `buildClaudePrintCmd` already receives a `sessionID` parameter (generated via `uuid.New()`), and the JSON output's `claudePrintJSONResult` struct contains a `SessionID` field.

```go
// ClaudeCodeAgent implements the SessionResumer interface.

// RunTurn resumes a claude-code session with a follow-up message.
func (a *ClaudeCodeAgent) RunTurn(ctx context.Context, rt Runtime, opts ExecOptions,
    message transcript.Message, sessionID string) (*SessionResult, error) {
    start := time.Now()

    envVars := a.credentialEnvVars(credential.EnvAnthropicAPIKey, credential.EnvAnthropicBaseURL)
    envVars["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
    envVars["IS_SANDBOX"] = "1"
    opts = a.mergeExecOptionsEnv(ctx, opts, envVars, nil)

    instruction := message.Content
    cmd := nodeRuntimeCommandWithGuard("claude",
        buildClaudeResumePrintCmd(sessionID, a.effectiveModelName(ctx), instruction))

    result, err := rt.Exec(ctx, cmd, opts)
    sessionResult := a.buildSessionResult(ctx, rt, opts, instruction, start, result)

    // Auth failure check (same logic as in the Run method)
    if authMsg, ok := providerAuthFailureSignal(result, sessionResult); ok {
        if sessionResult != nil && sessionResult.ExitCode == 0 {
            sessionResult.ExitCode = 1
        }
        return sessionResult, fmt.Errorf("claude-code authentication failed: %s", authMsg)
    }
    // Rate limit check (same logic as in the Run method)
    if rateLimitMsg, ok := providerRateLimitSignal(result, sessionResult); ok {
        if sessionResult != nil && sessionResult.ExitCode == 0 {
            sessionResult.ExitCode = 1
        }
        return sessionResult, fmt.Errorf("claude-code provider rate limit: %s", rateLimitMsg)
    }
    if err != nil {
        if sessionResult == nil {
            sessionResult = &SessionResult{
                Engine:     a.Name(),
                ExitCode:   1,
                DurationMs: time.Since(start).Milliseconds(),
                Stderr:     result.Stderr,
                Artifacts:  &SessionArtifacts{},
            }
        }
        return sessionResult, fmt.Errorf("claude-code resume failed: %w", err)
    }
    if result.ExitCode != 0 {
        return sessionResult, fmt.Errorf("claude-code resume failed (exit %d): %s", result.ExitCode, result.Stderr)
    }
    return sessionResult, nil
}

// buildClaudeResumePrintCmd builds the claude command with --resume flag.
// The claude code CLI's --resume parameter directly takes the session ID value (no --session-id needed).
func buildClaudeResumePrintCmd(sessionID, model, instruction string) string {
    cmd := "claude --settings " + shellQuote(`{"disableAllHooks":true}`) +
        " --resume " + shellQuote(sessionID) +
        " -p --permission-mode=bypassPermissions"
    if model != "" {
        cmd += " --model " + shellQuote(model)
    }
    cmd += " " + shellQuote(instruction)
    return cmd
}
```

**Compile-time assertion** (ensures `ClaudeCodeAgent` implements `SessionResumer`):

```go
var _ SessionResumer = (*ClaudeCodeAgent)(nil)
```

#### Codex Implementation

The codex CLI supports `codex exec resume <SESSION_ID>` for non-interactive session resume (confirmed by [official docs](https://developers.openai.com/codex/cli/features)). Session IDs are stored under the `~/.codex/sessions/` directory, but that directory is global to the user account. Because skill-up supports `cases.parallelism`, codex multi-turn support must obtain a session ID with a deterministic association to the current case. Reading the newest file in the global directory is not safe.

```go
// CodexAgent implements the SessionResumer interface.

// RunTurn resumes a codex session with a follow-up message.
func (a *CodexAgent) RunTurn(ctx context.Context, rt Runtime, opts ExecOptions,
    message transcript.Message, sessionID string) (*SessionResult, error) {
    start := time.Now()

    instruction := message.Content
    sandboxFlag := codexBypassSandbox
    if rt.RequiresProcessSandbox() {
        sandboxFlag = codexProcessSandbox
    }
    lastMessagePath := filepath.Join(rt.Workspace(), ".skill-up", "codex-last-message.txt")

    // codex exec resume <SESSION_ID> continues an existing session
    cmd := "mkdir -p " + shellQuote(filepath.Dir(lastMessagePath)) + "\n" +
        nodeRuntimeCommandWithGuard("codex",
            buildCodexResumeCmd(sessionID, instruction, a.effectiveModelName(ctx),
                a.runProviderConfig(ctx), sandboxFlag, lastMessagePath))

    envVars := a.credentialEnvVars(credential.EnvOpenAIAPIKey, credential.EnvOpenAIBaseURL)
    opts = a.mergeExecOptionsEnv(ctx, opts, envVars, a.buildAgentObservabilityAttrs(nil))
    ctx = observability.ContextWithConfiguredAgentSpanAttributes(ctx, opts.Env)

    result, err := rt.Exec(ctx, cmd, opts)
    sessionResult := a.buildSessionResult(ctx, rt, opts, instruction, start, result, lastMessagePath)
    if err != nil {
        if sessionResult == nil {
            sessionResult = &SessionResult{
                Engine:     a.Name(),
                ExitCode:   1,
                DurationMs: time.Since(start).Milliseconds(),
                Stderr:     result.Stderr,
                Artifacts:  &SessionArtifacts{},
            }
        }
        return sessionResult, fmt.Errorf("codex resume failed: %w", err)
    }
    return sessionResult, nil
}

// buildCodexResumeCmd builds the codex command for resuming a session.
func buildCodexResumeCmd(sessionID, instruction, model string, provider codexProviderConfig,
    sandboxFlag, lastMessagePath string) string {
    cmd := "codex exec resume " + shellQuote(sessionID) + " --json --skip-git-repo-check"
    if sandboxFlag != "" {
        cmd += " " + sandboxFlag
    }
    cmd += codexProviderFlags(provider)
    if model != "" {
        cmd += " -m " + shellQuote(model)
    }
    if lastMessagePath != "" {
        cmd += " --output-last-message " + shellQuote(lastMessagePath)
    }
    cmd += " " + shellQuote(instruction)
    return cmd
}

var _ SessionResumer = (*CodexAgent)(nil)
```

**Codex Session ID Extraction**: Unlike claude_code, codex may not return a session_id in its JSON output. The implementation must first prefer a CLI-emitted session ID if available. If it must inspect session files, it has to correlate candidates with the current case's initial `Run` and avoid races with other skill-up cases. Acceptable strategies include a case-isolated codex session directory, a lock around the initial `Run` plus before/after session-file diffing, or marking codex multi-turn unsupported unless `cases.parallelism: 1`.

```go
type SessionFileSnapshot map[string]struct{}

// snapshotCodexSessions records the known codex session files before the
// first turn starts, so extraction can identify files created by this Run.
func snapshotCodexSessions(ctx context.Context, rt Runtime) SessionFileSnapshot {
    // Implementation detail: list ~/.codex/sessions/*.jsonl or a configured
    // case-isolated codex session directory and return their basenames.
    return SessionFileSnapshot{}
}

func diffSessionSnapshots(before, after SessionFileSnapshot) []string {
    var newFiles []string
    for file := range after {
        if _, existed := before[file]; !existed {
            newFiles = append(newFiles, file)
        }
    }
    sort.Strings(newFiles)
    return newFiles
}

// extractCodexSessionID extracts the session ID from a codex SessionResult.
// The implementation must select a file created by this case's initial Run,
// not the global newest file, because other cases may run concurrently.
func extractCodexSessionID(ctx context.Context, rt Runtime, beforeRun SessionFileSnapshot) string {
    afterRun := snapshotCodexSessions(ctx, rt)
    newFiles := diffSessionSnapshots(beforeRun, afterRun)
    if len(newFiles) != 1 {
        return ""
    }
    sessionID := strings.TrimSuffix(newFiles[0], ".jsonl")
    if sessionID == "" {
        return ""
    }
    return sessionID
}
```

#### QoderCLI Implementation

The latest qodercli supports non-interactive session resume with `-p` print mode, `--output-format=json`, and `-r <session-id>`. The initial turn runs in print mode and returns a JSON object containing `sessionId` and `content`; subsequent turns resume the exact session with `-r`. The adapter should use the configured qoder executable name (`qodercli`, `qoder`, or `qoderclicn`) consistently, and should keep the same workspace with `-w <workspace>` when resuming to avoid path drift. qodercli also supports `-c` to continue the most recent session, but skill-up must not use it for evaluation because concurrent cases require exact session identity.

```shell
qodercli -p "first question" --output-format=json -w /path/to/repo > round1.json
SID=$(jq -r .sessionId round1.json)
qodercli -r "$SID" -p "follow-up question" --output-format=json -w /path/to/repo > round2.json
```

In unattended evaluation runs, the adapter should pass qodercli's permission-bypass flag (for example `--yolo` in the latest CLI) or the equivalent supported by the installed qodercli version; otherwise tool calls may pause waiting for interactive confirmation.

```go
type qoderPrintJSONResult struct {
    SessionID string `json:"sessionId"`
    Content   string `json:"content"`
}

func qoderExecutableName(cfg Config) string {
    if cfg.Entry != "" {
        return cfg.Entry
    }
    return "qodercli"
}

// QoderCLIAgent implements the SessionResumer interface.

// RunTurn resumes a qodercli session with a follow-up message.
func (a *QoderCLIAgent) RunTurn(ctx context.Context, rt Runtime, opts ExecOptions,
    message transcript.Message, sessionID string) (*SessionResult, error) {
    start := time.Now()
    instruction := message.Content

    cmd := buildQoderResumePrintCmd(
        qoderExecutableName(a.Cfg),
        sessionID,
        instruction,
        a.effectiveModelName(ctx),
        rt.Workspace(),
    )

    envVars := a.credentialEnvVars("", "")
    opts = a.mergeExecOptionsEnv(ctx, opts, envVars, a.buildAgentObservabilityAttrs(nil))
    ctx = observability.ContextWithConfiguredAgentSpanAttributes(ctx, opts.Env)

    result, err := rt.Exec(ctx, cmd, opts)
    sessionResult := a.buildQoderJSONSessionResult(ctx, rt, opts, instruction, message.Turn, start, result)
    if err != nil {
        if sessionResult == nil {
            sessionResult = &SessionResult{
                Engine:     a.Name(),
                ExitCode:   1,
                DurationMs: time.Since(start).Milliseconds(),
                Stderr:     result.Stderr,
                Artifacts:  &SessionArtifacts{},
            }
        }
        return sessionResult, fmt.Errorf("qodercli resume failed: %w", err)
    }
    if result.ExitCode != 0 {
        return sessionResult, fmt.Errorf("qodercli resume failed (exit %d): %s", result.ExitCode, result.Stderr)
    }
    return sessionResult, nil
}

// buildQoderRunCmd starts a new qodercli session in parseable non-interactive mode.
func buildQoderRunCmd(executable, instruction, model, workspace string) string {
    cmd := shellQuote(executable) + " --yolo --output-format=json"
    if model != "" {
        cmd += " --model " + shellQuote(model)
    }
    if workspace != "" {
        cmd += " -w " + shellQuote(workspace)
    }
    cmd += " -p " + shellQuote(instruction)
    return cmd
}

// buildQoderResumePrintCmd resumes a specific qodercli session.
func buildQoderResumePrintCmd(executable, sessionID, instruction, model, workspace string) string {
    cmd := shellQuote(executable) + " --yolo -r " + shellQuote(sessionID) + " --output-format=json"
    if model != "" {
        cmd += " --model " + shellQuote(model)
    }
    if workspace != "" {
        cmd += " -w " + shellQuote(workspace)
    }
    cmd += " -p " + shellQuote(instruction)
    return cmd
}

// buildQoderJSONSessionResult parses qodercli JSON stdout and preserves the
// sessionId for subsequent turns.
func (a *QoderCLIAgent) buildQoderJSONSessionResult(ctx context.Context, rt Runtime, opts ExecOptions,
    instruction string, turnNumber int, start time.Time, result ExecResult) *SessionResult {
    var payload qoderPrintJSONResult
    _ = json.Unmarshal([]byte(result.Stdout), &payload)

    finalMessage := payload.Content
    if finalMessage == "" {
        finalMessage = result.Stdout
    }

    return &SessionResult{
        Engine:       a.Name(),
        ExitCode:     result.ExitCode,
        DurationMs:   time.Since(start).Milliseconds(),
        Turns:        1,
        FinalMessage: finalMessage,
        Stderr:       result.Stderr,
        SessionID:    payload.SessionID,
        Transcript: transcript.Transcript{
            {Role: transcript.RoleUser, Content: instruction, Turn: turnNumber},
            {Role: transcript.RoleAssistant, Content: finalMessage, Turn: turnNumber},
        },
        Artifacts: &SessionArtifacts{},
    }
}

var _ SessionResumer = (*QoderCLIAgent)(nil)
```

The `extractSessionID` in the evaluator needs to dispatch based on Agent type:

```go
func extractSessionID(ctx context.Context, rt runtime.Runtime, runAgent agent.Agent, sr *agent.SessionResult, codexSessionSnapshotBeforeRun SessionFileSnapshot) string {
    if sr == nil {
        return ""
    }
    // claude_code/qodercli: session ID is stored in SessionResult.SessionID.
    // codex also uses this path when the CLI emits a session ID directly.
    if sr.SessionID != "" {
        return sr.SessionID
    }
    // codex: extract from session filesystem
    if runAgent.Name() == "codex" {
        return extractCodexSessionID(ctx, rt, codexSessionSnapshotBeforeRun)
    }
    return ""
}
```

#### Session ID Extraction

`SessionResult` needs a shared `SessionID` field so the evaluator can resume sessions without knowing each Agent's output shape. claude_code already exposes `session_id` in JSON output, and qodercli exposes `sessionId` when invoked with `--output-format=json`. Both values should be normalized into `SessionResult.SessionID`. codex uses the same field when a CLI-emitted session ID is available; otherwise it falls back to the safe session-file correlation strategy described above.

```go
// SessionResult with new SessionID field:
type SessionResult struct {
    // Existing fields
    Engine       string                `json:"engine,omitempty"`
    Model        string                `json:"model,omitempty"`
    ExitCode     int                   `json:"exit_code"`
    DurationMs   int64                 `json:"duration_ms"`
    Turns        int                   `json:"turns"`
    InputTokens  int                   `json:"input_tokens,omitempty"`
    OutputTokens int                   `json:"output_tokens,omitempty"`
    FinalMessage string                `json:"final_message,omitempty"`
    Stderr       string                `json:"stderr,omitempty"`
    Transcript   transcript.Transcript `json:"transcript,omitempty"`
    Artifacts    *SessionArtifacts     `json:"artifacts,omitempty"`

    // New field
    // SessionID is the agent session identifier, used for multi-turn resume.
    // Populated by agents that support session resume (e.g. claude_code, qodercli, codex).
    SessionID string `json:"session_id,omitempty"`
}
```

In claude_code's `buildClaudePrintJSONSessionResult` and stream-json parsing logic, assign `payload.SessionID` to `SessionResult.SessionID`:

```go
// Add in buildClaudePrintJSONSessionResult:
sessionResult.SessionID = payload.SessionID

// Add in parseStreamOutput's result event handler:
if payload.SessionID != "" {
    sessionResult.SessionID = payload.SessionID
}
```

In qodercli's JSON parsing path, map the camelCase `sessionId` field to the same normalized field:

```go
var payload qoderPrintJSONResult
if err := json.Unmarshal([]byte(result.Stdout), &payload); err == nil {
    sessionResult.SessionID = payload.SessionID
    sessionResult.FinalMessage = payload.Content
}
```

The evaluator extracts session IDs using a unified multi-parameter version that supports dispatch logic for different Agents (see the `extractSessionID` definition in the codex implementation section above).

#### Fallback Strategy

The fallback logic is built into `executeMultiTurn` (via `agent.SessionResumer` type assertion); see the `executeMultiTurnFallback` method above.

**Fallback mode behavior**:
- Concatenates all turns into a single instruction and sends to the Agent in one shot (existing behavior)
- Annotates the evaluation result with `execution_mode: "single_shot_fallback"`
- Neither `post_condition` nor `capture` are executed (since there are no per-turn results)
- Per-turn Judge assertions (e.g., `turn_response_contains`) will return FAIL due to missing `TurnResults`
- A warning is output in the report, suggesting the user switch to an Agent that supports session resume

```go
// executeMultiTurnFallback is called when the Agent does not support SessionResumer,
// concatenating multi-turn turns into a single instruction for one-shot execution (i.e., existing behavior).
func (e *defaultEvaluator) executeMultiTurnFallback(
    ctx context.Context,
    caseCfg *config.CaseConfig,
    configName string,
    rt runtime.Runtime,
    runAgent agent.Agent,
) EvalResult {
    // Directly reuse the existing executeCaseOnce flow.
    //
    // caseCfg.Input.Turns already exists; buildCaseMessages() concatenates them into messages,
    // then BuildInstructionFromMessages() merges them into a single instruction — this is the existing behavior.
    // executeCaseOnce internally contains the complete:
    //   - tracing span management (agentSpan.End())
    //   - artifact collection (finalizeArtifacts, ensureArtifactsInOutputDir)
    //   - session result normalization (normalizeSessionResult)
    //   - execution error handling (handleExecutionResult: timeout, non-zero exit code, etc.)
    //   - expect pre-check + judge evaluation
    //
    // Note: In fallback mode, neither post_condition nor capture are executed (no per-turn results).
    // Per-turn Judge assertions (turn_response_contains, etc.) will return FAIL since TurnResults is empty.
    logging.WarnContextf(ctx, "Evaluator: multi-turn case %s running in single-shot fallback mode", caseCfg.ID)
    return e.executeCaseOnce(ctx, caseCfg, configName, rt, runAgent)
}
```

### Judge Per-Turn Assertions

#### TurnResult Passing to Judge

Add the following to `Input` in `internal/judge/judge.go`:

```go
type Input struct {
    // Existing fields
    CaseID         string
    Transcript     transcript.Transcript
    FinalMessage   string
    ExitCode       int
    WorkspacePath  string
    SkillDir       string
    WorkspaceDiff  string
    GeneratedFiles []string
    ArtifactDir    string
    SessionResult  *agent.SessionResult
    TurnsExecuted  int
    TurnsTotal     int

    // New field
    // TurnResults holds per-turn execution results for multi-turn cases.
    // Empty for single-turn cases.
    TurnResults []TurnResult `json:"turn_results,omitempty"`
}

// TurnResult is the judge-visible representation of a single turn's execution.
type TurnResult struct {
    TurnNumber int                   `json:"turn_number"` // 1-indexed
    Content    string                `json:"content"`     // the user message sent in this turn
    Response   string                `json:"response"`
    Transcript transcript.Transcript `json:"transcript"`
    Status     string                `json:"status"`      // completed, skipped, failed, error
}
```

#### rule_based Assertion Implementation

Add new case branches in `evaluateAssertion` in `internal/judge/rule_based.go`:

```go
func evaluateAssertion(rule config.Rule, in Input) AssertionResult {
    switch {
    // Existing cases
    case rule.OutputContains != nil:
        return evalOutputContains(rule.OutputContains, in.FinalMessage)
    case rule.ExitCode != nil:
        return evalExitCode(*rule.ExitCode, in.ExitCode)
    case rule.ToolCalled != nil:
        return evalToolCalled(rule.ToolCalled, in.Transcript)
    case len(rule.FilesExist) > 0:
        return evalFilesExist(rule.FilesExist, in.WorkspacePath)
    case len(rule.FilesNotExist) > 0:
        return evalFilesNotExist(rule.FilesNotExist, in.WorkspacePath)

    // New: per-turn assertions
    case rule.TurnResponseContains != nil:
        return evalTurnResponseContains(rule.TurnResponseContains, in.TurnResults)

    case rule.TurnResponseNotContains != nil:
        return evalTurnResponseNotContains(rule.TurnResponseNotContains, in.TurnResults)

    case rule.ToolCalledInTurn != nil:
        return evalToolCalledInTurn(rule.ToolCalledInTurn, in.TurnResults)

    case rule.ToolNotCalledInTurn != nil:
        return evalToolNotCalledInTurn(rule.ToolNotCalledInTurn, in.TurnResults)

    default:
        return AssertionResult{Text: "unknown rule", Passed: false, Evidence: "unrecognized assertion type"}
    }
}

func evalTurnResponseContains(rule *config.TurnResponseContainsRule, turnResults []TurnResult) AssertionResult {
    turnIdx := rule.Turn - 1
    if turnIdx < 0 || turnIdx >= len(turnResults) {
        return AssertionResult{
            Text:     fmt.Sprintf("turn_response_contains(turn=%d)", rule.Turn),
            Passed:   false,
            Evidence: fmt.Sprintf("turn %d not found (total turns: %d)", rule.Turn, len(turnResults)),
        }
    }

    tr := turnResults[turnIdx]
    if tr.Status != "completed" {
        return AssertionResult{
            Text:     fmt.Sprintf("turn_response_contains(turn=%d)", rule.Turn),
            Passed:   false,
            Evidence: fmt.Sprintf("turn %d was %s, not completed", rule.Turn, tr.Status),
        }
    }

    response := strings.ToLower(tr.Response)

    // contains_all: AND semantics
    for _, keyword := range rule.ContainsAll {
        if !strings.Contains(response, strings.ToLower(keyword)) {
            return AssertionResult{
                Text:     fmt.Sprintf("turn_response_contains(turn=%d, all)", rule.Turn),
                Passed:   false,
                Evidence: fmt.Sprintf("turn %d response missing: %q", rule.Turn, keyword),
            }
        }
    }

    // contains_any: OR semantics
    if len(rule.ContainsAny) > 0 {
        found := false
        for _, keyword := range rule.ContainsAny {
            if strings.Contains(response, strings.ToLower(keyword)) {
                found = true
                break
            }
        }
        if !found {
            return AssertionResult{
                Text:     fmt.Sprintf("turn_response_contains(turn=%d, any)", rule.Turn),
                Passed:   false,
                Evidence: fmt.Sprintf("turn %d response missing any of: %v", rule.Turn, rule.ContainsAny),
            }
        }
    }

    return AssertionResult{
        Text:     fmt.Sprintf("turn_response_contains(turn=%d)", rule.Turn),
        Passed:   true,
        Evidence: fmt.Sprintf("turn %d response matched", rule.Turn),
    }
}

// evalTurnResponseNotContains checks that a specific turn's response does NOT contain forbidden text.
func evalTurnResponseNotContains(rule *config.TurnResponseNotContainsRule, turnResults []TurnResult) AssertionResult {
    turnIdx := rule.Turn - 1
    if turnIdx < 0 || turnIdx >= len(turnResults) {
        return AssertionResult{
            Text:     fmt.Sprintf("turn_response_not_contains(turn=%d)", rule.Turn),
            Passed:   false,
            Evidence: fmt.Sprintf("turn %d not found (total turns: %d)", rule.Turn, len(turnResults)),
        }
    }

    tr := turnResults[turnIdx]
    if tr.Status != "completed" {
        return AssertionResult{
            Text:     fmt.Sprintf("turn_response_not_contains(turn=%d)", rule.Turn),
            Passed:   false,
            Evidence: fmt.Sprintf("turn %d was %s, not completed", rule.Turn, tr.Status),
        }
    }

    response := strings.ToLower(tr.Response)
    for _, keyword := range rule.NotContains {
        if strings.Contains(response, strings.ToLower(keyword)) {
            return AssertionResult{
                Text:     fmt.Sprintf("turn_response_not_contains(turn=%d)", rule.Turn),
                Passed:   false,
                Evidence: fmt.Sprintf("turn %d response contains forbidden keyword: %q", rule.Turn, keyword),
            }
        }
    }

    return AssertionResult{
        Text:     fmt.Sprintf("turn_response_not_contains(turn=%d)", rule.Turn),
        Passed:   true,
        Evidence: fmt.Sprintf("turn %d response does not contain any forbidden keywords", rule.Turn),
    }
}

// evalToolCalledInTurn checks that a specific tool was called in a specific turn.
func evalToolCalledInTurn(rule *config.ToolCalledInTurnRule, turnResults []TurnResult) AssertionResult {
    turnIdx := rule.Turn - 1
    if turnIdx < 0 || turnIdx >= len(turnResults) {
        return AssertionResult{
            Text:     fmt.Sprintf("tool_called_in_turn(turn=%d, tool=%s)", rule.Turn, rule.Name),
            Passed:   false,
            Evidence: fmt.Sprintf("turn %d not found (total turns: %d)", rule.Turn, len(turnResults)),
        }
    }

    tr := turnResults[turnIdx]
    if tr.Status != "completed" {
        return AssertionResult{
            Text:     fmt.Sprintf("tool_called_in_turn(turn=%d, tool=%s)", rule.Turn, rule.Name),
            Passed:   false,
            Evidence: fmt.Sprintf("turn %d was %s, not completed", rule.Turn, tr.Status),
        }
    }

    // Search for the tool call within this turn's transcript
    for _, msg := range tr.Transcript {
        if msg.Role != transcript.RoleToolCall || msg.ToolCall == nil {
            continue
        }
        if msg.ToolCall.Name != rule.Name {
            continue
        }
        // Name matched; check args if specified (partial match)
        if len(rule.Args) == 0 {
            return AssertionResult{
                Text:     fmt.Sprintf("tool_called_in_turn(turn=%d, tool=%s)", rule.Turn, rule.Name),
                Passed:   true,
                Evidence: fmt.Sprintf("tool %q was called in turn %d", rule.Name, rule.Turn),
            }
        }
        if argsMatch(rule.Args, msg.ToolCall.Arguments) {
            return AssertionResult{
                Text:     fmt.Sprintf("tool_called_in_turn(turn=%d, tool=%s, with args)", rule.Turn, rule.Name),
                Passed:   true,
                Evidence: fmt.Sprintf("tool %q was called in turn %d with matching args", rule.Name, rule.Turn),
            }
        }
    }

    return AssertionResult{
        Text:     fmt.Sprintf("tool_called_in_turn(turn=%d, tool=%s)", rule.Turn, rule.Name),
        Passed:   false,
        Evidence: fmt.Sprintf("tool %q was not called in turn %d", rule.Name, rule.Turn),
    }
}

// evalToolNotCalledInTurn checks that a specific tool was NOT called in a specific turn.
func evalToolNotCalledInTurn(rule *config.ToolNotCalledInTurnRule, turnResults []TurnResult) AssertionResult {
    turnIdx := rule.Turn - 1
    if turnIdx < 0 || turnIdx >= len(turnResults) {
        return AssertionResult{
            Text:     fmt.Sprintf("tool_not_called_in_turn(turn=%d, tool=%s)", rule.Turn, rule.Name),
            Passed:   true, // Turn doesn't exist → tool was not called → pass
            Evidence: fmt.Sprintf("turn %d not found, so tool %q was not called", rule.Turn, rule.Name),
        }
    }

    tr := turnResults[turnIdx]
    for _, msg := range tr.Transcript {
        if msg.Role == transcript.RoleToolCall && msg.ToolCall != nil && msg.ToolCall.Name == rule.Name {
            return AssertionResult{
                Text:     fmt.Sprintf("tool_not_called_in_turn(turn=%d, tool=%s)", rule.Turn, rule.Name),
                Passed:   false,
                Evidence: fmt.Sprintf("tool %q was unexpectedly called in turn %d", rule.Name, rule.Turn),
            }
        }
    }

    return AssertionResult{
        Text:     fmt.Sprintf("tool_not_called_in_turn(turn=%d, tool=%s)", rule.Turn, rule.Name),
        Passed:   true,
        Evidence: fmt.Sprintf("tool %q was not called in turn %d as expected", rule.Name, rule.Turn),
    }
}
```

### Validator Changes

Multiple new fields have been added to the schema; corresponding validation rules need to be added in `ValidateCaseConfig` in `internal/config/validator.go`.

#### New Validation Rules

```go
// New validation logic in ValidateCaseConfig:

// 1. Validate post_condition for each turn in input.turns
for i, turn := range cfg.Input.Turns {
    if turn.PostCondition != nil {
        if turn.PostCondition.OnFail != "" &&
            turn.PostCondition.OnFail != "skip_remaining" &&
            turn.PostCondition.OnFail != "fail" {
            errs = append(errs, fmt.Sprintf(
                "input.turns[%d].post_condition.on_fail must be 'skip_remaining' or 'fail', got %q", i, turn.PostCondition.OnFail))
        }
        // post_condition requires at least one matching condition
        hasCondition := len(turn.PostCondition.MustContainAny) > 0 ||
            len(turn.PostCondition.MustContainAll) > 0 ||
            len(turn.PostCondition.MustNotContain) > 0
        if !hasCondition {
            errs = append(errs, fmt.Sprintf(
                "input.turns[%d].post_condition must specify at least one of: must_contain_any, must_contain_all, must_not_contain", i))
        }
    }

    // 2. Capture rule validation
    for j, cap := range turn.Capture {
        if cap.Variable == "" {
            errs = append(errs, fmt.Sprintf(
                "input.turns[%d].capture[%d].variable is required", i, j))
        }
        if cap.Pattern == "" && cap.JSONPath == "" {
            errs = append(errs, fmt.Sprintf(
                "input.turns[%d].capture[%d] must specify either pattern or jsonpath", i, j))
        }
        if cap.Pattern != "" && cap.JSONPath != "" {
            errs = append(errs, fmt.Sprintf(
                "input.turns[%d].capture[%d] must specify only one of pattern or jsonpath, not both", i, j))
        }
        // Verify regex compiles
        if cap.Pattern != "" {
            if _, err := regexp.Compile(cap.Pattern); err != nil {
                errs = append(errs, fmt.Sprintf(
                    "input.turns[%d].capture[%d].pattern is invalid regex: %v", i, j, err))
            }
        }
    }

    // 3. Per-turn timeout validation
    if turn.TimeoutSeconds < 0 {
        errs = append(errs, fmt.Sprintf(
            "input.turns[%d].timeout_seconds must be non-negative", i))
    }

    // 4. content required validation
    if turn.Content == "" {
        errs = append(errs, fmt.Sprintf(
            "input.turns[%d].content is required", i))
    }
}

// 4. Validate per-turn assertions in judge.success / judge.failure
for i, rule := range cfg.Judge.Success {
    errs = append(errs, validateTurnRule(fmt.Sprintf("judge.success[%d]", i), rule, len(cfg.Input.Turns))...)
}
for i, rule := range cfg.Judge.Failure {
    errs = append(errs, validateTurnRule(fmt.Sprintf("judge.failure[%d]", i), rule, len(cfg.Input.Turns))...)
}
```

#### Per-Turn Assertion Validation Function

```go
// validateTurnRule validates turn-specific rule fields.
func validateTurnRule(prefix string, rule Rule, totalTurns int) []string {
    var errs []string

    if rule.TurnResponseContains != nil {
        r := rule.TurnResponseContains
        if r.Turn < 1 {
            errs = append(errs, fmt.Sprintf("%s.turn_response_contains.turn must be >= 1", prefix))
        }
        if totalTurns > 0 && r.Turn > totalTurns {
            errs = append(errs, fmt.Sprintf(
                "%s.turn_response_contains.turn (%d) exceeds total turns (%d)", prefix, r.Turn, totalTurns))
        }
        if len(r.ContainsAll) == 0 && len(r.ContainsAny) == 0 {
            errs = append(errs, fmt.Sprintf(
                "%s.turn_response_contains must specify contains_all or contains_any", prefix))
        }
    }

    if rule.TurnResponseNotContains != nil {
        r := rule.TurnResponseNotContains
        if r.Turn < 1 {
            errs = append(errs, fmt.Sprintf("%s.turn_response_not_contains.turn must be >= 1", prefix))
        }
        if totalTurns > 0 && r.Turn > totalTurns {
            errs = append(errs, fmt.Sprintf(
                "%s.turn_response_not_contains.turn (%d) exceeds total turns (%d)", prefix, r.Turn, totalTurns))
        }
        if len(r.NotContains) == 0 {
            errs = append(errs, fmt.Sprintf(
                "%s.turn_response_not_contains.not_contains is required", prefix))
        }
    }

    if rule.ToolCalledInTurn != nil {
        r := rule.ToolCalledInTurn
        if r.Turn < 1 {
            errs = append(errs, fmt.Sprintf("%s.tool_called_in_turn.turn must be >= 1", prefix))
        }
        if r.Name == "" {
            errs = append(errs, fmt.Sprintf("%s.tool_called_in_turn.name is required", prefix))
        }
    }

    if rule.ToolNotCalledInTurn != nil {
        r := rule.ToolNotCalledInTurn
        if r.Turn < 1 {
            errs = append(errs, fmt.Sprintf("%s.tool_not_called_in_turn.turn must be >= 1", prefix))
        }
        if r.Name == "" {
            errs = append(errs, fmt.Sprintf("%s.tool_not_called_in_turn.name is required", prefix))
        }
    }

    return errs
}
```

### Reliability Mechanisms

#### 1. post_condition Pre-Assertions

After each turn executes, `post_condition` is checked; when not met, it is handled according to the `on_fail` strategy:

| on_fail Value    | Behavior                       | Evaluation Status                   |
| ---------------- | ------------------------------ | ----------------------------------- |
| `skip_remaining` | Skip all subsequent turns      | `SKIP` (reason annotated in report) |
| `fail` (default) | Immediately terminate the case | `FAIL`                              |

Representation in reports:

```json
{
  "case_id": "confirm-then-execute",
  "status": "SKIP",
  "skip_reason": "Turn 1 post_condition not met: response missing any of: [confirm, OK, continue?]",
  "turns_executed": 1,
  "turns_total": 2,
  "turn_results": [
    {
      "turn_number": 1,
      "status": "completed",
      "post_condition_passed": false
    },
    {
      "turn_number": 2,
      "status": "skipped",
      "skip_reason": "skipped due to turn 1 post_condition failure"
    }
  ]
}
```

#### 2. Capture Value Extraction

Two extraction methods are supported:

**Regex extraction**:
```yaml
capture:
  - variable: plan_name
    pattern: "created plan[「\"'](?P<value>[^「\"']+)[」\"']"
```

**JSONPath extraction** (extracting from ToolResult messages in the turn's transcript):
```yaml
capture:
  - variable: plan_id
    jsonpath: "$.transcript.tool_results[0].call_id"
```

> **Data source note**: The JSONPath root object `$` is a structured turn result JSON containing `response` (Agent text response) and `transcript` (the turn's transcript, with `tool_calls` and `tool_results` arrays). Tool result entries expose `call_id`, `status`, and `content`; tool call entries expose `id`, `name`, and `arguments`. JSONPath examples must include the `transcript` prefix, for example `$.transcript.tool_results[0].call_id`.

Extracted values are referenced in subsequent turns via `{{variable_name}}`:
```yaml
- role: user
  content: "Add an approval node to {{plan_id}}"
```

#### 3. retry_on Extension

```yaml
cases:
  retry_policy:
    max_retries: 2
    retry_on:
      - timeout
      - error
      - turn_precondition_fail  # New: retry the entire case when post_condition fails
```

#### 4. Multi-Turn Transcript Format

The complete multi-turn transcript preserves messages from each turn, annotated with turn numbers:

```json
[
  {"role": "user", "content": "sdd_bootstrap: task=implement login", "turn": 1},
  {"role": "assistant", "content": "Entering Research phase...", "turn": 1},
  {"role": "user", "content": "Skip Research, write code directly", "turn": 2},
  {"role": "assistant", "content": "Need to complete Research phase first...", "turn": 2}
]
```

## Test Plan

### Unit Tests

| Test Scenario              | Package     | Description                                                                 |
| -------------------------- | ----------- | --------------------------------------------------------------------------- |
| Schema parsing             | `config`    | Verify YAML parsing of Turn.Capture, PostCondition new fields               |
| Validator                  | `config`    | Verify turns validation rules (empty content, invalid on_fail values, etc.) |
| post_condition             | `evaluator` | Verify `checkPostCondition` AND/OR/NOT logic                                |
| capture extraction         | `evaluator` | Verify both regex and JSONPath extraction methods                           |
| Template rendering         | `evaluator` | Verify `{{variable}}` substitution logic                                    |
| turn_response_contains     | `judge`     | Verify per-turn assertion matching logic                                    |
| turn_response_not_contains | `judge`     | Verify per-turn negative assertions                                         |
| tool_called_in_turn        | `judge`     | Verify per-turn tool call checks                                            |
| Turn out of bounds         | `judge`     | Verify FAIL is returned when specifying a non-existent turn                 |

### Integration Tests

| Test Scenario                | Description                                                           |
| ---------------------------- | --------------------------------------------------------------------- |
| Two-turn normal execution    | Both turns succeed, Judge passes                                      |
| post_condition skip          | Turn 1 post_condition fails, turn 2 is skipped                        |
| post_condition fail          | Turn 1 post_condition fails, entire case FAILs                        |
| capture + template reference | Turn 1 capture value correctly substituted in turn 2                  |
| session resume fallback      | Falls back to single-shot execution when Agent doesn't support resume |
| Single-turn compatibility    | Existing `input.prompt` cases behave unchanged                        |

### E2E Tests

| Test Scenario              | Description                                         |
| -------------------------- | --------------------------------------------------- |
| Full multi-turn evaluation | Execute 2-3 turn multi-turn cases with a real Agent |
| Report format verification | Verify JSON/HTML reports contain turn_results       |

## Drawbacks

1. **Increased complexity**: The multi-turn execution path is significantly more complex than single-turn, increasing evaluator maintenance cost
2. **Longer execution time**: Multi-turn interaction time and token consumption is several times that of single-turn
3. **Agent dependency**: Session resume depends on the Agent CLI's `--resume` capability, subject to upstream API changes
4. **Debugging difficulty**: When multi-turn cases fail, each turn's input/output needs to be analyzed, increasing debugging complexity
5. **Model randomness**: Model randomness is amplified in multi-turn interactions, potentially requiring looser matching strategies or more retries

## Alternatives

### Alternative A: Pure Concatenation Mode (Existing Behavior Optimization)

**Approach**: Concatenate multi-turn turns into a single large prompt simulating conversation history, sending to the Agent in one shot.

```
[Simulated conversation history]
User: sdd_bootstrap: task=implement login
Assistant: [expected response placeholder]
User: Skip Research, write code directly

Please respond to the last user message based on the conversation history above.
```

**Pros**: Simple implementation, no Agent interface changes needed.

**Cons**:
- The Agent cannot distinguish between "real prior interactions" and "simulated conversation history"
- Cannot verify the actual output from previous turns
- Does not support intermediate checks like post_condition, capture
- Cannot test the Agent's actual session state management capability

**Conclusion**: Cannot meet core requirements, **not adopted**.

### Alternative B: Standalone Multi-Turn Test Framework

**Approach**: Without modifying the skill-up core, build a dedicated multi-turn testing tool separately.

**Pros**: Does not affect existing code, can evolve independently.

**Cons**:
- Duplicate infrastructure (runtime, agent adapter, judge, report all need reimplementation)
- Users need to learn and maintain two toolsets
- Cannot share skill-up's infrastructure (credential management, sandbox, reporting)

**Conclusion**: Cost too high, **not adopted**.

### Alternative C: Add RunTurn to Agent Interface (This Proposal)

**Approach**: Within the existing skill-up framework, implement via Agent interface extension `RunTurn` + evaluator multi-turn execution engine.

**Pros**:
- Minimizes changes, reuses existing infrastructure
- Backward compatible, single-turn cases unaffected
- Leverages Agent CLI's native session resume capability

**Cons**:
- Requires each Agent to implement `RunTurn`
- Constrained by Agent CLI's session resume capabilities

**Conclusion**: **This proposal is adopted**.

### Alternative D: Scenario-Driven ChatterAgent Mode (Future Extension)

**Approach**: Instead of requiring the case author to predefine every user turn, a separate simulated-user Agent (for example, `ChatterAgent`) receives a scenario objective, available knowledge, behavior constraints, and a required `max_turns`. It then generates adaptive user messages until the objective appears completed, impossible, or the turn limit is reached. The existing Judge still evaluates the final transcript, tool calls, files, and workspace state.

Example shape:

```yaml
input:
  conversation:
    mode: chatter_agent
    max_turns: 6
    chatter:
      role: "business user"
      objective: >
        Create a new member named Alice, then upgrade her membership level to Gold.
        If the Agent asks for missing information, provide the required information.
      knowledge:
        member:
          name: Alice
          age: 28
          phone: "13800000000"
          initial_level: Silver
          target_level: Gold
      behavior:
        - "Act like a normal user, not an evaluator."
        - "Do not reveal all information at once unless the Agent asks for it."
        - "Do not invent information outside the provided knowledge."
        - "Stop once the task appears completed."
```

**Pros**:
- Easier for authors to express open-ended business workflows where the next user message depends on the Agent's previous response
- Reduces brittle keyword tuning in cases whose main purpose is task completion rather than protocol conformance
- Still reuses the same transcript and Judge infrastructure if implemented as another conversation input mode

**Cons**:
- Adds another probabilistic Agent to the evaluation path, which can reduce reproducibility and increase cost
- Requires strict stop conditions, transcript attribution, and reporting of generated user turns for debuggability
- Needs additional safeguards so the simulated user does not judge, coach, or leak hidden evaluation criteria to the Agent under test

**Conclusion**: Accepted as a complementary future direction, but **not adopted in this proposal**. This PR focuses on deterministic `input.turns` because it is the lower-level primitive needed for protocol checks, phase gating, double confirmation, exact regressions, and reproducible implementation tests. A later proposal can layer `input.conversation.mode: chatter_agent` on top of the same turn-by-turn execution and judging primitives.

## Infrastructure Needed

- **No new external dependencies**: Regex extraction for capture uses Go's standard library `regexp`; JSONPath extraction uses existing dependencies or a lightweight implementation
- **No new services**: All changes are internal to the skill-up CLI
- **Agent CLI requirements**:
  - claude_code: Must support `--resume <session-id>` + `-p` parameters (verified, confirmed by [official docs](https://code.claude.com/docs/en/cli-reference))
  - codex: Must support `codex exec resume <SESSION_ID>` non-interactive mode (verified, confirmed by [official docs](https://developers.openai.com/codex/cli/features))
  - qodercli: Must support `-r <SESSION_ID>` + `-p` + `--output-format=json` non-interactive mode; the initial `-p` run must return `sessionId` in JSON output for exact subsequent resume
- **JSONPath library**: JSONPath extraction for capture requires adding a new dependency `github.com/PaesslerAG/jsonpath` to `go.mod` (MIT license, lightweight with no transitive dependencies). Introduced via `go get github.com/PaesslerAG/jsonpath`

## Upgrade & Migration Strategy

### Backward Compatibility

| Scenario                                              | Impact             | Handling                                                                                      |
| ----------------------------------------------------- | ------------------ | --------------------------------------------------------------------------------------------- |
| Existing `input.prompt` cases                         | No impact          | Takes existing single-turn execution path                                                     |
| Existing `input.turns` cases (without post_condition) | Behavior change    | Changes from "concatenate and send once" to "execute turn by turn"; results are more accurate |
| Existing Judge rules                                  | No impact          | Global assertions continue to apply to the complete transcript                                |
| Schema version                                        | Remains `v1alpha1` | All new fields are optional                                                                   |

### Migration Steps

1. **Phase 1**: Implement evaluator multi-turn execution engine + Agent `RunTurn` interface, with claude_code and qodercli adapters first because both expose parseable session IDs in JSON output
2. **Phase 2**: Implement per-turn Judge assertions (`turn_response_contains`, etc.)
3. **Phase 3**: Implement capture + template variables + retry extension
4. **Phase 4**: codex `RunTurn` implementation after finalizing safe session-file correlation

Each Phase can be released independently without blocking subsequent Phases.

## Design Self-Review and Implementation Notes

### Identified Technical Risks and Mitigations

#### 1. `executeMultiTurnFallback` Implementation Strategy (Resolved)

**Original problem**: Earlier versions of `executeMultiTurnFallback` called `evaluateCaseSession` directly, missing critical intermediate steps in `executeCaseOnce` such as tracing span, artifact collection, `normalizeSessionResult`, and `handleExecutionResult`.

**Adopted solution**: The `executeMultiTurnFallback` in the main text has been changed to call `e.executeCaseOnce(ctx, caseCfg, configName, rt, runAgent)` directly, fully reusing all intermediate steps of the existing single-turn flow with no risk of omission.

> **Risk level**: ✅ Eliminated.

#### 2. Codex Session ID Extraction Race Condition

**Problem**: `extractCodexSessionID` must not retrieve the most recent session file via `ls -t ~/.codex/sessions/*.jsonl | head -1`. skill-up supports case-level concurrency through `cases.parallelism`, so two codex cases can start at nearly the same time and create session files in the same global directory. Without a correlation key, one case can resume another case's session and contaminate both transcripts.

**Adopted constraints**:
- Prefer parsing a session ID emitted by codex CLI if available
- Otherwise use a case-isolated session directory or serialize the initial `Run` + session-file diff window so the new session file is associated with the current case
- If neither strategy is available, codex multi-turn support must be disabled with an explicit ERROR/unsupported diagnostic, or documented as requiring `cases.parallelism: 1`

> **Risk level**: 🟡 Medium after mitigation. The risk is real in the current evaluator because `cases.parallelism` already permits concurrent cases, but it is controllable by requiring an explicit session correlation strategy before codex multi-turn support is marked implemented.

#### 3. Behavior When `capture` Extraction Fails

**Problem**: When `extractCapturedValue` returns an empty string or silently ignores a failed match, the `{{variable}}` placeholder in subsequent turns can remain as-is and be sent to the Agent, contaminating the conversation without a clear diagnostic.

**Adopted solution**:
- `extractCapturedValue` returns `(string, error)` and treats invalid regex, no regex match, JSONPath miss, and empty extracted values as errors
- `executeTurnsSequentially` marks the current turn as `TurnError` and stops the case when any configured capture fails
- `renderTemplate` detects unresolved `{{variable}}` placeholders before sending a prompt and returns a `TurnError` instead of forwarding raw template syntax to the Agent

```go
func renderTemplate(content string, vars map[string]string) (string, error) {
    result := content
    for name, value := range vars {
        result = strings.ReplaceAll(result, "{{"+name+"}}", value)
    }
    unresolved := regexp.MustCompile(`\{\{[a-zA-Z_][a-zA-Z0-9_]*\}\}`).FindAllString(result, -1)
    if len(unresolved) > 0 {
        return "", fmt.Errorf("unresolved template variables: %v", unresolved)
    }
    return result, nil
}
```

> **Risk level**: 🟢 Low after mitigation. Capture failures are explicit ERRORs with diagnostics, so they do not silently change the prompt sent to later turns.

#### 4. `RunTurn` Behavior When `sessionID` is Empty

**Problem**: If the first turn's `Run` succeeds but `extractSessionID` returns an empty string (e.g., due to abnormal Agent output format, missing qodercli `sessionId`, or unsafe codex session-file correlation), subsequent `RunTurn` calls with an empty `sessionID` will cause the CLI command to error.

**Mitigations**:
- In `executeTurnsSequentially`, check whether `sessionID` is empty after the first turn executes
- If empty, mark subsequent turns as `TurnError` and terminate, rather than passing an empty sessionID causing CLI errors

After the first turn's execution closure returns in `executeTurnsSequentially` (i.e., after step 3's closure call completes in the code above), append a sessionID empty-value check. The complete code snippet is as follows:

```go
sessionResult, execErr := func() (*agent.SessionResult, error) {
    turnCtx := ctx
    if turn.TimeoutSeconds > 0 {
        var cancel context.CancelFunc
        turnCtx, cancel = context.WithTimeout(ctx, time.Duration(turn.TimeoutSeconds)*time.Second)
        defer cancel()
    }
    if turnNum == 1 {
        if runAgent.Name() == "codex" {
            codexSessionSnapshotBeforeRun = snapshotCodexSessions(turnCtx, rt)
        }
        sr, err := runAgent.Run(turnCtx, rt, agent.ExecOptions{}, []transcript.Message{message})
        if sr != nil {
            sessionID = extractSessionID(turnCtx, rt, runAgent, sr, codexSessionSnapshotBeforeRun)
        }
        return sr, err
    }
    return resumer.RunTurn(turnCtx, rt, agent.ExecOptions{}, message, sessionID)
}()

// Empty sessionID check (only for first turn when there are subsequent turns)
if turnNum == 1 && sessionID == "" && turnsTotal > 1 && execErr == nil {
    turnResult.Response = sessionResult.FinalMessage
    turnResult.Transcript = sessionResult.Transcript
    turnResult.SessionResult = sessionResult
    turnResult.Status = TurnCompleted
    turnResults = append(turnResults, turnResult)
    for j := turnNum; j < turnsTotal; j++ {
        turnResults = append(turnResults, TurnResult{
            TurnNumber: j + 1,
            Status:     TurnError,
            SkipReason: "failed to extract session ID from initial run; cannot resume session",
        })
    }
    return turnResults
}
```

> **Risk level**: 🟡 Medium. Session ID extraction failure will prevent the entire multi-turn case from executing.

#### 5. JSONPath Library Dependency

**Problem**: The `extractByJSONPath` in this proposal uses a `jsonpath.Get(path, data)` call, requiring an external JSONPath library (e.g., `github.com/PaesslerAG/jsonpath`).

**Mitigations**:
- Phase 1 only supports regex capture (covers most scenarios); JSONPath capture is implemented in Phase 3
- Phase 3 implementation introduces the dependency via `go get github.com/PaesslerAG/jsonpath` (MIT license, no transitive dependencies, API is `jsonpath.Get(path, data)`)

> **Risk level**: 🟢 Low. Regex capture can satisfy most scenarios; JSONPath is an incremental capability.

### Suggested Implementation Priority

| Priority | Module                                              | Rationale                                                |
| -------- | --------------------------------------------------- | -------------------------------------------------------- |
| P0       | `executeCaseOnce` branching + `executeMultiTurn`    | Critical path, must be implemented first                 |
| P0       | `SessionResumer` interface + claude_code/qodercli `RunTurn` | Foundation for multi-turn execution                      |
| P0       | `executeTurnsSequentially`                          | Turn-by-turn execution engine                            |
| P0       | `checkPostCondition`                                | Per-turn assertions, core value of multi-turn evaluation |
| P1       | `SessionResult.SessionID` field + extraction logic  | Prerequisite for session resume                          |
| P1       | `finalizeMultiTurnResult`                           | Result aggregation and Judge execution                   |
| P1       | `turn_response_contains` and other Judge assertions | Per-turn evaluation                                      |
| P1       | New Validator rules                                 | Prevent invalid configurations                           |
| P2       | codex `RunTurn` implementation                      | Requires safe session-file correlation                   |
| P2       | `capture` + `renderTemplate`                        | Dynamic value passing                                    |
| P3       | JSONPath capture                                    | Only needed when regex is insufficient                   |
| P3       | `retry_on: turn_precondition_fail`                  | Nice to Have                                             |

### Design Completeness Self-Assessment

| Dimension                  | Rating     | Description                                                                                                                                                          |
| -------------------------- | ---------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Schema design              | ✅ Complete | Turn, PostCondition, CaptureRule, Rule extensions all have complete definitions                                                                                      |
| Evaluator execution engine | ✅ Complete | `executeCaseOnce` branching, `executeMultiTurn`, `executeTurnsSequentially`, `finalizeMultiTurnResult`, `executeMultiTurnFallback` all have complete implementations |
| Agent interface            | ✅ Complete | `SessionResumer` interface definition, claude_code, qodercli, and codex `RunTurn` implementations, `extractSessionID` dispatch logic all provided                    |
| Judge assertions           | ✅ Complete | `evalTurnResponseContains`, `evalTurnResponseNotContains`, `evalToolCalledInTurn`, `evalToolNotCalledInTurn` all have complete implementations                       |
| Validator                  | ✅ Complete | Validation rules for post_condition, capture, per-turn assertions, and content required are all provided                                                             |
| Helper functions           | ✅ Complete | `renderTemplate`, `extractCapturedValue`, `checkPostCondition`, and 6 helper functions all have complete implementations                                             |
| Backward compatibility     | ✅ Verified | Branch condition `len(caseCfg.Input.Turns) > 1` ensures single-turn cases are unaffected                                                                             |
| Edge cases                 | ✅ Complete | Edge cases like empty sessionID and capture failure are all addressed with complete handling code in the main text and reflection sections                           |
| Executability              | ✅ Feasible | All code blocks have complete implementations and can be used directly as implementation references                                                                  |
