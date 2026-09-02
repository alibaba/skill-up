package agent

import (
	"fmt"
	"os"

	"github.com/alibaba/skill-up/internal/agentkind"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/logging"
)

// DetectAgent constructs an agent implementation for the given engine name.
func DetectAgent(engineName string, cfg Config) (Agent, error) {
	if cfg.Name == "" {
		cfg.Name = engineName
	}

	switch engineName {
	case agentkind.QoderCLIAlias, agentkind.QoderAlias, agentkind.QoderCLI:
		return NewQoderCLIAgent(cfg), nil
	case agentkind.ClaudeCodeAlias, agentkind.ClaudeCode:
		return NewClaudeCodeAgent(cfg), nil
	case agentkind.Codex:
		return NewCodexAgent(cfg), nil
	case agentkind.QwenCode, agentkind.QwenCodeAlias, agentkind.QwenAlias:
		return NewQwenCodeAgent(cfg), nil
	default:
		// A non-built-in engine name is a Custom Engine when engine.custom
		// is configured; otherwise it is unsupported.
		if cfg.Custom != nil {
			return NewCustomAgent(cfg), nil
		}
		return nil, &UnsupportedAgentError{Name: engineName}
	}
}

// DetectAgentWithResolvedConfig maps a resolved role configuration into the
// selected adapter without reinterpreting raw YAML or CLI values.
func DetectAgentWithResolvedConfig(params credential.ResolvedAgentConfig) (Agent, error) {
	connection := params.AppliedConnection
	if connection.Protocol == "" {
		return nil, fmt.Errorf("agent config for engine %q was not materialized", params.Engine)
	}
	engineName := params.Engine
	cfg := Config{
		Name:               engineName,
		Version:            params.Version,
		Entry:              params.Entry,
		ModelName:          params.AppliedModel,
		RequestedModelName: params.Model,
		RequestedProvider:  params.Provider,
		ModelProvider:      connection.Provider,
		Protocol:           string(connection.Protocol),
		Warnings:           params.Warnings,
		APIKey:             connection.APIKey,
		BaseURL:            connection.BaseURL,
		EnvVars:            make(map[string]string),
		Kwargs:             params.Kwargs,
		Custom:             params.Custom,
	}
	logUnknownEngineKwargs(engineName, params.Kwargs)

	switch engineName {
	case agentkind.QoderCLIAlias, agentkind.QoderAlias, agentkind.QoderCLI:
		// The selected edition's PAT is qodercli's own auth credential, independent of the
		// underlying model provider (e.g. anthropic). params.APIKey may hold a provider-scoped
		// key (e.g. ANTHROPIC_API_KEY) which must not be forwarded as the qodercli token.
		// See docs/bugfix/Bug_ QODER_PERSONAL_ACCESS_TOKEN is invalid.md for details.
		profile := qoderProfileForKwargs(params.Kwargs)
		sourceEnv := profile.credentialEnv
		token := os.Getenv(sourceEnv)
		if token == "" && profile.edition == qoderEditionCN {
			sourceEnv = credential.EnvQoderCNAccessToken
			token = os.Getenv(sourceEnv)
		}
		if token != "" {
			cfg.EnvVars[profile.credentialEnv] = token
			logging.Debugf(
				"AGENT_CONFIG kind=%s engine=%s edition=%s auth_env=%s source.auth=process_env source.env=%s",
				params.Role, engineName, profile.edition, profile.credentialEnv, sourceEnv,
			)
		}
		if params.BaseURL != "" {
			logging.Debugf("AGENT_CONFIG kind=%s engine=%s ignored.base_url reason=unsupported_by_agent", params.Role, engineName)
		}
	}

	return DetectAgent(engineName, cfg)
}
