package judge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScriptJudgeRealSubprocessTimeoutCollectsResult runs a real child process
// that writes its invocation-bound result and then outlives the judge timeout, so
// the collector sees a file the child actually produced before being killed.
//
// Scratch paths deliberately live outside t.TempDir and the child's working
// directory is the shared temp root: a lingering Windows grandchild otherwise
// holds the directory the harness wants to delete, which is exactly why
// TestScriptJudge_Timeout stays POSIX-only upstream. Removal is best-effort.

const realTimeoutPayload = `{"schema_version":1,"invocation_id":"inv-42","metrics":[{"metric_id":"correctness","availability":"AVAILABLE","value":0.25,"evidence":["written before the kill"]},{"metric_id":"completeness","availability":"AVAILABLE","value":0,"evidence":["explicit zero"]}]}`

const realTimeoutPosix = "#!/bin/sh\n" +
	"cat > \"$EVAL_METRICS_RESULT_PATH\" <<JSON\n" +
	realTimeoutPayload + "\n" +
	"JSON\n" +
	"sleep 5\nexit 0\n"

var realTimeoutWindows = "$body = '" + strings.ReplaceAll(realTimeoutPayload, "'", "''") + "'\n" +
	"Set-Content -LiteralPath $env:EVAL_METRICS_RESULT_PATH -Value $body\n" +
	"Start-Sleep -Seconds 5\nexit 0\n"

func TestScriptJudgeRealSubprocessTimeoutCollectsResult(t *testing.T) {
	cases := []struct {
		name         string
		timeoutError bool
		wantStatus   Status
		wantErr      bool
	}{
		{"grouped mapping reports ERROR", true, StatusError, true},
		{"single-judge mapping reports FAIL", false, StatusFail, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scratch := t.TempDir()
			script := writeMetricsScript(t, scratch, metricsScriptCase{
				posix:   realTimeoutPosix,
				windows: realTimeoutWindows,
			})
			j := &ScriptJudge{
				ScriptPath:          script,
				TimeoutSeconds:      1,
				TimeoutIsError:      tc.timeoutError,
				MetricSlots:         SlotsFromConfig(twoMetricRegistry()),
				MetricsResultPath:   filepath.Join(scratch, "metrics-result.json"),
				MetricsInvocationID: "inv-42",
			}
			res, err := j.Evaluate(context.Background(), Input{WorkspacePath: os.TempDir()})
			if tc.wantErr && err == nil {
				t.Fatal("expected a timeout error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if res == nil || res.Metrics == nil {
				t.Fatalf("no envelope collected from a real child that wrote before the kill: %+v", res)
			}
			if res.Metrics.Coverage.Available != 2 {
				t.Fatalf("envelope = %+v, want both real scores retained", res.Metrics)
			}
			for _, metric := range res.Metrics.Metrics {
				if metric.Availability != Available || metric.Value == nil {
					t.Fatalf("slot = %+v, an accepted score must stay AVAILABLE", metric)
				}
			}
			if res.Metrics.Quality != QualityNotConfigured {
				t.Fatalf("quality = %s, want NOT_CONFIGURED", res.Metrics.Quality)
			}
			if res.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s", res.Status, tc.wantStatus)
			}
		})
	}
}
