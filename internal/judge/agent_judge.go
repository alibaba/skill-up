package judge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// Criterion is the original criterion text.
	Criterion string `json:"criterion"`

	// Passed indicates whether the criterion was met.
	Passed bool `json:"passed"`

	// Evidence is the concrete reason for the pass/fail determination.
	// Required: the agent must provide evidence (design doc: "evidence is required").
	Evidence string `json:"evidence"`
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

	// Context controls which evaluation materials are written, referenced, or
	// inlined for this agent_judge invocation.
	Context *config.JudgeContextConfig
}

// NewAgentJudge creates an AgentJudge with sensible defaults.
func NewAgentJudge(ag agent.Agent, rt runtime.Runtime, model string, criteria []string, passThreshold *float64, timeoutSeconds int) *AgentJudge {
	return NewAgentJudgeWithContext(ag, rt, model, criteria, passThreshold, nil, timeoutSeconds)
}

// NewAgentJudgeWithContext creates an AgentJudge with explicit context delivery.
func NewAgentJudgeWithContext(ag agent.Agent, rt runtime.Runtime, model string, criteria []string, passThreshold *float64, contextCfg *config.JudgeContextConfig, timeoutSeconds int) *AgentJudge {
	threshold := DefaultPassThreshold
	if passThreshold != nil {
		threshold = *passThreshold
	}
	return &AgentJudge{
		Agent:          ag,
		Runtime:        rt,
		Model:          model,
		Criteria:       criteria,
		PassThreshold:  threshold,
		TimeoutSeconds: timeoutSeconds,
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
	prompt := buildJudgePrompt(ctx, j.Criteria, materialized)
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
	if err := extractJSON(sessionResult.FinalMessage, &resp); err != nil {
		return nil, &SessionResultError{
			Err:     fmt.Errorf("agent_judge failed to parse agent output: %w", err),
			Session: sessionResult,
		}
	}
	criterionResults := resp.Results

	if err := validateAgentJudgeResponse(j.Criteria, criterionResults); err != nil {
		return nil, &SessionResultError{
			Err:     err,
			Session: sessionResult,
		}
	}

	return j.buildResult(in, sessionResult, materialized, prompt, criterionResults), nil
}

func (j *AgentJudge) buildResult(in Input, sessionResult *agent.SessionResult, materialized *MaterializedContext, prompt string, criterionResults []CriterionResult) *Result {
	assertions := make([]AssertionResult, 0, len(criterionResults))
	for _, cr := range criterionResults {
		assertions = append(assertions, AssertionResult{
			Text:     cr.Criterion,
			Passed:   cr.Passed,
			Evidence: cr.Evidence,
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
		MaterializedDir: materialized.Dir,
		Manifest:        &materialized.Manifest,
		PromptBytes:     len([]byte(prompt)),
	}
	if sessionResult != nil && sessionResult.PromptDelivery != nil {
		metadata.PromptDelivery = sessionResult.PromptDelivery.Mode
		metadata.PromptBytes = sessionResult.PromptDelivery.PromptBytes
	}
	return metadata
}

func validateAgentJudgeResponse(criteria []string, results []CriterionResult) error {
	// A short response inflates pass_rate because NewResult uses the returned
	// count as the denominator (e.g. 1-of-1 returned = 100% even if 3 were sent).
	if len(results) != len(criteria) {
		return fmt.Errorf("agent_judge: expected %d criterion results, got %d", len(criteria), len(results))
	}
	for i, cr := range results {
		if strings.TrimSpace(cr.Evidence) == "" {
			return fmt.Errorf("agent_judge: criterion %d %q has empty evidence", i+1, cr.Criterion)
		}
	}
	return nil
}

// extractJSON finds and parses a JSON object from agent output text.
// The agent may wrap the JSON in markdown code blocks or other text.
func extractJSON(output string, v any) error {
	candidates := []string{output}
	if fenced := extractFencedJSON(output); fenced != "" {
		candidates = append(candidates, fenced)
	}

	for _, candidate := range candidates {
		if err := json.Unmarshal([]byte(candidate), v); err == nil {
			return nil
		}
	}

	for _, candidate := range findJSONObjectCandidates(output) {
		if err := json.Unmarshal([]byte(candidate), v); err == nil {
			return nil
		}
	}

	// Last resort: LLMs sometimes emit unescaped double-quotes inside JSON
	// string values (e.g. in evidence text). Try to repair and re-parse.
	repaired := repairJSONQuotes(output)
	if repaired != output {
		for _, candidate := range findJSONObjectCandidates(repaired) {
			if err := json.Unmarshal([]byte(candidate), v); err == nil {
				return nil
			}
		}
	}

	return fmt.Errorf("no valid JSON found in agent output (length=%d)", len(output))
}

// repairJSONQuotes attempts to fix unescaped double-quotes inside JSON string
// values. LLMs frequently produce evidence text like:
//
//	"evidence": "output contains \"hello\" which is wrong"
//
// but forget to escape the inner quotes, yielding invalid JSON. The heuristic:
// while scanning inside a JSON string, a '"' that is NOT followed (after
// optional whitespace) by a JSON structural character ( : , } ] ) is treated
// as an inner quote and escaped with a backslash.
func repairJSONQuotes(raw string) string {
	var buf strings.Builder
	buf.Grow(len(raw) + 64)

	inString := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]

		if !inString {
			buf.WriteByte(ch)
			if ch == '"' {
				inString = true
			}
			continue
		}

		// Inside a JSON string value.
		if ch == '\\' {
			// Already-escaped sequence — pass through.
			buf.WriteByte(ch)
			i++
			if i < len(raw) {
				buf.WriteByte(raw[i])
			}
			continue
		}

		if ch == '"' {
			// Decide whether this quote closes the string or is an
			// unescaped inner quote.
			rest := strings.TrimLeft(raw[i+1:], " \t\r\n")
			if len(rest) == 0 || rest[0] == ':' || rest[0] == ',' || rest[0] == '}' || rest[0] == ']' {
				// Looks like a structural boundary — close the string.
				buf.WriteByte(ch)
				inString = false
			} else {
				// Inner quote — escape it.
				buf.WriteString(`\"`)
			}
			continue
		}

		buf.WriteByte(ch)
	}

	return buf.String()
}

func extractFencedJSON(output string) string {
	idx := strings.Index(output, "```json")
	if idx < 0 {
		return ""
	}

	start := idx + len("```json")
	end := strings.Index(output[start:], "```")
	if end < 0 {
		return ""
	}

	return strings.TrimSpace(output[start : start+end])
}

func findJSONObjectCandidates(output string) []string {
	candidates := make([]string, 0)
	start := strings.Index(output, "{")
	for start >= 0 {
		end, ok := findJSONObjectEnd(output, start)
		if !ok {
			break
		}
		candidates = append(candidates, output[start:end+1])

		next := strings.Index(output[end+1:], "{")
		if next < 0 {
			break
		}
		start = end + 1 + next
	}

	return candidates
}

// findJSONObjectEnd finds the closing brace of a top-level JSON object embedded
// in free-form model output.
//
// This is intentionally a small state machine instead of a regexp:
// nested braces and escaped quotes make regexp matching brittle, and the
// standard JSON decoder cannot help until we first isolate a candidate object.
//
// Single-quoted content is treated as "string-like" only for scanning. That
// lets us skip over Python-style pseudo-JSON fragments while continuing to look
// for a later valid JSON object. Single-quoted content is not accepted as valid
// JSON by extractJSON; the final decode still uses encoding/json.
func findJSONObjectEnd(output string, start int) (int, bool) {
	depth := 0
	inDoubleQuotedString := false
	inSingleQuotedString := false

	for i := start; i < len(output); i++ {
		ch := output[i]
		if inDoubleQuotedString || inSingleQuotedString {
			if ch == '\\' {
				i++
				continue
			}
			if inDoubleQuotedString && ch == '"' {
				inDoubleQuotedString = false
			}
			if inSingleQuotedString && ch == '\'' {
				inSingleQuotedString = false
			}
			continue
		}

		switch ch {
		case '"':
			inDoubleQuotedString = true
		case '\'':
			inSingleQuotedString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}

	return 0, false
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
func buildJudgePrompt(_ context.Context, criteria []string, materialized *MaterializedContext) string {
	var sb strings.Builder

	sb.WriteString("You are an expert evaluator for an AI agent skill evaluation.\n")
	sb.WriteString("You must assess the agent's output against the following criteria.\n")
	sb.WriteString("For EACH criterion, you MUST provide:\n")
	sb.WriteString("- \"passed\": true/false\n")
	sb.WriteString("- \"evidence\": concrete evidence from the output supporting your judgment\n\n")
	sb.WriteString("You MUST NOT pass a criterion without specific evidence.\n\n")
	sb.WriteString("IMPORTANT: Your response MUST be valid JSON. If any string value contains double quotes, ")
	sb.WriteString("you MUST escape them with a backslash (e.g. \\\"example\\\"). Do NOT use unescaped double quotes inside string values.\n\n")

	sb.WriteString("## Criteria\n")
	for i, c := range criteria {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, c)
	}

	if materialized != nil {
		sb.WriteString("\n## Review Materials\n")
		sb.WriteString("Read files from this table as needed to evaluate the criteria. Evidence must be traceable to the listed material keys or inline excerpts.\n\n")
		sb.WriteString("| key | mode | path | bytes | notes |\n")
		sb.WriteString("| --- | --- | --- | ---: | --- |\n")
		for _, m := range materialized.Materials {
			notes := ""
			if m.Truncated {
				notes = "inline excerpt truncated; full text is at path"
			}
			fmt.Fprintf(&sb, "| %s | %s | %s | %d | %s |\n", materialLabel(m), m.Mode, m.Path, m.OriginalBytes, notes)
		}
		for _, m := range materialized.Materials {
			if strings.TrimSpace(m.InlineContent) == "" {
				continue
			}
			fmt.Fprintf(&sb, "\n### Inline Material: %s\n", materialLabel(m))
			if m.Truncated {
				sb.WriteString("The excerpt below is truncated; read the full file at the path above if needed.\n")
			}
			sb.WriteString("```\n")
			sb.WriteString(m.InlineContent)
			sb.WriteString("\n```\n")
		}
	}

	sb.WriteString("\n## Required Response Format (JSON)\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"results\": [\n")
	for i, c := range criteria {
		fmt.Fprintf(&sb, "    {\"criterion\": %q, \"passed\": true|false, \"evidence\": \"...\"}", c)
		if i < len(criteria)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("  ]\n")
	sb.WriteString("}\n")
	sb.WriteString("```\n")

	return sb.String()
}

func materialLabel(m ContextMaterial) string {
	if m.Label != "" {
		return m.Key + ":" + m.Label
	}
	return m.Key
}
