package judge

import (
	"context"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// ---------------------------------------------------------------------------
// output_contains
// ---------------------------------------------------------------------------

func TestRuleBased_OutputContains_All_Pass(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			OutputContains: &config.OutputContainsRule{All: []string{"null", "bug"}},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "found null pointer bug"})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

func TestRuleBased_OutputContains_All_Fail(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			OutputContains: &config.OutputContainsRule{All: []string{"null", "missing"}},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "found null pointer"})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

func TestRuleBased_OutputContains_Any_Pass(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			OutputContains: &config.OutputContainsRule{Any: []string{"error", "bug"}},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "found a bug"})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

func TestRuleBased_OutputContains_Any_Fail(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			OutputContains: &config.OutputContainsRule{Any: []string{"error", "bug"}},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "everything is fine"})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

func TestRuleBased_OutputContains_Not_Pass(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			OutputContains: &config.OutputContainsRule{Not: []string{"LGTM"}},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "found issues"})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

func TestRuleBased_OutputContains_Not_Fail(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			OutputContains: &config.OutputContainsRule{Not: []string{"LGTM"}},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "LGTM, no issues"})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

func TestRuleBased_OutputContains_Combined(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			OutputContains: &config.OutputContainsRule{
				All: []string{"null", "bug"},
				Not: []string{"LGTM"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "null pointer bug detected"})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

// ---------------------------------------------------------------------------
// exit_code
// ---------------------------------------------------------------------------

func TestRuleBased_ExitCode_Pass(t *testing.T) {
	ec := 0
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{ExitCode: &ec}},
	})
	r, err := j.Evaluate(context.Background(), Input{ExitCode: 0})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

func TestRuleBased_ExitCode_Fail(t *testing.T) {
	ec := 0
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{ExitCode: &ec}},
	})
	r, err := j.Evaluate(context.Background(), Input{ExitCode: 1})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

// ---------------------------------------------------------------------------
// tool_called
// ---------------------------------------------------------------------------

func TestRuleBased_ToolCalled_NameOnly_Pass(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			ToolCalled: &config.ToolCalledRule{Name: "github::create_pull_request"},
		}},
	})
	in := Input{
		Transcript: transcript.Transcript{
			{Role: transcript.RoleToolCall, ToolCall: &transcript.ToolCallInfo{
				Name: "github::create_pull_request",
			}},
		},
	}
	r, err := j.Evaluate(context.Background(), in)
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

func TestRuleBased_ToolCalled_NameOnly_Fail(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			ToolCalled: &config.ToolCalledRule{Name: "github::create_pull_request"},
		}},
	})
	in := Input{
		Transcript: transcript.Transcript{
			{Role: transcript.RoleToolCall, ToolCall: &transcript.ToolCallInfo{
				Name: "github::list_issues",
			}},
		},
	}
	r, err := j.Evaluate(context.Background(), in)
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

func TestRuleBased_ToolCalled_WithArgs_Pass(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			ToolCalled: &config.ToolCalledRule{
				Name: "project-mgmt::create_publish_plan_simple",
				Args: map[string]any{
					"name":            "Q1-major-release",
					"planReleaseDate": "2026-04-03",
				},
			},
		}},
	})
	in := Input{
		Transcript: transcript.Transcript{
			{Role: transcript.RoleToolCall, ToolCall: &transcript.ToolCallInfo{
				Name: "project-mgmt::create_publish_plan_simple",
				Arguments: map[string]any{
					"name":            "Q1-major-release",
					"planReleaseDate": "2026-04-03",
					"extraField":      "ignored",
				},
			}},
		},
	}
	r, err := j.Evaluate(context.Background(), in)
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

func TestRuleBased_ToolCalled_WithArgs_Fail_WrongValue(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			ToolCalled: &config.ToolCalledRule{
				Name: "project-mgmt::create_publish_plan_simple",
				Args: map[string]any{"name": "Q1-major-release"},
			},
		}},
	})
	in := Input{
		Transcript: transcript.Transcript{
			{Role: transcript.RoleToolCall, ToolCall: &transcript.ToolCallInfo{
				Name:      "project-mgmt::create_publish_plan_simple",
				Arguments: map[string]any{"name": "wrong_name"},
			}},
		},
	}
	r, err := j.Evaluate(context.Background(), in)
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

func TestRuleBased_ToolCalled_EmptyTranscript(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			ToolCalled: &config.ToolCalledRule{Name: "any_tool"},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

// ---------------------------------------------------------------------------
// output_contains not (replaces removed turn_response_contains/not_contains)
// ---------------------------------------------------------------------------

func TestRuleBased_TurnResponseContains_All_Pass(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			OutputContains: &config.OutputContainsRule{
				All: []string{"must be completed", "Research"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "Sorry, the Research phase must be completed first"})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

func TestRuleBased_TurnResponseContains_Any_Pass(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			OutputContains: &config.OutputContainsRule{
				Any: []string{"cannot skip", "must follow order", "must be completed"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "The previous step must be completed first"})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

func TestRuleBased_TurnResponseContains_Fail(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			OutputContains: &config.OutputContainsRule{
				Any: []string{"cannot skip"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "Sure, starting to write code"})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

// ---------------------------------------------------------------------------
// output_contains not (replaces removed turn_response_not_contains)
// ---------------------------------------------------------------------------

func TestRuleBased_TurnResponseNotContains_Pass(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			OutputContains: &config.OutputContainsRule{
				Not: []string{"```python", "create file"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "The Research phase must be completed first"})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

func TestRuleBased_TurnResponseNotContains_Fail(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			OutputContains: &config.OutputContainsRule{
				Not: []string{"```python", "create file"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "Sure, create file main.py"})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

// ---------------------------------------------------------------------------
// failure rules priority
// ---------------------------------------------------------------------------

func TestRuleBased_FailureRules_TakePriority(t *testing.T) {
	// Even if success rules would pass, a matching failure rule overrides.
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			OutputContains: &config.OutputContainsRule{All: []string{"review"}},
		}},
		Failure: []config.Rule{{
			OutputContains: &config.OutputContainsRule{Any: []string{"LGTM", "no changes needed"}},
		}},
	})
	// Message contains both "review" (success) and "LGTM" (failure).
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "LGTM, review passed"})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

func TestRuleBased_FailureRules_NoMatch_SuccessEvaluated(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			OutputContains: &config.OutputContainsRule{All: []string{"bug"}},
		}},
		Failure: []config.Rule{{
			OutputContains: &config.OutputContainsRule{Any: []string{"LGTM"}},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "found a bug"})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

// ---------------------------------------------------------------------------
// multiple success rules (AND logic)
// ---------------------------------------------------------------------------

func TestRuleBased_MultipleSuccess_AllPass(t *testing.T) {
	ec := 0
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{
			{OutputContains: &config.OutputContainsRule{All: []string{"null", "bug"}}},
			{ExitCode: &ec},
		},
	})
	r, err := j.Evaluate(context.Background(), Input{
		FinalMessage: "null pointer bug",
		ExitCode:     0,
	})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
	if r.Summary.Total != 2 {
		t.Fatalf("expected 2 assertions, got %d", r.Summary.Total)
	}
}

func TestRuleBased_MultipleSuccess_OneFails(t *testing.T) {
	ec := 0
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{
			{OutputContains: &config.OutputContainsRule{All: []string{"null"}}},
			{ExitCode: &ec},
		},
	})
	r, err := j.Evaluate(context.Background(), Input{
		FinalMessage: "null pointer",
		ExitCode:     1,
	})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
	if r.Summary.Passed != 1 || r.Summary.Failed != 1 {
		t.Fatalf("expected 1 passed 1 failed, got passed=%d failed=%d", r.Summary.Passed, r.Summary.Failed)
	}
}

// ---------------------------------------------------------------------------
// empty rules
// ---------------------------------------------------------------------------

func TestRuleBased_NoRules_DefaultsToPass(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{})
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "anything"})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

// ---------------------------------------------------------------------------
// argsMatch helper
// ---------------------------------------------------------------------------

func TestArgsMatch_PartialMatch(t *testing.T) {
	expected := map[string]any{"name": "test"}
	actual := map[string]any{"name": "test", "extra": "ignored"}
	if !argsMatch(expected, actual) {
		t.Fatal("expected partial match to succeed")
	}
}

func TestArgsMatch_MissingKey(t *testing.T) {
	expected := map[string]any{"name": "test", "missing": "value"}
	actual := map[string]any{"name": "test"}
	if argsMatch(expected, actual) {
		t.Fatal("expected match to fail when key is missing")
	}
}

func TestArgsMatch_WrongValue(t *testing.T) {
	expected := map[string]any{"name": "test"}
	actual := map[string]any{"name": "other"}
	if argsMatch(expected, actual) {
		t.Fatal("expected match to fail when value differs")
	}
}

func TestArgsMatch_Empty(t *testing.T) {
	if !argsMatch(map[string]any{}, map[string]any{"any": "value"}) {
		t.Fatal("empty expected should always match")
	}
}

// ---------------------------------------------------------------------------
// turn_response_contains
// ---------------------------------------------------------------------------

func TestRuleBased_TurnResponseContains_ContainsAll_Pass(t *testing.T) {
	turns := []InputTurnResult{
		{TurnNumber: 1, Response: "hello world", Status: "completed"},
		{TurnNumber: 2, Response: "I created file.go and test_file.go", Status: "completed"},
	}
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			TurnResponseContains: &config.TurnResponseContainsRule{
				Turn:        2,
				ContainsAll: []string{"file.go", "test_file.go"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{TurnResults: turns, TurnsTotal: 2, TurnsExecuted: 2})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

func TestRuleBased_TurnResponseContains_ContainsAll_Fail(t *testing.T) {
	turns := []InputTurnResult{
		{TurnNumber: 1, Response: "I created file.go only", Status: "completed"},
	}
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			TurnResponseContains: &config.TurnResponseContainsRule{
				Turn:        1,
				ContainsAll: []string{"file.go", "test_file.go"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{TurnResults: turns, TurnsTotal: 1, TurnsExecuted: 1})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
	if r.AssertionResults[0].Evidence == "" {
		t.Fatal("expected evidence with missing keywords")
	}
}

func TestRuleBased_TurnResponseContains_ContainsAny_Pass(t *testing.T) {
	turns := []InputTurnResult{
		{TurnNumber: 1, Response: "result is success", Status: "completed"},
	}
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			TurnResponseContains: &config.TurnResponseContainsRule{
				Turn:        1,
				ContainsAny: []string{"success", "ok"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{TurnResults: turns, TurnsTotal: 1, TurnsExecuted: 1})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

func TestRuleBased_TurnResponseContains_ContainsAny_Fail(t *testing.T) {
	turns := []InputTurnResult{
		{TurnNumber: 1, Response: "something else", Status: "completed"},
	}
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			TurnResponseContains: &config.TurnResponseContainsRule{
				Turn:        1,
				ContainsAny: []string{"success", "ok"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{TurnResults: turns, TurnsTotal: 1, TurnsExecuted: 1})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

func TestRuleBased_TurnResponseContains_MissingTurn(t *testing.T) {
	turns := []InputTurnResult{
		{TurnNumber: 1, Response: "only one turn", Status: "completed"},
	}
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			TurnResponseContains: &config.TurnResponseContainsRule{
				Turn:        3,
				ContainsAll: []string{"something"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{TurnResults: turns, TurnsTotal: 3, TurnsExecuted: 1})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
	if r.AssertionResults[0].Evidence == "" {
		t.Fatal("expected evidence about missing turn")
	}
}

func TestRuleBased_TurnResponseContains_UsesTurnNumber(t *testing.T) {
	turns := []InputTurnResult{
		{TurnNumber: 2, Response: "target turn response", Status: "completed"},
		{TurnNumber: 1, Response: "first turn response", Status: "completed"},
	}
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			TurnResponseContains: &config.TurnResponseContainsRule{
				Turn:        2,
				ContainsAll: []string{"target"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{TurnResults: turns, TurnsTotal: 2, TurnsExecuted: 2})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

func TestRuleBased_TurnResponseContains_SkippedTurn(t *testing.T) {
	turns := []InputTurnResult{
		{TurnNumber: 1, Response: "hello", Status: "completed"},
		{TurnNumber: 2, Response: "", Status: "skipped", Reason: "post_condition skip_remaining"},
	}
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			TurnResponseContains: &config.TurnResponseContainsRule{
				Turn:        2,
				ContainsAll: []string{"hello"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{TurnResults: turns, TurnsTotal: 2, TurnsExecuted: 2})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
	if r.AssertionResults[0].Evidence == "" {
		t.Fatal("expected evidence about skipped turn")
	}
}

// ---------------------------------------------------------------------------
// turn_response_not_contains
// ---------------------------------------------------------------------------

func TestRuleBased_TurnResponseNotContains_PerTurn_Pass(t *testing.T) {
	turns := []InputTurnResult{
		{TurnNumber: 1, Response: "clean output", Status: "completed"},
	}
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			TurnResponseNotContains: &config.TurnResponseNotContainsRule{
				Turn:        1,
				NotContains: []string{"error", "panic"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{TurnResults: turns, TurnsTotal: 1, TurnsExecuted: 1})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

func TestRuleBased_TurnResponseNotContains_PerTurn_Fail(t *testing.T) {
	turns := []InputTurnResult{
		{TurnNumber: 1, Response: "runtime error occurred", Status: "completed"},
	}
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			TurnResponseNotContains: &config.TurnResponseNotContainsRule{
				Turn:        1,
				NotContains: []string{"error", "panic"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{TurnResults: turns, TurnsTotal: 1, TurnsExecuted: 1})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

func TestRuleBased_TurnResponseNotContains_MissingTurn(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			TurnResponseNotContains: &config.TurnResponseNotContainsRule{
				Turn:        1,
				NotContains: []string{"error"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{TurnResults: nil, TurnsTotal: 1, TurnsExecuted: 0})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

// ---------------------------------------------------------------------------
// tool_called_in_turn
// ---------------------------------------------------------------------------

func TestRuleBased_ToolCalledInTurn_Pass(t *testing.T) {
	turns := []InputTurnResult{
		{
			TurnNumber: 1,
			Response:   "done",
			Status:     "completed",
			Transcript: transcript.Transcript{
				{Role: "tool_call", ToolCall: &transcript.ToolCallInfo{Name: "write_file", Arguments: map[string]any{"path": "main.go"}}},
			},
		},
	}
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			ToolCalledInTurn: &config.ToolCalledInTurnRule{
				Turn: 1,
				Name: "write_file",
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{TurnResults: turns, TurnsTotal: 1, TurnsExecuted: 1})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

func TestRuleBased_ToolCalledInTurn_WithArgs_Pass(t *testing.T) {
	turns := []InputTurnResult{
		{
			TurnNumber: 1,
			Response:   "done",
			Status:     "completed",
			Transcript: transcript.Transcript{
				{Role: "tool_call", ToolCall: &transcript.ToolCallInfo{Name: "write_file", Arguments: map[string]any{"path": "main.go", "content": "pkg"}}},
			},
		},
	}
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			ToolCalledInTurn: &config.ToolCalledInTurnRule{
				Turn: 1,
				Name: "write_file",
				Args: map[string]any{"path": "main.go"},
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{TurnResults: turns, TurnsTotal: 1, TurnsExecuted: 1})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

func TestRuleBased_ToolCalledInTurn_Fail(t *testing.T) {
	turns := []InputTurnResult{
		{TurnNumber: 1, Response: "done", Status: "completed", Transcript: transcript.Transcript{
			{Role: "tool_call", ToolCall: &transcript.ToolCallInfo{Name: "read_file"}},
		}},
	}
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			ToolCalledInTurn: &config.ToolCalledInTurnRule{Turn: 1, Name: "write_file"},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{TurnResults: turns, TurnsTotal: 1, TurnsExecuted: 1})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

func TestRuleBased_ToolCalledInTurn_FailedTurn(t *testing.T) {
	turns := []InputTurnResult{
		{TurnNumber: 1, Response: "", Status: "failed", Reason: "post_condition failed"},
	}
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			ToolCalledInTurn: &config.ToolCalledInTurnRule{
				Turn: 1,
				Name: "write_file",
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{TurnResults: turns, TurnsTotal: 1, TurnsExecuted: 1})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

// ---------------------------------------------------------------------------
// tool_not_called_in_turn
// ---------------------------------------------------------------------------

func TestRuleBased_ToolNotCalledInTurn_PassAndFail(t *testing.T) {
	tests := []struct {
		name       string
		toolName   string
		ruleName   string
		wantStatus Status
	}{
		{"pass_tool_absent", "read_file", "delete_file", StatusPass},
		{"fail_tool_present", "delete_file", "delete_file", StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			turns := []InputTurnResult{
				{TurnNumber: 1, Response: "done", Status: "completed", Transcript: transcript.Transcript{
					{Role: "tool_call", ToolCall: &transcript.ToolCallInfo{Name: tt.toolName}},
					{Role: "assistant", Content: "result"},
				}},
			}
			j := NewRuleBasedJudge(config.JudgeConfig{
				Success: []config.Rule{{
					ToolNotCalledInTurn: &config.ToolNotCalledInTurnRule{Turn: 1, Name: tt.ruleName},
				}},
			})
			r, err := j.Evaluate(context.Background(), Input{TurnResults: turns, TurnsTotal: 1, TurnsExecuted: 1})
			assertNoError(t, err)
			assertStatus(t, r, tt.wantStatus)
		})
	}
}

func TestRuleBased_ToolNotCalledInTurn_MissingTurn(t *testing.T) {
	j := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{
			ToolNotCalledInTurn: &config.ToolNotCalledInTurnRule{
				Turn: 5,
				Name: "delete_file",
			},
		}},
	})
	r, err := j.Evaluate(context.Background(), Input{TurnResults: nil, TurnsTotal: 5, TurnsExecuted: 0})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}
