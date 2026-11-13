package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateJudgeMetrics(t *testing.T) {
	cases := []struct {
		name     string
		metrics  []MetricConfig
		wantErrs []string
		wantOK   bool
	}{
		{name: "absent registry is valid legacy configuration", metrics: nil, wantOK: true},
		{
			name:    "two well-formed slots",
			metrics: []MetricConfig{{ID: "correctness", Required: true}, {ID: "completeness", MinScore: new(0.7)}},
			wantOK:  true,
		},
		{
			name:    "a zero threshold is distinguishable from an absent one and stays valid",
			metrics: []MetricConfig{{ID: "correctness", MinScore: new(0.0)}},
			wantOK:  true,
		},
		{
			name:    "upper bound is inclusive",
			metrics: []MetricConfig{{ID: "correctness", MinScore: new(1.0)}},
			wantOK:  true,
		},
		{
			name:     "threshold above the score range is rejected",
			metrics:  []MetricConfig{{ID: "correctness", MinScore: new(1.1)}},
			wantErrs: []string{"min_score must be between 0.0 and 1.0"},
		},
		{
			name:     "negative threshold is rejected",
			metrics:  []MetricConfig{{ID: "correctness", MinScore: new(-0.5)}},
			wantErrs: []string{"min_score must be between 0.0 and 1.0"},
		},
		{
			name:     "duplicate slot ids are rejected",
			metrics:  []MetricConfig{{ID: "correctness"}, {ID: "correctness"}},
			wantErrs: []string{`metrics[1].id "correctness" is duplicated`},
		},
		{
			name:     "a NaN threshold is rejected because every comparison against it is false",
			metrics:  []MetricConfig{{ID: "correctness", MinScore: new(math.NaN())}},
			wantErrs: []string{"min_score must be between 0.0 and 1.0"},
		},
		{
			name:     "an infinite threshold is rejected",
			metrics:  []MetricConfig{{ID: "correctness", MinScore: new(math.Inf(1))}},
			wantErrs: []string{"min_score must be between 0.0 and 1.0"},
		},
		{
			name:     "an unsafe identifier is rejected",
			metrics:  []MetricConfig{{ID: "Correctness!"}},
			wantErrs: []string{"id must be a safe lowercase identifier"},
		},
		{
			name:     "an empty identifier is rejected",
			metrics:  []MetricConfig{{ID: ""}},
			wantErrs: []string{"id must be a safe lowercase identifier"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := validateJudgeMetrics("judge", tc.metrics)
			if tc.wantOK {
				if len(errs) != 0 {
					t.Fatalf("want valid, got %v", errs)
				}
				return
			}
			if len(errs) != len(tc.wantErrs) {
				t.Fatalf("errors = %v, want %v", errs, tc.wantErrs)
			}
			for i, want := range tc.wantErrs {
				if !strings.Contains(errs[i], want) {
					t.Fatalf("error %d = %q, want substring %q", i, errs[i], want)
				}
			}
		})
	}
}

func TestMetricRegistryKeepsAnAbsentThresholdDistinctFromZero(t *testing.T) {
	// A nil MinScore means "no threshold decision configured", which the quality
	// derivation reports as NOT_CONFIGURED rather than UNKNOWN; a zero threshold is
	// a real requirement. The two must not collapse during configuration loading.
	absent := MetricConfig{ID: "correctness"}
	zero := MetricConfig{ID: "completeness", MinScore: new(0.0)}
	if absent.MinScore != nil {
		t.Fatalf("unset threshold must stay nil, got %v", *absent.MinScore)
	}
	if zero.MinScore == nil || *zero.MinScore != 0 {
		t.Fatal("an explicit zero threshold was lost")
	}
}

// TestYAMLRegistryParsesNanThreshold pins the premise behind the non-finite
// rejection: the real YAML loader turns `min_score: .nan` into a NaN *float64, and
// every comparison against NaN is false, so an unvalidated registry would reach
// quality derivation and report MET for any available score.
func TestYAMLRegistryParsesNanThreshold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eval.yaml")
	content := `schema_version: v1alpha1
judge:
  type: script
  script_path: score.sh
  metrics:
    - id: correctness
      min_score: .nan
cases:
  files: []
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := NewLoader(path).LoadEvalConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	metric := cfg.Judge.Metrics[0]
	if metric.MinScore == nil || !math.IsNaN(*metric.MinScore) {
		t.Fatalf("min_score = %+v, want NaN parsed from `.nan`", metric.MinScore)
	}
	if errs := validateJudgeMetrics("judge.metrics", cfg.Judge.Metrics); len(errs) == 0 || !strings.Contains(errs[0], "min_score") {
		t.Fatalf("validation errors = %v, want the NaN threshold rejected", errs)
	}
}
