// Package report — grading.go implements Anthropic-compatible grading.json
// and eval_metadata.json writers.
//
// These formats align with Anthropic's skill-creator evaluation outputs,
// enabling interoperability with eval-viewer and other Anthropic tooling.
package report

import (
	"github.com/alibaba/skill-up/internal/judge"
)

// ---------------------------------------------------------------------------
// grading.json — Anthropic format
// ---------------------------------------------------------------------------

// AnthropicGrading corresponds to the Anthropic grading.json schema.
//
// Example output (from demo/chinese-jokes-workspace):
//
//	{
//	  "expectations": [
//	    {"text": "...", "passed": true, "evidence": "..."}
//	  ],
//	  "summary": {"passed": 5, "failed": 0, "total": 5, "pass_rate": 1.0}
//	}
type AnthropicGrading struct {
	Expectations []AnthropicExpectation `json:"expectations"`
	Summary      AnthropicSummary       `json:"summary"`
	JudgeContext *judge.ContextMetadata `json:"judge_context,omitempty"`
}

// AnthropicExpectation is a single expectation result in the Anthropic format.
type AnthropicExpectation struct {
	Text     string `json:"text"`
	Passed   bool   `json:"passed"`
	Evidence string `json:"evidence"`
}

// AnthropicSummary holds aggregate pass/fail statistics in the Anthropic format.
type AnthropicSummary struct {
	Passed   int     `json:"passed"`
	Failed   int     `json:"failed"`
	Total    int     `json:"total"`
	PassRate float64 `json:"pass_rate"`
}

// ConvertToAnthropicGrading converts an internal judge.Result to the Anthropic
// grading.json format.
//
// Mapping:
//   - judge.AssertionResult.Text     -> AnthropicExpectation.Text
//   - judge.AssertionResult.Passed   -> AnthropicExpectation.Passed
//   - judge.AssertionResult.Evidence -> AnthropicExpectation.Evidence
//   - judge.ResultSummary            -> AnthropicSummary (direct field mapping)
func ConvertToAnthropicGrading(result *judge.Result) *AnthropicGrading {
	if result == nil {
		return &AnthropicGrading{
			Expectations: []AnthropicExpectation{},
			Summary:      AnthropicSummary{},
		}
	}

	expectations := make([]AnthropicExpectation, 0, len(result.AssertionResults))
	for _, ar := range result.AssertionResults {
		expectations = append(expectations, AnthropicExpectation{
			Text:     ar.Text,
			Passed:   ar.Passed,
			Evidence: ar.Evidence,
		})
	}

	return &AnthropicGrading{
		Expectations: expectations,
		Summary: AnthropicSummary{
			Passed:   result.Summary.Passed,
			Failed:   result.Summary.Failed,
			Total:    result.Summary.Total,
			PassRate: result.Summary.PassRate,
		},
		JudgeContext: result.JudgeContext,
	}
}

// WriteGradingJSON writes an AnthropicGrading to the specified file path
// as formatted JSON.
func WriteGradingJSON(path string, grading *AnthropicGrading) error {
	return writeJSONFile(path, grading, "grading json")
}

// ---------------------------------------------------------------------------
// eval_metadata.json — Anthropic format
// ---------------------------------------------------------------------------

// EvalMetadata corresponds to the per-case eval_metadata.json file.
//
// Example output:
//
//	{
//	  "eval_id": 1,
//	  "eval_name": "bored-coding",
//	  "prompt": "I'm so bored after coding all afternoon, my brain is fried.",
//	  "assertions": ["...", "..."]
//	}
type EvalMetadata struct {
	EvalID     int      `json:"eval_id"`
	EvalName   string   `json:"eval_name"`
	Prompt     string   `json:"prompt"`
	Assertions []string `json:"assertions"`
}

// WriteEvalMetadata writes an EvalMetadata to the specified file path
// as formatted JSON.
func WriteEvalMetadata(path string, meta *EvalMetadata) error {
	return writeJSONFile(path, meta, "eval metadata")
}
