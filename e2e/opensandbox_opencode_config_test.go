//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	rt "github.com/alibaba/skill-up/internal/runtime"
)

// TestOpenSandboxOpenCodeDefaultRouting checks the real CLI's installation and
// default-provider initialization without requiring native-provider connectivity.
func TestOpenSandboxOpenCodeDefaultRouting(t *testing.T) {
	if os.Getenv("SKILL_UP_OPENCODE_CONFIG_E2E") != "1" {
		t.Skip("set SKILL_UP_OPENCODE_CONFIG_E2E=1 with OpenSandbox credentials")
	}
	for _, name := range []string{"OPENSANDBOX_API_KEY", "OPENSANDBOX_BASE_URL", "OPENSANDBOX_IMAGE"} {
		if os.Getenv(name) == "" {
			t.Fatalf("%s is required", name)
		}
	}
	environment := config.Environment{Type: "opensandbox", Image: os.Getenv("OPENSANDBOX_IMAGE"), ReadyTimeoutSeconds: 90, SandboxTimeoutSeconds: 300, Metadata: map[string]string{"skill_up_test": "opencode-default-config"}}
	cfg := environment.ToRuntimeConfig()
	cfg.Delete = true
	current, err := rt.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if err := current.Create(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := current.Close(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	if err := (&openCodeProviderRunner{}).Install(ctx, current); err != nil {
		t.Fatal(err)
	}
	current.MergeEnv(map[string]string{"OPENCODE_CONFIG_CONTENT": `{"mcp":{"keep":{"type":"remote","url":"https://mcp.example"}},"provider":{"openai":{"options":{"baseURL":"http://127.0.0.1:9/v1","apiKey":"{env:SKILL_UP_OPENCODE_API_KEY}"}},"other":{"options":{"baseURL":"https://other.example/v1"}}}}`})
	ag := agent.NewOpenCodeAgent(agent.Config{ModelProvider: "openai", ModelName: "gpt-5-mini", Version: os.Getenv("SKILL_UP_OPENCODE_JUDGE_VERSION")})
	if err := ag.Install(ctx, current); err != nil {
		t.Fatal(err)
	}
	observation, err := agent.Preflight(ctx, current, ag)
	if err != nil {
		t.Fatal(err)
	}
	if err := ag.InstallJudgeConfig(ctx, current); err != nil {
		t.Fatal(err)
	}
	read, err := current.Exec(ctx, `printf '%s' "$OPENCODE_CONFIG_CONTENT"`, rt.ExecOptions{})
	if err != nil || read.ExitCode != 0 {
		t.Fatalf("read config: %v", err)
	}
	assertOpenCodeDefaultRoute(t, read.Stdout)
	t.Logf("OpenCode version=%s; default openai routing reset; MCP and other provider preserved", observation.Version)
}

func assertOpenCodeDefaultRoute(t *testing.T, content string) {
	t.Helper()
	var value struct {
		MCP      map[string]json.RawMessage `json:"mcp"`
		Provider map[string]json.RawMessage `json:"provider"`
	}
	if err := json.Unmarshal([]byte(content), &value); err != nil {
		t.Fatal(err)
	}
	if value.Provider["openai"] != nil || value.Provider["other"] == nil || value.MCP["keep"] == nil {
		t.Fatal("default routing reset lost unrelated configuration or retained runner provider")
	}
}
