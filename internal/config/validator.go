package config

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/alibaba/skill-up/internal/agentkind"
	"github.com/alibaba/skill-up/internal/customengine"
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
	errs = append(errs, validateExpect("cases.defaults.expect", cfg.Cases.Defaults.Expect)...)
	errs = append(errs, validateJudgeTypeAndLocalFields(cfg.Judge)...)

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

	// input.prompt or input.turns is required, but not both
	if cfg.Input.Prompt == "" && len(cfg.Input.Turns) == 0 {
		errs = append(errs, "input.prompt or input.turns is required")
	}
	if cfg.Input.Prompt != "" && len(cfg.Input.Turns) > 0 {
		errs = append(errs, "input.prompt and input.turns are mutually exclusive")
	}

	// if turns is specified, each turn must have role=user and content
	for i, turn := range cfg.Input.Turns {
		if turn.Role == "" {
			errs = append(errs, fmt.Sprintf("input.turns[%d].role is required", i))
		} else if turn.Role != "user" {
			errs = append(errs, fmt.Sprintf("input.turns[%d].role must be \"user\", got %q", i, turn.Role))
		}
		if turn.Content == "" {
			errs = append(errs, fmt.Sprintf("input.turns[%d].content is required", i))
		}
		if turn.TimeoutSeconds < 0 {
			errs = append(errs, fmt.Sprintf("input.turns[%d].timeout_seconds must be non-negative", i))
		}
		if turn.PostCondition != nil {
			errs = append(errs, validatePostCondition(i, turn.PostCondition)...)
		}
		for j, cr := range turn.Capture {
			errs = append(errs, validateCaptureRule(i, j, cr)...)
		}
	}

	// validate per-turn judge rules reference valid turn numbers
	turnsTotal := len(cfg.Input.Turns)
	if judgeNeedsLocalValidation(cfg.Judge) {
		errs = append(errs, validateJudgeTypeAndLocalFields(cfg.Judge)...)
	}
	var allRules []Rule
	allRules = append(allRules, cfg.Judge.Success...)
	allRules = append(allRules, cfg.Judge.Failure...)
	errs = append(errs, validatePerTurnRules(allRules, turnsTotal)...)

	errs = append(errs, validateCollectArtifacts("collect_artifacts", cfg.CollectArtifacts)...)
	errs = append(errs, validateExpect("expect", cfg.Expect)...)

	errs = append(errs, validateCaseMCP(cfg.MCP)...)

	if len(errs) > 0 {
		return fmt.Errorf("validation errors:\n  - %s", strings.Join(errs, "\n  - "))
	}

	return nil
}

func validateExpect(prefix string, expect Expect) []string {
	var errs []string
	validateStrings := func(field string, values []string) {
		for i, value := range values {
			if strings.TrimSpace(value) == "" {
				errs = append(errs, fmt.Sprintf("%s.%s[%d] must not be empty", prefix, field, i))
			}
		}
	}

	validateStrings("must_contain", expect.MustContain)
	validateStrings("must_not_contain", expect.MustNotContain)
	validateStrings("files_exist", expect.FilesExist)
	validateStrings("files_not_exist", expect.FilesNotExist)

	for i, check := range expect.FileContains {
		if strings.TrimSpace(check.Path) == "" {
			errs = append(errs, fmt.Sprintf("%s.file_contains[%d].path is required", prefix, i))
		}
		if strings.TrimSpace(check.Content) == "" {
			errs = append(errs, fmt.Sprintf("%s.file_contains[%d].content is required", prefix, i))
		}
	}

	return errs
}

// validateCaseMCP validates case-level MCP overrides. The MVP restricts
// case-level servers to mocked fixture variation: every server needs a name,
// must use mode: mocked, must be unique within the case, and must not declare
// real-server transport fields (transport: http, endpoint, command, args).
func validateCaseMCP(mcpCfg MCPConfig) []string {
	var errs []string
	seen := make(map[string]struct{}, len(mcpCfg.Servers))
	for i, server := range mcpCfg.Servers {
		if server.Name == "" {
			errs = append(errs, fmt.Sprintf("mcp.servers[%d].name is required", i))
			continue
		}
		if _, dup := seen[server.Name]; dup {
			errs = append(errs, fmt.Sprintf("mcp.servers[%d].name %q is duplicated", i, server.Name))
			continue
		}
		seen[server.Name] = struct{}{}

		if server.Mode != mcpModeMocked {
			errs = append(errs, fmt.Sprintf("mcp.servers[%d] (%q) must use mode: mocked at case level", i, server.Name))
		}
		if server.Mode == mcpModeMocked && server.Name != builtinFilesystemMCPServer && strings.TrimSpace(server.ConfigRef) == "" {
			errs = append(errs, fmt.Sprintf("mcp.servers[%d] (%q) mocked mode requires config_ref", i, server.Name))
		}
		if server.Transport != "" && server.Transport != "stdio" {
			errs = append(errs, fmt.Sprintf("mcp.servers[%d] (%q) mocked mode only supports stdio transport", i, server.Name))
		}
		if server.Endpoint != "" {
			errs = append(errs, fmt.Sprintf("mcp.servers[%d] (%q) mocked mode does not support endpoint", i, server.Name))
		}
		if server.Command != "" || len(server.Args) > 0 {
			errs = append(errs, fmt.Sprintf("mcp.servers[%d] (%q) mocked mode does not support command/args", i, server.Name))
		}
	}
	return errs
}

func judgeNeedsLocalValidation(judge JudgeConfig) bool {
	return judge.Type != "" || len(judge.Skills) > 0 || judge.Context != nil
}

// validateJudgeTypeAndLocalFields validates judge fields that do not depend on inheritance.
func validateJudgeTypeAndLocalFields(judge JudgeConfig) []string {
	var errs []string

	if judge.Type != "" && !isValidJudgeType(judge.Type) {
		errs = append(errs, "judge.type must be one of: rule_based, script, agent_judge")
	}
	if len(judge.Skills) > 0 && judge.Type != judgeTypeAgentJudge {
		errs = append(errs, "judge.skills is only supported when judge.type is agent_judge")
	}
	errs = append(errs, validateSkillRefs("judge.skills", judge.Skills)...)

	errs = append(errs, validatePassThreshold(judge.PassThreshold)...)
	errs = append(errs, validateJudgeContext(judge.Context)...)

	if judge.TimeoutSeconds != nil && *judge.TimeoutSeconds < 0 {
		errs = append(errs, "judge.timeout_seconds must be non-negative")
	}

	return errs
}

func validateJudgeTypeAndRequiredFields(judge JudgeConfig) []string {
	errs := validateJudgeTypeAndLocalFields(judge)
	if judge.Type == judgeTypeScript && judge.ScriptPath == "" {
		errs = append(errs, "judge.script_path is required when judge.type is script")
	}
	if judge.Type == judgeTypeAgentJudge && judge.Model == "" {
		errs = append(errs, "judge.model is required when judge.type is agent_judge")
	}
	if judge.Type == judgeTypeAgentJudge && len(judge.Criteria) == 0 {
		errs = append(errs, "judge.criteria is required when judge.type is agent_judge")
	}
	return errs
}

func validateSkillRefs(field string, refs []SkillRef) []string {
	var errs []string
	for i, ref := range refs {
		if strings.TrimSpace(ref.Source) == "" {
			errs = append(errs, fmt.Sprintf("%s[%d].source is required", field, i))
		}
		if strings.TrimSpace(ref.Path) == "" {
			errs = append(errs, fmt.Sprintf("%s[%d].path is required", field, i))
		}
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

func validateJudgeContext(ctx *JudgeContextConfig) []string {
	if ctx == nil {
		return nil
	}

	var errs []string
	if ctx.Profile != "" && ctx.Profile != "minimal" && ctx.Profile != "standard" {
		errs = append(errs, "judge.context.profile must be one of: minimal, standard")
	}
	errs = append(errs, validateJudgeContextModes(ctx)...)
	errs = append(errs, validateJudgeContextLimits(ctx.Limits)...)
	for i, attachment := range ctx.Attachments {
		if strings.TrimSpace(attachment.Path) == "" {
			errs = append(errs, fmt.Sprintf("judge.context.attachments[%d].path must not be empty", i))
		}
	}
	return errs
}

func validateJudgeContextModes(ctx *JudgeContextConfig) []string {
	var errs []string
	for field, mode := range map[string]string{
		"final_message":  ctx.FinalMessage,
		"transcript":     ctx.Transcript,
		"workspace_diff": ctx.WorkspaceDiff,
	} {
		if mode != "" && !isValidJudgeContextMode(mode) {
			errs = append(errs, fmt.Sprintf("judge.context.%s must be one of: include, omit, truncate, file_ref", field))
		}
	}
	if ctx.GeneratedFiles != "" && ctx.GeneratedFiles != "omit" && ctx.GeneratedFiles != "index" && ctx.GeneratedFiles != "include" {
		errs = append(errs, "judge.context.generated_files must be one of: omit, index, include")
	}
	return errs
}

func validateJudgeContextLimits(limits *JudgeContextLimits) []string {
	if limits == nil {
		return nil
	}
	var errs []string
	if limits.MaxBytes < 0 {
		errs = append(errs, "judge.context.limits.max_bytes must be non-negative")
	}
	if limits.TranscriptMaxTurns < 0 {
		errs = append(errs, "judge.context.limits.transcript_max_turns must be non-negative")
	}
	if limits.WorkspaceDiffMaxLines < 0 {
		errs = append(errs, "judge.context.limits.workspace_diff_max_lines must be non-negative")
	}
	return errs
}

func isValidJudgeContextMode(mode string) bool {
	switch mode {
	case "include", "omit", "truncate", "file_ref":
		return true
	default:
		return false
	}
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
	return v.ValidateCasesWithEvalDefaults(result.Eval, result.Cases)
}

// ValidateCases validates a set of cases: it rejects duplicate effective IDs
// and duplicate source-file references within the set, then validates each case
// document.
//
// `skill-up run` calls this with only the cases left after include/exclude
// filters, so an invalid unselected case never blocks a filtered run;
// `skill-up validate` passes every case (via ValidateAll) for full-suite
// validation. Duplicate detection keys off each case's ID and loader-populated
// SourceFile, so it is correct for any subset, not only the full in-order suite.
func (v *Validator) ValidateCases(cases []*CaseConfig) error {
	if errs := duplicateCaseErrors(cases); len(errs) > 0 {
		return fmt.Errorf("validation errors:\n  - %s", strings.Join(errs, "\n  - "))
	}

	for _, c := range cases {
		if err := v.ValidateCaseConfig(c); err != nil {
			return fmt.Errorf("case %s: %w", c.ID, err)
		}
	}

	return nil
}

// ValidateCasesWithEvalDefaults validates already-loaded cases and effective
// judge settings after applying eval-level defaults. Unlike ValidateAll, it
// does not require eval.cases.files and is therefore suitable for filtered
// runs and Anthropic evals.json auto mode where cases are loaded directly.
func (v *Validator) ValidateCasesWithEvalDefaults(eval *EvalConfig, cases []*CaseConfig) error {
	if err := v.ValidateCases(cases); err != nil {
		return err
	}
	for _, c := range cases {
		effectiveJudge := mergeJudgeConfigForValidation(eval.Judge, c.Judge)
		if errs := validateJudgeTypeAndRequiredFields(effectiveJudge); len(errs) > 0 {
			return fmt.Errorf("case %s: validation errors:\n  - %s", c.ID, strings.Join(errs, "\n  - "))
		}
	}
	return nil
}

func mergeJudgeConfigForValidation(global, caseLevel JudgeConfig) JudgeConfig {
	if caseLevel.Type != "" {
		merged := caseLevel
		if merged.Model == "" {
			merged.Model = global.Model
		}
		if merged.PassThreshold == nil {
			merged.PassThreshold = global.PassThreshold
		}
		if merged.TimeoutSeconds == nil {
			merged.TimeoutSeconds = global.TimeoutSeconds
		}
		return merged
	}
	return global
}

// duplicateCaseErrors reports duplicate source-file references and duplicate
// effective case IDs within the given case set. skill-up writes reports and
// artifacts keyed by case ID and runs one case per cases.files entry, so a
// collision would silently overwrite results or double-run a case.
func duplicateCaseErrors(cases []*CaseConfig) []string {
	var errs []string
	errs = append(errs, duplicateCaseFileErrors(cases)...)
	errs = append(errs, duplicateCaseIDErrors(cases)...)
	return errs
}

// duplicateCaseFileErrors reports cases loaded from the same source file after
// path normalization (e.g. "cases/a.yaml" vs "./cases/a.yaml"). Cases with no
// SourceFile (e.g. loaded from an Anthropic evals.json rather than cases.files)
// are skipped. Each duplicate is reported once, in first-seen order.
func duplicateCaseFileErrors(cases []*CaseConfig) []string {
	counts := make(map[string]int, len(cases))
	for _, c := range cases {
		if c.SourceFile == "" {
			continue
		}
		counts[path.Clean(c.SourceFile)]++
	}
	var errs []string
	reported := make(map[string]bool)
	for _, c := range cases {
		if c.SourceFile == "" {
			continue
		}
		norm := path.Clean(c.SourceFile)
		if counts[norm] > 1 && !reported[norm] {
			errs = append(errs, fmt.Sprintf("cases.files lists %q %d times (after path normalization); each case file must appear once", norm, counts[norm]))
			reported[norm] = true
		}
	}
	return errs
}

// duplicateCaseIDErrors reports effective case IDs shared by more than one case,
// covering both explicit `id:` fields and filename-derived IDs. Each case's
// SourceFile (when known) names the offending file in the message.
func duplicateCaseIDErrors(cases []*CaseConfig) []string {
	idFiles := make(map[string][]string, len(cases))
	var order []string
	for _, c := range cases {
		src := c.SourceFile
		if src == "" {
			src = "<unknown source>"
		}
		if _, seen := idFiles[c.ID]; !seen {
			order = append(order, c.ID)
		}
		idFiles[c.ID] = append(idFiles[c.ID], src)
	}
	var errs []string
	for _, id := range order {
		if srcs := idFiles[id]; len(srcs) > 1 {
			errs = append(errs, fmt.Sprintf("duplicate case id %q used by %d case files: %s", id, len(srcs), strings.Join(srcs, ", ")))
		}
	}
	return errs
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

func validateCustomEngine(custom *customengine.Config) []string {
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
// or glob.
func validateCustomHTTP(h *customengine.HTTPConfig) []string {
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
		if p := h.Files[i].Path; !customengine.WorkspaceRelPathSafe(p) {
			errs = append(errs, fmt.Sprintf(
				"engine.custom.http.files[%d].path %q must be a non-empty relative path inside the workspace (no leading / or ..)", i, p))
		}
	}
	return errs
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

// captureVariablePattern is the allowed pattern for capture variable names.
var captureVariablePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validatePostCondition validates a turn's post_condition block.
func validatePostCondition(turnIdx int, pc *PostCondition) []string {
	var errs []string
	prefix := fmt.Sprintf("input.turns[%d].post_condition", turnIdx)

	// on_fail must be empty, "fail", or "skip_remaining"
	if pc.OnFail != "" && pc.OnFail != "fail" && pc.OnFail != "skip_remaining" {
		errs = append(errs, fmt.Sprintf("%s.on_fail must be empty, \"fail\", or \"skip_remaining\", got %q", prefix, pc.OnFail))
	}

	// Must include at least one check field
	if len(pc.MustContainAny) == 0 && len(pc.MustContainAll) == 0 && len(pc.MustNotContain) == 0 {
		errs = append(errs, prefix+" must include at least one of must_contain_any, must_contain_all, or must_not_contain")
	}

	return errs
}

// validateCaptureRule validates a single capture rule.
func validateCaptureRule(turnIdx, capIdx int, cr CaptureRule) []string {
	var errs []string
	prefix := fmt.Sprintf("input.turns[%d].capture[%d]", turnIdx, capIdx)

	// variable must match identifier pattern
	if cr.Variable == "" {
		errs = append(errs, prefix+".variable is required")
	} else if !captureVariablePattern.MatchString(cr.Variable) {
		errs = append(errs, fmt.Sprintf("%s.variable %q must match pattern [A-Za-z_][A-Za-z0-9_]*", prefix, cr.Variable))
	}

	// Exactly one extractor must be specified
	hasPattern := cr.Pattern != ""
	hasJSONPath := cr.JSONPath != ""
	if !hasPattern && !hasJSONPath {
		errs = append(errs, prefix+" must specify exactly one extractor: pattern or jsonpath")
	} else if hasPattern && hasJSONPath {
		errs = append(errs, prefix+" must specify exactly one extractor: pattern or jsonpath (both set)")
	}

	// Regex pattern must compile
	if hasPattern {
		if _, err := regexp.Compile(cr.Pattern); err != nil {
			errs = append(errs, fmt.Sprintf("%s.pattern is invalid regex: %v", prefix, err))
		}
	}

	return errs
}

// validatePerTurnRules validates per-turn judge rules reference valid turn numbers.
func validatePerTurnRules(rules []Rule, turnsTotal int) []string {
	var errs []string
	for _, rule := range rules {
		if rule.TurnResponseContains != nil {
			errs = append(errs, validateTurnResponseContainsRule(rule.TurnResponseContains, turnsTotal)...)
		}
		if rule.TurnResponseNotContains != nil {
			errs = append(errs, validateTurnResponseNotContainsRule(rule.TurnResponseNotContains, turnsTotal)...)
		}
		if rule.ToolCalledInTurn != nil {
			errs = append(errs, validateToolCalledInTurnRule(rule.ToolCalledInTurn, turnsTotal)...)
		}
		if rule.ToolNotCalledInTurn != nil {
			errs = append(errs, validateToolNotCalledInTurnRule(rule.ToolNotCalledInTurn, turnsTotal)...)
		}
	}
	return errs
}

func validateTurnResponseContainsRule(r *TurnResponseContainsRule, turnsTotal int) []string {
	var errs []string
	if r.Turn < 1 {
		errs = append(errs, "turn_response_contains.turn must be >= 1")
	} else if turnsTotal > 0 && r.Turn > turnsTotal {
		errs = append(errs, fmt.Sprintf("turn_response_contains.turn %d exceeds total turns %d", r.Turn, turnsTotal))
	}
	if len(r.ContainsAll) == 0 && len(r.ContainsAny) == 0 {
		errs = append(errs, "turn_response_contains must specify contains_all or contains_any")
	}
	return errs
}

func validateTurnResponseNotContainsRule(r *TurnResponseNotContainsRule, turnsTotal int) []string {
	var errs []string
	if r.Turn < 1 {
		errs = append(errs, "turn_response_not_contains.turn must be >= 1")
	} else if turnsTotal > 0 && r.Turn > turnsTotal {
		errs = append(errs, fmt.Sprintf("turn_response_not_contains.turn %d exceeds total turns %d", r.Turn, turnsTotal))
	}
	if len(r.NotContains) == 0 {
		errs = append(errs, "turn_response_not_contains.not_contains is required")
	}
	return errs
}

func validateToolCalledInTurnRule(r *ToolCalledInTurnRule, turnsTotal int) []string {
	var errs []string
	if r.Turn < 1 {
		errs = append(errs, "tool_called_in_turn.turn must be >= 1")
	} else if turnsTotal > 0 && r.Turn > turnsTotal {
		errs = append(errs, fmt.Sprintf("tool_called_in_turn.turn %d exceeds total turns %d", r.Turn, turnsTotal))
	}
	if r.Name == "" {
		errs = append(errs, "tool_called_in_turn.name is required")
	}
	return errs
}

func validateToolNotCalledInTurnRule(r *ToolNotCalledInTurnRule, turnsTotal int) []string {
	var errs []string
	if r.Turn < 1 {
		errs = append(errs, "tool_not_called_in_turn.turn must be >= 1")
	} else if turnsTotal > 0 && r.Turn > turnsTotal {
		errs = append(errs, fmt.Sprintf("tool_not_called_in_turn.turn %d exceeds total turns %d", r.Turn, turnsTotal))
	}
	if r.Name == "" {
		errs = append(errs, "tool_not_called_in_turn.name is required")
	}
	return errs
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
