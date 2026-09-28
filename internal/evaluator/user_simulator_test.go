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
