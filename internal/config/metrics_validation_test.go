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

func TestCaseMetricRegistryRequiresExplicitJudgeType(t *testing.T) {
	eval := &EvalConfig{Judge: JudgeConfig{Type: "script", ScriptPath: "global.sh"}}
	v := &Validator{}
	for _, tc := range []struct {
		name    string
		local   JudgeConfig
		wantErr bool
	}{
		{name: "typed registry", local: JudgeConfig{
			Type: "script", ScriptPath: "local.sh", Metrics: []MetricConfig{{ID: "correctness", MinScore: new(0.7)}},
		}},
		{name: "inherited judge without a local registry"},
		{name: "untyped registry with a threshold", local: JudgeConfig{
			Metrics: []MetricConfig{{ID: "correctness", MinScore: new(0.7)}},
		}, wantErr: true},
		{name: "untyped explicit empty registry", local: JudgeConfig{Metrics: []MetricConfig{}}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &CaseConfig{ID: "local", Input: Input{Prompt: "hello"}, Judge: tc.local, JudgeSet: true}
			_, planErr := ResolveJudgePlan(eval, c)
			for name, err := range map[string]error{
				"case validation":  v.ValidateCaseConfig(c),
				"suite validation": v.ValidateCasesWithEvalDefaults(eval, []*CaseConfig{c}),
				"runtime plan":     planErr,
			} {
				if tc.wantErr && (err == nil || !strings.Contains(err.Error(), "judge.type is required when judge.metrics is configured")) {
					t.Errorf("%s error = %v, want explicit judge type requirement", name, err)
				}
				if !tc.wantErr && err != nil {
					t.Errorf("%s rejected valid configuration: %v", name, err)
				}
			}
		})
	}
}

func TestEvalMetricRegistryRequiresExplicitJudgeType(t *testing.T) {
	const base = `schema_version: v1alpha1
environment:
  type: none
engine:
  name: claude_code
cases:
  files: [cases/example.yaml]
`
	const metrics = `  metrics:
    - id: correctness
      min_score: 0.7
`
	for _, tc := range []struct {
		name    string
		judge   string
		wantErr bool
	}{
		{name: "no judge"},
		{name: "typed registry", judge: "judge:\n  type: script\n  script_path: judge.sh\n" + metrics},
		{name: "untyped registry with a threshold", judge: "judge:\n" + metrics, wantErr: true},
		{name: "untyped explicit empty registry", judge: "judge:\n  metrics: []\n", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "eval.yaml")
			if err := os.WriteFile(path, []byte(base+tc.judge), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := NewLoader(path).LoadEvalConfig()
			if err != nil {
				t.Fatalf("load eval config: %v", err)
			}
			_, planErr := ResolveJudgePlan(cfg, &CaseConfig{})
			for name, err := range map[string]error{
				"eval validation": NewValidator().ValidateEvalConfig(cfg),
				"runtime plan":    planErr,
			} {
				if tc.wantErr && (err == nil || !strings.Contains(err.Error(), "judge.type is required when judge.metrics is configured")) {
					t.Errorf("%s error = %v, want explicit judge type requirement", name, err)
				}
				if !tc.wantErr && err != nil {
					t.Errorf("%s rejected valid configuration: %v", name, err)
				}
			}
		})
	}
}
