package credential

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/logging"
)

var logCaptureMu sync.Mutex

func resolveRunnerConfigForTest(
	engine string,
	model config.ModelConfig,
	custom *config.CustomEngineConfig,
	resolver *Resolver,
	cliModel string,
	cliAPIKey string,
) ResolvedAgentConfig {
	return ResolveRunnerConfig(config.EngineConfig{
		Name:   engine,
		Model:  model,
		Custom: custom,
	}, resolver, CLIOverrides{Model: cliModel, APIKey: cliAPIKey})
}

func TestMaskAPIKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		key      string
		expected string
	}{
		{name: "normal key", key: "sk-abcdefghijklmnopqrstuvwxyz123456", expected: "sk****56"},
		{name: "short key (4 chars)", key: "abcd", expected: "****"},
		{name: "shorter than mask length", key: "abc", expected: "****"},
		{name: "empty key", key: "", expected: "****"},
		{name: "exactly 5 chars", key: "abcde", expected: "ab****de"},
		{name: "anthropic key format", key: "sk-ant-api03-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", expected: "sk****xx"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := MaskAPIKey(tt.key); got != tt.expected {
				t.Errorf("MaskAPIKey(%q) = %q, want %q", tt.key, got, tt.expected)
			}
		})
	}
}

func TestDefaultConfPath(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home directory: %v", err)
	}

	got := DefaultConfPath()
	want := filepath.Join(home, ".skill-up", "credentials.yaml")
	if got != want {
		t.Errorf("DefaultConfPath() = %q, want %q", got, want)
	}
}

func TestResolver_Get(t *testing.T) {
	t.Parallel()

	r := NewResolver("")
	r.creds["openai"] = &config.APIKeyConfig{
		Provider: "openai",
		APIKey:   "sk-test-key",
		BaseURL:  "https://custom.openai.com",
	}

	cred, ok := r.Get("openai")
	if !ok {
		t.Fatal("expected to find credential for openai")
	}
	if cred.APIKey != "sk-test-key" {
		t.Errorf("APIKey = %q, want %q", cred.APIKey, "sk-test-key")
	}
	if cred.BaseURL != "https://custom.openai.com" {
		t.Errorf("BaseURL = %q, want %q", cred.BaseURL, "https://custom.openai.com")
	}
	if _, ok := r.Get("anthropic"); ok {
		t.Error("expected not to find credential for anthropic")
	}
}

func TestResolver_ConfigFile_Loading(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := tmpDir + "/credentials.yaml"
	configContent := `
schema_version: v1alpha1
providers:
  openai:
    api_key: sk-openai-config-key
    base_url: https://custom.openai.com/v1
  anthropic:
    api_key: sk-ant-config-key
    base_url: https://custom.anthropic.com
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	r := NewResolver(configPath)
	if err := r.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	openAI, ok := r.Get("openai")
	if !ok {
		t.Fatal("expected to find openai credential")
	}
	if openAI.APIKey != "sk-openai-config-key" {
		t.Errorf("openai APIKey = %q, want %q", openAI.APIKey, "sk-openai-config-key")
	}
	if openAI.BaseURL != "https://custom.openai.com/v1" {
		t.Errorf("openai BaseURL = %q, want %q", openAI.BaseURL, "https://custom.openai.com/v1")
	}

	anthropic, ok := r.Get("anthropic")
	if !ok {
		t.Fatal("expected to find anthropic credential")
	}
	if anthropic.APIKey != "sk-ant-config-key" {
		t.Errorf("anthropic APIKey = %q, want %q", anthropic.APIKey, "sk-ant-config-key")
	}
}

func TestResolver_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	r := NewResolver("")
	r.creds["openai"] = &config.APIKeyConfig{Provider: "openai", APIKey: "sk-test"}

	done := make(chan bool, 15)
	for range 10 {
		go func() {
			for range 100 {
				r.Get("openai")
			}
			done <- true
		}()
	}

	for i := range 5 {
		go func(idx int) {
			for j := range 50 {
				r.mu.Lock()
				r.creds["openai"] = &config.APIKeyConfig{
					Provider: "openai",
					APIKey:   fmt.Sprintf("sk-test-%d-%d", idx, j),
				}
				r.mu.Unlock()
			}
			done <- true
		}(i)
	}

	for range 15 {
		<-done
	}

	if _, ok := r.Get("openai"); !ok {
		t.Error("resolver should still have openai credential after concurrent access")
	}
}

func TestResolver_Load_MissingConfigFile(t *testing.T) {
	r := NewResolver("/nonexistent/path/credentials.yaml")
	if err := r.Load(); err != nil {
		t.Errorf("Load() should not return error for missing file, got: %v", err)
	}
	if len(r.creds) != 0 {
		t.Errorf("len(creds) = %d, want 0", len(r.creds))
	}
}

func TestResolver_Load_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/invalid.yaml"
	invalidYAML := `
schema_version: v1alpha1
providers:
  openai:
    api_key: sk-test
    base_url: [invalid yaml structure
`
	if err := os.WriteFile(configPath, []byte(invalidYAML), 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	r := NewResolver(configPath)
	if err := r.Load(); err != nil {
		t.Errorf("Load() should not return error for parse errors, got: %v", err)
	}
	if len(r.creds) != 0 {
		t.Errorf("len(creds) = %d, want 0", len(r.creds))
	}
}

func TestResolver_Load_DoesNotImportProcessEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "env-anthropic-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://env.anthropic.example.com")

	r := NewResolver("")
	if err := r.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(r.creds) != 0 {
		t.Fatalf("len(creds) = %d, want 0 when only process env is set", len(r.creds))
	}
}

func TestResolveRunnerConfig_PrefersProviderEnvOverResolver(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "gpt-5.5-env")
	t.Setenv("OPENAI_API_KEY", "sk-env-openai")
	t.Setenv("OPENAI_BASE_URL", "https://env.example.com/v1")

	r := NewResolver("")
	r.creds["openai"] = &config.APIKeyConfig{
		Provider: "openai",
		APIKey:   "file-openai-key",
		BaseURL:  "https://file.example.com/v1",
	}

	params := resolveRunnerConfigForTest("codex", config.ModelConfig{
		Provider: "openai",
		Name:     "gpt-5.4",
	}, nil, r, "", "")

	if params.Role != AgentRoleRunner {
		t.Fatalf("Kind = %q, want %q", params.Role, AgentRoleRunner)
	}
	if params.Model != "gpt-5.5-env" {
		t.Fatalf("Model = %q, want provider env value", params.Model)
	}
	if params.APIKey != "sk-env-openai" {
		t.Fatalf("APIKey = %q, want env value", params.APIKey)
	}
	if params.BaseURL != "https://env.example.com/v1" {
		t.Fatalf("BaseURL = %q, want env value", params.BaseURL)
	}
	if params.ModelSource != ValueSourceEnv || params.APIKeySource != ValueSourceEnv || params.BaseURLSource != ValueSourceEnv {
		t.Fatalf("unexpected env sources: %#v", params)
	}
}

func TestResolveRunnerConfig_DoesNotScanProviderEnvWhenProviderMissing(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "gpt-5.5-env")
	t.Setenv("OPENAI_API_KEY", "sk-env-openai")
	t.Setenv("OPENAI_BASE_URL", "https://env.example.com/v1")

	params := resolveRunnerConfigForTest("codex", config.ModelConfig{Name: "gpt-5.4"}, nil, nil, "", "")

	if params.Provider != "" {
		t.Fatalf("Provider = %q, want empty", params.Provider)
	}
	if params.Model != "gpt-5.4" {
		t.Fatalf("Model = %q, want config value", params.Model)
	}
	if params.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty", params.APIKey)
	}
	if params.BaseURL != "" {
		t.Fatalf("BaseURL = %q, want empty", params.BaseURL)
	}
	if params.ModelSource != ValueSourceConfig {
		t.Fatalf("ModelSource = %q, want %q", params.ModelSource, ValueSourceConfig)
	}
}

func TestResolveRunnerConfig_PrefersCLIOverrides(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "gpt-5.5-env")
	t.Setenv("OPENAI_API_KEY", "sk-env-openai")

	params := resolveRunnerConfigForTest("codex", config.ModelConfig{
		Provider: "openai",
		Name:     "gpt-5.4",
	}, nil, nil, "gpt-5.6-cli", "sk-cli-openai")

	if params.Model != "gpt-5.6-cli" || params.ModelSource != ValueSourceCLI {
		t.Fatalf("unexpected CLI model resolution: %#v", params)
	}
	if params.APIKey != "sk-cli-openai" || params.APIKeySource != ValueSourceCLI {
		t.Fatalf("unexpected CLI api-key resolution: %#v", params)
	}
}

func TestResolveRunnerConfig_ResolvesCLIModelOnceWithoutMutatingInput(t *testing.T) {
	t.Setenv("DASHSCOPE_API_KEY", "dashscope-env-key")
	engine := config.EngineConfig{
		Name:    "codex",
		Version: "1.2.3",
		Entry:   "codex",
		Model: config.ModelConfig{
			Provider: "anthropic",
			Name:     "yaml-model",
			Params:   map[string]string{"reasoning": "high"},
		},
		Kwargs: map[string]string{"bypass_sandbox": "true"},
	}

	resolved := ResolveRunnerConfig(engine, nil, CLIOverrides{
		Model: "dashscope/qwen3.6-plus",
	})

	if resolved.Provider != "dashscope" || resolved.Model != "qwen3.6-plus" {
		t.Fatalf("resolved model = %q/%q, want dashscope/qwen3.6-plus", resolved.Provider, resolved.Model)
	}
	if resolved.ProviderSource != ValueSourceCLI || resolved.ModelSource != ValueSourceCLI {
		t.Fatalf("CLI sources not retained: %#v", resolved)
	}
	if resolved.APIKey != "dashscope-env-key" || resolved.APIKeySource != ValueSourceEnv {
		t.Fatalf("provider credentials were not resolved from final CLI provider: %#v", resolved)
	}
	if engine.Model.Provider != "anthropic" || engine.Model.Name != "yaml-model" {
		t.Fatalf("input engine config was mutated: %#v", engine.Model)
	}
	engine.Kwargs["bypass_sandbox"] = "false"
	engine.Model.Params["reasoning"] = "low"
	if resolved.Kwargs["bypass_sandbox"] != "true" || resolved.ModelParams["reasoning"] != "high" {
		t.Fatalf("resolved maps alias input config: kwargs=%v params=%v", resolved.Kwargs, resolved.ModelParams)
	}
}

func TestResolveRunnerConfig_PreservesOpaqueSlashedCLIModel(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_MODELSCOPE_API_KEY", "ANTHROPIC_MODELSCOPE_BASE_URL"} {
		t.Setenv(key, "")
	}

	resolved := ResolveRunnerConfig(config.EngineConfig{
		Name:  "codex",
		Model: config.ModelConfig{Provider: "openai", Name: "yaml-model"},
	}, nil, CLIOverrides{ //nolint:gosec // test credential, not a real secret
		Model:  "anthropic_modelscope/deepseek-v4-pro",
		APIKey: "sk-cli-openai",
	})

	if resolved.Provider != "" || resolved.Model != "anthropic_modelscope/deepseek-v4-pro" {
		t.Fatalf("opaque model was split: %#v", resolved)
	}
	if resolved.APIKey != "sk-cli-openai" || resolved.APIKeySource != ValueSourceCLI {
		t.Fatalf("CLI key was not retained for provider-empty model: %#v", resolved)
	}
}

func TestResolveRunnerConfig_LogsCLIAPIKeySource(t *testing.T) {
	logging.SetVerbosity(1)
	defer logging.SetVerbosity(0)

	output := captureLogOutput(t, func() {
		resolveRunnerConfigForTest("codex", config.ModelConfig{
			Provider: "openai",
			Name:     "gpt-5.4",
		}, nil, nil, "", "sk-cli-openai")
	})

	if !strings.Contains(output, "source.api_key=cli") {
		t.Fatalf("expected CLI api-key observability log, got %q", output)
	}
	if !strings.Contains(output, "api_key=sk****ai") {
		t.Fatalf("expected masked CLI api-key log, got %q", output)
	}
}

func TestResolveRunnerConfig_CLIAPIKeyAppliesWithoutProvider(t *testing.T) {
	// `--api-key K` must apply even when Provider is empty (e.g. user
	// passed `--model literal_opaque/id` and the prefix is not a
	// configured namespace, so ResolveModelRef returned Provider="").
	// Each agent routes cfg.APIKey via its own hardcoded env
	// (ANTHROPIC_API_KEY / OPENAI_API_KEY), so the key reaches upstream
	// correctly regardless of Provider. Previously this case was dropped
	// with a provider_required_for_cli_override warning — that guard was
	// defensive paranoia, not a structural requirement, and it broke
	// literal-opaque-id flows.
	params := resolveRunnerConfigForTest("codex", config.ModelConfig{
		Name: "gpt-5.4",
	}, nil, nil, "", "sk-cli-openai")

	if params.APIKey != "sk-cli-openai" {
		t.Fatalf("APIKey = %q, want sk-cli-openai (CLI key must apply even with empty Provider)", params.APIKey)
	}
	if params.APIKeySource != ValueSourceCLI {
		t.Fatalf("APIKeySource = %v, want ValueSourceCLI", params.APIKeySource)
	}
}

func TestResolveRunnerConfig_CustomEngineCLIAPIKeyApplies(t *testing.T) {
	// A custom engine references the CLI key explicitly via ${api_key} and
	// has no model provider. The key must still be applied (it reaches the
	// agent through engine.custom.env / ${api_key}).
	params := resolveRunnerConfigForTest("my-agent", config.ModelConfig{}, &config.CustomEngineConfig{
		Transport: "local",
		Local:     &config.CustomLocalConfig{Command: "/opt/agent"},
	}, nil, "", "sk-cli-custom")

	if params.APIKey != "sk-cli-custom" {
		t.Fatalf("APIKey = %q, want sk-cli-custom for a custom engine", params.APIKey)
	}
	if params.Custom == nil {
		t.Fatal("Custom config was not threaded into ResolvedAgentConfig")
	}
}

func captureLogOutput(t *testing.T, fn func()) string {
	t.Helper()

	logCaptureMu.Lock()
	defer logCaptureMu.Unlock()

	var buf bytes.Buffer
	restoreOutput := logging.SetOutputForTest(&buf)

	fn()

	restoreOutput()
	return buf.String()
}

func TestResolveJudgeConfig_FallsBackToRunnerWhenJudgeModelEmpty(t *testing.T) {
	// Clear env vars that would override runner fallback.
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL"} {
		t.Setenv(key, "")
	}

	runner := ResolvedAgentConfig{
		Role:           AgentRoleRunner,
		Engine:         "claude-code",
		Provider:       "anthropic",
		Model:          "claude-sonnet-4-6",
		APIKey:         "sk-runner",
		BaseURL:        "https://runner.example.com",
		ProviderSource: ValueSourceConfig,
		ModelSource:    ValueSourceConfig,
		APIKeySource:   ValueSourceCLI,
		BaseURLSource:  ValueSourceResolver,
	}

	params := ResolveJudgeConfig(config.JudgeConfig{}, runner, nil)

	if params.Role != AgentRoleJudge {
		t.Fatalf("Kind = %q, want %q", params.Role, AgentRoleJudge)
	}
	if params.Provider != runner.Provider || params.Model != runner.Model || params.APIKey != runner.APIKey || params.BaseURL != runner.BaseURL {
		t.Fatalf("judge params = %#v, want runner values", params)
	}
	if params.ProviderSource != ValueSourceRunner || params.ModelSource != ValueSourceRunner || params.APIKeySource != ValueSourceRunner || params.BaseURLSource != ValueSourceRunner {
		t.Fatalf("expected runner sources, got %#v", params)
	}
}

func TestResolveJudgeConfig_InheritsRunnerLifecycleWithoutAliasingKwargs(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"} {
		t.Setenv(key, "")
	}
	runner := ResolvedAgentConfig{
		Role:        AgentRoleRunner,
		Engine:      "codex",
		Version:     "0.42.0",
		Entry:       "codex",
		Provider:    "openai",
		Model:       "gpt-5.4",
		Kwargs:      map[string]string{"bypass_sandbox": "true"},
		ModelParams: map[string]string{"reasoning": "high"},
	}

	resolved := ResolveJudgeConfig(config.JudgeConfig{
		Type:  "agent_judge",
		Model: "anthropic/claude-sonnet-4-6",
	}, runner, nil)

	if resolved.Role != AgentRoleJudge || resolved.Engine != runner.Engine || resolved.Version != runner.Version || resolved.Entry != runner.Entry {
		t.Fatalf("judge lifecycle config = %#v, want runner engine lifecycle", resolved)
	}
	if resolved.Provider != "anthropic" || resolved.Model != "claude-sonnet-4-6" {
		t.Fatalf("judge role model was not independently resolved: %#v", resolved)
	}
	runner.Kwargs["bypass_sandbox"] = "false"
	runner.ModelParams["reasoning"] = "low"
	if resolved.Kwargs["bypass_sandbox"] != "true" || resolved.ModelParams["reasoning"] != "high" {
		t.Fatalf("judge config aliases runner maps: kwargs=%v params=%v", resolved.Kwargs, resolved.ModelParams)
	}
}

func TestResolveJudgeConfig_FallsBackToRunnerBaseURLBeforeCredentialFallback(t *testing.T) {
	// Clear env vars that would override runner fallback.
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL"} {
		t.Setenv(key, "")
	}

	runner := ResolvedAgentConfig{
		Role:          AgentRoleRunner,
		Engine:        "claude-code",
		Provider:      "anthropic",
		Model:         "claude-sonnet-4-6",
		BaseURL:       "https://runner.example.com",
		BaseURLSource: ValueSourceResolver,
	}

	params := ResolveJudgeConfig(config.JudgeConfig{}, runner, nil)

	if params.BaseURL != "https://runner.example.com" {
		t.Fatalf("BaseURL = %q, want runner base URL", params.BaseURL)
	}
	if params.BaseURLSource != ValueSourceRunner {
		t.Fatalf("BaseURLSource = %q, want %q", params.BaseURLSource, ValueSourceRunner)
	}
}

func TestResolveJudgeConfig_ParsesIndependentJudgeModel(t *testing.T) {
	const provider = "judgeprovider"

	r := NewResolver("")
	r.creds[provider] = &config.APIKeyConfig{
		Provider: provider,
		APIKey:   "sk-judge",
		BaseURL:  "https://judge.example.com/v1",
	}

	params := ResolveJudgeConfig(config.JudgeConfig{
		Type:  "agent_judge",
		Model: provider + "/gpt-5.4",
	}, ResolvedAgentConfig{
		Role:     AgentRoleRunner,
		Engine:   "codex",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
	}, r)

	if params.Provider != provider || params.Model != "gpt-5.4" {
		t.Fatalf("judge params = %#v, want %s/gpt-5.4", params, provider)
	}
	if params.ProviderSource != ValueSourceJudge || params.ModelSource != ValueSourceJudge {
		t.Fatalf("expected judge-config sources, got %#v", params)
	}
	if params.APIKey != "sk-judge" || params.BaseURL != "https://judge.example.com/v1" {
		t.Fatalf("judge credential resolution failed: %#v", params)
	}
}

func TestResolveJudgeConfig_PrefersProviderScopedModelEnv(t *testing.T) {
	t.Setenv("JUDGEPROVIDER_MODEL", "gpt-5.5-judge-env")

	params := ResolveJudgeConfig(config.JudgeConfig{
		Type:  "agent_judge",
		Model: "judgeprovider/gpt-5.4",
	}, ResolvedAgentConfig{
		Role:     AgentRoleRunner,
		Engine:   "codex",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
	}, nil)

	if params.Model != "gpt-5.5-judge-env" {
		t.Fatalf("Model = %q, want provider env value", params.Model)
	}
	if params.ModelSource != ValueSourceEnv {
		t.Fatalf("ModelSource = %q, want %q", params.ModelSource, ValueSourceEnv)
	}
}

func TestResolveRunnerConfig_UsesGenericProviderScopedEnv(t *testing.T) {
	t.Setenv("DASHSCOPE_MODEL", "qwen-max-env")
	t.Setenv("DASHSCOPE_API_KEY", "dashscope-env-key")
	t.Setenv("DASHSCOPE_BASE_URL", "https://dashscope.example.com")

	params := resolveRunnerConfigForTest("custom", config.ModelConfig{
		Provider: "dashscope",
		Name:     "qwen-max",
	}, nil, nil, "", "")

	if params.Model != "qwen-max-env" || params.ModelSource != ValueSourceEnv {
		t.Fatalf("unexpected model resolution: %#v", params)
	}
	if params.APIKey != "dashscope-env-key" || params.APIKeySource != ValueSourceEnv {
		t.Fatalf("unexpected api-key resolution: %#v", params)
	}
	if params.BaseURL != "https://dashscope.example.com" || params.BaseURLSource != ValueSourceEnv {
		t.Fatalf("unexpected base-url resolution: %#v", params)
	}
}

func TestResolveRunnerConfig_DropsCustomForBuiltinEngine(t *testing.T) {
	custom := &config.CustomEngineConfig{
		Transport: "local",
		Local:     &config.CustomLocalConfig{Command: "/opt/agent"},
	}
	params := resolveRunnerConfigForTest("codex", config.ModelConfig{Name: "auto"}, custom, nil, "", "")

	// A built-in engine ignores engine.custom; it must not leak into params.
	if params.Custom != nil {
		t.Fatalf("Custom = %#v, want nil for a built-in engine", params.Custom)
	}
	if params.Model != "" {
		t.Fatalf("Model = %q, want legacy non-Qoder auto normalization", params.Model)
	}
}

func TestResolveRunnerConfig_PreservesAutoForQoderCLI(t *testing.T) {
	params := resolveRunnerConfigForTest("qodercli", config.ModelConfig{Name: "auto"}, nil, nil, "", "")
	if params.Model != "auto" {
		t.Fatalf("Model = %q, want auto for qodercli", params.Model)
	}
}

func TestResolveRunnerConfig_KeepsCustomForCustomEngine(t *testing.T) {
	custom := &config.CustomEngineConfig{
		Transport: "local",
		Local:     &config.CustomLocalConfig{Command: "/opt/agent"},
	}
	params := resolveRunnerConfigForTest("my-agent", config.ModelConfig{}, custom, nil, "", "")

	if params.Custom == nil {
		t.Fatal("Custom = nil, want it preserved for a custom engine")
	}
}

// HasProvider / ResolveModelRef tests live in provider_query_test.go now
// that those helpers were extracted out of agent_init.go.
