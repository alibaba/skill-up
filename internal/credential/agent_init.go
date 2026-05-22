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
	// Provider-empty was previously a hard guard ("provider_required_for_cli_override"),
	// but that was a defensive paranoia, not a structural requirement. Each agent routes
	// cfg.APIKey to its own hardcoded env (claude_code → ANTHROPIC_API_KEY,
	// codex → OPENAI_API_KEY, see BaseAgent.credentialEnvVars), so the key reaches
	// upstream correctly regardless of Provider. Dropping it here broke the
	// `--api-key K --model literal_opaque/id` flow, where ResolveModelRef
	// rightly returns Provider="" because the prefix isn't a configured namespace.
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

// Provider-existence and slashed-model disambiguation helpers live in
// provider_query.go (Resolver.HasProvider, ResolveModelRef) so that
// agent_init.go stays focused on AgentInitParams construction.

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
