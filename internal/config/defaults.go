package config

import (
	_ "embed"

	"gopkg.in/yaml.v3"
)

var defaultEvalConfig *EvalConfig

func init() {
	var err error
	defaultEvalConfig, err = loadDefaults()
	if err != nil {
		panic("failed to load defaults.yaml: " + err.Error())
	}
}

// DefaultEvalConfig returns a deep copy of the default evaluation configuration.
// Each call returns an independent instance that is safe to modify.
func DefaultEvalConfig() *EvalConfig {
	return deepCopyEvalConfig(defaultEvalConfig)
}

// deepCopyEvalConfig creates a deep copy of EvalConfig using YAML serialization.
// This ensures returned instances are independent and safe to modify.
func deepCopyEvalConfig(cfg *EvalConfig) *EvalConfig {
	if cfg == nil {
		return nil
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		panic("failed to marshal EvalConfig: " + err.Error())
	}
	var copied EvalConfig
	if err := yaml.Unmarshal(data, &copied); err != nil {
		panic("failed to unmarshal EvalConfig: " + err.Error())
	}
	copied.Engine.Normalize()
	// Ensure nil slices are initialized to empty slices for consistency
	// (YAML unmarshal may produce nil for empty arrays)
	if copied.MCP.Servers == nil {
		copied.MCP.Servers = []MCPServer{}
	}
	if copied.Skills == nil {
		copied.Skills = []SkillRef{}
	}
	if copied.Cases.Files == nil {
		copied.Cases.Files = []string{}
	}
	if copied.Report.Artifacts == nil {
		copied.Report.Artifacts = []string{}
	}
	if copied.Environment.Env == nil {
		copied.Environment.Env = map[string]string{}
	}

	return &copied
}

func loadDefaults() (*EvalConfig, error) {
	var cfg EvalConfig
	if err := yaml.Unmarshal(defaultsYAML, &cfg); err != nil {
		return nil, err
	}
	cfg.Engine.Normalize()

	return &cfg, nil
}

//go:embed defaults.yaml
var defaultsYAML []byte
