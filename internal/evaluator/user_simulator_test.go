package evaluator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/internal/usersimulator"
	"github.com/alibaba/skill-up/pkg/transcript"
)

type scriptedSimulator struct {
	decisions []usersimulator.Decision
	histories [][]usersimulator.Exchange
	policies  []usersimulator.ResponsePolicy
}

type failingSimulator struct{}

func (failingSimulator) Next(context.Context, string, string, []usersimulator.Exchange, usersimulator.ResponsePolicy) (usersimulator.Decision, error) {
	return usersimulator.Decision{}, errors.New("simulator unavailable")
}

func TestSimulatedReplyFailurePreservesEarlierTurns(t *testing.T) {
	ag := &strictIncrementalMockResumerAgent{mockResumerAgent: &mockResumerAgent{
		mockAgent: mockAgent{name: "incremental"},
		runTurnFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, msg transcript.Message, _ string) (*agent.SessionResult, error) {
			return &agent.SessionResult{
				FinalMessage: "reply to " + msg.Content,
				SessionID:    "session-1",
				InputTokens:  msg.Turn,
				OutputTokens: msg.Turn * 2,
				DurationMs:   int64(msg.Turn * 10),
				Transcript: transcript.Transcript{
					{Role: transcript.RoleUser, Content: msg.Content},
					{Role: transcript.RoleAssistant, Content: "reply to " + msg.Content},
				},
				Artifacts: &agent.SessionArtifacts{Files: []agent.ArtifactFile{{Name: "turn.txt", Content: msg.Content}}},
			}, nil
		},
	}}
	e := newTestEvaluator(EvalOptions{Agent: ag, Simulator: failingSimulator{}, EvalCfg: &config.EvalConfig{Cases: config.CasesConfig{Defaults: config.CaseDefaults{MaxTurns: 3}}}})
	caseCfg := &config.CaseConfig{ID: "simulator-failure", UserSimulator: &config.UserSimulatorScenario{Scenario: "staging"}, Input: config.Input{Turns: []config.Turn{
		{Role: "user", Content: "first"},
		{Role: "user", Content: "second"},
		{Role: "user", Respond: "answer the question"},
	}}}
	results, aggregate, err := e.executeMultiTurn(context.Background(), &mockRuntime{workspace: t.TempDir()}, caseCfg, ag, agent.ExecOptions{})
	if err == nil || !strings.Contains(err.Error(), "simulator unavailable") {
		t.Fatalf("error = %v, want simulator failure", err)
	}
	if ag.turnCall != 2 || len(results) != 2 || aggregate == nil || aggregate.Turns != 2 {
		t.Fatalf("calls=%d results=%+v aggregate=%+v", ag.turnCall, results, aggregate)
	}
	if len(aggregate.Transcript) != 4 || aggregate.Transcript[0].Content != "first" || aggregate.Transcript[2].Content != "second" {
		t.Errorf("lost prior transcript: %+v", aggregate.Transcript)
	}
	if aggregate.InputTokens != 3 || aggregate.OutputTokens != 6 || aggregate.DurationMs != 30 {
		t.Errorf("lost prior usage: %+v", aggregate)
	}
	if aggregate.Artifacts == nil || len(aggregate.Artifacts.Files) != 2 {
		t.Errorf("lost prior artifacts: %+v", aggregate.Artifacts)
	}
}

func TestGeneratedFirstTurnPopulatesReportedPrompt(t *testing.T) {
	ag := &mockResumerAgent{mockAgent: mockAgent{name: "simulated"}}
	simulator := &scriptedSimulator{decisions: []usersimulator.Decision{{Action: "reply", Message: "Use staging"}}}
	e := newTestEvaluator(EvalOptions{Agent: ag, Simulator: simulator, EvalCfg: &config.EvalConfig{
		Engine: config.EngineConfig{Name: "codex"},
		Cases:  config.CasesConfig{Defaults: config.CaseDefaults{MaxTurns: 2}},
		Judge:  config.JudgeConfig{Type: "rule_based"},
	}})
	caseCfg := &config.CaseConfig{ID: "generated-first", UserSimulator: &config.UserSimulatorScenario{Scenario: "staging"}, Input: config.Input{Turns: []config.Turn{
		{Role: "user", Respond: "Start the conversation"},
		{Role: "user", Content: "Now summarize"},
	}}}
	result := e.executeCaseOnce(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: t.TempDir()}, ag)
	if result.Prompt != "Use staging" || len(result.TurnResults) != 2 || result.TurnResults[0].Content != "Use staging" {
		t.Fatalf("reported prompt or turns do not match the sent message: %+v", result)
	}
}

func TestAutonomousSimulationRequiresSessionIDBeforeResume(t *testing.T) {
	ag := &strictIncrementalMockResumerAgent{mockResumerAgent: &mockResumerAgent{
		mockAgent: mockAgent{name: "strict"},
		runTurnFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ transcript.Message, _ string) (*agent.SessionResult, error) {
			return &agent.SessionResult{FinalMessage: "Which environment?"}, nil
		},
	}}
	simulator := &scriptedSimulator{decisions: []usersimulator.Decision{{Action: "reply", Message: "staging"}}}
	e := newTestEvaluator(EvalOptions{Agent: ag, Simulator: simulator, EvalCfg: &config.EvalConfig{Cases: config.CasesConfig{Defaults: config.CaseDefaults{MaxTurns: 3}}}})
	caseCfg := &config.CaseConfig{ID: "needs-session", UserSimulator: &config.UserSimulatorScenario{Scenario: "staging"}, Input: config.Input{Prompt: "Create config"}}
	results, _, err := e.executeMultiTurn(context.Background(), &mockRuntime{workspace: t.TempDir()}, caseCfg, ag, agent.ExecOptions{})
	if err == nil || !strings.Contains(err.Error(), "no session_id") || ag.turnCall != 1 {
		t.Fatalf("results=%+v calls=%d error=%v", results, ag.turnCall, err)
	}
	if len(results) != 1 || results[0].Status != TurnError {
		t.Fatalf("turn results = %+v", results)
	}
}

func TestAutonomousSimulationRejectsMissingSessionIDForNonStrictResumer(t *testing.T) {
	ag := &mockResumerAgent{mockAgent: mockAgent{name: "non-strict"}, runTurnFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ transcript.Message, _ string) (*agent.SessionResult, error) {
		return &agent.SessionResult{FinalMessage: "Which environment?"}, nil
	}}
	simulator := &scriptedSimulator{decisions: []usersimulator.Decision{{Action: "reply", Message: "staging"}}}
	e := newTestEvaluator(EvalOptions{Agent: ag, Simulator: simulator, EvalCfg: &config.EvalConfig{Cases: config.CasesConfig{Defaults: config.CaseDefaults{MaxTurns: 3}}}})
	caseCfg := &config.CaseConfig{ID: "needs-session-non-strict", UserSimulator: &config.UserSimulatorScenario{Scenario: "staging"}, Input: config.Input{Prompt: "Create config"}}
	results, _, err := e.executeMultiTurn(context.Background(), &mockRuntime{workspace: t.TempDir()}, caseCfg, ag, agent.ExecOptions{})
	if err == nil || !strings.Contains(err.Error(), "no session_id") || ag.turnCall != 1 {
		t.Fatalf("results=%+v calls=%d error=%v", results, ag.turnCall, err)
	}
	if len(results) != 1 || results[0].Status != TurnError {
		t.Fatalf("turn results = %+v", results)
	}
}

func TestSimulatedTurnExecutionErrorKeepsSource(t *testing.T) {
	ag := &mockResumerAgent{mockAgent: mockAgent{name: "error"}, runTurnFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, msg transcript.Message, _ string) (*agent.SessionResult, error) {
		if msg.Turn == 2 {
			return nil, errors.New("execution failed")
		}
		return &agent.SessionResult{FinalMessage: "Which environment?", SessionID: "session-1"}, nil
	}}
	simulator := &scriptedSimulator{decisions: []usersimulator.Decision{{Action: "reply", Message: "staging"}}}
	e := newTestEvaluator(EvalOptions{Agent: ag, Simulator: simulator, EvalCfg: &config.EvalConfig{Cases: config.CasesConfig{Defaults: config.CaseDefaults{MaxTurns: 3}}}})
	caseCfg := &config.CaseConfig{ID: "error", UserSimulator: &config.UserSimulatorScenario{Scenario: "staging"}, Input: config.Input{Turns: []config.Turn{{Role: "user", Content: "Create config"}, {Role: "user", Respond: "Answer environment"}}}}
	results, _, err := e.executeMultiTurn(context.Background(), &mockRuntime{workspace: t.TempDir()}, caseCfg, ag, agent.ExecOptions{})
	if err == nil || len(results) != 2 || results[1].Source != simulatedSource || results[1].Status != TurnError {
		t.Fatalf("turn results = %+v, error = %v", results, err)
	}
}

const simulatedSource = "simulated"

func (s *scriptedSimulator) Next(_ context.Context, _, _ string, history []usersimulator.Exchange, policy usersimulator.ResponsePolicy) (usersimulator.Decision, error) {
	s.histories = append(s.histories, history)
	s.policies = append(s.policies, policy)
	decision := s.decisions[0]
	s.decisions = s.decisions[1:]
	return decision, nil
}

func TestUserSimulationMixedTurns(t *testing.T) {
	ag := &mockResumerAgent{mockAgent: mockAgent{name: "simulated"}}
	simulator := &scriptedSimulator{decisions: []usersimulator.Decision{{Action: "reply", Message: "staging"}}}
	e := newTestEvaluator(EvalOptions{Agent: ag, Simulator: simulator, EvalCfg: &config.EvalConfig{Cases: config.CasesConfig{Defaults: config.CaseDefaults{MaxTurns: 4}}}})
	caseCfg := &config.CaseConfig{ID: "mixed", UserSimulator: &config.UserSimulatorScenario{Scenario: "staging only"}, Input: config.Input{Turns: []config.Turn{
		{Role: "user", Content: "Create config"},
		{Role: "user", Respond: "Answer environment question"},
		{Role: "user", Content: "Set replicas to 3"},
	}}}
	results, _, err := e.executeMultiTurn(context.Background(), &mockRuntime{workspace: t.TempDir()}, caseCfg, ag, agent.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results[1].Content != "staging" || results[1].Source != simulatedSource || results[2].Source != "fixed" {
		t.Fatalf("mixed turn results = %+v", results)
	}
	if len(simulator.histories) != 1 || len(simulator.histories[0]) != 1 || simulator.policies[0] != usersimulator.MustReply {
		t.Fatalf("simulator context = %+v; policies = %v", simulator.histories, simulator.policies)
	}
}

func TestSimulatedRepliesPreserveLiteralPlaceholders(t *testing.T) {
	const reply = "Use Hello {{name}} as the email template; keep the placeholder literal."
	for _, test := range []struct {
		name      string
		input     config.Input
		decisions []usersimulator.Decision
	}{
		{
			name: "mixed",
			input: config.Input{Turns: []config.Turn{
				{Role: "user", Content: "Create an email template"},
				{Role: "user", Respond: "Answer the clarification"},
			}},
			decisions: []usersimulator.Decision{{Action: "reply", Message: reply}},
		},
		{
			name:      "autonomous",
			input:     config.Input{Prompt: "Create an email template"},
			decisions: []usersimulator.Decision{{Action: "reply", Message: reply}, {Action: "stop", Reason: "done"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var sent []string
			ag := &mockResumerAgent{mockAgent: mockAgent{name: "simulated"}, runTurnFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, msg transcript.Message, _ string) (*agent.SessionResult, error) {
				sent = append(sent, msg.Content)
				return &agent.SessionResult{FinalMessage: "ack", SessionID: "session-1"}, nil
			}}
			e := newTestEvaluator(EvalOptions{Agent: ag, Simulator: &scriptedSimulator{decisions: test.decisions}, EvalCfg: &config.EvalConfig{
				Engine: config.EngineConfig{Name: "codex"},
				Cases:  config.CasesConfig{Defaults: config.CaseDefaults{MaxTurns: 3}},
				Judge:  config.JudgeConfig{Type: "rule_based"},
			}})
			caseCfg := &config.CaseConfig{ID: test.name, UserSimulator: &config.UserSimulatorScenario{Scenario: "Preserve a template placeholder"}, Input: test.input}
			result := e.executeCaseOnce(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: t.TempDir()}, ag)
			if result.Error != nil || len(sent) != 2 || sent[1] != reply || len(result.TurnResults) != 2 || result.TurnResults[1].Content != reply || result.TurnResults[1].Source != simulatedSource {
				t.Fatalf("error=%v sent=%q turns=%+v", result.Error, sent, result.TurnResults)
			}
		})
	}
}

func TestUnsupportedSimulatorEngineRetainsBenchmarkVariant(t *testing.T) {
	ag := &mockAgent{name: "batch-only"}
	e := newTestEvaluator(EvalOptions{Agent: ag, EvalCfg: &config.EvalConfig{Benchmark: config.BenchmarkConfig{Enabled: true}}})
	caseCfg := &config.CaseConfig{ID: "unsupported", Input: config.Input{Prompt: "start"}, UserSimulator: &config.UserSimulatorScenario{Scenario: "answer questions"}}
	for _, variant := range []string{"with_skill", "without_skill"} {
		result := e.executeCaseOnce(context.Background(), caseCfg, variant, &mockRuntime{workspace: t.TempDir()}, ag)
		if result.Configuration != variant || result.Error == nil || !strings.Contains(result.Error.Error(), "does not support session resumption") {
			t.Fatalf("variant %q result = %+v", variant, result)
		}
	}
	if ag.runCall.Load() != 0 {
		t.Fatalf("unsupported engine ran %d times", ag.runCall.Load())
	}
}

func TestUserSimulationAutonomousStopAndLimit(t *testing.T) {
	for _, test := range []struct {
		name       string
		decisions  []usersimulator.Decision
		wantError  string
		wantTurns  int
		stopReason string
	}{
		{"stop", []usersimulator.Decision{{Action: "reply", Message: "staging"}, {Action: "stop", Reason: "done"}}, "", 2, "done"},
		{"limit", []usersimulator.Decision{{Action: "reply", Message: "staging"}, {Action: "reply", Message: "more"}}, "max_turns", 2, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			ag := &mockResumerAgent{mockAgent: mockAgent{name: "simulated"}, runTurnFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, msg transcript.Message, _ string) (*agent.SessionResult, error) {
				return &agent.SessionResult{FinalMessage: "response to " + msg.Content, SessionID: "session-1"}, nil
			}}
			simulator := &scriptedSimulator{decisions: test.decisions}
			e := newTestEvaluator(EvalOptions{Agent: ag, Simulator: simulator, EvalCfg: &config.EvalConfig{Cases: config.CasesConfig{Defaults: config.CaseDefaults{MaxTurns: 2}}}})
			caseCfg := &config.CaseConfig{ID: "auto", UserSimulator: &config.UserSimulatorScenario{Scenario: "staging only"}, Input: config.Input{Prompt: "Create config"}}
			results, _, err := e.executeMultiTurn(context.Background(), &mockRuntime{workspace: t.TempDir()}, caseCfg, ag, agent.ExecOptions{})
			if (err != nil && !strings.Contains(err.Error(), test.wantError)) || (err == nil && test.wantError != "") {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
			if len(results) != test.wantTurns || results[len(results)-1].StopReason != test.stopReason || results[1].Source != simulatedSource {
				t.Fatalf("results = %+v", results)
			}
		})
	}
}

func TestMixedSimulationRejectsMissingSessionBeforeModelCall(t *testing.T) {
	ag := &mockResumerAgent{mockAgent: mockAgent{name: "non-strict"}, runTurnFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ transcript.Message, _ string) (*agent.SessionResult, error) {
		return &agent.SessionResult{
			FinalMessage: "Which environment?", InputTokens: 7, OutputTokens: 3, DurationMs: 10,
			Transcript: transcript.Transcript{{Role: transcript.RoleUser, Content: "Create config"}, {Role: transcript.RoleAssistant, Content: "Which environment?"}},
		}, nil
	}}
	simulator := &scriptedSimulator{decisions: []usersimulator.Decision{{Action: "reply", Message: "staging"}}}
	e := newTestEvaluator(EvalOptions{Agent: ag, Simulator: simulator, EvalCfg: &config.EvalConfig{}})
	caseCfg := &config.CaseConfig{ID: "mixed-needs-session", UserSimulator: &config.UserSimulatorScenario{Scenario: "staging"}, Input: config.Input{Turns: []config.Turn{
		{Role: "user", Content: "Create config"}, {Role: "user", Respond: "Answer the question"},
	}}}
	results, aggregate, err := e.executeMultiTurn(context.Background(), &mockRuntime{workspace: t.TempDir()}, caseCfg, ag, agent.ExecOptions{})
	if err == nil || !strings.Contains(err.Error(), "no session_id") || ag.turnCall != 1 || len(simulator.histories) != 0 {
		t.Fatalf("calls=%d simulator calls=%d error=%v", ag.turnCall, len(simulator.histories), err)
	}
	if len(results) != 1 || aggregate == nil || aggregate.InputTokens != 7 || aggregate.OutputTokens != 3 || aggregate.DurationMs != 10 || len(aggregate.Transcript) != 2 {
		t.Fatalf("completed turns were lost: results=%+v aggregate=%+v", results, aggregate)
	}
}

func TestInitialSimulatedMessageDoesNotRequireSessionID(t *testing.T) {
	ag := &mockResumerAgent{mockAgent: mockAgent{name: "non-strict"}, runTurnFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, msg transcript.Message, _ string) (*agent.SessionResult, error) {
		return &agent.SessionResult{FinalMessage: "reply to " + msg.Content}, nil
	}}
	simulator := &scriptedSimulator{decisions: []usersimulator.Decision{{Action: "reply", Message: "Create staging config"}}}
	e := newTestEvaluator(EvalOptions{Agent: ag, Simulator: simulator, EvalCfg: &config.EvalConfig{}})
	caseCfg := &config.CaseConfig{ID: "generated-opening", UserSimulator: &config.UserSimulatorScenario{Scenario: "staging"}, Input: config.Input{Turns: []config.Turn{{Role: "user", Respond: "Start the conversation"}}}}
	results, _, err := e.executeMultiTurn(context.Background(), &mockRuntime{workspace: t.TempDir()}, caseCfg, ag, agent.ExecOptions{})
	if err != nil || len(results) != 1 || ag.turnCall != 1 || len(simulator.histories) != 1 || results[0].Content != "Create staging config" {
		t.Fatalf("results=%+v error=%v", results, err)
	}
}
