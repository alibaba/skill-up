package agent

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/logging"
)

func TestCapabilitiesForEngine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		engine          string
		protocol        Protocol
		modelPolicy     ModelPolicy
		supportsBaseURL bool
		kwarg           string
		arbitraryKwargs bool
	}{
		{engine: "claude_code", protocol: ProtocolAnthropic, modelPolicy: ModelPolicyPassthrough, supportsBaseURL: true},
		{engine: "codex", protocol: ProtocolOpenAI, modelPolicy: ModelPolicyCodexProvider, supportsBaseURL: true, kwarg: KwargBypassSandbox},
		{engine: "qoder-cli", protocol: ProtocolQoder, modelPolicy: ModelPolicyQoderTier, kwarg: KwargEdition},
		{engine: "qwen", protocol: ProtocolOpenAI, modelPolicy: ModelPolicyPassthrough, supportsBaseURL: true},
		{engine: "custom-agent", protocol: ProtocolCustom, modelPolicy: ModelPolicyPassthrough, supportsBaseURL: true, arbitraryKwargs: true},
	}
	for _, tt := range tests {
		t.Run(tt.engine, func(t *testing.T) {
			t.Parallel()
			got := CapabilitiesForEngine(tt.engine)
			if got.Protocol != tt.protocol || got.ModelPolicy != tt.modelPolicy || got.SupportsBaseURL != tt.supportsBaseURL || got.ArbitraryKwargs != tt.arbitraryKwargs {
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
		name          string
		params        credential.ResolvedAgentConfig
		wantProtocol  Protocol
		wantEffective string
		wantWarning   string
	}{
		{
			name:          "claude passthrough",
			params:        credential.ResolvedAgentConfig{Engine: "claude_code", Model: " claude-sonnet-4-6 "},
			wantProtocol:  ProtocolAnthropic,
			wantEffective: "claude-sonnet-4-6",
		},
		{
			name:          "codex custom provider",
			params:        credential.ResolvedAgentConfig{Engine: "codex", Provider: "dashscope", Model: "qwen3.6-plus", BaseURL: "https://example.test/v1"},
			wantProtocol:  ProtocolOpenAI,
			wantEffective: "qwen3.6-plus",
		},
		{
			name:         "codex unusable provider",
			params:       credential.ResolvedAgentConfig{Engine: "codex", Provider: "dashscope", Model: "qwen3.6-plus"},
			wantProtocol: ProtocolOpenAI,
			wantWarning:  "requires base_url",
		},
		{
			name:          "qoder supported tier",
			params:        credential.ResolvedAgentConfig{Engine: "qodercli", Model: "auto"},
			wantProtocol:  ProtocolQoder,
			wantEffective: "auto",
		},
		{
			name:         "qoder unsupported model",
			params:       credential.ResolvedAgentConfig{Engine: "qodercli", Model: "qwen3.6-plus"},
			wantProtocol: ProtocolQoder,
			wantWarning:  "does not support model",
		},
		{
			name:          "qwen passthrough",
			params:        credential.ResolvedAgentConfig{Engine: "qwen_code", Model: "qwen3-coder-plus"},
			wantProtocol:  ProtocolOpenAI,
			wantEffective: "qwen3-coder-plus",
		},
		{
			name:          "custom passthrough",
			params:        credential.ResolvedAgentConfig{Engine: "my-agent", Model: "opaque/model"},
			wantProtocol:  ProtocolCustom,
			wantEffective: "opaque/model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveAdapterConfig(tt.params)
			if got.Protocol != string(tt.wantProtocol) || got.EffectiveModel != tt.wantEffective {
				t.Fatalf("ResolveAdapterConfig() protocol/effective = %q/%q, want %q/%q", got.Protocol, got.EffectiveModel, tt.wantProtocol, tt.wantEffective)
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

func TestResolveAdapterConfig_ValidatesExplicitSettingsWithoutAliasing(t *testing.T) {
	t.Parallel()

	kwargs := map[string]string{
		KwargBypassSandbox:       "not-a-bool",
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

	got := ResolveAdapterConfig(params)
	for _, key := range []string{KwargBypassSandbox, KwargMaxJSONLRecordBytes, "typo"} {
		if _, ok := got.Kwargs[key]; ok {
			t.Fatalf("invalid or unsupported kwarg %q was not removed: %v", key, got.Kwargs)
		}
	}
	for _, want := range []string{"engine.version", "engine.entry", "engine.model.params", "requires boolean", "requires positive integer", "does not support kwarg"} {
		if !containsWarning(got.Warnings, want) {
			t.Fatalf("warnings = %v, want substring %q", got.Warnings, want)
		}
	}
	if kwargs[KwargBypassSandbox] != "not-a-bool" || kwargs["typo"] != "value" {
		t.Fatalf("ResolveAdapterConfig mutated source kwargs: %v", kwargs)
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
	})
	if got.Kwargs[KwargEdition] != qoderEditionGlobal {
		t.Fatalf("edition = %q, want %q", got.Kwargs[KwargEdition], qoderEditionGlobal)
	}
	if !containsWarning(got.Warnings, "does not support edition") {
		t.Fatalf("warnings = %v, want unsupported edition warning", got.Warnings)
	}
}

func TestBaseAgentAnnotateSessionResult(t *testing.T) {
	t.Parallel()

	base := NewBaseAgent(Config{
		Name:               "codex",
		Protocol:           string(ProtocolOpenAI),
		ModelProvider:      "dashscope",
		RequestedModelName: "requested-model",
		ModelName:          "effective-model",
		Warnings:           []string{"model fallback"},
	})
	result := &SessionResult{}
	base.annotateSessionResult(result)
	if result.Engine != "codex" || result.Protocol != string(ProtocolOpenAI) || result.Provider != "dashscope" || result.RequestedModel != "requested-model" || result.Model != "effective-model" {
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
	})
	output := captureStdout(t, func() { LogAdapterConfig(context.Background(), params) })
	for _, forbidden := range []string{"secret-api-key", "user:secret", "example.test"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("configuration log exposed %q: %s", forbidden, output)
		}
	}
	for _, want := range []string{"protocol=openai", "requested.model=gpt-5.4", "effective.model=gpt-5.4"} {
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
