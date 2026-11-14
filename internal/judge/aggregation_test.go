package judge

import "testing"

func TestAllRequiredAggregationPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []Status
		want     Status
	}{
		{"all pass", []Status{StatusPass, StatusPass}, StatusPass},
		{"fail", []Status{StatusPass, StatusFail}, StatusFail},
		{"error before fail", []Status{StatusError, StatusFail}, StatusError},
		{"error after fail", []Status{StatusFail, StatusError}, StatusError},
		{"skipped", []Status{StatusPass, StatusSkip}, StatusError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcomes := make([]Outcome, len(tc.statuses))
			for i, status := range tc.statuses {
				outcomes[i] = Outcome{Status: status}
			}
			aggregator := DefaultOutcomeAggregator()
			result, status := aggregator.Aggregate(outcomes, 2, 3)
			if aggregator.Strategy() != "all_required" || status != tc.want || result.Status != tc.want || result.Summary.Total != len(outcomes) || result.TurnsExecuted != 2 || result.TurnsTotal != 3 {
				t.Fatalf("strategy=%s status=%s result=%+v", aggregator.Strategy(), status, result)
			}
		})
	}
}
