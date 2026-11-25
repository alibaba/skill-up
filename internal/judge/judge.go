// Package judge evaluates case outputs using rule_based, script, or agent_judge strategies.
package judge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
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

// WorkspaceFiles exposes the minimal workspace access needed by host-side file
// checks. Implementations may read a local directory or a remote runtime.
type WorkspaceFiles interface {
	Exists(ctx context.Context, relPath string) (bool, error)
	ReadFile(ctx context.Context, relPath string) ([]byte, error)
}

type hostPathWorkspaceFiles interface {
	hostWorkspacePaths()
}

type localWorkspaceFiles string

func (localWorkspaceFiles) hostWorkspacePaths() {}

func (l localWorkspaceFiles) Exists(_ context.Context, relPath string) (bool, error) {
	_, err := os.Stat(filepath.Join(string(l), relPath))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (l localWorkspaceFiles) ReadFile(_ context.Context, relPath string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(string(l), relPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, runtime.ErrFileNotFound
		}
		return nil, err
	}
	return data, nil
}

// NewLocalWorkspaceFiles returns a WorkspaceFiles backed by a host directory.
func NewLocalWorkspaceFiles(dir string) WorkspaceFiles {
	return localWorkspaceFiles(dir)
}

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
	// Runtime-executed judges use it as the execution working directory.
	WorkspacePath string

	// Files provides workspace file access for host-side checks.
	Files WorkspaceFiles

	// SkillDir is the absolute path to the directory containing SKILL.md.
	// Fixture-style references such as golden_file are resolved from here.
	SkillDir string

	// WorkspaceDiff is the git diff of workspace changes after engine execution.
	WorkspaceDiff string

	// GeneratedFiles lists file paths created by the agent.
	GeneratedFiles []string

	// ArtifactDir is the host directory where a judge that invokes an agent
	// should write or download its own run artifacts.
	ArtifactDir string

	// ContextArtifactDir is the context archive path relative to case outputs.
	// Empty preserves the legacy judge/context layout for standalone callers.
	ContextArtifactDir string

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

	// Recovery records when agent_judge accepted a valid result after its
	// agent call returned an error. It is carried into report.json separately so
	// grading.json remains compatible with the Anthropic grading contract.
	Recovery *RecoveryMetadata `json:"-"`
}

// RecoveryMetadata describes a recovered judge result without exposing the
// underlying agent error, which can contain provider-specific details.
type RecoveryMetadata struct {
	Recovered bool   `json:"recovered"`
	Reason    string `json:"reason"`
}

// Outcome preserves one independent judge's result and diagnostic session.
type Outcome struct {
	ID         string               `json:"id"`
	Type       string               `json:"type"`
	Status     Status               `json:"status"`
	Result     *Result              `json:"result,omitempty"`
	Error      string               `json:"error,omitempty"`
	SkipReason string               `json:"skip_reason,omitempty"`
	Artifacts  string               `json:"artifacts_dir,omitempty"`
	DurationMs int64                `json:"duration_ms"`
	Recovery   *RecoveryMetadata    `json:"recovery,omitempty"`
	Session    *agent.SessionResult `json:"-"`
	Skills     []SkillInfo          `json:"judge_skills,omitempty"`
}

// ContextMetadata is report-facing metadata for agent_judge context
// materialization and prompt delivery.
type ContextMetadata struct {
	Profile         string           `json:"profile"`
	MaterializedDir string           `json:"materialized_dir,omitempty"`
	Manifest        *ContextManifest `json:"manifest,omitempty"`
	PromptDelivery  string           `json:"prompt_delivery,omitempty"`
	PromptBytes     int              `json:"prompt_bytes,omitempty"`
	// Engine names the agent that graded the case, so a report shows whether the
	// judge was independent of the evaluated agent. Provider and Model are the
	// values skill-up forwarded to that engine; one it did not forward stays
	// empty, because the engine's own configuration then decides it.
	// ObservedModel is set only when the adapter captured a model the engine
	// reported; empty means the model that ran is unknown.
	Engine        string `json:"engine,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	ObservedModel string `json:"observed_model,omitempty"`
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
// Mapping to grading.json assertion_results[]:
//
//	{ "text": "...", "passed": true, "evidence": "..." }
type AssertionResult struct {
	// Text describes the assertion (e.g. "Output identifies the null-pointer dereference bug").
	Text string `json:"text"`

	// Passed indicates whether this assertion was satisfied.
	Passed bool `json:"passed"`

	// Skipped indicates that the assertion was not evaluated and received no credit.
	Skipped bool `json:"skipped,omitempty"`

	// Evidence provides the concrete reason for the pass/fail determination.
	Evidence string `json:"evidence"`

	// Error records an access/inconclusive error that prevents a reliable
	// pass/fail determination for this assertion.
	Error *string `json:"error,omitempty"`
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

// normalizeWorkspacePath resolves and validates a workspace-relative path using
// the runtime backend's path rules, returning the normalized path for I/O.
func normalizeWorkspacePath(in Input, rel string) (string, error) {
	_, hostPaths := in.Files.(hostPathWorkspaceFiles)
	_, noneRuntime := in.Files.(*runtime.NoneRuntime)
	if hostPaths || noneRuntime {
		clean := filepath.Clean(rel)
		if filepath.IsAbs(clean) {
			return "", fmt.Errorf("path %q escapes workspace %q", rel, in.WorkspacePath)
		}
		workspace := filepath.Clean(in.WorkspacePath)
		joined := filepath.Join(workspace, clean)
		if joined != workspace && !strings.HasPrefix(joined, workspace+string(filepath.Separator)) {
			return "", fmt.Errorf("path %q escapes workspace %q", rel, in.WorkspacePath)
		}
		return clean, nil
	}
	workspace := path.Clean(in.WorkspacePath)
	slash := strings.ReplaceAll(rel, "\\", "/")
	if len(slash) >= 2 && slash[1] == ':' {
		return "", fmt.Errorf("path %q escapes workspace %q", rel, workspace)
	}
	clean := path.Clean(slash)
	if path.IsAbs(clean) {
		return "", fmt.Errorf("path %q escapes workspace %q", rel, workspace)
	}
	joined := path.Join(workspace, clean)
	if joined != workspace && !strings.HasPrefix(joined, workspace+"/") {
		return "", fmt.Errorf("path %q escapes workspace %q", rel, workspace)
	}
	return clean, nil
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
