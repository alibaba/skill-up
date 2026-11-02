package evaluator

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/judge"
)

// TestExecuteCaseMetricPolicyVerdictReachesCaseTelemetry checks that the metric
// policy's revised verdict reaches the shared case record before the deferred
// telemetry runs. The judge phase writes its grading into the caller's record and
// the finalizer then works on a copy, so a threshold miss or a contract violation
// can leave the persisted case FAIL/ERROR while the case span and the
// skill_up.case.run.count still say PASS.
func TestExecuteCaseMetricPolicyVerdictReachesCaseTelemetry(t *testing.T) {
	cases := []struct {
		name    string
		caseID  string
		payload string
		want    judge.Status
	}{
		{name: "threshold miss", caseID: "otel-threshold-miss", payload: scorePayload("0.4"), want: judge.StatusFail},
		{name: "contract violation", caseID: "otel-contract-error", payload: `{"schema_version":1,"invocation_id":"__INVOCATION__","metrics":[]}`, want: judge.StatusError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			original := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() {
				_ = provider.Shutdown(context.Background())
				otel.SetTracerProvider(original)
			})

			dir := t.TempDir()
			limit := 30
			member := config.JudgeConfig{
				ID: "scorer", Type: "script",
				ScriptPath:     metricsPayloadScript(t, dir, tc.payload),
				TimeoutSeconds: &limit,
				Metrics:        thresholdRegistry(),
			}
			e := newTestEvaluator(EvalOptions{
				Agent: &mockAgent{name: "test", output: "done"}, OutputDir: t.TempDir(),
				EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judge: member},
			})
			result := e.executeCase(context.Background(), &config.CaseConfig{ID: tc.caseID, Input: config.Input{Prompt: "hello"}}, "with_skill", newScriptRuntime(t), nil)
			// Both axes are reported, so a failing run shows the exact divergence the
			// fix removes: persisted verdict vs case span status.
			spanStatus := caseSpanStatus(t, recorder.Ended(), tc.caseID)
			t.Logf("persisted verdict = %s, case span status = %s", result.Status, spanStatus)
			if result.Status != tc.want {
				t.Errorf("persisted verdict = %s error=%v, want %s", result.Status, result.Error, tc.want)
			}
			if spanStatus != string(tc.want) {
				t.Errorf("case span status = %q, want %q to match the persisted verdict", spanStatus, tc.want)
			}
		})
	}
}

// findEndedCaseSpan returns the completed evaluator.case span for one case id, so
// consecutive sub-tests cannot read each other's recording.
func findEndedCaseSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, caseID string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name() != "evaluator.case" {
			continue
		}
		for _, kv := range span.Attributes() {
			if string(kv.Key) == "skill_up.case.id" && kv.Value.AsString() == caseID {
				return span
			}
		}
	}
	t.Fatalf("no evaluator.case span recorded for case %q: %v spans", caseID, len(spans))
	return nil
}

// TestExecuteCaseMultiTurnMetricPolicyVerdictReachesCaseTelemetry covers the same
// write-back on the multi-turn route: executeCaseOnce delegates to the multi-turn
// engine and used to return its result without updating the record the deferred
// telemetry reads, so a metric decision on a multi-turn case left the case span
// (and the case counter, which shares that one read) on the pre-judge status.
func TestExecuteCaseMultiTurnMetricPolicyVerdictReachesCaseTelemetry(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	original := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(original)
	})

	dir := t.TempDir()
	limit := 30
	member := config.JudgeConfig{
		ID:             "scorer",
		Type:           "script",
		ScriptPath:     metricsPayloadScript(t, dir, scorePayload("0.4")),
		TimeoutSeconds: &limit,
		Metrics:        thresholdRegistry(),
	}
	e := newTestEvaluator(EvalOptions{
		Agent:     &mockResumerAgent{mockAgent: mockAgent{name: "test", output: "done"}},
		OutputDir: t.TempDir(),
		EvalCfg:   &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judge: member},
	})
	caseCfg := &config.CaseConfig{ID: "otel-multi-turn", Input: config.Input{Turns: []config.Turn{
		{Role: "user", Content: "first"},
		{Role: "user", Content: "second"},
	}}}
	result := e.executeCase(context.Background(), caseCfg, "with_skill", newScriptRuntime(t), &mockResumerAgent{mockAgent: mockAgent{name: "test", output: "done"}})

	spanStatus := caseSpanStatus(t, recorder.Ended(), "otel-multi-turn")
	t.Logf("persisted verdict = %s, case span status = %s", result.Status, spanStatus)
	if result.Status != judge.StatusFail {
		t.Errorf("persisted verdict = %s error=%v, want FAIL", result.Status, result.Error)
	}
	if result.Metrics == nil || result.Metrics.Quality != judge.QualityNotMet {
		t.Fatalf("metrics = %+v, want NOT_MET", result.Metrics)
	}
	if result.Turns != 2 {
		t.Fatalf("turns = %d, want the multi-turn route to have run both turns", result.Turns)
	}
	if spanStatus != string(judge.StatusFail) {
		t.Errorf("case span status = %q, want FAIL to match the persisted verdict", spanStatus)
	}
}

// caseSpanStatus reads the case-status attribute the deferred telemetry writes.
// The counter datapoint uses the same read site (one local computed once), so this
// is where a stale case record would show up.
func caseSpanStatus(t *testing.T, spans []sdktrace.ReadOnlySpan, caseID string) string {
	t.Helper()
	span := findEndedCaseSpan(t, spans, caseID)
	for _, kv := range span.Attributes() {
		if string(kv.Key) == "skill_up.case.status" {
			return kv.Value.AsString()
		}
	}
	t.Fatalf("case span for %q has no skill_up.case.status attribute: %v", caseID, span.Attributes())
	return ""
}
