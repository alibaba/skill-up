package judge

import (
	"testing"

	"github.com/alibaba/skill-up/internal/config"
)

func TestNewJudge_RuleBased(t *testing.T) {
	cfg := config.JudgeConfig{
		Type: "rule_based",
		Success: []config.Rule{
			{FilesExist: []string{"main.go"}},
		},
	}
	j, err := NewJudge(cfg, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if j == nil {
		t.Fatal("expected non-nil judge")
	}
	if _, ok := j.(*RuleBasedJudge); !ok {
		t.Fatalf("expected *RuleBasedJudge, got %T", j)
	}
}

func TestNewJudge_Script(t *testing.T) {
	cfg := config.JudgeConfig{
		Type:       "script",
		ScriptPath: "/tmp/test.sh",
	}
	j, err := NewJudge(cfg, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sj, ok := j.(*ScriptJudge)
	if !ok {
		t.Fatalf("expected *ScriptJudge, got %T", j)
	}
	if sj.ScriptPath != "/tmp/test.sh" {
		t.Errorf("expected script path /tmp/test.sh, got %s", sj.ScriptPath)
	}
	if sj.TimeoutSeconds != 0 {
		t.Errorf("expected default timeout 0, got %d", sj.TimeoutSeconds)
	}
}

func TestNewJudge_ScriptWithTimeout(t *testing.T) {
	cfg := config.JudgeConfig{
		Type:           "script",
		ScriptPath:     "/tmp/test.sh",
		TimeoutSeconds: intPtr(45),
	}
	j, err := NewJudge(cfg, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sj, ok := j.(*ScriptJudge)
	if !ok {
		t.Fatalf("expected *ScriptJudge, got %T", j)
	}
	if sj.TimeoutSeconds != 45 {
		t.Errorf("expected timeout 45, got %d", sj.TimeoutSeconds)
	}
}

func TestNewJudge_ScriptMissingPath(t *testing.T) {
	cfg := config.JudgeConfig{Type: "script"}
	_, err := NewJudge(cfg, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing script_path")
	}
}

func TestNewJudge_AgentJudge(t *testing.T) {
	cfg := config.JudgeConfig{
		Type:     "agent_judge",
		Model:    "test-model",
		Criteria: []string{"criterion1"},
	}
	// Need a mock Agent
	ag := &mockJudgeTestAgent{}
	rt := &mockJudgeTestRuntime{}
	j, err := NewJudge(cfg, ag, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aj, ok := j.(*AgentJudge)
	if !ok {
		t.Fatalf("expected *AgentJudge, got %T", j)
	}
	if aj.Model != "test-model" {
		t.Errorf("expected model test-model, got %s", aj.Model)
	}
	if aj.PassThreshold != DefaultPassThreshold {
		t.Errorf("expected default threshold %f, got %f", DefaultPassThreshold, aj.PassThreshold)
	}
}

func TestNewJudge_AgentJudge_PropagatesJudgeSkills(t *testing.T) {
	cfg := config.JudgeConfig{
		Type:     "agent_judge",
		Model:    "test-model",
		Criteria: []string{"c1"},
		Skills:   []config.SkillRef{{Source: "local_path", Path: "evals/fixtures/judge-skill", Target: "~/.claude/skills/judge-skill"}},
	}
	j, err := NewJudge(cfg, &mockJudgeTestAgent{}, &mockJudgeTestRuntime{})
	if err != nil {
		t.Fatalf("NewJudge() error = %v", err)
	}
	aj, ok := j.(*AgentJudge)
	if !ok {
		t.Fatalf("expected *AgentJudge, got %T", j)
	}
	if len(aj.JudgeSkills) != 1 || aj.JudgeSkills[0].Path != "evals/fixtures/judge-skill" {
		t.Fatalf("JudgeSkills = %#v", aj.JudgeSkills)
	}
}

func TestNewJudge_AgentJudge_CustomThreshold(t *testing.T) {
	threshold := 0.9
	cfg := config.JudgeConfig{
		Type:          "agent_judge",
		Model:         "test-model",
		Criteria:      []string{"c1"},
		PassThreshold: &threshold,
	}
	j, err := NewJudge(cfg, &mockJudgeTestAgent{}, &mockJudgeTestRuntime{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aj, ok := j.(*AgentJudge)
	if !ok {
		t.Fatal("expected *AgentJudge type")
	}
	if aj.PassThreshold != 0.9 {
		t.Errorf("expected threshold 0.9, got %f", aj.PassThreshold)
	}
}

// Regression: judge.timeout_seconds must reach AgentJudge through the factory.
// Was silently dropped at one point when the parameter list was rewritten in
// bulk and the factory call site got a hardcoded 0.
func TestNewJudge_AgentJudge_PropagatesTimeoutSeconds(t *testing.T) {
	cfg := config.JudgeConfig{
		Type:           "agent_judge",
		Model:          "test-model",
		Criteria:       []string{"c1"},
		TimeoutSeconds: intPtr(45),
	}
	j, err := NewJudge(cfg, &mockJudgeTestAgent{}, &mockJudgeTestRuntime{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aj, ok := j.(*AgentJudge)
	if !ok {
		t.Fatalf("expected *AgentJudge, got %T", j)
	}
	if aj.TimeoutSeconds != 45 {
		t.Errorf("expected TimeoutSeconds 45 from cfg, got %d", aj.TimeoutSeconds)
	}
}

func TestNewJudge_AgentJudge_NilTimeoutSecondsDefaultsToZero(t *testing.T) {
	cfg := config.JudgeConfig{
		Type:     "agent_judge",
		Model:    "test-model",
		Criteria: []string{"c1"},
	}
	j, err := NewJudge(cfg, &mockJudgeTestAgent{}, &mockJudgeTestRuntime{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aj, ok := j.(*AgentJudge)
	if !ok {
		t.Fatalf("expected *AgentJudge, got %T", j)
	}
	if aj.TimeoutSeconds != 0 {
		t.Errorf("expected TimeoutSeconds 0 when unset, got %d", aj.TimeoutSeconds)
	}
}

func TestNewJudge_AgentJudge_NilAgent(t *testing.T) {
	cfg := config.JudgeConfig{Type: "agent_judge"}
	_, err := NewJudge(cfg, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil Agent")
	}
}

func TestNewJudge_Empty(t *testing.T) {
	cfg := config.JudgeConfig{}
	j, err := NewJudge(cfg, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if j != nil {
		t.Fatalf("expected nil judge for empty type, got %T", j)
	}
}

func TestNewJudge_Unknown(t *testing.T) {
	cfg := config.JudgeConfig{Type: "unknown"}
	_, err := NewJudge(cfg, nil, nil)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestMergeJudgeConfig_CaseOverridesGlobal(t *testing.T) {
	globalThreshold := 0.7
	global := config.JudgeConfig{
		Type:           "agent_judge",
		Model:          "global-model",
		PassThreshold:  &globalThreshold,
		Criteria:       []string{"global-c"},
		Skills:         []config.SkillRef{{Source: "local_path", Path: "evals/fixtures/global-judge"}},
		TimeoutSeconds: intPtr(60),
	}
	caseLevel := config.JudgeConfig{
		Type:     "rule_based",
		Criteria: []string{"case-c"},
	}
	merged := MergeJudgeConfig(global, caseLevel)
	if merged.Type != "rule_based" {
		t.Errorf("expected type rule_based, got %s", merged.Type)
	}
	if merged.Model != "global-model" {
		t.Errorf("expected model global-model, got %s", merged.Model)
	}
	if merged.PassThreshold == nil || *merged.PassThreshold != 0.7 {
		t.Errorf("expected threshold 0.7, got %v", merged.PassThreshold)
	}
	if merged.TimeoutSeconds == nil || *merged.TimeoutSeconds != 60 {
		t.Errorf("expected timeout 60 from global, got %v", merged.TimeoutSeconds)
	}
	if len(merged.Skills) != 0 {
		t.Errorf("expected case override to drop global skills, got %#v", merged.Skills)
	}
}

func TestMergeJudgeConfig_CaseAgentJudgeOverridesSkills(t *testing.T) {
	global := config.JudgeConfig{
		Type:     "agent_judge",
		Model:    "global-model",
		Criteria: []string{"global-c"},
		Skills:   []config.SkillRef{{Source: "local_path", Path: "evals/fixtures/global-judge"}},
	}
	caseLevel := config.JudgeConfig{
		Type:     "agent_judge",
		Criteria: []string{"case-c"},
		Skills:   []config.SkillRef{{Source: "local_path", Path: "evals/fixtures/case-judge"}},
	}

	merged := MergeJudgeConfig(global, caseLevel)
	if merged.Model != "global-model" {
		t.Fatalf("expected inherited model, got %q", merged.Model)
	}
	if len(merged.Skills) != 1 || merged.Skills[0].Path != "evals/fixtures/case-judge" {
		t.Fatalf("expected case skills only, got %#v", merged.Skills)
	}
}

func TestMergeJudgeConfig_CaseTimeoutOverridesGlobal(t *testing.T) {
	global := config.JudgeConfig{
		Type:           "script",
		ScriptPath:     "/tmp/global.sh",
		TimeoutSeconds: intPtr(60),
	}
	caseLevel := config.JudgeConfig{
		Type:           "script",
		ScriptPath:     "/tmp/case.sh",
		TimeoutSeconds: intPtr(120),
	}
	merged := MergeJudgeConfig(global, caseLevel)
	if merged.TimeoutSeconds == nil || *merged.TimeoutSeconds != 120 {
		t.Errorf("expected case-level timeout 120, got %v", merged.TimeoutSeconds)
	}
}

func TestMergeJudgeConfig_NoCaseLevel(t *testing.T) {
	global := config.JudgeConfig{
		Type:  "rule_based",
		Model: "global-model",
	}
	caseLevel := config.JudgeConfig{}
	merged := MergeJudgeConfig(global, caseLevel)
	if merged.Type != "rule_based" {
		t.Errorf("expected type rule_based, got %s", merged.Type)
	}
}
