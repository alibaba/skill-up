package config

import (
	"strings"
	"testing"
)

func evalWithJudge(judge JudgeConfig) *EvalConfig {
	return &EvalConfig{
		SchemaVersion: "v1alpha1",
		Environment:   Environment{Type: "none"},
		Engine: EngineConfig{
			Name:  "claude_code",
			Model: ModelConfig{Provider: "anthropic", Name: "claude-sonnet-4-6"},
		},
		Cases: CasesConfig{Files: []string{"evals/cases/test.yaml"}},
		Judge: judge,
	}
}

func TestValidator_RejectsEmptyToolCalledName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "   "} {
		cfg := evalWithJudge(JudgeConfig{
			Type:    "rule_based",
			Success: []Rule{{ToolCalled: &ToolCalledRule{Name: name}}},
		})
		err := NewValidator().ValidateEvalConfig(cfg)
		if err == nil || !strings.Contains(err.Error(), "judge.success[0].tool_called.name is required") {
			t.Errorf("name %q: error = %v, want tool_called.name required", name, err)
		}
	}

	cfg := evalWithJudge(JudgeConfig{
		Type:    "rule_based",
		Success: []Rule{{ToolCalled: &ToolCalledRule{Name: "bash"}}},
	})
	if err := NewValidator().ValidateEvalConfig(cfg); err != nil {
		t.Fatalf("valid tool_called name rejected: %v", err)
	}
}

func TestValidator_RejectsInvalidFileRulePaths(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"", "   ", ".", "./", `.\`, "/", `\`, "foo/.."} {
		cfg := evalWithJudge(JudgeConfig{
			Type:    "rule_based",
			Success: []Rule{{FilesExist: []string{path}}},
		})
		err := NewValidator().ValidateEvalConfig(cfg)
		if err == nil || !strings.Contains(err.Error(), "judge.success[0].files_exist[0]") {
			t.Errorf("files_exist %q: error = %v, want a path error", path, err)
		}

		cfg = evalWithJudge(JudgeConfig{
			Type:    "rule_based",
			Failure: []Rule{{FilesNotExist: []string{path}}},
		})
		err = NewValidator().ValidateEvalConfig(cfg)
		if err == nil || !strings.Contains(err.Error(), "judge.failure[0].files_not_exist[0]") {
			t.Errorf("files_not_exist %q: error = %v, want a path error", path, err)
		}
	}
}

func TestValidator_AcceptsPortableFileRulePaths(t *testing.T) {
	t.Parallel()

	cfg := evalWithJudge(JudgeConfig{
		Type: "rule_based",
		Success: []Rule{{
			FilesExist:    []string{"out.txt", "dir/file.txt", `dir\other.txt`},
			FilesNotExist: []string{"missing.txt"},
		}},
	})
	if err := NewValidator().ValidateEvalConfig(cfg); err != nil {
		t.Fatalf("portable file paths rejected: %v", err)
	}
}

func TestValidator_RejectsInvalidExpectFilePaths(t *testing.T) {
	t.Parallel()

	for _, path := range []string{".", "./", `.\`, "/", `\`, "foo/.."} {
		cfg := &CaseConfig{
			ID:     "case-1",
			Input:  Input{Prompt: "hello"},
			Expect: Expect{FilesExist: []string{path}},
		}
		err := NewValidator().ValidateCaseConfig(cfg)
		if err == nil || !strings.Contains(err.Error(), "expect.files_exist[0] invalid file path") {
			t.Errorf("expect.files_exist %q: error = %v, want an invalid path error", path, err)
		}

		cfg.Expect = Expect{FilesNotExist: []string{path}}
		err = NewValidator().ValidateCaseConfig(cfg)
		if err == nil || !strings.Contains(err.Error(), "expect.files_not_exist[0] invalid file path") {
			t.Errorf("expect.files_not_exist %q: error = %v, want an invalid path error", path, err)
		}
	}

	cfg := &CaseConfig{
		ID:     "case-1",
		Input:  Input{Prompt: "hello"},
		Expect: Expect{FilesExist: []string{"out.txt"}, FilesNotExist: []string{"missing.txt"}},
	}
	if err := NewValidator().ValidateCaseConfig(cfg); err != nil {
		t.Fatalf("portable expect paths rejected: %v", err)
	}
}

func TestValidator_PostConditionRejectsEmptyMatcherEntries(t *testing.T) {
	t.Parallel()

	cfg := &CaseConfig{
		ID: "case-1",
		Input: Input{Turns: []Turn{{
			Role:          "user",
			Content:       "hello",
			PostCondition: &PostCondition{MustContainAny: []string{""}},
		}}},
	}
	if err := NewValidator().ValidateCaseConfig(cfg); err == nil || !strings.Contains(err.Error(), "post_condition.must_contain_any[0] must not be empty") {
		t.Errorf("empty post_condition entry: error = %v, want a must-not-be-empty error", err)
	}

	// Whitespace is a real matcher (e.g. a newline), so it stays valid.
	cfg = &CaseConfig{
		ID: "case-1",
		Input: Input{Turns: []Turn{{
			Role:          "user",
			Content:       "hello",
			PostCondition: &PostCondition{MustContainAny: []string{"\n", "  "}},
		}}},
	}
	if err := NewValidator().ValidateCaseConfig(cfg); err != nil {
		t.Fatalf("whitespace post_condition matcher rejected: %v", err)
	}
}
