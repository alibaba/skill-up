package credential

import "maps"

// Protocol identifies the wire protocol selected by an agent adapter.
type Protocol string

const (
	// ProtocolAnthropic is the Anthropic-compatible messages protocol.
	ProtocolAnthropic Protocol = "anthropic"
	// ProtocolOpenAI is the OpenAI-compatible protocol.
	ProtocolOpenAI Protocol = "openai"
	// ProtocolQoder is Qoder CLI's managed local protocol and authentication flow.
	ProtocolQoder Protocol = "qoder"
	// ProtocolCustom delegates protocol behavior to a custom engine definition.
	ProtocolCustom Protocol = "custom"
)

// AuthMode records whether skill-up injects authentication or delegates it to
// the agent's local state. Delegation does not prove that a local login exists.
type AuthMode string

const (
	// AuthModeInjected means skill-up selected a non-empty credential to inject.
	AuthModeInjected AuthMode = "injected"
	// AuthModeAgentLocal means skill-up leaves authentication to the agent.
	AuthModeAgentLocal AuthMode = "agent_local"
)

// RoutingMode records how endpoint routing is materialized.
type RoutingMode string

const (
	// RoutingModeExplicit means skill-up selected an explicit endpoint.
	RoutingModeExplicit RoutingMode = "explicit"
	// RoutingModeNativeConfig means an adapter materializes native agent config.
	RoutingModeNativeConfig RoutingMode = "native_config"
	// RoutingModeAgentLocal means endpoint selection is delegated to the agent.
	RoutingModeAgentLocal RoutingMode = "agent_local"
	// RoutingModeUnresolved means an adapter has not selected a routing strategy.
	RoutingModeUnresolved RoutingMode = "unresolved"
)

// ConnectionValue retains both presence and source. Set distinguishes an
// absent value from an explicit empty value that suppresses inheritance.
type ConnectionValue struct {
	Value  string
	Source ValueSource
	Set    bool
}

// ExplicitConnectionValue constructs a present connection value.
func ExplicitConnectionValue(value string, source ValueSource) ConnectionValue {
	return ConnectionValue{Value: value, Source: source, Set: true}
}

// ProviderEndpointConfig holds protocol-specific provider configuration.
type ProviderEndpointConfig struct {
	APIKey  ConnectionValue `json:"-" yaml:"-"`
	BaseURL ConnectionValue
}

// ProviderConfiguration is the static credential and endpoint configuration
// for one provider namespace. Protocol values inherit flat values only when
// the corresponding protocol field is absent.
type ProviderConfiguration struct {
	Provider  string
	APIKey    ConnectionValue `json:"-" yaml:"-"`
	BaseURL   ConnectionValue
	Endpoints map[Protocol]ProviderEndpointConfig
}

// ModelConnectionSpec selects one provider and adapter protocol. Explicit
// values take precedence over environment and credential-file values; an
// explicit empty value intentionally suppresses those lower-priority layers.
type ModelConnectionSpec struct {
	Provider string
	Protocol Protocol
	APIKey   ConnectionValue `json:"-" yaml:"-"`
	BaseURL  ConnectionValue
}

// ResolvedModelConnection describes the connection selected for an adapter.
// It is requested/applied configuration, not observed runtime state. Secrets
// held in APIKey must never be serialized into reports or logs.
type ResolvedModelConnection struct {
	Provider string
	Protocol Protocol

	APIKey        string `json:"-" yaml:"-"`
	BaseURL       string
	APIKeySet     bool
	BaseURLSet    bool
	APIKeySource  ValueSource
	BaseURLSource ValueSource
	AuthMode      AuthMode
	RoutingMode   RoutingMode
}

// GetProviderConfiguration returns a copy of the static provider configuration.
func (r *Resolver) GetProviderConfiguration(provider string) (ProviderConfiguration, bool) {
	if r == nil {
		return ProviderConfiguration{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	providerConfig, ok := r.providers[provider]
	if !ok {
		return ProviderConfiguration{}, false
	}
	providerConfig.Endpoints = cloneProviderEndpoints(providerConfig.Endpoints)
	return providerConfig, true
}

// ResolveModelConnection selects credentials and an endpoint for one
// (provider, protocol) pair without probing the agent or making a model call.
func (r *Resolver) ResolveModelConnection(spec ModelConnectionSpec) ResolvedModelConnection {
	resolved := ResolvedModelConnection{
		Provider:    spec.Provider,
		Protocol:    spec.Protocol,
		AuthMode:    AuthModeAgentLocal,
		RoutingMode: RoutingModeAgentLocal,
	}

	providerConfig, _ := r.GetProviderConfiguration(spec.Provider)
	resolved.APIKey, resolved.APIKeySet, resolved.APIKeySource = resolveConnectionValue(
		spec.APIKey,
		providerEnvironmentValue(spec.Provider, valueAPIKey),
		providerConfigValue(providerConfig, spec.Protocol, valueAPIKey),
	)
	resolved.BaseURL, resolved.BaseURLSet, resolved.BaseURLSource = resolveConnectionValue(
		spec.BaseURL,
		providerEnvironmentValue(spec.Provider, valueBaseURL),
		providerConfigValue(providerConfig, spec.Protocol, valueBaseURL),
	)
	if resolved.APIKey != "" {
		resolved.AuthMode = AuthModeInjected
	}
	if resolved.BaseURL != "" {
		resolved.RoutingMode = RoutingModeExplicit
	}

	return resolved
}

func resolveConnectionValue(values ...ConnectionValue) (string, bool, ValueSource) {
	for _, value := range values {
		if value.Set {
			return value.Value, true, value.Source
		}
	}
	return "", false, ""
}

func providerEnvironmentValue(provider string, kind scopedValueKind) ConnectionValue {
	value, _, ok := lookupProviderEnv(provider, kind)
	if !ok {
		return ConnectionValue{}
	}
	return ExplicitConnectionValue(value, ValueSourceEnv)
}

func providerConfigValue(providerConfig ProviderConfiguration, protocol Protocol, kind scopedValueKind) ConnectionValue {
	endpoint := providerConfig.Endpoints[protocol]
	var protocolValue, flatValue ConnectionValue
	switch kind {
	case valueAPIKey:
		protocolValue, flatValue = endpoint.APIKey, providerConfig.APIKey
	case valueBaseURL:
		protocolValue, flatValue = endpoint.BaseURL, providerConfig.BaseURL
	}
	if protocolValue.Set {
		return protocolValue
	}
	return flatValue
}

func cloneProviderEndpoints(source map[Protocol]ProviderEndpointConfig) map[Protocol]ProviderEndpointConfig {
	return maps.Clone(source)
}
