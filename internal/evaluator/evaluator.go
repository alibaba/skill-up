// Package evaluator provides the evaluation orchestrator for skill-up.
//
// The Evaluator coordinates: agent execution → expect pre-check → judge evaluation → result collection.
// It supports both with_skill and without_skill (baseline) execution modes.
package evaluator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/mcp"
	"github.com/alibaba/skill-up/internal/observability"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

var (
	sleepWithContext          = sleepContext
	agentDetectWithInitParams = agent.DetectAgentWithInitParams
)

// ProgressObserver receives progress notifications during evaluation.
// Implementations must be safe for concurrent use.
type ProgressObserver interface {
	OnCaseStart(index, total int, caseID, title string)
	OnCaseComplete(index, total int, caseID string, status judge.Status, passRate float64)
}

// EvalOptions controls evaluation behavior.
type EvalOptions struct {
	WithBaseline bool

	SkillName       string
	SkillDir        string
	OutputDir       string
	Concurrency     int
	DeleteWorkspace bool
	Loader          *config.Loader
	Resolver        *credential.Resolver
	Agent           agent.Agent
	RunnerParams    credential.AgentInitParams

	EvalCfg  *config.EvalConfig
	Observer ProgressObserver
}

// EvalResult holds the complete evaluation output for a single case execution.
type EvalResult struct {
	// SessionResult is the execution result returned by the agent.
	// It is embedded so callers can continue to access fields like
	// FinalMessage/InputTokens/Turns without mirroring them in EvalResult.
	*agent.SessionResult

	// CaseID is the unique identifier of the case.
	CaseID string

	// CaseName is the human-readable name of the case.
	CaseName string

	// Prompt is the input prompt sent to the agent.
	Prompt string

	// Status is the overall evaluation outcome (PASS, FAIL, SKIP, ERROR).
	Status judge.Status

	// TurnsTotal is the total number of turns defined in the case.
	TurnsTotal int

	// TurnResults holds per-turn outcomes for multi-turn evaluations.
	// Nil for single-turn cases.
	TurnResults []TurnResult

	// Grading is the judge evaluation result (nil if judge was skipped).
	Grading *judge.Result

	// ExpectResult is the expect pre-check result (nil if no expect block).
	ExpectResult *judge.ExpectResult

	// Error holds any execution error.
	Error error

	// Configuration is "with_skill" or "without_skill".
	Configuration string
}

// CaseResult is an alias for EvalResult for backward compatibility with runner.
//
// Deprecated: Use EvalResult instead.
type CaseResult = EvalResult

type task struct {
	caseCfg    *config.CaseConfig
	configName string
}

type workspaceDiffState struct {
	enabled     bool
	baselineRev string
}

// Evaluator orchestrates the evaluation pipeline for test cases.
type Evaluator interface {
	EvaluateAll(ctx context.Context, cases []*config.CaseConfig) ([]EvalResult, error)
}

// defaultEvaluator is the default implementation of Evaluator.
type defaultEvaluator struct {
	skillName    string
	skillDir     string
	outputDir    string
	concurrency  int
	withBaseline bool

	evalCfg         *config.EvalConfig
	loader          *config.Loader
	resolver        *credential.Resolver
	ag              agent.Agent
	runnerParams    credential.AgentInitParams
	fixtures        *fixtureRegistry
	deleteWorkspace bool
	observer        ProgressObserver
	mcpOnce         sync.Once
	mcpCfg          runtime.MCPConfig
	mcpEnv          map[string]string
	mcpErr          error
}

// NewEvaluator creates a new Evaluator with the given options.
func NewEvaluator(opts EvalOptions) Evaluator {
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	evalCfg := opts.EvalCfg
	if evalCfg == nil {
		evalCfg = &config.EvalConfig{}
	}

	return &defaultEvaluator{
		skillName:       opts.SkillName,
		skillDir:        opts.SkillDir,
		outputDir:       opts.OutputDir,
		concurrency:     concurrency,
		withBaseline:    opts.WithBaseline,
		evalCfg:         evalCfg,
		loader:          opts.Loader,
		resolver:        opts.Resolver,
		ag:              opts.Agent,
		runnerParams:    opts.RunnerParams,
		fixtures:        newFixtureRegistry(),
		deleteWorkspace: opts.DeleteWorkspace,
		observer:        opts.Observer,
	}
}

// EvaluateAll executes all cases through the evaluation pipeline and returns results.
func (e *defaultEvaluator) EvaluateAll(ctx context.Context, cases []*config.CaseConfig) ([]EvalResult, error) {
	ctx, span := observability.Tracer().Start(ctx, "evaluator.evaluate_all")
	defer span.End()
	span.SetAttributes(
		attribute.Int("skill_up.cases.count", len(cases)),
		attribute.Int("skill_up.concurrency", e.concurrency),
		attribute.Bool("skill_up.baseline.enabled", e.withBaseline),
	)

	if e.ag != nil {
		if err := e.ag.CheckCredentials(ctx); err != nil {
			return nil, fmt.Errorf("agent credential check failed: %w", err)
		}
	}

	configCount := 1
	if e.withBaseline {
		configCount = 2
	}

	tasks := make([]task, 0, len(cases)*configCount)
	for _, c := range cases {
		tasks = append(tasks, task{caseCfg: c, configName: "with_skill"})
		if e.withBaseline {
			tasks = append(tasks, task{caseCfg: c, configName: "without_skill"})
		}
	}

	results := make([]EvalResult, len(tasks))
	sem := make(chan struct{}, e.concurrency)
	totalCases := len(tasks)

	var wg sync.WaitGroup
	for i, t := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		caseNum := i + 1
		go func(idx int, tk task, cn int) {
			defer wg.Done()
			defer func() { <-sem }()
			if e.observer != nil {
				e.observer.OnCaseStart(cn, totalCases, tk.caseCfg.ID, tk.caseCfg.Title)
			}
			results[idx] = e.executeCase(ctx, tk.caseCfg, tk.configName, nil, nil)
			if e.observer != nil {
				res := results[idx]
				passRate := float64(-1)
				if res.Grading != nil {
					passRate = res.Grading.Summary.PassRate
				}
				e.observer.OnCaseComplete(cn, totalCases, tk.caseCfg.ID, res.Status, passRate)
			}
		}(i, t, caseNum)
	}
	wg.Wait()

	return results, nil
}

func casePromptAndTurnsTotal(caseCfg *config.CaseConfig) (string, int) {
	prompt := caseCfg.Input.Prompt
	if prompt == "" && len(caseCfg.Input.Turns) > 0 {
		prompt = caseCfg.Input.Turns[0].Content
	}
	turnsTotal := 1
	if len(caseCfg.Input.Turns) > 0 {
		turnsTotal = len(caseCfg.Input.Turns)
	}
	return prompt, turnsTotal
}

// caseMaxTurns returns the case max-turns constraint, falling back to the
// global cases.defaults value.
func caseMaxTurns(evalCfg *config.EvalConfig, caseCfg *config.CaseConfig) int {
	if caseCfg.Constraints.MaxTurns > 0 {
		return caseCfg.Constraints.MaxTurns
	}
	return evalCfg.Cases.Defaults.MaxTurns
}

// caseTimeoutSeconds returns the case timeout constraint, falling back to the
// global cases.defaults value.
func caseTimeoutSeconds(evalCfg *config.EvalConfig, caseCfg *config.CaseConfig) int {
	if caseCfg.Constraints.TimeoutSeconds > 0 {
		return caseCfg.Constraints.TimeoutSeconds
	}
	return evalCfg.Cases.Defaults.TimeoutSeconds
}

// buildCaseMessages builds transcript messages from case input (single prompt or multi-turn).
func buildCaseMessages(caseCfg *config.CaseConfig) []transcript.Message {
	if caseCfg.Input.Prompt != "" {
		return []transcript.Message{{Role: transcript.RoleUser, Content: caseCfg.Input.Prompt, Turn: 1}}
	}
	messages := make([]transcript.Message, 0, len(caseCfg.Input.Turns))
	for i, turn := range caseCfg.Input.Turns {
		role := transcript.RoleUser
		if turn.Role != "" {
			role = transcript.Role(turn.Role)
		}
		messages = append(messages, transcript.Message{
			Role:    role,
			Content: turn.Content,
			Turn:    i + 1,
		})
	}
	return messages
}

func (e *defaultEvaluator) executeCase(ctx context.Context, caseCfg *config.CaseConfig, configName string, overrideRT runtime.Runtime, overrideAgent agent.Agent) EvalResult { //nolint:cyclop
	maxAttempts := max(e.evalCfg.Cases.RetryPolicy.MaxRetries+1, 1)

	var result EvalResult
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptCtx, cancel, timeoutSource, timeoutSec := withCaseTimeout(ctx, e.evalCfg.Cases.Defaults.TimeoutSeconds, caseCfg.Constraints.TimeoutSeconds)
		result = e.executeCaseOnce(attemptCtx, caseCfg, configName, overrideRT, overrideAgent)
		// Decide whether to annotate *before* cancel() so (a) cancel doesn't
		// overwrite Err with Canceled and erase the signal, and (b) a
		// parent-supplied deadline (e.g. a caller wrapping EvaluateAll in
		// context.WithTimeout) firing through attemptCtx isn't relabelled as a
		// case timeout — that would point users at the wrong YAML knob. A
		// tighter child deadline (e.g. judge.timeout_seconds) gets the same
		// protection because attemptCtx.Err() stays nil when the case-level
		// deadline never fires. The decision lives at the caller so the
		// annotate helper stays free of a control-flag parameter.
		if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			annotateCaseTimeoutError(&result, timeoutSource, timeoutSec)
		}
		cancel()

		retryReason, ok := retryReasonForResult(result)
		if !ok || !retryAllowed(e.evalCfg.Cases.RetryPolicy, retryReason) || attempt == maxAttempts {
			return result
		}

		logging.WarnContextf(
			ctx,
			"Runner: case %s (%s) attempt %d/%d hit %s, retrying after %s",
			caseCfg.ID,
			configName,
			attempt,
			maxAttempts,
			retryReason,
			retryBackoffDelay(attempt),
		)
		if err := sleepWithContext(ctx, retryBackoffDelay(attempt)); err != nil {
			return result
		}
	}

	return result
}

func (e *defaultEvaluator) executeCaseOnce(ctx context.Context, caseCfg *config.CaseConfig, configName string, overrideRT runtime.Runtime, overrideAgent agent.Agent) EvalResult { //nolint:cyclop,gocyclo,funlen // orchestrates the full case lifecycle (runtime prep → agent run → judge → finalize); splitting further would obscure the linear flow
	ctx, span := observability.Tracer().Start(ctx, "evaluator.case")
	defer span.End()
	span.SetAttributes(
		attribute.String("skill_up.case.id", caseCfg.ID),
		attribute.String("skill_up.case.configuration", configName),
	)
	ctx = observability.ContextWithAgentAttributes(ctx, map[string]string{
		"skill_up.case.id":            caseCfg.ID,
		"skill_up.case.configuration": configName,
	})

	startTime := time.Now()

	prompt, turnsTotal := casePromptAndTurnsTotal(caseCfg)
	messages := buildCaseMessages(caseCfg)

	result := EvalResult{
		CaseID:        caseCfg.ID,
		CaseName:      caseCfg.Title,
		Prompt:        prompt,
		SessionResult: &agent.SessionResult{},
		TurnsTotal:    turnsTotal,
	}
	defer func() {
		status := string(result.Status)
		if status == "" {
			status = "UNKNOWN"
		}
		durationMs := result.DurationMs
		if durationMs == 0 {
			durationMs = time.Since(startTime).Milliseconds()
		}
		observability.RecordCaseCompleted(ctx, e.evalCfg.Engine.Name, status, durationMs)
		span.SetAttributes(attribute.String("skill_up.case.status", status))
	}()

	runAgent := e.ag
	if overrideAgent != nil {
		runAgent = overrideAgent
	}

	var rt runtime.Runtime
	if overrideRT != nil {
		rt = overrideRT
	} else {
		var err error
		rt, err = e.prepareRuntimeForCase(ctx, caseCfg, configName, runAgent)
		if err != nil {
			result.Status = judge.StatusError
			result.Error = err
			result.Configuration = configName
			return result
		}
		defer func() { _ = rt.Close() }()
	}

	logging.DebugContextf(ctx, "Runner: case %s (%s): %s", caseCfg.ID, configName, caseCfg.Title)

	judgeCfg := judge.MergeJudgeConfig(e.evalCfg.Judge, caseCfg.Judge)

	// Multi-turn branch: delegate to dedicated engine when the case defines
	// more than one turn AND the agent supports session resumption. Agents
	// that do not implement SessionResumer fall through to the existing
	// single-shot path (all messages in one Run call).
	if len(caseCfg.Input.Turns) > 1 {
		if _, ok := runAgent.(agent.SessionResumer); ok {
			agentExecOpts := agent.ExecOptions{
				ArtifactDir: e.prepareOutputDir(ctx, configName, caseCfg.ID, "agent/run"),
				TimeoutSec:  caseTimeoutSeconds(e.evalCfg, caseCfg),
				AgentMetadata: &runtime.AgentMetadata{
					CaseID:   caseCfg.ID,
					Variant:  configName,
					MaxTurns: caseMaxTurns(e.evalCfg, caseCfg),
				},
			}
			return e.executeMultiTurnCase(ctx, rt, caseCfg, configName, runAgent, agentExecOpts, startTime, judgeCfg, &result)
		}
	}

	var cleanupArtifacts func()
	finalizeArtifacts := func(*agent.SessionResult) {}
	if judgeNeedsWorkspaceDiff(judgeCfg) {
		cleanupArtifacts, finalizeArtifacts = e.prepareWorkspaceArtifacts(ctx, rt, caseCfg)
		defer cleanupArtifacts()
	}

	agentArtifactDir := e.prepareOutputDir(ctx, configName, caseCfg.ID, "agent/run")
	agentCtx := observability.ContextWithConfiguredAgentSpanAttributes(ctx, nil)
	agentCtx, agentSpan := startAgentRunSpan(agentCtx)
	agentSpan.SetAttributes(
		attribute.String("skill_up.case.id", caseCfg.ID),
		attribute.String("skill_up.case.configuration", configName),
		attribute.String("skill_up.engine", runAgent.Name()),
	)
	if observability.LinkedTraceTopologyEnabled() {
		logging.DebugContextf(agentCtx, "Evaluator: linked agent trace started for case %s (%s)", caseCfg.ID, configName)
	}
	agentExecOpts := agent.ExecOptions{
		ArtifactDir: agentArtifactDir,
		TimeoutSec:  caseTimeoutSeconds(e.evalCfg, caseCfg),
		AgentMetadata: &runtime.AgentMetadata{
			CaseID:   caseCfg.ID,
			Variant:  configName,
			MaxTurns: caseMaxTurns(e.evalCfg, caseCfg),
		},
	}
	sessionResult, execErr := runAgent.Run(agentCtx, rt, agentExecOpts, messages)
	agentSpan.End()
	finalizeArtifacts(sessionResult)
	result.SessionResult = normalizeSessionResult(sessionResult)
	if sessionResult != nil && sessionResult.Artifacts != nil && len(sessionResult.Artifacts.GeneratedFiles) > 0 {
		e.ensureArtifactsInOutputDir(ctx, rt, configName, caseCfg.ID, "agent/run", agentArtifactDir, sessionResult)
	}
	// Download glob-selected workspace artifacts unconditionally — before the
	// timeout/error early-return below and before the deferred rt.Close() — so
	// they are captured even when the agent failed or timed out.
	e.collectGlobArtifacts(ctx, rt, configName, caseCfg)
	if shouldReturn := e.handleExecutionResult(ctx, caseCfg, configName, startTime, &result, execErr); shouldReturn {
		return result
	}
	// A genuine non-zero exit may still be handled by expect.exit_code or a
	// configured judge; otherwise it's just a FAIL. (handleExecutionResult
	// already converted any execErr — including ctx.DeadlineExceeded — into
	// an ERROR return above, so we're guaranteed execErr == nil here and the
	// case timeout strictly bounds agent + judge as a single budget.)
	if sessionResult != nil && sessionResult.ExitCode != 0 {
		hasExitCodeCheck := caseCfg.Expect.ExitCode != nil
		hasJudge := judgeCfg.Type != ""
		if !hasExitCodeCheck && !hasJudge {
			if result.DurationMs == 0 {
				result.DurationMs = time.Since(startTime).Milliseconds()
			}
			result.Status = judge.StatusFail
			result.Configuration = configName
			logging.DebugContextf(ctx, "Evaluator: case %s agent exited with code %d (no expect.exit_code or judge), marking FAIL", caseCfg.ID, sessionResult.ExitCode)
			return result
		}
		logging.DebugContextf(ctx, "Evaluator: case %s agent exited with code %d, proceeding to evaluation", caseCfg.ID, sessionResult.ExitCode)
	}

	return e.evaluateCaseSession(ctx, rt, caseCfg, configName, judgeCfg, turnsTotal, runAgent, sessionResult, &result)
}

//nolint:spancheck // caller owns the returned span and must end it.
func startAgentRunSpan(ctx context.Context) (context.Context, trace.Span) {
	if observability.LinkedTraceTopologyEnabled() {
		return observability.StartLinkedRootSpan(ctx, "agent.run")
	}

	return observability.Tracer().Start(ctx, "agent.run")
}

func (e *defaultEvaluator) evaluateCaseSession(
	ctx context.Context,
	rt runtime.Runtime,
	caseCfg *config.CaseConfig,
	configName string,
	judgeCfg config.JudgeConfig,
	turnsTotal int,
	runAgent agent.Agent,
	sessionResult *agent.SessionResult,
	result *EvalResult,
) EvalResult {
	judgeInput := judge.Input{
		CaseID:         caseCfg.ID,
		FinalMessage:   result.FinalMessage,
		ExitCode:       result.ExitCode,
		WorkspacePath:  rt.Workspace(),
		SkillDir:       e.skillDir,
		TurnsExecuted:  result.Turns,
		TurnsTotal:     turnsTotal,
		Transcript:     sessionTranscript(sessionResult),
		WorkspaceDiff:  sessionWorkspaceDiff(sessionResult),
		GeneratedFiles: sessionGeneratedFiles(sessionResult),
		SessionResult:  sessionResult,
		TurnResults:    toJudgeTurnResults(result.TurnResults),
	}

	if failed := e.runExpectPreCheck(ctx, caseCfg, configName, judgeInput, turnsTotal, result); failed {
		return *result
	}

	var expectAssertions []judge.AssertionResult
	if result.ExpectResult != nil {
		expectAssertions = result.ExpectResult.ToAssertionResults()
	}

	finalResult := e.runJudgePhase(ctx, rt, caseCfg, configName, judgeCfg, turnsTotal, runAgent, judgeInput, result)
	if len(expectAssertions) > 0 && finalResult.Grading != nil {
		finalResult.Grading.AssertionResults = append(expectAssertions, finalResult.Grading.AssertionResults...)
		finalResult.Grading.Summary.Passed += len(expectAssertions)
		finalResult.Grading.Summary.Total += len(expectAssertions)
		if finalResult.Grading.Summary.Total > 0 {
			finalResult.Grading.Summary.PassRate = float64(finalResult.Grading.Summary.Passed) / float64(finalResult.Grading.Summary.Total)
		}
	}

	return finalResult
}

func (e *defaultEvaluator) runExpectPreCheck(
	ctx context.Context,
	caseCfg *config.CaseConfig,
	configName string,
	judgeInput judge.Input,
	turnsTotal int,
	result *EvalResult,
) bool {
	expectCfg := resolveExpectConfig(&caseCfg.Expect)
	if expectCfg == nil {
		return false
	}

	expectResult := judge.CheckExpect(expectCfg, judgeInput)
	result.ExpectResult = expectResult
	if expectResult.Passed {
		return false
	}

	assertions := expectResult.ToAssertionResults()
	result.Grading = judge.NewResult(assertions, result.Turns, turnsTotal)
	result.Status = judge.StatusFail
	result.Configuration = configName
	logging.DebugContextf(ctx, "Judge: case %s expect pre-check FAILED", caseCfg.ID)
	return true
}

func (e *defaultEvaluator) runJudgePhase(
	ctx context.Context,
	rt runtime.Runtime,
	caseCfg *config.CaseConfig,
	configName string,
	judgeCfg config.JudgeConfig,
	turnsTotal int,
	runAgent agent.Agent,
	judgeInput judge.Input,
	result *EvalResult,
) EvalResult {
	if observability.LinkedTraceTopologyEnabled() && judgeNeedsWorkspaceDiff(judgeCfg) {
		ctx = observability.ContextWithConfiguredAgentSpanAttributes(ctx, nil)
		ctx, span := observability.StartLinkedRootSpan(ctx, "evaluator.judge")
		defer span.End()
		logging.DebugContextf(ctx, "Evaluator: linked judge trace started for case %s (%s)", caseCfg.ID, configName)
		return e.runJudgePhaseWithSpan(ctx, span, rt, caseCfg, configName, judgeCfg, turnsTotal, runAgent, judgeInput, result)
	}
	ctx, span := observability.Tracer().Start(ctx, "evaluator.judge")
	defer span.End()
	return e.runJudgePhaseWithSpan(ctx, span, rt, caseCfg, configName, judgeCfg, turnsTotal, runAgent, judgeInput, result)
}

func (e *defaultEvaluator) runJudgePhaseWithSpan(
	ctx context.Context,
	span trace.Span,
	rt runtime.Runtime,
	caseCfg *config.CaseConfig,
	configName string,
	judgeCfg config.JudgeConfig,
	turnsTotal int,
	runAgent agent.Agent,
	judgeInput judge.Input,
	result *EvalResult,
) EvalResult {
	span.SetAttributes(
		attribute.String("skill_up.case.id", caseCfg.ID),
		attribute.String("skill_up.judge.type", judgeCfg.Type),
	)

	logging.DebugContextf(ctx, "Judge: case %s using %s", caseCfg.ID, judgeLabel(judgeCfg))
	logging.DebugContextf(ctx, "Judge: case %s generated_files=%d workspace_diff=%t", caseCfg.ID, len(judgeInput.GeneratedFiles), judgeInput.WorkspaceDiff != "")

	j, err := e.newJudgeForCase(ctx, rt, judgeCfg, runAgent)
	if err != nil {
		result.Status = judge.StatusError
		result.Error = err
		result.Configuration = configName
		return *result
	}
	if j == nil {
		result.Status = judge.StatusPass
		result.Grading = judge.NewResult(nil, result.Turns, turnsTotal)
		result.Configuration = configName
		logging.DebugContextf(ctx, "Judge: case %s has no judge configured, default PASS", caseCfg.ID)
		return *result
	}
	if judgeCfg.Type == "agent_judge" {
		judgeInput.ArtifactDir = e.prepareOutputDir(ctx, configName, caseCfg.ID, "judge/run")
	}

	// For ScriptJudge, serialize transcript to a temp file so that
	// EVAL_TRANSCRIPT_PATH is populated for the evaluation script.
	if sj, ok := j.(*judge.ScriptJudge); ok && len(judgeInput.Transcript) > 0 {
		transcriptPath, cleanupFn, serErr := serializeTranscript(judgeInput.Transcript)
		if serErr != nil {
			logging.WarnContextf(ctx, "Evaluator: failed to serialize transcript for script judge: %v", serErr)
		} else {
			defer cleanupFn()
			sj.TranscriptPath = transcriptPath
		}
	}

	grading, err := j.Evaluate(ctx, judgeInput)
	if err != nil {
		if judgeSession := judge.SessionResultFromError(err); judgeSession != nil {
			if judgeSession.Artifacts != nil {
				e.ensureArtifactsInOutputDir(ctx, rt, configName, caseCfg.ID, "judge/run", judgeInput.ArtifactDir, judgeSession)
			}
		}
		result.Status = judge.StatusError
		result.Error = fmt.Errorf("judge evaluation failed: %w", err)
		result.Configuration = configName
		return *result
	}

	result.Grading = grading
	result.Status = grading.Status
	result.Configuration = configName
	span.SetAttributes(attribute.String("skill_up.case.status", string(result.Status)))
	logging.DebugContextf(ctx, "Judge: case %s: %s (pass_rate: %.1f%%)", caseCfg.ID, grading.Status, grading.Summary.PassRate*100)
	if grading.JudgeSession != nil {
		if grading.JudgeSession.Artifacts != nil {
			e.ensureArtifactsInOutputDir(ctx, rt, configName, caseCfg.ID, "judge/run", judgeInput.ArtifactDir, grading.JudgeSession)
		}
	}

	return *result
}

func (e *defaultEvaluator) newJudgeForCase(
	ctx context.Context,
	rt runtime.Runtime,
	judgeCfg config.JudgeConfig,
	runAgent agent.Agent,
) (judge.Judge, error) {
	judgeCfg = resolveJudgeScriptPath(e.judgeScriptBaseDir(), judgeCfg)

	judgeAgent, err := e.resolveJudgeAgent(ctx, judgeCfg, runAgent)
	if err != nil {
		return nil, err
	}

	j, err := judge.NewJudge(judgeCfg, judgeAgent, rt)
	if err != nil {
		return nil, fmt.Errorf("failed to create judge: %w", err)
	}

	return j, nil
}

func (e *defaultEvaluator) resolveJudgeAgent(ctx context.Context, judgeCfg config.JudgeConfig, runAgent agent.Agent) (agent.Agent, error) {
	if judgeCfg.Type != "agent_judge" {
		return runAgent, nil
	}

	judgeAgent := runAgent
	if e.evalCfg.Engine.Name != "" {
		judgeParams := credential.ResolveJudgeInitParams(e.evalCfg.Engine.Name, judgeCfg, e.runnerParams, e.resolver)
		var err error
		judgeAgent, err = agentDetectWithInitParams(e.evalCfg.Engine.Name, judgeParams, e.evalCfg.Engine.Kwargs)
		if err != nil {
			return nil, fmt.Errorf("failed to create judge agent: %w", err)
		}
	}
	if err := judgeAgent.CheckCredentials(ctx); err != nil {
		return nil, fmt.Errorf("judge agent credential check failed: %w", err)
	}

	return judgeAgent, nil
}

func (e *defaultEvaluator) judgeScriptBaseDir() string {
	if strings.TrimSpace(e.skillDir) != "" {
		return e.skillDir
	}
	if e.loader != nil {
		return e.loader.SkillDir()
	}
	return ""
}

func resolveJudgeScriptPath(skillDir string, judgeCfg config.JudgeConfig) config.JudgeConfig {
	if judgeCfg.Type != "script" || judgeCfg.ScriptPath == "" {
		return judgeCfg
	}
	if !filepath.IsAbs(judgeCfg.ScriptPath) {
		judgeCfg.ScriptPath = filepath.Join(skillDir, judgeCfg.ScriptPath)
	}
	if absScriptPath, err := filepath.Abs(judgeCfg.ScriptPath); err == nil {
		judgeCfg.ScriptPath = absScriptPath
	}

	return judgeCfg
}

// annotateCaseTimeoutError attaches the configured case timeout and its
// source config key to a result whose error chain contains
// context.DeadlineExceeded. The caller is responsible for ensuring this is
// only invoked when the case-level deadline actually fired (see executeCase);
// here we just wrap. The original DeadlineExceeded is preserved via %w so
// callers (and isTimeoutError) still match it.
func annotateCaseTimeoutError(result *EvalResult, source string, seconds int) {
	if result == nil || result.Error == nil || source == "" || seconds <= 0 {
		return
	}
	if !errors.Is(result.Error, context.DeadlineExceeded) {
		return
	}
	result.Error = fmt.Errorf("%w (case timeout %ds via %s)", result.Error, seconds, source)
}

// withCaseTimeout returns a derived context bounded by the case-level
// timeout, along with the resolved value and the config key that supplied it
// (so timeout errors can name the knob users need to adjust). source/seconds
// are empty/zero when no timeout is configured.
func withCaseTimeout(ctx context.Context, defaultTimeoutSec, caseTimeoutSec int) (context.Context, context.CancelFunc, string, int) {
	if caseTimeoutSec > 0 {
		c, cancel := context.WithTimeout(ctx, time.Duration(caseTimeoutSec)*time.Second)
		return c, cancel, "case.constraints.timeout_seconds", caseTimeoutSec
	}
	if defaultTimeoutSec > 0 {
		c, cancel := context.WithTimeout(ctx, time.Duration(defaultTimeoutSec)*time.Second)
		return c, cancel, "cases.defaults.timeout_seconds", defaultTimeoutSec
	}
	return ctx, func() {}, "", 0
}

func retryAllowed(policy config.RetryPolicy, reason string) bool {
	if reason == "" || policy.MaxRetries <= 0 {
		return false
	}
	for _, allowed := range policy.RetryOn {
		if strings.EqualFold(allowed, reason) {
			return true
		}
	}
	return false
}

func retryReasonForResult(result EvalResult) (string, bool) {
	if result.Status != judge.StatusError || result.Error == nil {
		return "", false
	}
	if isTimeoutError(result.Error) {
		return "timeout", true
	}
	return "error", true
}

// handleExecutionResult finalises the EvalResult when the agent run returned an
// error. Any execErr (including ctx.DeadlineExceeded) terminates the case as
// ERROR: the case timeout is treated as a single budget for agent + judge, so
// we do not try to salvage partial output by running the judge against a
// truncated agent transcript.
func (e *defaultEvaluator) handleExecutionResult(
	_ context.Context,
	_ *config.CaseConfig,
	configName string,
	startTime time.Time,
	result *EvalResult,
	execErr error,
) bool {
	if execErr == nil {
		return false
	}
	if result.DurationMs == 0 {
		result.DurationMs = time.Since(startTime).Milliseconds()
	}
	result.Status = judge.StatusError
	result.Error = fmt.Errorf("agent execution failed: %w", execErr)
	result.Configuration = configName
	return true
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded)
}

func retryBackoffDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return time.Duration(1<<attempt) * time.Second
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func resolveExpectConfig(expectCfg *config.Expect) *config.Expect {
	if expectCfg.MustContain == nil && expectCfg.MustNotContain == nil &&
		expectCfg.ExitCode == nil && expectCfg.FilesExist == nil &&
		expectCfg.FilesNotExist == nil && expectCfg.GoldenFile == "" &&
		expectCfg.FileContains == nil {
		return nil
	}
	return expectCfg
}

func (e *defaultEvaluator) prepareRuntimeForCase(ctx context.Context, caseCfg *config.CaseConfig, configName string, ag agent.Agent) (runtime.Runtime, error) {
	rtCfg := e.evalCfg.Environment.ToRuntimeConfig()
	rtCfg.Delete = e.deleteWorkspace
	mcpCfg, mcpEnv, err := e.provisionMCPConfig()
	if err != nil {
		return nil, err
	}
	rtCfg.Env = mergeEnvMaps(rtCfg.Env, mcpEnv)

	rt, err := runtime.NewRuntime(rtCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create runtime: %w", err)
	}
	if err := rt.Create(ctx); err != nil {
		_ = rt.Close()
		return nil, fmt.Errorf("failed to create runtime workspace: %w", err)
	}
	if err := e.setupCaseEnvironment(ctx, rt, caseCfg, configName, ag, mcpCfg); err != nil {
		_ = rt.Close()
		return nil, fmt.Errorf("failed to setup case environment: %w", err)
	}
	return rt, nil
}

func (e *defaultEvaluator) setupCaseEnvironment(ctx context.Context, rt runtime.Runtime, caseCfg *config.CaseConfig, configName string, ag agent.Agent, mcpCfg runtime.MCPConfig) error {
	for i, step := range e.evalCfg.Environment.SetupSteps {
		result, err := rt.Exec(ctx, step.Run, runtime.ExecOptions{})
		if err != nil {
			return fmt.Errorf("setup step %d (%q) failed: %w", i+1, step.Run, err)
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("setup step %d (%q) exited with code %d: %s", i+1, step.Run, result.ExitCode, result.Stderr)
		}
	}

	if e.evalCfg.Environment.Type != "none" {
		if err := ag.Install(ctx, rt); err != nil {
			return fmt.Errorf("failed to install agent %s: %w", ag.Name(), err)
		}
	}

	if err := ag.InstallMCP(ctx, rt, mcpCfg); err != nil {
		return fmt.Errorf("failed to install MCP servers: %w", err)
	}

	if configName != "without_skill" && e.loader != nil {
		for _, skillRef := range e.evalCfg.Skills {
			skillSourceDir := e.loader.SkillDir()
			skillSource := filepath.Join(skillSourceDir, skillRef.Path)
			skillCfg := runtime.SkillConfig{
				Source: skillSource,
				Target: skillRef.Target,
			}
			if err := ag.InstallSkill(ctx, rt, skillCfg); err != nil {
				return fmt.Errorf("failed to install skill %s: %w", skillRef.Path, err)
			}
			logging.DebugContextf(ctx, "Evaluator: skill installed: %s", filepath.Base(skillSource))
		}
	}

	if e.fixtures != nil && e.loader != nil && e.skillDir != "" {
		fixtureBaseDir := e.loader.SkillDir()
		if err := e.fixtures.UploadAll(ctx, rt, caseCfg, e.skillDir, fixtureBaseDir); err != nil {
			return fmt.Errorf("failed to upload fixtures: %w", err)
		}
	}

	return nil
}

func (e *defaultEvaluator) provisionMCPConfig() (runtime.MCPConfig, map[string]string, error) {
	e.mcpOnce.Do(func() {
		skillDir := e.skillDir
		if e.loader != nil {
			skillDir = e.loader.SkillDir()
		}
		provisioner := mcp.Provisioner{SkillDir: skillDir}
		e.mcpCfg, e.mcpEnv, e.mcpErr = provisioner.Provision(e.evalCfg.MCP)
		if e.mcpErr != nil {
			e.mcpErr = fmt.Errorf("failed to provision MCP servers: %w", e.mcpErr)
		}
	})
	if e.mcpErr != nil {
		return runtime.MCPConfig{}, nil, e.mcpErr
	}
	return cloneMCPConfig(e.mcpCfg), maps.Clone(e.mcpEnv), nil
}

func mergeEnvMaps(base, override map[string]string) map[string]string {
	if len(override) == 0 {
		return base
	}
	merged := make(map[string]string, len(base)+len(override))
	maps.Copy(merged, base)
	maps.Copy(merged, override)
	return merged
}

func cloneMCPConfig(cfg runtime.MCPConfig) runtime.MCPConfig {
	servers := make([]runtime.MCPServerConfig, len(cfg.Servers))
	for i, server := range cfg.Servers {
		servers[i] = server
		servers[i].Args = append([]string(nil), server.Args...)
		servers[i].Env = maps.Clone(server.Env)
		servers[i].Headers = maps.Clone(server.Headers)
		servers[i].HeaderEnv = maps.Clone(server.HeaderEnv)
	}
	return runtime.MCPConfig{Servers: servers}
}

func normalizeSessionResult(sessionResult *agent.SessionResult) *agent.SessionResult {
	if sessionResult == nil {
		return &agent.SessionResult{}
	}

	normalized := *sessionResult
	if finalMsg := normalized.Transcript.FinalAssistantMessage(); finalMsg != "" {
		normalized.FinalMessage = finalMsg
	}

	return &normalized
}

func judgeNeedsWorkspaceDiff(cfg config.JudgeConfig) bool {
	return cfg.Type == "agent_judge"
}

func judgeLabel(cfg config.JudgeConfig) string {
	if strings.TrimSpace(cfg.Type) == "" {
		return "no_judge"
	}
	return cfg.Type
}

func (e *defaultEvaluator) prepareWorkspaceArtifacts(ctx context.Context, rt runtime.Runtime, caseCfg *config.CaseConfig) (func(), func(*agent.SessionResult)) {
	state, err := prepareWorkspaceDiffState(ctx, rt, caseCfg.Context.Git)
	if err != nil {
		logging.WarnContextf(ctx, "Judge: failed to snapshot workspace before run for case %s: %v", caseCfg.ID, err)
	}

	finalize := func(sessionResult *agent.SessionResult) {
		if sessionResult == nil || !state.enabled {
			return
		}
		workspaceDiff, diffErr := collectWorkspaceDiff(ctx, rt, state, sessionGeneratedFiles(sessionResult))
		if diffErr != nil {
			logging.WarnContextf(ctx, "Judge: failed to collect workspace diff for case %s: %v", caseCfg.ID, diffErr)
			return
		}
		ensureArtifacts(sessionResult).WorkspaceDiff = workspaceDiff
	}

	return func() {}, finalize
}

func ensureArtifacts(sessionResult *agent.SessionResult) *agent.SessionArtifacts {
	if sessionResult.Artifacts == nil {
		sessionResult.Artifacts = &agent.SessionArtifacts{}
	}
	return sessionResult.Artifacts
}

func sessionTranscript(sessionResult *agent.SessionResult) transcript.Transcript {
	if sessionResult == nil {
		return nil
	}
	return sessionResult.Transcript
}

func sessionWorkspaceDiff(sessionResult *agent.SessionResult) string {
	if sessionResult == nil || sessionResult.Artifacts == nil {
		return ""
	}
	return sessionResult.Artifacts.WorkspaceDiff
}

func sessionGeneratedFiles(sessionResult *agent.SessionResult) []string {
	if sessionResult == nil || sessionResult.Artifacts == nil {
		return nil
	}
	return sessionResult.Artifacts.GeneratedFiles
}

// toJudgeTurnResults converts evaluator TurnResults to judge InputTurnResults.
func toJudgeTurnResults(turns []TurnResult) []judge.InputTurnResult {
	if len(turns) == 0 {
		return nil
	}
	out := make([]judge.InputTurnResult, len(turns))
	for i, tr := range turns {
		out[i] = judge.InputTurnResult{
			TurnNumber: tr.TurnNumber,
			Content:    tr.Content,
			Response:   tr.Response,
			Transcript: tr.Transcript,
			Status:     string(tr.Status),
			Reason:     tr.Reason,
		}
	}
	return out
}

func prepareWorkspaceDiffState(ctx context.Context, rt runtime.Runtime, gitCtx *config.GitContext) (workspaceDiffState, error) {
	if gitCtx == nil || !gitCtx.Init {
		const probeScript = `if git rev-parse --git-dir >/dev/null 2>&1; then
  printf git
else
  printf no-git
fi`
		result, err := rt.Exec(ctx, probeScript, runtime.ExecOptions{Cwd: rt.Workspace()})
		if err != nil {
			return workspaceDiffState{}, fmt.Errorf("prepare workspace diff state: %w", err)
		}
		if strings.TrimSpace(result.Stdout) != "git" {
			logging.TraceContextf(ctx, "Judge: workspace diff disabled because %s is not a git repo", rt.Workspace())
			return workspaceDiffState{}, nil
		}
	}

	const script = `set -eu
if git rev-parse --git-dir >/dev/null 2>&1; then
  :
else
  git init -q >/dev/null 2>&1 || exit 0
fi
if ! git config user.name >/dev/null 2>&1; then
  git config user.name skill-up
fi
if ! git config user.email >/dev/null 2>&1; then
  git config user.email skill-up@example.invalid
fi
git add --all
git commit --allow-empty -qm skill-up-baseline
git rev-parse HEAD`
	result, err := rt.Exec(ctx, script, runtime.ExecOptions{Cwd: rt.Workspace()})
	if err != nil {
		return workspaceDiffState{}, fmt.Errorf("prepare workspace diff state: %w", err)
	}
	if result.ExitCode != 0 {
		return workspaceDiffState{}, fmt.Errorf("prepare workspace diff state exited with code %d: %s", result.ExitCode, result.Stderr)
	}
	baselineRev := strings.TrimSpace(result.Stdout)
	if baselineRev == "" {
		logging.TraceContextf(ctx, "Judge: workspace diff disabled because git baseline initialization did not produce a revision")
		return workspaceDiffState{}, nil
	}
	return workspaceDiffState{enabled: true, baselineRev: baselineRev}, nil
}

func collectWorkspaceDiff(ctx context.Context, rt runtime.Runtime, state workspaceDiffState, generatedFiles []string) (string, error) {
	if !state.enabled {
		return "", nil
	}

	cmd := strings.Builder{}
	cmd.WriteString("set -eu\n")
	cmd.WriteString(`git add --all` + "\n")
	cmd.WriteString(`git diff --cached --no-ext-diff ` + shellQuote(state.baselineRev) + ` -- .`)
	for _, pathspec := range gitDiffExcludePathspecs(rt.Workspace(), generatedFiles) {
		cmd.WriteString(" " + shellQuote(pathspec))
	}

	result, err := rt.Exec(ctx, cmd.String(), runtime.ExecOptions{Cwd: rt.Workspace()})
	if err != nil {
		return "", fmt.Errorf("git diff workspace baseline: %w", err)
	}
	if result.ExitCode != 0 && result.ExitCode != 1 {
		return "", fmt.Errorf("git diff workspace baseline exited with code %d: %s", result.ExitCode, result.Stderr)
	}
	return result.Stdout, nil
}

func gitDiffExcludePathspecs(workspaceRoot string, generatedFiles []string) []string {
	pathspecs := make([]string, 0, len(generatedFiles))
	for _, artifactPath := range generatedFiles {
		relPath, ok := snapshotRelativeArtifactPath(workspaceRoot, artifactPath)
		if !ok {
			continue
		}
		pathspecs = append(pathspecs, ":(exclude,literal)"+filepath.ToSlash(relPath))
	}
	return pathspecs
}

func snapshotRelativeArtifactPath(workspaceRoot, artifactPath string) (string, bool) {
	cleanPath := filepath.Clean(artifactPath)
	if cleanPath == "." || cleanPath == "" {
		return "", false
	}

	if filepath.IsAbs(cleanPath) {
		if workspaceRoot == "" {
			return "", false
		}
		relPath, err := filepath.Rel(filepath.Clean(workspaceRoot), cleanPath)
		if err != nil {
			return "", false
		}
		if relPath == "." || relPath == "" || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
			return "", false
		}
		return relPath, true
	}

	if cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return "", false
	}
	return cleanPath, true
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (e *defaultEvaluator) downloadOutputs(ctx context.Context, configName, caseID, targetSubpath string, downloadFn func(targetDir string) error) {
	if configName == "" || caseID == "" {
		logging.WarnContextf(ctx, "Evaluator: configName or caseID is empty, skipping download")
		return
	}
	if e.outputDir == "" {
		logging.WarnContextf(ctx, "Evaluator: outputDir is empty, skipping download")
		return
	}
	targetDir := filepath.Join(e.outputDir, caseID, configName, "outputs", targetSubpath)
	if err := os.RemoveAll(targetDir); err != nil {
		logging.WarnContextf(ctx, "Evaluator: failed to clear target dir %s: %v", targetDir, err)
		return
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		logging.WarnContextf(ctx, "Evaluator: failed to create target dir %s: %v", targetDir, err)
		return
	}
	if err := downloadFn(targetDir); err != nil {
		logging.WarnContextf(ctx, "Evaluator: failed to download to %s: %v", targetSubpath, err)
	} else {
		logging.DebugContextf(ctx, "Evaluator: downloaded to %s: %s", targetSubpath, targetDir)
	}
}

func (e *defaultEvaluator) prepareOutputDir(ctx context.Context, configName, caseID, targetSubpath string) string {
	if configName == "" || caseID == "" || e.outputDir == "" {
		return ""
	}
	targetDir := filepath.Join(e.outputDir, caseID, configName, "outputs", targetSubpath)
	if _, err := os.Stat(targetDir); err == nil {
		if err := os.RemoveAll(targetDir); err != nil {
			logging.WarnContextf(ctx, "Evaluator: failed to clear target dir %s: %v", targetDir, err)
			return ""
		}
	} else if !os.IsNotExist(err) {
		logging.WarnContextf(ctx, "Evaluator: failed to stat target dir %s: %v", targetDir, err)
		return ""
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		logging.WarnContextf(ctx, "Evaluator: failed to create target dir %s: %v", targetDir, err)
		return ""
	}
	return targetDir
}

func artifactsInDir(files []string, dir string) bool {
	if dir == "" || len(files) == 0 {
		return false
	}
	cleanDir := filepath.Clean(dir)
	for _, file := range files {
		if !artifactInCleanDir(file, cleanDir) {
			return false
		}
	}
	return true
}

func artifactInCleanDir(file, cleanDir string) bool {
	cleanFile := filepath.Clean(file)
	if !filepath.IsAbs(cleanFile) {
		return false
	}
	rel, err := filepath.Rel(cleanDir, cleanFile)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (e *defaultEvaluator) ensureArtifactsInOutputDir(
	ctx context.Context,
	rt runtime.Runtime,
	configName,
	caseID,
	targetSubpath,
	preparedDir string,
	sessionResult *agent.SessionResult,
) {
	if sessionResult == nil || sessionResult.Artifacts == nil || len(sessionResult.Artifacts.GeneratedFiles) == 0 {
		return
	}
	if artifactsInDir(sessionResult.Artifacts.GeneratedFiles, preparedDir) {
		return
	}
	if preparedDir != "" {
		e.downloadArtifactsIntoDir(ctx, rt, targetSubpath, preparedDir, sessionResult)
		return
	}
	e.downloadArtifacts(ctx, rt, configName, caseID, targetSubpath, sessionResult)
}

func (e *defaultEvaluator) downloadArtifactsIntoDir(ctx context.Context, rt runtime.Runtime, targetSubpath, targetDir string, sessionResult *agent.SessionResult) {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		logging.WarnContextf(ctx, "Evaluator: failed to create target dir %s: %v", targetDir, err)
		return
	}
	if err := e.copySessionArtifacts(ctx, rt, targetDir, sessionResult); err != nil {
		logging.WarnContextf(ctx, "Evaluator: failed to download to %s: %v", targetSubpath, err)
	}
}

// serializeTranscript writes a transcript to a temporary JSON file and returns
// the file path, a cleanup function, and any error encountered.
func serializeTranscript(t transcript.Transcript) (string, func(), error) {
	noop := func() {}
	f, err := os.CreateTemp("", "eval-transcript-*.json")
	if err != nil {
		return "", noop, fmt.Errorf("create temp transcript file: %w", err)
	}
	data, err := json.Marshal(t)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", noop, fmt.Errorf("marshal transcript: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", noop, fmt.Errorf("write transcript: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", noop, fmt.Errorf("close transcript file: %w", err)
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	return f.Name(), cleanup, nil
}

// downloadArtifacts copies GeneratedFiles from an agent run into outputs/ under targetSubpath.
func (e *defaultEvaluator) downloadArtifacts(ctx context.Context, rt runtime.Runtime, configName, caseID, targetSubpath string, sessionResult *agent.SessionResult) {
	if sessionResult == nil || sessionResult.Artifacts == nil || len(sessionResult.Artifacts.GeneratedFiles) == 0 {
		return
	}
	e.downloadOutputs(ctx, configName, caseID, targetSubpath, func(targetDir string) error {
		return e.copySessionArtifacts(ctx, rt, targetDir, sessionResult)
	})
}

func (e *defaultEvaluator) copySessionArtifacts(ctx context.Context, rt runtime.Runtime, targetDir string, sessionResult *agent.SessionResult) error {
	cleanTargetDir := filepath.Clean(targetDir)
	for _, file := range sessionResult.Artifacts.GeneratedFiles {
		if artifactInCleanDir(file, cleanTargetDir) {
			continue
		}
		fileName := filepath.Base(file)
		targetPath := filepath.Join(targetDir, fileName)
		if _, err := os.Stat(targetPath); err == nil {
			continue
		}
		if err := rt.DownloadFile(ctx, file, targetPath); err != nil {
			logging.WarnContextf(ctx, "Evaluator: failed to download artifact %s: %v", file, err)
		}
	}
	return nil
}
