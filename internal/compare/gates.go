package compare

import "fmt"

// EvaluateGates evaluates the configured comparison gates.
func EvaluateGates(result Result, options Options) GateResult {
	failures := make([]string, 0, 2)
	regressions := len(result.Cases.Regressed)
	if options.FailOnRegression && regressions > 0 {
		failures = append(failures, fmt.Sprintf("%d case(s) regressed", regressions))
	}
	if options.MaxRegressions != nil && regressions > *options.MaxRegressions {
		failures = append(failures, fmt.Sprintf("%d regressions exceeds maximum %d", regressions, *options.MaxRegressions))
	}
	if options.MaxTokenIncreasePercent != nil {
		oldTokens := result.Run.Old.TotalTokens
		newTokens := result.Run.New.TotalTokens
		switch {
		case oldTokens == 0 && newTokens > 0:
			failures = append(failures, "token increase exceeds limit: old total tokens is 0")
		case oldTokens > 0:
			increasePercent := float64(newTokens-oldTokens) / float64(oldTokens) * 100
			if increasePercent > *options.MaxTokenIncreasePercent {
				failures = append(failures, fmt.Sprintf("token increase %.2f%% exceeds limit %.2f%%", increasePercent, *options.MaxTokenIncreasePercent))
			}
		}
	}
	return GateResult{Passed: len(failures) == 0, Failures: failures}
}
