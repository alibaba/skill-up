package judge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

const (
	// DefaultPassThreshold is the default minimum pass rate for agent_judge.
	DefaultPassThreshold = 0.7

	agentJudgeRawResponseAttempt1 = "raw-response-attempt-1.txt"
	agentJudgeRawResponseAttempt2 = "raw-response-attempt-2.txt"
	agentJudgeRetryArtifactDir    = "retry"
)

type agentJudgeCorrectionMode uint8

const (
	agentJudgeCorrectionIndependent agentJudgeCorrectionMode = iota
	agentJudgeCorrectionResumed
)

// ---------------------------------------------------------------------------
// Data types for agent_judge JSON parsing
// ---------------------------------------------------------------------------

// CriterionResult is the agent's assessment of a single criterion.
type CriterionResult struct {
	// CriterionID is the stable identifier assigned by skill-up.
	CriterionID string `json:"criterion_id"`

	// Passed indicates whether the criterion was met. A pointer distinguishes a
	// missing field from an explicit false value.
	Passed *bool `json:"passed"`

	// Evidence contains concrete observations supporting the judgment.
	Evidence []string `json:"evidence"`

	// Failures contains unmet requirements for a failed criterion.
	Failures []string `json:"failures"`

	// Diagnosis is kept as raw JSON so optional diagnostic mistakes never
	// invalidate an otherwise valid, strict verdict response.
	Diagnosis json.RawMessage `json:"diagnosis,omitempty"`
}

// judgeResponse is the expected JSON structure from the agent judge output.
type judgeResponse struct {
	Results []CriterionResult `json:"results"`
}

// ---------------------------------------------------------------------------
// AgentJudge implementation
// ---------------------------------------------------------------------------

// AgentJudge uses an agent.Agent to grade outputs against criteria.
//
// The agent receives the judge prompt (transcript + workspace diff + criteria)
// and returns a JSON response with per-criterion pass/fail and evidence.
//
// Design doc: "the judge agent emits pass/fail and concrete evidence for each
// criterion; the overall result passes when passed_criteria / total_criteria
// ≥ pass_threshold".
type AgentJudge struct {
	// Agent is the agent used for judge evaluation.
	Agent agent.Agent

	// Runtime is the runtime environment for the agent.
	Runtime runtime.Runtime

	// Model is the judge model identifier.
	Model string

	// Criteria are the evaluation standards.
	Criteria []string

	// PassThreshold is the minimum pass rate (default 0.7).
	PassThreshold float64

	// TimeoutSeconds bounds a single Evaluate call. <=0 means no judge-level
	// deadline; the parent context still applies.
	TimeoutSeconds int

	// JudgeSkills are installed judge Skills that must guide grading.
	JudgeSkills []SkillInfo

	// Context controls which evaluation materials are written, referenced, or
	// inlined for this agent_judge invocation.
	Context *config.JudgeContextConfig
}

// NewAgentJudge creates an AgentJudge with sensible defaults.
func NewAgentJudge(ag agent.Agent, rt runtime.Runtime, model string, criteria []string, passThreshold *float64, timeoutSeconds int, judgeSkills ...[]SkillInfo) *AgentJudge {
	return NewAgentJudgeWithContextAndSkills(ag, rt, model, criteria, passThreshold, nil, timeoutSeconds, judgeSkills...)
}

// NewAgentJudgeWithContext creates an AgentJudge with explicit context delivery.
func NewAgentJudgeWithContext(ag agent.Agent, rt runtime.Runtime, model string, criteria []string, passThreshold *float64, contextCfg *config.JudgeContextConfig, timeoutSeconds int) *AgentJudge {
	return NewAgentJudgeWithContextAndSkills(ag, rt, model, criteria, passThreshold, contextCfg, timeoutSeconds)
}

// NewAgentJudgeWithContextAndSkills creates an AgentJudge with context delivery and judge Skills.
func NewAgentJudgeWithContextAndSkills(ag agent.Agent, rt runtime.Runtime, model string, criteria []string, passThreshold *float64, contextCfg *config.JudgeContextConfig, timeoutSeconds int, judgeSkills ...[]SkillInfo) *AgentJudge {
	threshold := DefaultPassThreshold
	if passThreshold != nil {
		threshold = *passThreshold
	}
	var skills []SkillInfo
	if len(judgeSkills) > 0 {
		skills = judgeSkills[0]
	}
	return &AgentJudge{
		Agent:          ag,
		Runtime:        rt,
		Model:          model,
		Criteria:       criteria,
		PassThreshold:  threshold,
		TimeoutSeconds: timeoutSeconds,
		JudgeSkills:    skills,
		Context:        contextCfg,
	}
}

// Evaluate implements the Judge interface.
func (j *AgentJudge) Evaluate(ctx context.Context, in Input) (*Result, error) {
	if len(j.Criteria) == 0 {
		return NewResult(nil, in.TurnsExecuted, in.TurnsTotal), nil
	}

	parentCtx := ctx
	if j.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(j.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	materialized, err := MaterializeJudgeContext(ctx, j.Runtime, j.Context, in, in.ArtifactDir)
	if err != nil {
		return nil, fmt.Errorf("agent_judge materialize context: %w", err)
	}

	// Build the judge prompt.
	prompt := buildJudgePrompt(ctx, j.Criteria, materialized, j.JudgeSkills)
	messages := []transcript.Message{{Role: transcript.RoleUser, Content: prompt, Turn: 1}}

	// Snapshot parentCtx.Err() immediately after each call so a parent timer
	// firing later is not mislabeled as judge.timeout_seconds.
	firstSession, runErr := j.Agent.Run(ctx, j.Runtime, agent.ExecOptions{ArtifactDir: in.ArtifactDir}, messages)
	if firstSession == nil && runErr == nil {
		runErr = errors.New("agent returned no session result")
	}
	parentErr := parentCtx.Err()
	persistAgentJudgeAttemptArtifacts(ctx, j.Runtime, in.ArtifactDir, in.ArtifactDir, agentJudgeRawResponseAttempt1, firstSession)
	if callErr := j.agentCallError(ctx, runErr, firstSession, parentErr, "agent call"); callErr != nil {
		return nil, &SessionResultError{Err: callErr, Session: firstSession}
	}

	criterionResults, validationErr := parseAgentJudgeResults(j.Criteria, firstSession.FinalMessage)
	if validationErr == nil {
		return j.buildResult(in, firstSession, materialized, prompt, criterionResults), nil
	}
	if ctx.Err() != nil {
		return nil, &SessionResultError{
			Err: fmt.Errorf(
				"agent_judge output validation failed and correction retry could not start: %w",
				errors.Join(validationErr, ctx.Err()),
			),
			Session: firstSession,
		}
	}
	logging.WarnContextf(ctx, "agent_judge output failed validation; retrying once with correction guidance: %v", validationErr)

	correctionPrompt := buildAgentJudgeCorrectionPrompt(j.Criteria, validationErr, materialized)
	retryArtifactDir := ""
	if in.ArtifactDir != "" {
		retryArtifactDir = filepath.Join(in.ArtifactDir, agentJudgeRetryArtifactDir)
	}
	retrySession, correctionMode, retryErr := j.runAgentJudgeCorrection(ctx, firstSession, prompt, correctionPrompt, retryArtifactDir)
	if retrySession == nil && retryErr == nil {
		retryErr = errors.New("agent returned no session result")
	}
	parentErr = parentCtx.Err()
	persistAgentJudgeAttemptArtifacts(ctx, j.Runtime, retryArtifactDir, in.ArtifactDir, agentJudgeRawResponseAttempt2, retrySession)
	aggregateSession := aggregateAgentJudgeSessions(firstSession, retrySession, correctionMode)
	if callErr := j.agentCallError(ctx, retryErr, retrySession, parentErr, "correction call"); callErr != nil {
		return nil, &SessionResultError{
			Err: fmt.Errorf(
				"agent_judge correction retry failed after initial validation error %q: %w",
				validationErr.Error(),
				callErr,
			),
			Session: aggregateSession,
		}
	}

	criterionResults, correctionErr := parseAgentJudgeResults(j.Criteria, retrySession.FinalMessage)
	if correctionErr != nil {
		return nil, &SessionResultError{
			Err: fmt.Errorf(
				"agent_judge correction retry remained invalid after initial validation error %q: %w",
				validationErr.Error(),
				correctionErr,
			),
			Session: aggregateSession,
		}
	}
	logging.DebugContextf(ctx, "agent_judge correction retry produced a valid response")
	return j.buildResult(in, aggregateSession, materialized, prompt, criterionResults), nil
}

func (j *AgentJudge) agentCallError(ctx context.Context, err error, sessionResult *agent.SessionResult, parentErr error, callLabel string) error {
	if err == nil {
		return nil
	}
	annotated := err
	if parentErr == nil {
		annotated = j.annotateTimeoutError(ctx, err)
	}
	if !canRecoverAgentJudgeResult(err, sessionResult) {
		return fmt.Errorf("agent_judge %s failed: %w", callLabel, annotated)
	}
	if callLabel == "agent call" {
		logging.WarnContextf(
			ctx,
			"agent_judge recovering judge output despite agent error: %v (judge.timeout_seconds=%d, parent_ctx_expired=%t)",
			err,
			j.TimeoutSeconds,
			parentErr != nil,
		)
	} else {
		logging.WarnContextf(
			ctx,
			"agent_judge recovering output from %s despite agent error: %v (judge.timeout_seconds=%d, parent_ctx_expired=%t)",
			callLabel,
			err,
			j.TimeoutSeconds,
			parentErr != nil,
		)
	}
	return nil
}

func (j *AgentJudge) runAgentJudgeCorrection(
	ctx context.Context,
	firstSession *agent.SessionResult,
	originalPrompt,
	correctionPrompt,
	artifactDir string,
) (*agent.SessionResult, agentJudgeCorrectionMode, error) {
	opts := agent.ExecOptions{ArtifactDir: artifactDir}
	if resumer, ok := j.Agent.(agent.SessionResumer); ok && firstSession != nil && firstSession.SessionID != "" {
		sessionResult, err := resumer.RunTurn(
			ctx,
			j.Runtime,
			opts,
			transcript.Message{Role: transcript.RoleUser, Content: correctionPrompt, Turn: 2},
			firstSession.SessionID,
		)
		return sessionResult, agentJudgeCorrectionResumed, err
	}

	invalidResponse := ""
	if firstSession != nil {
		invalidResponse = firstSession.FinalMessage
	}
	fallbackPrompt := buildAgentJudgeFallbackCorrectionPrompt(originalPrompt, invalidResponse, correctionPrompt)
	sessionResult, err := j.Agent.Run(ctx, j.Runtime, opts, []transcript.Message{{
		Role:    transcript.RoleUser,
		Content: fallbackPrompt,
		Turn:    1,
	}})
	return sessionResult, agentJudgeCorrectionIndependent, err
}

func parseAgentJudgeResults(criteria []string, output string) ([]CriterionResult, error) {
	var resp judgeResponse
	if err := decodeAgentJudgeResponse(output, &resp); err != nil {
		return nil, fmt.Errorf("agent_judge failed to parse agent output: %w", err)
	}
	return validateAgentJudgeResponse(criteria, resp.Results)
}

func persistAgentJudgeAttemptArtifacts(
	ctx context.Context,
	rt runtime.Runtime,
	attemptArtifactDir,
	rawArtifactDir,
	rawFileName string,
	sessionResult *agent.SessionResult,
) {
	snapshotAgentJudgeAttemptArtifacts(ctx, rt, attemptArtifactDir, sessionResult)
	persistAgentJudgeRawResponse(ctx, rawArtifactDir, rawFileName, sessionResult)
}

func persistAgentJudgeRawResponse(ctx context.Context, artifactDir, fileName string, sessionResult *agent.SessionResult) {
	if artifactDir == "" || sessionResult == nil {
		return
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		logging.WarnContextf(ctx, "agent_judge failed to create raw response artifact directory %s: %v", artifactDir, err)
		return
	}
	path := filepath.Join(artifactDir, fileName)
	if err := os.WriteFile(path, []byte(sessionResult.FinalMessage), 0o600); err != nil {
		logging.WarnContextf(ctx, "agent_judge failed to persist raw response artifact %s: %v", path, err)
		return
	}
	if sessionResult.Artifacts == nil {
		sessionResult.Artifacts = &agent.SessionArtifacts{}
	}
	sessionResult.Artifacts.GeneratedFiles = appendUniqueString(sessionResult.Artifacts.GeneratedFiles, path)
}

// snapshotAgentJudgeAttemptArtifacts preserves runtime-backed artifacts before
// a correction attempt can overwrite their workspace paths. Artifacts already
// materialized inside the attempt directory are left untouched. Snapshot
// failures are best-effort: the original path remains available to the
// evaluator and the Judge result is not changed.
func snapshotAgentJudgeAttemptArtifacts(
	ctx context.Context,
	rt runtime.Runtime,
	artifactDir string,
	sessionResult *agent.SessionResult,
) {
	if artifactDir == "" || sessionResult == nil || sessionResult.Artifacts == nil {
		return
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		logging.WarnContextf(ctx, "agent_judge failed to create attempt artifact directory %s: %v", artifactDir, err)
		return
	}

	generatedFiles := sessionResult.Artifacts.GeneratedFiles
	for i, sourcePath := range generatedFiles {
		if sourcePath == "" || pathWithinDir(sourcePath, artifactDir) {
			continue
		}
		targetPath := filepath.Join(artifactDir, filepath.Base(sourcePath))
		if err := rt.DownloadFile(ctx, sourcePath, targetPath); err != nil {
			logging.WarnContextf(ctx, "agent_judge failed to snapshot attempt artifact %s to %s: %v", sourcePath, targetPath, err)
			continue
		}
		generatedFiles[i] = targetPath
	}
	sessionResult.Artifacts.GeneratedFiles = appendUniqueStrings(nil, generatedFiles)
}

func pathWithinDir(path, dir string) bool {
	cleanPath := filepath.Clean(path)
	cleanDir := filepath.Clean(dir)
	if !filepath.IsAbs(cleanPath) || !filepath.IsAbs(cleanDir) {
		return false
	}
	rel, err := filepath.Rel(cleanDir, cleanPath)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func aggregateAgentJudgeSessions(first, second *agent.SessionResult, correctionMode agentJudgeCorrectionMode) *agent.SessionResult {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}

	aggregate := *second
	aggregate.DurationMs = first.DurationMs + second.DurationMs
	if correctionMode == agentJudgeCorrectionResumed && judgeSessionMetricsAreCumulative(first, second) {
		aggregate.InputTokens = max(first.InputTokens, second.InputTokens)
		aggregate.OutputTokens = max(first.OutputTokens, second.OutputTokens)
		aggregate.Turns = max(first.Turns, second.Turns)
	} else {
		aggregate.InputTokens = first.InputTokens + second.InputTokens
		aggregate.OutputTokens = first.OutputTokens + second.OutputTokens
		aggregate.Turns = first.Turns + second.Turns
	}
	aggregate.Artifacts = mergeAgentJudgeSessionArtifacts(first.Artifacts, second.Artifacts)
	if first.PromptDelivery != nil {
		aggregate.PromptDelivery = first.PromptDelivery
	}
	return &aggregate
}

func judgeSessionMetricsAreCumulative(first, second *agent.SessionResult) bool {
	if len(first.Transcript) == 0 || len(second.Transcript) < len(first.Transcript) {
		return false
	}
	for i := range first.Transcript {
		if !reflect.DeepEqual(first.Transcript[i], second.Transcript[i]) {
			return false
		}
	}
	return true
}

func mergeAgentJudgeSessionArtifacts(first, second *agent.SessionArtifacts) *agent.SessionArtifacts {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}

	merged := *second
	if merged.WorkspaceDiff == "" {
		merged.WorkspaceDiff = first.WorkspaceDiff
	}
	merged.GeneratedFiles = appendUniqueStrings(first.GeneratedFiles, second.GeneratedFiles)
	merged.Files = appendUniqueArtifactFiles(first.Files, second.Files)
	switch {
	case first.Logs == "":
	case merged.Logs == "":
		merged.Logs = first.Logs
	case first.Logs != merged.Logs:
		merged.Logs = first.Logs + "\n" + merged.Logs
	}
	return &merged
}

func appendUniqueStrings(groups ...[]string) []string {
	seen := make(map[string]struct{})
	var values []string
	for _, group := range groups {
		for _, value := range group {
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			values = append(values, value)
		}
	}
	return values
}

func appendUniqueString(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

func appendUniqueArtifactFiles(groups ...[]agent.ArtifactFile) []agent.ArtifactFile {
	seen := make(map[agent.ArtifactFile]struct{})
	var files []agent.ArtifactFile
	for _, group := range groups {
		for _, file := range group {
			if _, ok := seen[file]; ok {
				continue
			}
			seen[file] = struct{}{}
			files = append(files, file)
		}
	}
	return files
}

func (j *AgentJudge) buildResult(in Input, sessionResult *agent.SessionResult, materialized *MaterializedContext, prompt string, criterionResults []CriterionResult) *Result {
	assertions := make([]AssertionResult, 0, len(criterionResults))
	for i, cr := range criterionResults {
		var diagnosis *FailureDiagnosis
		if !*cr.Passed {
			diagnosis = normalizeFailureDiagnosis(cr.Diagnosis, in)
		}
		assertions = append(assertions, AssertionResult{
			Text:      j.Criteria[i],
			Passed:    *cr.Passed,
			Evidence:  formatCriterionEvidence(cr),
			Diagnosis: diagnosis,
		})
	}

	result := NewResult(assertions, in.TurnsExecuted, in.TurnsTotal)
	result.JudgeSession = sessionResult
	result.JudgeContext = buildContextMetadata(sessionResult, materialized, prompt)
	if len(assertions) > 0 && result.Summary.PassRate >= j.PassThreshold {
		result.Status = StatusPass
	} else if len(assertions) > 0 {
		result.Status = StatusFail
	}
	return result
}

func buildContextMetadata(sessionResult *agent.SessionResult, materialized *MaterializedContext, prompt string) *ContextMetadata {
	metadata := &ContextMetadata{
		Profile:         materialized.Manifest.Profile,
		MaterializedDir: materialized.Manifest.MaterializedDir,
		Manifest:        &materialized.Manifest,
		PromptBytes:     len([]byte(prompt)),
	}
	if sessionResult != nil && sessionResult.PromptDelivery != nil {
		metadata.PromptDelivery = sessionResult.PromptDelivery.Mode
		metadata.PromptBytes = sessionResult.PromptDelivery.PromptBytes
	}
	return metadata
}

func validateAgentJudgeResponse(criteria []string, results []CriterionResult) ([]CriterionResult, error) {
	if results == nil {
		return nil, errors.New("agent_judge: results is required and must be an array")
	}
	// A short response inflates pass_rate because NewResult uses the returned
	// count as the denominator (e.g. 1-of-1 returned = 100% even if 3 were sent).
	if len(results) != len(criteria) {
		return nil, fmt.Errorf("agent_judge: expected %d criterion results, got %d", len(criteria), len(results))
	}

	expected := make(map[string]int, len(criteria))
	for i := range criteria {
		expected[criterionID(i)] = i
	}
	ordered := make([]CriterionResult, len(criteria))
	seen := make(map[string]struct{}, len(results))

	for i, cr := range results {
		if strings.TrimSpace(cr.CriterionID) == "" {
			return nil, fmt.Errorf("agent_judge: result %d is missing criterion_id", i+1)
		}
		position, ok := expected[cr.CriterionID]
		if !ok {
			return nil, fmt.Errorf("agent_judge: result %d has unknown criterion_id %q", i+1, cr.CriterionID)
		}
		if _, duplicate := seen[cr.CriterionID]; duplicate {
			return nil, fmt.Errorf("agent_judge: duplicate criterion_id %q", cr.CriterionID)
		}
		seen[cr.CriterionID] = struct{}{}

		normalized, err := validateCriterionResultFields(cr)
		if err != nil {
			return nil, err
		}

		ordered[position] = normalized
	}

	for id := range expected {
		if _, ok := seen[id]; !ok {
			return nil, fmt.Errorf("agent_judge: missing criterion_id %q", id)
		}
	}
	return ordered, nil
}

func validateCriterionResultFields(result CriterionResult) (CriterionResult, error) {
	if result.Passed == nil {
		return CriterionResult{}, fmt.Errorf("agent_judge: criterion %q is missing passed", result.CriterionID)
	}
	if result.Evidence == nil {
		return CriterionResult{}, fmt.Errorf("agent_judge: criterion %q is missing evidence", result.CriterionID)
	}
	if len(result.Evidence) == 0 {
		return CriterionResult{}, fmt.Errorf("agent_judge: criterion %q has empty evidence", result.CriterionID)
	}
	for evidenceIndex := range result.Evidence {
		result.Evidence[evidenceIndex] = strings.TrimSpace(result.Evidence[evidenceIndex])
		if result.Evidence[evidenceIndex] == "" {
			return CriterionResult{}, fmt.Errorf("agent_judge: criterion %q evidence[%d] is empty", result.CriterionID, evidenceIndex)
		}
	}
	if result.Failures == nil {
		return CriterionResult{}, fmt.Errorf("agent_judge: criterion %q is missing failures", result.CriterionID)
	}
	for failureIndex := range result.Failures {
		result.Failures[failureIndex] = strings.TrimSpace(result.Failures[failureIndex])
		if result.Failures[failureIndex] == "" {
			return CriterionResult{}, fmt.Errorf("agent_judge: criterion %q failures[%d] is empty", result.CriterionID, failureIndex)
		}
	}
	if *result.Passed && len(result.Failures) != 0 {
		return CriterionResult{}, fmt.Errorf("agent_judge: criterion %q passed but reported failures", result.CriterionID)
	}
	if !*result.Passed && len(result.Failures) == 0 {
		return CriterionResult{}, fmt.Errorf("agent_judge: criterion %q failed but reported no failures", result.CriterionID)
	}
	return result, nil
}

func formatCriterionEvidence(result CriterionResult) string {
	evidence := strings.Join(result.Evidence, "; ")
	if !*result.Passed {
		evidence += " | Failures: " + strings.Join(result.Failures, "; ")
	}
	return evidence
}

func normalizeFailureDiagnosis(raw json.RawMessage, in Input) *FailureDiagnosis {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}

	var diagnosis FailureDiagnosis
	if err := json.Unmarshal(raw, &diagnosis); err != nil {
		return nil
	}
	diagnosis.FailureAttribution = FailureAttribution(strings.TrimSpace(string(diagnosis.FailureAttribution)))
	diagnosis.Confidence = DiagnosisConfidence(strings.TrimSpace(string(diagnosis.Confidence)))
	diagnosis.AttributionEvidence = strings.TrimSpace(diagnosis.AttributionEvidence)
	diagnosis.ImprovementSuggestion = strings.TrimSpace(diagnosis.ImprovementSuggestion)
	if diagnosis.FailureAttribution == "" || diagnosis.AttributionEvidence == "" || diagnosis.ImprovementSuggestion == "" {
		return nil
	}
	if !validFailureAttribution(diagnosis.FailureAttribution) {
		diagnosis.FailureAttribution = FailureAttributionOther
	}
	if !validDiagnosisConfidence(diagnosis.Confidence) {
		diagnosis.Confidence = DiagnosisConfidenceLow
	}

	if in.Configuration == configurationWithoutSkill && isSkillAttribution(diagnosis.FailureAttribution) {
		return nil
	}
	if diagnosis.FailureAttribution == FailureAttributionSkillNotTriggered &&
		(in.SkillUsage == nil || !in.SkillUsage.Reliable || in.SkillUsage.Status != SkillUsageNotTriggered) {
		return nil
	}
	return &diagnosis
}

func validFailureAttribution(attribution FailureAttribution) bool {
	switch attribution {
	case FailureAttributionSkillNotTriggered,
		FailureAttributionSkillMissingInfo,
		FailureAttributionSkillMisleadingInfo,
		FailureAttributionCaseDesignIssue,
		FailureAttributionEnvironmentConfiguration,
		FailureAttributionExternalDependencyUnavailable,
		FailureAttributionInfrastructureError,
		FailureAttributionAgentCapability,
		FailureAttributionUndetermined,
		FailureAttributionOther:
		return true
	default:
		return false
	}
}

func validDiagnosisConfidence(confidence DiagnosisConfidence) bool {
	switch confidence {
	case DiagnosisConfidenceLow, DiagnosisConfidenceMedium, DiagnosisConfidenceHigh:
		return true
	default:
		return false
	}
}

func isSkillAttribution(attribution FailureAttribution) bool {
	return attribution == FailureAttributionSkillNotTriggered ||
		attribution == FailureAttributionSkillMissingInfo ||
		attribution == FailureAttributionSkillMisleadingInfo
}

// decodeAgentJudgeResponse accepts one JSON object, optionally wrapped in one
// complete JSON code fence, and rejects unknown fields or trailing values.
func decodeAgentJudgeResponse(output string, v any) error {
	candidate, err := agentJudgeJSONCandidate(output)
	if err != nil {
		return err
	}
	if err := validateAgentJudgeJSONKeys(candidate); err != nil {
		return fmt.Errorf("invalid JSON response: %w", err)
	}

	decoder := json.NewDecoder(strings.NewReader(candidate))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON response: %w", err)
	}

	var trailing any
	err = decoder.Decode(&trailing)
	if !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("agent_judge response contains multiple JSON values")
		}
		return fmt.Errorf("agent_judge response has trailing content: %w", err)
	}
	return nil
}

type agentJudgeJSONContext uint8

const (
	agentJudgeJSONAny agentJudgeJSONContext = iota
	agentJudgeJSONResponse
	agentJudgeJSONResults
	agentJudgeJSONResult
	agentJudgeJSONDiagnosis
)

// validateAgentJudgeJSONKeys scans the JSON token stream before decoding into
// structs because encoding/json accepts duplicate keys using the last value and
// matches struct fields case-insensitively. The wire contract requires unique,
// exactly spelled field names at both object levels.
func validateAgentJudgeJSONKeys(input string) error {
	decoder := json.NewDecoder(strings.NewReader(input))
	return scanAgentJudgeJSONValue(decoder, agentJudgeJSONResponse)
}

func scanAgentJudgeJSONValue(decoder *json.Decoder, contractContext agentJudgeJSONContext) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}

	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if duplicateAgentJudgeJSONKey(seen, key, contractContext) {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			childContext, allowed := agentJudgeJSONChildContext(contractContext, key)
			if !allowed {
				return fmt.Errorf("JSON object key %q is not allowed in the agent_judge contract", key)
			}
			if err := scanAgentJudgeJSONValue(decoder, childContext); err != nil {
				return err
			}
		}
	case '[':
		childContext := agentJudgeJSONAny
		if contractContext == agentJudgeJSONResults {
			childContext = agentJudgeJSONResult
		}
		for decoder.More() {
			if err := scanAgentJudgeJSONValue(decoder, childContext); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}

	_, err = decoder.Token()
	return err
}

func duplicateAgentJudgeJSONKey(seen map[string]struct{}, key string, contractContext agentJudgeJSONContext) bool {
	_, exists := seen[key]
	return exists && contractContext != agentJudgeJSONDiagnosis
}

func agentJudgeJSONChildContext(contractContext agentJudgeJSONContext, key string) (agentJudgeJSONContext, bool) {
	switch contractContext {
	case agentJudgeJSONResponse:
		if key == "results" {
			return agentJudgeJSONResults, true
		}
		return agentJudgeJSONAny, false
	case agentJudgeJSONResult:
		switch key {
		case "criterion_id", "passed", "evidence", "failures":
			return agentJudgeJSONAny, true
		case "diagnosis":
			return agentJudgeJSONDiagnosis, true
		default:
			return agentJudgeJSONAny, false
		}
	case agentJudgeJSONDiagnosis:
		return agentJudgeJSONDiagnosis, true
	default:
		return agentJudgeJSONAny, true
	}
}

func agentJudgeJSONCandidate(output string) (string, error) {
	trimmed := strings.TrimSpace(output)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed, nil
	}

	lineEnd := strings.IndexByte(trimmed, '\n')
	if lineEnd < 0 || !strings.EqualFold(strings.TrimSpace(trimmed[3:lineEnd]), "json") {
		return "", errors.New("agent_judge response must be JSON or a single JSON code fence")
	}
	remainder := strings.TrimSpace(trimmed[lineEnd+1:])
	if !strings.HasSuffix(remainder, "```") {
		return "", errors.New("agent_judge JSON code fence is not closed")
	}
	candidate := strings.TrimSpace(strings.TrimSuffix(remainder, "```"))
	return candidate, nil
}

func criterionID(index int) string {
	return fmt.Sprintf("criterion-%d", index+1)
}

// annotateTimeoutError tags a context.DeadlineExceeded chain with the
// judge-level timeout knob and seconds. The caller (Evaluate) decides whether
// to invoke this — specifically, only when the parent context had not already
// expired at the moment Run returned — so this function does not re-check the
// parent. It only verifies that *our* judge ctx really fired before labeling
// (an upstream HTTP layer can also return DeadlineExceeded with its own
// shorter deadline, which is not ours to label).
func (j *AgentJudge) annotateTimeoutError(judgeCtx context.Context, err error) error {
	if err == nil || j.TimeoutSeconds <= 0 {
		return err
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if judgeCtx.Err() == nil {
		return err
	}
	return fmt.Errorf("%w (judge timeout %ds via judge.timeout_seconds)", err, j.TimeoutSeconds)
}

func canRecoverAgentJudgeResult(err error, sessionResult *agent.SessionResult) bool {
	if err == nil || sessionResult == nil {
		return false
	}
	if strings.TrimSpace(sessionResult.FinalMessage) == "" {
		return false
	}
	// Never recover from explicit cancellation.
	if errors.Is(err, context.Canceled) {
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Prompt builder
// ---------------------------------------------------------------------------

// buildJudgePrompt constructs the system + user prompt for the judge agent.
func buildJudgePrompt(_ context.Context, criteria []string, materialized *MaterializedContext, judgeSkills ...[]SkillInfo) string {
	var sb strings.Builder
	var skills []SkillInfo
	if len(judgeSkills) > 0 {
		skills = judgeSkills[0]
	}

	sb.WriteString("You are an expert evaluator for an AI agent skill evaluation.\n")
	sb.WriteString("You must assess the agent's output against the following criteria.\n")
	sb.WriteString("For EACH criterion, you MUST provide:\n")
	sb.WriteString("- \"criterion_id\": the exact stable ID shown below\n")
	sb.WriteString("- \"passed\": true/false\n")
	sb.WriteString("- \"evidence\": a non-empty JSON array of concrete observations supporting your judgment\n")
	sb.WriteString("- \"failures\": an empty JSON array when passed is true, otherwise a non-empty array of unmet requirements\n\n")
	sb.WriteString("You MUST NOT pass a criterion without specific evidence.\n\n")
	appendDiagnosisInstructions(&sb, materialized)
	sb.WriteString("IMPORTANT: Return only the required JSON object, optionally wrapped in one JSON code fence. Do not add prose or extra fields. ")
	sb.WriteString("Escape double quotes inside string values (e.g. \\\"example\\\").\n\n")

	appendJudgeSkillInstructions(&sb, skills)

	sb.WriteString("## Criteria\n")
	for i, c := range criteria {
		fmt.Fprintf(&sb, "[%s] %s\n", criterionID(i), c)
	}

	appendReviewMaterials(&sb, materialized)
	appendRequiredResponseFormat(&sb, criteria)
	return sb.String()
}

func buildAgentJudgeCorrectionPrompt(criteria []string, validationErr error, materialized *MaterializedContext) string {
	encodedError, err := json.Marshal(validationErr.Error())
	if err != nil {
		encodedError = []byte(`"agent_judge response validation failed"`)
	}

	var sb strings.Builder
	sb.WriteString("## Agent Judge Output Correction\n\n")
	sb.WriteString("Your previous response failed the program-owned output contract. Correct the response using the same evaluation. ")
	sb.WriteString("Do not add commentary, do not wrap the response in a Markdown fence, and do not introduce fields outside the schema.\n\n")
	sb.WriteString("Validation error (JSON string): ")
	sb.Write(encodedError)
	sb.WriteString("\n\n")
	sb.WriteString("Allowed root field: results.\n")
	sb.WriteString("Required result fields with exact casing: criterion_id, passed, evidence, failures.\n")
	sb.WriteString("The only additional result field is diagnosis, using the exact casing shown below.\n")
	sb.WriteString("Use every configured criterion_id exactly once. Evidence must be a non-empty string array. ")
	sb.WriteString("Failures must be empty when passed is true and non-empty when passed is false.\n")
	appendDiagnosisInstructions(&sb, materialized)
	appendRequiredResponseFormat(&sb, criteria)
	return sb.String()
}

func buildAgentJudgeFallbackCorrectionPrompt(originalPrompt, invalidResponse, correctionPrompt string) string {
	encodedResponse, err := json.Marshal(invalidResponse)
	if err != nil {
		encodedResponse = []byte(`""`)
	}

	var sb strings.Builder
	sb.WriteString("This is a serialized correction retry for a previous agent_judge evaluation.\n\n")
	sb.WriteString("## Original Judge Request\n\n")
	sb.WriteString(originalPrompt)
	sb.WriteString("\n\n## Previous Invalid Response (JSON string)\n\n")
	sb.Write(encodedResponse)
	sb.WriteString("\n\n")
	sb.WriteString(correctionPrompt)
	return sb.String()
}

func appendDiagnosisInstructions(sb *strings.Builder, materialized *MaterializedContext) {
	configuration := configurationWithSkill
	var usage *SkillUsageEvidence
	if materialized != nil {
		if materialized.Configuration != "" {
			configuration = materialized.Configuration
		}
		usage = materialized.SkillUsage
	}

	sb.WriteString("For each FAILED criterion, provide a \"diagnosis\" object with:\n")
	sb.WriteString("- \"failure_attribution\": one allowed category from the list below\n")
	sb.WriteString("- \"confidence\": \"low\", \"medium\", or \"high\"\n")
	sb.WriteString("- \"attribution_evidence\": why the available materials support the likely cause; do not repeat verdict evidence\n")
	sb.WriteString("- \"improvement_suggestion\": one concrete next action\n")
	sb.WriteString("Diagnosis is required for failed criteria and must be omitted for passed criteria. If the cause is not defensible from evidence, use \"undetermined\" and low confidence. Never invent evidence.\n")
	sb.WriteString("Allowed failure_attribution values: skill_not_triggered, skill_missing_info, skill_misleading_info, case_design_issue, environment_configuration, external_dependency_unavailable, infrastructure_error, agent_capability, undetermined, other.\n")
	fmt.Fprintf(sb, "Evaluation configuration: %s.\n", configuration)
	if configuration == configurationWithoutSkill {
		sb.WriteString("This is an intentional without_skill baseline. Do not use skill_not_triggered, skill_missing_info, or skill_misleading_info.\n")
	}
	if usage == nil || !usage.Reliable || usage.Status == SkillUsageUnavailable {
		sb.WriteString("Reliable Skill-usage evidence is unavailable. Do not use skill_not_triggered.\n\n")
		return
	}
	if usage.Status == SkillUsageTriggered {
		sb.WriteString("An explicit Skill invocation was observed. Do not use skill_not_triggered.\n\n")
		return
	}
	sb.WriteString("A complete engine evidence channel reports that the Skill was not triggered; skill_not_triggered may be used when relevant.\n\n")
}

func appendJudgeSkillInstructions(sb *strings.Builder, skills []SkillInfo) {
	if len(skills) == 0 {
		return
	}
	sb.WriteString("## Mandatory Judge Skill Use\n")
	sb.WriteString("Before evaluating the case, you MUST invoke the Skill tool for EACH installed judge Skill below using its callable skill name and read the full Skill body. ")
	sb.WriteString("Do not grade the case until you have loaded every listed judge Skill. ")
	sb.WriteString("Do not grade this case using only the inline criteria. The inline criteria identify result dimensions, while the judge Skill(s) define detailed rubric, constraints, and evidence rules. ")
	sb.WriteString("If an inline criterion conflicts with a judge Skill, follow the judge Skill unless the criterion defines a more specific case-level acceptance condition.\n\n")
	sb.WriteString("Installed judge Skill(s):\n")
	for _, skill := range skills {
		fmt.Fprintf(sb, "- invoke Skill tool with name %q", skillIdentifier(skill))
		if skill.Target != "" {
			fmt.Fprintf(sb, " (target: %s)", skill.Target)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
}

func appendReviewMaterials(sb *strings.Builder, materialized *MaterializedContext) {
	if materialized == nil {
		return
	}
	sb.WriteString("\n## Review Materials\n")
	sb.WriteString("Read files from this table as needed to evaluate the criteria. Evidence must be traceable to the listed material keys or inline excerpts.\n\n")
	sb.WriteString("| key | mode | path | bytes | notes |\n")
	sb.WriteString("| --- | --- | --- | ---: | --- |\n")
	for _, m := range materialized.Materials {
		notes := ""
		if m.Truncated {
			notes = "inline excerpt truncated; full text is at path"
		}
		fmt.Fprintf(sb, "| %s | %s | %s | %d | %s |\n", materialLabel(m), m.Mode, materialPromptPath(m), m.OriginalBytes, notes)
	}
	for _, m := range materialized.Materials {
		if strings.TrimSpace(m.InlineContent) == "" {
			continue
		}
		fmt.Fprintf(sb, "\n### Inline Material: %s\n", materialLabel(m))
		if m.Truncated {
			sb.WriteString("The excerpt below is truncated; read the full file at the path above if needed.\n")
		}
		sb.WriteString("```\n")
		sb.WriteString(m.InlineContent)
		sb.WriteString("\n```\n")
	}
}

func appendRequiredResponseFormat(sb *strings.Builder, criteria []string) {
	sb.WriteString("\n## Required Response Format (JSON)\n")
	sb.WriteString("Use each criterion_id exactly once. Set passed and the arrays according to your judgment. Diagnosis is null/omitted for a pass and populated for a failure.\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"results\": [\n")
	for i := range criteria {
		fmt.Fprintf(sb, "    {\"criterion_id\": %q, \"passed\": true, \"evidence\": [\"concrete observation\"], \"failures\": [], \"diagnosis\": null}", criterionID(i))
		if i < len(criteria)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("  ]\n")
	sb.WriteString("}\n")
	sb.WriteString("```\n")
}

func skillIdentifier(skill SkillInfo) string {
	if skill.Name != "" {
		return skill.Name
	}
	if skill.Path != "" {
		return skill.Path
	}
	return skill.Source
}

func materialLabel(m ContextMaterial) string {
	if m.Label != "" {
		return m.Key + ":" + m.Label
	}
	return m.Key
}

func materialPromptPath(m ContextMaterial) string {
	if m.RuntimePath != "" {
		return m.RuntimePath
	}
	return m.Path
}
