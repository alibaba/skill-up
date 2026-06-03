package config

import (
	"fmt"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
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
	runtimeTypeDocker      = "docker"
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
		errs = append(errs, "environment.type is required (none, opensandbox, docker)")
	} else if !isValidRuntimeType(cfg.Environment.Type) {
		errs = append(errs, "environment.type must be one of: none, opensandbox, docker")
	}

	// Docker-runtime fields that would otherwise silently pass `skill-up
	// validate` and only fail later when NewDockerRuntime constructs the
	// container — catch them at the preflight gate.
	if cfg.Environment.Type == runtimeTypeDocker {
		errs = append(errs, validateDockerEnvironment(cfg.Environment)...)
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

	errs = append(errs, validateCollectArtifacts("cases.defaults.collect_artifacts", cfg.Cases.Defaults.CollectArtifacts)...)

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

	errs = append(errs, validateCollectArtifacts("collect_artifacts", cfg.CollectArtifacts)...)

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

// validateCollectArtifacts checks that every collect_artifacts entry is a
// non-empty, valid doublestar glob. field is the YAML path used in error
// messages (e.g. "cases.defaults.collect_artifacts").
func validateCollectArtifacts(field string, patterns []string) []string {
	var errs []string
	for i, p := range patterns {
		if strings.TrimSpace(p) == "" {
			errs = append(errs, fmt.Sprintf("%s[%d] must not be empty", field, i))
			continue
		}
		if !doublestar.ValidatePattern(p) {
			errs = append(errs, fmt.Sprintf("%s[%d] is not a valid glob pattern: %q", field, i, p))
		}
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
	return t == runtimeTypeNone || t == runtimeTypeOpenSandbox || t == runtimeTypeDocker
}

// validateDockerEnvironment collects preflight errors for fields the docker
// runtime would otherwise reject only at NewDockerRuntime construction time.
// Keep this in sync with the parallel checks in internal/runtime/docker.go
// — both layers enforce, but catching it here turns a runtime-error CI
// failure into a clean validate-step failure.
func validateDockerEnvironment(env Environment) []string {
	var errs []string

	// environment.image: required (opensandbox has its own image-or-
	// sandbox-template fallback, docker does not).
	if strings.TrimSpace(env.Image) == "" {
		errs = append(errs, "environment.image is required when environment.type is docker")
	}

	// environment.workspace_mount: optional, but when set must be
	// absolute — `docker create --workdir <relative>` is rejected, and
	// our Upload/Download paths treat relative values as joined under
	// the workspace which makes no sense if the workspace itself isn't
	// absolute.
	if mount := strings.TrimSpace(env.WorkspaceMount); mount != "" && !path.IsAbs(mount) {
		errs = append(errs, fmt.Sprintf("environment.workspace_mount must be absolute when environment.type is docker, got %q", mount))
	}

	return errs
}

func isValidJudgeType(t string) bool {
	return t == judgeTypeRuleBased || t == judgeTypeScript || t == judgeTypeAgentJudge
}

func validateNetworkPolicy(env Environment) []string {
	policy := env.NetworkPolicy
	if policy == "" {
		if len(env.AllowedEgress) > 0 {
			return []string{"allowed_egress requires network_policy: allow_declared"}
		}
		return nil
	}
	if policy != "deny_all" && policy != "allow_declared" {
		return []string{"network_policy must be one of: deny_all, allow_declared"}
	}
	if env.Type == runtimeTypeNone {
		return []string{"network_policy requires environment.type opensandbox or docker (none cannot enforce network isolation)"}
	}
	// docker runtime currently implements only deny_all (via --network=none).
	// allow_declared needs an egress proxy / iptables sidecar that is not
	// yet in place. Reject early so users do not get a silent "all egress
	// allowed" container at run time.
	if env.Type == runtimeTypeDocker && policy == "allow_declared" {
		return []string{"network_policy: allow_declared is not supported for environment.type docker (use deny_all or opensandbox)"}
	}

	var errs []string
	switch policy {
	case "allow_declared":
		if len(env.AllowedEgress) == 0 {
			errs = append(errs, "network_policy: allow_declared requires a non-empty allowed_egress list")
		}
		for i, target := range env.AllowedEgress {
			t := strings.TrimSpace(target)
			if t == "" {
				errs = append(errs, fmt.Sprintf("allowed_egress[%d] must not be empty", i))
				continue
			}
			if strings.ContainsAny(t, "/ ") {
				errs = append(errs, fmt.Sprintf("allowed_egress[%d] %q must be a bare FQDN or wildcard domain, not a URL", i, target))
			}
		}
	case "deny_all":
		if len(env.AllowedEgress) > 0 {
			errs = append(errs, "allowed_egress is only valid with network_policy: allow_declared")
		}
	}
	return errs
}
