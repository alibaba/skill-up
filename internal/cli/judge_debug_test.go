package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// writeJudgeDebugInput serialises a judgeDebugInput to a JSON file and returns the path.
func writeJudgeDebugInput(t *testing.T, dir string, input judgeDebugInput) string {
	t.Helper()
	data, err := json.Marshal(input) //nolint:musttag // debug input embeds config structs with existing tags.
	if err != nil {
		t.Fatalf("marshal judge debug input: %v", err)
	}
	path := filepath.Join(dir, "input.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write input.json: %v", err)
	}
	return path
}

type debugCriterionOutcome bool

func debugCriterionResult(index int, outcome debugCriterionOutcome, evidence string) judge.CriterionResult {
	passed := bool(outcome)
	failures := []string{}
	if !passed {
		failures = []string{evidence}
	}
	return judge.CriterionResult{
		CriterionID: fmt.Sprintf("criterion-%d", index+1),
		Passed:      &passed,
		Evidence:    []string{evidence},
		Failures:    failures,
	}
}

// newJudgeDebugCmd creates an isolated cobra command wired to runJudgeDebug for testing.
func newJudgeDebugCmd(outputPath string) *cobra.Command {
	cmd := &cobra.Command{RunE: runJudgeDebug}
	cmd.Flags().String("output", outputPath, "")
	cmd.Flags().String("report", "", "")
	return cmd
}

// ---------------------------------------------------------------------------
// buildJudgeFromConfig
// ---------------------------------------------------------------------------

func TestBuildJudgeFromConfig_RuleBased(t *testing.T) {
	t.Parallel()
	cfg := config.JudgeConfig{Type: "rule_based"}
	j, err := buildJudgeFromConfig(cfg, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if j == nil {
		t.Fatal("expected non-nil judge")
	}
}

func TestBuildJudgeFromConfig_EmptyType(t *testing.T) {
	t.Parallel()
	// Empty type defaults to rule_based.
	cfg := config.JudgeConfig{}
	j, err := buildJudgeFromConfig(cfg, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if j == nil {
		t.Fatal("expected non-nil judge")
	}
}

func TestBuildJudgeFromConfig_Script_NoPath(t *testing.T) {
	t.Parallel()
	cfg := config.JudgeConfig{Type: "script"} // missing ScriptPath
	_, err := buildJudgeFromConfig(cfg, nil, "")
	if err == nil {
		t.Fatal("expected error when script_path is empty, got nil")
	}
	if !strings.Contains(err.Error(), "script_path") {
		t.Errorf("error should mention script_path, got: %v", err)
	}
}

func TestBuildJudgeFromConfig_Script_WithPath(t *testing.T) {
	t.Parallel()
	cfg := config.JudgeConfig{Type: "script", ScriptPath: "/tmp/fake-script.sh"}
	j, err := buildJudgeFromConfig(cfg, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if j == nil {
		t.Fatal("expected non-nil judge")
	}
}

func TestBuildJudgeFromConfig_AgentJudge_NoMockResults(t *testing.T) {
	t.Parallel()
	cfg := config.JudgeConfig{Type: "agent_judge", Criteria: []string{"output is correct"}}
	_, err := buildJudgeFromConfig(cfg, nil, "")
	if err == nil {
		t.Fatal("expected error when mock_results is empty, got nil")
	}
	if !strings.Contains(err.Error(), "mock_results") {
		t.Errorf("error should mention mock_results, got: %v", err)
	}
}

func TestBuildJudgeFromConfig_AgentJudge_WithMockResults(t *testing.T) {
	t.Parallel()
	cfg := config.JudgeConfig{Type: "agent_judge", Criteria: []string{"output is correct"}}
	mockResults := []judge.CriterionResult{
		debugCriterionResult(0, true, "looks good"),
	}
	j, err := buildJudgeFromConfig(cfg, mockResults, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if j == nil {
		t.Fatal("expected non-nil judge")
	}
}

func TestBuildJudgeFromConfig_Unsupported(t *testing.T) {
	t.Parallel()
	cfg := config.JudgeConfig{Type: "llm_judge"}
	_, err := buildJudgeFromConfig(cfg, nil, "")
	if err == nil {
		t.Fatal("expected error for unsupported judge type, got nil")
	}
	if !strings.Contains(err.Error(), "llm_judge") {
		t.Errorf("error should mention the unsupported type, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// judgeDebugInput.toJudgeInput
// ---------------------------------------------------------------------------

func TestJudgeDebugInput_ToJudgeInput(t *testing.T) {
	t.Parallel()

	tr := transcript.Transcript{
		{Role: "user", Content: "hello", Turn: 1},
		{Role: "assistant", Content: "world", Turn: 1},
	}
	input := judgeDebugInput{
		CaseID:         "case-42",
		FinalMessage:   "done",
		ExitCode:       0,
		SkillDir:       "/tmp/skill",
		WorkspacePath:  "/tmp/ws",
		WorkspaceDiff:  "diff output",
		GeneratedFiles: []string{"out.txt"},
		TurnsExecuted:  2,
		TurnsTotal:     3,
		Transcript:     tr,
	}

	ji := input.toJudgeInput()

	if ji.CaseID != "case-42" {
		t.Errorf("CaseID: want case-42, got %s", ji.CaseID)
	}
	if ji.FinalMessage != "done" {
		t.Errorf("FinalMessage: want done, got %s", ji.FinalMessage)
	}
	if ji.ExitCode != 0 {
		t.Errorf("ExitCode: want 0, got %d", ji.ExitCode)
	}
	if ji.WorkspacePath != "/tmp/ws" {
		t.Errorf("WorkspacePath: want /tmp/ws, got %s", ji.WorkspacePath)
	}
	if ji.SkillDir != "/tmp/skill" {
		t.Errorf("SkillDir: want /tmp/skill, got %s", ji.SkillDir)
	}
	if ji.WorkspaceDiff != "diff output" {
		t.Errorf("WorkspaceDiff mismatch")
	}
	if len(ji.GeneratedFiles) != 1 || ji.GeneratedFiles[0] != "out.txt" {
		t.Errorf("GeneratedFiles mismatch")
	}
	if ji.TurnsExecuted != 2 {
		t.Errorf("TurnsExecuted: want 2, got %d", ji.TurnsExecuted)
	}
	if ji.TurnsTotal != 3 {
		t.Errorf("TurnsTotal: want 3, got %d", ji.TurnsTotal)
	}
	if len(ji.Transcript) != 2 {
		t.Errorf("Transcript length: want 2, got %d", len(ji.Transcript))
	}
}

// ---------------------------------------------------------------------------
// mockJudgeAgent
// ---------------------------------------------------------------------------

func TestMockJudgeAgent_Name(t *testing.T) {
	t.Parallel()
	ag := &mockJudgeAgent{results: nil}
	if ag.Name() != "mock-judge" {
		t.Errorf("Name: want mock-judge, got %s", ag.Name())
	}
}

func TestMockJudgeAgent_NoOps(t *testing.T) {
	t.Parallel()
	ag := &mockJudgeAgent{}
	ctx := context.Background()
	if err := ag.Install(ctx, nil); err != nil {
		t.Errorf("Install: unexpected error: %v", err)
	}
	if err := ag.InstallMCP(ctx, nil, runtime.MCPConfig{}); err != nil {
		t.Errorf("InstallMCP: unexpected error: %v", err)
	}
	if err := ag.InstallSkill(ctx, nil, runtime.SkillConfig{}); err != nil {
		t.Errorf("InstallSkill: unexpected error: %v", err)
	}
	if err := ag.Check(ctx, nil); err != nil {
		t.Errorf("Check: unexpected error: %v", err)
	}
}

func TestMockJudgeAgent_Run(t *testing.T) {
	t.Parallel()
	mockResults := []judge.CriterionResult{
		debugCriterionResult(0, true, "it works"),
		debugCriterionResult(1, false, "nope"),
	}
	ag := &mockJudgeAgent{results: mockResults}

	sr, err := ag.Run(context.Background(), nil, agent.ExecOptions{}, []transcript.Message{})
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if sr == nil {
		t.Fatal("Run: nil SessionResult")
		return
	}

	var parsed struct {
		Results []judge.CriterionResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(sr.FinalMessage), &parsed); err != nil {
		t.Fatalf("Run output is not valid JSON: %v\noutput: %s", err, sr.FinalMessage)
	}
	if len(parsed.Results) != 2 {
		t.Errorf("expected 2 results, got %d", len(parsed.Results))
	}
	if parsed.Results[0].CriterionID != "criterion-1" {
		t.Errorf("unexpected criterion ID: %s", parsed.Results[0].CriterionID)
	}
	if parsed.Results[0].Passed == nil || !*parsed.Results[0].Passed {
		t.Error("expected first result to be passed")
	}
}

// ---------------------------------------------------------------------------
// runJudgeDebug
// ---------------------------------------------------------------------------

func TestRunJudgeDebug_RuleBased_Pass(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "grading.json")

	input := judgeDebugInput{
		CaseID:       "rule-pass",
		FinalMessage: "the output contains hello",
		ExitCode:     0,
		Judge: config.JudgeConfig{
			Type: "rule_based",
			Success: []config.Rule{
				{OutputContains: &config.OutputContainsRule{All: []string{"hello"}}},
			},
		},
	}
	inputPath := writeJudgeDebugInput(t, dir, input)

	cmd := newJudgeDebugCmd(outputPath)
	cmd.SetErr(&bytes.Buffer{})

	if err := runJudgeDebug(cmd, []string{inputPath}); err != nil {
		t.Fatalf("runJudgeDebug error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("grading.json not created: %v", err)
	}

	var result judge.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("grading.json is not valid JSON: %v", err)
	}
	if result.Status != judge.StatusPass {
		t.Errorf("expected PASS, got %s", result.Status)
	}
}

func TestRunJudgeDebug_RuleBased_Fail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "grading.json")

	input := judgeDebugInput{
		CaseID:       "rule-fail",
		FinalMessage: "no match here",
		ExitCode:     0,
		Judge: config.JudgeConfig{
			Type: "rule_based",
			Success: []config.Rule{
				{OutputContains: &config.OutputContainsRule{All: []string{"missing-keyword"}}},
			},
		},
	}
	inputPath := writeJudgeDebugInput(t, dir, input)

	cmd := newJudgeDebugCmd(outputPath)
	cmd.SetErr(&bytes.Buffer{})

	if err := runJudgeDebug(cmd, []string{inputPath}); err != nil {
		t.Fatalf("runJudgeDebug error: %v", err)
	}

	data, _ := os.ReadFile(outputPath)
	var result judge.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("grading.json parse error: %v", err)
	}
	if result.Status != judge.StatusFail {
		t.Errorf("expected FAIL, got %s", result.Status)
	}
}

func TestRunJudgeDebug_AgentJudge_WithMock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "grading.json")

	input := judgeDebugInput{
		CaseID:       "agent-mock",
		FinalMessage: "some output",
		Judge: config.JudgeConfig{
			Type:     "agent_judge",
			Criteria: []string{"output is correct"},
		},
		MockResults: []judge.CriterionResult{
			debugCriterionResult(0, true, "all good"),
		},
	}
	inputPath := writeJudgeDebugInput(t, dir, input)

	cmd := newJudgeDebugCmd(outputPath)
	cmd.SetErr(&bytes.Buffer{})

	if err := runJudgeDebug(cmd, []string{inputPath}); err != nil {
		t.Fatalf("runJudgeDebug error: %v", err)
	}

	data, _ := os.ReadFile(outputPath)
	var result judge.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("grading.json parse error: %v", err)
	}
	if result.Status != judge.StatusPass {
		t.Errorf("expected PASS, got %s", result.Status)
	}
}

func TestRunJudgeDebug_AgentJudge_ShippedFixture(t *testing.T) {
	t.Parallel()
	inputPath := filepath.Join("..", "..", "examples", "judge-debug-agent.json")
	outputPath := filepath.Join(t.TempDir(), "grading.json")

	cmd := newJudgeDebugCmd(outputPath)
	cmd.SetErr(&bytes.Buffer{})

	if err := runJudgeDebug(cmd, []string{inputPath}); err != nil {
		t.Fatalf("run shipped agent_judge fixture: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read grading.json: %v", err)
	}
	var result judge.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("parse grading.json: %v", err)
	}
	if result.Status != judge.StatusFail {
		t.Errorf("expected FAIL at the fixture's 0.7 threshold, got %s", result.Status)
	}
	if result.Summary.Total != 3 || result.Summary.Passed != 2 || result.Summary.Failed != 1 {
		t.Errorf("unexpected summary: %+v", result.Summary)
	}
	if len(result.AssertionResults) != 3 {
		t.Fatalf("expected 3 assertion results, got %d", len(result.AssertionResults))
	}
	if got := result.AssertionResults[0].Text; got != "Whether the Agent correctly identified the bug in the code" {
		t.Errorf("unexpected first criterion text: %q", got)
	}
}

func TestRunJudgeDebug_MissingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cmd := newJudgeDebugCmd(filepath.Join(dir, "grading.json"))
	cmd.SetErr(&bytes.Buffer{})

	err := runJudgeDebug(cmd, []string{"/nonexistent/input.json"})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestRunJudgeDebug_InvalidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	badPath := filepath.Join(dir, "input.json")
	if err := os.WriteFile(badPath, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newJudgeDebugCmd(filepath.Join(dir, "grading.json"))
	cmd.SetErr(&bytes.Buffer{})

	err := runJudgeDebug(cmd, []string{badPath})
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestRunJudgeDebug_WithOptionalReport(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	outputPath := filepath.Join(dir, "grading.json")

	input := judgeDebugInput{
		CaseID:       "with-report",
		FinalMessage: "hello world",
		Judge:        config.JudgeConfig{Type: "rule_based"},
	}
	inputPath := writeJudgeDebugInput(t, dir, input)

	cmd := newJudgeDebugCmd(outputPath)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Flags().Set("report", "json"); err != nil {
		t.Fatalf("set flag: %v", err)
	}

	if err := runJudgeDebug(cmd, []string{inputPath}); err != nil {
		t.Fatalf("runJudgeDebug error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "report.json")); err != nil {
		t.Errorf("report.json not created: %v", err)
	}
}

func TestRunJudgeDebug_StderrSummary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "grading.json")

	input := judgeDebugInput{
		CaseID:       "stderr-test",
		FinalMessage: "result",
		Judge:        config.JudgeConfig{Type: "rule_based"},
	}
	inputPath := writeJudgeDebugInput(t, dir, input)

	cmd := newJudgeDebugCmd(outputPath)
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)

	if err := runJudgeDebug(cmd, []string{inputPath}); err != nil {
		t.Fatalf("runJudgeDebug error: %v", err)
	}

	if len(errBuf.String()) == 0 {
		t.Error("expected non-empty stderr summary")
	}
}
