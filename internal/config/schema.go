package config

import (
	"time"

	"github.com/alibaba/skill-up/internal/runtime"
)

// EvalConfig is the root document loaded from evals/eval.yaml (v1alpha1).
type EvalConfig struct {
	SchemaVersion string          `yaml:"schema_version"`
	Environment   Environment     `yaml:"environment"`
	MCP           MCPConfig       `yaml:"mcp"`
	Skills        []SkillRef      `yaml:"skills"`
	Engine        EngineConfig    `yaml:"engine"`
	Cases         CasesConfig     `yaml:"cases"`
	Judge         JudgeConfig     `yaml:"judge"`
	Benchmark     BenchmarkConfig `yaml:"benchmark"`
	Report        ReportConfig    `yaml:"report"`
}

// Environment defines the runtime environment for evaluation.
type Environment struct {
	Type                  string            `yaml:"type"` // none, opensandbox, docker
	Image                 string            `yaml:"image,omitempty"`
	SandboxTemplate       string            `yaml:"sandbox_template,omitempty"`
	WorkspaceMount        string            `yaml:"workspace_mount,omitempty"`
	Env                   map[string]string `yaml:"env,omitempty"`
	SetupSteps            []SetupStep       `yaml:"setup_steps,omitempty"`
	UseServerProxy        bool              `yaml:"use_server_proxy,omitempty"`
	ReadyTimeoutSeconds   int               `yaml:"ready_timeout_seconds,omitempty"`
	SandboxTimeoutSeconds int               `yaml:"sandbox_timeout_seconds,omitempty"`
	Entrypoint            []string          `yaml:"entrypoint,omitempty"`
	Metadata              map[string]string `yaml:"metadata,omitempty"`
	Kwargs                map[string]string `yaml:"kwargs,omitempty"`
	NetworkPolicy         string            `yaml:"network_policy,omitempty"` // deny_all, allow_declared
	AllowedEgress         []string          `yaml:"allowed_egress,omitempty"` // FQDN/wildcard egress allowlist for allow_declared
}

// ToRuntimeConfig converts Environment to runtime.Config.
func (e Environment) ToRuntimeConfig() runtime.Config {
	setupSteps := make([]runtime.SetupStep, len(e.SetupSteps))
	for i, s := range e.SetupSteps {
		setupSteps[i] = runtime.SetupStep{Run: s.Run}
	}

	return runtime.Config{
		Type:            e.Type,
		Image:           e.Image,
		WorkspaceMount:  e.WorkspaceMount,
		Env:             e.Env,
		SetupSteps:      setupSteps,
		SandboxTemplate: e.SandboxTemplate,
		UseServerProxy:  e.UseServerProxy,
		ReadyTimeout:    time.Duration(e.ReadyTimeoutSeconds) * time.Second,
		SandboxTimeout:  time.Duration(e.SandboxTimeoutSeconds) * time.Second,
		Entrypoint:      e.Entrypoint,
		Metadata:        e.Metadata,
		Kwargs:          e.Kwargs,
		NetworkPolicy:   e.NetworkPolicy,
		AllowedEgress:   e.AllowedEgress,
		Delete:          true,
	}
}

// SetupStep is a single environment initialization command.
type SetupStep struct {
	Run string `yaml:"run"`
}

// MCPConfig defines MCP (Model Context Protocol) server configuration.
type MCPConfig struct {
	Servers []MCPServer `yaml:"servers"`
}

// MCPServer describes a single MCP server.
type MCPServer struct {
	Name      string   `yaml:"name"`
	Mode      string   `yaml:"mode"`                // mocked, real
	Transport string   `yaml:"transport,omitempty"` // stdio, http
	Command   string   `yaml:"command,omitempty"`   // for stdio MCP servers
	Args      []string `yaml:"args,omitempty"`      // for stdio MCP servers
	Endpoint  string   `yaml:"endpoint,omitempty"`  // for http MCP servers
	ConfigRef string   `yaml:"config_ref,omitempty"`
}

// SkillRef describes a skill to install.
type SkillRef struct {
	Source string `yaml:"source"` // local_path, registry
	Path   string `yaml:"path,omitempty"`
	Target string `yaml:"target,omitempty"`
}

// EngineConfig defines the Agent Engine configuration.
type EngineConfig struct {
	Name    string      `yaml:"name"` // claude_code, codex, custom
	Version string      `yaml:"version,omitempty"`
	Entry   string      `yaml:"entry,omitempty"`
	Model   ModelConfig `yaml:"model"`
	// Kwargs carries agent-specific key/value options. Each agent reads only
	// the keys it understands; unknown keys are ignored. Recognised keys are
	// documented per agent (e.g. codex honours "bypass_sandbox").
	Kwargs map[string]string   `yaml:"kwargs,omitempty"`
	Custom *CustomEngineConfig `yaml:"custom,omitempty"`
}

// CustomEngineConfig describes a user-defined agent engine that is not a
// built-in agent. It is read only when engine.name does not match a built-in
// agent. See docs/design/custom-engine.md for the full contract.
type CustomEngineConfig struct {
	Transport      string             `yaml:"transport"` // local, http
	TimeoutSeconds int                `yaml:"timeout_seconds,omitempty"`
	ResponseFormat string             `yaml:"response_format,omitempty"` // session_result (default), text
	Env            map[string]string  `yaml:"env,omitempty"`
	Kwargs         map[string]string  `yaml:"kwargs,omitempty"`
	Local          *CustomLocalConfig `yaml:"local,omitempty"`
	HTTP           *CustomHTTPConfig  `yaml:"http,omitempty"`
}

// CustomLocalConfig configures the local transport: a command executed inside
// the current runtime via runtime.Exec.
type CustomLocalConfig struct {
	Command    string   `yaml:"command"`
	Args       []string `yaml:"args,omitempty"`
	Cwd        string   `yaml:"cwd,omitempty"`
	InputFile  string   `yaml:"input_file,omitempty"`
	OutputFile string   `yaml:"output_file,omitempty"`
}

// CustomHTTPConfig configures the http transport: a remote or local HTTP agent
// service. The implementation lands in a follow-up phase.
type CustomHTTPConfig struct {
	URL         string            `yaml:"url"`
	Method      string            `yaml:"method,omitempty"` // POST
	Headers     map[string]string `yaml:"headers,omitempty"`
	Files       []CustomHTTPFile  `yaml:"files,omitempty"`
	RequestBody map[string]any    `yaml:"request_body,omitempty"`
}

// CustomHTTPFile declares a workspace file (or glob) uploaded with an HTTP request.
type CustomHTTPFile struct {
	Path     string `yaml:"path"`
	Required *bool  `yaml:"required,omitempty"` // defaults to true
}

// ModelConfig describes the model to use.
type ModelConfig struct {
	Provider string            `yaml:"provider"` // anthropic, openai, azure, ollama
	Name     string            `yaml:"name"`
	BaseURL  string            `yaml:"base_url,omitempty"`
	Params   map[string]string `yaml:"params,omitempty"`
}

// CasesConfig describes the test cases configuration.
type CasesConfig struct {
	Files       []string     `yaml:"files"`
	Defaults    CaseDefaults `yaml:"defaults,omitempty"`
	Parallelism int          `yaml:"parallelism,omitempty"`
	RetryPolicy RetryPolicy  `yaml:"retry_policy,omitempty"`
}

// CaseDefaults provides default values for cases.
type CaseDefaults struct {
	TimeoutSeconds int `yaml:"timeout_seconds,omitempty"`
	MaxTurns       int `yaml:"max_turns,omitempty"`
	// CollectArtifacts lists glob patterns (doublestar syntax, e.g. "*.md",
	// "a/**/b", "**/*.json") selecting workspace files to download as run
	// artifacts for every case. Matches are copied into
	// <output-dir>/<case>/<config>/outputs/workspace/ preserving their relative
	// path, regardless of whether the agent succeeded, failed, or timed out.
	// Per-case CaseConfig.CollectArtifacts is merged (union) on top of this.
	CollectArtifacts []string `yaml:"collect_artifacts,omitempty"`
}

// RetryPolicy describes retry behavior.
type RetryPolicy struct {
	MaxRetries int      `yaml:"max_retries,omitempty"`
	RetryOn    []string `yaml:"retry_on,omitempty"` // timeout, error
}

// JudgeConfig describes the evaluation strategy.
type JudgeConfig struct {
	Type       string   `json:"type"                     yaml:"type"` // rule_based, script, agent_judge
	ScriptPath string   `json:"script_path,omitempty"    yaml:"script_path,omitempty"`
	Model      string   `json:"model,omitempty"          yaml:"model,omitempty"`
	Criteria   []string `json:"criteria,omitempty"       yaml:"criteria,omitempty"`
	// PassThreshold is the minimum pass rate for agent_judge.
	// Nil means "not configured", so the judge layer applies its default of 0.7.
	// When set explicitly, the value must be in the inclusive range [0.0, 1.0].
	PassThreshold *float64 `json:"pass_threshold,omitempty" yaml:"pass_threshold,omitempty"`
	// TimeoutSeconds bounds a single judge invocation in seconds (applied on
	// top of any case-level timeout). Nil means "not configured" — the global
	// value is inherited via MergeJudgeConfig. When set, the meaning of 0
	// depends on judge type: script judges fall back to DefaultScriptTimeout
	// (30s); agent_judge treats 0 as "no judge-level deadline".
	TimeoutSeconds *int   `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
	Success        []Rule `json:"success,omitempty"        yaml:"success,omitempty"`
	Failure        []Rule `json:"failure,omitempty"        yaml:"failure,omitempty"`
}

// Rule is a single assertion rule for rule_based evaluation.
type Rule struct {
	OutputContains *OutputContainsRule `json:"output_contains,omitempty" yaml:"output_contains,omitempty"`
	ExitCode       *int                `json:"exit_code,omitempty"       yaml:"exit_code,omitempty"`
	ToolCalled     *ToolCalledRule     `json:"tool_called,omitempty"     yaml:"tool_called,omitempty"`
	FilesExist     []string            `json:"files_exist,omitempty"     yaml:"files_exist,omitempty"`
	FilesNotExist  []string            `json:"files_not_exist,omitempty" yaml:"files_not_exist,omitempty"`
}

// OutputContainsRule checks if output contains specific text.
type OutputContainsRule struct {
	All []string `json:"all,omitempty" yaml:"all,omitempty"`
	Any []string `json:"any,omitempty" yaml:"any,omitempty"`
	Not []string `json:"not,omitempty" yaml:"not,omitempty"`
}

// ToolCalledRule checks for MCP tool calls.
type ToolCalledRule struct {
	Name string         `json:"name"           yaml:"name"`
	Args map[string]any `json:"args,omitempty" yaml:"args,omitempty"`
}

// BenchmarkConfig enables baseline comparison.
type BenchmarkConfig struct {
	Enabled bool `yaml:"enabled"`
}

// ReportConfig describes report output.
type ReportConfig struct {
	Formats   []string `yaml:"formats,omitempty"`   // json, junit, html
	Artifacts []string `yaml:"artifacts,omitempty"` // transcript, logs
}

// CaseConfig is a single test case loaded from the case files listed in evals/eval.yaml.
type CaseConfig struct {
	ID          string      `yaml:"id"`
	Title       string      `yaml:"title"`
	Description string      `yaml:"description"`
	Tag         string      `yaml:"tag"` // trigger_test, functional_test
	Input       Input       `yaml:"input"`
	Context     Context     `yaml:"context"`
	Constraints Constraints `yaml:"constraints"`
	Expect      Expect      `yaml:"expect"`
	Judge       JudgeConfig `yaml:"judge,omitempty"`
	// CollectArtifacts lists glob patterns (doublestar syntax) selecting
	// workspace files to download as artifacts for this case. It is merged
	// (union, de-duplicated) with cases.defaults.collect_artifacts. See
	// CaseDefaults.CollectArtifacts for the download layout and semantics.
	CollectArtifacts []string `yaml:"collect_artifacts,omitempty"`
}

// Input describes the agent input (prompt or turns).
type Input struct {
	Prompt string `yaml:"prompt,omitempty"`
	Turns  []Turn `yaml:"turns,omitempty"`
}

// Turn is a single conversation turn.
type Turn struct {
	Role          string         `yaml:"role"` // user
	Content       string         `yaml:"content"`
	PostCondition *PostCondition `yaml:"post_condition,omitempty"`
}

// PostCondition checks output after a turn.
type PostCondition struct {
	MustContainAny []string `yaml:"must_contain_any,omitempty"`
	OnFail         string   `yaml:"on_fail,omitempty"` // skip_remaining, fail
}

// Context describes test case setup.
type Context struct {
	RepoFixture string            `yaml:"repo_fixture,omitempty"`
	Git         *GitContext       `yaml:"git,omitempty"`
	Files       map[string]string `yaml:"files,omitempty"`
}

// GitContext describes git setup.
type GitContext struct {
	Init      bool        `yaml:"init,omitempty"`
	Checkout  string      `yaml:"checkout,omitempty"`
	ApplyDiff string      `yaml:"apply_diff,omitempty"`
	Remotes   []GitRemote `yaml:"remotes,omitempty"`
}

// GitRemote describes a git remote.
type GitRemote struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// Constraints limit execution.
type Constraints struct {
	TimeoutSeconds int `yaml:"timeout_seconds,omitempty"`
	MaxTurns       int `yaml:"max_turns,omitempty"`
}

// Expect describes basic output checks.
type Expect struct {
	MustContain    []string            `json:"must_contain,omitempty"     yaml:"must_contain,omitempty"`
	MustNotContain []string            `json:"must_not_contain,omitempty" yaml:"must_not_contain,omitempty"`
	ExitCode       *int                `json:"exit_code,omitempty"        yaml:"exit_code,omitempty"`
	FilesExist     []string            `json:"files_exist,omitempty"      yaml:"files_exist,omitempty"`
	FilesNotExist  []string            `json:"files_not_exist,omitempty"  yaml:"files_not_exist,omitempty"`
	GoldenFile     string              `json:"golden_file,omitempty"      yaml:"golden_file,omitempty"`
	FileContains   []FileContainsCheck `json:"file_contains,omitempty"    yaml:"file_contains,omitempty"`
}

// FileContainsCheck verifies that a specific file contains expected text.
type FileContainsCheck struct {
	Path    string `json:"path"    yaml:"path"`
	Content string `json:"content" yaml:"content"`
}

// EvalResult is the result of loading an eval with its cases.
type EvalResult struct {
	Eval  *EvalConfig
	Cases []*CaseConfig
}

// APIKeyConfig contains credentials for a model provider.
type APIKeyConfig struct {
	Provider  string // provider name: anthropic, openai, dashscope
	BaseURL   string // API endpoint base URL
	APIKey    string // API key value
	APIKeyVar string // environment variable name for the API key
}

// LoadMeta contains metadata about a loaded eval.
type LoadMeta struct {
	EvalPath      string
	EvalDir       string
	SkillDir      string
	CasesDir      string
	FixtureDir    string
	LoadedAt      time.Time
	SchemaVersion string
}
