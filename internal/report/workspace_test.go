package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewIterationWorkspace_FirstIteration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rootDir := filepath.Join(dir, "test-workspace")

	ws, err := NewIterationWorkspace(rootDir, "test-skill", 1)
	if err != nil {
		t.Fatalf("NewIterationWorkspace error: %v", err)
	}

	if ws.IterationNum != 1 {
		t.Errorf("expected iteration 1, got %d", ws.IterationNum)
	}
	if ws.SkillName != "test-skill" {
		t.Errorf("expected skill name 'test-skill', got %s", ws.SkillName)
	}
}

func TestIterationWorkspace_Paths(t *testing.T) {
	t.Parallel()
	ws, err := NewIterationWorkspace("/tmp/test-workspace", "test-skill", 1)
	if err != nil {
		t.Fatalf("NewIterationWorkspace error: %v", err)
	}

	if ws.IterationDir() != "/tmp/test-workspace/iteration-1" {
		t.Errorf("unexpected iteration dir: %s", ws.IterationDir())
	}
	if ws.CaseDir("case-1") != "/tmp/test-workspace/iteration-1/case-1" {
		t.Errorf("unexpected case dir: %s", ws.CaseDir("case-1"))
	}
	if ws.WithSkillDir("case-1") != "/tmp/test-workspace/iteration-1/case-1/with_skill" {
		t.Errorf("unexpected with_skill dir: %s", ws.WithSkillDir("case-1"))
	}
	if ws.WithoutSkillDir("case-1") != "/tmp/test-workspace/iteration-1/case-1/without_skill" {
		t.Errorf("unexpected without_skill dir: %s", ws.WithoutSkillDir("case-1"))
	}
}

func TestIterationWorkspace_EnsureDirs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ws, err := NewIterationWorkspace(dir, "test-skill", 1)
	if err != nil {
		t.Fatalf("NewIterationWorkspace error: %v", err)
	}

	if err := ws.EnsureDirsWithBaseline([]string{"case-a", "case-b"}); err != nil {
		t.Fatalf("EnsureDirsWithBaseline error: %v", err)
	}

	// Verify directories were created.
	for _, caseID := range []string{"case-a", "case-b"} {
		for _, config := range []string{"with_skill", "without_skill"} {
			outputsDir := filepath.Join(ws.ConfigDir(caseID, config), "outputs")
			if _, err := os.Stat(outputsDir); os.IsNotExist(err) {
				t.Errorf("expected dir to exist: %s", outputsDir)
			}
		}
	}
}

func TestIterationWorkspace_EnsureDirs_NoBaseline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ws, err := NewIterationWorkspace(dir, "test-skill", 1)
	if err != nil {
		t.Fatalf("NewIterationWorkspace error: %v", err)
	}

	if err := ws.EnsureDirs([]string{"case-a"}); err != nil {
		t.Fatalf("EnsureDirs error: %v", err)
	}

	// with_skill should exist.
	wsDir := filepath.Join(ws.WithSkillDir("case-a"), "outputs")
	if _, err := os.Stat(wsDir); os.IsNotExist(err) {
		t.Error("expected with_skill/outputs to exist")
	}

	// without_skill should NOT exist.
	wosDir := filepath.Join(ws.WithoutSkillDir("case-a"), "outputs")
	if _, err := os.Stat(wosDir); !os.IsNotExist(err) {
		t.Error("expected without_skill/outputs to NOT exist")
	}
}

func TestIterationWorkspace_WriteResponse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ws, err := NewIterationWorkspace(dir, "test-skill", 1)
	if err != nil {
		t.Fatalf("NewIterationWorkspace error: %v", err)
	}

	if err := ws.WriteResponse("case-1", "with_skill", "Hello world"); err != nil {
		t.Fatalf("WriteResponse error: %v", err)
	}

	path := filepath.Join(ws.WithSkillDir("case-1"), "outputs", "response.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file error: %v", err)
	}
	if string(data) != "Hello world\n" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

func TestIterationWorkspace_WriteGrading(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ws, err := NewIterationWorkspace(dir, "test-skill", 1)
	if err != nil {
		t.Fatalf("NewIterationWorkspace error: %v", err)
	}

	// Create the directory first.
	if err := ws.EnsureDirs([]string{"case-1"}); err != nil {
		t.Fatalf("EnsureDirs error: %v", err)
	}

	grading := &AnthropicGrading{
		Expectations: []AnthropicExpectation{
			{Text: "test", Passed: true, Evidence: "ok"},
		},
		Summary: AnthropicSummary{Passed: 1, Total: 1, PassRate: 1.0},
	}

	if err := ws.WriteGrading("case-1", "with_skill", grading); err != nil {
		t.Fatalf("WriteGrading error: %v", err)
	}

	path := filepath.Join(ws.WithSkillDir("case-1"), "grading.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file error: %v", err)
	}

	var loaded AnthropicGrading
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if loaded.Summary.PassRate != 1.0 {
		t.Errorf("expected pass_rate 1.0, got %f", loaded.Summary.PassRate)
	}
}

func TestIterationWorkspace_WriteEvalMeta(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ws, err := NewIterationWorkspace(dir, "test-skill", 1)
	if err != nil {
		t.Fatalf("NewIterationWorkspace error: %v", err)
	}

	if err := ws.EnsureDirs([]string{"case-1"}); err != nil {
		t.Fatalf("EnsureDirs error: %v", err)
	}

	meta := &EvalMetadata{
		EvalID:     1,
		EvalName:   "case-1",
		Prompt:     "test prompt",
		Assertions: []string{"assert1"},
	}

	if err := ws.WriteEvalMeta("case-1", meta); err != nil {
		t.Fatalf("WriteEvalMeta error: %v", err)
	}

	path := filepath.Join(ws.CaseDir("case-1"), "eval_metadata.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected eval_metadata.json to exist")
	}
}

func TestIterationWorkspace_WriteBenchmark(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ws, err := NewIterationWorkspace(dir, "test-skill", 1)
	if err != nil {
		t.Fatalf("NewIterationWorkspace error: %v", err)
	}

	// Create iteration dir.
	if err := os.MkdirAll(ws.IterationDir(), 0o755); err != nil {
		t.Fatalf("mkdir error: %v", err)
	}

	bm := &AnthropicBenchmark{
		Metadata: BenchmarkMetadata{SkillName: "test"},
		Runs:     []BenchmarkRun{},
		RunSummary: AnthropicRunSummary{
			WithSkill: AnthropicStatSummary{
				PassRate: AnthropicStatValue{Mean: 1.0},
			},
		},
	}

	if err := ws.WriteBenchmark(bm); err != nil {
		t.Fatalf("WriteBenchmark error: %v", err)
	}

	path := filepath.Join(ws.IterationDir(), "benchmark.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected benchmark.json to exist")
	}
}
