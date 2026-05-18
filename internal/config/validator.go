package config

import (
	"fmt"
	"strings"
)

// Judge type constants.
const (
	judgeTypeRuleBased    = "rule_based"
	judgeTypeScript       = "script"
	judgeTypeAgentJudge   = "agent_judge"
	maxRetryPolicyRetries = 10
)

// Runtime type constants.
const (
	runtimeTypeNone        = "none"
	runtimeTypeOpenSandbox = "opensandbox"
)

// Validator checks eval and case documents against the v1alpha1 schema.
type Validator struct{}

// NewValidator creates a new Validator.
func NewValidator() *Validator {
	return &Validator{}
}

// ValidateEvalConfig validates an eval configuration.
func (v *Validator) ValidateEvalConfig(cfg *EvalConfig) error {
	var errs []string

	// schema_version is required and must be v1alpha1
	if cfg.SchemaVersion == "" {
		errs = append(errs, "schema_version is required")
	} else if cfg.SchemaVersion != "v1alpha1" {
		errs = append(errs, fmt.Sprintf("schema_version must be 'v1alpha1', got '%s'", cfg.SchemaVersion))
	}

	// environment.type is required
	if cfg.Environment.Type == "" {
		errs = append(errs, "environment.type is required (none, opensandbox)")
	} else if !isValidRuntimeType(cfg.Environment.Type) {
		errs = append(errs, "environment.type must be one of: none, opensandbox")
	}

	errs = append(errs, validateNetworkPolicy(cfg.Environment)...)

	// engine.name is required
	if cfg.Engine.Name == "" {
		errs = append(errs, "engine.name is required")
	}

	// engine.model.provider and engine.model.name are optional.
	// When omitted, the engine uses its local default model configuration.

	// cases.files is required and must have at least one file
	if len(cfg.Cases.Files) == 0 {
		errs = append(errs, "cases.files must contain at least one case file")
	}
	if cfg.Cases.RetryPolicy.MaxRetries > maxRetryPolicyRetries {
		errs = append(errs, fmt.Sprintf("cases.retry_policy.max_retries must be <= %d", maxRetryPolicyRetries))
	}

	if len(errs) > 0 {
		return fmt.Errorf("validation errors:\n  - %s", strings.Join(errs, "\n  - "))
	}

	return nil
}

// ValidateCaseConfig validates a case configuration.
func (v *Validator) ValidateCaseConfig(cfg *CaseConfig) error {
	var errs []string

	// id is optional - Loader auto-generates from filename if not specified
	// See loader.go:LoadCaseConfig for the fallback logic

	// input.prompt or input.turns is required
	if cfg.Input.Prompt == "" && len(cfg.Input.Turns) == 0 {
		errs = append(errs, "input.prompt or input.turns is required")
	}

	// if turns is specified, each turn must have role and content
	for i, turn := range cfg.Input.Turns {
		if turn.Role == "" {
			errs = append(errs, fmt.Sprintf("input.turns[%d].role is required", i))
		}
		if turn.Content == "" {
			errs = append(errs, fmt.Sprintf("input.turns[%d].content is required", i))
		}
	}

	// validate judge config if specified at case level
	if cfg.Judge.Type != "" {
		errs = append(errs, validateJudgeTypeAndFields(cfg.Judge)...)
	}

	if len(errs) > 0 {
		return fmt.Errorf("validation errors:\n  - %s", strings.Join(errs, "\n  - "))
	}

	return nil
}

// validateJudgeTypeAndFields validates judge type and its conditional fields.
func validateJudgeTypeAndFields(judge JudgeConfig) []string {
	var errs []string

	if judge.Type != "" && !isValidJudgeType(judge.Type) {
		errs = append(errs, "judge.type must be one of: rule_based, script, agent_judge")
	}

	// script type requires script_path
	if judge.Type == judgeTypeScript && judge.ScriptPath == "" {
		errs = append(errs, "judge.script_path is required when judge.type is script")
	}

	// agent_judge type requires model and criteria
	if judge.Type == judgeTypeAgentJudge && judge.Model == "" {
		errs = append(errs, "judge.model is required when judge.type is agent_judge")
	}

	if judge.Type == judgeTypeAgentJudge && len(judge.Criteria) == 0 {
		errs = append(errs, "judge.criteria is required when judge.type is agent_judge")
	}

	errs = append(errs, validatePassThreshold(judge.PassThreshold)...)

	if judge.TimeoutSeconds != nil && *judge.TimeoutSeconds < 0 {
		errs = append(errs, "judge.timeout_seconds must be non-negative")
	}

	return errs
}

func validatePassThreshold(threshold *float64) []string {
	if threshold == nil {
		return nil
	}
	if *threshold < 0.0 || *threshold > 1.0 {
		return []string{"judge.pass_threshold must be between 0.0 and 1.0"}
	}
	return nil
}

// ValidateAll validates an eval config and all its cases.
func (v *Validator) ValidateAll(result *EvalResult) error {
	if err := v.ValidateEvalConfig(result.Eval); err != nil {
		return err
	}

	for _, c := range result.Cases {
		if err := v.ValidateCaseConfig(c); err != nil {
			return fmt.Errorf("case %s: %w", c.ID, err)
		}
	}

	return nil
}

func isValidRuntimeType(t string) bool {
	return t == runtimeTypeNone || t == runtimeTypeOpenSandbox
}

func isValidJudgeType(t string) bool {
	return t == judgeTypeRuleBased || t == judgeTypeScript || t == judgeTypeAgentJudge
}

func validateNetworkPolicy(env Environment) []string {
	policy := env.NetworkPolicy
	if policy == "" {
		return nil
	}
	if policy != "deny_all" && policy != "allow_declared" {
		return []string{"network_policy must be one of: deny_all, allow_declared"}
	}
	if env.Type == runtimeTypeNone {
		return []string{"network_policy requires environment.type opensandbox (none cannot enforce network isolation)"}
	}
	return nil
}
