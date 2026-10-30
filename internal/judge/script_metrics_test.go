package judge

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/platform"
)

// These tests drive the real ScriptJudge.Evaluate path: a script is written to
// disk, executed as a child process, and the invocation-bound metrics file it
// produces is collected, validated and materialized. Nothing here hand-builds an
// envelope.

type metricsScriptCase struct {
	name    string
	posix   string
	windows string
	// registry configures the planned slots for this judge.
	registry []config.MetricConfig
	// expectMetrics is keyed by metric id.
	expectAvailability map[string]Availability
	expectValues       map[string]float64
	expectQuality      Quality
	expectPlanned      int
	expectReasons      map[string]string
	// expectDiagnostic is one required entry of the envelope's diagnostics list.
	expectDiagnostic string
}

func (tc metricsScriptCase) scriptName() string {
	if runtime.GOOS == platform.GOOSWindows {
		return "metrics.ps1"
	}
	return "metrics.sh"
}

func (tc metricsScriptCase) body() string {
	if runtime.GOOS == platform.GOOSWindows {
		return tc.windows
	}
	return tc.posix
}

// writeMetricsScript puts the producer script in dir and returns its path.
func writeMetricsScript(t *testing.T, dir string, tc metricsScriptCase) string {
	t.Helper()
	path := filepath.Join(dir, tc.scriptName())
	if err := os.WriteFile(path, []byte(tc.body()), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func runMetricsScriptCase(t *testing.T, tc metricsScriptCase) *Result {
	t.Helper()
	dir := t.TempDir()
	script := writeMetricsScript(t, dir, tc)
	resultPath := filepath.Join(dir, "metrics-result.json")
	j := &ScriptJudge{
		ScriptPath:          script,
		MetricSlots:         SlotsFromConfig(tc.registry),
		MetricsResultPath:   resultPath,
		MetricsInvocationID: "inv-42",
	}
	res, err := j.Evaluate(context.Background(), Input{WorkspacePath: dir, FinalMessage: "output", ExitCode: 0})
	if err != nil {
		t.Fatalf("Evaluate returned %v", err)
	}
	return res
}

const (
	posixWriteTwoMetrics = `#!/bin/sh
cat > "$EVAL_METRICS_RESULT_PATH" <<JSON
{"schema_version":1,"invocation_id":"$EVAL_METRICS_INVOCATION_ID","metrics":[{"metric_id":"correctness","availability":"AVAILABLE","value":0,"evidence":["explicit zero"]},{"metric_id":"completeness","availability":"AVAILABLE","value":0.6,"evidence":["partial"]}]}
JSON
echo "all checks passed"
exit 0
`
	windowsWriteTwoMetrics = "$body = '{\"schema_version\":1,\"invocation_id\":\"' + $env:EVAL_METRICS_INVOCATION_ID + '\",\"metrics\":[{\"metric_id\":\"correctness\",\"availability\":\"AVAILABLE\",\"value\":0,\"evidence\":[\"explicit zero\"]},{\"metric_id\":\"completeness\",\"availability\":\"AVAILABLE\",\"value\":0.6,\"evidence\":[\"partial\"]}]}'\n" +
		"Set-Content -LiteralPath $env:EVAL_METRICS_RESULT_PATH -Value $body\n" +
		"Write-Output \"all checks passed\"\nexit 0\n"

	posixOneBadValue = `#!/bin/sh
cat > "$EVAL_METRICS_RESULT_PATH" <<JSON
{"schema_version":1,"invocation_id":"$EVAL_METRICS_INVOCATION_ID","metrics":[{"metric_id":"correctness","availability":"AVAILABLE","value":1.4,"evidence":["out of range"]},{"metric_id":"completeness","availability":"AVAILABLE","value":0.4,"evidence":["fine"]}]}
JSON
exit 0
`
	windowsOneBadValue = "$body = '{\"schema_version\":1,\"invocation_id\":\"' + $env:EVAL_METRICS_INVOCATION_ID + '\",\"metrics\":[{\"metric_id\":\"correctness\",\"availability\":\"AVAILABLE\",\"value\":1.4,\"evidence\":[\"out of range\"]},{\"metric_id\":\"completeness\",\"availability\":\"AVAILABLE\",\"value\":0.4,\"evidence\":[\"fine\"]}]}'\n" +
		"Set-Content -LiteralPath $env:EVAL_METRICS_RESULT_PATH -Value $body\nexit 0\n"

	posixDuplicateKey = `#!/bin/sh
cat > "$EVAL_METRICS_RESULT_PATH" <<JSON
{"schema_version":1,"schema_version":2,"invocation_id":"$EVAL_METRICS_INVOCATION_ID","metrics":[]}
JSON
exit 0
`
	windowsDuplicateKey = "$body = '{\"schema_version\":1,\"schema_version\":2,\"invocation_id\":\"' + $env:EVAL_METRICS_INVOCATION_ID + '\",\"metrics\":[]}'\n" +
		"Set-Content -LiteralPath $env:EVAL_METRICS_RESULT_PATH -Value $body\nexit 0\n"

	posixWrongInvocation = `#!/bin/sh
cat > "$EVAL_METRICS_RESULT_PATH" <<JSON
{"schema_version":1,"invocation_id":"not-this-invocation","metrics":[]}
JSON
exit 0
`
	windowsWrongInvocation = "$body = '{\"schema_version\":1,\"invocation_id\":\"not-this-invocation\",\"metrics\":[]}'\n" +
		"Set-Content -LiteralPath $env:EVAL_METRICS_RESULT_PATH -Value $body\nexit 0\n"

	posixWritesNothing = `#!/bin/sh
echo "no metrics produced"
exit 0
`
	windowsWritesNothing = "Write-Output \"no metrics produced\"\nexit 0\n"

	// A valid file written and then a nonzero exit: the exit code still wins and
	// the payload is never collected as a result (contract row P5).
	posixValidFileThenFail = `#!/bin/sh
cat > "$EVAL_METRICS_RESULT_PATH" <<JSON
{"schema_version":1,"invocation_id":"$EVAL_METRICS_INVOCATION_ID","metrics":[{"metric_id":"correctness","availability":"AVAILABLE","value":0.9,"evidence":["ok"]}]}
JSON
echo "script says it scored" 1>&2
exit 3
`
	windowsValidFileThenFail = "$body = '{\"schema_version\":1,\"invocation_id\":\"' + $env:EVAL_METRICS_INVOCATION_ID + '\",\"metrics\":[{\"metric_id\":\"correctness\",\"availability\":\"AVAILABLE\",\"value\":0.9,\"evidence\":[\"ok\"]}]}'\n" +
		"Set-Content -LiteralPath $env:EVAL_METRICS_RESULT_PATH -Value $body\nexit 3\n"

	posixDeclinesOne = `#!/bin/sh
cat > "$EVAL_METRICS_RESULT_PATH" <<JSON
{"schema_version":1,"invocation_id":"$EVAL_METRICS_INVOCATION_ID","metrics":[{"metric_id":"correctness","availability":"AVAILABLE","value":0.8,"evidence":["ok"]},{"metric_id":"completeness","availability":"SKIP","reason":"insufficient evidence"}]}
JSON
exit 0
`
	windowsDeclinesOne = "$body = '{\"schema_version\":1,\"invocation_id\":\"' + $env:EVAL_METRICS_INVOCATION_ID + '\",\"metrics\":[{\"metric_id\":\"correctness\",\"availability\":\"AVAILABLE\",\"value\":0.8,\"evidence\":[\"ok\"]},{\"metric_id\":\"completeness\",\"availability\":\"SKIP\",\"reason\":\"insufficient evidence\"}]}'\n" +
		"Set-Content -LiteralPath $env:EVAL_METRICS_RESULT_PATH -Value $body\nexit 0\n"
)

func twoMetricRegistry() []config.MetricConfig {
	return []config.MetricConfig{{ID: "correctness", Required: true}, {ID: "completeness"}}
}

func metricsScriptCases() []metricsScriptCase {
	return append(metricsScriptBasicCases(), metricsScriptAdditionalCases()...)
}

func metricsScriptBasicCases() []metricsScriptCase {
	return []metricsScriptCase{
		{
			name:     "two metrics including an explicit zero",
			posix:    posixWriteTwoMetrics,
			windows:  windowsWriteTwoMetrics,
			registry: twoMetricRegistry(),
			expectAvailability: map[string]Availability{
				"correctness": Available, "completeness": Available,
			},
			expectValues:  map[string]float64{"correctness": 0, "completeness": 0.6},
			expectQuality: QualityNotConfigured,
			expectPlanned: 2,
		},
		{
			name:     "one out-of-range value poisons only its own slot",
			posix:    posixOneBadValue,
			windows:  windowsOneBadValue,
			registry: twoMetricRegistry(),
			expectAvailability: map[string]Availability{
				"correctness": Errored, "completeness": Available,
			},
			expectValues:  map[string]float64{"completeness": 0.4},
			expectQuality: QualityNotConfigured,
			expectPlanned: 2,
			expectReasons: map[string]string{"correctness": "invalid_value"},
		},
		{
			name:     "duplicate json key invalidates the whole payload",
			posix:    posixDuplicateKey,
			windows:  windowsDuplicateKey,
			registry: twoMetricRegistry(),
			expectAvailability: map[string]Availability{
				"correctness": Errored, "completeness": Errored,
			},
			expectQuality: QualityNotAssessed,
			expectPlanned: 2,
			expectReasons: map[string]string{
				"correctness": "duplicate_json_key", "completeness": "duplicate_json_key",
			},
		},
		{
			name:     "a result bound to another invocation is rejected",
			posix:    posixWrongInvocation,
			windows:  windowsWrongInvocation,
			registry: twoMetricRegistry(),
			expectAvailability: map[string]Availability{
				"correctness": Errored, "completeness": Errored,
			},
			expectQuality: QualityNotAssessed,
			expectPlanned: 2,
			expectReasons: map[string]string{
				"correctness": "unbound_or_stale_result", "completeness": "unbound_or_stale_result",
			},
		},
	}
}

func metricsScriptAdditionalCases() []metricsScriptCase {
	minScore := 0.5
	return []metricsScriptCase{
		{
			// Contract §10: the runner collects the exact assigned file after a
			// successful exit and rejects a missing one, so a zero-exit script that
			// produced no structured result is a contract failure, not MISSING.
			name:     "no file after a successful run is rejected as an unbound result",
			posix:    posixWritesNothing,
			windows:  windowsWritesNothing,
			registry: twoMetricRegistry(),
			expectAvailability: map[string]Availability{
				"correctness": Errored, "completeness": Errored,
			},
			expectQuality: QualityNotAssessed,
			expectPlanned: 2,
			expectReasons: map[string]string{
				"correctness": "unbound_or_stale_result", "completeness": "unbound_or_stale_result",
			},
		},
		{
			// A nonzero exit is an unsuccessful process: nothing it wrote can become
			// a score, and the planned slots record that instead of looking omitted.
			name:     "valid file then nonzero exit collects no score",
			posix:    posixValidFileThenFail,
			windows:  windowsValidFileThenFail,
			registry: twoMetricRegistry(),
			expectAvailability: map[string]Availability{
				"correctness": NotProduced, "completeness": NotProduced,
			},
			expectReasons: map[string]string{
				"correctness": "evaluation_prevented", "completeness": "evaluation_prevented",
			},
			expectQuality:    QualityNotAssessed,
			expectPlanned:    2,
			expectDiagnostic: "result_file_not_accepted",
		},
		{
			name:     "judge-declared SKIP keeps its own reason",
			posix:    posixDeclinesOne,
			windows:  windowsDeclinesOne,
			registry: twoMetricRegistry(),
			expectAvailability: map[string]Availability{
				"correctness": Available, "completeness": Declined,
			},
			expectValues:  map[string]float64{"correctness": 0.8},
			expectQuality: QualityNotConfigured,
			expectPlanned: 2,
		},
		{
			name:     "a configured threshold turns the same scores into a decision",
			posix:    posixDeclinesOne,
			windows:  windowsDeclinesOne,
			registry: []config.MetricConfig{{ID: "correctness", Required: true, MinScore: &minScore}, {ID: "completeness"}},
			// completeness carries min_score? no: only correctness is thresholded and
			// it is AVAILABLE with 0.8 >= 0.5, so the decision is evaluable.
			expectAvailability: map[string]Availability{
				"correctness": Available, "completeness": Declined,
			},
			expectValues:  map[string]float64{"correctness": 0.8},
			expectQuality: QualityMet,
			expectPlanned: 2,
		},
		{
			name:     "a threshold on the declined metric makes the decision unevaluable",
			posix:    posixDeclinesOne,
			windows:  windowsDeclinesOne,
			registry: []config.MetricConfig{{ID: "correctness", Required: true}, {ID: "completeness", MinScore: &minScore}},
			expectAvailability: map[string]Availability{
				"correctness": Available, "completeness": Declined,
			},
			expectQuality: QualityUnknown,
			expectPlanned: 2,
		},
	}
}

func TestScriptJudgeCollectsNamedMetrics(t *testing.T) {
	cases := metricsScriptCases()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { checkMetricsScriptCase(t, tc) })
	}
}

func checkMetricsScriptCase(t *testing.T, tc metricsScriptCase) {
	t.Helper()
	res := runMetricsScriptCase(t, tc)
	if res.Metrics == nil {
		t.Fatal("a configured registry must report one slot per planned metric")
	}
	env := res.Metrics
	if env.Coverage.Planned != tc.expectPlanned {
		t.Fatalf("planned = %d, want %d", env.Coverage.Planned, tc.expectPlanned)
	}
	if len(env.Metrics) != tc.expectPlanned {
		t.Fatalf("materialized %d slots, want %d", len(env.Metrics), tc.expectPlanned)
	}
	if env.Quality != tc.expectQuality {
		t.Fatalf("quality = %s, want %s", env.Quality, tc.expectQuality)
	}
	checkMetricValues(t, env, tc)
	if tc.expectDiagnostic != "" && !slices.Contains(env.Diagnostics, tc.expectDiagnostic) {
		t.Fatalf("diagnostics = %v, want %q", env.Diagnostics, tc.expectDiagnostic)
	}
	// The verdict mapping itself must not change because metrics exist.
	if res.Status != StatusPass && !tc.nameIsFailing() {
		t.Fatalf("status = %s, expected the legacy PASS for a zero-exit script", res.Status)
	}
}

func checkMetricValues(t *testing.T, env *MetricsEnvelope, tc metricsScriptCase) {
	t.Helper()
	for _, metric := range env.Metrics {
		want, ok := tc.expectAvailability[metric.MetricID]
		if !ok {
			t.Fatalf("unexpected slot %q", metric.MetricID)
		}
		if metric.Availability != want {
			t.Fatalf("%s availability = %s, want %s (reason %q)", metric.MetricID, metric.Availability, want, metric.Reason)
		}
		if want != Available && metric.Value != nil {
			t.Fatalf("%s carries value %v while unavailable: no fabricated zero allowed", metric.MetricID, *metric.Value)
		}
		if reason, ok := tc.expectReasons[metric.MetricID]; ok && !strings.Contains(metric.Reason, reason) {
			t.Fatalf("%s reason = %q, want substring %q", metric.MetricID, metric.Reason, reason)
		}
	}
	for id, wantValue := range tc.expectValues {
		for _, metric := range env.Metrics {
			if metric.MetricID == id {
				if metric.Value == nil || *metric.Value != wantValue {
					t.Fatalf("%s value = %v, want %v", id, metric.Value, wantValue)
				}
			}
		}
	}
}

func (tc metricsScriptCase) nameIsFailing() bool {
	return strings.Contains(tc.name, "nonzero exit")
}

func TestScriptJudgeWithoutRegistryKeepsLegacyShape(t *testing.T) {
	dir := t.TempDir()
	name, body := "check.sh", "#!/bin/sh\necho ok\nexit 0\n"
	if runtime.GOOS == platform.GOOSWindows {
		name, body = "check.ps1", "Write-Output ok\nexit 0\n"
	}
	script := writeMetricsScript(t, dir, metricsScriptCase{posix: body, windows: body})
	_ = name
	j := &ScriptJudge{ScriptPath: script}
	res, err := j.Evaluate(context.Background(), Input{WorkspacePath: dir})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Metrics != nil {
		t.Fatalf("a judge with no registry gained an envelope: %+v", res.Metrics)
	}
	if res.Status != StatusPass {
		t.Fatalf("status = %s, want legacy PASS", res.Status)
	}
}

func TestParseMetricsPayloadRejections(t *testing.T) {
	cases := []struct {
		name          string
		raw           string
		invocation    string
		wantProblem   PayloadProblem
		wantEmissions int
	}{
		{"valid empty metrics", `{"schema_version":1,"invocation_id":"i","metrics":[]}`, "i", ProblemNone, 0},
		{"top level not an object", `[]`, "i", ProblemTopLevelShape, 0},
		{"garbage", `not json`, "i", ProblemUnparseable, 0},
		{"duplicate nested key", `{"schema_version":1,"invocation_id":"i","metrics":[{"metric_id":"a","metric_id":"a"}]}`, "i", ProblemDuplicateJSONKey, 0},
		{"unknown top level field", `{"schema_version":1,"invocation_id":"i","metrics":[],"extra":1}`, "i", ProblemUnknownTopField, 0},
		{"wrong schema version", `{"schema_version":2,"invocation_id":"i","metrics":[]}`, "i", ProblemSchemaVersion, 0},
		{"mismatched invocation", `{"schema_version":1,"invocation_id":"other","metrics":[]}`, "i", ProblemUnboundResult, 0},
		{"missing metrics array", `{"schema_version":1,"invocation_id":"i"}`, "i", ProblemTopLevelShape, 0},
		{"trailing second value", `{"schema_version":1,"invocation_id":"i","metrics":[]} {}`, "i", ProblemTopLevelShape, 0},
		{"unknown entry field", `{"schema_version":1,"invocation_id":"i","metrics":[{"metric_id":"a","bogus":1}]}`, "i", ProblemUnknownTopField, 0},
		{"non numeric value stays a slot problem", `{"schema_version":1,"invocation_id":"i","metrics":[{"metric_id":"a","availability":"AVAILABLE","value":"0.5"}]}`, "i", ProblemNone, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			emitted, problem := ParseMetricsPayload([]byte(tc.raw), tc.invocation)
			if problem != tc.wantProblem {
				t.Fatalf("problem = %q, want %q", problem, tc.wantProblem)
			}
			if len(emitted) != tc.wantEmissions {
				t.Fatalf("emissions = %d, want %d", len(emitted), tc.wantEmissions)
			}
		})
	}
}

// Timeout does not invalidate a trustworthy result (#6437995472 §2).
//
// These drive the real DeadlineExceeded branch of ScriptJudge.Evaluate with an
// already-expired parent context, which is deterministic on every platform. The
// result file stands in for the child having written it before the kill: it is
// bound to this exact invocation id, so the collector must accept it while the
// legacy verdict mapping stays unchanged. A full process-kill variant needs POSIX
// process-group semantics and is the same case upstream keeps POSIX-only.
func TestScriptJudgeTimeoutCollectsValidInvocationBoundResult(t *testing.T) {
	for _, tc := range []struct {
		name         string
		timeoutError bool
		wantStatus   Status
		wantErr      bool
	}{
		{"grouped mapping keeps ERROR", true, StatusError, true},
		{"single-judge mapping keeps FAIL", false, StatusFail, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := writeMetricsScript(t, dir, metricsScriptCase{
				posix:   "#!/bin/sh\nsleep 5\nexit 0\n",
				windows: "Start-Sleep -Seconds 5\nexit 0\n",
			})
			resultPath := filepath.Join(dir, "metrics-result.json")
			payload := `{"schema_version":1,"invocation_id":"inv-42","metrics":[{"metric_id":"correctness","availability":"AVAILABLE","value":0.8,"evidence":["written before the deadline"]},{"metric_id":"completeness","availability":"AVAILABLE","value":0,"evidence":["explicit zero"]}]}`
			if err := os.WriteFile(resultPath, []byte(payload), 0o600); err != nil {
				t.Fatal(err)
			}
			expired, cancelExpired := context.WithTimeout(context.Background(), 0)
			t.Cleanup(cancelExpired)
			j := &ScriptJudge{
				ScriptPath:          script,
				TimeoutIsError:      tc.timeoutError,
				MetricSlots:         SlotsFromConfig(twoMetricRegistry()),
				MetricsResultPath:   resultPath,
				MetricsInvocationID: "inv-42",
			}
			res, err := j.Evaluate(expired, Input{WorkspacePath: dir})
			if tc.wantErr && err == nil {
				t.Fatal("expected the timeout error to still be returned")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if res == nil {
				t.Fatal("expected a result envelope even on timeout")
			}
			if res.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s (verdict mapping must not change)", res.Status, tc.wantStatus)
			}
			if res.Metrics == nil {
				t.Fatal("a timeout discarded a collectible valid result")
			}
			if res.Metrics.Coverage.Available != 2 || res.Metrics.Quality != QualityNotConfigured {
				t.Fatalf("envelope = %+v, want both scores retained", res.Metrics)
			}
			for _, metric := range res.Metrics.Metrics {
				if metric.Availability != Available || metric.Value == nil {
					t.Fatalf("slot = %+v, an accepted score must stay AVAILABLE", metric)
				}
			}
		})
	}
}

func TestScriptJudgeTimeoutWithoutCollectedFileLeavesSlotsNotProduced(t *testing.T) {
	dir := t.TempDir()
	script := writeMetricsScript(t, dir, metricsScriptCase{
		posix:   "#!/bin/sh\nsleep 5\nexit 0\n",
		windows: "Start-Sleep -Seconds 5\nexit 0\n",
	})
	expired, cancelExpired := context.WithTimeout(context.Background(), 0)
	t.Cleanup(cancelExpired)
	j := &ScriptJudge{
		ScriptPath:          script,
		MetricSlots:         SlotsFromConfig(twoMetricRegistry()),
		MetricsResultPath:   filepath.Join(dir, "never-written.json"),
		MetricsInvocationID: "inv-42",
	}
	res, err := j.Evaluate(expired, Input{WorkspacePath: dir})
	if res == nil {
		t.Fatalf("expected a result, got nil (err=%v)", err)
	}
	if res.Metrics == nil {
		t.Fatal("expected an envelope describing the unproduced slots")
	}
	for _, metric := range res.Metrics.Metrics {
		if metric.Availability != NotProduced || metric.Value != nil {
			t.Fatalf("slot = %+v, want NOT_PRODUCED with no value after an interrupted run", metric)
		}
	}
	if res.Metrics.Quality != QualityNotAssessed {
		t.Fatalf("quality = %s, want NOT_ASSESSED", res.Metrics.Quality)
	}
}

func testOversizedMetricsPayload(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := writeMetricsScript(t, dir, metricsScriptCase{
		posix: "#!/bin/sh\nexit 0\n", windows: "exit 0\n",
	})
	resultPath := filepath.Join(dir, "metrics-result.json")
	// A padding string plus valid JSON shape: over the collector limit.
	padding := strings.Repeat("x", MaxMetricsPayloadBytes+16)
	payload := `{"schema_version":1,"invocation_id":"inv-42","metrics":[{"metric_id":"correctness","availability":"AVAILABLE","value":0.5,"evidence":["` + padding + `"]}]}`
	if err := os.WriteFile(resultPath, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	j := &ScriptJudge{
		ScriptPath: script, MetricSlots: SlotsFromConfig(twoMetricRegistry()),
		MetricsResultPath: resultPath, MetricsInvocationID: "inv-42",
	}
	res, err := j.Evaluate(context.Background(), Input{WorkspacePath: dir})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	for _, metric := range res.Metrics.Metrics {
		if metric.Availability != Errored || metric.Reason != string(ProblemSizeLimit) {
			t.Fatalf("slot = %+v, want ERROR/%s", metric, ProblemSizeLimit)
		}
	}
}

func TestScriptJudgeRejectsOversizedAndNonRegularResults(t *testing.T) {
	t.Run("oversized payload is rejected before it is read", testOversizedMetricsPayload)

	t.Run("a non-regular path is never collected", func(t *testing.T) {
		dir := t.TempDir()
		script := writeMetricsScript(t, dir, metricsScriptCase{
			posix: "#!/bin/sh\nexit 0\n", windows: "exit 0\n",
		})
		resultPath := filepath.Join(dir, "metrics-result.json")
		if err := os.Mkdir(resultPath, 0o755); err != nil {
			t.Fatal(err)
		}
		j := &ScriptJudge{
			ScriptPath: script, MetricSlots: SlotsFromConfig(twoMetricRegistry()),
			MetricsResultPath: resultPath, MetricsInvocationID: "inv-42",
		}
		res, err := j.Evaluate(context.Background(), Input{WorkspacePath: dir})
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		for _, metric := range res.Metrics.Metrics {
			if metric.Availability != Errored || metric.Reason != string(ProblemUnboundResult) {
				t.Fatalf("slot = %+v, want ERROR/%s", metric, ProblemUnboundResult)
			}
		}
	})

	t.Run("an unavailable channel is reported as such, not as a stale file", func(t *testing.T) {
		dir := t.TempDir()
		script := writeMetricsScript(t, dir, metricsScriptCase{
			posix: "#!/bin/sh\necho ok\nexit 0\n", windows: "Write-Output ok\nexit 0\n",
		})
		j := &ScriptJudge{
			ScriptPath: script, MetricSlots: SlotsFromConfig(twoMetricRegistry()),
			// MetricsResultPath deliberately empty: a non-local runtime never gets one.
			MetricsInvocationID: "inv-42",
		}
		res, err := j.Evaluate(context.Background(), Input{WorkspacePath: dir})
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if res.Status != StatusPass {
			t.Fatalf("legacy verdict changed: %s", res.Status)
		}
		for _, metric := range res.Metrics.Metrics {
			if metric.Availability != Errored || metric.Reason != string(ProblemChannelUnsupported) {
				t.Fatalf("slot = %+v, want ERROR/%s", metric, ProblemChannelUnsupported)
			}
		}
	})
}
