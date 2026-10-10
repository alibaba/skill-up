package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestResolveJudgePlanInheritanceAndIdentity(t *testing.T) {
	t.Parallel()
	global := []JudgeConfig{{ID: "functional", Type: "script", ScriptPath: "check.sh"}, {ID: "semantic", Type: "agent_judge", Model: "test", Criteria: []string{"quality"}}}
	eval := &EvalConfig{Judges: &global}
	for _, test := range []struct {
		name    string
		caseCfg CaseConfig
		wantIDs []string
		multi   bool
	}{
		{"inherit", CaseConfig{}, []string{"functional", "semantic"}, true},
		{"override-list", CaseConfig{Judges: &[]JudgeConfig{{ID: "only", Type: "script", ScriptPath: "other.sh"}}}, []string{"only"}, true},
		{"override-legacy", CaseConfig{Judge: JudgeConfig{Type: "script", ScriptPath: "legacy.sh"}}, []string{""}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan, err := ResolveJudgePlan(eval, &test.caseCfg)
			if err != nil || plan.Multi != test.multi || len(plan.Judges) != len(test.wantIDs) {
				t.Fatalf("plan = %+v, %v", plan, err)
			}
			for i, id := range test.wantIDs {
				if plan.Judges[i].ID != id {
					t.Fatalf("judges[%d].id = %q", i, plan.Judges[i].ID)
				}
			}
		})
	}
}

func TestDefaultEvalConfigAcceptsProgrammaticJudges(t *testing.T) {
	t.Parallel()
	eval := DefaultEvalConfig()
	members := []JudgeConfig{{ID: "check", Type: "rule_based"}}
	eval.Judges = &members
	plan, err := ResolveJudgePlan(eval, &CaseConfig{})
	if err != nil || !plan.Multi || len(plan.Judges) != 1 {
		t.Fatalf("plan=%+v err=%v judge_set=%t", plan, err, eval.JudgeSet)
	}
}

func TestJudgeFieldPresenceRejectsNullAndMixedYAML(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"judge: {}\njudges:\n  - id: check\n    type: script\n    script_path: check.sh\n",
		"judges: null\n",
		"judges: []\n",
	} {
		var eval EvalConfig
		if err := yaml.Unmarshal([]byte(raw), &eval); err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveJudgePlan(&eval, &CaseConfig{}); err == nil {
			t.Fatalf("accepted YAML %q", raw)
		}
	}
}

func TestMultiJudgeValidationRejectsOutOfRangeTurnRule(t *testing.T) {
	t.Parallel()
	members := []JudgeConfig{{
		ID: "turn-check", Type: "rule_based",
		Success: []Rule{{TurnResponseContains: &TurnResponseContainsRule{Turn: 5, ContainsAll: []string{"done"}}}},
	}}
	caseCfg := &CaseConfig{ID: "case", Input: Input{Turns: []Turn{{Role: "user", Content: "one"}, {Role: "user", Content: "two"}}}}
	eval := &EvalConfig{Judges: &members}
	err := NewValidator().ValidateCasesWithEvalDefaults(eval, []*CaseConfig{caseCfg})
	if err == nil || !strings.Contains(err.Error(), "exceeds total turns 2") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestResolveJudgePlanRejectsAmbiguousOrUnsafeLists(t *testing.T) {
	t.Parallel()
	for _, members := range [][]JudgeConfig{
		{},
		{{ID: "same", Type: "script"}, {ID: "same", Type: "script"}},
		{{ID: "../escape", Type: "script"}},
	} {
		_, err := ResolveJudgePlan(&EvalConfig{Judges: &members}, &CaseConfig{})
		if err == nil {
			t.Fatalf("accepted %+v", members)
		}
	}
	members := []JudgeConfig{{ID: "check", Type: "script"}}
	_, err := ResolveJudgePlan(&EvalConfig{Judges: &members, Judge: JudgeConfig{Type: "rule_based"}}, &CaseConfig{})
	if err == nil || !strings.Contains(err.Error(), "both judge and judges") {
		t.Fatalf("ambiguous plan error = %v", err)
	}
}

func TestMultiJudgeValidatesSimulatorBeforeJudgeBranch(t *testing.T) {
	members := []JudgeConfig{{ID: "check", Type: "rule_based"}}
	eval := &EvalConfig{Judges: &members}
	c := &CaseConfig{ID: "simulated", Input: Input{Prompt: "start"}, UserSimulator: &UserSimulatorScenario{Scenario: "follow up"}}
	v := NewValidator()
	if err := v.ValidateCasesWithEvalDefaults(eval, []*CaseConfig{c}); err == nil || !strings.Contains(err.Error(), "eval-level provider") {
		t.Fatalf("missing simulator model error: %v", err)
	}
	eval.UserSimulator = UserSimulatorModel{Provider: "simulation", Protocol: "openai", Model: "test"}
	if err := v.ValidateCasesWithEvalDefaults(eval, []*CaseConfig{c}); err != nil {
		t.Fatal(err)
	}
}
