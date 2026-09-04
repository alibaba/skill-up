package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/logging"
)

const testDashscopeProvider = "dashscope"

func TestCapabilitiesForEngine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		engine          string
		protocol        Protocol
		modelPolicy     ModelPolicy
		supportsBaseURL bool
		supportsVersion bool
		kwarg           string
		arbitraryKwargs bool
	}{
		{engine: "claude_code", protocol: ProtocolAnthropic, modelPolicy: ModelPolicyPassthrough, supportsBaseURL: true, supportsVersion: true},
		{engine: "codex", protocol: ProtocolOpenAI, modelPolicy: ModelPolicyCodexProvider, supportsBaseURL: true, supportsVersion: true, kwarg: KwargBypassSandbox},
		{engine: "qoder-cli", protocol: ProtocolQoder, modelPolicy: ModelPolicyQoderTier, kwarg: KwargEdition},
		{engine: "qwen", protocol: ProtocolOpenAI, modelPolicy: ModelPolicyPassthrough, supportsBaseURL: true, supportsVersion: true},
		{engine: "custom-agent", protocol: ProtocolCustom, modelPolicy: ModelPolicyPassthrough},
	}
	for _, tt := range tests {
		t.Run(tt.engine, func(t *testing.T) {
			t.Parallel()
			got := CapabilitiesForEngine(tt.engine)
			if got.Protocol != tt.protocol || got.ModelPolicy != tt.modelPolicy || got.SupportsBaseURL != tt.supportsBaseURL || got.SupportsVersion != tt.supportsVersion || got.ArbitraryKwargs != tt.arbitraryKwargs {
				t.Fatalf("CapabilitiesForEngine(%q) = %+v", tt.engine, got)
			}
			if tt.kwarg != "" && !slices.Contains(got.SupportedKwargs, tt.kwarg) {
				t.Fatalf("CapabilitiesForEngine(%q).SupportedKwargs = %v, want %q", tt.engine, got.SupportedKwargs, tt.kwarg)
			}
		})
	}
}

func TestResolveAdapterConfig_ModelPolicies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		params       credential.ResolvedAgentConfig
		wantProtocol Protocol
		wantApplied  string
		wantProvider string
		wantWarning  string
	}{
		{
			name:         "claude passthrough",
			params:       credential.ResolvedAgentConfig{Engine: "claude_code", Model: " claude-sonnet-4-6 "},
			wantProtocol: ProtocolAnthropic,
			wantApplied:  "claude-sonnet-4-6",
		},
		{
			name:         "codex custom provider",
			params:       credential.ResolvedAgentConfig{Engine: "codex", Provider: testDashscopeProvider, Model: "qwen3.6-plus", BaseURL: "https://example.test/v1"},
			wantProtocol: ProtocolOpenAI,
			wantApplied:  "qwen3.6-plus",
			wantProvider: testDashscopeProvider,
		},
		{
			name:         "codex unusable provider",
			params:       credential.ResolvedAgentConfig{Engine: "codex", Provider: testDashscopeProvider, Model: "qwen3.6-plus"},
			wantProtocol: ProtocolOpenAI,
			wantWarning:  "requires base_url",
		},
		{
			name:         "qoder supported tier",
			params:       credential.ResolvedAgentConfig{Engine: "qodercli", Model: "auto"},
			wantProtocol: ProtocolQoder,
			wantApplied:  "auto",
		},
		{
			name:         "qoder unsupported model",
			params:       credential.ResolvedAgentConfig{Engine: "qodercli", Model: "qwen3.6-plus"},
			wantProtocol: ProtocolQoder,
			wantWarning:  "does not support model",
		},
		{
			name:         "qwen passthrough",
			params:       credential.ResolvedAgentConfig{Engine: "qwen_code", Model: "qwen3-coder-plus"},
			wantProtocol: ProtocolOpenAI,
			wantApplied:  "qwen3-coder-plus",
		},
		{
			name:         "custom passthrough",
			params:       credential.ResolvedAgentConfig{Engine: "my-agent", Model: "opaque/model"},
			wantProtocol: ProtocolCustom,
			wantApplied:  "opaque/model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveAdapterConfig(tt.params, nil)
			if got.Protocol != string(tt.wantProtocol) || got.AppliedProvider != tt.wantProvider || got.AppliedModel != tt.wantApplied {
				t.Fatalf("ResolveAdapterConfig() protocol/provider/model = %q/%q/%q, want %q/%q/%q", got.Protocol, got.AppliedProvider, got.AppliedModel, tt.wantProtocol, tt.wantProvider, tt.wantApplied)
			}
			if got.Model != tt.params.Model {
				t.Fatalf("requested Model = %q, want preserved %q", got.Model, tt.params.Model)
			}
			if tt.wantWarning != "" && !containsWarning(got.Warnings, tt.wantWarning) {
				t.Fatalf("warnings = %v, want substring %q", got.Warnings, tt.wantWarning)
			}
		})
	}
}

func TestResolveAdapterConfig_SelectsConnectionForAdapterProtocol(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "credentials.yaml")
	content := []byte(`
providers:
  adapter_gateway:
    api_key: flat-key
    base_url: https://flat.example.test
    openai:
      api_key: openai-key
      base_url: https://openai.example.test/v1
    anthropic:
      api_key: anthropic-key
      base_url: https://anthropic.example.test
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	resolver := credential.NewResolver(path)
	if err := resolver.Load(); err != nil {
		t.Fatalf("load credentials: %v", err)
	}

	tests := []struct {
		engine   string
		protocol credential.Protocol
		apiKey   string
		baseURL  string
		provider string
	}{
		{engine: "codex", protocol: credential.ProtocolOpenAI, apiKey: "openai-key", baseURL: "https://openai.example.test/v1", provider: "adapter_gateway"},
		{engine: "claude_code", protocol: credential.ProtocolAnthropic, apiKey: "anthropic-key", baseURL: "https://anthropic.example.test", provider: "adapter_gateway"}, //nolint:gosec // test credential
	}
	for _, tt := range tests {
		t.Run(tt.engine, func(t *testing.T) {
			t.Parallel()
			got := ResolveAdapterConfig(credential.ResolvedAgentConfig{
				Engine:        tt.engine,
				Provider:      tt.provider,
				APIKey:        "flat-key",
				BaseURL:       "https://flat.example.test",
				APIKeySource:  credential.ValueSourceResolver,
				BaseURLSource: credential.ValueSourceResolver,
			}, resolver)
			connection := got.AppliedConnection
			if connection.Protocol != tt.protocol || connection.APIKey != tt.apiKey || connection.BaseURL != tt.baseURL {
				t.Fatalf("applied connection = %#v, want %s endpoint", connection, tt.protocol)
			}
			if connection.Provider != tt.provider || connection.AuthMode != credential.AuthModeInjected || connection.RoutingMode != credential.RoutingModeExplicit {
				t.Fatalf("applied connection metadata = %#v", connection)
			}
		})
	}
}

func TestResolveAdapterConfig_PreservesExplicitEmptyConnectionDelegation(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "credentials.yaml")
	if err := os.WriteFile(path, []byte(`
providers:
  local_gateway:
    api_key: flat-key
    base_url: https://flat.example.test
    anthropic:
      api_key: ""
      base_url: ""
`), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	resolver := credential.NewResolver(path)
	if err := resolver.Load(); err != nil {
		t.Fatalf("load credentials: %v", err)
	}

	got := ResolveAdapterConfig(credential.ResolvedAgentConfig{
		Engine:   "claude_code",
		Provider: "local_gateway",
	}, resolver)
	connection := got.AppliedConnection
	if connection.APIKey != "" || connection.BaseURL != "" || !connection.APIKeySet || !connection.BaseURLSet {
		t.Fatalf("explicit empty connection was not preserved: %#v", connection)
	}
	if connection.AuthMode != credential.AuthModeAgentLocal || connection.RoutingMode != credential.RoutingModeAgentLocal {
		t.Fatalf("delegated modes = %q/%q", connection.AuthMode, connection.RoutingMode)
	}
}

func TestResolveAdapterConfig_InheritedJudgeKeepsRunnerProtocolConnection(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "credentials.yaml")
	if err := os.WriteFile(path, []byte(`
providers:
  judge_gateway:
    openai:
      api_key: openai-key
      base_url: https://openai.example.test/v1
    anthropic:
      api_key: anthropic-key
      base_url: https://anthropic.example.test
`), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	resolver := credential.NewResolver(path)
	if err := resolver.Load(); err != nil {
		t.Fatalf("load credentials: %v", err)
	}

	runner := credential.ResolveRunnerConfig(config.EngineConfig{
		Name: "claude_code",
		Model: config.ModelConfig{
			Provider: "judge_gateway",
			Name:     "claude-sonnet-4-6",
		},
	}, resolver, credential.CLIOverrides{})
	runner = ResolveAdapterConfig(runner, resolver)
	judge := credential.ResolveJudgeConfig(config.JudgeConfig{Type: "agent_judge"}, runner, resolver)
	judge = ResolveAdapterConfig(judge, resolver)

	if judge.AppliedConnection.Protocol != credential.ProtocolAnthropic || judge.AppliedConnection.APIKey != "anthropic-key" || judge.AppliedConnection.BaseURL != "https://anthropic.example.test" {
		t.Fatalf("judge connection = %#v, want inherited Anthropic connection", judge.AppliedConnection)
	}
}

func TestResolveAdapterConfig_JudgeProviderConnectionIsolation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fields  string
		apiKey  string
		baseURL string
		keySet  bool
		urlSet  bool
	}{
		{"flat", "    api_key: judge-key\n    base_url: https://judge.example.test\n", "judge-key", "https://judge.example.test", true, true},
		{"protocol", "    api_key: flat-key\n    base_url: https://flat.example.test\n    PROTOCOL:\n      api_key: judge-key\n      base_url: https://judge.example.test\n", "judge-key", "https://judge.example.test", true, true},
		{"missing_key", "    base_url: https://judge.example.test\n", "", "https://judge.example.test", false, true},
		{"missing_endpoint", "    api_key: judge-key\n", "judge-key", "", true, false},
		{"missing_both", "    {}\n", "", "", false, false},
		{"explicit_empty", "    api_key: flat-key\n    base_url: https://flat.example.test\n    PROTOCOL:\n      api_key: \"\"\n      base_url: \"\"\n", "", "", true, true},
	}
	for _, engine := range []string{"claude_code", "qwen_code"} {
		for _, tt := range tests {
			t.Run(engine+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				protocol := CapabilitiesForEngine(engine).Protocol
				path := filepath.Join(t.TempDir(), "credentials.yaml")
				data := "providers:\n  isolated_judge:\n" + strings.ReplaceAll(tt.fields, "PROTOCOL", string(protocol))
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
				resolver := credential.NewResolver(path)
				if err := resolver.Load(); err != nil {
					t.Fatal(err)
				}
				runner := credential.ResolveRunnerConfig(config.EngineConfig{
					Name:  engine,
					Model: config.ModelConfig{Provider: "isolated_runner", Name: "runner-model", BaseURL: "https://runner.example.test"},
				}, resolver, credential.CLIOverrides{APIKey: "runner-key"})
				runner = ResolveAdapterConfig(runner, resolver)
				judge := credential.ResolveJudgeConfig(config.JudgeConfig{Type: "agent_judge", Model: "isolated_judge/judge-model"}, runner, resolver)
				judge = ResolveAdapterConfig(judge, resolver)
				connection := judge.AppliedConnection
				if connection.Provider != "isolated_judge" || connection.Protocol != protocol || connection.APIKey != tt.apiKey || connection.BaseURL != tt.baseURL || connection.APIKeySet != tt.keySet || connection.BaseURLSet != tt.urlSet || judge.APIKeySource == credential.ValueSourceRunner || judge.BaseURLSource == credential.ValueSourceRunner {
					t.Fatalf("unexpected judge connection: %#v", connection)
				}
				if (connection.AuthMode == credential.AuthModeInjected) != (tt.apiKey != "") || (connection.RoutingMode == credential.RoutingModeExplicit) != (tt.baseURL != "") {
					t.Fatalf("unexpected auth/routing modes: %s/%s", connection.AuthMode, connection.RoutingMode)
				}
				assertJudgeAdapterEnvironment(t, judge, tt.apiKey, tt.baseURL)
			})
		}
	}
}

func assertJudgeAdapterEnvironment(t *testing.T, judge credential.ResolvedAgentConfig, apiKey, baseURL string) {
	t.Helper()
	ag, err := DetectAgentWithResolvedConfig(judge)
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]string
	var keyEnv, urlEnv string
	switch a := ag.(type) {
	case *ClaudeCodeAgent:
		keyEnv, urlEnv = credential.EnvAnthropicAPIKey, credential.EnvAnthropicBaseURL
		env = a.credentialEnvVars(keyEnv, urlEnv)
	case *QwenCodeAgent:
		keyEnv, urlEnv = credential.EnvOpenAIAPIKey, credential.EnvOpenAIBaseURL
		env = a.credentialEnvVars(keyEnv, urlEnv)
	default:
		t.Fatalf("unexpected agent type %T", ag)
	}
	if env[keyEnv] != apiKey || env[urlEnv] != baseURL {
		t.Fatal("adapter environment does not match the independent judge connection")
	}
}

func TestResolveAdapterConfig_CodexFallbackDoesNotForwardProviderCredential(t *testing.T) {
	t.Parallel()

	got := ResolveAdapterConfig(credential.ResolvedAgentConfig{ //nolint:gosec // dummy test credential
		Engine: "codex", Provider: testDashscopeProvider, APIKey: "dashscope-test-key",
	}, nil)
	if got.Provider != testDashscopeProvider || got.APIKey != "dashscope-test-key" {
		t.Fatalf("requested provider credential was mutated: %+v", got)
	}
	if got.AppliedProvider != "" || got.AppliedModel != "" || got.AppliedAPIKey != "" || got.AppliedBaseURL != "" {
		t.Fatalf("fallback applied config = provider %q model %q key %q baseURL %q, want all omitted", got.AppliedProvider, got.AppliedModel, got.AppliedAPIKey, got.AppliedBaseURL)
	}
	if got.AppliedConnection.Provider != "" || got.AppliedConnection.APIKeySet || got.AppliedConnection.BaseURLSet || got.AppliedConnection.APIKeySource != "" || got.AppliedConnection.BaseURLSource != "" {
		t.Fatalf("fallback applied connection retained rejected provider configuration: %#v", got.AppliedConnection)
	}
	if !containsWarning(got.Warnings, "provider-scoped credential are omitted") {
		t.Fatalf("Warnings = %v, want provider fallback warning even without a model", got.Warnings)
	}
}

func TestResolveAdapterConfig_ValidatesExplicitSettingsWithoutAliasing(t *testing.T) {
	t.Parallel()

	kwargs := map[string]string{
		KwargBypassSandbox:       "sensitive-invalid-value",
		KwargMaxJSONLRecordBytes: "0",
		"typo":                   "value",
	}
	params := credential.ResolvedAgentConfig{
		Engine:      "codex",
		Version:     "1.2.3",
		Entry:       "codex-custom",
		Model:       "gpt-5.4",
		Kwargs:      kwargs,
		ModelParams: map[string]string{"reasoning": "high"},
	}

	got := ResolveAdapterConfig(params, nil)
	for _, key := range []string{KwargBypassSandbox, KwargMaxJSONLRecordBytes, "typo"} {
		if _, ok := got.Kwargs[key]; ok {
			t.Fatalf("invalid or unsupported kwarg %q was not removed: %v", key, got.Kwargs)
		}
	}
	for _, want := range []string{"engine.entry", "engine.model.params", "requires boolean", "requires positive integer", "does not support kwarg"} {
		if !containsWarning(got.Warnings, want) {
			t.Fatalf("warnings = %v, want substring %q", got.Warnings, want)
		}
	}
	if containsWarning(got.Warnings, "engine.version") {
		t.Fatalf("warnings = %v, did not expect supported codex version warning", got.Warnings)
	}
	if kwargs[KwargBypassSandbox] != "sensitive-invalid-value" || kwargs["typo"] != "value" {
		t.Fatalf("ResolveAdapterConfig mutated source kwargs: %v", kwargs)
	}
	for _, warning := range got.Warnings {
		if strings.Contains(warning, "sensitive-invalid-value") {
			t.Fatalf("warning exposed invalid kwarg value: %q", warning)
		}
	}
	params.ModelParams["reasoning"] = "low"
	if got.ModelParams["reasoning"] != "high" {
		t.Fatalf("resolved ModelParams aliases source: %v", got.ModelParams)
	}
}

func TestResolveAdapterConfig_QoderNormalizesUnsupportedEdition(t *testing.T) {
	t.Parallel()

	got := ResolveAdapterConfig(credential.ResolvedAgentConfig{
		Engine: "qodercli",
		Kwargs: map[string]string{KwargEdition: "enterprise"},
	}, nil)
	if got.Kwargs[KwargEdition] != qoderEditionGlobal {
		t.Fatalf("edition = %q, want %q", got.Kwargs[KwargEdition], qoderEditionGlobal)
	}
	if !containsWarning(got.Warnings, "does not support the configured edition") {
		t.Fatalf("warnings = %v, want unsupported edition warning", got.Warnings)
	}
}

func TestResolveAdapterConfig_QoderWarnsForUnsupportedVersion(t *testing.T) {
	t.Parallel()

	got := ResolveAdapterConfig(credential.ResolvedAgentConfig{Engine: "qodercli", Version: "1.2.3"}, nil)
	if !containsWarning(got.Warnings, "engine.version") {
		t.Fatalf("Warnings = %v, want unsupported version warning", got.Warnings)
	}
}

func TestResolveAdapterConfig_CustomRejectsUnusedTopLevelSettings(t *testing.T) {
	t.Parallel()

	got := ResolveAdapterConfig(credential.ResolvedAgentConfig{
		Engine:  "custom-agent",
		BaseURL: "https://unused.example.test",
		Kwargs:  map[string]string{"profile": "unused"},
	}, nil)
	if got.AppliedBaseURL != "" || len(got.Kwargs) != 0 {
		t.Fatalf("custom applied unused top-level settings: baseURL=%q kwargs=%v", got.AppliedBaseURL, got.Kwargs)
	}
	for _, want := range []string{"does not support base_url", `does not support kwarg "profile"`} {
		if !containsWarning(got.Warnings, want) {
			t.Fatalf("warnings = %v, want substring %q", got.Warnings, want)
		}
	}
}

func TestBaseAgentAnnotateSessionResult(t *testing.T) {
	t.Parallel()

	base := NewBaseAgent(Config{
		Name:               "codex",
		Protocol:           string(ProtocolOpenAI),
		RequestedProvider:  testDashscopeProvider,
		ModelProvider:      testDashscopeProvider,
		RequestedModelName: "requested-model",
		ModelName:          "applied-model",
		Warnings:           []string{"model fallback"},
	})
	result := &SessionResult{}
	base.annotateSessionResult(result)
	if result.Engine != "codex" || result.AppliedProtocol != string(ProtocolOpenAI) || result.RequestedProvider != testDashscopeProvider || result.AppliedProvider != testDashscopeProvider || result.RequestedModel != "requested-model" || result.AppliedModel != "applied-model" || result.Model != "" {
		t.Fatalf("annotated session = %+v", result)
	}
	if !slices.Equal(result.Warnings, []string{"model fallback"}) {
		t.Fatalf("Warnings = %v", result.Warnings)
	}
}

func TestLogAdapterConfig_DoesNotExposeCredentialsOrEndpoint(t *testing.T) {
	logging.SetVerbosity(1)
	defer logging.SetVerbosity(0)

	params := ResolveAdapterConfig(credential.ResolvedAgentConfig{ //nolint:gosec // dummy test credential and URL
		Role:     credential.AgentRoleRunner,
		Engine:   "codex",
		Provider: "openai",
		Model:    "gpt-5.4",
		APIKey:   "secret-api-key",
		BaseURL:  "https://user:secret@example.test/v1",
	}, nil)
	output := captureStdout(t, func() { LogAdapterConfig(context.Background(), params) })
	for _, forbidden := range []string{"secret-api-key", "user:secret", "example.test"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("configuration log exposed %q: %s", forbidden, output)
		}
	}
	for _, want := range []string{"protocol=openai", "requested.provider=openai", "applied.provider=skill-up-openai", "requested.model=gpt-5.4", "applied.model=gpt-5.4"} {
		if !strings.Contains(output, want) {
			t.Fatalf("configuration log missing %q: %s", want, output)
		}
	}
}

func containsWarning(warnings []string, substring string) bool {
	return slices.ContainsFunc(warnings, func(warning string) bool {
		return strings.Contains(warning, substring)
	})
}
