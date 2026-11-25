package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/alibaba/skill-up/internal/logging"
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

func TestCaseJudgeWithoutTypeWarnsAndInherits(t *testing.T) {
	evalTimeout := 60
	evalJudge := JudgeConfig{Type: "rule_based", TimeoutSeconds: &evalTimeout, Success: []Rule{{OutputContains: &OutputContainsRule{All: []string{"eval rule"}}}}}
	for _, test := range []struct {
		name     string
		caseDoc  string
		wantWarn bool
		wantRule string
	}{
		{name: "rules without type", caseDoc: "judge:\n  success:\n    - output_contains:\n        all: [case rule]\n", wantWarn: true, wantRule: "eval rule"},
		{name: "timeout only", caseDoc: "judge:\n  timeout_seconds: 30\n", wantWarn: true, wantRule: "eval rule"},
		{name: "zero timeout", caseDoc: "judge: {timeout_seconds: 0}\n", wantWarn: true, wantRule: "eval rule"},
		{name: "empty field", caseDoc: "judge: {model: ''}\n", wantWarn: true, wantRule: "eval rule"},
		{name: "null field", caseDoc: "judge: {model: null}\n", wantWarn: true, wantRule: "eval rule"},
		{name: "empty block", caseDoc: "judge:\n", wantRule: "eval rule"},
		{name: "empty map", caseDoc: "judge: {}\n", wantRule: "eval rule"},
		{name: "null block", caseDoc: "judge: null\n", wantRule: "eval rule"},
		{name: "merged block", caseDoc: "<<: {judge: {timeout_seconds: 30}}\n", wantWarn: true, wantRule: "eval rule"},
		{name: "merged judge fields", caseDoc: "judge: {<<: {timeout_seconds: 30}}\n", wantWarn: true, wantRule: "eval rule"},
		{name: "typed block", caseDoc: "judge:\n  type: rule_based\n  success:\n    - output_contains:\n        all: [case rule]\n", wantRule: "case rule"},
		{name: "no block inherits the eval judge", wantRule: "eval rule"},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			var logs bytes.Buffer
			restore := logging.SetOutputForTest(&logs)
			t.Cleanup(restore)
			err = NewValidator().ValidateCasesWithEvalDefaults(eval, []*CaseConfig{caseCfg})
			if err != nil {
				t.Fatalf("validate error = %v", err)
			}
			wantCount := 0
			if test.wantWarn {
				wantCount = 1
			}
			if got := strings.Count(logs.String(), "case case: judge.type is missing; case judge fields are ignored"); got != wantCount {
				t.Fatalf("warning count = %d, want %d; logs: %s", got, wantCount, logs.String())
			}
			plan, err := ResolveJudgePlan(eval, caseCfg)
			if err != nil {
				t.Fatalf("ResolveJudgePlan error = %v", err)
			}
			if got := plan.Judges[0].Success[0].OutputContains.All[0]; got != test.wantRule {
				t.Fatalf("resolved rule = %q, want %q", got, test.wantRule)
			}
			if got := *plan.Judges[0].TimeoutSeconds; got != evalTimeout {
				t.Fatalf("resolved timeout = %d, want %d", got, evalTimeout)
			}
		})
	}
}

func TestCaseJudgeWithoutTypeKeepsEvalJudgeListValidation(t *testing.T) {
	t.Parallel()
	judges := []JudgeConfig{{ID: "check", Type: "rule_based"}}
	eval := &EvalConfig{Judges: &judges}
	for _, doc := range []string{"judge:\n", "judge: null\n", "judge: {}\n", "judge: {timeout_seconds: 30}\n"} {
		var c CaseConfig
		var err error
		c.JudgeSet, c.JudgesSet, err = decodeConfig([]byte("input: {prompt: task}\n"+doc), &c)
		if err != nil {
			t.Fatal(err)
		}
		if err := NewValidator().ValidateCasesWithEvalDefaults(eval, []*CaseConfig{&c}); err == nil || !strings.Contains(err.Error(), "case judge.type is required when overriding eval judges") {
			t.Fatalf("document %q: error = %v", doc, err)
		}
	}
}

func TestCaseWithoutAnyCheckWarns(t *testing.T) {
	const warning = "case case: no grading checks are configured (judge rules, expect checks or turn post_conditions); the case can pass without checking the agent's output"
	ruleJudge := JudgeConfig{Type: "rule_based", Success: []Rule{{OutputContains: &OutputContainsRule{All: []string{"done"}}}}}
	listJudge := ruleJudge
	listJudge.ID = "rules"
	exitCode := 0
	for _, test := range []struct {
		name     string
		eval     EvalConfig
		caseDoc  string
		wantWarn bool
	}{
		{name: "nothing configured", wantWarn: true},
		{name: "empty judge block", caseDoc: "judge: {}\n", wantWarn: true},
		{name: "empty case expect list", caseDoc: "expect:\n  must_contain: []\n", wantWarn: true},
		{name: "empty default expect list", eval: EvalConfig{Cases: CasesConfig{Defaults: CaseDefaults{Expect: Expect{FilesExist: []string{}}}}}, wantWarn: true},
		{name: "rule_based judge without rules", eval: EvalConfig{Judge: JudgeConfig{Type: "rule_based"}, JudgeSet: true}, wantWarn: true},
		{name: "case rule_based judge without rules", eval: EvalConfig{Judge: ruleJudge, JudgeSet: true}, caseDoc: "judge:\n  type: rule_based\n", wantWarn: true},
		{name: "judges list without rules", eval: EvalConfig{Judges: &[]JudgeConfig{{ID: "rules", Type: "rule_based"}}, JudgesSet: true}, wantWarn: true},
		{name: "failure rule only", eval: EvalConfig{Judge: JudgeConfig{Type: "rule_based", Failure: ruleJudge.Success}, JudgeSet: true}},
		{name: "script judge", eval: EvalConfig{Judge: JudgeConfig{Type: "script", ScriptPath: "evals/judge.sh"}, JudgeSet: true}},
		{name: "eval judge", eval: EvalConfig{Judge: ruleJudge, JudgeSet: true}},
		{name: "eval judges list", eval: EvalConfig{Judges: &[]JudgeConfig{listJudge}, JudgesSet: true}},
		{name: "case judge", caseDoc: "judge:\n  type: rule_based\n  success:\n    - output_contains:\n        all: [done]\n"},
		{name: "case expect", caseDoc: "expect:\n  must_contain: [done]\n"},
		{name: "default expect", eval: EvalConfig{Cases: CasesConfig{Defaults: CaseDefaults{Expect: Expect{ExitCode: &exitCode}}}}},
		{name: "turn post_condition", caseDoc: "input:\n  turns:\n    - role: user\n      content: task\n      post_condition:\n        must_contain_all: [done]\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			evalsDir := filepath.Join(t.TempDir(), "evals")
			if err := os.MkdirAll(evalsDir, 0o755); err != nil {
				t.Fatal(err)
			}
			caseDoc := test.caseDoc
			if !strings.Contains(caseDoc, "input:") {
				caseDoc = "input:\n  prompt: task\n" + caseDoc
			}
			if err := os.WriteFile(filepath.Join(evalsDir, "case.yaml"), []byte(caseDoc), 0o600); err != nil {
				t.Fatal(err)
			}
			caseCfg, err := NewLoader(filepath.Join(evalsDir, "eval.yaml")).LoadCaseConfig("evals/case.yaml")
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			restore := logging.SetOutputForTest(&logs)
			t.Cleanup(restore)
			if err := NewValidator().ValidateCasesWithEvalDefaults(&test.eval, []*CaseConfig{caseCfg}); err != nil {
				t.Fatalf("validate error = %v", err)
			}
			wantCount := 0
			if test.wantWarn {
				wantCount = 1
			}
			if got := strings.Count(logs.String(), warning); got != wantCount {
				t.Fatalf("warning count = %d, want %d; logs: %s", got, wantCount, logs.String())
			}
		})
	}
}
