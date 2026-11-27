package report

import "github.com/alibaba/skill-up/internal/judge"

// GroupedEvaluation is the canonical machine-readable multi-judge result.
type GroupedEvaluation struct {
	Version      int                      `json:"version"`
	Gates        *judge.ExpectResult      `json:"gates,omitempty"`
	JudgeResults []judge.Outcome          `json:"judge_results"`
	Aggregation  GroupAggregation         `json:"aggregation"`
	Execution    *judge.ExecutionMetadata `json:"execution,omitempty"`
	Metrics      *judge.MetricsEnvelope   `json:"metrics,omitempty"`
}

// GroupAggregation records the case-level decision without flattening criteria.
//
// Completion and Cause are derived group metadata (#317): completion folds over the
// member completions and never replaces Status, and Cause records why the plan ended
// separately from how far it got.
type GroupAggregation struct {
	Strategy   string           `json:"strategy"`
	Status     judge.Status     `json:"status"`
	Completion judge.Completion `json:"completion_state,omitempty"`
	Cause      judge.Cause      `json:"termination_cause,omitempty"`
}
