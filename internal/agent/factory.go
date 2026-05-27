package agent

import (
	"os"

	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/logging"
)

const (
	qoderCLIEngineAlias  = "qoder-cli"
	qoderEngineAlias     = "qoder"
	qoderCLIEngineName   = "qodercli"
	claudeCodeEngineName = "claude-code"
	claudeCodeAlias      = "claude_code"
)

// DetectAgent constructs an agent implementation for the given engine name.
func DetectAgent(engineName string, cfg Config) (Agent, error) {
	if cfg.Name == "" {
		cfg.Name = engineName
	}

	switch engineName {
	case qoderCLIEngineAlias, qoderEngineAlias, qoderCLIEngineName:
		return NewQoderCLIAgent(cfg), nil
	case claudeCodeEngineName, claudeCodeAlias:
		return NewClaudeCodeAgent(cfg), nil
	case codexEngineName:
		return NewCodexAgent(cfg), nil
	default:
		return nil, &UnsupportedAgentError{Name: engineName}
	}
}

// DetectAgentWithInitParams maps resolved init params into an engine-specific agent config.
// kwargs carries engine.kwargs from eval.yaml (or --engine-kwarg overrides) and
// is forwarded as-is to the agent; each agent reads only the keys it understands.
func DetectAgentWithInitParams(engineName string, params credential.AgentInitParams, kwargs map[string]string) (Agent, error) {
	model := params.Model
	// "auto" is a QoderCLI-specific model tier; strip it for other engines
	// so downstream agents don't need to hard-code awareness of it.
	if model == "auto" && !isQoderCLIEngine(engineName) {
		model = ""
	}

	cfg := Config{
		Name:          engineName,
		ModelName:     model,
		ModelProvider: params.Provider,
		APIKey:        params.APIKey,
		BaseURL:       params.BaseURL,
		EnvVars:       make(map[string]string),
		Kwargs:        kwargs,
	}
	logUnknownEngineKwargs(engineName, kwargs)

	switch engineName {
	case qoderCLIEngineAlias, qoderEngineAlias, qoderCLIEngineName:
		// QODER_PERSONAL_ACCESS_TOKEN is qodercli's own auth credential, independent of the
		// underlying model provider (e.g. anthropic). params.APIKey may hold a provider-scoped
		// key (e.g. ANTHROPIC_API_KEY) which must not be forwarded as the qodercli token.
		// See docs/bugfix/Bug_ QODER_PERSONAL_ACCESS_TOKEN is invalid.md for details.
		if token := os.Getenv(credential.EnvQoderPersonalAccessToken); token != "" {
			cfg.EnvVars[credential.EnvQoderPersonalAccessToken] = token
			logging.Debugf("AGENT_CONFIG kind=%s engine=%s auth_env=%s source.auth=process_env", params.Kind, engineName, credential.EnvQoderPersonalAccessToken)
		}
		if params.BaseURL != "" {
			logging.Debugf("AGENT_CONFIG kind=%s engine=%s ignored.base_url reason=unsupported_by_agent", params.Kind, engineName)
		}
	}

	return DetectAgent(engineName, cfg)
}

func isQoderCLIEngine(name string) bool {
	switch name {
	case qoderCLIEngineAlias, qoderEngineAlias, qoderCLIEngineName:
		return true
	default:
		return false
	}
}
