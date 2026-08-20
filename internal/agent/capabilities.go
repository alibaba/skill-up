package agent

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/alibaba/skill-up/internal/agentkind"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/logging"
)

// Protocol identifies the wire protocol selected by an agent adapter.
type Protocol string

const (
	// ProtocolAnthropic is the Anthropic-compatible messages protocol.
	ProtocolAnthropic Protocol = "anthropic"
	// ProtocolOpenAI is the OpenAI-compatible protocol used by Codex and Qwen Code.
	ProtocolOpenAI Protocol = "openai"
	// ProtocolQoder is Qoder CLI's managed local protocol and authentication flow.
	ProtocolQoder Protocol = "qoder"
	// ProtocolCustom delegates protocol behavior to a custom engine definition.
	ProtocolCustom Protocol = "custom"
)

// ModelPolicy describes how an adapter handles an explicitly requested model.
type ModelPolicy string

const (
	// ModelPolicyPassthrough forwards any non-empty model to the adapter.
	ModelPolicyPassthrough ModelPolicy = "passthrough"
	// ModelPolicyCodexProvider requires a usable Codex provider configuration.
	ModelPolicyCodexProvider ModelPolicy = "codex_provider"
	// ModelPolicyQoderTier accepts only Qoder's named model tiers.
	ModelPolicyQoderTier ModelPolicy = "qoder_tier"
)

// Capabilities declares the configuration surface consumed by an adapter.
// Installation/version behavior remains intentionally deferred to issue #196 PR 5.
type Capabilities struct {
	Protocol        Protocol
	ModelPolicy     ModelPolicy
	SupportsBaseURL bool
	SupportsVersion bool
	SupportsEntry   bool
	SupportsParams  bool
	SupportedKwargs []string
	ArbitraryKwargs bool
}

var (
	codexKwargs = []string{
		KwargBypassSandbox,
		KwargMaxJSONLRecordBytes,
		KwargMaxJSONLOutputBytes,
	}
	qoderKwargs = []string{KwargEdition}
)

// CapabilitiesForEngine returns the explicit configuration contract for an adapter.
func CapabilitiesForEngine(engineName string) Capabilities {
	switch engineName {
	case agentkind.ClaudeCode, agentkind.ClaudeCodeAlias:
		return Capabilities{Protocol: ProtocolAnthropic, ModelPolicy: ModelPolicyPassthrough, SupportsBaseURL: true}
	case agentkind.Codex:
		return Capabilities{
			Protocol:        ProtocolOpenAI,
			ModelPolicy:     ModelPolicyCodexProvider,
			SupportsBaseURL: true,
			SupportedKwargs: slices.Clone(codexKwargs),
		}
	case agentkind.QoderCLI, agentkind.QoderAlias, agentkind.QoderCLIAlias:
		return Capabilities{
			Protocol:        ProtocolQoder,
			ModelPolicy:     ModelPolicyQoderTier,
			SupportedKwargs: slices.Clone(qoderKwargs),
		}
	case agentkind.QwenCode, agentkind.QwenCodeAlias, agentkind.QwenAlias:
		return Capabilities{Protocol: ProtocolOpenAI, ModelPolicy: ModelPolicyPassthrough, SupportsBaseURL: true}
	default:
		return Capabilities{
			Protocol:        ProtocolCustom,
			ModelPolicy:     ModelPolicyPassthrough,
			SupportsBaseURL: true,
			ArbitraryKwargs: true,
		}
	}
}

// ResolveAdapterConfig applies an adapter's capability contract to an already
// merged role configuration. Requested values remain intact while AppliedModel
// records the model that skill-up will forward to the CLI. It does not claim
// that the CLI ultimately selected that model: local CLI configuration may
// override it, and most adapters do not report their final runtime choice.
func ResolveAdapterConfig(params credential.ResolvedAgentConfig) credential.ResolvedAgentConfig {
	if params.Role == "" {
		params.Role = credential.AgentRoleRunner
	}
	params.Kwargs = maps.Clone(params.Kwargs)
	params.ModelParams = maps.Clone(params.ModelParams)
	params.Warnings = slices.Clone(params.Warnings)
	params.AppliedAPIKey = params.APIKey
	params.AppliedBaseURL = params.BaseURL

	capabilities := CapabilitiesForEngine(params.Engine)
	params.Protocol = string(capabilities.Protocol)
	params.AppliedProvider = resolveAppliedProvider(&params, capabilities)
	params.AppliedModel = resolveAppliedModel(&params, capabilities)
	validateBaseURL(&params, capabilities)
	validateDeferredFields(&params, capabilities)
	validateKwargs(&params, capabilities)
	return params
}

func resolveAppliedProvider(params *credential.ResolvedAgentConfig, capabilities Capabilities) string {
	switch capabilities.ModelPolicy {
	case ModelPolicyQoderTier:
		// Qoder owns provider routing and authentication. A provider namespace
		// can participate in requested-value compatibility, but it is not sent
		// to qodercli.
		params.AppliedAPIKey = ""
		params.AppliedBaseURL = ""
		return ""
	case ModelPolicyCodexProvider:
		if reason := codexCustomProviderUnavailableReason(params.Provider, params.BaseURL); reason != "" {
			params.Warnings = appendUniqueWarning(params.Warnings, fmt.Sprintf(
				"engine %q cannot apply provider %q: provider %s; the provider override, endpoint, and provider-scoped credential are omitted and local Codex settings will be used",
				params.Engine, params.Provider, reason,
			))
			params.AppliedAPIKey = ""
			params.AppliedBaseURL = ""
			return ""
		}
		if params.BaseURL == "" {
			// Codex receives no model_provider override in this case. Even an
			// explicit provider namespace cannot prove which local provider Codex
			// ultimately selects.
			return ""
		}
		if params.Provider == "" || params.Provider == agentProviderOpenAI {
			return codexOpenAIOverrideProvider
		}
	}
	return params.Provider
}

func resolveAppliedModel(params *credential.ResolvedAgentConfig, capabilities Capabilities) string {
	requested := strings.TrimSpace(params.Model)
	if requested == "" {
		return ""
	}

	switch capabilities.ModelPolicy {
	case ModelPolicyQoderTier:
		if slices.Contains(supportedQoderModels, requested) {
			return requested
		}
		params.Warnings = appendUniqueWarning(params.Warnings, fmt.Sprintf(
			"engine %q does not support model %q; the model override is omitted and local Qoder settings will be used",
			params.Engine, requested,
		))
		return ""
	case ModelPolicyCodexProvider:
		if reason := codexCustomProviderUnavailableReason(params.Provider, params.BaseURL); reason != "" {
			return ""
		}
	}

	return requested
}

func validateBaseURL(params *credential.ResolvedAgentConfig, capabilities Capabilities) {
	if params.BaseURL == "" || capabilities.SupportsBaseURL {
		return
	}
	params.Warnings = appendUniqueWarning(params.Warnings, fmt.Sprintf(
		"engine %q does not support base_url; the configured endpoint is ignored",
		params.Engine,
	))
	params.AppliedBaseURL = ""
}

func validateDeferredFields(params *credential.ResolvedAgentConfig, capabilities Capabilities) {
	if params.Version != "" && !capabilities.SupportsVersion {
		params.Warnings = appendUniqueWarning(params.Warnings, fmt.Sprintf(
			"engine %q does not yet enforce engine.version; the configured version %q is not guaranteed",
			params.Engine, params.Version,
		))
	}
	if params.Entry != "" && !capabilities.SupportsEntry {
		params.Warnings = appendUniqueWarning(params.Warnings, fmt.Sprintf(
			"engine %q does not support engine.entry; the configured entry is ignored",
			params.Engine,
		))
	}
	if len(params.ModelParams) != 0 && !capabilities.SupportsParams {
		params.Warnings = appendUniqueWarning(params.Warnings, fmt.Sprintf(
			"engine %q does not support engine.model.params; the configured parameters are ignored",
			params.Engine,
		))
	}
}

func validateKwargs(params *credential.ResolvedAgentConfig, capabilities Capabilities) {
	if len(params.Kwargs) == 0 || capabilities.ArbitraryKwargs {
		return
	}

	supported := make(map[string]struct{}, len(capabilities.SupportedKwargs))
	for _, key := range capabilities.SupportedKwargs {
		supported[key] = struct{}{}
	}
	keys := make([]string, 0, len(params.Kwargs))
	for key := range params.Kwargs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := params.Kwargs[key]
		if _, ok := supported[key]; !ok {
			params.Warnings = appendUniqueWarning(params.Warnings, fmt.Sprintf(
				"engine %q does not support kwarg %q; the configured value is ignored",
				params.Engine, key,
			))
			delete(params.Kwargs, key)
			continue
		}
		validateKwargValue(params, key, value)
	}
}

func validateKwargValue(params *credential.ResolvedAgentConfig, key, value string) {
	switch key {
	case KwargEdition:
		edition := strings.ToLower(strings.TrimSpace(value))
		if edition == "" || edition == qoderEditionGlobal || edition == qoderEditionCN {
			return
		}
		params.Warnings = appendUniqueWarning(params.Warnings, fmt.Sprintf(
			"engine %q does not support edition %q; edition %q will be used",
			params.Engine, value, qoderEditionGlobal,
		))
		params.Kwargs[key] = qoderEditionGlobal
	case KwargBypassSandbox:
		if _, err := strconv.ParseBool(strings.TrimSpace(value)); err == nil {
			return
		}
		params.Warnings = appendUniqueWarning(params.Warnings, fmt.Sprintf(
			"engine %q requires boolean kwarg %q; invalid value %q is ignored",
			params.Engine, key, value,
		))
		delete(params.Kwargs, key)
	case KwargMaxJSONLRecordBytes, KwargMaxJSONLOutputBytes:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil && parsed > 0 {
			return
		}
		params.Warnings = appendUniqueWarning(params.Warnings, fmt.Sprintf(
			"engine %q requires positive integer kwarg %q; invalid value %q is ignored",
			params.Engine, key, value,
		))
		delete(params.Kwargs, key)
	}
}

func appendUniqueWarning(warnings []string, warning string) []string {
	if slices.Contains(warnings, warning) {
		return warnings
	}
	return append(warnings, warning)
}

// LogAdapterConfig reports requested/effective values and actionable warnings.
func LogAdapterConfig(ctx context.Context, params credential.ResolvedAgentConfig) {
	logging.DebugContextf(
		ctx,
		"AGENT_CONFIG kind=%s engine=%s protocol=%s requested.provider=%s applied.provider=%s requested.model=%s applied.model=%s",
		params.Role, params.Engine, params.Protocol, params.Provider, params.AppliedProvider, params.Model, params.AppliedModel,
	)
	for _, warning := range params.Warnings {
		logging.WarnContextf(ctx, "AGENT_CONFIG kind=%s engine=%s warning=%s", params.Role, params.Engine, warning)
	}
}
