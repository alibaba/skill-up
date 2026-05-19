package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/evaluator"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/userconfig"
)

const agentJudgeType = "agent_judge"

const testRuntimeOpenSandbox = "opensandbox"

func TestRunCommand_UsesUsageAwareArgs(t *testing.T) {
	t.Parallel()

	if runCmd.Args == nil {
		t.Fatal("expected run command args validator to be configured")
	}
}

func TestGetVerbosity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "default", args: nil, want: 0},
		{name: "short once", args: []string{"-v"}, want: 1},
		{name: "short twice", args: []string{"-vv"}, want: 2},
		{name: "long bool true", args: []string{"--verbose=true"}, want: 1},
		{name: "long bool false", args: []string{"--verbose=false"}, want: 0},
		{name: "long numeric", args: []string{"--verbose=2"}, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := &cobra.Command{Use: "test"}
			var verbose verbosityValue
			cmd.Flags().VarP(&verbose, "verbose", "v", "")
			cmd.Flags().Lookup("verbose").NoOptDefVal = "true"

			if err := cmd.Flags().Parse(tt.args); err != nil {
				t.Fatalf("parse flags: %v", err)
			}

			got, err := getVerbosity(cmd)
			if err != nil {
				t.Fatalf("getVerbosity: %v", err)
			}
			if got != tt.want {
				t.Fatalf("verbosity: got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRunCommand_ValidConfig(t *testing.T) {
	if _, err := os.Stat(testEvalPath); os.IsNotExist(err) {
		t.Skip("examples/code-stats/evals/eval.yaml not found")
	}

	t.Parallel()

	loader := config.NewLoader(testEvalPath)
	result, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	validator := config.NewValidator()
	err = validator.ValidateAll(result)
	if err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}

	if len(result.Cases) == 0 {
		t.Error("expected at least one case")
	}
}

func TestRunCommand_EngineConfig(t *testing.T) {
	if _, err := os.Stat(testEvalPath); os.IsNotExist(err) {
		t.Skip("examples/code-stats/evals/eval.yaml not found")
	}

	t.Parallel()

	loader := config.NewLoader(testEvalPath)
	result, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if result.Eval.Engine.Name == "" {
		t.Error("engine name should not be empty")
	}
	// engine.model.provider and engine.model.name are optional;
	// when omitted, the engine uses its local default model configuration.
}

func TestRunCommand_RuntimeConfig(t *testing.T) {
	if _, err := os.Stat(testEvalPath); os.IsNotExist(err) {
		t.Skip("examples/code-stats/evals/eval.yaml not found")
	}

	t.Parallel()

	loader := config.NewLoader(testEvalPath)
	result, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	runtimeType := result.Eval.Environment.Type
	if runtimeType == "" {
		t.Error("runtime type should not be empty")
	}
}

func TestRunCommand_NonexistentPath(t *testing.T) {
	t.Parallel()

	evalPath := "/nonexistent/path/eval.yaml"
	loader := config.NewLoader(evalPath)
	_, err := loader.LoadAll()
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestRunCommand_CaseFilter(t *testing.T) {
	if _, err := os.Stat(testEvalPath); os.IsNotExist(err) {
		t.Skip("examples/code-stats/evals/eval.yaml not found")
	}

	t.Parallel()

	loader := config.NewLoader(testEvalPath)
	result, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if len(result.Cases) == 0 {
		t.Skip("no cases to filter")
	}

	// Test case filter logic
	caseID := result.Cases[0].ID
	var filtered []*config.CaseConfig
	for _, c := range result.Cases {
		if c.ID == caseID {
			filtered = append(filtered, c)

			break
		}
	}

	if len(filtered) != 1 {
		t.Errorf("expected 1 filtered case, got %d", len(filtered))
	}
	if filtered[0].ID != caseID {
		t.Errorf("expected case ID %s, got %s", caseID, filtered[0].ID)
	}
}

func TestRunCommand_CaseNotFound(t *testing.T) {
	if _, err := os.Stat(testEvalPath); os.IsNotExist(err) {
		t.Skip("examples/code-stats/evals/eval.yaml not found")
	}

	t.Parallel()

	loader := config.NewLoader(testEvalPath)
	result, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Test case not found
	caseFilter := "nonexistent-case-id"
	var filtered []*config.CaseConfig
	for _, c := range result.Cases {
		if c.ID == caseFilter {
			filtered = append(filtered, c)

			break
		}
	}

	if len(filtered) != 0 {
		t.Errorf("expected 0 filtered cases for nonexistent ID, got %d", len(filtered))
	}
}

func TestLoadAnthropicEvals(t *testing.T) {
	t.Parallel()

	// Create a temp evals.json
	evalsJSON := `{
		"skill_name": "test-skill",
		"evals": [
			{
				"id": 1,
				"prompt": "Do something",
				"expected_output": "Something done",
				"expectations": ["output is correct", "format is valid"]
			},
			{
				"id": 2,
				"prompt": "Do another thing",
				"expected_output": "Another thing done",
				"expectations": ["result is accurate"]
			}
		]
	}`

	tmpFile, err := os.CreateTemp(t.TempDir(), "evals-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	if _, err := tmpFile.WriteString(evalsJSON); err != nil {
		t.Fatalf("failed to write: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("failed to close temp file: %v", err)
	}

	cases, err := loadAnthropicEvals(tmpFile.Name())
	if err != nil {
		t.Fatalf("loadAnthropicEvals failed: %v", err)
	}

	if len(cases) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(cases))
	}

	// Check first case
	if cases[0].ID != "case-1" {
		t.Errorf("expected case-1, got %s", cases[0].ID)
	}
	if cases[0].Input.Prompt != "Do something" {
		t.Errorf("unexpected prompt: %s", cases[0].Input.Prompt)
	}
	if cases[0].Judge.Type != agentJudgeType {
		t.Errorf("expected agent_judge, got %s", cases[0].Judge.Type)
	}
	if len(cases[0].Judge.Criteria) != 2 {
		t.Errorf("expected 2 criteria, got %d", len(cases[0].Judge.Criteria))
	}
}

func TestLoadAutoModeEvaluation_EvalsJSONInstallsLocalSkill(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	evalsDir := filepath.Join(root, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# Test Skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	evalsJSON := `{
		"skill_name": "test-skill",
		"evals": [
			{
				"id": 1,
				"prompt": "Do something",
				"expected_output": "Something done",
				"expectations": ["output is correct"]
			}
		]
	}`
	if err := os.WriteFile(filepath.Join(evalsDir, "evals.json"), []byte(evalsJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	cases, evalCfg, loader, err := loadAutoModeEvaluation(t.Context(), root)
	if err != nil {
		t.Fatalf("loadAutoModeEvaluation failed: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("expected 1 case, got %d", len(cases))
	}
	if len(evalCfg.Skills) != 1 {
		t.Fatalf("expected auto mode to install local skill, got %d skills", len(evalCfg.Skills))
	}
	if got := evalCfg.Skills[0]; got.Source != localPathSkillSource || got.Path != currentDirectorySkill {
		t.Fatalf("unexpected skill ref: %+v", got)
	}
	if loader.SkillDir() != root {
		t.Fatalf("expected loader skill dir %q, got %q", root, loader.SkillDir())
	}
}

func TestLoadAutoModeEvaluation_PrefersEvalYAML(t *testing.T) {
	root := t.TempDir()
	writeAutoModeSkill(t, root)
	writeAutoModeEvalYAML(t, root, "yaml-case")
	writeAutoModeEvalsJSON(t, root)

	cases, _, _, err := loadAutoModeEvaluation(t.Context(), root)
	if err != nil {
		t.Fatalf("loadAutoModeEvaluation failed: %v", err)
	}

	if len(cases) != 1 {
		t.Fatalf("expected 1 case from eval.yaml, got %d", len(cases))
	}
	if cases[0].ID != "yaml-case" {
		t.Fatalf("case ID = %q, want yaml-case", cases[0].ID)
	}
}

func TestLoadAutoModeEvaluation_FallsBackToEvalsJSON(t *testing.T) {
	root := t.TempDir()
	writeAutoModeSkill(t, root)
	writeAutoModeEvalsJSON(t, root)

	cases, _, _, err := loadAutoModeEvaluation(t.Context(), root)
	if err != nil {
		t.Fatalf("loadAutoModeEvaluation failed: %v", err)
	}

	if len(cases) != 1 {
		t.Fatalf("expected 1 case from evals.json, got %d", len(cases))
	}
	if cases[0].ID != "case-1" {
		t.Fatalf("case ID = %q, want case-1", cases[0].ID)
	}
}

func writeAutoModeSkill(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# test skill\n"), 0o600); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}
}

func writeAutoModeEvalYAML(t *testing.T, root, caseID string) {
	t.Helper()
	evalsDir := filepath.Join(root, "evals")
	casesDir := filepath.Join(evalsDir, "cases")
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		t.Fatalf("failed to create evals/cases: %v", err)
	}

	evalYAML := fmt.Sprintf(`schema_version: v1alpha1
environment:
  type: none
engine:
  name: claude_code
  model:
    provider: dashscope
    name: qwen3.6-plus
cases:
  files:
    - evals/cases/%s.yaml
`, caseID)
	if err := os.WriteFile(filepath.Join(evalsDir, "eval.yaml"), []byte(evalYAML), 0o600); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	caseYAML := fmt.Sprintf("id: %s\ninput:\n  prompt: Say hi\n", caseID)
	if err := os.WriteFile(filepath.Join(casesDir, caseID+".yaml"), []byte(caseYAML), 0o600); err != nil {
		t.Fatalf("failed to write case yaml: %v", err)
	}
}

func writeAutoModeEvalsJSON(t *testing.T, root string) {
	t.Helper()
	evalsDir := filepath.Join(root, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatalf("failed to create evals dir: %v", err)
	}
	evalsJSON := `{
  "skill_name": "test-skill",
  "evals": [
    {
      "id": 1,
      "prompt": "Use the skill",
      "expectations": ["works"]
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(evalsDir, "evals.json"), []byte(evalsJSON), 0o600); err != nil {
		t.Fatalf("failed to write evals.json: %v", err)
	}
}

func TestDiscoverEvalYAMLFromSkillTree(t *testing.T) {
	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	deep := filepath.Join(inner, "deep")
	evalRel := filepath.Join("evals", "eval.yaml")
	if err := os.MkdirAll(filepath.Join(inner, "evals"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, evalRel), []byte("eval: {}\ncases: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Chdir(deep)

	want, err := filepath.Abs(filepath.Join(inner, evalRel))
	if err != nil {
		t.Fatal(err)
	}
	got, err := discoverEvalYAMLFromSkillTree()
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	gotAbs, err := filepath.Abs(got)
	if err != nil {
		t.Fatal(err)
	}
	if gotAbs != want {
		t.Fatalf("got %q, want %q", gotAbs, want)
	}
}

func TestDiscoverEvalYAMLFromSkillTree_notFound(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	_, err := discoverEvalYAMLFromSkillTree()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDiscoverAutoEvalRootFromSkillTree(t *testing.T) {
	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	deep := filepath.Join(inner, "deep")
	if err := os.MkdirAll(filepath.Join(inner, "evals"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "evals", "evals.json"), []byte(`{"skill_name":"x","evals":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Chdir(deep)

	want, err := filepath.Abs(inner)
	if err != nil {
		t.Fatal(err)
	}
	got, err := discoverAutoEvalRootFromSkillTree()
	if err != nil {
		t.Fatalf("discover auto root: %v", err)
	}
	gotAbs, err := filepath.Abs(got)
	if err != nil {
		t.Fatal(err)
	}
	if gotAbs != want {
		t.Fatalf("got %q, want %q", gotAbs, want)
	}
}

func TestDiscoverAutoEvalRootFromSkillTree_notFound(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	_, err := discoverAutoEvalRootFromSkillTree()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNormalizeAutoEvalRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	evalsDir := filepath.Join(root, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	evalPath := filepath.Join(evalsDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte("eval: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	gotDir, err := normalizeAutoEvalRoot(root)
	if err != nil {
		t.Fatalf("normalize dir: %v", err)
	}
	if gotDir != root {
		t.Fatalf("normalize dir got %q, want %q", gotDir, root)
	}

	gotFile, err := normalizeAutoEvalRoot(evalPath)
	if err != nil {
		t.Fatalf("normalize file: %v", err)
	}
	if gotFile != root {
		t.Fatalf("normalize file got %q, want %q", gotFile, root)
	}

	_, err = normalizeAutoEvalRoot(filepath.Join(root, "missing"))
	if err == nil {
		t.Fatal("expected error for missing path")
	}

	looseFile := filepath.Join(root, "eval.yaml")
	if err := os.WriteFile(looseFile, []byte("eval: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = normalizeAutoEvalRoot(looseFile)
	if err == nil {
		t.Fatal("expected error for file outside evals directory")
	}
}

func TestLoadAutoModeEvaluation_AddsLocalSkillWhenSkillFileExists(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	evalsDir := filepath.Join(root, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	evalsJSON := `{"skill_name":"x","evals":[{"id":1,"prompt":"hi","expected_output":"hi","expectations":["hi"]}]}`
	if err := os.WriteFile(filepath.Join(evalsDir, "evals.json"), []byte(evalsJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	cases, evalCfg, loader, err := loadAutoModeEvaluation(t.Context(), root)
	if err != nil {
		t.Fatalf("loadAutoModeEvaluation: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("expected 1 case, got %d", len(cases))
	}
	if loader == nil {
		t.Fatal("expected loader")
	}
	if got := loader.SkillDir(); got != root {
		t.Fatalf("loader.SkillDir() = %q, want %q", got, root)
	}
	if len(evalCfg.Skills) != 1 {
		t.Fatalf("expected 1 auto skill, got %d", len(evalCfg.Skills))
	}
	if evalCfg.Skills[0].Source != localPathSkillSource || evalCfg.Skills[0].Path != currentDirectorySkill {
		t.Fatalf("unexpected auto skill: %+v", evalCfg.Skills[0])
	}
}

func TestLoadAutoModeEvaluation_DoesNotAddLocalSkillWithoutSkillFile(t *testing.T) {
	root := t.TempDir()
	evalsDir := filepath.Join(root, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	evalsJSON := `{"skill_name":"x","evals":[{"id":1,"prompt":"hi","expected_output":"hi","expectations":["hi"]}]}`
	if err := os.WriteFile(filepath.Join(evalsDir, "evals.json"), []byte(evalsJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	cases, evalCfg, loader, err := loadAutoModeEvaluation(t.Context(), root)
	if err != nil {
		t.Fatalf("loadAutoModeEvaluation: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("expected 1 case, got %d", len(cases))
	}
	if loader == nil {
		t.Fatal("expected loader")
	}
	if len(evalCfg.Skills) != 0 {
		t.Fatalf("expected 0 auto skills, got %d", len(evalCfg.Skills))
	}
}

func TestLoadFromEvalYAML_AddsImplicitLocalSkillWhenSkillsOmitted(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	evalsDir := filepath.Join(root, "evals")
	casesDir := filepath.Join(evalsDir, "cases")
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	evalYAML := `schema_version: v1alpha1
environment:
  type: none
engine:
  name: qoder-cli
cases:
  files:
    - evals/cases/case.yaml
`
	if err := os.WriteFile(filepath.Join(evalsDir, "eval.yaml"), []byte(evalYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	caseYAML := `id: case-1
input:
  prompt: hi
expect:
  must_contain:
    - hi
`
	if err := os.WriteFile(filepath.Join(casesDir, "case.yaml"), []byte(caseYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, evalCfg, loader, err := loadFromEvalYAML(filepath.Join(evalsDir, "eval.yaml"))
	if err != nil {
		t.Fatalf("loadFromEvalYAML: %v", err)
	}
	if loader == nil {
		t.Fatal("expected loader")
	}
	if len(evalCfg.Skills) != 1 {
		t.Fatalf("expected 1 implicit skill, got %d", len(evalCfg.Skills))
	}
	if evalCfg.Skills[0].Source != localPathSkillSource || evalCfg.Skills[0].Path != currentDirectorySkill {
		t.Fatalf("unexpected implicit skill: %+v", evalCfg.Skills[0])
	}
}

func TestLoadFromEvalYAML_RespectsExplicitEmptySkills(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	evalsDir := filepath.Join(root, "evals")
	casesDir := filepath.Join(evalsDir, "cases")
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	evalYAML := `schema_version: v1alpha1
environment:
  type: none
skills: []
engine:
  name: qoder-cli
cases:
  files:
    - evals/cases/case.yaml
`
	if err := os.WriteFile(filepath.Join(evalsDir, "eval.yaml"), []byte(evalYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	caseYAML := `id: case-1
input:
  prompt: hi
expect:
  must_contain:
    - hi
`
	if err := os.WriteFile(filepath.Join(casesDir, "case.yaml"), []byte(caseYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	evalPath := filepath.Join(evalsDir, "eval.yaml")
	gotEvalPath, cases, evalCfg, loader, err := loadFromEvalYAML(evalPath)
	if err != nil {
		t.Fatalf("loadFromEvalYAML: %v", err)
	}
	if gotEvalPath != evalPath {
		t.Fatalf("got eval path %q, want %q", gotEvalPath, evalPath)
	}
	if len(cases) != 1 {
		t.Fatalf("expected 1 case, got %d", len(cases))
	}
	if loader == nil {
		t.Fatal("expected loader")
	}
	if len(evalCfg.Skills) != 0 {
		t.Fatalf("expected explicit empty skills to stay empty, got %d", len(evalCfg.Skills))
	}
}

func TestExitStatusError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		results []evaluator.EvalResult
		wantErr string
	}{
		{
			name: "all pass returns nil",
			results: []evaluator.EvalResult{
				{Status: judge.StatusPass},
				{Status: judge.StatusSkip},
			},
		},
		{
			name: "fail returns error",
			results: []evaluator.EvalResult{
				{Status: judge.StatusPass},
				{Status: judge.StatusFail},
			},
			wantErr: "one or more cases failed",
		},
		{
			name: "error returns error",
			results: []evaluator.EvalResult{
				{Status: judge.StatusPass},
				{Status: judge.StatusError},
			},
			wantErr: "one or more cases errored",
		},
		{
			name: "error takes precedence over fail",
			results: []evaluator.EvalResult{
				{Status: judge.StatusFail},
				{Status: judge.StatusError},
			},
			wantErr: "one or more cases errored",
		},
		{
			name: "without_skill fail does not affect exit status",
			results: []evaluator.EvalResult{
				{Configuration: "with_skill", Status: judge.StatusPass},
				{Configuration: "without_skill", Status: judge.StatusFail},
			},
		},
		{
			name: "without_skill error does not affect exit status",
			results: []evaluator.EvalResult{
				{Configuration: "with_skill", Status: judge.StatusPass},
				{Configuration: "without_skill", Status: judge.StatusError},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := exitStatusError(nil, tc.results)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}

				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("expected error %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestExitStatusError_PrintsCaseErrors(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	err := exitStatusError(&stderr, []evaluator.EvalResult{
		{CaseID: "case-pass", Status: judge.StatusPass},
		{CaseID: "baseline-error", Configuration: "without_skill", Status: judge.StatusError, Error: errors.New("baseline error")},
		{CaseID: "case-1", Status: judge.StatusError, Error: errors.New("judge evaluation failed: API Error: 400")},
		{CaseID: "", Status: judge.StatusError},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got, want := err.Error(), "one or more cases errored"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}

	got := stderr.String()
	if !strings.Contains(got, "[ERROR] case case-1: judge evaluation failed: API Error: 400") {
		t.Fatalf("stderr missing case-specific error, got %q", got)
	}
	if !strings.Contains(got, "[ERROR] case <unknown>: <nil>") {
		t.Fatalf("stderr missing fallback error formatting, got %q", got)
	}
	if strings.Contains(got, "baseline-error") {
		t.Fatalf("stderr should ignore without_skill errors, got %q", got)
	}
}

func TestEvaluateOptionsFromFlags(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}
	cmd.Flags().Bool("no-delete", false, "")
	cmd.Flags().String("output-dir", "", "")
	cmd.Flags().Int("iteration", 1, "")
	cmd.Flags().StringArray("format", nil, "")

	if err := cmd.Flags().Set("no-delete", "true"); err != nil {
		t.Fatalf("set no-delete: %v", err)
	}
	if err := cmd.Flags().Set("output-dir", "/tmp/custom-output"); err != nil {
		t.Fatalf("set output-dir: %v", err)
	}
	if err := cmd.Flags().Set("iteration", "99"); err != nil {
		t.Fatalf("set iteration: %v", err)
	}

	opts, err := evaluateOptionsFromFlags(cmd)
	if err != nil {
		t.Fatalf("evaluateOptionsFromFlags failed: %v", err)
	}

	if opts.DeleteWorkspace {
		t.Fatal("DeleteWorkspace = true, want false when --no-delete is set")
	}
	if opts.OutputDir != "/tmp/custom-output" {
		t.Fatalf("OutputDir = %q, want /tmp/custom-output", opts.OutputDir)
	}
	if opts.Iteration != 99 {
		t.Fatalf("Iteration = %d, want 99", opts.Iteration)
	}
	if len(opts.Formats) != 0 {
		t.Fatalf("Formats = %v, want empty", opts.Formats)
	}
}

func TestEvaluateOptionsFromFlags_RejectsNegativeIteration(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}
	cmd.Flags().Bool("no-delete", false, "")
	cmd.Flags().String("output-dir", "", "")
	cmd.Flags().Int("iteration", 0, "")
	cmd.Flags().StringArray("format", nil, "")

	if err := cmd.Flags().Set("iteration", "-1"); err != nil {
		t.Fatalf("set iteration: %v", err)
	}

	_, err := evaluateOptionsFromFlags(cmd)
	if err == nil {
		t.Fatal("expected error for iteration=-1")
	}
	if got := err.Error(); got != "invalid --iteration -1: must be >= 0" {
		t.Fatalf("unexpected error: %q", got)
	}
}

// runResolveEvalConfigCase parametrises "set --model flag → resolveEvalConfig
// → assert provider/name". Extracted to silence dupl on near-identical bodies.
func runResolveEvalConfigCase(t *testing.T, modelFlag, engine, wantProvider, wantName string) {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().String("model", "", "")
	if err := cmd.Flags().Set("model", modelFlag); err != nil {
		t.Fatalf("set model: %v", err)
	}

	cfg := resolveEvalConfig(config.DefaultEvalConfig(), engine, cmd)
	if got := cfg.Engine.Model.Provider; got != wantProvider {
		t.Fatalf("Engine.Model.Provider = %q, want %q", got, wantProvider)
	}
	if got := cfg.Engine.Model.Name; got != wantName {
		t.Fatalf("Engine.Model.Name = %q, want %q", got, wantName)
	}
}

func TestResolveEvalConfig_AllowsRawModelOverrideForAnyEngine(t *testing.T) {
	t.Parallel()
	runResolveEvalConfigCase(t, "auto", "codex", "", "auto")
}

func TestResolveEvalConfig_ParsesProviderQualifiedModel(t *testing.T) {
	t.Parallel()
	runResolveEvalConfigCase(t, "anthropic/auto", "claude_code", "anthropic", "auto")
}

func TestApplyRunConfigOverrides_Parallelism(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}
	cmd.Flags().Int("parallelism", 0, "")
	if err := cmd.Flags().Set("parallelism", "4"); err != nil {
		t.Fatalf("set parallelism: %v", err)
	}

	cfg := config.DefaultEvalConfig()
	cfg.Cases.Parallelism = 1
	if err := applyRunConfigOverrides(cfg, cmd); err != nil {
		t.Fatalf("applyRunConfigOverrides: %v", err)
	}
	if got := cfg.Cases.Parallelism; got != 4 {
		t.Fatalf("Cases.Parallelism = %d, want 4", got)
	}
}

func TestApplyRunConfigOverrides_ParallelismUnsetPreservesConfig(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}
	cmd.Flags().Int("parallelism", 0, "")

	cfg := config.DefaultEvalConfig()
	cfg.Cases.Parallelism = 3
	if err := applyRunConfigOverrides(cfg, cmd); err != nil {
		t.Fatalf("applyRunConfigOverrides: %v", err)
	}
	if got := cfg.Cases.Parallelism; got != 3 {
		t.Fatalf("Cases.Parallelism = %d, want 3", got)
	}
}

func TestApplyRunConfigOverrides_RuntimeKwargs(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}
	cmd.Flags().SetNormalizeFunc(normalizeRunFlagName)
	cmd.Flags().StringArray(runtimeKwargFlagName, nil, "")
	if err := cmd.Flags().Set("runtime-kwarg", "base_url=https://sandbox.example.test"); err != nil {
		t.Fatalf("set runtime-kwarg: %v", err)
	}
	if err := cmd.Flags().Set("rk", `extensions={"profile":"ci"}`); err != nil {
		t.Fatalf("set rk: %v", err)
	}

	cfg := config.DefaultEvalConfig()
	cfg.Environment.Kwargs = map[string]string{"base_url": "https://old.example.test"}
	if err := applyRunConfigOverrides(cfg, cmd); err != nil {
		t.Fatalf("applyRunConfigOverrides: %v", err)
	}
	if got := cfg.Environment.Kwargs["base_url"]; got != "https://sandbox.example.test" {
		t.Fatalf("base_url kwarg = %q, want CLI override", got)
	}
	if got := cfg.Environment.Kwargs["extensions"]; got != `{"profile":"ci"}` {
		t.Fatalf("extensions kwarg = %q, want CLI value", got)
	}
}

func TestApplyRunConfigOverrides_RuntimeType(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}
	cmd.Flags().String(runtimeFlagName, "", "")
	if err := cmd.Flags().Set(runtimeFlagName, testRuntimeOpenSandbox); err != nil {
		t.Fatalf("set runtime: %v", err)
	}

	cfg := config.DefaultEvalConfig()
	cfg.Environment.Type = "none"
	if err := applyRunConfigOverrides(cfg, cmd); err != nil {
		t.Fatalf("applyRunConfigOverrides: %v", err)
	}
	if got := cfg.Environment.Type; got != testRuntimeOpenSandbox {
		t.Fatalf("Environment.Type = %q, want opensandbox", got)
	}
}

func TestApplyRunConfigOverrides_RuntimeTypeUnsetPreservesConfig(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}
	cmd.Flags().String(runtimeFlagName, "", "")

	cfg := config.DefaultEvalConfig()
	cfg.Environment.Type = testRuntimeOpenSandbox
	if err := applyRunConfigOverrides(cfg, cmd); err != nil {
		t.Fatalf("applyRunConfigOverrides: %v", err)
	}
	if got := cfg.Environment.Type; got != testRuntimeOpenSandbox {
		t.Fatalf("Environment.Type = %q, want opensandbox", got)
	}
}

func TestApplyRunConfigOverrides_RuntimeTypeRejectsInvalid(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}
	cmd.Flags().String(runtimeFlagName, "", "")
	if err := cmd.Flags().Set(runtimeFlagName, "docker"); err != nil {
		t.Fatalf("set runtime: %v", err)
	}

	cfg := config.DefaultEvalConfig()
	if err := applyRunConfigOverrides(cfg, cmd); err == nil {
		t.Fatal("applyRunConfigOverrides: want error for invalid --runtime, got nil")
	}
}

func TestApplyRunConfigOverrides_RuntimeTypeNoneRejectsNetworkPolicy(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}
	cmd.Flags().String(runtimeFlagName, "", "")
	if err := cmd.Flags().Set(runtimeFlagName, "none"); err != nil {
		t.Fatalf("set runtime: %v", err)
	}

	cfg := config.DefaultEvalConfig()
	cfg.Environment.Type = testRuntimeOpenSandbox
	cfg.Environment.NetworkPolicy = "deny_all"
	if err := applyRunConfigOverrides(cfg, cmd); err == nil {
		t.Fatal("applyRunConfigOverrides: want error for --runtime none with network_policy, got nil")
	}
	if got := cfg.Environment.Type; got != testRuntimeOpenSandbox {
		t.Fatalf("Environment.Type = %q, want unchanged after rejected override", got)
	}
}

func TestApplyRunConfigOverrides_UserConfigKwargs(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}
	cmd.Flags().SetNormalizeFunc(normalizeRunFlagName)
	cmd.Flags().StringArray(runtimeKwargFlagName, nil, "")
	if err := cmd.Flags().Set("runtime-kwarg", "base_url=https://cli.example.test"); err != nil {
		t.Fatalf("set runtime-kwarg: %v", err)
	}

	userCfg := userconfig.Config{
		RuntimeKwargs: map[string]userconfig.Kwargs{
			"opensandbox": {
				"base_url":   "https://userconfig.example.test",
				"extensions": "from-userconfig",
				"new_key":    "from-userconfig",
			},
		},
	}
	ctx := userconfig.WithContext(context.Background(), userCfg, nil)
	cmd.SetContext(ctx)

	cfg := config.DefaultEvalConfig()
	cfg.Environment.Type = "opensandbox"
	cfg.Environment.Kwargs = map[string]string{"extensions": "from-eval"}

	if err := applyRunConfigOverrides(cfg, cmd); err != nil {
		t.Fatalf("applyRunConfigOverrides: %v", err)
	}
	if got := cfg.Environment.Kwargs["base_url"]; got != "https://cli.example.test" {
		t.Fatalf("base_url = %q, want CLI override", got)
	}
	if got := cfg.Environment.Kwargs["extensions"]; got != "from-eval" {
		t.Fatalf("extensions = %q, want eval.yaml value (userconfig should NOT override)", got)
	}
	if got := cfg.Environment.Kwargs["new_key"]; got != "from-userconfig" {
		t.Fatalf("new_key = %q, want userconfig fill-in", got)
	}
}

func TestApplyRunConfigOverrides_RuntimeKwargRejectsInvalidFormat(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}
	cmd.Flags().SetNormalizeFunc(normalizeRunFlagName)
	cmd.Flags().StringArray(runtimeKwargFlagName, nil, "")
	if err := cmd.Flags().Set("runtime-kwarg", "base_url"); err != nil {
		t.Fatalf("set runtime-kwarg: %v", err)
	}

	err := applyRunConfigOverrides(config.DefaultEvalConfig(), cmd)
	if err == nil {
		t.Fatal("expected error for invalid runtime kwarg")
	}
	if got, want := err.Error(), `invalid --runtime-kwarg "base_url": expected key=value`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestApplyRunConfigOverrides_RejectsInvalidParallelism(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		value     string
		wantError string
	}{
		{
			name:      "zero",
			value:     "0",
			wantError: "invalid --parallelism 0: must be >= 1",
		},
		{
			name:      "negative",
			value:     "-1",
			wantError: "invalid --parallelism -1: must be >= 1",
		},
		{
			name:      "too high",
			value:     "257",
			wantError: "invalid --parallelism 257: must be <= 256",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := &cobra.Command{}
			cmd.Flags().Int("parallelism", 0, "")
			if err := cmd.Flags().Set("parallelism", tt.value); err != nil {
				t.Fatalf("set parallelism: %v", err)
			}

			err := applyRunConfigOverrides(config.DefaultEvalConfig(), cmd)
			if err == nil {
				t.Fatalf("expected error for parallelism=%s", tt.value)
			}
			if got := err.Error(); got != tt.wantError {
				t.Fatalf("unexpected error: %q", got)
			}
		})
	}
}

func TestNormalizeCLIModelOverride_StripsProviderPrefix(t *testing.T) {
	t.Parallel()

	if got := normalizeCLIModelOverride("anthropic/auto"); got != "auto" {
		t.Fatalf("normalizeCLIModelOverride() = %q, want auto", got)
	}
}

func TestNormalizeCLIModelOverride_PreservesRawModel(t *testing.T) {
	t.Parallel()

	if got := normalizeCLIModelOverride("claude-sonnet-4-6"); got != "claude-sonnet-4-6" {
		t.Fatalf("normalizeCLIModelOverride() = %q, want claude-sonnet-4-6", got)
	}
}

func TestFilterCases(t *testing.T) {
	t.Parallel()

	cases := []*config.CaseConfig{
		{ID: "basic-hello"},
		{ID: "basic-world"},
		{ID: "advanced-feature"},
	}

	// Include filter
	filtered := filterCases(cases, []string{"basic-*"}, nil)
	if len(filtered) != 2 {
		t.Errorf("include filter: expected 2, got %d", len(filtered))
	}

	// Exclude filter
	filtered = filterCases(cases, nil, []string{"basic-*"})
	if len(filtered) != 1 {
		t.Errorf("exclude filter: expected 1, got %d", len(filtered))
	}
	if filtered[0].ID != "advanced-feature" {
		t.Errorf("expected advanced-feature, got %s", filtered[0].ID)
	}
}
