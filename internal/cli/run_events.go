package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/evalevent"
	"github.com/alibaba/skill-up/internal/evaluator"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/runner"
	"github.com/alibaba/skill-up/internal/ui"
)

type evaluationEventOptions struct {
	Enabled    bool
	Path       string
	Attributes map[string]string
}

type evaluationEventStream struct {
	publisher *evalevent.Publisher
	lifecycle *evalevent.Lifecycle
	adapter   *evaluationEventAdapter
}

func evaluationEventOptionsFromFlags(cmd *cobra.Command) (evaluationEventOptions, error) {
	eventLogFlag := cmd.Flags().Lookup(eventLogFlagName)
	eventAttributeFlag := cmd.Flags().Lookup(eventAttributeFlagName)
	if eventLogFlag == nil && eventAttributeFlag == nil {
		return evaluationEventOptions{}, nil
	}

	eventLogChanged := eventLogFlag != nil && eventLogFlag.Changed
	eventAttributeChanged := eventAttributeFlag != nil && eventAttributeFlag.Changed
	if eventAttributeChanged && !eventLogChanged {
		return evaluationEventOptions{}, errors.New("--event-attribute requires --event-log")
	}
	if !eventLogChanged {
		return evaluationEventOptions{}, nil
	}

	path, err := cmd.Flags().GetString(eventLogFlagName)
	if err != nil {
		return evaluationEventOptions{}, fmt.Errorf("read --event-log: %w", err)
	}
	if path == "" {
		return evaluationEventOptions{}, errors.New("--event-log path must not be empty")
	}
	if path == "-" {
		return evaluationEventOptions{}, errors.New("--event-log does not support stdout; provide a file path")
	}

	var attributeValues []string
	if eventAttributeFlag != nil {
		attributeValues, err = cmd.Flags().GetStringArray(eventAttributeFlagName)
		if err != nil {
			return evaluationEventOptions{}, fmt.Errorf("read --event-attribute: %w", err)
		}
	}
	attributes, err := parseEvaluationEventAttributes(attributeValues)
	if err != nil {
		return evaluationEventOptions{}, err
	}

	return evaluationEventOptions{Enabled: true, Path: path, Attributes: attributes}, nil
}

func parseEvaluationEventAttributes(values []string) (map[string]string, error) {
	attributes := make(map[string]string, len(values))
	for _, value := range values {
		key, attributeValue, ok := strings.Cut(value, "=")
		if !ok || key == "" || attributeValue == "" {
			return nil, fmt.Errorf("invalid --event-attribute %q: expected non-empty key=value", value)
		}
		if _, exists := attributes[key]; exists {
			return nil, fmt.Errorf("duplicate --event-attribute key %q", key)
		}
		attributes[key] = attributeValue
	}
	if err := evalevent.ValidateUserAttributes(attributes); err != nil {
		return nil, fmt.Errorf("invalid --event-attribute: %w", err)
	}
	return attributes, nil
}

func runEvalWithEventLog(
	cmd *cobra.Command,
	cases []*config.CaseConfig,
	evalCfg *config.EvalConfig,
	loader *config.Loader,
	eventOptions evaluationEventOptions,
) error {
	evaluateOpts, err := evaluateOptionsFromFlags(cmd)
	if err != nil {
		return err
	}
	if len(evaluateOpts.Formats) == 0 {
		evaluateOpts.Formats = evalCfg.Report.Formats
	}

	planningRunner := runner.NewRunner(evalCfg, loader, nil, credential.ResolvedAgentConfig{})
	executionPlan := planningRunner.BuildExecutionPlan(cases, evaluateOpts)
	eventPlan, err := evaluationEventPlan(executionPlan)
	if err != nil {
		return fmt.Errorf("build event plan: %w", err)
	}
	eventPath, err := validateEvaluationEventLogPath(eventOptions.Path, executionPlan)
	if err != nil {
		return err
	}
	stream, err := startEvaluationEventStream(
		cmd.Context(),
		eventPath,
		eventOptions.Attributes,
		eventPlan,
		evalCfg.Engine.Name,
		executionPlan.ReportName,
	)
	if err != nil {
		return err
	}

	ag, resolver, runnerConfig, err := loadCredentialsAndAgent(cmd, evalCfg)
	if err != nil {
		return errors.Join(err, stream.finish(context.WithoutCancel(cmd.Context()), eventRunStatus(cmd.Context(), err)))
	}

	stream.adapter.setPhase(cmd.Context(), evalevent.RunPhaseExecuting)
	results, evalErr := executePlannedEvaluation(
		cmd,
		evalCfg,
		loader,
		resolver,
		runnerConfig,
		ag,
		executionPlan,
		evaluateOpts,
		stream.adapter,
	)
	finalErr := stream.finish(context.WithoutCancel(cmd.Context()), eventRunStatus(cmd.Context(), evalErr))
	if evalErr != nil {
		return errors.Join(evalErr, finalErr)
	}

	ui.Separator()
	passed, failed, errored := countResultStatus(results)
	ui.Summary(passed, failed, errored)
	return errors.Join(exitStatusError(cmd.ErrOrStderr(), results), finalErr)
}

func executePlannedEvaluation(
	cmd *cobra.Command,
	evalCfg *config.EvalConfig,
	loader *config.Loader,
	resolver *credential.Resolver,
	runnerConfig credential.ResolvedAgentConfig,
	ag agent.Agent,
	plan runner.ExecutionPlan,
	evaluateOpts runner.EvaluateOptions,
	adapter *evaluationEventAdapter,
) ([]evaluator.EvalResult, error) {
	ui.Blank()
	ui.Stepf("🚀", "Running evaluation (%d cases)", plan.CaseCount)

	evaluateOpts.Observer = &uiProgressObserver{}
	evaluateOpts.TaskObserver = adapter
	evaluateOpts.IterationObserver = adapter

	run := runner.NewRunner(evalCfg, loader, resolver, runnerConfig)
	logging.DebugContextf(cmd.Context(), "Runner: starting evaluation for %d case(s) with agent %s", plan.CaseCount, evalCfg.Engine.Name)
	results, err := run.EvaluatePlan(cmd.Context(), plan, ag, evaluateOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate cases: %w", err)
	}
	return results, nil
}

func startEvaluationEventStream(
	ctx context.Context,
	path string,
	attributes map[string]string,
	plan evalevent.Plan,
	engine string,
	skillName string,
) (*evaluationEventStream, error) {
	if err := evalevent.ValidatePlan(plan); err != nil {
		return nil, fmt.Errorf("invalid event plan: %w", err)
	}
	if err := evalevent.ValidateUserAttributes(attributes); err != nil {
		return nil, fmt.Errorf("invalid event attributes: %w", err)
	}

	sink, err := evalevent.NewJSONLFileSink(path)
	if err != nil {
		return nil, err
	}
	publisher, err := evalevent.NewPublisher(evalevent.PublisherConfig{Sink: sink, Attributes: attributes})
	if err != nil {
		return nil, errors.Join(err, sink.Close())
	}
	lifecycle, err := evalevent.NewLifecycle(publisher, plan, evalevent.LifecycleOptions{})
	if err != nil {
		return nil, errors.Join(err, publisher.Close())
	}
	adapter := &evaluationEventAdapter{lifecycle: lifecycle}
	stream := &evaluationEventStream{publisher: publisher, lifecycle: lifecycle, adapter: adapter}
	if err := lifecycle.Start(ctx, engine, skillName); err != nil {
		return nil, joinDistinctErrors(publisher.Close(), err)
	}
	return stream, nil
}

func (s *evaluationEventStream) finish(ctx context.Context, status evalevent.RunStatus) error {
	finishErr := s.lifecycle.Finish(ctx, status)
	closeErr := s.publisher.Close()
	return joinDistinctErrors(closeErr, finishErr, s.adapter.errValue())
}

func joinDistinctErrors(errs ...error) error {
	unique := make([]error, 0, len(errs))
	for _, candidate := range errs {
		if candidate == nil {
			continue
		}
		duplicate := false
		for _, existing := range unique {
			if errors.Is(existing, candidate) || errors.Is(candidate, existing) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			unique = append(unique, candidate)
		}
	}
	return errors.Join(unique...)
}

func eventRunStatus(ctx context.Context, invocationErr error) evalevent.RunStatus {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) ||
		errors.Is(invocationErr, context.Canceled) || errors.Is(invocationErr, context.DeadlineExceeded) {
		return evalevent.RunStatusCancelled
	}
	if invocationErr != nil {
		return evalevent.RunStatusError
	}
	return evalevent.RunStatusCompleted
}

func evaluationEventPlan(plan runner.ExecutionPlan) (evalevent.Plan, error) {
	iterations := make([]evalevent.Iteration, 0, len(plan.TaskPlan.Iterations))
	taskCount := 0
	for _, iteration := range plan.TaskPlan.Iterations {
		iterationNumber, err := positiveSafeEventInteger("iteration", iteration.Number)
		if err != nil {
			return evalevent.Plan{}, err
		}
		tasks := make([]evalevent.Task, 0, len(iteration.Tasks))
		for _, task := range iteration.Tasks {
			taskCount++
			if task.Case == nil {
				return evalevent.Plan{}, fmt.Errorf("task %q has no case", task.ID)
			}
			if task.Iteration != iteration.Number {
				return evalevent.Plan{}, fmt.Errorf("task %q iteration %d does not match iteration %d", task.ID, task.Iteration, iteration.Number)
			}
			if task.GlobalTotal != plan.TaskPlan.TaskTotal {
				return evalevent.Plan{}, fmt.Errorf("task %q total %d does not match plan total %d", task.ID, task.GlobalTotal, plan.TaskPlan.TaskTotal)
			}
			index, err := positiveSafeEventInteger("task index", task.GlobalIndex)
			if err != nil {
				return evalevent.Plan{}, fmt.Errorf("task %q: %w", task.ID, err)
			}
			configuration, err := evaluationEventConfiguration(task.Configuration)
			if err != nil {
				return evalevent.Plan{}, fmt.Errorf("task %q: %w", task.ID, err)
			}
			tasks = append(tasks, evalevent.Task{
				ID:            task.ID,
				Iteration:     iterationNumber,
				CaseID:        task.Case.ID,
				Configuration: configuration,
				Index:         index,
				Title:         task.Case.Title,
			})
		}
		iterations = append(iterations, evalevent.Iteration{Number: iterationNumber, Tasks: tasks})
	}
	if taskCount != plan.TaskPlan.TaskTotal {
		return evalevent.Plan{}, fmt.Errorf("expanded task count %d does not match plan total %d", taskCount, plan.TaskPlan.TaskTotal)
	}
	return evalevent.Plan{Iterations: iterations}, nil
}

func positiveSafeEventInteger(name string, value int) (uint64, error) {
	if value < 1 || uint64(value) > evalevent.MaxSafeInteger {
		return 0, fmt.Errorf("%s must be between 1 and %d", name, evalevent.MaxSafeInteger)
	}
	return uint64(value), nil
}

func evaluationEventConfiguration(configuration string) (evalevent.Configuration, error) {
	switch configuration {
	case evaluator.ConfigurationWithSkill:
		return evalevent.ConfigurationWithSkill, nil
	case evaluator.ConfigurationWithoutSkill:
		return evalevent.ConfigurationWithoutSkill, nil
	default:
		return "", fmt.Errorf("unsupported task configuration %q", configuration)
	}
}

func validateEvaluationEventLogPath(path string, plan runner.ExecutionPlan) (string, error) {
	canonicalPath, err := canonicalEventLogPath(path)
	if err != nil {
		return "", fmt.Errorf("invalid --event-log path %q: %w", path, err)
	}
	workspacePath, err := canonicalizePotentialPath(plan.WorkspaceDir)
	if err != nil {
		return "", fmt.Errorf("resolve evaluation workspace: %w", err)
	}
	if canonicalPath == workspacePath {
		return "", fmt.Errorf("--event-log path %q conflicts with the evaluation workspace", path)
	}
	for _, iteration := range plan.TaskPlan.Iterations {
		iterationPath, err := canonicalizePotentialPath(filepath.Join(plan.WorkspaceDir, fmt.Sprintf("iteration-%d", iteration.Number)))
		if err != nil {
			return "", fmt.Errorf("resolve iteration %d workspace: %w", iteration.Number, err)
		}
		if pathWithin(iterationPath, canonicalPath) {
			return "", fmt.Errorf("--event-log path %q resolves inside scheduled iteration directory %q", path, iterationPath)
		}
	}
	return canonicalPath, nil
}

func canonicalEventLogPath(path string) (string, error) {
	absolutePath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	_, err = os.Lstat(absolutePath)
	if err == nil {
		resolvedPath, resolveErr := filepath.EvalSymlinks(absolutePath)
		if resolveErr != nil {
			return "", resolveErr
		}
		resolvedInfo, statErr := os.Stat(resolvedPath)
		if statErr != nil {
			return "", statErr
		}
		if resolvedInfo.IsDir() {
			return "", errors.New("path is a directory")
		}
		return filepath.Clean(resolvedPath), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(absolutePath)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return "", fmt.Errorf("parent directory: %w", err)
	}
	if !parentInfo.IsDir() {
		return "", errors.New("parent path is not a directory")
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", fmt.Errorf("resolve parent directory: %w", err)
	}
	return filepath.Join(resolvedParent, filepath.Base(absolutePath)), nil
}

func canonicalizePotentialPath(path string) (string, error) {
	absolutePath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	current := absolutePath
	missing := make([]string, 0)
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return "", resolveErr
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %q", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func pathWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

type evaluationEventAdapter struct {
	lifecycle *evalevent.Lifecycle

	mu  sync.Mutex
	err error
}

func (a *evaluationEventAdapter) setPhase(ctx context.Context, phase evalevent.RunPhase) {
	a.record(a.lifecycle.SetPhase(eventObserverContext(ctx), phase))
}

func (a *evaluationEventAdapter) OnIterationStart(ctx context.Context, iteration evaluator.IterationPlan) {
	number, err := positiveSafeEventInteger("iteration", iteration.Number)
	if err != nil {
		a.record(err)
		return
	}
	a.record(a.lifecycle.IterationStarted(eventObserverContext(ctx), number))
}

func (a *evaluationEventAdapter) OnIterationComplete(ctx context.Context, iteration evaluator.IterationPlan, _ []evaluator.EvalResult) {
	number, err := positiveSafeEventInteger("iteration", iteration.Number)
	if err != nil {
		a.record(err)
		return
	}
	a.record(a.lifecycle.IterationCompleted(eventObserverContext(ctx), number))
}

func (a *evaluationEventAdapter) OnTaskStart(ctx context.Context, task evaluator.PlannedTask) {
	a.record(a.lifecycle.CaseStarted(eventObserverContext(ctx), task.ID))
}

func (a *evaluationEventAdapter) OnTaskComplete(ctx context.Context, task evaluator.PlannedTask, result evaluator.EvalResult) {
	status, err := evaluationEventCaseStatus(result.Status)
	if err != nil {
		a.record(fmt.Errorf("task %q: %w", task.ID, err))
		return
	}
	var passRate *float64
	if result.Grading != nil {
		value := result.Grading.Summary.PassRate
		passRate = &value
	}
	a.record(a.lifecycle.CaseCompleted(eventObserverContext(ctx), task.ID, status, passRate))
}

func eventObserverContext(ctx context.Context) context.Context {
	// These callbacks describe work that already happened. Preserve context
	// values, but do not let graceful cancellation poison the synchronous file
	// publisher before the final CANCELLED event can be attempted.
	return context.WithoutCancel(ctx)
}

func (a *evaluationEventAdapter) record(err error) {
	if err == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err == nil {
		a.err = err
	}
}

func (a *evaluationEventAdapter) errValue() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

func evaluationEventCaseStatus(status judge.Status) (evalevent.CaseStatus, error) {
	switch status {
	case judge.StatusPass:
		return evalevent.CaseStatusPass, nil
	case judge.StatusFail:
		return evalevent.CaseStatusFail, nil
	case judge.StatusError:
		return evalevent.CaseStatusError, nil
	case judge.StatusSkip:
		return evalevent.CaseStatusSkip, nil
	default:
		return "", fmt.Errorf("unsupported case status %q", status)
	}
}
