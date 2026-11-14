package report

import "github.com/alibaba/skill-up/internal/judge"

// GroupedEvaluation is the canonical machine-readable multi-judge result.
type GroupedEvaluation struct {
	Version      int                 `json:"version"`
	Gates        *judge.ExpectResult `json:"gates,omitempty"`
	JudgeResults []judge.Outcome     `json:"judge_results"`
	Aggregation  GroupAggregation    `json:"aggregation"`
}

// GroupAggregation records the case-level decision without flattening criteria.
type GroupAggregation struct {
	Strategy string       `json:"strategy"`
	Status   judge.Status `json:"status"`
}
