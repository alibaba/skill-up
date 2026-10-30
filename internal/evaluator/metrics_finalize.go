package evaluator

import (
	"context"
	"errors"
	"strings"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/judge"
)

func (e *defaultEvaluator) recordSkippedJudgesForCase(result *EvalResult, caseCfg *config.CaseConfig, reason string) {
	plan, err := config.ResolveJudgePlan(e.evalCfg, caseCfg)
	if err == nil {
		recordSkippedJudges(result, plan, reason)
	}
}

// finalizeJudgeMetrics applies the opt-in named-metric contract and preserves
// slots and execution facts outside grading for early returns and nil results.
func finalizeJudgeMetrics(ctx context.Context, result *EvalResult, cfg config.JudgeConfig) {
	planned := judge.SlotsFromConfig(cfg.Metrics)
	if len(planned) == 0 {
		return
	}
	stage := result.judgeStage
	if stage == "" {
		stage = judge.StageNotInvoked
	}
	env := caseMetricsEnvelope(ctx, result, cfg, planned, stage)
	result.Metrics = env
	if result.Grading != nil {
		result.Grading.Metrics = env
	}

	contractError := metricContractError(env, planned, metricProduction(ctx, result, stage))
	switch {
	case contractError != "":
		applyMetricError(result, contractError)
	case env.Quality == judge.QualityNotMet:
		applyMetricFailure(result, unmetThresholds(env, planned))
	}
	result.Execution = deriveSingleExecution(ctx, result, env, planned, contractError, stage)
}

func caseMetricsEnvelope(ctx context.Context, result *EvalResult, cfg config.JudgeConfig, planned []judge.MetricSlot, stage judge.Stage) *judge.MetricsEnvelope {
	if result.Grading != nil && result.Grading.Metrics != nil {
		return result.Grading.Metrics
	}
	problem := judge.ProblemNone
	if cfg.Type != "script" && stage == judge.StageStarted {
		problem = judge.ProblemChannelUnsupported
	}
	return judge.MaterializeSlots(cfg.ID, planned, nil, problem, metricProduction(ctx, result, stage))
}

// metricProduction reports whether a scoring pass was allowed to reach a
// successful final outcome. A pass that never started, was stopped mid-flight, or
// ended as an unsuccessful process leaves its planned slots NOT_PRODUCED rather
// than MISSING, so an execution failure is never reported as omitted output.
func metricProduction(ctx context.Context, result *EvalResult, stage judge.Stage) judge.MetricProduction {
	if stage != judge.StageStarted || ctx.Err() != nil {
		return judge.MetricPrevented
	}
	if ex := gradingExecution(result); ex != nil && ex.Completion != judge.CompletionFinished {
		return judge.MetricPrevented
	}
	return judge.MetricProduced
}

func gradingExecution(result *EvalResult) *judge.ExecutionMetadata {
	if result == nil || result.Grading == nil {
		return nil
	}
	return result.Grading.Execution
}

func metricContractError(env *judge.MetricsEnvelope, planned []judge.MetricSlot, production judge.MetricProduction) string {
	byID := make(map[string]judge.MetricResult, len(env.Metrics))
	for _, metric := range env.Metrics {
		byID[metric.MetricID] = metric
		if metric.Availability == judge.Errored {
			return "named metric " + metric.MetricID + " is invalid: " + metric.Reason
		}
	}
	if production == judge.MetricPrevented {
		return ""
	}
	for _, slot := range planned {
		metric, ok := byID[slot.ID]
		if !ok {
			if slot.Required || slot.MinScore != nil {
				return "required named metric unavailable: " + slot.ID
			}
			continue
		}
		// A slot the execution never reached is an execution fact, not omitted
		// scoring output: it must not reclassify the verdict or its cause.
		if metric.Availability == judge.NotProduced {
			continue
		}
		if metric.Availability != judge.Available && (slot.Required || slot.MinScore != nil) {
			return "required named metric unavailable: " + slot.ID
		}
	}
	return ""
}

// unmetThresholds names the metrics whose collected value fell below the
// configured threshold, so the policy decision stays evidenced after the
// compatibility assertions are left untouched.
func unmetThresholds(env *judge.MetricsEnvelope, planned []judge.MetricSlot) []string {
	byID := make(map[string]judge.MetricResult, len(env.Metrics))
	for _, metric := range env.Metrics {
		byID[metric.MetricID] = metric
	}
	var unmet []string
	for _, slot := range planned {
		if slot.MinScore == nil {
			continue
		}
		metric, ok := byID[slot.ID]
		if !ok || metric.Availability != judge.Available || metric.Value == nil {
			continue
		}
		if *metric.Value < *slot.MinScore {
			unmet = append(unmet, slot.ID)
		}
	}
	return unmet
}

// applyMetricError records a metric contract violation. An execution error the
// judge already reported outranks it: the verdict, its error text and therefore
// its retry classification must survive.
func applyMetricError(result *EvalResult, message string) {
	if result.Status == judge.StatusError {
		result.Metrics.AddDiagnostic("metric_contract_error: " + message)
		return
	}
	result.Status = judge.StatusError
	result.Error = errors.New(message)
	if result.Grading != nil {
		result.Grading.Status = judge.StatusError
		result.Grading.ErrorReason = &message
	}
}

// applyMetricFailure turns a below-threshold score into a FAIL verdict without
// touching the assertion or member denominators, and never downgrades an
// execution outcome that already failed or errored.
func applyMetricFailure(result *EvalResult, unmet []string) {
	if len(unmet) > 0 {
		result.Metrics.AddDiagnostic("threshold_not_met: " + strings.Join(unmet, ","))
	}
	if result.Status != judge.StatusPass {
		return
	}
	result.Status = judge.StatusFail
	if result.Grading != nil {
		result.Grading.Status = judge.StatusFail
	}
}

func deriveSingleExecution(ctx context.Context, result *EvalResult, env *judge.MetricsEnvelope, planned []judge.MetricSlot, contractError string, stage judge.Stage) *judge.ExecutionMetadata {
	switch {
	case ctx.Err() != nil:
		stop := judge.ClassifyStop(ctx.Err(), judge.StopObservation{DeadlineSource: DeadlineSource(ctx), SignalObserved: SignalObserved(ctx)})
		return interruptedExecution(stage, stop)
	case result.Grading != nil && result.Grading.Execution != nil:
		return result.Grading.Execution
	case contractError != "":
		return &judge.ExecutionMetadata{Stage: judge.StageFinished, Completion: completionForContract(env, planned), Cause: judge.CauseContractError}
	case result.Error != nil:
		return &judge.ExecutionMetadata{Stage: stage, Completion: judge.CompletionFailed, Cause: judge.CauseExecutionError, Detail: result.Error.Error()}
	default:
		return &judge.ExecutionMetadata{Stage: judge.StageFinished, Completion: completionForContract(env, planned), Cause: judge.CauseNone}
	}
}
