package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestNewLoader_normalizesRelativeEvalPath(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "my-skill")
	evalsDir := filepath.Join(project, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	evalFile := filepath.Join(evalsDir, "eval.yaml")
	content := `schema_version: v1alpha1
cases:
  files: []
`
	if err := os.WriteFile(evalFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Chdir(project)

	rel := filepath.Join("evals", "eval.yaml")
	loader := NewLoader(rel)
	if !filepath.IsAbs(loader.EvalPath()) {
		t.Fatalf("EvalPath should be absolute, got %q", loader.EvalPath())
	}
	if !filepath.IsAbs(loader.EvalDir()) {
		t.Fatalf("EvalDir should be absolute, got %q", loader.EvalDir())
	}
	// macOS may resolve /var vs /private/var; assert structure instead of string equality.
	if filepath.Base(loader.EvalDir()) != "evals" {
		t.Fatalf("EvalDir basename: got %q want evals (full %q)", filepath.Base(loader.EvalDir()), loader.EvalDir())
	}
	skillRoot := filepath.Dir(loader.EvalDir())
	if filepath.Base(skillRoot) != "my-skill" {
		t.Fatalf("skill root basename: got %q want my-skill (from %q)", filepath.Base(skillRoot), skillRoot)
	}
}

func TestStripExt(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"case.yaml":        "case",
		"case.with.dots":   "case.with",
		"case-without-ext": "case-without-ext",
	}
	for input, want := range tests {
		if got := stripExt(input); got != want {
			t.Fatalf("stripExt(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLoader_LoadEvalConfig(t *testing.T) {
	t.Parallel()
	content := `schema_version: v1alpha1

environment:
  type: none
  workspace_mount: /workspace
  kwargs:
    base_url: https://sandbox.example.test
    extensions: '{"profile":"ci"}'

mcp:
  servers: []

skills:
  - source: local_path
    path: .
    target: ~/.claude/skills/test

engine:
  name: claude_code
  model:
    provider: anthropic
    name: claude-sonnet-4-6

cases:
  files:
    - evals/cases/test1.yaml
    - evals/cases/test2.yaml
  defaults:
    timeout_seconds: 300
    max_turns: 12

judge:
  type: script
  script_path: evals/fixtures/scripts/check.sh

report:
  formats: [json]
`

	tmpDir := t.TempDir()
	evalPath := filepath.Join(tmpDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write temp eval.yaml: %v", err)
	}

	loader := NewLoader(evalPath)
	cfg, err := loader.LoadEvalConfig()
	if err != nil {
		t.Fatalf("LoadEvalConfig failed: %v", err)
	}

	if cfg.SchemaVersion != "v1alpha1" {
		t.Errorf("expected schema_version 'v1alpha1', got '%s'", cfg.SchemaVersion)
	}
	if cfg.Environment.Type != "none" {
		t.Errorf("expected environment.type 'none', got '%s'", cfg.Environment.Type)
	}
	if cfg.Environment.WorkspaceMount != "/workspace" {
		t.Errorf("expected workspace_mount '/workspace', got '%s'", cfg.Environment.WorkspaceMount)
	}
	if cfg.Environment.Kwargs["base_url"] != "https://sandbox.example.test" {
		t.Errorf("expected environment.kwargs.base_url to be loaded, got %q", cfg.Environment.Kwargs["base_url"])
	}
	if cfg.Environment.Kwargs["extensions"] != `{"profile":"ci"}` {
		t.Errorf("expected environment.kwargs.extensions to be loaded, got %q", cfg.Environment.Kwargs["extensions"])
	}

	if len(cfg.Engine.Model.Provider) == 0 {
		t.Error("expected engine.model.provider to be set")
	}

	if len(cfg.Cases.Files) != 2 {
		t.Errorf("expected 2 case files, got %d", len(cfg.Cases.Files))
	}

	if cfg.Judge.Type != "script" {
		t.Errorf("expected judge.type 'script', got '%s'", cfg.Judge.Type)
	}
}

// nolint:funlen // table-driven test cases drive the line count; splitting hurts readability.
func TestLoader_LoadEvalConfig_PassThreshold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		judgeBlock    string
		wantNil       bool
		wantThreshold float64
	}{
		{
			name: "unset threshold stays nil",
			judgeBlock: `judge:
  type: agent_judge
  model: test-model
  criteria:
    - criterion one
`,
			wantNil: true,
		},
		{
			name: "zero threshold is preserved",
			judgeBlock: `judge:
  type: agent_judge
  model: test-model
  criteria:
    - criterion one
  pass_threshold: 0
`,
			wantThreshold: 0.0,
		},
		{
			name: "non-zero threshold is preserved",
			judgeBlock: `judge:
  type: agent_judge
  model: test-model
  criteria:
    - criterion one
  pass_threshold: 0.85
`,
			wantThreshold: 0.85,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			content := `schema_version: v1alpha1

environment:
  type: none

engine:
  name: claude_code
  model:
    provider: anthropic
    name: claude-sonnet-4-6

cases:
  files:
    - evals/cases/test1.yaml

` + tt.judgeBlock

			tmpDir := t.TempDir()
			evalPath := filepath.Join(tmpDir, "eval.yaml")
			if err := os.WriteFile(evalPath, []byte(content), 0o600); err != nil {
				t.Fatalf("failed to write temp eval.yaml: %v", err)
			}

			loader := NewLoader(evalPath)
			cfg, err := loader.LoadEvalConfig()
			if err != nil {
				t.Fatalf("LoadEvalConfig failed: %v", err)
			}

			if tt.wantNil {
				if cfg.Judge.PassThreshold != nil {
					t.Fatalf("expected nil pass_threshold, got %v", *cfg.Judge.PassThreshold)
				}
				return
			}

			if cfg.Judge.PassThreshold == nil {
				t.Fatal("expected non-nil pass_threshold")
			}
			if *cfg.Judge.PassThreshold != tt.wantThreshold {
				t.Fatalf("expected pass_threshold %v, got %v", tt.wantThreshold, *cfg.Judge.PassThreshold)
			}
		})
	}
}

// nolint:funlen // table-driven matrix over the documented defaults; keeping
// each case inline beats spreading them across helpers.
func TestLoader_LoadEvalConfig_AppliesDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		evalYAML       string
		wantTimeout    int
		wantMaxTurns   int
		wantParallel   int
		wantWorkspace  string
		wantReportFmts []string
	}{
		{
			name: "omitted cases.defaults inherits 300s timeout and 10 turns",
			evalYAML: `schema_version: v1alpha1
cases:
  files:
    - evals/cases/test1.yaml
`,
			wantTimeout:    300,
			wantMaxTurns:   10,
			wantParallel:   1,
			wantWorkspace:  "",
			wantReportFmts: []string{"json"},
		},
		{
			name: "partial cases.defaults keeps unspecified timeout at 300",
			evalYAML: `schema_version: v1alpha1
cases:
  files:
    - evals/cases/test1.yaml
  defaults:
    max_turns: 5
`,
			wantTimeout:    300,
			wantMaxTurns:   5,
			wantParallel:   1,
			wantWorkspace:  "",
			wantReportFmts: []string{"json"},
		},
		{
			name: "explicit timeout overrides default",
			evalYAML: `schema_version: v1alpha1
cases:
  files:
    - evals/cases/test1.yaml
  defaults:
    timeout_seconds: 600
`,
			wantTimeout:    600,
			wantMaxTurns:   10,
			wantParallel:   1,
			wantWorkspace:  "",
			wantReportFmts: []string{"json"},
		},
		{
			name: "explicit zero timeout falls back to documented default",
			evalYAML: `schema_version: v1alpha1
cases:
  files:
    - evals/cases/test1.yaml
  defaults:
    timeout_seconds: 0
`,
			wantTimeout:    300,
			wantMaxTurns:   10,
			wantParallel:   1,
			wantWorkspace:  "",
			wantReportFmts: []string{"json"},
		},
		{
			name: "explicit negative timeout opts out of the deadline",
			evalYAML: `schema_version: v1alpha1
cases:
  files:
    - evals/cases/test1.yaml
  defaults:
    timeout_seconds: -1
`,
			wantTimeout:    -1,
			wantMaxTurns:   10,
			wantParallel:   1,
			wantWorkspace:  "",
			wantReportFmts: []string{"json"},
		},
		{
			name: "user-supplied report.formats replaces default",
			evalYAML: `schema_version: v1alpha1
cases:
  files:
    - evals/cases/test1.yaml
report:
  formats: [json, junit, html]
`,
			wantTimeout:    300,
			wantMaxTurns:   10,
			wantParallel:   1,
			wantWorkspace:  "",
			wantReportFmts: []string{"json", "junit", "html"},
		},
		{
			name: "user-supplied workspace_mount is preserved (not auto-defaulted)",
			evalYAML: `schema_version: v1alpha1
environment:
  type: none
  workspace_mount: /custom
cases:
  files:
    - evals/cases/test1.yaml
`,
			wantTimeout:    300,
			wantMaxTurns:   10,
			wantParallel:   1,
			wantWorkspace:  "/custom",
			wantReportFmts: []string{"json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			evalPath := filepath.Join(tmpDir, "eval.yaml")
			if err := os.WriteFile(evalPath, []byte(tt.evalYAML), 0o600); err != nil {
				t.Fatalf("failed to write temp eval.yaml: %v", err)
			}

			cfg, err := NewLoader(evalPath).LoadEvalConfig()
			if err != nil {
				t.Fatalf("LoadEvalConfig failed: %v", err)
			}

			if cfg.Cases.Defaults.TimeoutSeconds != tt.wantTimeout {
				t.Errorf("Cases.Defaults.TimeoutSeconds = %d, want %d", cfg.Cases.Defaults.TimeoutSeconds, tt.wantTimeout)
			}
			if cfg.Cases.Defaults.MaxTurns != tt.wantMaxTurns {
				t.Errorf("Cases.Defaults.MaxTurns = %d, want %d", cfg.Cases.Defaults.MaxTurns, tt.wantMaxTurns)
			}
			if cfg.Cases.Parallelism != tt.wantParallel {
				t.Errorf("Cases.Parallelism = %d, want %d", cfg.Cases.Parallelism, tt.wantParallel)
			}
			if cfg.Environment.WorkspaceMount != tt.wantWorkspace {
				t.Errorf("Environment.WorkspaceMount = %q, want %q", cfg.Environment.WorkspaceMount, tt.wantWorkspace)
			}
			if !equalStringSlice(cfg.Report.Formats, tt.wantReportFmts) {
				t.Errorf("Report.Formats = %v, want %v", cfg.Report.Formats, tt.wantReportFmts)
			}
		})
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLoader_LoadCaseConfig(t *testing.T) {
	t.Parallel()
	content := `id: my-test-case
title: My Test Case
description: A test case for validation

input:
  prompt: |
    Say hello to the user.

context:
  files:
    /workspace/test.txt: "test content"

constraints:
  timeout_seconds: 120

expect:
  must_contain:
    - "hello"
  must_not_contain:
    - "error"
`

	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "my-skill")
	evalsDir := filepath.Join(skillDir, "evals")
	casesDir := filepath.Join(evalsDir, "cases")
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		t.Fatalf("failed to create cases dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# skill\n"), 0o600); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	evalPath := filepath.Join(evalsDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte("schema_version: v1alpha1\n"), 0o600); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	casePath := filepath.Join(casesDir, "my-case.yaml")
	if err := os.WriteFile(casePath, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write case file: %v", err)
	}

	loader := NewLoader(evalPath)
	cfg, err := loader.LoadCaseConfig("evals/cases/my-case.yaml")
	if err != nil {
		t.Fatalf("LoadCaseConfig failed: %v", err)
	}

	if cfg.ID != "my-test-case" {
		t.Errorf("expected id 'my-test-case', got '%s'", cfg.ID)
	}

	if cfg.Title != "My Test Case" {
		t.Errorf("expected title 'My Test Case', got '%s'", cfg.Title)
	}

	if len(cfg.Input.Prompt) == 0 {
		t.Error("expected prompt to be set")
	}

	if len(cfg.Expect.MustContain) != 1 || cfg.Expect.MustContain[0] != "hello" {
		t.Errorf("expected must_contain ['hello'], got %v", cfg.Expect.MustContain)
	}
}

func TestLoader_LoadAllCases(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "my-skill")
	evalsDir := filepath.Join(skillDir, "evals")
	casesDir := filepath.Join(evalsDir, "cases")
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		t.Fatalf("failed to create cases dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# skill\n"), 0o600); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// Create eval.yaml
	evalContent := `schema_version: v1alpha1

environment:
  type: none

engine:
  name: claude_code
  model:
    provider: anthropic
    name: test

cases:
  files:
    - evals/cases/case1.yaml
    - evals/cases/case2.yaml

judge:
  type: rule_based
`
	evalPath := filepath.Join(evalsDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte(evalContent), 0o600); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	// Create case files
	case1 := `id: case-1
input:
  prompt: Test prompt 1
`
	case2 := `id: case-2
input:
  prompt: Test prompt 2
`
	if err := os.WriteFile(filepath.Join(casesDir, "case1.yaml"), []byte(case1), 0o600); err != nil {
		t.Fatalf("failed to write case1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(casesDir, "case2.yaml"), []byte(case2), 0o600); err != nil {
		t.Fatalf("failed to write case2: %v", err)
	}

	loader := NewLoader(evalPath)
	cfg, err := loader.LoadEvalConfig()
	if err != nil {
		t.Fatalf("LoadEvalConfig failed: %v", err)
	}

	cases, err := loader.LoadAllCases(cfg)
	if err != nil {
		t.Fatalf("LoadAllCases failed: %v", err)
	}

	if len(cases) != 2 {
		t.Errorf("expected 2 cases, got %d", len(cases))
	}

	if cases[0].ID != "case-1" {
		t.Errorf("expected first case id 'case-1', got '%s'", cases[0].ID)
	}

	if cases[1].ID != "case-2" {
		t.Errorf("expected second case id 'case-2', got '%s'", cases[1].ID)
	}
}

func TestLoader_LoadAll(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "my-skill")
	evalsDir := filepath.Join(skillDir, "evals")
	casesDir := filepath.Join(evalsDir, "cases")
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		t.Fatalf("failed to create cases dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# skill\n"), 0o600); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	evalContent := `schema_version: v1alpha1

environment:
  type: none

engine:
  name: claude_code
  model:
    provider: anthropic
    name: test

cases:
  files:
    - evals/cases/test.yaml

judge:
  type: rule_based
`
	evalPath := filepath.Join(evalsDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte(evalContent), 0o600); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	caseContent := `id: test
input:
  prompt: Test
`
	if err := os.WriteFile(filepath.Join(casesDir, "test.yaml"), []byte(caseContent), 0o600); err != nil {
		t.Fatalf("failed to write case: %v", err)
	}

	loader := NewLoader(evalPath)
	result, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	if result.Eval == nil {
		t.Error("expected Eval to be set")
	}

	if len(result.Cases) != 1 {
		t.Errorf("expected 1 case, got %d", len(result.Cases))
	}
}

func TestLoader_EvalDir(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "sample-skill")
	evalPath := filepath.Join(skillDir, "evals", "eval.yaml")
	if err := os.MkdirAll(filepath.Dir(evalPath), 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# sample skill\n"), 0o600); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}
	if err := os.WriteFile(evalPath, []byte("schema_version: v1alpha1\n"), 0o600); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	loader := NewLoader(evalPath)
	if loader.EvalDir() != filepath.Dir(evalPath) {
		t.Errorf("expected EvalDir '%s', got '%s'", filepath.Dir(evalPath), loader.EvalDir())
	}

	if loader.CasesDir() != filepath.Join(skillDir, "evals", "cases") {
		t.Errorf("expected CasesDir '%s', got '%s'", filepath.Join(skillDir, "evals", "cases"), loader.CasesDir())
	}

	if loader.SkillDir() != skillDir {
		t.Errorf("expected SkillDir '%s', got '%s'", skillDir, loader.SkillDir())
	}

	if loader.FixtureDir() != filepath.Join(skillDir, "evals", "fixtures") {
		t.Errorf("expected FixtureDir '%s', got '%s'", filepath.Join(skillDir, "evals", "fixtures"), loader.FixtureDir())
	}
}

func TestLoader_FixtureDirUsesActualEvalDir(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "sample-skill")
	evalPath := filepath.Join(skillDir, "benchmarks", "eval.yaml")
	if err := os.MkdirAll(filepath.Dir(evalPath), 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# sample skill\n"), 0o600); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}
	if err := os.WriteFile(evalPath, []byte("schema_version: v1alpha1\n"), 0o600); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	loader := NewLoader(evalPath)
	want := filepath.Join(filepath.Dir(evalPath), "fixtures")
	if loader.FixtureDir() != want {
		t.Errorf("expected FixtureDir '%s', got '%s'", want, loader.FixtureDir())
	}
}

func TestLoader_FileNotFound(t *testing.T) {
	t.Parallel()
	loader := NewLoader("/nonexistent/eval.yaml")
	_, err := loader.LoadEvalConfig()
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoader_InvalidYAML(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	evalPath := filepath.Join(tmpDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte("invalid: [yaml: content"), 0o600); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	loader := NewLoader(evalPath)
	_, err := loader.LoadEvalConfig()
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoader_SkillDirFallback(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	evalPath := filepath.Join(tmpDir, "level1", "level2", "evals", "eval.yaml")

	loader := NewLoader(evalPath)

	if loader.SkillDir() != filepath.Dir(filepath.Dir(evalPath)) {
		t.Errorf("expected fallback SkillDir '%s', got '%s'", filepath.Dir(filepath.Dir(evalPath)), loader.SkillDir())
	}
}

func TestLoader_SkillDirSearchDepthLimit(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	rootSkillDir := filepath.Join(tmpDir, "root-skill")
	if err := os.MkdirAll(rootSkillDir, 0o755); err != nil {
		t.Fatalf("failed to create root skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootSkillDir, "SKILL.md"), []byte("# root skill\n"), 0o600); err != nil {
		t.Fatalf("failed to write root SKILL.md: %v", err)
	}

	deepEvalDir := rootSkillDir
	for i := range maxSkillDirSearchDepth + 2 {
		deepEvalDir = filepath.Join(deepEvalDir, fmt.Sprintf("level-%d", i))
	}
	evalPath := filepath.Join(deepEvalDir, "evals", "eval.yaml")

	loader := NewLoader(evalPath)
	want := filepath.Dir(filepath.Dir(evalPath))
	if loader.SkillDir() != want {
		t.Errorf("expected depth-limited fallback SkillDir '%s', got '%s'", want, loader.SkillDir())
	}
}
