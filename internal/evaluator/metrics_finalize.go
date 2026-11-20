package evaluator

import (
	"context"
	"errors"
	"strings"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/judge"
)

// recordSkippedJudgesForCase materializes skipped judges for a case that never
// reached judging. cause is the error that ended the agent run, if any: it lets a
// case deadline or cancellation keep its own termination cause.
func (e *defaultEvaluator) recordSkippedJudgesForCase(ctx context.Context, cause error, result *EvalResult, caseCfg *config.CaseConfig, reason string) {
	plan, err := config.ResolveJudgePlan(e.evalCfg, caseCfg)
	if err == nil {
		recordSkippedJudges(ctx, cause, result, plan, reason)
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
	production := metricProduction(ctx, result, stage)
	env := caseMetricsEnvelope(result, cfg, planned, stage, production)
	result.Metrics = env
	if result.Grading != nil {
		result.Grading.Metrics = env
	}

	contractError, contractCause := metricContractError(env, planned, production)
	switch {
	case contractError != "":
		applyMetricError(result, contractError)
	case env.Quality == judge.QualityNotMet:
		applyMetricFailure(result, unmetThresholds(env, planned))
	}
	result.Execution = deriveSingleExecution(ctx, result, env, planned, contractCause, stage)
}

func caseMetricsEnvelope(result *EvalResult, cfg config.JudgeConfig, planned []judge.MetricSlot, stage judge.Stage, production judge.MetricProduction) *judge.MetricsEnvelope {
	if result.Grading != nil && result.Grading.Metrics != nil {
		return result.Grading.Metrics
	}
	problem := judge.ProblemNone
	if cfg.Type != "script" && stage == judge.StageStarted {
		problem = judge.ProblemChannelUnsupported
	}
	return judge.MaterializeSlots(cfg.ID, planned, nil, problem, production)
}

// metricProduction reports whether a scoring pass was allowed to reach a
// successful final outcome. A pass that never started, was stopped mid-flight, or
// ended as an unsuccessful process leaves its planned slots NOT_PRODUCED rather
// than MISSING, so an execution failure is never reported as omitted output.
func metricProduction(ctx context.Context, result *EvalResult, stage judge.Stage) judge.MetricProduction {
	if metricsFinalized(result) {
		return judge.MetricProduced
	}
	if stage != judge.StageStarted || ctx.Err() != nil {
		return judge.MetricPrevented
	}
	if result.Error != nil && result.Grading == nil {
		// The judge failed before producing any grading result, so scoring never
		// reached an outcome to omit: the slots were not produced.
		return judge.MetricPrevented
	}
	if ex := gradingExecution(result); ex != nil && ex.Completion != judge.CompletionFinished {
		return judge.MetricPrevented
	}
	return judge.MetricProduced
}

func metricsFinalized(result *EvalResult) bool {
	return result.Grading != nil && result.Grading.MetricsFinalized
}

func gradingExecution(result *EvalResult) *judge.ExecutionMetadata {
	if result == nil || result.Grading == nil {
		return nil
	}
	return result.Grading.Execution
}

func metricContractError(env *judge.MetricsEnvelope, planned []judge.MetricSlot, production judge.MetricProduction) (string, judge.Cause) {
	byID := make(map[string]judge.MetricResult, len(env.Metrics))
	for _, metric := range env.Metrics {
		byID[metric.MetricID] = metric
		if metric.Availability == judge.Errored {
			return "named metric " + metric.MetricID + " is invalid: " + metric.Reason, judge.CauseContractError
		}
	}
	if production == judge.MetricPrevented {
		return "", judge.CauseNone
	}
	var declined string
	for _, slot := range planned {
		if !slot.Required && slot.MinScore == nil {
			continue
		}
		switch byID[slot.ID].Availability {
		case judge.Available, judge.NotProduced:
			continue
		case judge.Declined:
			if declined == "" {
				declined = slot.ID
			}
		default:
			return "required named metric unavailable: " + slot.ID, judge.CauseContractError
		}
	}
	if declined != "" {
		return "required named metric unavailable: " + declined, judge.CauseJudgeDeclined
	}
	return "", judge.CauseNone
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

// applyMetricError records a metric contract violation. Existing execution
// errors and script timeouts keep their verdict and retry classification.
func applyMetricError(result *EvalResult, message string) {
	observed := gradingExecution(result)
	if result.Status == judge.StatusError || (observed != nil && observed.Cause == judge.CauseDeadlineExpiry) {
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

func deriveSingleExecution(ctx context.Context, result *EvalResult, env *judge.MetricsEnvelope, planned []judge.MetricSlot, contractCause judge.Cause, stage judge.Stage) *judge.ExecutionMetadata {
	observed := gradingExecution(result)
	switch {
	case isRecoveredTimeout(observed):
		return completedMetricExecution(observed, env, planned)
	case observed != nil && observed.Cause == judge.CauseDeadlineExpiry:
		return observed
	case ctx.Err() != nil:
		stop := judge.ClassifyStop(ctx.Err(), judge.StopObservation{DeadlineSource: judge.DeadlineSource(ctx), SignalObserved: SignalObserved(ctx)})
		execution := interruptedExecution(stage, stop)
		if metricsFinalized(result) {
			return completedMetricExecution(execution, env, planned)
		}
		return execution
	case observed != nil:
		return observed
	case contractCause != judge.CauseNone:
		return &judge.ExecutionMetadata{Stage: judge.StageFinished, Completion: completionForContract(env, planned), Cause: contractCause}
	case result.Error != nil:
		return &judge.ExecutionMetadata{Stage: stage, Completion: judge.CompletionFailed, Cause: judge.CauseExecutionError, Detail: result.Error.Error()}
	default:
		return &judge.ExecutionMetadata{Stage: judge.StageFinished, Completion: completionForContract(env, planned), Cause: judge.CauseNone}
	}
}

// isRecoveredTimeout reports the collector's own fact: a deadline stopped the
// scoring pass, and a complete, invocation-bound, schema-valid result was still
// recovered from it. Only that combination makes the outcome final; a stop with
// nothing collected, or with a payload rejected as a whole, stays INTERRUPTED.
func isRecoveredTimeout(ex *judge.ExecutionMetadata) bool {
	return ex != nil && ex.Timeout != nil && ex.Timeout.Occurred && ex.Timeout.Recovered
}

// completedMetricExecution derives completion from validated metric slots
// while preserving the recorded stop and its provenance.
func completedMetricExecution(observed *judge.ExecutionMetadata, env *judge.MetricsEnvelope, planned []judge.MetricSlot) *judge.ExecutionMetadata {
	revised := *observed
	revised.Stage = judge.StageFinished
	revised.Completion = completionForContract(env, planned)
	return &revised
}
