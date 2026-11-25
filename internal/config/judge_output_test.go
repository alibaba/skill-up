package config

import (
	"strings"
	"testing"
)

func TestJudgeOutputValidation(t *testing.T) {
	for _, tt := range []struct {
		name, kind, mode string
		invalid          bool
	}{
		{"default", "agent_judge", "", false},
		{"file", "agent_judge", "file", false},
		{"chat", "agent_judge", "final_message", false},
		{"unknown", "agent_judge", "markdown", true},
		{"script", "script", "file", true},
		{"rule based", "rule_based", "file", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateJudgeTypeAndLocalFields(JudgeConfig{Type: tt.kind, Output: &JudgeOutputConfig{Mode: tt.mode}})
			found := false
			for _, err := range errs {
				if strings.Contains(err, "judge.output") {
					found = true
				}
			}
			if found != tt.invalid {
				t.Fatalf("errors = %v, want output error %v", errs, tt.invalid)
			}
		})
	}
	global := JudgeConfig{Type: "agent_judge", Output: &JudgeOutputConfig{Mode: "file"}}
	inherited := mergeJudgeConfigForValidation(global, JudgeConfig{Type: "agent_judge"})
	if inherited.Output == nil || inherited.Output.Mode != "file" {
		t.Fatal("validation merge lost output mode")
	}
}
