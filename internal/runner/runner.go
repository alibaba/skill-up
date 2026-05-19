// Package runner orchestrates loading config, preparing runtimes, running cases, and reporting.
//
// The Runner coordinates the full evaluation pipeline:
//
//	Load Config → Validate → For each case:
//	  → Prepare Runtime
//	  → Install Skills
//	  → Provision MCP
//	  → Execute Engine
//	  → Judge Output
//	  → Collect Result
//	→ Generate Report
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/evaluator"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/observability"
	"github.com/alibaba/skill-up/internal/report"
	"github.com/alibaba/skill-up/internal/ui"
)

var newEvaluator = evaluator.NewEvaluator

// EvaluateOptions controls evaluation behavior.
type EvaluateOptions struct {
	DeleteWorkspace bool
	OutputDir       string
	Iteration       int
	Formats         []string
	Observer        evaluator.ProgressObserver
}

// Runner coordinates eval execution end-to-end.
type Runner struct {
	evalCfg  *config.EvalConfig
	loader   *config.Loader
	resolver *credential.Resolver

	runnerParams credential.AgentInitParams

	workspace *report.IterationWorkspace
	startTime time.Time
	endTime   time.Time
}

// NewRunner creates a new Runner with the given eval config and loader.
func NewRunner(evalCfg *config.EvalConfig, loader *config.Loader, resolver *credential.Resolver, runnerParams credential.AgentInitParams) *Runner {
	return &Runner{
		evalCfg:      evalCfg,
		loader:       loader,
		resolver:     resolver,
		runnerParams: runnerParams,
	}
}

// InitWorkspace initializes the iteration workspace for artifact output.
func (r *Runner) InitWorkspace(outputDir, skillName string, iteration int) error {
	return r.setupWorkspace(context.TODO(), outputDir, skillName, iteration)
}

func (r *Runner) setupWorkspace(ctx context.Context, outputDir, skillName string, iteration int) error {
	ws, err := report.NewIterationWorkspace(outputDir, skillName, iteration)
	if err != nil {
		return fmt.Errorf("failed to create iteration workspace: %w", err)
	}

	if err := os.RemoveAll(ws.IterationDir()); err != nil {
		return fmt.Errorf("failed to clean iteration workspace: %w", err)
	}
	r.workspace = ws
	logging.DebugContextf(ctx, "Runner: iteration workspace: %s", ws.IterationDir())
	return nil
}

// Workspace returns the iteration workspace (nil if not initialized).
func (r *Runner) Workspace() *report.IterationWorkspace {
	return r.workspace
}

// Evaluate runs all cases through evaluator and agent, returns results.
func (r *Runner) Evaluate(ctx context.Context, cases []*config.CaseConfig, ag agent.Agent, opts EvaluateOptions) ([]evaluator.EvalResult, error) {
	ctx, span := observability.Tracer().Start(ctx, "runner.evaluate")
	defer span.End()
	span.SetAttributes(
		attribute.Int("skill_up.cases.count", len(cases)),
		attribute.String("skill_up.engine", ag.Name()),
	)

	skillDir := r.loader.SkillDir()
	cleanDir, _ := filepath.Abs(filepath.Clean(skillDir))
	skillName := filepath.Base(cleanDir)
	if skillName == "." {
		cwd, _ := os.Getwd()
		skillName = filepath.Base(cwd)
	}
	concurrency := r.evalCfg.Cases.Parallelism

	workspaceDir := opts.OutputDir
	if workspaceDir == "" {
		// Default workspace sits alongside the skill directory, not inside it,
		// matching the Anthropic eval layout (<skill>/ and <skill>-workspace/ as siblings).
		workspaceDir = filepath.Join(filepath.Dir(cleanDir), skillName+"-workspace")
	}
	runCount := opts.Iteration
	startIteration := 1
	if runCount <= 0 {
		// Auto-detect: find the next available iteration number in the workspace.
		runCount = 1
		startIteration = nextIterationNumber(workspaceDir)
	}
	span.SetAttributes(
		attribute.Int("skill_up.iteration.start", startIteration),
		attribute.Int("skill_up.iteration.count", runCount),
	)
	allResults := make([]evaluator.EvalResult, 0, len(cases)*runCount)
	r.startTime = time.Now()
	for i := range runCount {
		runNumber := startIteration + i
		if err := r.setupWorkspace(ctx, workspaceDir, skillName, runNumber); err != nil {
			return allResults, fmt.Errorf("failed to init workspace for run %d: %w", runNumber, err)
		}

		if runCount == 1 {
			logging.DebugContextf(ctx, "Runner: running %d case(s) with agent %s", len(cases), ag.Name())
		} else {
			logging.DebugContextf(ctx, "Runner: running %d case(s) with agent %s (run %d/%d)", len(cases), ag.Name(), runNumber, runCount)
		}
		logging.DebugContextf(ctx, "Runner: benchmark=%t concurrency=%d output_dir=%s iteration=%d", r.evalCfg.Benchmark.Enabled, concurrency, workspaceDir, runNumber)

		ev := newEvaluator(evaluator.EvalOptions{
			SkillName:       skillName,
			SkillDir:        skillDir,
			EvalCfg:         r.evalCfg,
			Loader:          r.loader,
			Resolver:        r.resolver,
			Agent:           ag,
			RunnerParams:    r.runnerParams,
			OutputDir:       r.workspace.IterationDir(),
			Concurrency:     concurrency,
			DeleteWorkspace: opts.DeleteWorkspace,
			WithBaseline:    r.evalCfg.Benchmark.Enabled,
			Observer:        opts.Observer,
		})

		results, err := ev.EvaluateAll(ctx, cases)
		if err != nil {
			return allResults, err
		}
		allResults = append(allResults, results...)
		r.endTime = time.Now()

		if err := r.WriteResults(ctx, results, skillName, skillDir, runNumber, opts.Formats); err != nil {
			logging.WarnContextf(ctx, "Runner: failed to write results for run %d: %v", runNumber, err)
		}
	}
	return allResults, nil
}

// caseResults groups with_skill and without_skill results for a single case.
type caseResults struct {
	withSkill    *evaluator.EvalResult
	withoutSkill *evaluator.EvalResult
}

// WriteResults writes one run's evaluation results to the current iteration workspace.
// formats specifies which report formats to generate in addition to result.json (always written).
// Supported values: "json" (no-op, result.json suffices), "junit", "html".
func (r *Runner) WriteResults(ctx context.Context, results []evaluator.EvalResult, skillName, skillPath string, runNumber int, formats []string) error {
	if r.workspace == nil {
		return errors.New("workspace not initialized; call InitWorkspace first")
	}

	ws := r.workspace

	caseIDs := indexCaseResults(results)
	if err := ensureCaseDirs(ws, results, caseIDs); err != nil {
		return err
	}

	grouped := groupResultsByCase(results)

	withSkillRuns, withoutSkillRuns, err := r.writeGroupedCaseArtifacts(ws, grouped, caseIDs, runNumber)
	if err != nil {
		return err
	}

	bm := report.ComputeAnthropicBenchmark(skillName, skillPath, withSkillRuns, withoutSkillRuns)
	if err := ws.WriteBenchmark(bm); err != nil {
		return fmt.Errorf("failed to write benchmark.json: %w", err)
	}
	if err := ws.WriteBenchmarkMD(bm); err != nil {
		return fmt.Errorf("failed to write benchmark.md: %w", err)
	}

	input := buildReportInput(skillName, grouped, caseIDs, r.startTime, r.endTime, r.evalCfg)

	// result.json is always written as the raw evaluation data source.
	resultJSON, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal result.json: %w", err)
	}
	if err := ws.WriteFile("result.json", append(resultJSON, '\n')); err != nil {
		return fmt.Errorf("failed to write result.json: %w", err)
	}

	if err := writeFormattedReports(ctx, ws.IterationDir(), formats, input); err != nil {
		return err
	}

	ui.Blank()
	ui.Step("📊", "Generating reports...")
	logging.DebugContextf(ctx, "Runner: results written to %s", ws.IterationDir())
	ui.Statusf("✅", "Reports saved to %s", ws.IterationDir())
	return nil
}

// indexCaseResults returns the unique case IDs in their first-seen order.
func indexCaseResults(results []evaluator.EvalResult) []string {
	seen := make(map[string]struct{}, len(results))
	caseIDs := make([]string, 0, len(results))
	for _, res := range results {
		if _, ok := seen[res.CaseID]; ok {
			continue
		}
		seen[res.CaseID] = struct{}{}
		caseIDs = append(caseIDs, res.CaseID)
	}
	return caseIDs
}

// hasBaselineConfiguration reports whether any result belongs to the
// "without_skill" baseline configuration, which selects the workspace layout
// that includes a baseline subtree.
func hasBaselineConfiguration(results []evaluator.EvalResult) bool {
	for _, res := range results {
		if res.Configuration == "without_skill" {
			return true
		}
	}
	return false
}

// ensureCaseDirs creates the per-case workspace subdirectories, dispatching to
// the baseline layout when any result represents the baseline configuration.
func ensureCaseDirs(ws *report.IterationWorkspace, results []evaluator.EvalResult, caseIDs []string) error {
	var err error
	if hasBaselineConfiguration(results) {
		err = ws.EnsureDirsWithBaseline(caseIDs)
	} else {
		err = ws.EnsureDirs(caseIDs)
	}
	if err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}
	return nil
}

// groupResultsByCase splits a flat result slice into a per-case bucket holding
// at most one with-skill and one without-skill result.
func groupResultsByCase(results []evaluator.EvalResult) map[string]*caseResults {
	grouped := make(map[string]*caseResults)
	for i := range results {
		res := &results[i]
		if _, ok := grouped[res.CaseID]; !ok {
			grouped[res.CaseID] = &caseResults{}
		}
		if res.Configuration == "without_skill" {
			grouped[res.CaseID].withoutSkill = res
		} else {
			grouped[res.CaseID].withSkill = res
		}
	}
	return grouped
}

// writeGroupedCaseArtifacts persists the per-case artifacts for each
// configuration and accumulates the matching benchmark runs in case-id order.
func (r *Runner) writeGroupedCaseArtifacts(ws *report.IterationWorkspace, grouped map[string]*caseResults, caseIDs []string, runNumber int) (withSkillRuns, withoutSkillRuns []report.BenchmarkRun, err error) { //nolint:revive,nonamedreturns // named returns required by revive confusing-results
	for _, caseID := range caseIDs {
		cr := grouped[caseID]
		if cr.withSkill != nil {
			if writeErr := r.writeCaseArtifacts(ws, cr.withSkill, "with_skill"); writeErr != nil {
				return nil, nil, fmt.Errorf("failed to write with_skill artifacts for %s: %w", caseID, writeErr)
			}
			withSkillRuns = append(withSkillRuns, resultToBenchmarkRun(cr.withSkill, runNumber))
		}
		if cr.withoutSkill != nil {
			if writeErr := r.writeCaseArtifacts(ws, cr.withoutSkill, "without_skill"); writeErr != nil {
				return nil, nil, fmt.Errorf("failed to write without_skill artifacts for %s: %w", caseID, writeErr)
			}
			withoutSkillRuns = append(withoutSkillRuns, resultToBenchmarkRun(cr.withoutSkill, runNumber))
		}
	}
	return withSkillRuns, withoutSkillRuns, nil
}

// writeFormattedReports generates report files for each requested format.
// "json" is silently skipped because result.json is always written by the caller.
func writeFormattedReports(ctx context.Context, iterDir string, formats []string, input report.Input) error {
	for _, format := range formats {
		switch format {
		case "json":
			// result.json already written; skip.
		case "junit":
			reporter := &report.JUnitReporter{OutputPath: filepath.Join(iterDir, "report.xml")}
			if err := reporter.Write(ctx, input); err != nil {
				return fmt.Errorf("write junit report: %w", err)
			}
		case "html":
			reporter := &report.HTMLReporter{OutputPath: filepath.Join(iterDir, "report.html")}
			if err := reporter.Write(ctx, input); err != nil {
				return fmt.Errorf("write html report: %w", err)
			}
		}
	}
	return nil
}

func (r *Runner) writeCaseArtifacts(ws *report.IterationWorkspace, res *evaluator.EvalResult, cfgName string) error {
	if err := ws.WriteResponse(res.CaseID, cfgName, responseContent(res)); err != nil {
		return err
	}

	if res.Grading != nil {
		grading := report.ConvertToAnthropicGrading(res.Grading)
		if err := ws.WriteGrading(res.CaseID, cfgName, grading); err != nil {
			return err
		}
	}

	var assertions []string
	if res.Grading != nil {
		for _, a := range res.Grading.AssertionResults {
			assertions = append(assertions, a.Text)
		}
	}
	meta := &report.EvalMetadata{
		EvalName:   res.CaseName,
		Prompt:     res.Prompt,
		Assertions: assertions,
	}
	return ws.WriteEvalMeta(res.CaseID, meta)
}

func resultToBenchmarkRun(res *evaluator.EvalResult, runNumber int) report.BenchmarkRun {
	var passRate float64
	var passed, failed, total int
	var expectations []report.AnthropicExpectation

	if res.Grading != nil {
		passRate = res.Grading.Summary.PassRate
		passed = res.Grading.Summary.Passed
		failed = res.Grading.Summary.Failed
		total = res.Grading.Summary.Total

		for _, a := range res.Grading.AssertionResults {
			expectations = append(expectations, report.AnthropicExpectation{
				Text:     a.Text,
				Passed:   a.Passed,
				Evidence: a.Evidence,
			})
		}
	}

	return report.BenchmarkRun{
		EvalName:      res.CaseName,
		Configuration: res.Configuration,
		RunNumber:     runNumber,
		Result: report.BenchmarkRunResult{
			PassRate:    passRate,
			Passed:      passed,
			Failed:      failed,
			Total:       total,
			TimeSeconds: float64(res.DurationMs) / 1000.0,
			Tokens:      res.InputTokens + res.OutputTokens,
		},
		Expectations: expectations,
	}
}

func buildReportInput(skillName string, grouped map[string]*caseResults, caseIDs []string, startTime, endTime time.Time, evalCfg *config.EvalConfig) report.Input {
	modelName := evalCfg.Engine.Model.Name
	if evalCfg.Engine.Model.Provider != "" && modelName != "" {
		modelName = evalCfg.Engine.Model.Provider + "/" + modelName
	}
	input := report.Input{
		SkillName:     skillName,
		SchemaVersion: evalCfg.SchemaVersion,
		EngineName:    evalCfg.Engine.Name,
		ModelName:     modelName,
		StartTime:     startTime,
		EndTime:       endTime,
	}

	for _, caseID := range caseIDs {
		cr := grouped[caseID]
		if cr.withSkill != nil {
			caseResult := evalResultToCaseResult(cr.withSkill)
			input.CaseResults = append(input.CaseResults, caseResult)
			input.TotalTokens += caseResult.InputTokens + caseResult.OutputTokens
		}
		if cr.withoutSkill != nil {
			caseResult := evalResultToCaseResult(cr.withoutSkill)
			input.CaseResults = append(input.CaseResults, caseResult)
			input.TotalTokens += caseResult.InputTokens + caseResult.OutputTokens
		}
	}

	return input
}

func evalResultToCaseResult(res *evaluator.EvalResult) report.CaseResult {
	cr := report.CaseResult{
		CaseID:        res.CaseID,
		Title:         res.CaseName,
		Status:        res.Status,
		DurationMs:    res.DurationMs,
		Turns:         res.Turns,
		InputTokens:   res.InputTokens,
		OutputTokens:  res.OutputTokens,
		Grading:       res.Grading,
		Configuration: res.Configuration,
		Prompt:        res.Prompt,
		Response:      responseContent(res),
	}
	if res.Error != nil {
		cr.Error = res.Error.Error()
	}
	return cr
}

func responseContent(res *evaluator.EvalResult) string {
	if res == nil || res.SessionResult == nil {
		return ""
	}
	if res.FinalMessage != "" {
		return res.FinalMessage
	}
	if res.Error == nil && res.ExitCode == 0 {
		return ""
	}
	return res.Stderr
}

// nextIterationNumber scans workspaceDir for iteration-N directories and returns max(N)+1.
// Returns 1 if the directory is absent or contains no iteration dirs.
func nextIterationNumber(workspaceDir string) int {
	entries, err := os.ReadDir(workspaceDir)
	if err != nil {
		return 1
	}
	maxIter := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		var n int
		if _, err := fmt.Sscanf(name, "iteration-%d", &n); err == nil &&
			name == fmt.Sprintf("iteration-%d", n) && n > maxIter {
			maxIter = n
		}
	}
	return maxIter + 1
}
