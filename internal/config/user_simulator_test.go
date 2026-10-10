package config

import (
	"strings"
	"testing"
)

func TestValidateUserSimulatorConfiguration(t *testing.T) {
	t.Parallel()
	validator := NewValidator()
	eval := &EvalConfig{
		SchemaVersion: "v1alpha1",
		Environment:   Environment{Type: "none"},
		Engine:        EngineConfig{Name: "codex"},
		Cases:         CasesConfig{Files: []string{"evals/cases/test.yaml"}},
		UserSimulator: UserSimulatorModel{Provider: "simulation", Protocol: "openai", Model: "model-id"},
	}
	if err := validator.ValidateEvalConfig(eval); err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"openai", "anthropic"} {
		eval.UserSimulator.Protocol = protocol
		if err := validator.ValidateEvalConfig(eval); err != nil {
			t.Fatal(err)
		}
		simulated := &CaseConfig{Input: Input{Prompt: "start"}, UserSimulator: &UserSimulatorScenario{Scenario: "staging only"}}
		if err := validator.ValidateCasesWithEvalDefaults(eval, []*CaseConfig{simulated}); err != nil {
			t.Fatal(err)
		}
	}
	eval.UserSimulator.Protocol = "unsupported"
	if err := validator.ValidateEvalConfig(eval); err == nil || !strings.Contains(err.Error(), "protocol") {
		t.Fatalf("unsupported protocol error = %v", err)
	}

	caseCfg := &CaseConfig{Input: Input{Turns: []Turn{{Role: "user", Content: "start"}, {Role: "user", Respond: "answer"}}}, UserSimulator: &UserSimulatorScenario{Scenario: "staging only"}}
	if err := validator.ValidateCaseConfig(caseCfg); err != nil {
		t.Fatal(err)
	}
	caseCfg.Input.Turns[1].Content = "also fixed"
	if err := validator.ValidateCaseConfig(caseCfg); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("mixed content/respond error = %v", err)
	}
	caseCfg.Input.Turns[1].Content = ""
	caseCfg.UserSimulator = nil
	if err := validator.ValidateCaseConfig(caseCfg); err == nil || !strings.Contains(err.Error(), "requires user_simulator.scenario") {
		t.Fatalf("missing scenario error = %v", err)
	}
	caseCfg.Input = Input{Prompt: "start"}
	caseCfg.UserSimulator = &UserSimulatorScenario{Scenario: "staging only"}
	if err := validator.ValidateCaseConfig(caseCfg); err != nil {
		t.Fatalf("autonomous case: %v", err)
	}
	if err := validator.ValidateCasesWithEvalDefaults(&EvalConfig{}, []*CaseConfig{caseCfg}); err == nil || !strings.Contains(err.Error(), "eval-level provider") {
		t.Fatalf("missing eval-level model error = %v", err)
	}
}
