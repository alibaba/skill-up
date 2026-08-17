package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/evaluator"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
)

func TestNewRunner(t *testing.T) {
	evalCfg := &config.EvalConfig{
		Judge: config.JudgeConfig{Type: "rule_based"},
	}
	r := NewRunner(evalCfg, nil, nil, credential.AgentInitParams{})
	if r == nil {
		t.Fatal("expected non-nil runner")
	}
}

func TestRunner_InitWorkspace(t *testing.T) {
	r := NewRunner(&config.EvalConfig{}, nil, nil, credential.AgentInitParams{})

	tmpDir := t.TempDir()
	err := r.InitWorkspace(tmpDir, "test-skill", 1)
	if err != nil {
		t.Fatalf("InitWorkspace failed: %v", err)
	}
	if r.workspace == nil {
		t.Fatal("expected non-nil workspace")
	}

	// Verify iteration dir path is set correctly
	iterDir := r.workspace.IterationDir()
	if iterDir == "" {
		t.Error("expected non-empty iteration dir")
	}
}

func TestRunner_WriteResults_WithSkillOnly(t *testing.T) {
	r := NewRunner(&config.EvalConfig{}, nil, nil, credential.AgentInitParams{})

	tmpDir := t.TempDir()
	err := r.InitWorkspace(tmpDir, "test-skill", 1)
	if err != nil {
		t.Fatalf("InitWorkspace failed: %v", err)
	}

	results := []evaluator.EvalResult{
		{
			CaseID:   "case-1",
			CaseName: "Test Case 1",
			Prompt:   "Do something",
			Status:   judge.StatusPass,
			SessionResult: &agent.SessionResult{
				FinalMessage: "Done",
				DurationMs:   1500,
				Turns:        1,
			},
			TurnsTotal:    1,
			Configuration: "with_skill",
			Grading: &judge.Result{
				Status: judge.StatusPass,
				AssertionResults: []judge.AssertionResult{
					{Text: "output correct", Passed: true, Evidence: "matched"},
				},
				Summary: judge.ResultSummary{Passed: 1, Failed: 0, Total: 1, PassRate: 1.0},
			},
		},
	}

	err = r.WriteResults(context.Background(), results, "test-skill", "/path/to/skill", 1, []string{"html"})
	if err != nil {
		t.Fatalf("WriteResults failed: %v", err)
	}

	iterDir := r.workspace.IterationDir()

	// Check grading.json
	gradingPath := filepath.Join(iterDir, "case-1", "with_skill", "grading.json")
	if _, err := os.Stat(gradingPath); os.IsNotExist(err) {
		t.Errorf("grading.json not created: %s", gradingPath)
	}

	// Check eval_metadata.json
	metaPath := filepath.Join(iterDir, "case-1", "eval_metadata.json")
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		t.Errorf("eval_metadata.json not created: %s", metaPath)
	}

	// Check response.md
	responsePath := filepath.Join(iterDir, "case-1", "with_skill", "outputs", "response.md")
	if _, err := os.Stat(responsePath); os.IsNotExist(err) {
		t.Errorf("response.md not created: %s", responsePath)
	}
	responseData, _ := os.ReadFile(responsePath)
	if string(responseData) != "Done\n" {
		t.Errorf("unexpected response content: %q", string(responseData))
	}

	// Check benchmark.json
	bmPath := filepath.Join(iterDir, "benchmark.json")
	if _, err := os.Stat(bmPath); os.IsNotExist(err) {
		t.Errorf("benchmark.json not created: %s", bmPath)
	}
	bmData, _ := os.ReadFile(bmPath)
	var bm report.AnthropicBenchmark
	if err := json.Unmarshal(bmData, &bm); err != nil {
		t.Errorf("invalid benchmark.json: %v", err)
	}

	// Check benchmark.md
	bmMDPath := filepath.Join(iterDir, "benchmark.md")
	if _, err := os.Stat(bmMDPath); os.IsNotExist(err) {
		t.Errorf("benchmark.md not created: %s", bmMDPath)
	}

	// Check report.html (requested via format)
	reportPath := filepath.Join(iterDir, "report.html")
	if _, err := os.Stat(reportPath); os.IsNotExist(err) {
		t.Errorf("report.html not created: %s", reportPath)
	}
}

func TestRunner_WriteResults_WithBaseline(t *testing.T) {
	r := NewRunner(&config.EvalConfig{Benchmark: config.BenchmarkConfig{Enabled: true}}, nil, nil, credential.AgentInitParams{})

	tmpDir := t.TempDir()
	err := r.InitWorkspace(tmpDir, "test-skill", 1)
	if err != nil {
		t.Fatalf("InitWorkspace failed: %v", err)
	}

	results := []evaluator.EvalResult{
		{
			CaseID:   "case-1",
			CaseName: "Test Case",
			Prompt:   "Do something",
			Status:   judge.StatusPass,
			SessionResult: &agent.SessionResult{
				FinalMessage: "Done with skill",
				DurationMs:   1200,
				Turns:        1,
			},
			TurnsTotal:    1,
			Configuration: "with_skill",
			Grading: &judge.Result{
				Status:           judge.StatusPass,
				AssertionResults: []judge.AssertionResult{{Text: "c1", Passed: true, Evidence: "ok"}},
				Summary:          judge.ResultSummary{Passed: 1, Total: 1, PassRate: 1.0},
			},
		},
		{
			CaseID:   "case-1",
			CaseName: "Test Case",
			Prompt:   "Do something",
			Status:   judge.StatusFail,
			SessionResult: &agent.SessionResult{
				FinalMessage: "Done without skill",
				DurationMs:   2000,
				Turns:        1,
			},
			TurnsTotal:    1,
			Configuration: "without_skill",
			Grading: &judge.Result{
				Status:           judge.StatusFail,
				AssertionResults: []judge.AssertionResult{{Text: "c1", Passed: false, Evidence: "nope"}},
				Summary:          judge.ResultSummary{Failed: 1, Total: 1, PassRate: 0.0},
			},
		},
	}

	err = r.WriteResults(context.Background(), results, "test-skill", "/path/to/skill", 1, nil)
	if err != nil {
		t.Fatalf("WriteResults failed: %v", err)
	}

	iterDir := r.workspace.IterationDir()

	// Check both with_skill and without_skill directories exist
	withDir := filepath.Join(iterDir, "case-1", "with_skill")
	withoutDir := filepath.Join(iterDir, "case-1", "without_skill")

	if _, err := os.Stat(filepath.Join(withDir, "grading.json")); os.IsNotExist(err) {
		t.Error("with_skill grading.json not created")
	}
	if _, err := os.Stat(filepath.Join(withoutDir, "grading.json")); os.IsNotExist(err) {
		t.Error("without_skill grading.json not created")
	}
}

func TestRunner_WriteResults_UsesStderrWhenFinalMessageEmpty(t *testing.T) {
	r := NewRunner(&config.EvalConfig{}, nil, nil, credential.AgentInitParams{})

	tmpDir := t.TempDir()
	err := r.InitWorkspace(tmpDir, "test-skill", 1)
	if err != nil {
		t.Fatalf("InitWorkspace failed: %v", err)
	}

	results := []evaluator.EvalResult{
		{
			CaseID:   "case-error",
			CaseName: "Errored Case",
			Prompt:   "Do something risky",
			Status:   judge.StatusError,
			SessionResult: &agent.SessionResult{
				Stderr:     "agent failed: boom",
				DurationMs: 500,
				Turns:      1,
			},
			TurnsTotal:    1,
			Configuration: "with_skill",
			Error:         os.ErrInvalid,
		},
	}

	err = r.WriteResults(context.Background(), results, "test-skill", "/path/to/skill", 1, nil)
	if err != nil {
		t.Fatalf("WriteResults failed: %v", err)
	}

	iterDir := r.workspace.IterationDir()
	responsePath := filepath.Join(iterDir, "case-error", "with_skill", "outputs", "response.md")
	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response.md: %v", err)
	}
	if string(responseData) != "agent failed: boom\n" {
		t.Fatalf("unexpected response content: %q", string(responseData))
	}

	resultPath := filepath.Join(iterDir, "result.json")
	resultData, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}

	var reportResult report.Input
	if err := json.Unmarshal(resultData, &reportResult); err != nil {
		t.Fatalf("unmarshal result.json: %v", err)
	}
	if len(reportResult.CaseResults) != 1 {
		t.Fatalf("expected 1 report result, got %d", len(reportResult.CaseResults))
	}
	if reportResult.CaseResults[0].Response != "agent failed: boom" {
		t.Fatalf("unexpected report response: %q", reportResult.CaseResults[0].Response)
	}
}

func TestRunner_WriteResults_DoesNotUseStderrOnSuccessfulEmptyResponse(t *testing.T) {
	r := NewRunner(&config.EvalConfig{}, nil, nil, credential.AgentInitParams{})

	tmpDir := t.TempDir()
	err := r.InitWorkspace(tmpDir, "test-skill", 1)
	if err != nil {
		t.Fatalf("InitWorkspace failed: %v", err)
	}

	results := []evaluator.EvalResult{
		{
			CaseID:   "case-success",
			CaseName: "Successful Empty Response",
			Prompt:   "Do something quiet",
			Status:   judge.StatusPass,
			SessionResult: &agent.SessionResult{
				Stderr:     "warning: harmless progress log",
				DurationMs: 200,
				Turns:      1,
				ExitCode:   0,
			},
			TurnsTotal:    1,
			Configuration: "with_skill",
		},
	}

	err = r.WriteResults(context.Background(), results, "test-skill", "/path/to/skill", 1, nil)
	if err != nil {
		t.Fatalf("WriteResults failed: %v", err)
	}

	iterDir := r.workspace.IterationDir()
	responsePath := filepath.Join(iterDir, "case-success", "with_skill", "outputs", "response.md")
	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response.md: %v", err)
	}
	if string(responseData) != "\n" {
		t.Fatalf("unexpected response content: %q", string(responseData))
	}

	resultPath := filepath.Join(iterDir, "result.json")
	resultData, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}

	var reportResult report.Input
	if err := json.Unmarshal(resultData, &reportResult); err != nil {
		t.Fatalf("unmarshal result.json: %v", err)
	}
	if len(reportResult.CaseResults) != 1 {
		t.Fatalf("expected 1 report result, got %d", len(reportResult.CaseResults))
	}
	if reportResult.CaseResults[0].Response != "" {
		t.Fatalf("expected empty report response, got %q", reportResult.CaseResults[0].Response)
	}
}

func TestBuildReportInput_EngineAndModelPopulated(t *testing.T) {
	now := time.Now()
	end := now.Add(30 * time.Second)

	evalCfg := &config.EvalConfig{
		SchemaVersion: "v1alpha1",
		Engine: config.EngineConfig{
			Name: "claude_code",
			Model: config.ModelConfig{
				Provider: "anthropic",
				Name:     "claude-sonnet-4-6",
			},
		},
	}

	grouped := map[string]*caseResults{
		"case-1": {
			withSkill: &evaluator.EvalResult{
				CaseID:   "case-1",
				CaseName: "Test",
				Status:   judge.StatusPass,
				SessionResult: &agent.SessionResult{
					DurationMs:   5000,
					InputTokens:  100,
					OutputTokens: 200,
					Turns:        1,
				},
				Configuration: "with_skill",
			},
		},
	}

	input := buildReportInput("my-skill", grouped, []string{"case-1"}, now, end, evalCfg)

	if input.SchemaVersion != "v1alpha1" {
		t.Errorf("SchemaVersion = %q, want %q", input.SchemaVersion, "v1alpha1")
	}
	if input.EngineName != "claude_code" {
		t.Errorf("EngineName = %q, want %q", input.EngineName, "claude_code")
	}
	if input.ModelName != "anthropic/claude-sonnet-4-6" {
		t.Errorf("ModelName = %q, want %q", input.ModelName, "anthropic/claude-sonnet-4-6")
	}
	if !input.StartTime.Equal(now) {
		t.Errorf("StartTime = %v, want %v", input.StartTime, now)
	}
	if !input.EndTime.Equal(end) {
		t.Errorf("EndTime = %v, want %v", input.EndTime, end)
	}
	if input.TotalTokens != 300 {
		t.Errorf("TotalTokens = %d, want 300", input.TotalTokens)
	}
	if input.SkillName != "my-skill" {
		t.Errorf("SkillName = %q, want %q", input.SkillName, "my-skill")
	}
}

func TestBuildReportInput_ModelNameVariants(t *testing.T) {
	tests := []struct {
		name     string
		evalCfg  *config.EvalConfig
		wantName string
	}{
		{
			name: "provider and model",
			evalCfg: &config.EvalConfig{
				Engine: config.EngineConfig{
					Name:  "codex",
					Model: config.ModelConfig{Provider: "openai", Name: "gpt-4"},
				},
			},
			wantName: "openai/gpt-4",
		},
		{
			name: "model only",
			evalCfg: &config.EvalConfig{
				Engine: config.EngineConfig{
					Name:  "codex",
					Model: config.ModelConfig{Name: "gpt-4"},
				},
			},
			wantName: "gpt-4",
		},
		{
			name: "provider only",
			evalCfg: &config.EvalConfig{
				Engine: config.EngineConfig{
					Name:  "codex",
					Model: config.ModelConfig{Provider: "openai"},
				},
			},
			wantName: "",
		},
		{
			name:     "empty engine",
			evalCfg:  &config.EvalConfig{},
			wantName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grouped := map[string]*caseResults{}
			input := buildReportInput("s", grouped, nil, time.Time{}, time.Time{}, tt.evalCfg)
			if input.ModelName != tt.wantName {
				t.Errorf("ModelName = %q, want %q", input.ModelName, tt.wantName)
			}
		})
	}
}

func TestBuildReportInput_TokenAccumulation(t *testing.T) {
	evalCfg := &config.EvalConfig{Engine: config.EngineConfig{Name: "test"}}
	grouped := map[string]*caseResults{
		"c1": {
			withSkill: &evaluator.EvalResult{
				CaseID: "c1", Status: judge.StatusPass, Configuration: "with_skill",
				SessionResult: &agent.SessionResult{InputTokens: 100, OutputTokens: 50},
				Grading: &judge.Result{JudgeSession: &agent.SessionResult{
					DurationMs: 2000, InputTokens: 30, OutputTokens: 10,
				}},
			},
			withoutSkill: &evaluator.EvalResult{
				CaseID: "c1", Status: judge.StatusPass, Configuration: "without_skill",
				SessionResult: &agent.SessionResult{InputTokens: 80, OutputTokens: 40},
				Grading: &judge.Result{JudgeSession: &agent.SessionResult{
					DurationMs: 1500, InputTokens: 20, OutputTokens: 5,
				}},
			},
		},
		"c2": {
			withSkill: &evaluator.EvalResult{
				CaseID: "c2", Status: judge.StatusPass, Configuration: "with_skill",
				SessionResult: &agent.SessionResult{InputTokens: 200, OutputTokens: 100},
			},
		},
	}

	input := buildReportInput("s", grouped, []string{"c1", "c2"}, time.Time{}, time.Time{}, evalCfg)

	// c1: (100+50) + (80+40) = 270, c2: (200+100) = 300, total = 570
	if input.TotalTokens != 570 {
		t.Errorf("TotalTokens = %d, want 570", input.TotalTokens)
	}
	if input.JudgeTokens != 65 {
		t.Errorf("JudgeTokens = %d, want 65", input.JudgeTokens)
	}
	if input.OverallTokens != 635 {
		t.Errorf("OverallTokens = %d, want 635", input.OverallTokens)
	}
	if len(input.CaseResults) != 3 {
		t.Errorf("CaseResults count = %d, want 3", len(input.CaseResults))
	}
	if got := input.CaseResults[0]; got.JudgeDurationMs != 2000 || got.JudgeInputTokens != 30 || got.JudgeOutputTokens != 10 {
		t.Errorf("with-skill judge metrics = %#v", got)
	}
}

func TestBuildReportInput_IncludesFailedJudgeSessionMetrics(t *testing.T) {
	evalCfg := &config.EvalConfig{Engine: config.EngineConfig{Name: "test"}}
	grouped := map[string]*caseResults{
		"judge-error": {
			withSkill: &evaluator.EvalResult{
				CaseID:        "judge-error",
				Status:        judge.StatusError,
				Configuration: "with_skill",
				SessionResult: &agent.SessionResult{
					InputTokens: 100, OutputTokens: 25,
				},
				JudgeSession: &agent.SessionResult{
					DurationMs: 2300, InputTokens: 120, OutputTokens: 30,
				},
				Error: errors.New("judge evaluation failed"),
			},
		},
	}

	input := buildReportInput("s", grouped, []string{"judge-error"}, time.Time{}, time.Time{}, evalCfg)

	if len(input.CaseResults) != 1 {
		t.Fatalf("CaseResults count = %d, want 1", len(input.CaseResults))
	}
	got := input.CaseResults[0]
	if got.Status != judge.StatusError {
		t.Errorf("Status = %s, want ERROR", got.Status)
	}
	if got.JudgeDurationMs != 2300 || got.JudgeInputTokens != 120 || got.JudgeOutputTokens != 30 {
		t.Errorf("failed judge metrics = %#v", got)
	}
	if input.JudgeTokens != 150 {
		t.Errorf("JudgeTokens = %d, want 150", input.JudgeTokens)
	}
	if input.OverallTokens != 275 {
		t.Errorf("OverallTokens = %d, want 275", input.OverallTokens)
	}
}

func TestRunner_WriteResults_NoWorkspace(t *testing.T) {
	r := NewRunner(&config.EvalConfig{}, nil, nil, credential.AgentInitParams{})
	err := r.WriteResults(context.Background(), nil, "", "", 1, nil)
	if err == nil {
		t.Error("expected error when workspace not initialized")
	}
}
