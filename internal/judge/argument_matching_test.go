package judge

import (
	"context"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/pkg/transcript"
)

func TestArgsMatch_StructuredValues(t *testing.T) {
	tests := []struct {
		name     string
		expected any
		actual   any
		want     bool
	}{
		{"split filename", []any{"sales 2025.csv"}, []any{"sales", "2025.csv"}, false},
		{"shifted list boundary", []any{"a b", "c"}, []any{"a", "b c"}, false},
		{"list versus string", []any{"sales.csv"}, "[sales.csv]", false},
		{"map entry boundary", map[string]any{"file": "a mode:b"}, map[string]any{"file": "a", "mode": "b"}, false},
		{"boolean versus string", true, "true", false},
		{"number versus string", 1, "1", false},
		{"numeric YAML and JSON types", 1, float64(1), true},
		{"equal lists", []string{"sales 2025.csv"}, []any{"sales 2025.csv"}, true},
		{"equal nested values", map[string]any{"limits": []any{1, 2}}, map[string]any{"limits": []any{float64(1), float64(2)}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := argsMatch(map[string]any{"value": tt.expected}, map[string]any{"value": tt.actual, "extra": "ignored"})
			if got != tt.want {
				t.Fatalf("argsMatch(%#v, %#v) = %t, want %t", tt.expected, tt.actual, got, tt.want)
			}
		})
	}
}

func TestRuleBased_StructuredToolArguments(t *testing.T) {
	for _, perTurn := range []bool{false, true} {
		name := "tool_called"
		if perTurn {
			name = "tool_called_in_turn"
		}
		t.Run(name, func(t *testing.T) {
			rule := config.Rule{ToolCalled: &config.ToolCalledRule{
				Name: "read_files", Args: map[string]any{"paths": []any{"sales 2025.csv"}},
			}}
			if perTurn {
				rule = config.Rule{ToolCalledInTurn: &config.ToolCalledInTurnRule{
					Turn: 1, Name: "read_files", Args: map[string]any{"paths": []any{"sales 2025.csv"}},
				}}
			}
			tr := transcript.Transcript{{Role: transcript.RoleToolCall, ToolCall: &transcript.ToolCallInfo{
				Name: "read_files", Arguments: map[string]any{"paths": []any{"sales", "2025.csv"}},
			}}}
			j := NewRuleBasedJudge(config.JudgeConfig{Success: []config.Rule{rule}})
			result, err := j.Evaluate(context.Background(), Input{
				Transcript:  tr,
				TurnResults: []InputTurnResult{{TurnNumber: 1, Status: "completed", Transcript: tr}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != StatusFail || result.Summary.PassRate != 0 {
				t.Fatalf("wrong filenames scored %s at %v, want FAIL at 0", result.Status, result.Summary.PassRate)
			}
		})
	}
}
