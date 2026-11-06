package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/evaluator"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
	evalruntime "github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// metricsPersistenceAgent is the smallest agent.Agent that lets a case reach the
// judge phase, so the script judge really runs and its envelope is persisted.
type metricsPersistenceAgent struct{}

func (metricsPersistenceAgent) Name() string                                 { return "metrics-test" }
func (metricsPersistenceAgent) Check(context.Context, agent.Runtime) error   { return nil }
func (metricsPersistenceAgent) CheckCredentials(context.Context) error       { return nil }
func (metricsPersistenceAgent) Install(context.Context, agent.Runtime) error { return nil }

func (metricsPersistenceAgent) InstallMCP(context.Context, agent.Runtime, evalruntime.MCPConfig) error {
	return nil
}

func (metricsPersistenceAgent) InstallSkill(context.Context, agent.Runtime, evalruntime.SkillConfig) error {
	return nil
}

func (metricsPersistenceAgent) Run(context.Context, agent.Runtime, agent.ExecOptions, []transcript.Message) (*agent.SessionResult, error) {
	return &agent.SessionResult{FinalMessage: "done"}, nil
}

// metricsPersistenceScript writes the platform's judge script, which emits one
// AVAILABLE metric carrying an explicit zero score.
func metricsPersistenceScript(t *testing.T) string {
	t.Helper()
	const payload = `{"schema_version":1,"invocation_id":"__INVOCATION__","metrics":[{"metric_id":"correctness","availability":"AVAILABLE","value":0}]}`
	name, body := "judge.sh", "#!/bin/sh\ncat > \"$EVAL_METRICS_RESULT_PATH\" <<JSON\n"+
		strings.ReplaceAll(payload, "__INVOCATION__", "$EVAL_METRICS_INVOCATION_ID")+"\nJSON\nexit 0\n"
	if runtime.GOOS == "windows" {
		name = "judge.ps1"
		body = "$body = '" + payload + "' -replace '__INVOCATION__', $env:EVAL_METRICS_INVOCATION_ID\n" +
			"Set-Content -LiteralPath $env:EVAL_METRICS_RESULT_PATH -Value $body\nexit 0\n"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write judge script: %v", err)
	}
	return path
}

// runSingleScriptMetricsCase evaluates one case with a real script judge.
func runSingleScriptMetricsCase(t *testing.T) []evaluator.EvalResult {
	t.Helper()
	limit := 30
	member := config.JudgeConfig{
		ID:             "configured-script-id",
		Type:           "script",
		ScriptPath:     metricsPersistenceScript(t),
		TimeoutSeconds: &limit,
		Metrics:        []config.MetricConfig{{ID: "correctness", Required: true}},
	}
	e := evaluator.NewEvaluator(evaluator.EvalOptions{
		Agent:     metricsPersistenceAgent{},
		OutputDir: t.TempDir(),
		EvalCfg:   &config.EvalConfig{Environment: config.Environment{Type: "none"}, Judge: member},
	})
	results, err := e.EvaluateAll(context.Background(), []*config.CaseConfig{{ID: "single", Input: config.Input{Prompt: "hello"}}})
	if err != nil || len(results) != 1 {
		t.Fatalf("evaluate: results=%d err=%v", len(results), err)
	}
	if results[0].Status != judge.StatusPass || results[0].Metrics == nil {
		t.Fatalf("result: %+v", results[0])
	}
	return results
}

// persistedSingleScriptEvaluation writes case artifacts and reads evaluation.json
// back from disk, so the assertion covers what a consumer actually loads.
func persistedSingleScriptEvaluation(t *testing.T, result *evaluator.EvalResult) report.GroupedEvaluation {
	t.Helper()
	ws, err := report.NewIterationWorkspace(t.TempDir(), "skill", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.EnsureDirs([]string{result.CaseID}); err != nil {
		t.Fatal(err)
	}
	if err := (&Runner{}).writeCaseArtifacts(ws, result, "with_skill"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(ws.ConfigDir(result.CaseID, "with_skill"), "evaluation.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved report.GroupedEvaluation
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestSingleScriptMetricsPersistThroughEvaluatorAndRunner(t *testing.T) {
	results := runSingleScriptMetricsCase(t)
	saved := persistedSingleScriptEvaluation(t, &results[0])
	if saved.Execution == nil || saved.Execution.Completion != judge.CompletionFinished {
		t.Fatalf("persisted execution = %+v, want FINISHED", saved.Execution)
	}
	if saved.Metrics == nil || saved.Metrics.JudgeID != "configured-script-id" || saved.Metrics.Coverage.Available != 1 {
		t.Fatalf("persisted metrics = %+v", saved.Metrics)
	}
	if len(saved.Metrics.Metrics) != 1 || saved.Metrics.Metrics[0].Value == nil || *saved.Metrics.Metrics[0].Value != 0 {
		t.Fatalf("persisted zero score = %+v", saved.Metrics)
	}
	if len(saved.JudgeResults) != 0 {
		t.Fatalf("a single judge plan must not fabricate member outcomes: %+v", saved.JudgeResults)
	}
}

func TestWriteCaseArtifactsPersistsSingleJudgeMetricsAndExecution(t *testing.T) {
	ws, err := report.NewIterationWorkspace(t.TempDir(), "skill", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.EnsureDirs([]string{"single"}); err != nil {
		t.Fatal(err)
	}
	score := 0.0
	result := &evaluator.EvalResult{
		CaseID: "single", CaseName: "single judge metrics", Status: judge.StatusPass,
		Metrics: judge.MaterializeSlots("configured-script-id", []judge.MetricSlot{{ID: "correctness", Required: true}}, []judge.EmittedMetric{{
			MetricID: "correctness", Availability: "AVAILABLE", Value: &score,
		}}, judge.ProblemNone, judge.MetricProduced),
		Execution: &judge.ExecutionMetadata{Stage: judge.StageFinished, Completion: judge.CompletionFinished, Cause: judge.CauseNone},
	}
	if err := (&Runner{}).writeCaseArtifacts(ws, result, "with_skill"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(ws.ConfigDir("single", "with_skill"), "evaluation.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved report.GroupedEvaluation
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.JudgeResults) != 0 || saved.Execution == nil || saved.Execution.Completion != judge.CompletionFinished {
		t.Fatalf("single execution metadata lost: %+v", saved)
	}
	if saved.Metrics == nil || saved.Metrics.JudgeID != "configured-script-id" || saved.Metrics.Coverage.Available != 1 {
		t.Fatalf("single metrics lost: %+v", saved.Metrics)
	}
	if saved.Metrics.Metrics[0].Value == nil || *saved.Metrics.Metrics[0].Value != 0 {
		t.Fatalf("explicit zero lost: %+v", saved.Metrics.Metrics[0])
	}
}
