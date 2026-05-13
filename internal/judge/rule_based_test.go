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
