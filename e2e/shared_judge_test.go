//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/evaluator"
	"github.com/alibaba/skill-up/internal/judge"
	rt "github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// This uses a deterministic agent fixture to exercise the real agent_judge
// pipeline without requiring a model-provider account. All files and commands
// run in the real runtime, including dependencies outside the workspace.
// Judges share filesystem state and start fresh sessions.
func TestDockerSharedJudgeEnvironment(t *testing.T) {
	if os.Getenv("SKILL_UP_DOCKER_JUDGE_E2E") != "1" {
		t.Skip("set SKILL_UP_DOCKER_JUDGE_E2E=1 with alpine:3.22 available")
	}
	if err := exec.CommandContext(context.Background(), "docker", "info").Run(); err != nil {
		t.Fatal(err)
	}
	runSharedJudgeEnvironment(t, config.Environment{Type: "docker", Image: "alpine:3.22", NetworkPolicy: "deny_all"})
}

func runSharedJudgeEnvironment(t *testing.T, environment config.Environment) {
	t.Helper()
	environment.SandboxTimeoutSeconds = 300
	environment.Env = map[string]string{"JUDGE_ENV": "ready"}
	environment.Metadata = map[string]string{"skill_up_test": "shared-judges"}
	environment.SetupSteps = []config.SetupStep{{Run: "touch /usr/local/setup-marker"}}
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("script-fail-%t", fail), func(t *testing.T) {
			exit := 0
			if fail {
				exit = 1
			}
			script := filepath.Join(t.TempDir(), "check.sh")
			scriptBody := fmt.Sprintf("#!/bin/sh\nset -eu\ntest \"$(cat answer.txt)\" = original\ntest \"$JUDGE_ENV\" = ready\ntest -f /usr/local/setup-marker\ntest \"$(judge-tool)\" = installed\ntest -z \"$(ip route)\"\nprintf changed > answer.txt\ntouch sibling-marker\nexit %d\n", exit)
			if environment.Type != "docker" {
				scriptBody = strings.ReplaceAll(scriptBody, "test -z \"$(ip route)\"\n", "")
			}
			writeFile(t, script, scriptBody)
			members := []config.JudgeConfig{
				{ID: "functional", Type: "script", ScriptPath: script},
				{ID: "semantic", Type: "agent_judge", Model: "fixture", Context: &config.JudgeContextConfig{FinalMessage: "file_ref"}, Criteria: []string{"completion"}},
				{ID: "review", Type: "agent_judge", Model: "fixture", Context: &config.JudgeContextConfig{FinalMessage: "file_ref"}, Criteria: []string{"completion"}},
			}
			ag := &sharedJudgeFixtureAgent{checkNetwork: environment.Type == "docker"}
			out := t.TempDir()
			e := evaluator.NewEvaluator(evaluator.EvalOptions{Agent: ag, OutputDir: out, DeleteWorkspace: true, EvalCfg: &config.EvalConfig{
				Environment: environment, Judges: &members,
			}})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			results, err := e.EvaluateAll(ctx, []*config.CaseConfig{{ID: "shared", Input: config.Input{Prompt: "task"}}})
			if err != nil {
				t.Fatal(err)
			}
			want := judge.StatusPass
			if fail {
				want = judge.StatusFail
			}
			if len(results) != 1 || results[0].Status != want || len(results[0].JudgeResults) != 3 || results[0].JudgeResults[1].Status != judge.StatusPass || results[0].JudgeResults[2].Status != judge.StatusPass {
				t.Fatalf("results=%+v", results)
			}
			assertSharedJudgeSessions(t, results[0], ag.calls, out)
		})
	}
}

func assertSharedJudgeSessions(t *testing.T, result evaluator.EvalResult, calls int, out string) {
	t.Helper()
	if calls != 3 {
		t.Fatalf("agent calls=%d, want one Agent run plus two new judge sessions", calls)
	}
	for i, id := range []string{"semantic", "review"} {
		outcome := result.JudgeResults[i+1]
		if outcome.Session == nil || outcome.Session.SessionID != fmt.Sprintf("session-%d", i+2) {
			t.Fatalf("judge %s reused a session: %+v", id, outcome.Session)
		}
		data, err := os.ReadFile(filepath.Join(out, "shared", "with_skill", "outputs", "judge", id, "context", "final_message.txt"))
		if err != nil || string(data) != "original" {
			t.Fatalf("judge %s material readback=%q, %v", id, data, err)
		}
	}
}

type sharedJudgeFixtureAgent struct {
	calls        int
	source       rt.Runtime
	checkNetwork bool
}

func (*sharedJudgeFixtureAgent) Name() string { return "shared-fixture" }

func (*sharedJudgeFixtureAgent) Install(context.Context, rt.Runtime) error { return nil }

func (*sharedJudgeFixtureAgent) InstallMCP(context.Context, rt.Runtime, rt.MCPConfig) error {
	return nil
}

func (*sharedJudgeFixtureAgent) InstallSkill(context.Context, rt.Runtime, rt.SkillConfig) error {
	return nil
}

func (*sharedJudgeFixtureAgent) CheckCredentials(context.Context) error { return nil }

func (*sharedJudgeFixtureAgent) Check(ctx context.Context, current rt.Runtime) error {
	_, err := current.Exec(ctx, "command -v sh", rt.ExecOptions{})
	return err
}

func (a *sharedJudgeFixtureAgent) Run(ctx context.Context, current rt.Runtime, _ agent.ExecOptions, messages []transcript.Message) (*agent.SessionResult, error) {
	a.calls++
	if a.source != nil && a.source != current {
		return nil, errors.New("judge received a different runtime")
	}
	if len(messages) != 1 || messages[0].Role != transcript.RoleUser {
		return nil, fmt.Errorf("expected one fresh-session prompt, got %d messages", len(messages))
	}
	command := `test "$(cat answer.txt)" = changed && test -e sibling-marker && test "$(judge-tool)" = installed && test -f /usr/local/setup-marker && test "$JUDGE_ENV" = ready && test -z "$(ip route)"`
	if !a.checkNetwork {
		command = strings.ReplaceAll(command, ` && test -z "$(ip route)"`, "")
	}
	output := `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":["shared output and dependencies preserved"],"failures":[]}]}`
	if a.calls == 1 {
		a.source = current
		command = `printf original > answer.txt; printf '#!/bin/sh\necho installed\n' > /usr/local/bin/judge-tool; chmod +x /usr/local/bin/judge-tool`
		output = "original"
	}
	result, err := current.Exec(ctx, command, rt.ExecOptions{})
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("shared runtime check failed: %s", result.Stderr)
	}
	return &agent.SessionResult{SessionID: fmt.Sprintf("session-%d", a.calls), FinalMessage: output}, nil
}
