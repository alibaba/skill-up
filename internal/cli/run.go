package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/evaluator"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/observability"
	"github.com/alibaba/skill-up/internal/runner"
	"github.com/alibaba/skill-up/internal/ui"
	"github.com/alibaba/skill-up/internal/userconfig"
)

const (
	modelFormatParts       = 2
	maxParallelismOverride = 256
	runtimeKwargFlagName   = "runtime-kwarg"
	runtimeKwargAlias      = "rk"
	runtimeFlagName        = "runtime"
)

// validRuntimeTypes lists the environment.type values accepted by --runtime.
var validRuntimeTypes = []string{"none", "opensandbox"}

type verbosityValue int

func (v *verbosityValue) String() string {
	return strconv.Itoa(int(*v))
}

func (v *verbosityValue) Set(s string) error {
	switch strings.ToLower(s) {
	case "", "true":
		*v++
		return nil
	case "false":
		*v = 0
		return nil
	}

	n, err := strconv.Atoi(s)
	if err != nil {
		return err
	}
	*v = verbosityValue(n)
	return nil
}

func (v *verbosityValue) Type() string {
	return "verbosity"
}

var runCmd = &cobra.Command{
	Use:   "run [path to eval.yaml]",
	Short: "Run evaluation cases",
	Long: `Run evaluation cases against an Agent Engine and produce reports.

The command loads eval.yaml and all case files, validates the configuration,
executes each case against the configured engine, evaluates outputs using
the judge module, and generates reports in the specified formats.

If no path is given, the current directory and its parents are searched for
SKILL.md together with evals/eval.yaml (skill layout); the first match wins.

Examples:
  skill-up run
  skill-up run ./evals/eval.yaml
  skill-up run ./evals/eval.yaml --include-case-name "basic-*"
  skill-up run ./evals/eval.yaml --format json --format junit`,
	Args: usageOnError(cobra.MaximumNArgs(1)),
	RunE: runEval,
}

func init() {
	var verbose verbosityValue

	runCmd.Flags().Bool("auto", false, "Auto-detect evals/ directory, preferring eval.yaml over evals.json")
	runCmd.Flags().StringArray("include-case-name", nil, "Include cases matching glob pattern (can be specified multiple times)")
	runCmd.Flags().StringArray("exclude-case-name", nil, "Exclude cases matching glob pattern (can be specified multiple times)")
	runCmd.Flags().StringArray("format", nil, "Report format (json, junit, html). Can be specified multiple times. Default: json")
	runCmd.Flags().String("output-dir", "", "Directory for report/artifact outputs. Default: <skill-name>-workspace alongside the skill directory")
	runCmd.Flags().String("engine", "", "Override engine name")
	runCmd.Flags().String(runtimeFlagName, "", "Override environment.type (none, opensandbox)")
	runCmd.Flags().String("model", "", "Override model (accepts either a bare model name or provider/name)")
	runCmd.Flags().String("api-key", "", "API key for the model provider")
	runCmd.Flags().Int("parallelism", 0, "Override cases.parallelism. Must be between 1 and 256 when specified")
	runCmd.Flags().SetNormalizeFunc(normalizeRunFlagName)
	runCmd.Flags().StringArray(runtimeKwargFlagName, nil, "Environment kwarg in key=value format (can be used multiple times; --rk is accepted as an alias)")
	runCmd.Flags().VarP(&verbose, "verbose", "v", "Increase log verbosity (-v or --verbose for debug, -vv or --verbose=2 for trace; --verbose=true/false is also supported)")
	runCmd.Flags().Lookup("verbose").NoOptDefVal = "true"
	runCmd.Flags().Int("iteration", 0, "Total number of evaluation runs. Each run writes artifacts to iteration-<N>. 0 = auto (appends after the last existing iteration). Default: auto")
	runCmd.Flags().Bool("no-delete", false, "Skip cleanup of workspace after evaluation (useful for debugging)")
	runCmd.Flags().Bool("dry-run", false, "Validate and show what would run without executing")
}

func runEval(cmd *cobra.Command, args []string) error {
	ctx, span := observability.Tracer().Start(cmd.Context(), "cli.run")
	defer span.End()
	cmd.SetContext(ctx)
	if spanCtx := span.SpanContext(); spanCtx.IsValid() {
		logging.InfoContextf(ctx, "Trace ID: %s", spanCtx.TraceID().String())
	}

	verbosity, err := getVerbosity(cmd)
	if err != nil {
		return err
	}
	logging.SetVerbosity(verbosity)
	defer logging.SetVerbosity(0)

	// --- Phase 1: Configuration ---
	cases, evalCfg, loader, err := loadAndPrepareConfig(ctx, cmd, args, span)
	if err != nil {
		return err
	}

	if maybeRunDryRun(cmd, cases, evalCfg) {
		return nil
	}

	// --- Phase 2: Credentials & agent ---
	ag, resolver, runnerParams, err := loadCredentialsAndAgent(cmd, evalCfg)
	if err != nil {
		return err
	}

	// --- Phase 3: Run evaluation ---
	results, err := executeEvaluation(cmd, cases, evalCfg, loader, resolver, runnerParams, ag)
	if err != nil {
		return err
	}

	// --- Phase 4: Summary ---
	ui.Separator()
	passed, failed, errored := countResultStatus(results)
	ui.Summary(passed, failed, errored)

	return exitStatusError(cmd.ErrOrStderr(), results)
}

// loadAndPrepareConfig executes Phase 1: read config + cases, apply CLI filters
// and overrides, and emit observability attributes/spans.
func loadAndPrepareConfig(ctx context.Context, cmd *cobra.Command, args []string, span trace.Span) ([]*config.CaseConfig, *config.EvalConfig, *config.Loader, error) {
	ui.Step("📋", "Loading configuration...")
	cases, evalCfg, loader, err := loadRunInputs(ctx, cmd, args)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(cases) == 0 {
		return nil, nil, nil, errors.New("no cases found")
	}

	includePatterns, _ := cmd.Flags().GetStringArray("include-case-name")
	excludePatterns, _ := cmd.Flags().GetStringArray("exclude-case-name")
	if len(includePatterns) > 0 || len(excludePatterns) > 0 {
		cases = filterCases(cases, includePatterns, excludePatterns)
	}
	if len(cases) == 0 {
		return nil, nil, nil, errors.New("no cases matched the filter")
	}

	engineName, _ := cmd.Flags().GetString("engine")
	evalCfg = resolveEvalConfig(evalCfg, engineName, cmd)
	if err := applyRunConfigOverrides(evalCfg, cmd); err != nil { //nolint:contextcheck // ctx accessed via cmd.Context() inside helpers
		return nil, nil, nil, err
	}

	modelRef := formatModelRef(evalCfg.Engine.Model.Provider, evalCfg.Engine.Model.Name)
	span.SetAttributes(
		attribute.Int("skill_up.cases.count", len(cases)),
		attribute.String("skill_up.engine", evalCfg.Engine.Name),
		attribute.String("skill_up.model", modelRef),
	)
	observability.RecordRunStarted(ctx, evalCfg.Engine.Name, modelRef, len(cases))
	logging.DebugContextf(ctx, "Config: loaded %d case(s); engine=%s model=%s parallelism=%d", len(cases), evalCfg.Engine.Name, modelRef, evalCfg.Cases.Parallelism)
	logging.DebugContextf(ctx, "Config: include-case-name=%v exclude-case-name=%v", includePatterns, excludePatterns)
	ui.Statusf("✅", "Loaded %d case(s) | engine: %s | model: %s", len(cases), evalCfg.Engine.Name, modelRef)
	return cases, evalCfg, loader, nil
}

// maybeRunDryRun handles --dry-run by printing the planned cases and reporting
// whether the caller should exit early. The flag itself is the only failure
// surface and Cobra has already validated it, so no error channel is needed.
func maybeRunDryRun(cmd *cobra.Command, cases []*config.CaseConfig, evalCfg *config.EvalConfig) bool {
	if dryRun, _ := cmd.Flags().GetBool("dry-run"); !dryRun {
		return false
	}
	fmt.Printf("Would run %d case(s) with agent %s\n", len(cases), evalCfg.Engine.Name) //nolint:forbidigo
	for _, c := range cases {
		fmt.Printf("  - %s\n", c.ID) //nolint:forbidigo
	}
	return true
}

// loadCredentialsAndAgent executes Phase 2: load resolver, derive runner init
// params, and instantiate the configured agent.
func loadCredentialsAndAgent(cmd *cobra.Command, evalCfg *config.EvalConfig) (agent.Agent, *credential.Resolver, credential.AgentInitParams, error) {
	ui.Blank()
	ui.Step("🔑", "Loading credentials...")
	cliModel, _ := cmd.Flags().GetString("model")
	cliAPIKey, _ := cmd.Flags().GetString("api-key")

	resolver := credential.NewResolver(credential.DefaultConfPath())
	if err := resolver.Load(); err != nil {
		return nil, nil, credential.AgentInitParams{}, fmt.Errorf("failed to load credentials: %w", err)
	}

	// `--model provider/name` was tentatively split into Provider+Name by
	// resolveEvalConfig (before the resolver was loaded). Now that we know
	// which providers actually have configuration, collapse the split back
	// when the provider is unknown: in that case the slashed string is more
	// likely a literal model identifier the upstream API expects verbatim
	// (e.g. anthropic-proxy gateways registering models under
	// `anthropic_modelscope/deepseek-v4-pro`) than a credential-namespace
	// prefix the agent should peel off.
	collapseUnconfiguredProviderSplit(evalCfg, resolver)

	runnerParams := credential.ResolveRunnerInitParams(
		evalCfg.Engine.Name,
		evalCfg.Engine.Model,
		resolver,
		normalizeCLIModelOverride(cliModel, resolver),
		cliAPIKey,
	)

	ag, err := agent.DetectAgentWithInitParams(evalCfg.Engine.Name, runnerParams)
	if err != nil {
		return nil, nil, credential.AgentInitParams{}, fmt.Errorf("failed to create agent: %w", err)
	}
	ui.Status("✅", "Credentials ready")
	return ag, resolver, runnerParams, nil
}

// executeEvaluation executes Phase 3: build the runner, derive evaluate
// options from CLI flags + config defaults, and run all cases.
func executeEvaluation(
	cmd *cobra.Command,
	cases []*config.CaseConfig,
	evalCfg *config.EvalConfig,
	loader *config.Loader,
	resolver *credential.Resolver,
	runnerParams credential.AgentInitParams,
	ag agent.Agent,
) ([]evaluator.EvalResult, error) {
	ui.Blank()
	ui.Stepf("🚀", "Running evaluation (%d cases)", len(cases))

	run := runner.NewRunner(evalCfg, loader, resolver, runnerParams)

	evaluateOpts, err := evaluateOptionsFromFlags(cmd)
	if err != nil {
		return nil, err
	}
	if len(evaluateOpts.Formats) == 0 {
		evaluateOpts.Formats = evalCfg.Report.Formats
	}
	evaluateOpts.Observer = &uiProgressObserver{}

	logging.DebugContextf(cmd.Context(), "Runner: starting evaluation for %d case(s) with agent %s", len(cases), evalCfg.Engine.Name)
	results, err := run.Evaluate(cmd.Context(), cases, ag, evaluateOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate cases: %w", err)
	}
	return results, nil
}

// uiProgressObserver implements evaluator.ProgressObserver to display case progress.
type uiProgressObserver struct{}

func (o *uiProgressObserver) OnCaseStart(index, total int, caseID, title string) {
	ui.CaseStart(index, total, caseID, title)
}

func (o *uiProgressObserver) OnCaseComplete(index, total int, caseID string, status judge.Status, passRate float64) {
	ui.CaseComplete(index, total, caseID, string(status), passRate)
}

func countResultStatus(results []evaluator.EvalResult) (passed, failed, errored int) {
	for _, r := range results {
		switch r.Status {
		case judge.StatusPass:
			passed++
		case judge.StatusFail:
			failed++
		case judge.StatusError:
			errored++
		}
	}
	return passed, failed, errored
}

func getVerbosity(cmd *cobra.Command) (int, error) {
	flag := cmd.Flags().Lookup("verbose")
	if flag == nil || !flag.Changed {
		return 0, nil
	}

	verbosity, err := strconv.Atoi(flag.Value.String())
	if err != nil {
		return 0, fmt.Errorf("invalid verbose value: %w", err)
	}
	return verbosity, nil
}

func exitStatusError(stderr io.Writer, results []evaluator.EvalResult) error {
	hasFail := false
	hasError := false

	for _, result := range results {
		if result.Configuration == "without_skill" {
			continue
		}
		switch result.Status {
		case judge.StatusFail:
			hasFail = true
		case judge.StatusError:
			hasError = true
		}
	}

	switch {
	case hasError:
		printCaseErrors(stderr, results)
		return errors.New("one or more cases errored")
	case hasFail:
		return errors.New("one or more cases failed")
	default:
		return nil
	}
}

func printCaseErrors(stderr io.Writer, results []evaluator.EvalResult) {
	if stderr == nil {
		return
	}

	for _, result := range results {
		if result.Configuration == "without_skill" {
			continue
		}
		if result.Status != judge.StatusError {
			continue
		}

		caseID := result.CaseID
		if caseID == "" {
			caseID = "<unknown>"
		}

		errMsg := "<nil>"
		if result.Error != nil {
			errMsg = result.Error.Error()
		}

		_, _ = fmt.Fprintf(stderr, "[ERROR] case %s: %s\n", caseID, errMsg) //nolint:forbidigo
	}
}

func evaluateOptionsFromFlags(cmd *cobra.Command) (runner.EvaluateOptions, error) {
	noDelete, err := cmd.Flags().GetBool("no-delete")
	if err != nil {
		return runner.EvaluateOptions{}, fmt.Errorf("read --no-delete: %w", err)
	}
	outputDir, err := cmd.Flags().GetString("output-dir")
	if err != nil {
		return runner.EvaluateOptions{}, fmt.Errorf("read --output-dir: %w", err)
	}
	iteration, err := cmd.Flags().GetInt("iteration")
	if err != nil {
		return runner.EvaluateOptions{}, fmt.Errorf("read --iteration: %w", err)
	}
	if iteration < 0 {
		return runner.EvaluateOptions{}, fmt.Errorf("invalid --iteration %d: must be >= 0", iteration)
	}
	formats, err := cmd.Flags().GetStringArray("format")
	if err != nil {
		return runner.EvaluateOptions{}, fmt.Errorf("read --format: %w", err)
	}
	for _, f := range formats {
		switch f {
		case "json", "junit", "html":
		default:
			return runner.EvaluateOptions{}, fmt.Errorf("unsupported --format %q (supported: json, junit, html)", f)
		}
	}

	return runner.EvaluateOptions{
		DeleteWorkspace: !noDelete,
		OutputDir:       outputDir,
		Iteration:       iteration,
		Formats:         formats,
	}, nil
}

// collapseUnconfiguredProviderSplit undoes the optimistic split that
// resolveEvalConfig performs on `--model provider/name` when the provider
// half is not actually configured (no resolver entry and no
// `<PROVIDER>_API_KEY` / `<PROVIDER>_BASE_URL` env). In that case the
// slashed input is more likely a literal model identifier the upstream API
// expects verbatim (e.g. an internal anthropic-proxy gateway that registers
// models under `anthropic_modelscope/deepseek-v4-pro`) than a credential
// namespace prefix the agent should peel off.
//
// Configured providers (e.g. `dashscope` with DASHSCOPE_API_KEY set) keep
// the split — that's the existing namespace-style usage where the bare
// model name is what should hit the wire.
//
// Safe no-op when Provider is empty or already collapsed.
func collapseUnconfiguredProviderSplit(evalCfg *config.EvalConfig, resolver *credential.Resolver) {
	if evalCfg == nil {
		return
	}
	provider := evalCfg.Engine.Model.Provider
	name := evalCfg.Engine.Model.Name
	if provider == "" || name == "" {
		return
	}
	if credential.ProviderConfigured(provider, resolver) {
		return
	}
	evalCfg.Engine.Model.Provider = ""
	evalCfg.Engine.Model.Name = provider + "/" + name
}

// resolveEvalConfig resolves the engine name and ensures evalCfg is non-nil.
func resolveEvalConfig(evalCfg *config.EvalConfig, engineName string, cmd *cobra.Command) *config.EvalConfig {
	if engineName == "" {
		if evalCfg != nil {
			engineName = evalCfg.Engine.Name
		}
		if engineName == "" {
			engineName = "claude_code"
		}
	}

	if evalCfg == nil {
		evalCfg = config.DefaultEvalConfig()
	}

	evalCfg.Engine.Name = engineName

	if modelFlag, _ := cmd.Flags().GetString("model"); modelFlag != "" {
		parts := strings.SplitN(modelFlag, "/", modelFormatParts)
		if len(parts) == modelFormatParts && parts[0] != "" && parts[1] != "" {
			evalCfg.Engine.Model.Provider = parts[0]
			evalCfg.Engine.Model.Name = parts[1]
		} else {
			evalCfg.Engine.Model.Provider = ""
			evalCfg.Engine.Model.Name = modelFlag
		}
	}

	return evalCfg
}

func applyRunConfigOverrides(evalCfg *config.EvalConfig, cmd *cobra.Command) error {
	if evalCfg == nil {
		return errors.New("eval config is nil")
	}

	// Order matters: applyRuntimeKwargOverrides must run first so CLI
	// --runtime-kwarg values land in evalCfg.Environment.Kwargs. Then
	// applyUserConfigKwargs fills only the keys that are still missing,
	// preserving the precedence CLI > eval.yaml > user-config.
	if err := applyRuntimeKwargOverrides(evalCfg, cmd); err != nil {
		return err
	}
	if err := applyRuntimeTypeOverride(evalCfg, cmd); err != nil {
		return err
	}
	applyUserConfigKwargs(cmd.Context(), evalCfg)

	parallelismFlag := cmd.Flags().Lookup("parallelism")
	if parallelismFlag == nil || !parallelismFlag.Changed {
		return nil
	}

	parallelism, err := strconv.Atoi(parallelismFlag.Value.String())
	if err != nil {
		return fmt.Errorf("read --parallelism: %w", err)
	}
	if parallelism < 1 {
		return fmt.Errorf("invalid --parallelism %d: must be >= 1", parallelism)
	}
	if parallelism > maxParallelismOverride {
		return fmt.Errorf("invalid --parallelism %d: must be <= %d", parallelism, maxParallelismOverride)
	}

	evalCfg.Cases.Parallelism = parallelism
	return nil
}

// applyUserConfigKwargs fills in missing environment kwargs from user-config
// defaults. CLI --runtime-kwarg and eval.yaml entries are preserved.
func applyUserConfigKwargs(ctx context.Context, evalCfg *config.EvalConfig) {
	if evalCfg == nil {
		return
	}
	cfg, _, ok := userconfig.FromContext(ctx)
	if !ok {
		return
	}
	merged := userconfig.MergeKwargs(cfg, evalCfg.Environment.Type, evalCfg.Environment.Kwargs)
	if len(merged) > 0 {
		evalCfg.Environment.Kwargs = merged
	}
}

func applyRuntimeKwargOverrides(evalCfg *config.EvalConfig, cmd *cobra.Command) error {
	runtimeKwargFlag := cmd.Flags().Lookup(runtimeKwargFlagName)
	if runtimeKwargFlag == nil || !runtimeKwargFlag.Changed {
		return nil
	}
	values, err := cmd.Flags().GetStringArray(runtimeKwargFlagName)
	if err != nil {
		return fmt.Errorf("read --runtime-kwarg: %w", err)
	}
	if len(values) == 0 {
		return nil
	}
	if evalCfg.Environment.Kwargs == nil {
		evalCfg.Environment.Kwargs = map[string]string{}
	}
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return fmt.Errorf("invalid --runtime-kwarg %q: expected key=value", value)
		}
		evalCfg.Environment.Kwargs[key] = val
	}
	return nil
}

// applyRuntimeTypeOverride applies the --runtime flag onto environment.type.
// The value and its compatibility with network_policy are validated here
// because config validation runs before CLI overrides are applied, so an
// overridden value would otherwise bypass validation.
func applyRuntimeTypeOverride(evalCfg *config.EvalConfig, cmd *cobra.Command) error {
	flag := cmd.Flags().Lookup(runtimeFlagName)
	if flag == nil || !flag.Changed {
		return nil
	}
	value := strings.TrimSpace(flag.Value.String())
	if !slices.Contains(validRuntimeTypes, value) {
		return fmt.Errorf("invalid --runtime %q (supported: %s)", value, strings.Join(validRuntimeTypes, ", "))
	}
	if value == "none" && evalCfg.Environment.NetworkPolicy != "" {
		return fmt.Errorf("--runtime none is incompatible with network_policy %q (none cannot enforce network isolation)", evalCfg.Environment.NetworkPolicy)
	}
	evalCfg.Environment.Type = value
	return nil
}

func normalizeRunFlagName(_ *pflag.FlagSet, name string) pflag.NormalizedName {
	if name == runtimeKwargAlias {
		return pflag.NormalizedName(runtimeKwargFlagName)
	}
	return pflag.NormalizedName(name)
}

func formatModelRef(provider, name string) string {
	switch {
	case provider != "" && name != "":
		return provider + "/" + name
	case name != "":
		return name
	case provider != "":
		return provider
	default:
		return "<unset>"
	}
}

func normalizeCLIModelOverride(modelFlag string, resolver *credential.Resolver) string {
	if modelFlag == "" {
		return modelFlag
	}
	parts := strings.SplitN(modelFlag, "/", modelFormatParts)
	if len(parts) == modelFormatParts && parts[0] != "" && parts[1] != "" {
		// Only peel off the provider prefix when it's a configured provider
		// — same disambiguation as collapseUnconfiguredProviderSplit. For
		// unknown providers the slashed string is more likely a literal
		// model identifier the upstream API expects verbatim (e.g. ducky's
		// `anthropic_modelscope/deepseek-v4-pro`); stripping would defeat
		// the un-split applied to evalCfg.Engine.Model.
		if credential.ProviderConfigured(parts[0], resolver) {
			return parts[1]
		}
	}
	return modelFlag
}

func isDirectory(pathname string) bool {
	info, err := os.Stat(pathname)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// defaultAutoSkills keeps skill-root entrypoints aligned: when a real skill
// root is present and the config does not explicitly declare skills, install
// that local skill by default.
func defaultAutoSkills(loader *config.Loader) []config.SkillRef {
	if loader == nil || !isRegularFile(filepath.Join(loader.SkillDir(), "SKILL.md")) {
		return []config.SkillRef{}
	}

	return []config.SkillRef{
		{Source: localPathSkillSource, Path: currentDirectorySkill},
	}
}

func applyImplicitSkills(evalCfg *config.EvalConfig, loader *config.Loader) {
	if evalCfg == nil || evalCfg.Skills != nil {
		return
	}
	evalCfg.Skills = defaultAutoSkills(loader)
}

func discoverFromWorkingTree(match func(string) (string, bool), missErr string) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	wd, err = filepath.Abs(wd)
	if err != nil {
		return "", err
	}
	for dir := wd; ; {
		if p, ok := match(dir); ok {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", errors.New(missErr)
}

func resolveAutoEvalRoot(ctx context.Context, args []string) (string, error) {
	if len(args) > 0 {
		return normalizeAutoEvalRoot(args[0])
	}

	p, err := discoverAutoEvalRootFromSkillTree()
	if err != nil {
		return "", fmt.Errorf("eval path required, or run from a skill directory containing SKILL.md and evals/evals.json or evals/eval.yaml: %w", err)
	}

	logging.InfoContextf(ctx, "Config: discovered auto eval root: %s", p)
	return p, nil
}

func normalizeAutoEvalRoot(pathname string) (string, error) {
	switch {
	case isRegularFile(pathname):
		evalsDir := filepath.Dir(pathname)
		if filepath.Base(evalsDir) != "evals" {
			return "", fmt.Errorf("auto eval file path must be inside an evals directory: %s", pathname)
		}
		return filepath.Dir(evalsDir), nil
	case isDirectory(pathname):
		return pathname, nil
	default:
		return "", fmt.Errorf("auto eval path not found or unsupported: %s", pathname)
	}
}

// discoverAutoEvalRootFromSkillTree walks from the working directory upward until
// it finds a directory containing SKILL.md and either evals/evals.json or evals/eval.yaml.
func discoverAutoEvalRootFromSkillTree() (string, error) {
	return discoverFromWorkingTree(func(dir string) (string, bool) {
		skillMD := filepath.Join(dir, "SKILL.md")
		evalsJSON := filepath.Join(dir, "evals", "evals.json")
		evalYaml := filepath.Join(dir, "evals", "eval.yaml")
		if isRegularFile(skillMD) && (isRegularFile(evalsJSON) || isRegularFile(evalYaml)) {
			return dir, true
		}
		return "", false
	}, "no directory from here to filesystem root contains SKILL.md and evals/evals.json or evals/eval.yaml")
}

func resolveEvalPath(ctx context.Context, args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}

	p, err := discoverEvalYAMLFromSkillTree()
	if err != nil {
		return "", fmt.Errorf("eval path required, or run from a skill directory containing SKILL.md and evals/eval.yaml: %w", err)
	}

	logging.InfoContextf(ctx, "Config: discovered eval.yaml: %s", p)
	return p, nil
}

func loadRunInputs(ctx context.Context, cmd *cobra.Command, args []string) ([]*config.CaseConfig, *config.EvalConfig, *config.Loader, error) {
	autoMode, _ := cmd.Flags().GetBool("auto")
	if autoMode {
		autoRoot, err := resolveAutoEvalRoot(ctx, args)
		if err != nil {
			return nil, nil, nil, err
		}
		cases, evalCfg, loader, err := loadAutoModeEvaluation(ctx, autoRoot)
		if err != nil {
			return nil, nil, nil, err
		}

		return cases, evalCfg, loader, nil
	}

	evalPathInput, err := resolveEvalPath(ctx, args)
	if err != nil {
		return nil, nil, nil, err
	}

	_, cases, evalCfg, loader, err := loadManualModeEvaluation(ctx, evalPathInput)
	if err != nil {
		return nil, nil, nil, err
	}

	return cases, evalCfg, loader, nil
}

// loadAutoModeEvaluation handles the --auto mode for loading eval.yaml or evals.json.
func loadAutoModeEvaluation(ctx context.Context, targetPath string) ([]*config.CaseConfig, *config.EvalConfig, *config.Loader, error) {
	evalsDir := filepath.Join(targetPath, "evals")
	evalsJSONPath := filepath.Join(evalsDir, "evals.json")
	evalYamlPath := filepath.Join(evalsDir, "eval.yaml")

	switch {
	case fileExists(evalYamlPath):
		logging.InfoContextf(ctx, "Config: Auto-detected eval.yaml at %s", evalYamlPath)
		_, cases, evalCfg, loader, err := loadFromEvalYAML(evalYamlPath)
		return cases, evalCfg, loader, err
	case fileExists(evalsJSONPath):
		logging.InfoContextf(ctx, "Config: Auto-detected Anthropic evals.json at %s", evalsJSONPath)
		cases, err := loadAnthropicEvals(evalsJSONPath)
		if err != nil {
			return nil, nil, nil, err
		}

		loader := config.NewLoader(evalsJSONPath)
		evalCfg := config.DefaultEvalConfig()
		evalCfg.Skills = defaultAutoSkills(loader)
		return cases, evalCfg, loader, nil
	default:
		return nil, nil, nil, fmt.Errorf("no evals/ directory found at %s (looked for eval.yaml and evals.json)", targetPath)
	}
}

// discoverEvalYAMLFromSkillTree walks from the working directory upward until it
// finds a directory containing both SKILL.md and evals/eval.yaml.
func discoverEvalYAMLFromSkillTree() (string, error) {
	return discoverFromWorkingTree(func(dir string) (string, bool) {
		skillMD := filepath.Join(dir, "SKILL.md")
		evalYaml := filepath.Join(dir, "evals", "eval.yaml")
		if isRegularFile(skillMD) && isRegularFile(evalYaml) {
			return evalYaml, true
		}
		return "", false
	}, "no directory from here to filesystem root contains both SKILL.md and evals/eval.yaml")
}

// loadManualModeEvaluation handles the manual mode for loading eval.yaml.
func loadManualModeEvaluation(ctx context.Context, targetPath string) (string, []*config.CaseConfig, *config.EvalConfig, *config.Loader, error) {
	var evalPath string

	switch {
	case isRegularFile(targetPath):
		evalPath = targetPath
	case isRegularFile(filepath.Join(targetPath, "evals", "eval.yaml")):
		evalPath = filepath.Join(targetPath, "evals", "eval.yaml")
	default:
		return "", nil, nil, nil, fmt.Errorf("eval.yaml not found at %s", targetPath)
	}
	logging.InfoContextf(ctx, "Config: loading eval.yaml from %s", evalPath)

	return loadFromEvalYAML(evalPath)
}

// loadFromEvalYAML loads evaluation from an eval.yaml file.
func loadFromEvalYAML(evalPath string) (string, []*config.CaseConfig, *config.EvalConfig, *config.Loader, error) {
	loader := config.NewLoader(evalPath)
	result, err := loader.LoadAll()
	if err != nil {
		return "", nil, nil, nil, fmt.Errorf("failed to load config: %w", err)
	}
	applyImplicitSkills(result.Eval, loader)

	validator := config.NewValidator()
	if err := validator.ValidateAll(result); err != nil {
		return "", nil, nil, nil, fmt.Errorf("validation failed: %w", err)
	}

	return evalPath, result.Cases, result.Eval, loader, nil
}

func fileExists(pathname string) bool {
	_, err := os.Stat(pathname)

	return err == nil
}

func isRegularFile(pathname string) bool {
	info, err := os.Stat(pathname)

	return err == nil && !info.IsDir()
}

// loadAnthropicEvals loads an Anthropic evals.json file and converts it to CaseConfigs.
func loadAnthropicEvals(evalsJSONPath string) ([]*config.CaseConfig, error) {
	data, err := os.ReadFile(evalsJSONPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read evals.json: %w", err)
	}

	var evalsData anthropicEvals
	if err := json.Unmarshal(data, &evalsData); err != nil {
		return nil, fmt.Errorf("failed to parse evals.json: %w", err)
	}

	cases := make([]*config.CaseConfig, 0, len(evalsData.Evals))
	for _, eval := range evalsData.Evals {
		cases = append(cases, convertEvalToCase(eval, evalsData.SkillName))
	}

	return cases, nil
}

func filterCases(cases []*config.CaseConfig, include, exclude []string) []*config.CaseConfig {
	filtered := make([]*config.CaseConfig, 0, len(cases))
	for _, c := range cases {
		excluded := false
		for _, pattern := range exclude {
			if matched, _ := path.Match(pattern, c.ID); matched {
				excluded = true

				break
			}
		}
		if excluded {
			continue
		}

		if len(include) > 0 {
			matched := false
			for _, pattern := range include {
				if m, _ := path.Match(pattern, c.ID); m {
					matched = true

					break
				}
			}
			if !matched {
				continue
			}
		}

		filtered = append(filtered, c)
	}

	return filtered
}
