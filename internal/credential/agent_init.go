package credential

import (
	"maps"
	"os"
	"strings"

	"github.com/alibaba/skill-up/internal/agentkind"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/customengine"
	"github.com/alibaba/skill-up/internal/logging"
)

// AgentRole identifies which evaluation agent a resolved config targets.
type AgentRole string

const (
	// AgentRoleRunner is the primary agent that executes a case.
	AgentRoleRunner AgentRole = "runner"
	// AgentRoleJudge is the agent used by agent_judge evaluation.
	AgentRoleJudge AgentRole = "judge"
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

// ResolvedAgentConfig is the role-aware configuration passed into agent initialization.
// It is built once after YAML, CLI, environment, and credential-file inputs are
// available. Mutable data is cloned during construction so later mutations of
// EvalConfig cannot change an already resolved value.
type ResolvedAgentConfig struct {
	Role     AgentRole
	Engine   string
	Version  string
	Entry    string
	Protocol string

	Provider       string
	Model          string
	EffectiveModel string
	APIKey         string
	BaseURL        string
	Kwargs         map[string]string
	ModelParams    map[string]string
	Warnings       []string

	// Custom carries the custom engine config when the engine name does not
	// match a built-in agent. It is nil for built-in agents.
	Custom *config.CustomEngineConfig

	ProviderSource ValueSource
	ModelSource    ValueSource
	APIKeySource   ValueSource
	BaseURLSource  ValueSource
}

// CLIOverrides contains explicit runner-only command-line overrides.
type CLIOverrides struct {
	Model  string
	APIKey string
}

type agentResolveInput struct {
	role        AgentRole
	engine      config.EngineConfig
	provider    string
	model       string
	baseURL     string
	valueSource ValueSource
	fallback    *ResolvedAgentConfig
	resolver    *Resolver
	cli         CLIOverrides
}

// ResolveRunnerConfig resolves the final configuration for the runner agent.
func ResolveRunnerConfig(engine config.EngineConfig, resolver *Resolver, cli CLIOverrides) ResolvedAgentConfig {
	return resolveResolvedAgentConfig(agentResolveInput{
		role:        AgentRoleRunner,
		engine:      engine,
		provider:    engine.Model.Provider,
		model:       engine.Model.Name,
		baseURL:     engine.Model.BaseURL,
		valueSource: ValueSourceConfig,
		resolver:    resolver,
		cli:         cli,
	})
}

// ResolveJudgeConfig resolves the final configuration for the judge agent.
// The judge inherits the runner engine lifecycle and kwargs until an explicit
// judge-engine schema is introduced, but its model/provider resolution is a
// separate role-aware pass.
func ResolveJudgeConfig(judgeCfg config.JudgeConfig, runner ResolvedAgentConfig, resolver *Resolver) ResolvedAgentConfig {
	provider, model := parseJudgeModel(judgeCfg.Model)
	var fallback *ResolvedAgentConfig
	if runner.Provider != "" || runner.Model != "" || runner.APIKey != "" || runner.BaseURL != "" {
		fallback = &runner
	}

	return resolveResolvedAgentConfig(agentResolveInput{
		role: AgentRoleJudge,
		engine: config.EngineConfig{
			Name:    runner.Engine,
			Version: runner.Version,
			Entry:   runner.Entry,
			Kwargs:  maps.Clone(runner.Kwargs),
			Custom:  runner.Custom,
			Model: config.ModelConfig{
				Params: maps.Clone(runner.ModelParams),
			},
		},
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

func resolveResolvedAgentConfig(in agentResolveInput) ResolvedAgentConfig {
	// A built-in engine ignores any engine.custom block, so it is not carried
	// into the init params — otherwise downstream logic (e.g. the model "auto"
	// strip) would mistake a built-in engine for a custom one.
	custom := customengine.CloneConfig(in.engine.Custom)
	if config.IsBuiltinEngineName(in.engine.Name) {
		custom = nil
	}
	params := ResolvedAgentConfig{
		Role:        in.role,
		Engine:      in.engine.Name,
		Version:     in.engine.Version,
		Entry:       in.engine.Entry,
		Provider:    in.provider,
		Model:       in.model,
		BaseURL:     in.baseURL,
		Kwargs:      maps.Clone(in.engine.Kwargs),
		ModelParams: maps.Clone(in.engine.Model.Params),
		Custom:      custom,
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

	applyCLIModelOverride(&params, in.cli.Model, in.resolver)
	applyFallback(&params, in.fallback)
	resolveProviderScopedFields(&params, in.resolver)
	applyFallbackCredentials(&params, in.fallback)
	applyCLIAPIKeyOverride(&params, in.cli.APIKey)
	normalizeLegacyModel(&params)
	logResolvedAgentConfig(params)

	return params
}

func applyFallback(params *ResolvedAgentConfig, fallback *ResolvedAgentConfig) {
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

func applyFallbackCredentials(params *ResolvedAgentConfig, fallback *ResolvedAgentConfig) {
	if fallback == nil {
		return
	}
	if params.APIKey == "" && fallback.APIKey != "" {
		params.APIKey = fallback.APIKey
		params.APIKeySource = ValueSourceRunner
	}
}

func applyCLIModelOverride(params *ResolvedAgentConfig, cliModel string, resolver *Resolver) {
	if cliModel != "" {
		params.Provider, params.Model = ResolveModelRef(cliModel, resolver)
		if params.Provider != "" {
			params.ProviderSource = ValueSourceCLI
		} else {
			params.ProviderSource = ""
		}
		params.ModelSource = ValueSourceCLI
	}
}

func applyCLIAPIKeyOverride(params *ResolvedAgentConfig, cliAPIKey string) {
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
	// This also subsumes the custom-engine case: a custom engine references the
	// CLI key explicitly via ${api_key} and never had a provider to begin with.
	params.APIKey = cliAPIKey
	params.APIKeySource = ValueSourceCLI
}

func normalizeLegacyModel(params *ResolvedAgentConfig) {
	if params.Model != "auto" || params.Custom != nil {
		return
	}
	switch params.Engine {
	case agentkind.QoderCLI, agentkind.QoderAlias, agentkind.QoderCLIAlias:
		return
	default:
		params.Model = ""
	}
}

func resolveProviderScopedFields(params *ResolvedAgentConfig, resolver *Resolver) {
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

func resolveValue(params *ResolvedAgentConfig, kind scopedValueKind, resolver *Resolver) {
	// A CLI model is applied before provider lookup so its provider prefix can
	// select credentials, but it must retain the historical highest precedence.
	if kind == valueModel && params.ModelSource == ValueSourceCLI {
		return
	}
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
// agent_init.go stays focused on ResolvedAgentConfig construction.

func setResolvedValue(params *ResolvedAgentConfig, kind scopedValueKind, value string, source ValueSource) {
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

func logProviderEnvResolution(params *ResolvedAgentConfig, kind scopedValueKind, envVar string) {
	switch kind {
	case valueModel:
		logging.Debugf("AGENT_CONFIG kind=%s engine=%s provider=%s model_env=%s source.model=%s",
			params.Role, params.Engine, params.Provider, envVar, ValueSourceEnv)
	case valueAPIKey:
		logging.Debugf("AGENT_CONFIG kind=%s engine=%s provider=%s auth_configured=true source.auth=%s",
			params.Role, params.Engine, params.Provider, ValueSourceEnv)
	case valueBaseURL:
		logging.Debugf("AGENT_CONFIG kind=%s engine=%s provider=%s base_url_env=%s source.base_url=%s",
			params.Role, params.Engine, params.Provider, envVar, ValueSourceEnv)
	}
}

func logResolvedAgentConfig(params ResolvedAgentConfig) {
	if params.Provider != "" {
		logging.Debugf("AGENT_CONFIG kind=%s engine=%s provider=%s source.provider=%s",
			params.Role, params.Engine, params.Provider, params.ProviderSource)
	}
	if params.Model != "" {
		logging.Debugf("AGENT_CONFIG kind=%s engine=%s model=%s source.model=%s",
			params.Role, params.Engine, params.Model, params.ModelSource)
	}
	if params.APIKey != "" {
		// Do not log the credential, its masked form, or fields derived from its
		// resolution path. The resolved config retains APIKeySource for callers
		// that need programmatic diagnostics without placing it in log output.
		logging.Debugf("AGENT_CONFIG kind=%s engine=%s auth_configured=true",
			params.Role, params.Engine)
	}
	if params.BaseURL != "" {
		logging.Debugf("AGENT_CONFIG kind=%s engine=%s base_url=%s source.base_url=%s",
			params.Role, params.Engine, params.BaseURL, params.BaseURLSource)
	}
}
