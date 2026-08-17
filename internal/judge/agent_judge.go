package judge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// DefaultPassThreshold is the default minimum pass rate for agent_judge.
const DefaultPassThreshold = 0.7

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

	// Get criterion results via agent.Agent. Snapshot parentCtx.Err() the
	// instant Run returns, before parent's timer has any chance to fire on
	// its own; this is how we distinguish a judge-level deadline from a
	// parent (case-level) one and decide whether to annotate. Reading
	// parentCtx.Err() later would race against the parent timer in the
	// caseTimeout ≈ judgeTimeout boundary.
	sessionResult, err := j.Agent.Run(ctx, j.Runtime, agent.ExecOptions{ArtifactDir: in.ArtifactDir}, messages)
	parentExpired := parentCtx.Err() != nil
	if err != nil {
		annotated := err
		if !parentExpired {
			annotated = j.annotateTimeoutError(ctx, err)
		}
		if !canRecoverAgentJudgeResult(err, sessionResult) {
			return nil, &SessionResultError{
				Err:     fmt.Errorf("agent_judge agent call failed: %w", annotated),
				Session: sessionResult,
			}
		}
		logging.WarnContextf(ctx, "agent_judge recovering judge output despite agent error: %v (judge.timeout_seconds=%d, parent_ctx_expired=%t)", err, j.TimeoutSeconds, parentExpired)
	}

	var resp judgeResponse
	if err := decodeAgentJudgeResponse(sessionResult.FinalMessage, &resp); err != nil {
		return nil, &SessionResultError{
			Err:     fmt.Errorf("agent_judge failed to parse agent output: %w", err),
			Session: sessionResult,
		}
	}
	criterionResults, err := validateAgentJudgeResponse(j.Criteria, resp.Results)
	if err != nil {
		return nil, &SessionResultError{
			Err:     err,
			Session: sessionResult,
		}
	}

	return j.buildResult(in, sessionResult, materialized, prompt, criterionResults), nil
}

func (j *AgentJudge) buildResult(in Input, sessionResult *agent.SessionResult, materialized *MaterializedContext, prompt string, criterionResults []CriterionResult) *Result {
	assertions := make([]AssertionResult, 0, len(criterionResults))
	for i, cr := range criterionResults {
		assertions = append(assertions, AssertionResult{
			Text:     j.Criteria[i],
			Passed:   *cr.Passed,
			Evidence: formatCriterionEvidence(cr),
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

// decodeAgentJudgeResponse accepts one JSON object, optionally wrapped in one
// complete JSON code fence, and rejects unknown fields or trailing values.
func decodeAgentJudgeResponse(output string, v any) error {
	candidate, err := agentJudgeJSONCandidate(output)
	if err != nil {
		return err
	}
	if err := rejectDuplicateJSONKeys(candidate); err != nil {
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

// rejectDuplicateJSONKeys scans the JSON token stream before decoding into a
// struct because encoding/json otherwise accepts duplicate keys using the last
// value. The strict agent_judge contract treats ambiguous objects as invalid.
func rejectDuplicateJSONKeys(input string) error {
	decoder := json.NewDecoder(strings.NewReader(input))
	return scanJSONValue(decoder)
}

func scanJSONValue(decoder *json.Decoder) error {
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
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}

	_, err = decoder.Token()
	return err
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
	sb.WriteString("Use each criterion_id exactly once. Set passed and the arrays according to your judgment.\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"results\": [\n")
	for i := range criteria {
		fmt.Fprintf(sb, "    {\"criterion_id\": %q, \"passed\": true, \"evidence\": [\"concrete observation\"], \"failures\": []}", criterionID(i))
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
