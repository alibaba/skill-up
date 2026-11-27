package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alibaba/skill-up/internal/judge"
)

// The named-metrics envelope has to survive the real artifact writer, not just
// struct marshalling: evaluation.json is what downstream tooling reads.

func plannedSlots() []judge.MetricSlot {
	return []judge.MetricSlot{{ID: "correctness", Required: true}, {ID: "completeness"}}
}

func collectedEnvelope(t *testing.T, judgeID string) *judge.MetricsEnvelope {
	t.Helper()
	slots := plannedSlots()
	payload := `{"schema_version":1,"invocation_id":"inv-1","metrics":[{"metric_id":"correctness","availability":"AVAILABLE","value":0,"evidence":["explicit zero"]},{"metric_id":"completeness","availability":"AVAILABLE","value":0.6,"evidence":["partial"]}]}`
	emitted, problem := judge.ParseMetricsPayload([]byte(payload), "inv-1")
	if problem != judge.ProblemNone {
		t.Fatalf("payload rejected: %s", problem)
	}
	env := judge.MaterializeSlots(judgeID, slots, emitted, judge.ProblemNone, judge.MetricProduced)
	if env.Coverage.Available != 2 {
		t.Fatalf("envelope = %+v", env)
	}
	return env
}

func TestGroupedEvaluationPersistsMetricsAndExecution(t *testing.T) {
	dir := t.TempDir()
	ws, err := NewIterationWorkspace(dir, "test-skill", 1)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	env := collectedEnvelope(t, "a")
	evaluation := &GroupedEvaluation{
		Version: 1,
		JudgeResults: []judge.Outcome{
			{
				ID: "a", Type: "script", Status: judge.StatusPass,
				Metrics:   env,
				Execution: &judge.ExecutionMetadata{Stage: judge.StageFinished, Completion: judge.CompletionFinished, Cause: judge.CauseNone},
			},
			{
				ID: "b", Type: "script", Status: judge.StatusSkip, SkipReason: "cancelled before invocation",
				Metrics: judge.MaterializeSlots("b", plannedSlots(), nil, judge.ProblemNone, judge.MetricPrevented),
				Execution: &judge.ExecutionMetadata{
					Stage: judge.StageNotInvoked, Completion: judge.CompletionInterrupted,
					Cause: judge.CauseCancellation, CancelSource: judge.CancelOSSignal,
				},
			},
		},
		Aggregation: GroupAggregation{
			Strategy: "all_required", Status: judge.StatusError,
			Completion: judge.CompletionInterrupted, Cause: judge.CauseCancellation,
		},
	}
	if err := ws.EnsureDirs([]string{"case-1"}); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	if err := ws.WriteEvaluation("case-1", "with_skill", evaluation); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(ws.ConfigDir("case-1", "with_skill"), "evaluation.json"))
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	var back GroupedEvaluation
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("read back: %v", err)
	}
	assertPersistedGroup(t, back)
}

func assertPersistedGroup(t *testing.T, back GroupedEvaluation) {
	t.Helper()
	if back.Aggregation.Completion != judge.CompletionInterrupted || back.Aggregation.Cause != judge.CauseCancellation {
		t.Fatalf("group state lost: %+v", back.Aggregation)
	}
	first, second := back.JudgeResults[0], back.JudgeResults[1]
	if first.Metrics == nil || first.Metrics.Coverage.Planned != 2 || first.Metrics.Coverage.Available != 2 {
		t.Fatalf("member a envelope = %+v", first.Metrics)
	}
	if first.Metrics.Quality != judge.QualityNotConfigured {
		t.Fatalf("member a quality = %s, want NOT_CONFIGURED", first.Metrics.Quality)
	}
	// The explicit zero must survive as zero, not as null and not as absent.
	var zeroValue *float64
	for _, metric := range first.Metrics.Metrics {
		if metric.MetricID == "correctness" {
			zeroValue = metric.Value
		}
	}
	if zeroValue == nil || *zeroValue != 0 {
		t.Fatalf("explicit zero not persisted: %v", zeroValue)
	}
	assertPersistedSecondMember(t, second)
	// Legacy compatibility fields are untouched.
	if first.Status != judge.StatusPass || second.Status != judge.StatusSkip || second.SkipReason == "" {
		t.Fatalf("compat fields changed: %+v %+v", first, second)
	}
}

func assertPersistedSecondMember(t *testing.T, second judge.Outcome) {
	t.Helper()
	if second.Execution == nil || second.Execution.Stage != judge.StageNotInvoked ||
		second.Execution.Completion != judge.CompletionInterrupted ||
		second.Execution.CancelSource != judge.CancelOSSignal {
		t.Fatalf("member b execution = %+v", second.Execution)
	}
	if second.Metrics == nil || second.Metrics.Coverage.NotProduced != 2 {
		t.Fatalf("member b metrics = %+v", second.Metrics)
	}
	for _, metric := range second.Metrics.Metrics {
		if metric.Availability != judge.NotProduced || metric.Value != nil {
			t.Fatalf("persisted slot = %+v", metric)
		}
	}
}
