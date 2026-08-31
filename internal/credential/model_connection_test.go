package credential

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveModelConnection_SelectsProtocolEndpoint(t *testing.T) {
	t.Parallel()

	resolver := loadConnectionResolver(t, `
providers:
  gateway:
    api_key: flat-key
    base_url: https://flat.example.test
    openai:
      api_key: openai-key
      base_url: https://openai.example.test/v1
    anthropic:
      api_key: test-anthropic-value
      base_url: https://anthropic.example.test
`)

	tests := []struct {
		name     string
		protocol Protocol
		apiKey   string
		baseURL  string
	}{
		{name: "openai", protocol: ProtocolOpenAI, apiKey: "openai-key", baseURL: "https://openai.example.test/v1"},
		{name: "anthropic", protocol: ProtocolAnthropic, apiKey: "test-anthropic-value", baseURL: "https://anthropic.example.test"}, //nolint:gosec // test credential
		{name: "other protocol inherits flat values", protocol: ProtocolCustom, apiKey: "flat-key", baseURL: "https://flat.example.test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resolved := resolver.ResolveModelConnection(ModelConnectionSpec{Provider: "gateway", Protocol: tt.protocol})
			if resolved.Provider != "gateway" || resolved.Protocol != tt.protocol {
				t.Fatalf("identity = %q/%q, want gateway/%q", resolved.Provider, resolved.Protocol, tt.protocol)
			}
			if resolved.APIKey != tt.apiKey || resolved.BaseURL != tt.baseURL {
				t.Fatalf("connection = %#v, want api key %q and base URL %q", resolved, tt.apiKey, tt.baseURL)
			}
			if !resolved.APIKeySet || !resolved.BaseURLSet || resolved.APIKeySource != ValueSourceResolver || resolved.BaseURLSource != ValueSourceResolver {
				t.Fatalf("resolver sources were not retained: %#v", resolved)
			}
			if resolved.AuthMode != AuthModeInjected || resolved.RoutingMode != RoutingModeExplicit {
				t.Fatalf("modes = %q/%q, want injected/explicit", resolved.AuthMode, resolved.RoutingMode)
			}
		})
	}
}

func TestResolver_NestedConfigKeepsLegacyFlattenedLookup(t *testing.T) {
	t.Parallel()

	resolver := loadConnectionResolver(t, `
providers:
  gateway:
    openai:
      api_key: openai-key
      base_url: https://openai.example.test/v1
    anthropic:
      api_key: anthropic-key
      base_url: https://anthropic.example.test
`)

	legacy, ok := resolver.Get("gateway")
	if !ok {
		t.Fatal("legacy credential not found")
	}
	if legacy.APIKey != "openai-key" || legacy.BaseURL != "https://openai.example.test/v1" {
		t.Fatalf("legacy flattened credential changed: %#v", legacy)
	}
}

func TestResolveModelConnection_ExplicitEmptySuppressesInheritance(t *testing.T) {
	t.Parallel()

	resolver := loadConnectionResolver(t, `
providers:
  empty_endpoint:
    api_key: flat-key
    base_url: https://flat.example.test
    anthropic:
      api_key: ""
      base_url: ""
    openai:
      api_key: null
      base_url: null
`)

	providerConfig, ok := resolver.GetProviderConfiguration("empty_endpoint")
	if !ok {
		t.Fatal("provider configuration not found")
	}
	for _, protocol := range []Protocol{ProtocolAnthropic, ProtocolOpenAI} {
		endpoint := providerConfig.Endpoints[protocol]
		if !endpoint.APIKey.Set || endpoint.APIKey.Value != "" || !endpoint.BaseURL.Set || endpoint.BaseURL.Value != "" {
			t.Fatalf("explicit empty %s endpoint values were not retained: %#v", protocol, endpoint)
		}

		resolved := resolver.ResolveModelConnection(ModelConnectionSpec{
			Provider: "empty_endpoint",
			Protocol: protocol,
		})
		if resolved.APIKey != "" || resolved.BaseURL != "" || !resolved.APIKeySet || !resolved.BaseURLSet {
			t.Fatalf("explicit empty %s values did not suppress flat inheritance: %#v", protocol, resolved)
		}
		if resolved.APIKeySource != ValueSourceResolver || resolved.BaseURLSource != ValueSourceResolver {
			t.Fatalf("explicit empty %s sources were not retained: %#v", protocol, resolved)
		}
		if resolved.AuthMode != AuthModeAgentLocal || resolved.RoutingMode != RoutingModeAgentLocal {
			t.Fatalf("%s modes = %q/%q, want delegated agent-local state", protocol, resolved.AuthMode, resolved.RoutingMode)
		}
	}
}

func TestResolveModelConnection_PreservesPrecedenceAndExplicitSuppression(t *testing.T) {
	const provider = "connection_precedence_test"
	t.Setenv("CONNECTION_PRECEDENCE_TEST_API_KEY", "env-key")
	t.Setenv("CONNECTION_PRECEDENCE_TEST_BASE_URL", "https://env.example.test")

	resolver := loadConnectionResolver(t, `
providers:
  connection_precedence_test:
    api_key: file-key
    base_url: https://file.example.test
`)

	fromEnvironment := resolver.ResolveModelConnection(ModelConnectionSpec{Provider: provider, Protocol: ProtocolOpenAI})
	if fromEnvironment.APIKey != "env-key" || fromEnvironment.BaseURL != "https://env.example.test" {
		t.Fatalf("environment did not override credential file: %#v", fromEnvironment)
	}
	if fromEnvironment.APIKeySource != ValueSourceEnv || fromEnvironment.BaseURLSource != ValueSourceEnv {
		t.Fatalf("environment sources were not retained: %#v", fromEnvironment)
	}

	explicit := resolver.ResolveModelConnection(ModelConnectionSpec{
		Provider: provider,
		Protocol: ProtocolOpenAI,
		APIKey:   ExplicitConnectionValue("", ValueSourceCLI),
		BaseURL:  ExplicitConnectionValue("https://config.example.test", ValueSourceConfig),
	})
	if explicit.APIKey != "" || !explicit.APIKeySet || explicit.APIKeySource != ValueSourceCLI {
		t.Fatalf("explicit empty API key did not suppress lower layers: %#v", explicit)
	}
	if explicit.BaseURL != "https://config.example.test" || explicit.BaseURLSource != ValueSourceConfig {
		t.Fatalf("explicit base URL did not win: %#v", explicit)
	}
	if explicit.AuthMode != AuthModeAgentLocal || explicit.RoutingMode != RoutingModeExplicit {
		t.Fatalf("modes = %q/%q, want agent_local/explicit", explicit.AuthMode, explicit.RoutingMode)
	}
}

func TestResolvedModelConnection_MarksAPIKeyNonSerializable(t *testing.T) {
	t.Parallel()

	field, ok := reflect.TypeFor[ResolvedModelConnection]().FieldByName("APIKey")
	if !ok {
		t.Fatal("ResolvedModelConnection.APIKey field not found")
	}
	if got := field.Tag.Get("json"); got != "-" {
		t.Fatalf("APIKey json tag = %q, want -", got)
	}
	if got := field.Tag.Get("yaml"); got != "-" {
		t.Fatalf("APIKey yaml tag = %q, want -", got)
	}
}

func loadConnectionResolver(t *testing.T, content string) *Resolver {
	t.Helper()

	path := filepath.Join(t.TempDir(), "credentials.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write credential config: %v", err)
	}
	resolver := NewResolver(path)
	if err := resolver.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return resolver
}
