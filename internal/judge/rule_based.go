package judge

import (
	"context"
	"fmt"
	"strings"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// RuleBasedJudge applies declarative assertions to transcripts and outputs.
//
// Evaluation priority (design doc):
//  1. Evaluate failure rules first — any match → immediate FAIL
//  2. Evaluate success rules — ALL must pass → PASS
type RuleBasedJudge struct {
	// Success assertions: all must pass for PASS.
	Success []config.Rule
	// Failure assertions: any match → immediate FAIL.
	Failure []config.Rule
}

// NewRuleBasedJudge creates a RuleBasedJudge from a JudgeConfig.
func NewRuleBasedJudge(cfg config.JudgeConfig) *RuleBasedJudge {
	return &RuleBasedJudge{
		Success: cfg.Success,
		Failure: cfg.Failure,
	}
}

// Evaluate implements the Judge interface.
func (j *RuleBasedJudge) Evaluate(_ context.Context, in Input) (*Result, error) {
	var allAssertions []AssertionResult

	// 1. Check failure rules first — any match is immediate FAIL.
	for _, rule := range j.Failure {
		ar := evaluateAssertion(rule, in)
		if ar.Passed {
			// A failure rule "passed" means the bad pattern WAS found → FAIL.
			allAssertions = append(allAssertions, AssertionResult{
				Text:     "failure: " + ar.Text,
				Passed:   false,
				Evidence: "failure rule matched: " + ar.Evidence,
			})
		}
		// If the failure rule did not match, we don't record it (it's good).
	}

	// Short-circuit: if any failure rule matched, return immediately.
	if len(allAssertions) > 0 {
		return NewResult(allAssertions, in.TurnsExecuted, in.TurnsTotal), nil
	}

	// 2. Check success rules — all must pass.
	for _, rule := range j.Success {
		ar := evaluateAssertion(rule, in)
		allAssertions = append(allAssertions, ar)
	}

	// If no rules defined at all, default to PASS.
	if len(allAssertions) == 0 {
		return NewResult(nil, in.TurnsExecuted, in.TurnsTotal), nil
	}

	return NewResult(allAssertions, in.TurnsExecuted, in.TurnsTotal), nil
}

// ---------------------------------------------------------------------------
// Assertion dispatcher
// ---------------------------------------------------------------------------

// evaluateAssertion dispatches a single Rule to the appropriate handler.
// Returns an AssertionResult where Passed=true means the assertion condition was met.
func evaluateAssertion(rule config.Rule, in Input) AssertionResult {
	switch {
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
	case rule.TurnResponseContains != nil:
		return evalTurnResponseContains(rule.TurnResponseContains, in.TurnResults)
	case rule.TurnResponseNotContains != nil:
		return evalTurnResponseNotContains(rule.TurnResponseNotContains, in.TurnResults)
	case rule.ToolCalledInTurn != nil:
		return evalToolCalledInTurn(rule.ToolCalledInTurn, in.TurnResults)
	case rule.ToolNotCalledInTurn != nil:
		return evalToolNotCalledInTurn(rule.ToolNotCalledInTurn, in.TurnResults)
	default:
		return AssertionResult{
			Text:     "unknown_rule",
			Passed:   false,
			Evidence: "no recognizable rule field set in assertion",
		}
	}
}

// ---------------------------------------------------------------------------
// Individual rule evaluators
// ---------------------------------------------------------------------------

// evalOutputContains checks the final output for keywords (all/any/not).
func evalOutputContains(rule *config.OutputContainsRule, finalMessage string) AssertionResult {
	// Check "all": every keyword must be present.
	var missing []string
	for _, kw := range rule.All {
		if !strings.Contains(finalMessage, kw) {
			missing = append(missing, kw)
		}
	}
	if len(missing) > 0 {
		return AssertionResult{
			Text:     fmt.Sprintf("output_contains.all: missing %v", missing),
			Passed:   false,
			Evidence: fmt.Sprintf("output does not contain required keywords: %v", missing),
		}
	}

	// Check "any": at least one keyword must be present.
	if len(rule.Any) > 0 {
		found := false
		for _, kw := range rule.Any {
			if strings.Contains(finalMessage, kw) {
				found = true
				break
			}
		}
		if !found {
			return AssertionResult{
				Text:     fmt.Sprintf("output_contains.any: %v", rule.Any),
				Passed:   false,
				Evidence: fmt.Sprintf("output does not contain any of %v", rule.Any),
			}
		}
	}

	// Check "not": none of the keywords should be present.
	for _, kw := range rule.Not {
		if strings.Contains(finalMessage, kw) {
			return AssertionResult{
				Text:     fmt.Sprintf("output_contains.not: %q", kw),
				Passed:   false,
				Evidence: fmt.Sprintf("output contains forbidden keyword %q", kw),
			}
		}
	}

	// Build description.
	var descParts []string
	if len(rule.All) > 0 {
		descParts = append(descParts, fmt.Sprintf("all:%v", rule.All))
	}
	if len(rule.Any) > 0 {
		descParts = append(descParts, fmt.Sprintf("any:%v", rule.Any))
	}
	if len(rule.Not) > 0 {
		descParts = append(descParts, fmt.Sprintf("not:%v", rule.Not))
	}
	desc := "output_contains"
	if len(descParts) > 0 {
		desc = fmt.Sprintf("output_contains{%s}", strings.Join(descParts, ", "))
	}

	return AssertionResult{
		Text:     desc,
		Passed:   true,
		Evidence: fmt.Sprintf("output satisfies all contains checks (%s)", strings.Join(descParts, ", ")),
	}
}

// evalExitCode checks that the exit code matches.
func evalExitCode(expected, actual int) AssertionResult {
	if actual == expected {
		return AssertionResult{
			Text:     fmt.Sprintf("exit_code: %d", expected),
			Passed:   true,
			Evidence: fmt.Sprintf("exit_code is %d as expected", actual),
		}
	}
	return AssertionResult{
		Text:     fmt.Sprintf("exit_code: %d", expected),
		Passed:   false,
		Evidence: fmt.Sprintf("expected exit_code %d, got %d", expected, actual),
	}
}

// evalToolCalled checks that a specific tool was called (with optional partial argument matching).
func evalToolCalled(rule *config.ToolCalledRule, tr transcript.Transcript) AssertionResult {
	calls := tr.ToolCalls()

	for _, msg := range calls {
		if msg.ToolCall == nil {
			continue
		}
		if msg.ToolCall.Name != rule.Name {
			continue
		}

		// Name matched. Check args if specified (partial match).
		if len(rule.Args) == 0 {
			return AssertionResult{
				Text:     "tool_called: " + rule.Name,
				Passed:   true,
				Evidence: fmt.Sprintf("tool %q was called", rule.Name),
			}
		}

		if argsMatch(rule.Args, msg.ToolCall.Arguments) {
			return AssertionResult{
				Text:     "tool_called: " + rule.Name + " (with args)",
				Passed:   true,
				Evidence: fmt.Sprintf("tool %q was called with matching args", rule.Name),
			}
		}
	}

	// Not found.
	desc := "tool_called: " + rule.Name
	evidence := fmt.Sprintf("tool %q was not called", rule.Name)
	if len(rule.Args) > 0 {
		evidence = fmt.Sprintf("tool %q was not called with matching args %v", rule.Name, rule.Args)
	}
	return AssertionResult{
		Text:     desc,
		Passed:   false,
		Evidence: evidence,
	}
}

// evalFilesExist checks that all listed files exist in the workspace.
func evalFilesExist(files []string, workspace string) AssertionResult {
	for _, f := range files {
		exists, err := fileExistsInWorkspace(workspace, f)
		if err != nil {
			return AssertionResult{
				Text:     "files_exist: " + f,
				Passed:   false,
				Evidence: fmt.Sprintf("invalid path %q: %v", f, err),
			}
		}
		if !exists {
			return AssertionResult{
				Text:     "files_exist: " + f,
				Passed:   false,
				Evidence: fmt.Sprintf("expected file %q does not exist", f),
			}
		}
	}
	return AssertionResult{
		Text:     fmt.Sprintf("files_exist: %v", files),
		Passed:   true,
		Evidence: "all required files exist",
	}
}

// evalFilesNotExist checks that none of the listed files exist in the workspace.
func evalFilesNotExist(files []string, workspace string) AssertionResult {
	for _, f := range files {
		exists, err := fileExistsInWorkspace(workspace, f)
		if err != nil {
			return AssertionResult{
				Text:     "files_not_exist: " + f,
				Passed:   false,
				Evidence: fmt.Sprintf("invalid path %q: %v", f, err),
			}
		}
		if exists {
			return AssertionResult{
				Text:     "files_not_exist: " + f,
				Passed:   false,
				Evidence: fmt.Sprintf("file %q should not exist but does", f),
			}
		}
	}
	return AssertionResult{
		Text:     fmt.Sprintf("files_not_exist: %v", files),
		Passed:   true,
		Evidence: "none of the forbidden files exist",
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// argsMatch checks that all expected args are present in the actual args (partial match).
// Only declared fields in expected are checked; extra fields in actual are ignored.
func argsMatch(expected, actual map[string]any) bool {
	for k, ev := range expected {
		av, ok := actual[k]
		if !ok {
			return false
		}
		// Compare as strings for flexibility (YAML values may deserialize differently).
		if fmt.Sprintf("%v", ev) != fmt.Sprintf("%v", av) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Per-turn rule evaluators
// ---------------------------------------------------------------------------

// lookupTurn retrieves the turn result for a given 1-based turn number.
// Returns (turn, ok). If the turn is missing or not completed, it returns a
// failing AssertionResult describing why.
func lookupTurn(turnNum int, turns []InputTurnResult, ruleName string) (*InputTurnResult, *AssertionResult) {
	if turnNum < 1 || turnNum > len(turns) {
		ar := AssertionResult{
			Text:     fmt.Sprintf("%s[turn=%d]", ruleName, turnNum),
			Passed:   false,
			Evidence: fmt.Sprintf("turn %d does not exist (executed %d turns)", turnNum, len(turns)),
		}
		return nil, &ar
	}
	tr := &turns[turnNum-1]
	if tr.Status != "completed" {
		ar := AssertionResult{
			Text:     fmt.Sprintf("%s[turn=%d]", ruleName, turnNum),
			Passed:   false,
			Evidence: fmt.Sprintf("turn %d has status %q (reason: %s); assertion requires completed turn", turnNum, tr.Status, tr.Reason),
		}
		return nil, &ar
	}
	return tr, nil
}

// evalTurnResponseContains checks a specific turn's response for required keywords.
func evalTurnResponseContains(rule *config.TurnResponseContainsRule, turns []InputTurnResult) AssertionResult {
	tr, failAR := lookupTurn(rule.Turn, turns, "turn_response_contains")
	if failAR != nil {
		return *failAR
	}

	// Check contains_all.
	var missing []string
	for _, kw := range rule.ContainsAll {
		if !strings.Contains(tr.Response, kw) {
			missing = append(missing, kw)
		}
	}
	if len(missing) > 0 {
		return AssertionResult{
			Text:     fmt.Sprintf("turn_response_contains[turn=%d].contains_all%v", rule.Turn, rule.ContainsAll),
			Passed:   false,
			Evidence: fmt.Sprintf("turn %d response missing required keywords: %v (checked: %v)", rule.Turn, missing, rule.ContainsAll),
		}
	}

	// Check contains_any.
	if len(rule.ContainsAny) > 0 {
		found := false
		for _, kw := range rule.ContainsAny {
			if strings.Contains(tr.Response, kw) {
				found = true
				break
			}
		}
		if !found {
			return AssertionResult{
				Text:     fmt.Sprintf("turn_response_contains[turn=%d].contains_any%v", rule.Turn, rule.ContainsAny),
				Passed:   false,
				Evidence: fmt.Sprintf("turn %d response does not contain any of %v", rule.Turn, rule.ContainsAny),
			}
		}
	}

	// Build descriptive evidence.
	var parts []string
	if len(rule.ContainsAll) > 0 {
		parts = append(parts, fmt.Sprintf("contains_all:%v", rule.ContainsAll))
	}
	if len(rule.ContainsAny) > 0 {
		parts = append(parts, fmt.Sprintf("contains_any:%v", rule.ContainsAny))
	}
	desc := strings.Join(parts, ", ")

	return AssertionResult{
		Text:     fmt.Sprintf("turn_response_contains[turn=%d]{%s}", rule.Turn, desc),
		Passed:   true,
		Evidence: fmt.Sprintf("turn %d response contains all required keywords (%s)", rule.Turn, desc),
	}
}

// evalTurnResponseNotContains checks a specific turn's response for forbidden keywords.
func evalTurnResponseNotContains(rule *config.TurnResponseNotContainsRule, turns []InputTurnResult) AssertionResult {
	tr, failAR := lookupTurn(rule.Turn, turns, "turn_response_not_contains")
	if failAR != nil {
		return *failAR
	}

	for _, kw := range rule.NotContains {
		if strings.Contains(tr.Response, kw) {
			return AssertionResult{
				Text:     fmt.Sprintf("turn_response_not_contains[turn=%d]{not:%v}", rule.Turn, rule.NotContains),
				Passed:   false,
				Evidence: fmt.Sprintf("turn %d response contains forbidden keyword %q (checked: %v)", rule.Turn, kw, rule.NotContains),
			}
		}
	}

	return AssertionResult{
		Text:     fmt.Sprintf("turn_response_not_contains[turn=%d]{not:%v}", rule.Turn, rule.NotContains),
		Passed:   true,
		Evidence: fmt.Sprintf("turn %d response does not contain any of the forbidden keywords %v", rule.Turn, rule.NotContains),
	}
}

// evalToolCalledInTurn checks that a specific tool was called during a specific turn.
func evalToolCalledInTurn(rule *config.ToolCalledInTurnRule, turns []InputTurnResult) AssertionResult {
	tr, failAR := lookupTurn(rule.Turn, turns, "tool_called_in_turn")
	if failAR != nil {
		return *failAR
	}

	calls := tr.Transcript.ToolCalls()
	for _, msg := range calls {
		if msg.ToolCall == nil {
			continue
		}
		if msg.ToolCall.Name != rule.Name {
			continue
		}
		// Name matched; check args if specified.
		if len(rule.Args) == 0 {
			return AssertionResult{
				Text:     fmt.Sprintf("tool_called_in_turn[turn=%d]: %s", rule.Turn, rule.Name),
				Passed:   true,
				Evidence: fmt.Sprintf("tool %q was called in turn %d", rule.Name, rule.Turn),
			}
		}
		if argsMatch(rule.Args, msg.ToolCall.Arguments) {
			return AssertionResult{
				Text:     fmt.Sprintf("tool_called_in_turn[turn=%d]: %s (with args)", rule.Turn, rule.Name),
				Passed:   true,
				Evidence: fmt.Sprintf("tool %q was called in turn %d with matching args", rule.Name, rule.Turn),
			}
		}
	}

	// Not found.
	evidence := fmt.Sprintf("tool %q was not called in turn %d", rule.Name, rule.Turn)
	if len(rule.Args) > 0 {
		evidence = fmt.Sprintf("tool %q was not called in turn %d with matching args %v", rule.Name, rule.Turn, rule.Args)
	}
	return AssertionResult{
		Text:     fmt.Sprintf("tool_called_in_turn[turn=%d]: %s", rule.Turn, rule.Name),
		Passed:   false,
		Evidence: evidence,
	}
}

// evalToolNotCalledInTurn checks that a specific tool was NOT called during a specific turn.
func evalToolNotCalledInTurn(rule *config.ToolNotCalledInTurnRule, turns []InputTurnResult) AssertionResult {
	tr, failAR := lookupTurn(rule.Turn, turns, "tool_not_called_in_turn")
	if failAR != nil {
		return *failAR
	}

	calls := tr.Transcript.ToolCalls()
	for _, msg := range calls {
		if msg.ToolCall == nil {
			continue
		}
		if msg.ToolCall.Name == rule.Name {
			return AssertionResult{
				Text:     fmt.Sprintf("tool_not_called_in_turn[turn=%d]: %s", rule.Turn, rule.Name),
				Passed:   false,
				Evidence: fmt.Sprintf("tool %q was called in turn %d but should not have been", rule.Name, rule.Turn),
			}
		}
	}

	return AssertionResult{
		Text:     fmt.Sprintf("tool_not_called_in_turn[turn=%d]: %s", rule.Turn, rule.Name),
		Passed:   true,
		Evidence: fmt.Sprintf("tool %q was not called in turn %d as expected", rule.Name, rule.Turn),
	}
}
