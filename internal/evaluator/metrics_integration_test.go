package evaluator

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/runtime"
)

// Integration tests for the named-metrics path through the real evaluator: a
// script judge is executed as a child process by executeCase, its
// invocation-bound result file is collected, and the resulting envelope reaches
// the grouped outcome and the derived group state.

func metricsScript(t *testing.T, dir string, body string) string {
	t.Helper()
	name := "score.sh"
	if goruntime.GOOS == windowsGOOS {
		name = "score.ps1"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

const (
	posixScore = `#!/bin/sh
cat > "$EVAL_METRICS_RESULT_PATH" <<JSON
{"schema_version":1,"invocation_id":"$EVAL_METRICS_INVOCATION_ID","metrics":[{"metric_id":"correctness","availability":"AVAILABLE","value":0,"evidence":["zero is valid"]},{"metric_id":"completeness","availability":"AVAILABLE","value":0.75,"evidence":["ok"]}]}
JSON
echo "scored"
exit 0
`
	powershellScore = "$body = '{\"schema_version\":1,\"invocation_id\":\"' + $env:EVAL_METRICS_INVOCATION_ID + '\",\"metrics\":[{\"metric_id\":\"correctness\",\"availability\":\"AVAILABLE\",\"value\":0,\"evidence\":[\"zero is valid\"]},{\"metric_id\":\"completeness\",\"availability\":\"AVAILABLE\",\"value\":0.75,\"evidence\":[\"ok\"]}]}'\n" +
		"Set-Content -LiteralPath $env:EVAL_METRICS_RESULT_PATH -Value $body\nWrite-Output scored\nexit 0\n"
)

// longWaitPosix and longWaitWindows outlive the judge timeout so a later cancellation
// or deadline lands while the member is still running.
const (
	longWaitPosix   = "#!/bin/sh\nsleep 5\nexit 0\n"
	longWaitWindows = "Start-Sleep -Seconds 5\nexit 0\n"
)

// scoreThenWaitBody emits valid scores and then outlives the judge deadline, so
// a collected result must survive the stop.
func scoreThenWaitBody() string {
	if goruntime.GOOS == windowsGOOS {
		return strings.Replace(powershellScore, "Write-Output scored\nexit 0", "Write-Output scored\nStart-Sleep -Seconds 6\nexit 0", 1)
	}
	return strings.Replace(posixScore, "echo \"scored\"\nexit 0", "echo \"scored\"\nsleep 6\nexit 0", 1)
}

func scoreRegistry() []config.MetricConfig {
	return []config.MetricConfig{{ID: "correctness", Required: true}, {ID: "completeness"}}
}

// newScratchRuntime builds a none runtime on a path the test removes best-effort,
// so a lingering Windows grandchild cannot block harness cleanup.
func newScratchRuntime(t *testing.T, dir string) runtime.Runtime {
	t.Helper()
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", WorkspaceDir: dir})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func newScriptRuntime(t *testing.T) runtime.Runtime {
	t.Helper()
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// metricsScratchDir allocates a workspace outside the directory tree the test
// harness removes itself. On Windows the runtime kills only the direct child, so
// the timed-out script's own wait keeps this directory as its working directory
// until it exits long after the test finished; POSIX process-group kill removes
// it right away, where cleanup succeeds. Leaving it is a harness cost, not a
// verdict: harness cleanup of that path would otherwise fail the test after
// every assertion passed.
func metricsScratchDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "skill-up-metrics-scratch-*") //nolint:usetesting // t.TempDir() cannot own a workspace a killed child still holds
	if err != nil {
		t.Fatalf("scratch dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Logf("scratch workspace %s still held by the timed-out script, left for the OS temp cleaner: %v", dir, err)
		}
	})
	return dir
}

func TestExecuteCaseSingleScriptJudgePersistsMetrics(t *testing.T) {
	dir := t.TempDir()
	script := metricsScript(t, dir, pick(posixScore, powershellScore))
	limit := 30
	member := config.JudgeConfig{
		ID: "scorer", Type: "script", ScriptPath: script,
		TimeoutSeconds: &limit, Metrics: scoreRegistry(),
	}
	ag := &mockAgent{name: "test", output: "done"}
	outputDir := t.TempDir()
	e := newTestEvaluator(EvalOptions{
		Agent: ag, OutputDir: outputDir,
		EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judge: member},
	})
	rt := newScriptRuntime(t)
	result := e.executeCase(context.Background(), &config.CaseConfig{ID: "m1", Input: config.Input{Prompt: "hello"}}, "with_skill", rt, nil)
	assertSingleScriptMetricPersistence(t, result)
}

func assertSingleScriptMetricPersistence(t *testing.T, result EvalResult) {
	t.Helper()
	if result.Status != judge.StatusPass {
		t.Fatalf("status = %s, want PASS (%v)", result.Status, result.Error)
	}
	env := singleScriptMetricEnvelope(t, result)
	assertMetricEnvelopeIdentityAndCoverage(t, env, result.Metrics)
	assertMetricValues(t, env.Metrics)
}

func singleScriptMetricEnvelope(t *testing.T, result EvalResult) *judge.MetricsEnvelope {
	t.Helper()
	if result.Grading == nil {
		t.Fatal("grading result is nil")
	}
	if result.Grading.Metrics == nil {
		t.Fatalf("expected a collected envelope, got %+v", result.Grading)
	}
	return result.Grading.Metrics
}

func assertMetricEnvelopeIdentityAndCoverage(t *testing.T, env, caseEnv *judge.MetricsEnvelope) {
	t.Helper()
	if env.JudgeID != "scorer" || caseEnv == nil || caseEnv.JudgeID != "scorer" {
		t.Fatalf("configured judge identity not retained: grading=%+v case=%+v", env, caseEnv)
	}
	if env.Coverage.Planned != 2 || env.Coverage.Available != 2 {
		t.Fatalf("coverage = %+v", env.Coverage)
	}
	if env.Quality != judge.QualityNotConfigured {
		t.Fatalf("quality = %s, want NOT_CONFIGURED (valid scores, no threshold decision)", env.Quality)
	}
}

func assertMetricValues(t *testing.T, metrics []judge.MetricResult) {
	t.Helper()
	var zero, partial bool
	for _, metric := range metrics {
		if metric.Value == nil {
			t.Fatalf("slot %s has no value", metric.MetricID)
		}
		if metric.MetricID == "correctness" && *metric.Value == 0 {
			zero = true
		}
		if metric.MetricID == "completeness" && *metric.Value == 0.75 {
			partial = true
		}
	}
	if !zero || !partial {
		t.Fatalf("collected values wrong: %+v", metrics)
	}
}

func TestExecuteCaseSingleScriptMetricContractControlsVerdict(t *testing.T) {
	for _, tc := range []struct {
		name     string
		payload  string
		registry []config.MetricConfig
		want     judge.Status
		quality  judge.Quality
		complete judge.Completion
		coverage judge.Coverage
	}{
		{
			name:     "threshold miss fails",
			payload:  `{"schema_version":1,"invocation_id":"__INVOCATION__","metrics":[{"metric_id":"correctness","availability":"AVAILABLE","value":0.4}]}`,
			registry: []config.MetricConfig{{ID: "correctness", Required: true, MinScore: new(0.7)}},
			want:     judge.StatusFail, quality: judge.QualityNotMet, complete: judge.CompletionFinished,
			coverage: judge.Coverage{Planned: 1, Available: 1},
		},
		{
			name:     "required missing is error",
			payload:  `{"schema_version":1,"invocation_id":"__INVOCATION__","metrics":[{"metric_id":"optional","availability":"AVAILABLE","value":0.8}]}`,
			registry: []config.MetricConfig{{ID: "correctness", Required: true}, {ID: "optional"}},
			want:     judge.StatusError, quality: judge.QualityNotConfigured, complete: judge.CompletionIncomplete,
			coverage: judge.Coverage{Planned: 2, Available: 1, Missing: 1},
		},
		{
			name:     "malformed payload is error",
			payload:  `not-json`,
			registry: []config.MetricConfig{{ID: "correctness", Required: true}},
			want:     judge.StatusError, quality: judge.QualityNotAssessed, complete: judge.CompletionFailed,
			coverage: judge.Coverage{Planned: 1, Errored: 1},
		},
		{
			name:     "optional-only empty output remains completed",
			payload:  `{"schema_version":1,"invocation_id":"__INVOCATION__","metrics":[]}`,
			registry: []config.MetricConfig{{ID: "optional"}},
			want:     judge.StatusPass, quality: judge.QualityNotAssessed, complete: judge.CompletionFinished,
			coverage: judge.Coverage{Planned: 1, Missing: 1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := metricsPayloadScript(t, dir, tc.payload)
			limit := 30
			member := config.JudgeConfig{ID: "scorer", Type: "script", ScriptPath: script, TimeoutSeconds: &limit, Metrics: tc.registry}
			e := newTestEvaluator(EvalOptions{
				Agent: &mockAgent{name: "test", output: "done"}, OutputDir: t.TempDir(),
				EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judge: member},
			})
			result := e.executeCase(context.Background(), &config.CaseConfig{ID: "metric-policy", Input: config.Input{Prompt: "hello"}}, "with_skill", newScriptRuntime(t), nil)
			if result.Status != tc.want {
				t.Fatalf("status=%s error=%v, want %s", result.Status, result.Error, tc.want)
			}
			if result.Metrics == nil || result.Metrics.Quality != tc.quality {
				t.Fatalf("metrics=%+v, want quality %s", result.Metrics, tc.quality)
			}
			if got := result.Metrics.Coverage; got.Planned != tc.coverage.Planned || got.Available != tc.coverage.Available ||
				got.Missing != tc.coverage.Missing || got.Errored != tc.coverage.Errored ||
				got.Skipped != tc.coverage.Skipped || got.NotProduced != tc.coverage.NotProduced {
				t.Fatalf("coverage = %+v, want %+v", got, tc.coverage)
			}
			if result.Execution == nil || result.Execution.Completion != tc.complete {
				t.Fatalf("execution=%+v, want completion %s", result.Execution, tc.complete)
			}
		})
	}
}

func metricsPayloadScript(t *testing.T, dir, payload string) string {
	t.Helper()
	return metricsScriptWithTail(t, dir, payload, "exit 0\n")
}

// metricsScriptWithTail writes an invocation-bound result file, then runs tail,
// which carries the script's own wait and exit status.
func metricsScriptWithTail(t *testing.T, dir, payload, tail string) string {
	t.Helper()
	if goruntime.GOOS == windowsGOOS {
		body := "$body = '" + payload + "' -replace '__INVOCATION__', $env:EVAL_METRICS_INVOCATION_ID\nSet-Content -LiteralPath $env:EVAL_METRICS_RESULT_PATH -Value $body\n" + tail
		return metricsScript(t, dir, body)
	}
	body := "#!/bin/sh\ncat > \"$EVAL_METRICS_RESULT_PATH\" <<JSON\n" + strings.ReplaceAll(payload, "__INVOCATION__", "$EVAL_METRICS_INVOCATION_ID") + "\nJSON\n" + tail
	return metricsScript(t, dir, body)
}

// scorePayload emits one AVAILABLE correctness metric with the given value.
func scorePayload(value string) string {
	return `{"schema_version":1,"invocation_id":"__INVOCATION__","metrics":[{"metric_id":"correctness","availability":"AVAILABLE","value":` + value + `}]}`
}

// thresholdRegistry plans one required correctness metric with a 0.8 threshold.
func thresholdRegistry() []config.MetricConfig {
	return []config.MetricConfig{{ID: "correctness", Required: true, MinScore: new(0.8)}}
}

// outlivesDeadline waits past any judge deadline in the fixture's configuration.
func outlivesDeadline() string {
	return pick("sleep 6\nexit 0\n", "Start-Sleep -Seconds 6\nexit 0\n")
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func assertTimeoutProvenance(t *testing.T, ex *judge.ExecutionMetadata) {
	t.Helper()
	if ex == nil || ex.Cause != judge.CauseDeadlineExpiry || ex.DeadlineSource != judgeTimeoutSource ||
		ex.Timeout == nil || !ex.Timeout.Occurred {
		t.Fatalf("timeout provenance = %+v", ex)
	}
}

func assertCollectedScoreAndDecision(t *testing.T, env *judge.MetricsEnvelope, want float64, quality judge.Quality, diagnostic string) {
	t.Helper()
	if env == nil || env.Coverage.Available != 1 || len(env.Metrics) != 1 {
		t.Fatalf("metrics = %+v, want one AVAILABLE slot", env)
	}
	if env.Metrics[0].Value == nil || *env.Metrics[0].Value != want {
		t.Fatalf("collected score = %+v, want %v", env.Metrics[0], want)
	}
	if env.Quality != quality {
		t.Fatalf("quality = %s, want %s", env.Quality, quality)
	}
	if !slices.Contains(env.Diagnostics, diagnostic) {
		t.Fatalf("diagnostics = %v, want %q", env.Diagnostics, diagnostic)
	}
}

// TestExecuteCaseGroupedTimeoutBelowThresholdKeepsRetryableError reproduces the
// reported interaction: the script writes a valid below-threshold score and is
// then killed by its own deadline. Both facts are real, so both must be
// reported - but the numerical decision may not replace the execution ERROR
// that makes the case retryable as a timeout.
func TestExecuteCaseGroupedTimeoutBelowThresholdKeepsRetryableError(t *testing.T) {
	scratch := metricsScratchDir(t)
	slowDir := filepath.Join(scratch, "slow")
	if err := os.MkdirAll(slowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	two := 2
	members := []config.JudgeConfig{{
		ID: "scorer", Type: "script",
		ScriptPath:     metricsScriptWithTail(t, slowDir, scorePayload("0.2"), outlivesDeadline()),
		TimeoutSeconds: &two,
		Metrics:        thresholdRegistry(),
	}}
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test", output: "done"}, OutputDir: t.TempDir(),
		EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judges: &members},
	})
	result := e.executeCase(context.Background(), &config.CaseConfig{ID: "timeout-threshold", Input: config.Input{Prompt: "hello"}}, "with_skill", newScratchRuntime(t, scratch), nil)

	if len(result.JudgeResults) != 1 {
		t.Fatalf("outcomes = %d", len(result.JudgeResults))
	}
	member := result.JudgeResults[0]
	assertTimeoutProvenance(t, member.Execution)
	if member.Status != judge.StatusError {
		t.Fatalf("member verdict = %s, want ERROR: a threshold miss may not downgrade an execution error", member.Status)
	}
	if result.Status != judge.StatusError {
		t.Fatalf("case verdict = %s, want ERROR", result.Status)
	}
	assertCollectedScoreAndDecision(t, member.Metrics, 0.2, judge.QualityNotMet, "threshold_not_met: correctness")
	if reason, ok := retryReasonForResult(result); !ok || reason != retryReasonTimeout {
		t.Fatalf("retry classification = %q/%v, want a retryable timeout", reason, ok)
	}
}

// TestExecuteCaseSingleTimeoutBelowThresholdKeepsLegacyVerdict covers the same
// interaction on the single-judge path, where the pre-existing script-timeout
// mapping already produced FAIL. The verdict and the compatibility counts must
// stay exactly as the execution layer reported them.
func TestExecuteCaseSingleTimeoutBelowThresholdKeepsLegacyVerdict(t *testing.T) {
	scratch := metricsScratchDir(t)
	dir := t.TempDir()
	one := 1
	member := config.JudgeConfig{
		ID: "scorer", Type: "script",
		ScriptPath:     metricsScriptWithTail(t, dir, scorePayload("0.2"), outlivesDeadline()),
		TimeoutSeconds: &one,
		Metrics:        thresholdRegistry(),
	}
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test", output: "done"}, OutputDir: t.TempDir(),
		EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judge: member},
	})
	result := e.executeCase(context.Background(), &config.CaseConfig{ID: "single-timeout-threshold", Input: config.Input{Prompt: "hello"}}, "with_skill", newScratchRuntime(t, scratch), nil)

	if result.Status != judge.StatusFail {
		t.Fatalf("status = %s error=%v, want the legacy timeout FAIL", result.Status, result.Error)
	}
	assertTimeoutProvenance(t, result.Execution)
	assertCollectedScoreAndDecision(t, result.Metrics, 0.2, judge.QualityNotMet, "threshold_not_met: correctness")
	want := judge.ResultSummary{Failed: 1, Total: 1}
	if result.Grading == nil || result.Grading.Summary != want {
		t.Fatalf("compatibility denominators = %+v, want %+v", result.Grading, want)
	}
}

// TestExecuteCaseNonzeroExitAfterWritingMetricsKeepsProcessFailure addresses the
// reclassification of a failed process as a missing-required-score contract
// error. The script writes a score that would clear the threshold and then exits
// 3: the file is diagnostic-only, the slots were never produced, and the verdict
// stays FAIL with cause process_exit on both paths.
func TestExecuteCaseNonzeroExitAfterWritingMetricsKeepsProcessFailure(t *testing.T) {
	payload, tail := scorePayload("0.9"), "exit 3\n"
	registry := thresholdRegistry()
	t.Run("single", func(t *testing.T) {
		dir := t.TempDir()
		limit := 30
		member := config.JudgeConfig{ID: "scorer", Type: "script", ScriptPath: metricsScriptWithTail(t, dir, payload, tail), TimeoutSeconds: &limit, Metrics: registry}
		e := newTestEvaluator(EvalOptions{
			Agent: &mockAgent{name: "test", output: "done"}, OutputDir: t.TempDir(),
			EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judge: member},
		})
		result := e.executeCase(context.Background(), &config.CaseConfig{ID: "exit-three", Input: config.Input{Prompt: "hello"}}, "with_skill", newScriptRuntime(t), nil)
		assertProcessFailure(t, result.Status, errorText(result.Error), result.Execution, result.Metrics)
	})
	t.Run("grouped", func(t *testing.T) {
		dir := t.TempDir()
		limit := 30
		members := []config.JudgeConfig{{ID: "scorer", Type: "script", ScriptPath: metricsScriptWithTail(t, dir, payload, tail), TimeoutSeconds: &limit, Metrics: registry}}
		e := newTestEvaluator(EvalOptions{
			Agent: &mockAgent{name: "test", output: "done"}, OutputDir: t.TempDir(),
			EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judges: &members},
		})
		result := e.executeCase(context.Background(), &config.CaseConfig{ID: "exit-three-grouped", Input: config.Input{Prompt: "hello"}}, "with_skill", newScriptRuntime(t), nil)
		if len(result.JudgeResults) != 1 {
			t.Fatalf("outcomes = %d", len(result.JudgeResults))
		}
		member := result.JudgeResults[0]
		assertProcessFailure(t, member.Status, member.Error, member.Execution, member.Metrics)
		if result.Status != judge.StatusFail {
			t.Fatalf("group verdict = %s, want FAIL", result.Status)
		}
		if result.GroupCompletion != judge.CompletionFailed || result.GroupCause != judge.CauseProcessExit {
			t.Fatalf("group = %s/%s, want FAILED/process_exit", result.GroupCompletion, result.GroupCause)
		}
	})
}

func assertProcessFailure(t *testing.T, status judge.Status, errorText string, ex *judge.ExecutionMetadata, env *judge.MetricsEnvelope) {
	t.Helper()
	if status != judge.StatusFail {
		t.Fatalf("status = %s, want FAIL: an unsuccessful process outranks score validation", status)
	}
	if strings.Contains(errorText, "named metric") {
		t.Fatalf("a metric contract complaint replaced the process failure: %q", errorText)
	}
	if ex == nil || ex.Stage != judge.StageFinished || ex.Completion != judge.CompletionFailed || ex.Cause != judge.CauseProcessExit {
		t.Fatalf("execution = %+v, want FINISHED/FAILED/process_exit", ex)
	}
	if env == nil || env.Coverage.Planned != 1 || env.Coverage.NotProduced != 1 || env.Coverage.Available != 0 {
		t.Fatalf("metrics = %+v, want the planned slot NOT_PRODUCED", env)
	}
	if env.Quality != judge.QualityUnknown {
		t.Fatalf("quality = %s, want UNKNOWN for an unproduced thresholded slot", env.Quality)
	}
	if !slices.Contains(env.Diagnostics, "result_file_not_accepted") {
		t.Fatalf("diagnostics = %v, want the written file recorded as unaccepted", env.Diagnostics)
	}
}

// TestExecuteCaseMetricPolicyKeepsCompatibilityDenominators pins the agreed
// separation: a metric decision may change the verdict, but it never adds a
// compatibility assertion or a member denominator entry.
// scriptMetricMember builds one script judge whose script emits payload.
func scriptMetricMember(t *testing.T, id, payload string, registry []config.MetricConfig) config.JudgeConfig {
	t.Helper()
	limit := 30
	return config.JudgeConfig{
		ID:             id,
		Type:           "script",
		ScriptPath:     metricsPayloadScript(t, t.TempDir(), payload),
		TimeoutSeconds: &limit,
		Metrics:        registry,
	}
}

// evaluateMetricPolicyCase runs one case through the real evaluator with the
// given members: a single member takes the single-judge path, more than one the
// grouped path.
func evaluateMetricPolicyCase(t *testing.T, members []config.JudgeConfig) EvalResult {
	t.Helper()
	cfg := &config.EvalConfig{Environment: config.Environment{Type: "none"}}
	if len(members) == 1 {
		cfg.Judge = members[0]
	} else {
		list := members
		cfg.Judges = &list
	}
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test", output: "done"}, OutputDir: t.TempDir(), EvalCfg: cfg,
	})
	return e.executeCase(context.Background(), &config.CaseConfig{ID: "metric-denominators", Input: config.Input{Prompt: "hello"}}, "with_skill", newScriptRuntime(t), nil)
}

// TestExecuteCaseThresholdMissChangesOnlyTheVerdict pins the agreed separation:
// a configured threshold may decide the verdict, but it must not add a
// compatibility assertion or a denominator entry, because those carry #309
// arithmetic into reports and benchmark projections.
func TestExecuteCaseThresholdMissChangesOnlyTheVerdict(t *testing.T) {
	result := evaluateMetricPolicyCase(t, []config.JudgeConfig{scriptMetricMember(t, "scorer", scorePayload("0.4"), thresholdRegistry())})
	if result.Status != judge.StatusFail {
		t.Fatalf("status = %s, want FAIL", result.Status)
	}
	if result.Grading == nil {
		t.Fatal("grading is nil")
	}
	if want := (judge.ResultSummary{Passed: 1, Total: 1, PassRate: 1}); result.Grading.Summary != want {
		t.Fatalf("summary = %+v, want %+v", result.Grading.Summary, want)
	}
	assertNoMetricContractAssertion(t, result.Grading)
	if !slices.Contains(result.Metrics.Diagnostics, "threshold_not_met: correctness") {
		t.Fatalf("diagnostics = %v, want the missed threshold recorded", result.Metrics.Diagnostics)
	}
}

// TestExecuteCaseGroupedThresholdMissKeepsOneAssertionPerMember is the grouped
// counterpart: the group keeps one assertion per configured member and each
// member keeps its own script assertion alone.
func TestExecuteCaseGroupedThresholdMissKeepsOneAssertionPerMember(t *testing.T) {
	registry := thresholdRegistry()
	result := evaluateMetricPolicyCase(t, []config.JudgeConfig{
		scriptMetricMember(t, "a", scorePayload("0.4"), registry),
		scriptMetricMember(t, "b", scorePayload("0.3"), registry),
	})
	if result.Status != judge.StatusFail || len(result.JudgeResults) != 2 {
		t.Fatalf("status = %s outcomes = %d", result.Status, len(result.JudgeResults))
	}
	if result.Grading == nil || result.Grading.Summary.Total != 2 || result.Grading.Summary.Passed != 0 {
		t.Fatalf("group summary = %+v, want one assertion per member", result.Grading)
	}
	assertNoMetricContractAssertion(t, result.Grading)
	for _, member := range result.JudgeResults {
		if member.Result == nil {
			t.Fatalf("member %s has no grading", member.ID)
		}
		if want := (judge.ResultSummary{Passed: 1, Total: 1, PassRate: 1}); member.Result.Summary != want {
			t.Fatalf("member %s summary = %+v, want the script assertion alone", member.ID, member.Result.Summary)
		}
		assertNoMetricContractAssertion(t, member.Result)
	}
}

// TestExecuteCaseMetricContractErrorKeepsCompatibilityCounts applies the same
// rule to the ERROR side of the metric policy.
func TestExecuteCaseMetricContractErrorKeepsCompatibilityCounts(t *testing.T) {
	result := evaluateMetricPolicyCase(t, []config.JudgeConfig{
		scriptMetricMember(t, "scorer", `{"schema_version":1,"invocation_id":"__INVOCATION__","metrics":[]}`, thresholdRegistry()),
	})
	if result.Status != judge.StatusError || result.Error == nil {
		t.Fatalf("status = %s error = %v, want ERROR with a contract reason", result.Status, result.Error)
	}
	if result.Grading == nil {
		t.Fatal("grading is nil")
	}
	if result.Grading.Summary.Total != 1 || result.Grading.Summary.Passed != 1 {
		t.Fatalf("summary = %+v, want the script assertion alone", result.Grading.Summary)
	}
	assertNoMetricContractAssertion(t, result.Grading)
}

func assertNoMetricContractAssertion(t *testing.T, result *judge.Result) {
	t.Helper()
	for _, assertion := range result.AssertionResults {
		if assertion.Text == "named metric contract" {
			t.Fatalf("metric policy injected a compatibility assertion: %+v", result.AssertionResults)
		}
	}
}

func TestExecuteCaseExpectGatePreservesSingleJudgeSlots(t *testing.T) {
	limit := 30
	member := config.JudgeConfig{ID: "scorer", Type: "script", ScriptPath: "unused", TimeoutSeconds: &limit, Metrics: scoreRegistry()}
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test", output: "done"}, OutputDir: t.TempDir(),
		EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judge: member},
	})
	result := e.executeCase(context.Background(), &config.CaseConfig{
		ID: "gate", Input: config.Input{Prompt: "hello"},
		Expect: config.Expect{MustContain: []string{"required text"}},
	}, "with_skill", newScriptRuntime(t), nil)
	if result.Status != judge.StatusFail || result.Metrics == nil || result.Metrics.Coverage.NotProduced != 2 {
		t.Fatalf("gate result lost planned metrics: status=%s metrics=%+v", result.Status, result.Metrics)
	}
	if result.Execution == nil || result.Execution.Stage != judge.StageNotInvoked || result.Execution.Cause != judge.CauseExpectGate {
		t.Fatalf("gate execution metadata = %+v", result.Execution)
	}
}

func TestExecuteCaseExpectGateFoldsGroupState(t *testing.T) {
	limit := 30
	members := []config.JudgeConfig{
		{ID: "a", Type: "script", ScriptPath: "unused-a", TimeoutSeconds: &limit, Metrics: scoreRegistry()},
		{ID: "b", Type: "script", ScriptPath: "unused-b", TimeoutSeconds: &limit, Metrics: scoreRegistry()},
	}
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test", output: "done"}, OutputDir: t.TempDir(),
		EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judges: &members},
	})
	result := e.executeCase(context.Background(), &config.CaseConfig{
		ID: "group-gate", Input: config.Input{Prompt: "hello"},
		Expect: config.Expect{MustContain: []string{"required text"}},
	}, "with_skill", newScriptRuntime(t), nil)
	if result.Status != judge.StatusFail || result.GroupCompletion != judge.CompletionInterrupted || result.GroupCause != judge.CauseExpectGate {
		t.Fatalf("group gate state status=%s completion=%s cause=%s", result.Status, result.GroupCompletion, result.GroupCause)
	}
}

func TestExecuteCaseSingleScriptTimeoutRecordsSourceAndKeepsScores(t *testing.T) {
	dir := t.TempDir()
	script := metricsScript(t, dir, scoreThenWaitBody())
	one := 1
	member := config.JudgeConfig{ID: "scorer", Type: "script", ScriptPath: script, TimeoutSeconds: &one, Metrics: scoreRegistry()}
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test", output: "done"}, OutputDir: t.TempDir(),
		EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judge: member},
	})
	result := e.executeCase(context.Background(), &config.CaseConfig{ID: "single-timeout", Input: config.Input{Prompt: "hello"}}, "with_skill", newScratchRuntime(t, metricsScratchDir(t)), nil)
	if result.Status != judge.StatusFail || result.Metrics == nil || result.Metrics.Coverage.Available != 2 {
		t.Fatalf("timeout verdict/scores status=%s metrics=%+v", result.Status, result.Metrics)
	}
	if result.Execution == nil || result.Execution.Cause != judge.CauseDeadlineExpiry || result.Execution.DeadlineSource != judgeTimeoutSource ||
		result.Execution.Timeout == nil || !result.Execution.Timeout.Occurred {
		t.Fatalf("single timeout provenance = %+v", result.Execution)
	}
}

// TestExecuteCaseGroupedJudgeTimeoutUnderLiveCaseDeadline drives the reviewer's
// shape for real: the case knob is armed at 300s and never fires while the judge
// knob is armed at 2s and does. The member runs as a child process, emits its
// scores, and is then killed by its own deadline, so the stop must name the knob
// that expired rather than the live case deadline above it.
func TestExecuteCaseGroupedJudgeTimeoutUnderLiveCaseDeadline(t *testing.T) {
	scratch := metricsScratchDir(t)
	slowDir := filepath.Join(scratch, "slow")
	if err := os.MkdirAll(slowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	slow := metricsScript(t, slowDir, scoreThenWaitBody())
	two := 2
	members := []config.JudgeConfig{
		{ID: "scorer", Type: "script", ScriptPath: slow, TimeoutSeconds: &two, Metrics: scoreRegistry()},
	}
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test", output: "done"}, OutputDir: t.TempDir(),
		EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judges: &members},
	})
	result := e.executeCase(context.Background(), &config.CaseConfig{
		ID: "live-case-deadline", Input: config.Input{Prompt: "hello"},
		Constraints: config.Constraints{TimeoutSeconds: 300},
	}, "with_skill", newScratchRuntime(t, scratch), nil)

	if len(result.JudgeResults) != 1 {
		t.Fatalf("outcomes = %d", len(result.JudgeResults))
	}
	member := result.JudgeResults[0]
	if member.Execution == nil || member.Execution.Cause != judge.CauseDeadlineExpiry ||
		member.Execution.DeadlineSource != judgeTimeoutSource {
		t.Fatalf("grouped timeout provenance = %+v", member.Execution)
	}
	if member.Execution.Timeout == nil || !member.Execution.Timeout.Occurred ||
		member.Execution.Timeout.Source != judgeTimeoutSource {
		t.Fatalf("timeout metadata = %+v", member.Execution.Timeout)
	}
	// Upstream (before #317) arms grouped script judges with TimeoutIsError=true
	// and the single-judge path with false, so the same judge deadline is an ERROR
	// verdict when grouped and FAIL when alone. What #317 must keep is that the
	// expired knob stays attributable and the scores already collected survive.
	if member.Status != judge.StatusError {
		t.Fatalf("grouped member verdict = %s, want ERROR under the pre-existing strict script timeout", member.Status)
	}
	if member.Metrics == nil || member.Metrics.Coverage.Available != 2 {
		t.Fatalf("collected scores lost on stop: metrics=%+v", member.Metrics)
	}
	if result.GroupCompletion != judge.CompletionInterrupted || result.GroupCause != judge.CauseDeadlineExpiry {
		t.Fatalf("group = %s/%s, want INTERRUPTED/deadline_expiry", result.GroupCompletion, result.GroupCause)
	}
}

func TestExecuteCaseMultiJudgeDerivesGroupStateFromMembers(t *testing.T) {
	dir := t.TempDir()
	script := metricsScript(t, dir, pick(posixScore, powershellScore))
	limit := 30
	members := []config.JudgeConfig{
		{ID: "a", Type: "script", ScriptPath: script, TimeoutSeconds: &limit, Metrics: scoreRegistry()},
		{ID: "b", Type: "script", ScriptPath: script, TimeoutSeconds: &limit, Metrics: scoreRegistry()},
	}
	ag := &mockAgent{name: "test", output: "done"}
	e := newTestEvaluator(EvalOptions{
		Agent: ag, OutputDir: t.TempDir(),
		EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judges: &members},
	})
	rt := newScriptRuntime(t)
	result := e.executeCase(context.Background(), &config.CaseConfig{ID: "multi", Input: config.Input{Prompt: "hello"}}, "with_skill", rt, nil)

	if len(result.JudgeResults) != 2 {
		t.Fatalf("outcomes = %d", len(result.JudgeResults))
	}
	for _, outcome := range result.JudgeResults {
		if outcome.Metrics == nil || outcome.Metrics.Coverage.Available != 2 {
			t.Fatalf("member %s envelope = %+v", outcome.ID, outcome.Metrics)
		}
		if outcome.Metrics.JudgeID != outcome.ID {
			t.Fatalf("member %s persisted producer identity %q, want the configured id", outcome.ID, outcome.Metrics.JudgeID)
		}
		if outcome.Execution == nil || outcome.Execution.Completion != judge.CompletionFinished ||
			outcome.Execution.Stage != judge.StageFinished {
			t.Fatalf("member %s execution = %+v", outcome.ID, outcome.Execution)
		}
	}
	if result.GroupCompletion != judge.CompletionFinished || result.GroupCause != judge.CauseNone {
		t.Fatalf("group = %s/%s, want COMPLETED/none", result.GroupCompletion, result.GroupCause)
	}
	if result.Status != judge.StatusPass {
		t.Fatalf("compat status = %s, want PASS", result.Status)
	}
}

// TestExecuteCaseParentCancellationStopsRemainingMembers drives the real
// grouped path: the first member completes and is accepted, the case context is
// cancelled while the second member is still running, so one member is
// INTERRUPTED(STARTED) and there is no third member left to invent. A cancelled
// accepted result must keep its verdict and its scores.
func TestExecuteCaseCancellationKeepsAcceptedResult(t *testing.T) {
	if goruntime.GOOS == windowsGOOS {
		// The accepted-then-cancelled ordering depends on the first member finishing
		// before the timer fires, which is not deterministic under Windows process
		// startup costs; the deterministic interruption case is covered by
		// TestExecuteCaseCancellationInterruptsRunningMember.
		t.Skip("timing-sensitive accepted-retention ordering; POSIX only")
	}
	scratch := t.TempDir()
	fastDir := filepath.Join(scratch, "fast")
	slowDir := filepath.Join(scratch, "slow")
	if err := os.MkdirAll(fastDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(slowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fast := metricsScript(t, fastDir, pick(posixScore, powershellScore))
	slow := metricsScript(t, slowDir, pick(longWaitPosix, longWaitWindows))
	five := 30
	members := []config.JudgeConfig{
		{ID: "fast", Type: "script", ScriptPath: fast, TimeoutSeconds: &five, Metrics: scoreRegistry()},
		{ID: "slow", Type: "script", ScriptPath: slow, TimeoutSeconds: &five, Metrics: scoreRegistry()},
	}
	ag := &mockAgent{name: "test", output: "done"}
	e := newTestEvaluator(EvalOptions{
		Agent: ag, OutputDir: t.TempDir(),
		EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judges: &members},
	})
	rt := newScratchRuntime(t, scratch)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(1500 * time.Millisecond)
		cancel()
	}()
	result := e.executeCase(ctx, &config.CaseConfig{ID: "cancel", Input: config.Input{Prompt: "hello"}}, "with_skill", rt, nil)
	assertAcceptedAndInterruptedMembers(t, result)
}

func assertAcceptedAndInterruptedMembers(t *testing.T, result EvalResult) {
	t.Helper()
	if len(result.JudgeResults) != 2 {
		t.Fatalf("outcomes = %d, want 2", len(result.JudgeResults))
	}
	assertAcceptedMember(t, result.JudgeResults[0])
	assertInterruptedMember(t, result.JudgeResults[1])
	assertInterruptedGroup(t, result)
}

func assertInterruptedGroup(t *testing.T, result EvalResult) {
	t.Helper()
	if result.GroupCompletion != judge.CompletionInterrupted || result.GroupCause != judge.CauseCancellation {
		t.Fatalf("group = %s/%s, want INTERRUPTED/cancellation", result.GroupCompletion, result.GroupCause)
	}
	if result.Status != judge.StatusError {
		t.Fatalf("compat status = %s, want ERROR for an interrupted member", result.Status)
	}
}

func assertAcceptedMember(t *testing.T, member judge.Outcome) {
	t.Helper()
	if member.Execution == nil || member.Execution.Completion != judge.CompletionFinished || member.Status != judge.StatusPass {
		t.Fatalf("accepted member was not retained: status=%s execution=%+v", member.Status, member.Execution)
	}
	if member.Metrics == nil || member.Metrics.Coverage.Available != 2 {
		t.Fatalf("accepted member metrics = %+v", member.Metrics)
	}
}

func assertInterruptedMember(t *testing.T, member judge.Outcome) {
	t.Helper()
	if member.Execution == nil || member.Execution.Completion != judge.CompletionInterrupted ||
		member.Execution.Stage != judge.StageStarted || member.Execution.Cause != judge.CauseCancellation {
		t.Fatalf("interrupted member = %+v", member.Execution)
	}
	if member.Metrics == nil {
		t.Fatal("interrupted member metrics are nil")
	}
	for _, metric := range member.Metrics.Metrics {
		if metric.Availability != judge.NotProduced || metric.Value != nil {
			t.Fatalf("stopped member slot = %+v, want NOT_PRODUCED with no value", metric)
		}
	}
}

// TestRunMultipleJudgesCancellationBeforeInvocation drives the real grouped loop
// with a case context that is already cancelled, so every member takes the
// never-invoked branch: status SKIP for compatibility, INTERRUPTED completion with
// stage NOT_INVOKED, cause cancellation, and each planned slot NOT_PRODUCED.
func TestRunMultipleJudgesCancellationBeforeInvocation(t *testing.T) {
	dir := t.TempDir()
	script := metricsScript(t, dir, pick(posixScore, powershellScore))
	limit := 30
	members := []config.JudgeConfig{
		{ID: "a", Type: "script", ScriptPath: script, TimeoutSeconds: &limit, Metrics: scoreRegistry()},
		{ID: "b", Type: "script", ScriptPath: script, TimeoutSeconds: &limit, Metrics: scoreRegistry()},
	}
	observer := &SignalObserver{}
	observer.MarkSignalObserved()
	ctx, cancel := context.WithCancel(ContextWithSignalObserver(context.Background(), observer))
	cancel()
	ag := &mockAgent{name: "test", output: "done"}
	e := newTestEvaluator(EvalOptions{
		Agent: ag, OutputDir: t.TempDir(),
		EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judges: &members},
	})
	rt := newScriptRuntime(t)
	result := &EvalResult{SessionResult: &agent.SessionResult{}, CaseID: "pending", CaseName: "pending"}
	out := e.runMultipleJudges(ctx, rt, &config.CaseConfig{ID: "pending", Input: config.Input{Prompt: "hello"}},
		"with_skill", config.JudgePlan{Judges: members, Multi: true}, 1, ag, judge.Input{CaseID: "pending"}, result)

	assertCancelledMembers(t, out)
}

func assertCancelledMembers(t *testing.T, out EvalResult) {
	t.Helper()
	if len(out.JudgeResults) != 2 {
		t.Fatalf("outcomes = %d, want 2: %s", len(out.JudgeResults), describeOutcomes(out.JudgeResults))
	}
	for _, outcome := range out.JudgeResults {
		assertCancelledMember(t, outcome)
	}
	if out.GroupCompletion != judge.CompletionInterrupted || out.GroupCause != judge.CauseCancellation {
		t.Fatalf("group = %s/%s, want INTERRUPTED/cancellation", out.GroupCompletion, out.GroupCause)
	}
	if out.Status != judge.StatusError {
		t.Fatalf("group verdict = %s, want ERROR (all_required unchanged)", out.Status)
	}
	// The #309 denominator stays put: two member assertions, neither passing.
	if out.Grading == nil || out.Grading.Summary.Total != 2 || out.Grading.Summary.Passed != 0 {
		t.Fatalf("compat grading summary = %+v", out.Grading)
	}
}

func assertCancelledMember(t *testing.T, outcome JudgeOutcome) {
	t.Helper()
	if outcome.Status != judge.StatusSkip {
		t.Fatalf("compat status = %s, want SKIP", outcome.Status)
	}
	if outcome.Execution == nil || outcome.Execution.Stage != judge.StageNotInvoked ||
		outcome.Execution.Completion != judge.CompletionInterrupted ||
		outcome.Execution.Cause != judge.CauseCancellation ||
		outcome.Execution.CancelSource != judge.CancelOSSignal {
		t.Fatalf("execution = %+v", outcome.Execution)
	}
	if outcome.Metrics == nil || outcome.Metrics.Coverage.Planned != 2 ||
		outcome.Metrics.Coverage.NotProduced != 2 {
		t.Fatalf("metrics = %+v", outcome.Metrics)
	}
	for _, metric := range outcome.Metrics.Metrics {
		if metric.Availability != judge.NotProduced || metric.Value != nil {
			t.Fatalf("slot = %+v, want NOT_PRODUCED with no value", metric)
		}
	}
	if outcome.Metrics.Quality != judge.QualityNotAssessed {
		t.Fatalf("quality = %s, want NOT_ASSESSED", outcome.Metrics.Quality)
	}
}

func describeOutcomes(outcomes []JudgeOutcome) string {
	parts := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		stage, completion, cause := "?", "?", "?"
		if o.Execution != nil {
			stage, completion, cause = string(o.Execution.Stage), string(o.Execution.Completion), string(o.Execution.Cause)
		}
		parts = append(parts, o.ID+":"+string(o.Status)+"/"+stage+"/"+completion+"/"+cause)
	}
	return strings.Join(parts, " ")
}

func pick(posix, windows string) string {
	if goruntime.GOOS == windowsGOOS {
		return windows
	}
	return posix
}

// TestExecuteCaseCancellationInterruptsRunningMember is deterministic: the case
// context is already cancelled, so the member in flight never reaches a final
// outcome. Its planned slots must read NOT_PRODUCED (never reached) rather than
// MISSING (omitted by a completed judge), while the legacy SKIP/ERROR verdict
// mapping and the member assertion denominator stay unchanged.
func TestExecuteCaseCancellationInterruptsRunningMember(t *testing.T) {
	scratch := t.TempDir()
	script := metricsScript(t, scratch, pick(posixScore, powershellScore))
	limit := 30
	members := []config.JudgeConfig{
		{ID: "only", Type: "script", ScriptPath: script, TimeoutSeconds: &limit, Metrics: scoreRegistry()},
	}
	ag := &mockAgent{name: "test", output: "done"}
	e := newTestEvaluator(EvalOptions{
		Agent: ag, OutputDir: t.TempDir(),
		EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judges: &members},
	})
	rt := newScratchRuntime(t, scratch)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := e.executeCase(ctx, &config.CaseConfig{ID: "interrupt", Input: config.Input{Prompt: "hello"}}, "with_skill", rt, nil)

	if len(result.JudgeResults) != 1 {
		t.Fatalf("outcomes = %d", len(result.JudgeResults))
	}
	outcome := result.JudgeResults[0]
	if outcome.Execution == nil || outcome.Execution.Completion != judge.CompletionInterrupted ||
		outcome.Execution.Cause != judge.CauseCancellation {
		t.Fatalf("execution = %+v", outcome.Execution)
	}
	if outcome.Metrics == nil {
		t.Fatal("expected an envelope for the planned slots")
	}
	for _, metric := range outcome.Metrics.Metrics {
		if metric.Availability != judge.NotProduced || metric.Value != nil {
			t.Fatalf("slot = %+v, want NOT_PRODUCED with no value", metric)
		}
	}
	if outcome.Metrics.Quality != judge.QualityNotAssessed {
		t.Fatalf("quality = %s, want NOT_ASSESSED", outcome.Metrics.Quality)
	}
	if result.GroupCompletion != judge.CompletionInterrupted || result.GroupCause != judge.CauseCancellation {
		t.Fatalf("group = %s/%s", result.GroupCompletion, result.GroupCause)
	}
}
