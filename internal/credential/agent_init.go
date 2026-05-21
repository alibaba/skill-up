package credential

import (
	"os"
	"strings"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/logging"
)

// AgentKind identifies which evaluation agent a resolved config targets.
type AgentKind string

const (
	// AgentKindRunner is the primary agent that executes a case.
	AgentKindRunner AgentKind = "runner"
	// AgentKindJudge is the agent used by agent_judge evaluation.
	AgentKindJudge AgentKind = "judge"
)

// ValueSource records where a resolved config value came from.
type ValueSource string

const (
	// ValueSourceConfig indicates the eval engine config.
	ValueSourceConfig ValueSource = "config"
	// ValueSourceJudge indicates the judge-specific config.
	ValueSourceJudge ValueSource = "judge_config"
	// ValueSourceRunner indicates fallback from runner config.
	ValueSourceRunner ValueSource = "runner"
	// ValueSourceEnv indicates a provider-scoped environment variable.
	ValueSourceEnv ValueSource = "env"
	// ValueSourceResolver indicates credential resolver state.
	ValueSourceResolver ValueSource = "resolver"
	// ValueSourceCLI indicates a CLI override.
	ValueSourceCLI ValueSource = "cli"
)

// AgentInitParams is the resolved configuration passed into agent initialization.
type AgentInitParams struct {
	Kind   AgentKind
	Engine string

	Provider string
	Model    string
	APIKey   string
	BaseURL  string

	ProviderSource ValueSource
	ModelSource    ValueSource
	APIKeySource   ValueSource
	BaseURLSource  ValueSource
}

type agentResolveInput struct {
	kind        AgentKind
	engine      string
	provider    string
	model       string
	baseURL     string
	valueSource ValueSource
	fallback    *AgentInitParams
	resolver    *Resolver
	cliModel    string
	cliAPIKey   string
}

// ResolveRunnerInitParams resolves the final init params for the runner agent.
func ResolveRunnerInitParams(engine string, modelCfg config.ModelConfig, resolver *Resolver, cliModel string, cliAPIKey string) AgentInitParams {
	return resolveAgentInitParams(agentResolveInput{
		kind:        AgentKindRunner,
		engine:      engine,
		provider:    modelCfg.Provider,
		model:       modelCfg.Name,
		baseURL:     modelCfg.BaseURL,
		valueSource: ValueSourceConfig,
		resolver:    resolver,
		cliModel:    cliModel,
		cliAPIKey:   cliAPIKey,
	})
}

// ResolveJudgeInitParams resolves the final init params for the judge agent.
func ResolveJudgeInitParams(engine string, judgeCfg config.JudgeConfig, runner AgentInitParams, resolver *Resolver) AgentInitParams {
	provider, model := parseJudgeModel(judgeCfg.Model)
	var fallback *AgentInitParams
	if runner.Provider != "" || runner.Model != "" || runner.APIKey != "" || runner.BaseURL != "" {
		fallback = &runner
	}

	return resolveAgentInitParams(agentResolveInput{
		kind:        AgentKindJudge,
		engine:      engine,
		provider:    provider,
		model:       model,
		valueSource: ValueSourceJudge,
		fallback:    fallback,
		resolver:    resolver,
	})
}

func parseJudgeModel(value string) (provider, model string) {
	parts := strings.SplitN(value, "/", 2)
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1]
	}

	return "", value
}

func resolveAgentInitParams(in agentResolveInput) AgentInitParams {
	params := AgentInitParams{
		Kind:     in.kind,
		Engine:   in.engine,
		Provider: in.provider,
		Model:    in.model,
		BaseURL:  in.baseURL,
	}
	if params.Provider != "" {
		params.ProviderSource = in.valueSource
	}
	if params.Model != "" {
		params.ModelSource = in.valueSource
	}
	if params.BaseURL != "" {
		params.BaseURLSource = in.valueSource
	}

	applyFallback(&params, in.fallback)
	resolveProviderScopedFields(&params, in.resolver)
	applyFallbackCredentials(&params, in.fallback)
	applyCLIOverrides(&params, in.cliModel, in.cliAPIKey)
	logResolvedAgentConfig(params)

	return params
}

func applyFallback(params *AgentInitParams, fallback *AgentInitParams) {
	if fallback == nil {
		return
	}
	if params.Provider == "" && fallback.Provider != "" {
		params.Provider = fallback.Provider
		params.ProviderSource = ValueSourceRunner
	}
	if params.Model == "" && fallback.Model != "" {
		params.Model = fallback.Model
		params.ModelSource = ValueSourceRunner
	}
	if params.BaseURL == "" && fallback.BaseURL != "" {
		params.BaseURL = fallback.BaseURL
		params.BaseURLSource = ValueSourceRunner
	}
}

func applyFallbackCredentials(params *AgentInitParams, fallback *AgentInitParams) {
	if fallback == nil {
		return
	}
	if params.APIKey == "" && fallback.APIKey != "" {
		params.APIKey = fallback.APIKey
		params.APIKeySource = ValueSourceRunner
	}
}

func applyCLIOverrides(params *AgentInitParams, cliModel string, cliAPIKey string) {
	if cliModel != "" {
		params.Model = cliModel
		params.ModelSource = ValueSourceCLI
	}
	if cliAPIKey == "" {
		return
	}
	if params.Provider == "" {
		logging.Warnf("kind=%s engine=%s ignored.api_key reason=provider_required_for_cli_override", params.Kind, params.Engine)
		return
	}
	params.APIKey = cliAPIKey
	params.APIKeySource = ValueSourceCLI
}

func resolveProviderScopedFields(params *AgentInitParams, resolver *Resolver) {
	if params.Provider == "" {
		return
	}

	resolveValue(params, valueModel, resolver)
	resolveValue(params, valueAPIKey, resolver)
	resolveValue(params, valueBaseURL, resolver)
}

type scopedValueKind string

const (
	valueModel   scopedValueKind = "MODEL"
	valueAPIKey  scopedValueKind = "API_KEY"
	valueBaseURL scopedValueKind = "BASE_URL"
)

func resolveValue(params *AgentInitParams, kind scopedValueKind, resolver *Resolver) {
	if value, envVar, ok := lookupProviderEnv(params.Provider, kind); ok {
		setResolvedValue(params, kind, value, ValueSourceEnv)
		logProviderEnvResolution(params, kind, envVar)
		return
	}
	if kind == valueModel || resolver == nil {
		return
	}
	cred, ok := resolver.Get(params.Provider)
	if !ok {
		return
	}
	switch kind {
	case valueAPIKey:
		if cred.APIKey != "" {
			setResolvedValue(params, kind, cred.APIKey, ValueSourceResolver)
		}
	case valueBaseURL:
		if params.BaseURL == "" && cred.BaseURL != "" {
			setResolvedValue(params, kind, cred.BaseURL, ValueSourceResolver)
		}
	}
}

func lookupProviderEnv(provider string, kind scopedValueKind) (value, envVar string, ok bool) {
	if provider == "" {
		return "", "", false
	}
	envVar = strings.ToUpper(provider) + "_" + string(kind)
	value = os.Getenv(envVar)
	if value == "" {
		return "", envVar, false
	}
	return value, envVar, true
}

// ProviderConfigured reports whether the caller has supplied a configuration
// path for this provider — i.e. the credential resolver has an entry for it,
// or one of `<PROVIDER>_API_KEY` / `<PROVIDER>_BASE_URL` /
// `<PROVIDER>_PERSONAL_ACCESS_TOKEN` is set in the process env. The PAT env
// is the canonical Qoder credential (see EnvQoderPersonalAccessToken /
// QoderCLIAgent.CheckCredentials), so probing it makes qoder-only-via-PAT
// setups count as configured.
//
// This is used to disambiguate the two valid interpretations of a slashed
// `--model provider/name` input:
//
//   - When the provider IS configured, the user intends `provider` as a
//     credential namespace (e.g. `provider: dashscope, name: claude-sonnet-4-6`
//     uses dashscope's API key but talks to a bare Anthropic model id) and a
//     split is appropriate.
//   - When the provider is NOT configured, the slashed string is more likely
//     a literal model identifier the upstream API expects verbatim
//     (e.g. `anthropic_modelscope/deepseek-v4-pro` registered as-is on an
//     internal anthropic-proxy gateway). Callers can treat it as opaque.
//
// Note: this helper intentionally does NOT consider CLI overrides such as
// `--api-key`; the CLI layer treats those as a separate "configured via
// CLI" signal when deciding whether to collapse a tentative split, so that
// `ProviderConfigured` itself stays purely about persisted/env-resident
// configuration. Returns false for empty provider input.
func ProviderConfigured(provider string, resolver *Resolver) bool {
	if provider == "" {
		return false
	}
	if resolver != nil {
		if _, ok := resolver.Get(provider); ok {
			return true
		}
	}
	if _, _, ok := lookupProviderEnv(provider, valueAPIKey); ok {
		return true
	}
	if _, _, ok := lookupProviderEnv(provider, valueBaseURL); ok {
		return true
	}
	// QODER_PERSONAL_ACCESS_TOKEN-style auth: provider-scoped PAT env that
	// some engines (Qoder) use instead of `<PROVIDER>_API_KEY`.
	if v := os.Getenv(strings.ToUpper(provider) + "_PERSONAL_ACCESS_TOKEN"); v != "" {
		return true
	}
	return false
}

func setResolvedValue(params *AgentInitParams, kind scopedValueKind, value string, source ValueSource) {
	switch kind {
	case valueModel:
		params.Model = value
		params.ModelSource = source
	case valueAPIKey:
		params.APIKey = value
		params.APIKeySource = source
	case valueBaseURL:
		params.BaseURL = value
		params.BaseURLSource = source
	}
}

func logProviderEnvResolution(params *AgentInitParams, kind scopedValueKind, envVar string) {
	switch kind {
	case valueModel:
		logging.Debugf("AGENT_CONFIG kind=%s engine=%s provider=%s model_env=%s source.model=%s",
			params.Kind, params.Engine, params.Provider, envVar, ValueSourceEnv)
	case valueAPIKey:
		logging.Debugf("AGENT_CONFIG kind=%s engine=%s provider=%s auth_env=%s source.auth=%s",
			params.Kind, params.Engine, params.Provider, envVar, ValueSourceEnv)
	case valueBaseURL:
		logging.Debugf("AGENT_CONFIG kind=%s engine=%s provider=%s base_url_env=%s source.base_url=%s",
			params.Kind, params.Engine, params.Provider, envVar, ValueSourceEnv)
	}
}

func logResolvedAgentConfig(params AgentInitParams) {
	if params.Provider != "" {
		logging.Debugf("AGENT_CONFIG kind=%s engine=%s provider=%s source.provider=%s",
			params.Kind, params.Engine, params.Provider, params.ProviderSource)
	}
	if params.Model != "" {
		logging.Debugf("AGENT_CONFIG kind=%s engine=%s model=%s source.model=%s",
			params.Kind, params.Engine, params.Model, params.ModelSource)
	}
	if params.APIKey != "" {
		logging.Debugf("AGENT_CONFIG kind=%s engine=%s api_key=%s source.api_key=%s",
			params.Kind, params.Engine, MaskAPIKey(params.APIKey), params.APIKeySource)
	}
	if params.BaseURL != "" {
		logging.Debugf("AGENT_CONFIG kind=%s engine=%s base_url=%s source.base_url=%s",
			params.Kind, params.Engine, params.BaseURL, params.BaseURLSource)
	}
}
