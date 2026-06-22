package config

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/alibaba/skill-up/internal/agentkind"
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

// Custom engine transport constants.
const (
	customTransportLocal = "local"
	customTransportHTTP  = "http"
)

// IsBuiltinEngineName reports whether name matches a built-in agent. A
// built-in engine ignores any engine.custom block. The set of names is
// defined once in internal/agentkind so the agent factory and config
// validator share a single source of truth (no more "keep in sync" lists).
func IsBuiltinEngineName(name string) bool {
	return agentkind.IsBuiltin(name)
}

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

	// engine.custom validation is deferred to ResolveCustomEngineConfig, which
	// runs after CLI overrides settle the final engine name.

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
//
// NOTE: this does NOT validate the engine.custom block. That validation is
// deferred to ResolveCustomEngineConfig because the final engine name is only
// known after CLI overrides (e.g. --engine) settle, and a custom block must
// only be enforced for non-built-in engines. Callers MUST also invoke
// ResolveCustomEngineConfig after applying any CLI overrides; cli/run.go and
// cli/validate.go both do this.
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

// validateEngine checks engine.custom against the Custom Engine contract.
// A non-built-in engine.name requires an engine.custom block; a built-in
// engine.name ignores engine.custom entirely.
func validateEngine(engine EngineConfig) []string {
	if engine.Name == "" {
		return nil
	}
	if IsBuiltinEngineName(engine.Name) {
		return nil
	}
	if engine.Custom == nil {
		return []string{fmt.Sprintf("unsupported agent %q: missing engine.custom", engine.Name)}
	}
	return validateCustomEngine(engine.Custom)
}

func validateCustomEngine(custom *CustomEngineConfig) []string {
	var errs []string

	// Both the local and http transports are implemented (JSON request/response
	// and multipart file upload). URL artifact download in the result is a
	// follow-up.
	switch custom.Transport {
	case "":
		errs = append(errs, "engine.custom.transport is required (local or http)")
	case customTransportLocal:
		if custom.Local == nil || custom.Local.Command == "" {
			errs = append(errs, "engine.custom.local.command is required when transport is local")
		}
	case customTransportHTTP:
		errs = append(errs, validateCustomHTTP(custom.HTTP)...)
	default:
		errs = append(errs, fmt.Sprintf("engine.custom.transport must be \"local\" or \"http\" (got %q)", custom.Transport))
	}

	if custom.ResponseFormat != "" &&
		custom.ResponseFormat != "session_result" && custom.ResponseFormat != "text" {
		errs = append(errs, fmt.Sprintf("engine.custom.response_format must be one of: session_result, text (got %q)", custom.ResponseFormat))
	}

	if custom.TimeoutSeconds < 0 {
		errs = append(errs, "engine.custom.timeout_seconds must be non-negative")
	}

	// kwargs are exposed to templates as ${kwargs.<key>}; a key that
	// collides with a built-in template variable (e.g. `model`, `case_id`)
	// would shadow or be shadowed by the built-in depending on overlay
	// order. Reject the collision so the user fixes the name explicitly.
	for k := range custom.Kwargs {
		if IsBuiltinTemplateVar(k) {
			errs = append(errs, fmt.Sprintf("engine.custom.kwargs key %q collides with a built-in template variable; rename it", k))
		}
	}

	return errs
}

// validateCustomHTTP validates the engine.custom.http block: url is required,
// method must be POST, and each files[].path must be a workspace-relative path
// or glob. URL artifact download in the result is a follow-up.
func validateCustomHTTP(h *CustomHTTPConfig) []string {
	if h == nil {
		return []string{"engine.custom.http.url is required when transport is http"}
	}
	var errs []string
	if h.URL == "" {
		errs = append(errs, "engine.custom.http.url is required when transport is http")
	}
	if h.Method != "" && !strings.EqualFold(h.Method, "POST") {
		errs = append(errs, fmt.Sprintf("engine.custom.http.method must be POST (got %q)", h.Method))
	}
	for i := range h.Files {
		if p := h.Files[i].Path; !WorkspaceRelPathSafe(p) {
			errs = append(errs, fmt.Sprintf(
				"engine.custom.http.files[%d].path %q must be a non-empty relative path inside the workspace (no leading / or ..)", i, p))
		}
	}
	return errs
}

// WorkspaceRelPathSafe reports whether p is a non-empty, workspace-relative
// path or glob: not absolute and with no ".." segment. It is shared by config
// validation and the http transport's file expansion so both apply the same
// rule (agent imports config).
func WorkspaceRelPathSafe(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && !slices.Contains(strings.Split(p, "/"), "..")
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
