package evaluator

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// TurnStatus describes the outcome of a single conversation turn.
type TurnStatus string

const (
	// TurnCompleted indicates the turn executed successfully.
	TurnCompleted TurnStatus = "completed"
	// TurnSkipped indicates the turn was skipped due to a prior post_condition failure.
	TurnSkipped TurnStatus = "skipped"
	// TurnFailed indicates the turn's post_condition failed with on_fail=fail.
	TurnFailed TurnStatus = "failed"
	// TurnError indicates a runtime or agent execution error.
	TurnError TurnStatus = "error"
)

// TurnResult holds the outcome of a single conversation turn during multi-turn evaluation.
type TurnResult struct {
	// TurnNumber is the 1-based index of this turn.
	TurnNumber int
	// Content is the user message sent to the agent (after template substitution).
	Content string
	// Response is the assistant response text for this turn.
	Response string
	// Transcript is the per-turn interaction record returned by the agent.
	Transcript transcript.Transcript
	// SessionResult is the raw agent result for this turn.
	SessionResult *agent.SessionResult
	// Status is the outcome of this turn.
	Status TurnStatus
	// Reason describes why the turn was skipped/failed/errored.
	Reason string
	// CapturedVars holds variables captured from the turn response.
	CapturedVars map[string]string
}

// multiTurnState holds mutable state for a multi-turn execution.
type multiTurnState struct {
	capturedVars map[string]string
	sessionID    string
	turnResults  []TurnResult
	transcript   transcript.Transcript
	inputTokens  int
	outputTokens int
	durationMs   int64
}

// executeMultiTurn orchestrates multi-turn evaluation for cases with
// len(input.turns) > 1. It calls the agent once per turn, evaluating
// post_conditions and capturing variables between turns.
func (e *defaultEvaluator) executeMultiTurn(
	ctx context.Context,
	rt runtime.Runtime,
	caseCfg *config.CaseConfig,
	runAgent agent.Agent,
	agentExecOpts agent.ExecOptions,
) ([]TurnResult, *agent.SessionResult, error) {
	resumer, hasResumer := runAgent.(agent.SessionResumer)
	if !hasResumer {
		return nil, nil, fmt.Errorf("agent %s does not implement SessionResumer for multi-turn evaluation", runAgent.Name())
	}

	turns := caseCfg.Input.Turns
	state := &multiTurnState{
		capturedVars: make(map[string]string),
		turnResults:  make([]TurnResult, 0, len(turns)),
	}

	var lastSessionResult *agent.SessionResult

	for i, turn := range turns {
		turnNum := i + 1

		// Template substitution.
		content, err := substituteTemplate(turn.Content, state.capturedVars)
		if err != nil {
			tr := TurnResult{TurnNumber: turnNum, Content: turn.Content, Status: TurnError, Reason: err.Error()}
			state.turnResults = append(state.turnResults, tr)
			return state.turnResults, lastSessionResult, fmt.Errorf("turn %d template substitution: %w", turnNum, err)
		}

		// Execute the turn via the resumer.
		sessionResult, execErr := executeSingleTurn(ctx, resumer, rt, agentExecOpts, content, turnNum, turn.TimeoutSeconds, state.sessionID)
		if execErr != nil {
			tr := TurnResult{TurnNumber: turnNum, Content: content, Status: TurnError, Reason: execErr.Error(), SessionResult: sessionResult}
			state.turnResults = append(state.turnResults, tr)
			return state.turnResults, buildAggregateResult(state, lastSessionResult), execErr
		}

		// Update state from the successful turn.
		if sessionResult != nil && sessionResult.SessionID != "" {
			state.sessionID = sessionResult.SessionID
		}
		lastSessionResult = sessionResult
		accumulateMetrics(state, sessionResult)

		// Build turn result.
		tr := buildTurnResult(turnNum, content, sessionResult)

		// Evaluate post_condition.
		if failed, earlyReturn := e.handlePostCondition(ctx, caseCfg.ID, turnNum, i, &tr, turn, turns, state); earlyReturn {
			return state.turnResults, buildAggregateResult(state, lastSessionResult), nil
		} else if failed {
			return state.turnResults, buildAggregateResult(state, lastSessionResult), nil
		}

		// Capture variables.
		if len(turn.Capture) > 0 {
			captured, captureErr := captureVariables(turn.Capture, tr.Response)
			if captureErr != nil {
				tr.Status = TurnError
				tr.Reason = captureErr.Error()
				state.turnResults = append(state.turnResults, tr)
				return state.turnResults, buildAggregateResult(state, lastSessionResult), captureErr
			}
			tr.CapturedVars = captured
			maps.Copy(state.capturedVars, captured)
		}

		state.turnResults = append(state.turnResults, tr)
		if tr.Transcript != nil {
			state.transcript = append(state.transcript, tr.Transcript...)
		}
	}

	return state.turnResults, buildAggregateResult(state, lastSessionResult), nil
}

// executeSingleTurn invokes the resumer for one turn, applying an optional per-turn timeout.
func executeSingleTurn(
	ctx context.Context,
	resumer agent.SessionResumer,
	rt runtime.Runtime,
	opts agent.ExecOptions,
	content string,
	turnNum int,
	timeoutSec int,
	sessionID string,
) (*agent.SessionResult, error) {
	msg := transcript.Message{Role: transcript.RoleUser, Content: content, Turn: turnNum}

	turnCtx := ctx
	var cancel context.CancelFunc
	if timeoutSec > 0 {
		turnCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	} else {
		cancel = func() {}
	}
	defer cancel()

	return resumer.RunTurn(turnCtx, rt, opts, msg, sessionID)
}

// buildTurnResult creates a TurnResult from a successful agent execution.
func buildTurnResult(turnNum int, content string, sessionResult *agent.SessionResult) TurnResult {
	tr := TurnResult{
		TurnNumber:    turnNum,
		Content:       content,
		SessionResult: sessionResult,
		Status:        TurnCompleted,
	}
	if sessionResult != nil {
		tr.Response = sessionResult.FinalMessage
		tr.Transcript = sessionResult.Transcript
	}
	return tr
}

// substituteTemplate replaces {{variable}} placeholders in content with captured values.
// Unresolved placeholders are treated as errors.
func substituteTemplate(content string, vars map[string]string) (string, error) {
	re := regexp.MustCompile(`\{\{(\w+)\}\}`)
	var unresolved []string
	result := re.ReplaceAllStringFunc(content, func(match string) string {
		name := match[2 : len(match)-2]
		if val, ok := vars[name]; ok {
			return val
		}
		unresolved = append(unresolved, name)
		return match
	})
	if len(unresolved) > 0 {
		return "", fmt.Errorf("unresolved template variable(s): %s", strings.Join(unresolved, ", "))
	}
	return result, nil
}

// evaluatePostCondition checks the response against the post_condition rules.
// Returns empty string on success; otherwise a human-readable failure reason.
func evaluatePostCondition(pc *config.PostCondition, response string) string {
	if pc == nil {
		return ""
	}

	// must_contain_all: all required strings must appear.
	for _, required := range pc.MustContainAll {
		if !strings.Contains(response, required) {
			return fmt.Sprintf("must_contain_all: missing %q", required)
		}
	}

	// must_contain_any: at least one string must appear.
	if len(pc.MustContainAny) > 0 {
		found := false
		for _, candidate := range pc.MustContainAny {
			if strings.Contains(response, candidate) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Sprintf("must_contain_any: none of %v found", pc.MustContainAny)
		}
	}

	// must_not_contain: none may appear.
	for _, forbidden := range pc.MustNotContain {
		if strings.Contains(response, forbidden) {
			return fmt.Sprintf("must_not_contain: found %q", forbidden)
		}
	}

	return ""
}

// captureVariables extracts values from the response using the configured capture rules.
func captureVariables(rules []config.CaptureRule, response string) (map[string]string, error) {
	captured := make(map[string]string, len(rules))
	for _, rule := range rules {
		if rule.Pattern == "" {
			return nil, fmt.Errorf("capture variable %q: empty pattern", rule.Variable)
		}
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, fmt.Errorf("capture variable %q: invalid regex %q: %w", rule.Variable, rule.Pattern, err)
		}
		match := re.FindStringSubmatch(response)
		if match == nil {
			return nil, fmt.Errorf("capture variable %q: pattern %q did not match response", rule.Variable, rule.Pattern)
		}
		value, extractErr := extractCaptureValue(re, match)
		if extractErr != nil {
			return nil, fmt.Errorf("capture variable %q: %w", rule.Variable, extractErr)
		}
		if value == "" {
			return nil, fmt.Errorf("capture variable %q: pattern %q matched but produced empty value", rule.Variable, rule.Pattern)
		}
		captured[rule.Variable] = value
	}
	return captured, nil
}

// extractCaptureValue retrieves the captured value from a regex match.
// It prefers a named group "value"; if absent, requires exactly one capture
// group (ambiguity from multiple unnamed groups is an error).
func extractCaptureValue(re *regexp.Regexp, match []string) (string, error) {
	// Try named group "value" first.
	for i, name := range re.SubexpNames() {
		if name == "value" && i < len(match) {
			return match[i], nil
		}
	}
	// Count unnamed capture groups (SubexpNames()[0] is always the full match, skip it).
	unnamedCount := countUnnamedGroups(re)
	if unnamedCount == 0 {
		return "", errors.New("pattern has no capture groups; use (?P<value>...) or a single (...)")
	}
	if unnamedCount > 1 {
		return "", fmt.Errorf("pattern has %d unnamed capture groups; use (?P<value>...) to disambiguate", unnamedCount)
	}
	// Exactly one unnamed group — use first non-empty submatch.
	if len(match) > 1 {
		return match[1], nil
	}
	return "", nil
}

// countUnnamedGroups counts capture groups that do not have a name.
func countUnnamedGroups(re *regexp.Regexp) int {
	count := 0
	for i, name := range re.SubexpNames() {
		if i == 0 {
			continue // skip full match
		}
		if name == "" {
			count++
		}
	}
	return count
}

// handlePostCondition evaluates a turn's post_condition and handles failure.
// Returns (failed, earlyReturn). earlyReturn=true means skip_remaining was triggered.
func (e *defaultEvaluator) handlePostCondition(
	ctx context.Context,
	caseID string,
	turnNum, turnIdx int,
	tr *TurnResult,
	turn config.Turn,
	turns []config.Turn,
	state *multiTurnState,
) (failed bool, earlyReturn bool) {
	if turn.PostCondition == nil {
		return false, false
	}
	reason := evaluatePostCondition(turn.PostCondition, tr.Response)
	if reason == "" {
		return false, false
	}

	onFail := turn.PostCondition.OnFail
	if onFail == "" {
		onFail = "fail"
	}
	tr.Status = TurnFailed
	tr.Reason = reason
	state.turnResults = append(state.turnResults, *tr)
	logging.DebugContextf(ctx, "Evaluator: case %s turn %d post_condition failed: %s (on_fail=%s)", caseID, turnNum, reason, onFail)

	if onFail == "skip_remaining" {
		for j := turnIdx + 1; j < len(turns); j++ {
			state.turnResults = append(state.turnResults, TurnResult{
				TurnNumber: j + 1,
				Content:    turns[j].Content,
				Status:     TurnSkipped,
				Reason:     fmt.Sprintf("skipped: turn %d post_condition failed", turnNum),
			})
		}
		return true, true
	}
	// on_fail == "fail"
	return true, false
}

// accumulateMetrics adds per-turn metrics to the running totals.
func accumulateMetrics(state *multiTurnState, result *agent.SessionResult) {
	if result == nil {
		return
	}
	state.inputTokens += result.InputTokens
	state.outputTokens += result.OutputTokens
	state.durationMs += result.DurationMs
}

// buildAggregateResult constructs a SessionResult that aggregates all turns.
func buildAggregateResult(state *multiTurnState, lastResult *agent.SessionResult) *agent.SessionResult {
	if lastResult == nil {
		return &agent.SessionResult{
			Transcript:   state.transcript,
			InputTokens:  state.inputTokens,
			OutputTokens: state.outputTokens,
			DurationMs:   state.durationMs,
			Turns:        countCompletedTurns(state.turnResults),
		}
	}
	agg := *lastResult
	agg.Transcript = state.transcript
	agg.InputTokens = state.inputTokens
	agg.OutputTokens = state.outputTokens
	agg.DurationMs = state.durationMs
	agg.Turns = countCompletedTurns(state.turnResults)
	// FinalMessage is the response from the last completed turn.
	if msg := lastCompletedResponse(state.turnResults); msg != "" {
		agg.FinalMessage = msg
	}
	return &agg
}

// countCompletedTurns returns the number of turns with TurnCompleted status.
func countCompletedTurns(results []TurnResult) int {
	n := 0
	for _, r := range results {
		if r.Status == TurnCompleted {
			n++
		}
	}
	return n
}

// lastCompletedResponse returns the Response from the last TurnCompleted result.
func lastCompletedResponse(results []TurnResult) string {
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].Status == TurnCompleted {
			return results[i].Response
		}
	}
	return ""
}

// multiTurnStatus determines the overall case status from turn results.
func multiTurnStatus(results []TurnResult) judge.Status {
	for i, r := range results {
		switch r.Status {
		case TurnError:
			return judge.StatusError
		case TurnFailed:
			// Distinguish hard failure (on_fail=fail) from skip_remaining.
			// on_fail=skip_remaining fills subsequent turns as TurnSkipped;
			// if skipped entries follow the failure, proceed to judge.
			hasSkippedAfter := false
			for j := i + 1; j < len(results); j++ {
				if results[j].Status == TurnSkipped {
					hasSkippedAfter = true
					break
				}
			}
			if !hasSkippedAfter {
				return judge.StatusFail
			}
			// skip_remaining: fall through to judge evaluation.
			return ""
		}
	}
	// If all remaining are completed/skipped, check if any completed.
	hasCompleted := false
	allSkipped := true
	for _, r := range results {
		if r.Status == TurnCompleted {
			hasCompleted = true
			allSkipped = false
		}
		if r.Status != TurnSkipped {
			allSkipped = false
		}
	}
	if allSkipped {
		return judge.StatusSkip
	}
	if hasCompleted {
		return "" // proceed to judge evaluation
	}
	return judge.StatusError
}

// executeMultiTurnCase is the top-level entry point that executeCaseOnce
// delegates to when a case has multiple turns. It handles the full lifecycle:
// agent execution, post-condition gates, result aggregation, and judge phase.
func (e *defaultEvaluator) executeMultiTurnCase(
	ctx context.Context,
	rt runtime.Runtime,
	caseCfg *config.CaseConfig,
	configName string,
	runAgent agent.Agent,
	agentExecOpts agent.ExecOptions,
	startTime time.Time,
	judgeCfg config.JudgeConfig,
	result *EvalResult,
) EvalResult {
	// Workspace diff hooks.
	var cleanupArtifacts func()
	finalizeArtifacts := func(*agent.SessionResult) {}
	if judgeNeedsWorkspaceDiff(judgeCfg) {
		cleanupArtifacts, finalizeArtifacts = e.prepareWorkspaceArtifacts(ctx, rt, caseCfg)
		defer cleanupArtifacts()
	}

	turnResults, aggregateSession, execErr := e.executeMultiTurn(ctx, rt, caseCfg, runAgent, agentExecOpts)
	result.TurnResults = turnResults

	finalizeArtifacts(aggregateSession)
	result.SessionResult = normalizeSessionResult(aggregateSession)

	// Collect glob artifacts unconditionally.
	e.collectGlobArtifacts(ctx, rt, configName, caseCfg)

	if execErr != nil {
		if result.DurationMs == 0 {
			result.DurationMs = time.Since(startTime).Milliseconds()
		}
		result.Status = judge.StatusError
		result.Error = fmt.Errorf("agent execution failed: %w", execErr)
		result.Configuration = configName
		return *result
	}

	// Determine status from turn results.
	status := multiTurnStatus(turnResults)
	switch status {
	case judge.StatusError:
		result.Status = judge.StatusError
		result.Configuration = configName
		return *result
	case judge.StatusFail:
		// Post-condition failure — mark FAIL, skip judge.
		if result.DurationMs == 0 {
			result.DurationMs = time.Since(startTime).Milliseconds()
		}
		result.Status = judge.StatusFail
		result.Configuration = configName
		return *result
	case judge.StatusSkip:
		result.Status = judge.StatusSkip
		result.Configuration = configName
		return *result
	}

	// All turns completed (or completed + skipped with on_fail=skip_remaining
	// that still produced judgeable data) — proceed to judge evaluation.
	turnsTotal := len(caseCfg.Input.Turns)
	return e.evaluateCaseSession(ctx, rt, caseCfg, configName, judgeCfg, turnsTotal, runAgent, aggregateSession, result)
}
