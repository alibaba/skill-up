package judge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptJudgeCollectsMetricsFromWindowsLongPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for range 5 {
		dir = filepath.Join(dir, strings.Repeat("d", 60))
	}
	path := filepath.Join(dir, "metrics.json")
	// Use extended paths for setup so this also works with long paths disabled.
	// The collector receives the original absolute path, as it does at runtime.
	if err := os.MkdirAll(extendedMetricsPath(dir), 0o700); err != nil {
		t.Fatalf("create long directory: %v", err)
	}
	const payload = `{"schema_version":1,"invocation_id":"long-path-test","metrics":[{"metric_id":"quality","availability":"AVAILABLE","value":0.75}]}`
	if err := os.WriteFile(extendedMetricsPath(path), []byte(payload), 0o600); err != nil {
		t.Fatalf("write metrics file: %v", err)
	}
	judge := &ScriptJudge{
		MetricsResultPath:   path,
		MetricsInvocationID: "long-path-test",
		MetricSlots:         []MetricSlot{{ID: "quality"}},
	}
	result := &Result{}
	if !judge.attachMetrics(result, MetricProduced) || result.Metrics == nil || len(result.Metrics.Metrics) != 1 {
		t.Fatalf("long-path result not collected: %+v", result.Metrics)
	}
	metric := result.Metrics.Metrics[0]
	if metric.Availability != Available || metric.Value == nil || *metric.Value != 0.75 {
		t.Fatalf("collected metric = %+v, want AVAILABLE 0.75", metric)
	}
}
