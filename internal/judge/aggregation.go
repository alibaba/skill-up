package judge

import "strings"

// OutcomeAggregator combines member verdicts without flattening their criteria.
// Execution errors and missing results must have explicit policy semantics.
type OutcomeAggregator interface {
	Strategy() string
	Aggregate(outcomes []Outcome, turnsExecuted, turnsTotal int) (*Result, Status)
}

// DefaultOutcomeAggregator returns the policy used by multi-judge evaluation.
// Configuration-selectable policies are reserved for a future schema change.
func DefaultOutcomeAggregator() OutcomeAggregator { return AllRequiredAggregator{} }

// AllRequiredAggregator passes only when every member passes. ERROR or SKIP
// takes precedence over FAIL; each member contributes one compatibility assertion.
type AllRequiredAggregator struct{}

// Strategy identifies the policy in grouped evaluation reports.
func (AllRequiredAggregator) Strategy() string { return "all_required" }

// Aggregate preserves each member's verdict as one compatibility assertion.
func (AllRequiredAggregator) Aggregate(outcomes []Outcome, turnsExecuted, turnsTotal int) (*Result, Status) {
	assertions := make([]AssertionResult, 0, len(outcomes))
	status := StatusPass
	for _, outcome := range outcomes {
		passed := outcome.Status == StatusPass
		var evidence []string
		if outcome.Error != "" {
			evidence = append(evidence, outcome.Error)
		}
		if outcome.SkipReason != "" {
			evidence = append(evidence, outcome.SkipReason)
		}
		if outcome.Result != nil {
			for _, assertion := range outcome.Result.AssertionResults {
				if !assertion.Passed && assertion.Evidence != "" {
					evidence = append(evidence, assertion.Evidence)
				}
			}
		}
		assertions = append(assertions, AssertionResult{
			Text: "judge " + outcome.ID + " (" + outcome.Type + ")", Passed: passed,
			Evidence: string(outcome.Status) + ": " + strings.Join(evidence, "; "),
		})
		switch outcome.Status {
		case StatusError, StatusSkip:
			status = StatusError
		case StatusFail:
			if status != StatusError {
				status = StatusFail
			}
		}
	}
	grading := NewResult(assertions, turnsExecuted, turnsTotal)
	grading.Status = status
	return grading, status
}
