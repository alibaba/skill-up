package evaluator

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// mockResumerAgent implements both agent.Agent and agent.SessionResumer.
type mockResumerAgent struct {
	mockAgent

	runTurnFunc func(ctx context.Context, rt runtime.Runtime, opts agent.ExecOptions, msg transcript.Message, sessionID string) (*agent.SessionResult, error)
	turnCall    int
}

func (m *mockResumerAgent) RunTurn(ctx context.Context, rt runtime.Runtime, opts agent.ExecOptions, msg transcript.Message, sessionID string) (*agent.SessionResult, error) {
	m.turnCall++
	if m.runTurnFunc != nil {
		return m.runTurnFunc(ctx, rt, opts, msg, sessionID)
	}
	return &agent.SessionResult{
		FinalMessage: fmt.Sprintf("response to turn %d", msg.Turn),
		SessionID:    "test-session-123",
		Turns:        1,
	}, nil
}

func TestSubstituteTemplate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		vars    map[string]string
		want    string
		wantErr bool
	}{
		{
			name:    "no_placeholders",
			content: "plain text without variables",
			vars:    map[string]string{"foo": "bar"},
			want:    "plain text without variables",
		},
		{
			name:    "single_substitution",
			content: "The file is {{filename}}",
			vars:    map[string]string{"filename": "test.go"},
			want:    "The file is test.go",
		},
		{
			name:    "multiple_substitutions",
			content: "{{greeting}} {{name}}, welcome to {{place}}",
			vars:    map[string]string{"greeting": "Hello", "name": "World", "place": "Go"},
			want:    "Hello World, welcome to Go",
		},
		{
			name:    "unresolved_variable",
			content: "Use {{undefined_var}} here",
			vars:    map[string]string{"other": "value"},
			wantErr: true,
		},
		{
			name:    "empty_vars_with_placeholder",
			content: "{{missing}}",
			vars:    nil,
			wantErr: true,
		},
		{
			name:    "empty_content",
			content: "",
			vars:    map[string]string{"x": "y"},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := substituteTemplate(tt.content, tt.vars)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got result %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEvaluatePostCondition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pc       *config.PostCondition
		response string
		wantFail bool
	}{
		{
			name:     "nil_post_condition",
			pc:       nil,
			response: "anything",
			wantFail: false,
		},
		{
			name:     "must_contain_all_pass",
			pc:       &config.PostCondition{MustContainAll: []string{"foo", "bar"}},
			response: "foo and bar are here",
			wantFail: false,
		},
		{
			name:     "must_contain_all_fail",
			pc:       &config.PostCondition{MustContainAll: []string{"foo", "missing"}},
			response: "only foo is here",
			wantFail: true,
		},
		{
			name:     "must_contain_any_pass",
			pc:       &config.PostCondition{MustContainAny: []string{"alpha", "beta"}},
			response: "beta is present",
			wantFail: false,
		},
		{
			name:     "must_contain_any_fail",
			pc:       &config.PostCondition{MustContainAny: []string{"alpha", "beta"}},
			response: "neither is present",
			wantFail: true,
		},
		{
			name:     "must_not_contain_pass",
			pc:       &config.PostCondition{MustNotContain: []string{"error", "fail"}},
			response: "everything is fine",
			wantFail: false,
		},
		{
			name:     "must_not_contain_fail",
			pc:       &config.PostCondition{MustNotContain: []string{"error", "fail"}},
			response: "there was an error",
			wantFail: true,
		},
		{
			name: "combined_rules_pass",
			pc: &config.PostCondition{
				MustContainAll: []string{"success"},
				MustNotContain: []string{"error"},
			},
			response: "operation success",
			wantFail: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reason := evaluatePostCondition(tt.pc, tt.response)
			if tt.wantFail && reason == "" {
				t.Fatal("expected failure reason, got empty")
			}
			if !tt.wantFail && reason != "" {
				t.Fatalf("unexpected failure: %s", reason)
			}
		})
	}
}

func TestCaptureVariables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rules    []config.CaptureRule
		response string
		want     map[string]string
		wantErr  bool
	}{
		{
			name:     "named_group_value",
			rules:    []config.CaptureRule{{Variable: "id", Pattern: `ID: (?P<value>\d+)`}},
			response: "Your ID: 42 is confirmed",
			want:     map[string]string{"id": "42"},
		},
		{
			name:     "first_capture_group",
			rules:    []config.CaptureRule{{Variable: "version", Pattern: `v(\d+\.\d+\.\d+)`}},
			response: "Version is v1.2.3",
			want:     map[string]string{"version": "1.2.3"},
		},
		{
			name:     "multiple_captures",
			rules:    []config.CaptureRule{{Variable: "a", Pattern: `a=(\w+)`}, {Variable: "b", Pattern: `b=(\w+)`}},
			response: "a=hello b=world",
			want:     map[string]string{"a": "hello", "b": "world"},
		},
		{
			name:     "no_match",
			rules:    []config.CaptureRule{{Variable: "x", Pattern: `xyz(\d+)`}},
			response: "no numbers here",
			wantErr:  true,
		},
		{
			name:     "empty_pattern",
			rules:    []config.CaptureRule{{Variable: "x", Pattern: ""}},
			response: "anything",
			wantErr:  true,
		},
		{
			name:     "invalid_regex",
			rules:    []config.CaptureRule{{Variable: "x", Pattern: `(?P<`}},
			response: "anything",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := captureVariables(tt.rules, tt.response)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Fatalf("variable %q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestMultiTurnStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		results []TurnResult
		want    judge.Status
	}{
		{
			name:    "all_completed",
			results: []TurnResult{{Status: TurnCompleted}, {Status: TurnCompleted}},
			want:    "", // proceed to judge
		},
		{
			name:    "one_error",
			results: []TurnResult{{Status: TurnCompleted}, {Status: TurnError}},
			want:    judge.StatusError,
		},
		{
			name:    "one_failed",
			results: []TurnResult{{Status: TurnCompleted}, {Status: TurnFailed}},
			want:    judge.StatusFail,
		},
		{
			name:    "completed_then_skipped",
			results: []TurnResult{{Status: TurnCompleted}, {Status: TurnSkipped}},
			want:    "", // has completed turns, proceed to judge
		},
		{
			name:    "all_skipped",
			results: []TurnResult{{Status: TurnSkipped}, {Status: TurnSkipped}},
			want:    judge.StatusSkip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := multiTurnStatus(tt.results)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExecuteMultiTurn_HappyPath(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	rt := &mockRuntime{workspace: workspace}

	turnResponses := []string{"first response", "second response", "third response"}
	turnIndex := 0

	ag := &mockResumerAgent{
		mockAgent: mockAgent{name: "test-resumer"},
		runTurnFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, msg transcript.Message, sessionID string) (*agent.SessionResult, error) {
			idx := turnIndex
			turnIndex++
			return &agent.SessionResult{
				FinalMessage: turnResponses[idx],
				SessionID:    "session-abc",
				Turns:        1,
				InputTokens:  10,
				OutputTokens: 20,
			}, nil
		},
	}

	caseCfg := &config.CaseConfig{
		ID: "happy-multi",
		Input: config.Input{
			Turns: []config.Turn{
				{Role: "user", Content: "first message"},
				{Role: "user", Content: "second message"},
				{Role: "user", Content: "third message"},
			},
		},
	}

	e := newTestEvaluator(EvalOptions{Agent: ag, EvalCfg: &config.EvalConfig{}})
	turnResults, aggResult, err := e.executeMultiTurn(context.Background(), rt, caseCfg, ag, agent.ExecOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(turnResults) != 3 {
		t.Fatalf("got %d turn results, want 3", len(turnResults))
	}
	for i, tr := range turnResults {
		if tr.Status != TurnCompleted {
			t.Fatalf("turn %d status = %v, want completed", i+1, tr.Status)
		}
		if tr.Response != turnResponses[i] {
			t.Fatalf("turn %d response = %q, want %q", i+1, tr.Response, turnResponses[i])
		}
	}
	if aggResult == nil {
		t.Fatal("aggregate result is nil")
		return // staticcheck: unreachable but signals nil-safety to SA5011
	}
	if got := aggResult.InputTokens; got != 30 {
		t.Fatalf("aggregate input tokens = %d, want 30", got)
	}
	if got := aggResult.OutputTokens; got != 60 {
		t.Fatalf("aggregate output tokens = %d, want 60", got)
	}
	if got := aggResult.Turns; got != 3 {
		t.Fatalf("aggregate turns = %d, want 3", got)
	}
	if got := aggResult.FinalMessage; got != "third response" {
		t.Fatalf("aggregate final message = %q, want %q", got, "third response")
	}
}

func TestExecuteMultiTurn_PostConditionFail(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	rt := &mockRuntime{workspace: workspace}

	ag := &mockResumerAgent{
		mockAgent: mockAgent{name: "test-resumer"},
		runTurnFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, msg transcript.Message, _ string) (*agent.SessionResult, error) {
			return &agent.SessionResult{
				FinalMessage: "no keyword here",
				SessionID:    "s1",
				Turns:        1,
			}, nil
		},
	}

	caseCfg := &config.CaseConfig{
		ID: "post-cond-fail",
		Input: config.Input{
			Turns: []config.Turn{
				{Role: "user", Content: "turn one", PostCondition: &config.PostCondition{
					MustContainAll: []string{"expected_keyword"},
					OnFail:         "fail",
				}},
				{Role: "user", Content: "turn two"},
			},
		},
	}

	e := newTestEvaluator(EvalOptions{Agent: ag, EvalCfg: &config.EvalConfig{}})
	turnResults, _, err := e.executeMultiTurn(context.Background(), rt, caseCfg, ag, agent.ExecOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(turnResults) != 1 {
		t.Fatalf("got %d turn results, want 1 (aborted at first turn)", len(turnResults))
	}
	if turnResults[0].Status != TurnFailed {
		t.Fatalf("turn 1 status = %v, want failed", turnResults[0].Status)
	}
}

func TestExecuteMultiTurn_PostConditionSkipRemaining(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	rt := &mockRuntime{workspace: workspace}

	ag := &mockResumerAgent{
		mockAgent: mockAgent{name: "test-resumer"},
		runTurnFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ transcript.Message, _ string) (*agent.SessionResult, error) {
			return &agent.SessionResult{
				FinalMessage: "partial response",
				SessionID:    "s2",
				Turns:        1,
			}, nil
		},
	}

	caseCfg := &config.CaseConfig{
		ID: "skip-remaining",
		Input: config.Input{
			Turns: []config.Turn{
				{Role: "user", Content: "first", PostCondition: &config.PostCondition{
					MustContainAll: []string{"required"},
					OnFail:         "skip_remaining",
				}},
				{Role: "user", Content: "second"},
				{Role: "user", Content: "third"},
			},
		},
	}

	e := newTestEvaluator(EvalOptions{Agent: ag, EvalCfg: &config.EvalConfig{}})
	turnResults, _, err := e.executeMultiTurn(context.Background(), rt, caseCfg, ag, agent.ExecOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(turnResults) != 3 {
		t.Fatalf("got %d turn results, want 3 (1 failed + 2 skipped)", len(turnResults))
	}
	if turnResults[0].Status != TurnFailed {
		t.Fatalf("turn 1 status = %v, want failed", turnResults[0].Status)
	}
	if turnResults[1].Status != TurnSkipped {
		t.Fatalf("turn 2 status = %v, want skipped", turnResults[1].Status)
	}
	if turnResults[2].Status != TurnSkipped {
		t.Fatalf("turn 3 status = %v, want skipped", turnResults[2].Status)
	}
}

func TestExecuteMultiTurn_TemplateSubstitution(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	rt := &mockRuntime{workspace: workspace}

	var receivedMessages []string
	ag := &mockResumerAgent{
		mockAgent: mockAgent{name: "test-resumer"},
		runTurnFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, msg transcript.Message, _ string) (*agent.SessionResult, error) {
			receivedMessages = append(receivedMessages, msg.Content)
			return &agent.SessionResult{
				FinalMessage: "file is report.txt version v2.0.0",
				SessionID:    "s3",
				Turns:        1,
			}, nil
		},
	}

	caseCfg := &config.CaseConfig{
		ID: "template-sub",
		Input: config.Input{
			Turns: []config.Turn{
				{
					Role:    "user",
					Content: "Create a file",
					Capture: []config.CaptureRule{
						{Variable: "filename", Pattern: `file is (\S+)`},
						{Variable: "ver", Pattern: `version (v\S+)`},
					},
				},
				{Role: "user", Content: "Now edit {{filename}} at {{ver}}"},
			},
		},
	}

	e := newTestEvaluator(EvalOptions{Agent: ag, EvalCfg: &config.EvalConfig{}})
	turnResults, _, err := e.executeMultiTurn(context.Background(), rt, caseCfg, ag, agent.ExecOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(turnResults) != 2 {
		t.Fatalf("got %d turn results, want 2", len(turnResults))
	}
	if len(receivedMessages) != 2 {
		t.Fatalf("agent received %d messages, want 2", len(receivedMessages))
	}
	expectedSecond := "Now edit report.txt at v2.0.0"
	if receivedMessages[1] != expectedSecond {
		t.Fatalf("second message = %q, want %q", receivedMessages[1], expectedSecond)
	}
	// Verify captured vars in turn results.
	if turnResults[0].CapturedVars["filename"] != "report.txt" {
		t.Fatalf("filename captured = %q, want report.txt", turnResults[0].CapturedVars["filename"])
	}
}

func TestExecuteMultiTurn_AgentError(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	rt := &mockRuntime{workspace: workspace}

	callCount := 0
	ag := &mockResumerAgent{
		mockAgent: mockAgent{name: "test-resumer"},
		runTurnFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ transcript.Message, _ string) (*agent.SessionResult, error) {
			callCount++
			if callCount == 2 {
				return nil, errors.New("agent crashed")
			}
			return &agent.SessionResult{FinalMessage: "ok", SessionID: "s4", Turns: 1}, nil
		},
	}

	caseCfg := &config.CaseConfig{
		ID: "agent-error",
		Input: config.Input{
			Turns: []config.Turn{
				{Role: "user", Content: "first"},
				{Role: "user", Content: "second"},
				{Role: "user", Content: "third"},
			},
		},
	}

	e := newTestEvaluator(EvalOptions{Agent: ag, EvalCfg: &config.EvalConfig{}})
	turnResults, _, err := e.executeMultiTurn(context.Background(), rt, caseCfg, ag, agent.ExecOptions{})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(turnResults) != 2 {
		t.Fatalf("got %d turn results, want 2 (1 completed + 1 error)", len(turnResults))
	}
	if turnResults[0].Status != TurnCompleted {
		t.Fatalf("turn 1 status = %v, want completed", turnResults[0].Status)
	}
	if turnResults[1].Status != TurnError {
		t.Fatalf("turn 2 status = %v, want error", turnResults[1].Status)
	}
}

func TestExecuteMultiTurn_NoResumer(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	rt := &mockRuntime{workspace: workspace}

	// Plain mockAgent does NOT implement SessionResumer.
	ag := &mockAgent{name: "no-resumer"}

	caseCfg := &config.CaseConfig{
		ID: "no-resumer",
		Input: config.Input{
			Turns: []config.Turn{
				{Role: "user", Content: "first"},
				{Role: "user", Content: "second"},
			},
		},
	}

	e := newTestEvaluator(EvalOptions{Agent: ag, EvalCfg: &config.EvalConfig{}})
	_, _, err := e.executeMultiTurn(context.Background(), rt, caseCfg, ag, agent.ExecOptions{})

	if err == nil {
		t.Fatal("expected error for non-resumer agent")
	}
	if !errors.Is(err, nil) { // just check it's not nil
		// Verify error message mentions SessionResumer.
		if got := err.Error(); !contains(got, "SessionResumer") {
			t.Fatalf("error = %q, want mention of SessionResumer", got)
		}
	}
}

func TestExecuteMultiTurn_UnresolvedTemplate(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	rt := &mockRuntime{workspace: workspace}

	ag := &mockResumerAgent{
		mockAgent: mockAgent{name: "test-resumer"},
		runTurnFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ transcript.Message, _ string) (*agent.SessionResult, error) {
			return &agent.SessionResult{FinalMessage: "ok", SessionID: "s5", Turns: 1}, nil
		},
	}

	caseCfg := &config.CaseConfig{
		ID: "unresolved",
		Input: config.Input{
			Turns: []config.Turn{
				{Role: "user", Content: "first"},
				{Role: "user", Content: "Use {{undefined_var}} here"},
			},
		},
	}

	e := newTestEvaluator(EvalOptions{Agent: ag, EvalCfg: &config.EvalConfig{}})
	turnResults, _, err := e.executeMultiTurn(context.Background(), rt, caseCfg, ag, agent.ExecOptions{})

	if err == nil {
		t.Fatal("expected error for unresolved template")
	}
	if len(turnResults) != 2 {
		t.Fatalf("got %d turn results, want 2 (1 completed + 1 error)", len(turnResults))
	}
	if turnResults[1].Status != TurnError {
		t.Fatalf("turn 2 status = %v, want error", turnResults[1].Status)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsCheck(s, substr))
}

func containsCheck(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
