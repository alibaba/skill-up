// Package judge evaluates case outputs using rule_based, script, or agent_judge strategies.
package judge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/pkg/transcript"
)

const (
	configurationWithSkill    = "with_skill"
	configurationWithoutSkill = "without_skill"
)

// Status represents the overall evaluation outcome for a case.
type Status string

const (
	// StatusPass indicates all assertions passed.
	StatusPass Status = "PASS"
	// StatusFail indicates one or more assertions failed.
	StatusFail Status = "FAIL"
	// StatusSkip indicates the case was skipped.
	StatusSkip Status = "SKIP"
	// StatusError indicates an unexpected execution error.
	StatusError Status = "ERROR"
)

// Judge scores or validates engine output for a case.
type Judge interface {
	Evaluate(ctx context.Context, in Input) (*Result, error)
}

// InputTurnResult holds the outcome of a single conversation turn, visible to judges.
type InputTurnResult struct {
	// TurnNumber is the 1-based index of this turn.
	TurnNumber int
	// Content is the user message sent to the agent.
	Content string
	// Response is the assistant response text for this turn.
	Response string
	// Transcript is the per-turn interaction record.
	Transcript transcript.Transcript
	// Status is the turn outcome: "completed", "skipped", "failed", "error".
	Status string
	// Reason describes why the turn was skipped/failed/errored.
	Reason string
}

// Input carries artifacts needed for grading.
//
// It is the unified data boundary between the execution layer and the evaluation layer.
// All judge implementations (rule_based, script, agent_judge) receive the same Input.
type Input struct {
	// CaseID is the unique identifier of the case being evaluated.
	CaseID string

	// Transcript is the complete interaction record from the engine.
	Transcript transcript.Transcript

	// FinalMessage is the last assistant response text.
	FinalMessage string

	// ExitCode is the engine process exit code.
	ExitCode int

	// WorkspacePath is the absolute path to the case workspace root directory.
	// Used by workspace-scoped file checks and script judge.
	WorkspacePath string

	// SkillDir is the absolute path to the directory containing SKILL.md.
	// Fixture-style references such as golden_file are resolved from here.
	SkillDir string

	// Configuration identifies the evaluated variant: "with_skill" or
	// "without_skill". Agent-judge diagnosis uses it to avoid attributing an
	// intentional benchmark baseline failure to a missing Skill.
	Configuration string

	// SkillSources carries immutable snapshots of the exact Skill sources used
	// by the evaluated run. It is judge-only input and is materialized
	// separately from the run agent's installed Skill directory.
	SkillSources []SkillSource

	// SkillUsage records trustworthy Skill invocation evidence when the engine
	// exposes it. A nil or unavailable value must not be treated as proof that
	// the Skill was not triggered.
	SkillUsage *SkillUsageEvidence

	// WorkspaceDiff is the git diff of workspace changes after engine execution.
	WorkspaceDiff string

	// GeneratedFiles lists file paths created by the agent.
	GeneratedFiles []string

	// ArtifactDir is the host directory where a judge that invokes an agent
	// should write or download its own run artifacts.
	ArtifactDir string

	// SessionResult is the full engine output, available for advanced judges.
	SessionResult *agent.SessionResult

	// TurnResults holds per-turn outcomes for multi-turn evaluations.
	// Nil for single-turn cases.
	TurnResults []InputTurnResult

	// TurnsExecuted is the number of turns actually executed.
	TurnsExecuted int

	// TurnsTotal is the total number of turns defined in the case.
	TurnsTotal int
}

// SkillSource identifies one local Skill source as installed for evaluation.
// Include and Exclude use the same doublestar filters as config.SkillRef.
type SkillSource struct {
	Name    string
	Path    string
	Include []string
	Exclude []string

	// Captured reports that Files is an immutable pre-execution snapshot. A
	// captured source is never re-read from Path during judging.
	Captured bool
	Files    []SkillSourceFile
}

// SkillSourceFile is one file in an immutable evaluated-Skill snapshot.
// Content is retained only for readable files that fit the configured context
// budget; every file still carries its pre-execution size and digest.
type SkillSourceFile struct {
	Path       string
	Bytes      int
	SHA256     string
	Content    []byte
	HasContent bool
}

// SkillUsageStatus describes what the captured engine evidence proves about
// Skill use. Unavailable means the evidence channel cannot support either a
// positive or negative conclusion.
type SkillUsageStatus string

const (
	// SkillUsageUnavailable means the engine did not expose trustworthy Skill
	// usage evidence.
	SkillUsageUnavailable SkillUsageStatus = "unavailable"
	// SkillUsageTriggered means at least one explicit Skill invocation was
	// observed.
	SkillUsageTriggered SkillUsageStatus = "triggered"
	// SkillUsageNotTriggered means a complete engine evidence channel proved
	// that the Skill was available but never invoked.
	SkillUsageNotTriggered SkillUsageStatus = "not_triggered"
)

// SkillUsageEvidence is a reportable summary of engine/session Skill evidence.
type SkillUsageEvidence struct {
	Status   SkillUsageStatus `json:"status"`
	Reliable bool             `json:"reliable"`
	Evidence []string         `json:"evidence,omitempty"`
}

// InferSkillUsageEvidence extracts only positive, explicit Skill tool calls
// from a transcript. No matching call yields unavailable rather than
// not_triggered because not every engine exposes a complete Skill-use channel.
func InferSkillUsageEvidence(trans transcript.Transcript) *SkillUsageEvidence {
	evidence := &SkillUsageEvidence{Status: SkillUsageUnavailable}
	for _, message := range trans.ToolCalls() {
		if message.ToolCall == nil || !strings.EqualFold(strings.TrimSpace(message.ToolCall.Name), "skill") {
			continue
		}
		detail := "observed explicit Skill tool call"
		for _, key := range []string{"skill", "name"} {
			if value, ok := message.ToolCall.Arguments[key].(string); ok && strings.TrimSpace(value) != "" {
				detail = fmt.Sprintf("observed explicit Skill tool call for %q", strings.TrimSpace(value))
				break
			}
		}
		evidence.Evidence = append(evidence.Evidence, detail)
	}
	if len(evidence.Evidence) > 0 {
		evidence.Status = SkillUsageTriggered
		evidence.Reliable = true
	}
	return evidence
}

// Result corresponds to grading.json summary fields.
//
// Mapping to grading.json:
//
//	{
//	  "status": "PASS",
//	  "skip_reason": null,
//	  "turns_executed": 2,
//	  "turns_total": 2,
//	  "assertion_results": [...],
//	  "summary": { "passed": 1, "failed": 0, "total": 1, "pass_rate": 1.0 }
//	}
type Result struct {
	// Status is the overall evaluation outcome.
	Status Status `json:"status"`

	// SkipReason is populated when Status == StatusSkip.
	SkipReason *string `json:"skip_reason,omitempty"`

	// ErrorReason is populated when Status == StatusError.
	ErrorReason *string `json:"error_reason,omitempty"`

	// TurnsExecuted is the number of turns actually executed.
	TurnsExecuted int `json:"turns_executed"`

	// TurnsTotal is the total number of turns in the case.
	TurnsTotal int `json:"turns_total"`

	// AssertionResults holds per-assertion details.
	AssertionResults []AssertionResult `json:"assertion_results"`

	// Summary provides aggregate pass/fail statistics.
	Summary ResultSummary `json:"summary"`

	// JudgeSession is set when the judge implementation runs a separate agent
	// session (e.g. agent_judge). It is not part of grading.json; the evaluator
	// uses it to download judge-run artifacts the same way as the main agent run.
	JudgeSession *agent.SessionResult `json:"-"`

	// JudgeContext records how agent_judge materialized and delivered review
	// materials. It is omitted for deterministic judges and older results.
	JudgeContext *ContextMetadata `json:"judge_context,omitempty"`
}

// ContextMetadata is report-facing metadata for agent_judge context
// materialization and prompt delivery.
type ContextMetadata struct {
	Profile         string           `json:"profile"`
	MaterializedDir string           `json:"materialized_dir,omitempty"`
	Manifest        *ContextManifest `json:"manifest,omitempty"`
	PromptDelivery  string           `json:"prompt_delivery,omitempty"`
	PromptBytes     int              `json:"prompt_bytes,omitempty"`
}

// SessionResultError preserves a judge-side session result when evaluation fails.
// Callers can unwrap the original error and still recover artifacts for debugging.
type SessionResultError struct {
	Err     error
	Session *agent.SessionResult
}

func (e *SessionResultError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *SessionResultError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// SessionResultFromError extracts a preserved session result from an error chain.
func SessionResultFromError(err error) *agent.SessionResult {
	var withSession *SessionResultError
	if errors.As(err, &withSession) {
		return withSession.Session
	}
	return nil
}

// AssertionResult records the outcome of a single assertion or criterion.
//
// Mapping to result.json assertion_results[]; the Anthropic-compatible
// grading.json projection intentionally excludes Diagnosis:
//
//	{ "text": "...", "passed": true, "evidence": "..." }
type AssertionResult struct {
	// Text describes the assertion (e.g. "Output identifies the null-pointer dereference bug").
	Text string `json:"text"`

	// Passed indicates whether this assertion was satisfied.
	Passed bool `json:"passed"`

	// Evidence provides the concrete reason for the pass/fail determination.
	Evidence string `json:"evidence"`

	// Diagnosis describes the likely cause and next action for a failed
	// agent-judge criterion. It is optional and never changes the verdict.
	Diagnosis *FailureDiagnosis `json:"diagnosis,omitempty"`
}

// FailureAttribution is the primary likely cause assigned to a failure.
type FailureAttribution string

// Supported failure attribution categories.
const (
	FailureAttributionSkillNotTriggered             FailureAttribution = "skill_not_triggered"
	FailureAttributionSkillMissingInfo              FailureAttribution = "skill_missing_info"
	FailureAttributionSkillMisleadingInfo           FailureAttribution = "skill_misleading_info"
	FailureAttributionCaseDesignIssue               FailureAttribution = "case_design_issue"
	FailureAttributionEnvironmentConfiguration      FailureAttribution = "environment_configuration"
	FailureAttributionExternalDependencyUnavailable FailureAttribution = "external_dependency_unavailable"
	FailureAttributionInfrastructureError           FailureAttribution = "infrastructure_error"
	FailureAttributionAgentCapability               FailureAttribution = "agent_capability"
	FailureAttributionUndetermined                  FailureAttribution = "undetermined"
	FailureAttributionOther                         FailureAttribution = "other"
)

// DiagnosisConfidence expresses confidence in a failure attribution, not in
// the original pass/fail verdict.
type DiagnosisConfidence string

// Supported diagnostic confidence levels.
const (
	DiagnosisConfidenceLow    DiagnosisConfidence = "low"
	DiagnosisConfidenceMedium DiagnosisConfidence = "medium"
	DiagnosisConfidenceHigh   DiagnosisConfidence = "high"
)

// FailureDiagnosis is an optional, AI-generated explanation of a failure.
// AttributionEvidence explains the causal classification, while the existing
// AssertionResult.Evidence remains the evidence for the verdict itself.
type FailureDiagnosis struct {
	FailureAttribution    FailureAttribution  `json:"failure_attribution"`
	Confidence            DiagnosisConfidence `json:"confidence,omitempty"`
	AttributionEvidence   string              `json:"attribution_evidence"`
	ImprovementSuggestion string              `json:"improvement_suggestion"`
}

// ResultSummary aggregates assertion statistics.
//
// Mapping to grading.json summary:
//
//	{ "passed": 1, "failed": 1, "total": 2, "pass_rate": 0.5 }
type ResultSummary struct {
	Passed   int     `json:"passed"`
	Failed   int     `json:"failed"`
	Total    int     `json:"total"`
	PassRate float64 `json:"pass_rate"`
}

// NewResult creates a Result from a list of assertion results and turn info.
func NewResult(assertions []AssertionResult, turnsExecuted, turnsTotal int) *Result {
	passed, failed := 0, 0
	for _, a := range assertions {
		if a.Passed {
			passed++
		} else {
			failed++
		}
	}
	total := passed + failed
	var passRate float64
	if total > 0 {
		passRate = float64(passed) / float64(total)
	} else {
		passRate = 1.0 // no assertions = vacuous pass
	}

	status := StatusPass
	if failed > 0 {
		status = StatusFail
	}

	return &Result{
		Status:        status,
		TurnsExecuted: turnsExecuted,
		TurnsTotal:    turnsTotal,
		AssertionResults: func() []AssertionResult {
			if assertions == nil {
				return []AssertionResult{}
			}
			return assertions
		}(),
		Summary: ResultSummary{
			Passed:   passed,
			Failed:   failed,
			Total:    total,
			PassRate: passRate,
		},
	}
}

// NewSkipResult creates a Result with SKIP status and a reason.
func NewSkipResult(reason string, turnsExecuted, turnsTotal int) *Result {
	return &Result{
		Status:           StatusSkip,
		SkipReason:       &reason,
		TurnsExecuted:    turnsExecuted,
		TurnsTotal:       turnsTotal,
		AssertionResults: []AssertionResult{},
		Summary:          ResultSummary{},
	}
}

// NewErrorResult creates a Result with ERROR status from an error.
func NewErrorResult(err error, turnsExecuted, turnsTotal int) *Result {
	reason := err.Error()
	return &Result{
		Status:           StatusError,
		ErrorReason:      &reason,
		TurnsExecuted:    turnsExecuted,
		TurnsTotal:       turnsTotal,
		AssertionResults: []AssertionResult{},
		Summary:          ResultSummary{},
	}
}

// fileExistsInWorkspace checks whether a file exists within the workspace.
// It validates path safety (no traversal) and returns:
//   - (true, nil) if the file exists
//   - (false, nil) if the file does not exist
//   - (false, err) if the path is invalid or a stat error occurs
func fileExistsInWorkspace(workspace, rel string) (bool, error) {
	abs, err := safePath(workspace, rel)
	if err != nil {
		return false, err
	}
	_, statErr := os.Stat(abs)
	if statErr == nil {
		return true, nil
	}
	if os.IsNotExist(statErr) {
		return false, nil
	}
	return false, statErr
}

// safePath validates that a relative path does not escape the workspace directory
// via path traversal (e.g. "../../../etc/passwd").
// Returns the cleaned absolute path, or an error if the path escapes the workspace.
func safePath(workspace, rel string) (string, error) {
	abs := filepath.Join(workspace, rel)
	clean := filepath.Clean(abs)
	workspaceClean := filepath.Clean(workspace)
	if clean == workspaceClean {
		return clean, nil
	}
	if !strings.HasPrefix(clean, workspaceClean+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes workspace", rel)
	}
	return clean, nil
}
