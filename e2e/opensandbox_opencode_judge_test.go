//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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

// TestOpenSandboxOpenCodeJudgeProvider uses the real OpenCode CLI and a live
// provider. The runner seeds a conflicting OpenCode endpoint to catch accidental
// inheritance; two judges must complete in separate sessions in the same sandbox.
func TestOpenSandboxOpenCodeJudgeProvider(t *testing.T) {
	if os.Getenv("SKILL_UP_OPENCODE_JUDGE_E2E") != "1" {
		t.Skip("set SKILL_UP_OPENCODE_JUDGE_E2E=1 with OpenSandbox and model-provider credentials")
	}
	for _, name := range []string{"OPENSANDBOX_API_KEY", "OPENSANDBOX_BASE_URL", "OPENSANDBOX_IMAGE", "JUDGE_LIVE_API_KEY", "SKILL_UP_OPENCODE_JUDGE_MODEL"} {
		if os.Getenv(name) == "" {
			t.Fatalf("%s is required", name)
		}
	}
	provider := os.Getenv("SKILL_UP_OPENCODE_JUDGE_PROVIDER")
	if provider == "" {
		provider = "judge_live"
	}
	endpoint := os.Getenv("SKILL_UP_OPENCODE_JUDGE_BASE_URL")
	if endpoint == "" && provider != "openai" && provider != "anthropic" {
		t.Fatal("SKILL_UP_OPENCODE_JUDGE_BASE_URL is required for a custom provider")
	}
	t.Setenv(strings.ToUpper(provider)+"_API_KEY", os.Getenv("JUDGE_LIVE_API_KEY"))
	t.Setenv(strings.ToUpper(provider)+"_BASE_URL", endpoint)
	t.Setenv(strings.ToUpper(provider)+"_MODEL", "")
	model := config.ModelConfig{Provider: provider, Name: os.Getenv("SKILL_UP_OPENCODE_JUDGE_MODEL"), BaseURL: endpoint}
	timeout := 180
	members := []config.JudgeConfig{}
	for _, id := range []string{"semantic", "review"} {
		members = append(members, config.JudgeConfig{ID: id, Type: "agent_judge", Engine: &config.EngineConfig{Name: "opencode", Version: os.Getenv("SKILL_UP_OPENCODE_JUDGE_VERSION"), Model: model}, Criteria: []string{"The agent final message is exactly completed."}, TimeoutSeconds: &timeout})
	}
	out := t.TempDir()
	e := evaluator.NewEvaluator(evaluator.EvalOptions{Agent: &openCodeProviderRunner{provider: provider}, OutputDir: out, DeleteWorkspace: true, EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: "opensandbox", Image: os.Getenv("OPENSANDBOX_IMAGE"), ReadyTimeoutSeconds: 90, SandboxTimeoutSeconds: 900, Metadata: map[string]string{"skill_up_test": "opencode-judge-provider"}}, Judges: &members}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	results, err := e.EvaluateAll(ctx, []*config.CaseConfig{{ID: "provider", Input: config.Input{Prompt: "Return completed"}}})
	if err != nil {
		t.Fatal(err)
	}
	assertOpenCodeJudgeResults(t, results)
}

func assertOpenCodeJudgeResults(t *testing.T, results []evaluator.EvalResult) {
	t.Helper()
	if len(results) != 1 {
		t.Fatalf("result count=%d", len(results))
	}
	result := results[0]
	if result.Status != judge.StatusPass {
		t.Fatalf("status=%s error=%v outcomes=%+v", result.Status, result.Error, result.JudgeResults)
	}
	if len(result.JudgeResults) != 2 {
		t.Fatalf("judge count=%d", len(result.JudgeResults))
	}
	sessions := map[string]bool{}
	for _, outcome := range result.JudgeResults {
		if outcome.Status != judge.StatusPass || outcome.Session == nil || outcome.Session.SessionID == "" || sessions[outcome.Session.SessionID] {
			t.Fatalf("invalid judge outcome: %+v", outcome)
		}
		sessions[outcome.Session.SessionID] = true
		t.Logf("judge=%s engine=%s session=%s status=%s", outcome.ID, outcome.Session.Engine, outcome.Session.SessionID, outcome.Status)
	}
}

type openCodeProviderRunner struct {
	sharedJudgeFixtureAgent

	provider string
}

func (*openCodeProviderRunner) Install(ctx context.Context, current rt.Runtime) error {
	path, err := current.Exec(ctx, `printf '%s' "${NVM_DIR:-$HOME/.nvm}/current/bin:$PATH"`, rt.ExecOptions{})
	if err != nil {
		return err
	}
	if path.ExitCode != 0 || strings.TrimSpace(path.Stdout) == "" {
		return errors.New("cannot determine sandbox Node path")
	}
	// Ground images may keep Node outside HOME; preserve their NVM location.
	current.MergeEnv(map[string]string{"PATH": strings.TrimSpace(path.Stdout)})
	return nil
}

func (a *openCodeProviderRunner) InstallMCP(_ context.Context, current rt.Runtime, _ rt.MCPConfig) error {
	options := map[string]string{"baseURL": "http://127.0.0.1:9/v1"}
	options["apiKey"] = "{env:SKILL_UP_OPENCODE_API_KEY}"
	content, err := json.Marshal(map[string]any{"provider": map[string]any{a.provider: map[string]any{
		"npm": "@ai-sdk/openai-compatible", "options": options,
	}}})
	if err != nil {
		return err
	}
	current.MergeEnv(map[string]string{"OPENCODE_CONFIG_CONTENT": string(content), "SKILL_UP_OPENCODE_API_KEY": "wrong-runner-key"})
	return nil
}

func (*openCodeProviderRunner) Run(_ context.Context, _ rt.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
	return &agent.SessionResult{SessionID: "runner-fixture", FinalMessage: "completed"}, nil
}
