//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/evaluator"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/runtime"
)

// TestOpenCodeJudgeConnectionOrder checks actual HTTP routing with a local CLI.
// Recording endpoints return 400; no model service or real credentials are used.
func TestOpenCodeJudgeConnectionOrder(t *testing.T) {
	if os.Getenv("SKILL_UP_OPENCODE_ORDER_E2E") != "1" {
		t.Skip("set SKILL_UP_OPENCODE_ORDER_E2E=1 with OpenCode installed")
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "fixture-key")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_MODEL", "")
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "independent-legacy", true: "legacy-independent"}[reverse], func(t *testing.T) {
			requests := make(chan string, 10)
			a := openCodeRoutingEndpoint(t, "A", requests)
			b := openCodeRoutingEndpoint(t, "B", requests)
			members := []config.JudgeConfig{
				{ID: "independent", Type: "agent_judge", Engine: &config.EngineConfig{Name: "opencode", Model: config.ModelConfig{Provider: "openai", Name: "gpt-5-mini", BaseURL: b.URL + "/v1"}}, Criteria: []string{"quality"}},
				{ID: "legacy", Type: "agent_judge", Model: "openai/gpt-5-mini", Criteria: []string{"quality"}},
			}
			want := []string{"B POST /v1/responses gpt-5-mini", "A POST /v1/responses gpt-5-mini"}
			if reverse {
				slices.Reverse(members)
				slices.Reverse(want)
			}
			root := t.TempDir()
			env := map[string]string{
				"XDG_CONFIG_HOME": filepath.Join(root, "config"), "XDG_DATA_HOME": filepath.Join(root, "data"), "XDG_CACHE_HOME": filepath.Join(root, "cache"),
				"OPENCODE_DISABLE_MODELS_FETCH": "true", "OPENCODE_DISABLE_AUTOUPDATE": "true", "OPENCODE_DISABLE_DEFAULT_PLUGINS": "true",
			}
			e := evaluator.NewEvaluator(evaluator.EvalOptions{
				Agent: &openCodeRoutingRunner{endpoint: a.URL + "/v1"}, OutputDir: t.TempDir(), DeleteWorkspace: true,
				RunnerConfig: credential.ResolvedAgentConfig{Engine: "opencode", Provider: "openai", Model: "gpt-5-mini", BaseURL: a.URL + "/v1", APIKey: "fixture-key"},
				EvalCfg:      &config.EvalConfig{Engine: config.EngineConfig{Name: "opencode"}, Environment: config.Environment{Type: "none", Env: env}, Judges: &members},
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			results, err := e.EvaluateAll(ctx, []*config.CaseConfig{{ID: "routing", Input: config.Input{Prompt: "Return completed"}}})
			if err != nil || len(results) != 1 || len(results[0].JudgeResults) != 2 || results[0].Status != judge.StatusError {
				t.Fatalf("results=%+v error=%v", results, err)
			}
			var got []string
			for len(requests) > 0 {
				got = append(got, <-requests)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("actual HTTP routes=%v, want %v; outcomes=%+v", got, want, results[0].JudgeResults)
			}
			t.Logf("actual HTTP routes=%v", got)
		})
	}
}

func openCodeRoutingEndpoint(t *testing.T, label string, requests chan<- string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		// Ignore OpenCode's auxiliary title-generation model.
		if body.Model == "gpt-5-mini" {
			requests <- label + " " + r.Method + " " + r.URL.Path + " " + body.Model
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"routing recorded","type":"invalid_request_error"}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

type openCodeRoutingRunner struct {
	openCodeProviderRunner

	endpoint string
}

func (a *openCodeRoutingRunner) InstallMCP(_ context.Context, current runtime.Runtime, _ runtime.MCPConfig) error {
	content, err := json.Marshal(map[string]any{"provider": map[string]any{"openai": map[string]any{"options": map[string]string{"baseURL": a.endpoint}}}})
	if err != nil {
		return err
	}
	current.MergeEnv(map[string]string{"OPENCODE_CONFIG_CONTENT": string(content)})
	return nil
}
