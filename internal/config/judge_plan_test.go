package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestIndependentJudgeEngineValidation(t *testing.T) {
	t.Parallel()
	engine := &EngineConfig{Name: "claude_code", Model: ModelConfig{Provider: "anthropic", Name: "judge-model"}}
	for _, test := range []struct {
		name    string
		judge   JudgeConfig
		wantErr string
	}{
		{"valid", JudgeConfig{Type: "agent_judge", Engine: engine, Criteria: []string{"quality"}}, ""},
		{"mixed-model", JudgeConfig{Type: "agent_judge", Engine: engine, Model: "old", Criteria: []string{"quality"}}, "mutually exclusive"},
		{"wrong-type", JudgeConfig{Type: "script", Engine: engine, ScriptPath: "check.sh"}, "only supported"},
		{"empty-engine", JudgeConfig{Type: "agent_judge", Engine: &EngineConfig{}, Criteria: []string{"quality"}}, "judge.engine.name is required"},
		{"missing-model", JudgeConfig{Type: "agent_judge", Engine: &EngineConfig{Name: "codex"}, Criteria: []string{"quality"}}, "judge.engine.model.provider"},
		{"custom-missing", JudgeConfig{Type: "agent_judge", Engine: &EngineConfig{Name: "my-judge", Model: engine.Model}, Criteria: []string{"quality"}}, "missing engine.custom"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			caseCfg := &CaseConfig{ID: "case", Input: Input{Prompt: "task"}, Judge: test.judge}
			err := NewValidator().ValidateCasesWithEvalDefaults(&EvalConfig{}, []*CaseConfig{caseCfg})
			if test.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validation=%v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestIndependentJudgeEngineInheritance(t *testing.T) {
	t.Parallel()
	engine := &EngineConfig{Name: "claude_code", Model: ModelConfig{Provider: "anthropic", Name: "judge-model"}}
	global := JudgeConfig{Type: "agent_judge", Engine: engine, Criteria: []string{"quality"}}
	for _, local := range []JudgeConfig{
		{Type: "agent_judge", Criteria: []string{"case quality"}},
		{Type: "agent_judge", Model: "openai/local-model", Criteria: []string{"case quality"}},
		{Type: "agent_judge", Engine: &EngineConfig{Name: "codex", Model: ModelConfig{Provider: "openai", Name: "local-model"}}, Criteria: []string{"case quality"}},
	} {
		plan, err := ResolveJudgePlan(&EvalConfig{Judge: global}, &CaseConfig{Judge: local})
		if err != nil {
			t.Fatal(err)
		}
		merged := plan.Judges[0]
		if local.Model != "" && merged.Engine != nil {
			t.Fatal("legacy model inherited independent engine")
		}
		if local.Model == "" && local.Engine == nil && merged.Engine != engine {
			t.Fatal("missing default engine")
		}
		if local.Engine != nil && merged.Engine != local.Engine {
			t.Fatal("case engine was not replaced")
		}
		if err := NewValidator().ValidateCasesWithEvalDefaults(&EvalConfig{Judge: global}, []*CaseConfig{{Input: Input{Prompt: "task"}, Judge: local}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIndependentJudgeEngineYAML(t *testing.T) {
	t.Parallel()
	var eval EvalConfig
	if err := yaml.Unmarshal([]byte("judge:\n  type: agent_judge\n  engine:\n    name: claude-code\n    version: 1.2.3\n    model:\n      provider: anthropic\n      name: judge-model\n    kwargs:\n      option: value\n  criteria: [quality]\n"), &eval); err != nil {
		t.Fatal(err)
	}
	if eval.Judge.Engine == nil || eval.Judge.Engine.Name != "claude-code" || eval.Judge.Engine.Version != "1.2.3" || eval.Judge.Engine.Kwargs["option"] != "value" {
		t.Fatalf("judge=%+v", eval.Judge)
	}
}

func TestResolveIndependentJudgeEngineEnvironment(t *testing.T) {
	t.Setenv("JUDGE_PROVIDER", "gateway")
	t.Setenv("JUDGE_MODEL", "review-model")
	t.Setenv("JUDGE_COMMAND", "judge-cli")
	t.Setenv("JUDGE_TRANSPORT", "local")
	input := JudgeConfig{Type: "agent_judge", Engine: &EngineConfig{
		Name: "custom-judge", Model: ModelConfig{Provider: "${JUDGE_PROVIDER}", Name: "${JUDGE_MODEL}"},
		Custom: &CustomEngineConfig{Transport: "${JUDGE_TRANSPORT}", Local: &CustomLocalConfig{Command: "${JUDGE_COMMAND}", Args: []string{"${model}"}}},
	}}
	if err := NewValidator().ValidateCasesWithEvalDefaults(&EvalConfig{}, []*CaseConfig{{Input: Input{Prompt: "task"}, Judge: JudgeConfig{Type: input.Type, Engine: input.Engine, Criteria: []string{"quality"}}}}); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveJudgeEngineConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Engine.Model.Provider != "gateway" || resolved.Engine.Model.Name != "review-model" || resolved.Engine.Custom.Local.Command != "judge-cli" || resolved.Engine.Custom.Local.Args[0] != "${model}" {
		t.Fatalf("engine=%+v", resolved.Engine)
	}
	if input.Engine.Model.Name != "${JUDGE_MODEL}" || input.Engine.Custom.Local.Command != "${JUDGE_COMMAND}" {
		t.Fatal("input engine mutated")
	}
}

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
		var err error
		eval.JudgeSet, eval.JudgesSet, err = decodeConfig([]byte(raw), &eval)
		if err != nil {
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

func TestCaseJudgeWithoutTypeIsRejected(t *testing.T) {
	t.Parallel()
	evalJudge := JudgeConfig{Type: "rule_based", Success: []Rule{{OutputContains: &OutputContainsRule{All: []string{"eval rule"}}}}}
	for _, test := range []struct {
		name     string
		caseDoc  string
		wantErr  string
		wantRule string
	}{
		{name: "rules without type", caseDoc: "judge:\n  success:\n    - output_contains:\n        all: [case rule]\n", wantErr: "case judge.type is required"},
		{name: "timeout only", caseDoc: "judge:\n  timeout_seconds: 30\n", wantErr: "case judge.type is required"},
		{name: "empty block", caseDoc: "judge:\n", wantErr: "case judge.type is required"},
		{name: "typed block", caseDoc: "judge:\n  type: rule_based\n  success:\n    - output_contains:\n        all: [case rule]\n", wantRule: "case rule"},
		{name: "no block inherits the eval judge", wantRule: "eval rule"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			evalsDir := filepath.Join(t.TempDir(), "evals")
			if err := os.MkdirAll(evalsDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(evalsDir, "case.yaml"), []byte("input:\n  prompt: task\n"+test.caseDoc), 0o600); err != nil {
				t.Fatal(err)
			}
			caseCfg, err := NewLoader(filepath.Join(evalsDir, "eval.yaml")).LoadCaseConfig("evals/case.yaml")
			if err != nil {
				t.Fatal(err)
			}
			eval := &EvalConfig{Judge: evalJudge, JudgeSet: true}

			err = NewValidator().ValidateCasesWithEvalDefaults(eval, []*CaseConfig{caseCfg})
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validate error = %v", err)
				}
				plan, err := ResolveJudgePlan(eval, caseCfg)
				if err != nil {
					t.Fatalf("ResolveJudgePlan error = %v", err)
				}
				if got := plan.Judges[0].Success[0].OutputContains.All[0]; got != test.wantRule {
					t.Fatalf("resolved rule = %q, want %q", got, test.wantRule)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validate error = %v, want %q", err, test.wantErr)
			}
			if _, err := ResolveJudgePlan(eval, caseCfg); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ResolveJudgePlan error = %v, want %q", err, test.wantErr)
			}
		})
	}
}
