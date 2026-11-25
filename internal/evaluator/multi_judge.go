package evaluator

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/runtime"
)

// JudgeOutcome preserves one independent judge result for evaluator callers.
type JudgeOutcome = judge.Outcome

const judgeTimeoutSource = judge.JudgeTimeoutSource

// MemberDecision indicates whether a judge result was accepted as the member's
// final outcome or interrupted before acceptance.
type MemberDecision uint8

const (
	// MemberNotAccepted means execution did not produce an accepted final result.
	MemberNotAccepted MemberDecision = iota
	// MemberAccepted means the judge's final result is retained.
	MemberAccepted
)

func judgePlanNeedsWorkspaceDiff(plan config.JudgePlan) bool {
	return slices.ContainsFunc(plan.Judges, judgeNeedsWorkspaceDiff)
}

func judgePlanNeedsWorkspaceFiles(plan config.JudgePlan) bool {
	for _, member := range plan.Judges {
		if member.Type != "rule_based" {
			continue
		}
		for _, rule := range member.Success {
			if len(rule.FilesExist) > 0 || len(rule.FilesNotExist) > 0 {
				return true
			}
		}
		for _, rule := range member.Failure {
			if len(rule.FilesExist) > 0 || len(rule.FilesNotExist) > 0 {
				return true
			}
		}
	}
	return false
}

func expectNeedsWorkspaceFiles(expectCfg *config.Expect) bool {
	if expectCfg == nil {
		return false
	}
	return len(expectCfg.FilesExist) > 0 ||
		len(expectCfg.FilesNotExist) > 0 ||
		len(expectCfg.FileContains) > 0
}

func (e *defaultEvaluator) runMultipleJudges(
	ctx context.Context,
	rt runtime.Runtime,
	caseCfg *config.CaseConfig,
	configName string,
	plan config.JudgePlan,
	turnsTotal int,
	runAgent agent.Agent,
	input judge.Input,
	result *EvalResult,
) EvalResult {
	result.JudgeResults = make([]JudgeOutcome, 0, len(plan.Judges))
	var causes []error
	for i, member := range plan.Judges {
		if err := ctx.Err(); err != nil {
			causes = append(causes, err)
			stop := judge.ClassifyStop(err, judge.StopObservation{DeadlineSource: judge.DeadlineSource(ctx), SignalObserved: SignalObserved(ctx)})
			for _, pending := range plan.Judges[i:] {
				result.JudgeResults = append(result.JudgeResults, JudgeOutcome{
					ID: pending.ID, Type: pending.Type, Status: judge.StatusSkip,
					SkipReason: stopDetail(stop),
					Execution:  interruptedExecution(judge.StageNotInvoked, stop),
					Metrics:    preventedMetrics(pending.ID, pending.Metrics),
				})
			}
			break
		}
		outcome, memberCauses := e.runJudgeMember(ctx, rt, caseCfg, configName, member, turnsTotal, runAgent, input, result)
		causes = append(causes, memberCauses...)
		result.JudgeResults = append(result.JudgeResults, outcome)
	}
	result.Grading, result.Status = judge.DefaultOutcomeAggregator().Aggregate(result.JudgeResults, result.Turns, turnsTotal)
	result.GroupCompletion, result.GroupCause = foldGroupState(result.JudgeResults)
	if result.Status == judge.StatusError {
		result.Error = multiJudgeError(result.JudgeResults, causes)
	}
	result.Configuration = configName
	return *result
}

func (e *defaultEvaluator) runJudgeMember(ctx context.Context, rt runtime.Runtime, caseCfg *config.CaseConfig, configName string, member config.JudgeConfig, turnsTotal int, runAgent agent.Agent, input judge.Input, result *EvalResult) (JudgeOutcome, []error) {
	started := time.Now()
	outcome := JudgeOutcome{ID: member.ID, Type: member.Type, Artifacts: "judge/" + member.ID + "/run"}
	e.prepareOutputDir(ctx, configName, caseCfg.ID, outcome.Artifacts)
	memberCtx := ctx
	cancel := func() {}
	if member.TimeoutSeconds != nil && *member.TimeoutSeconds > 0 {
		memberCtx, cancel, _ = judge.WithTimeoutSource(ctx, time.Duration(*member.TimeoutSeconds)*time.Second, judgeTimeoutSource)
	}
	defer cancel()
	memberResult := &EvalResult{SessionResult: result.SessionResult, CaseID: result.CaseID, CaseName: result.CaseName}
	graded := e.runJudgePhase(memberCtx, rt, caseCfg, configName, member, turnsTotal, runAgent, input, memberResult, outcome.Artifacts, true)
	finalizeJudgeMetrics(memberCtx, &graded, member)
	outcome.Status, outcome.Result = graded.Status, graded.Grading
	outcome.Session, outcome.Skills = graded.JudgeSession, graded.JudgeSkills
	if graded.Grading != nil {
		outcome.Recovery = graded.Grading.Recovery
	}
	outcome.Error = judgeOutcomeError(graded)
	causes := []error{}
	if graded.Status == judge.StatusError && graded.Error != nil {
		causes = append(causes, graded.Error)
	}
	if memberErr := applyMemberStop(memberCtx, &outcome, member, graded); memberErr != nil {
		causes = append(causes, memberErr)
	}
	outcome.DurationMs = time.Since(started).Milliseconds()
	return outcome, causes
}

func applyMemberStop(ctx context.Context, outcome *JudgeOutcome, member config.JudgeConfig, graded EvalResult) error {
	ctxErr := ctx.Err()
	stop := memberStop(ctx, ctxErr)
	decision := memberDecisionFor(graded, stop)
	outcome.Metrics = gradedMetrics(member, graded, decision)
	outcome.Execution = memberExecution(graded, stop, decision, judge.SlotsFromConfig(member.Metrics))
	// A deadline observed after metric collection belongs in the execution
	// metadata. It did not interrupt this member's scoring pass.
	if stop.Cause != judge.CauseDeadlineExpiry || !metricsFinalized(&graded) {
		return recordMemberContextError(ctxErr, outcome, decision)
	}
	return nil
}

func memberDecisionFor(graded EvalResult, stop judge.Stop) MemberDecision {
	if graded.Grading == nil || graded.Status == judge.StatusError {
		return MemberNotAccepted
	}
	if metricsFinalized(&graded) {
		return MemberAccepted
	}
	switch stop.Cause {
	case judge.CauseNone:
		return MemberAccepted
	case judge.CauseDeadlineExpiry:
		if envelopeHasScores(graded.Grading.Metrics) {
			return MemberAccepted
		}
	}
	return MemberNotAccepted
}

func recordMemberContextError(err error, outcome *JudgeOutcome, decision MemberDecision) error {
	if err == nil {
		return nil
	}
	// An accepted outcome keeps its own verdict; the stop is still recorded so the
	// timeout or cancellation stays visible in the execution metadata.
	if decision != MemberAccepted && outcome.Status != judge.StatusError {
		outcome.Status = judge.StatusError
		outcome.Error = err.Error()
	}
	return err
}

// memberStop classifies the captured error using the member's recorded deadline source.
func memberStop(ctx context.Context, ctxErr error) judge.Stop {
	return judge.ClassifyStop(ctxErr, judge.StopObservation{DeadlineSource: judge.DeadlineSource(ctx), SignalObserved: SignalObserved(ctx)})
}

// stopDetail renders a human-readable stop note. The machine-readable cause and
// source live in ExecutionMetadata and are never re-derived from this text.
func stopDetail(stop judge.Stop) string {
	switch stop.Cause {
	case judge.CauseDeadlineExpiry:
		return "case deadline expired"
	case judge.CauseCancellation:
		return "cancelled before invocation"
	case judge.CauseExpectGate:
		return "gate_failed"
	case judge.CauseNone:
		return ""
	default:
		return stop.Detail
	}
}

func interruptedExecution(stage judge.Stage, stop judge.Stop) *judge.ExecutionMetadata {
	meta := &judge.ExecutionMetadata{
		Stage:          stage,
		Completion:     judge.CompletionInterrupted,
		Cause:          stop.Cause,
		DeadlineSource: stop.DeadlineSource,
		CancelSource:   stop.CancelSource,
		Detail:         stop.Detail,
	}
	if stop.Cause == judge.CauseDeadlineExpiry {
		meta.Timeout = &judge.TimeoutMetadata{Occurred: true, Source: stop.DeadlineSource}
	}
	return meta
}

// preventedMetrics materializes every planned slot as NOT_PRODUCED for a member
// whose evaluation never reached a final outcome. The planned denominator is the
// registry, so a member that never started still reports each slot.
func preventedMetrics(judgeID string, registry []config.MetricConfig) *judge.MetricsEnvelope {
	slots := judge.SlotsFromConfig(registry)
	if len(slots) == 0 {
		return nil
	}
	return judge.MaterializeSlots(judgeID, slots, nil, judge.ProblemNone, judge.MetricPrevented)
}

// gradedMetrics builds the envelope from what a judge produced. A score that was
// already collected survives a later stop; slots that were never reached stay
// NOT_PRODUCED rather than becoming zero or looking omitted.
func gradedMetrics(member config.JudgeConfig, graded EvalResult, decision MemberDecision) *judge.MetricsEnvelope {
	slots := judge.SlotsFromConfig(member.Metrics)
	if len(slots) == 0 {
		return nil
	}
	if graded.Grading != nil && graded.Grading.Metrics != nil {
		return graded.Grading.Metrics
	}
	// Anything the execution layer stopped before a final outcome leaves its slots
	// NOT_PRODUCED: the metric was never reached, which is not the same as a judge
	// omitting or declining a metric in a completed result.
	production := judge.MetricPrevented
	if decision == MemberAccepted {
		production = judge.MetricProduced
	}
	return judge.MaterializeSlots(member.ID, slots, nil, judge.ProblemNone, production)
}

// memberExecution keeps scoring completion and stop metadata for final results,
// including a completed scoring pass with a contract error followed by a stop.
func memberExecution(graded EvalResult, stop judge.Stop, decision MemberDecision, planned []judge.MetricSlot) *judge.ExecutionMetadata {
	if observed := gradingExecution(&graded); observed != nil && observed.Cause == judge.CauseDeadlineExpiry {
		if isRecoveredTimeout(observed) {
			return completedMetricExecution(observed, memberEnvelope(graded), planned)
		}
		return observed
	}
	if decision == MemberAccepted || (metricsFinalized(&graded) && stop.Cause != judge.CauseNone) {
		return completedMemberExecution(graded, stop, planned)
	}
	if graded.Execution != nil {
		return graded.Execution
	}
	stage := graded.judgeStage
	if stage == "" {
		stage = judge.StageNotInvoked
	}
	execution := interruptedExecution(stage, stop)
	if execution.Cause == judge.CauseNone {
		execution.Cause = judge.CauseExecutionError
		if graded.Error != nil {
			execution.Detail = graded.Error.Error()
		}
		if graded.Status == judge.StatusError || graded.Grading != nil {
			execution.Completion = judge.CompletionFailed
		}
	}
	return execution
}

func completedMemberExecution(graded EvalResult, stop judge.Stop, planned []judge.MetricSlot) *judge.ExecutionMetadata {
	// A judge that reported how its own pass ended outranks a completion derived
	// from slots: a script that exited unsuccessfully keeps FAILED/process_exit
	// even though it reached a final outcome.
	if graded.Execution != nil && graded.Execution.Cause == judge.CauseProcessExit {
		return graded.Execution
	}
	completion := judge.CompletionFinished
	env := memberEnvelope(graded)
	if env != nil {
		completion = completionForContract(env, planned)
	}
	meta := &judge.ExecutionMetadata{Stage: judge.StageFinished, Completion: completion, Cause: judge.CauseNone}
	if stop.Cause == judge.CauseNone {
		return meta
	}
	meta.Cause, meta.DeadlineSource, meta.CancelSource, meta.Detail = stop.Cause, stop.DeadlineSource, stop.CancelSource, stop.Detail
	if stop.Cause == judge.CauseDeadlineExpiry {
		meta.Timeout = &judge.TimeoutMetadata{Occurred: true, Source: stop.DeadlineSource, Recovered: graded.Grading.Recovery != nil}
	}
	return meta
}

// memberEnvelope prefers the envelope the scoring pass produced over the one
// materialized for a member that never reached a result.
func memberEnvelope(graded EvalResult) *judge.MetricsEnvelope {
	if graded.Grading != nil && graded.Grading.Metrics != nil {
		return graded.Grading.Metrics
	}
	return graded.Metrics
}

// completionForContract maps the materialized slots onto the completion states:
// an errored slot failed the contract, an unfulfilled required slot or an
// unevaluable threshold leaves the outcome incomplete.
func completionForContract(env *judge.MetricsEnvelope, planned []judge.MetricSlot) judge.Completion {
	if env == nil {
		return judge.CompletionFinished
	}
	byID := make(map[string]judge.MetricResult, len(env.Metrics))
	for _, metric := range env.Metrics {
		byID[metric.MetricID] = metric
		if metric.Availability == judge.Errored {
			return judge.CompletionFailed
		}
	}
	for _, slot := range planned {
		metric, ok := byID[slot.ID]
		if !ok || metric.Availability != judge.Available {
			if slot.Required || slot.MinScore != nil {
				return judge.CompletionIncomplete
			}
		}
	}
	return judge.CompletionFinished
}

func multiJudgeError(outcomes []JudgeOutcome, causes []error) error {
	summary := errors.New(summarizeJudgeErrors(outcomes))
	if len(causes) == 0 {
		return summary
	}
	return fmt.Errorf("%w: %w", summary, errors.Join(causes...))
}

func judgeOutcomeError(graded EvalResult) string {
	if graded.Error != nil {
		return graded.Error.Error()
	}
	if graded.Grading != nil && graded.Grading.ErrorReason != nil {
		return *graded.Grading.ErrorReason
	}
	return ""
}

func summarizeJudgeErrors(outcomes []JudgeOutcome) string {
	var details []string
	for _, outcome := range outcomes {
		if outcome.Status == judge.StatusError || outcome.Status == judge.StatusSkip {
			detail := outcome.Error
			if detail == "" {
				detail = outcome.SkipReason
			}
			if execution := outcome.Execution; execution != nil && execution.Cause == judge.CauseDeadlineExpiry && execution.DeadlineSource != "" {
				detail += " (deadline via " + execution.DeadlineSource + ")"
			}
			details = append(details, outcome.ID+": "+detail)
		}
	}
	return "judges failed to complete: " + strings.Join(details, "; ")
}

// recordSkippedJudges materializes every planned member as skipped. Each member
// keeps execution metadata and its registry-derived metric slots even though no
// judge ran, so a gate or execution failure never loses that information. The
// reason strings are unchanged for compatibility; the machine-readable cause is
// the new source of truth and prefers the stop the case context actually observed.
func recordSkippedJudges(ctx context.Context, cause error, result *EvalResult, plan config.JudgePlan, reason string) {
	stop := skippedJudgeStop(ctx, cause, reason)
	if !plan.Multi {
		if len(plan.Judges) > 0 {
			member := plan.Judges[0]
			result.Metrics = preventedMetrics(member.ID, member.Metrics)
			if result.Metrics != nil {
				result.Execution = interruptedExecution(judge.StageNotInvoked, stop)
			}
		}
		return
	}
	for _, member := range plan.Judges {
		result.JudgeResults = append(result.JudgeResults, JudgeOutcome{
			ID: member.ID, Type: member.Type, Status: judge.StatusSkip, SkipReason: reason,
			Execution: interruptedExecution(judge.StageNotInvoked, stop),
			Metrics:   preventedMetrics(member.ID, member.Metrics),
		})
	}
	result.GroupCompletion, result.GroupCause = foldGroupState(result.JudgeResults)
}

// skippedJudgeStop records why judging was skipped. A context stop wins over the
// generic reason only when it is the same stop that ended the agent: the case
// context is expired and the failure wraps that very error. Otherwise the cause
// would claim a knob skill-up never armed — an agent reporting its own internal
// timeout, or an unrelated failure after the context expired, stays an execution
// error. A reason that already names a specific cause (an expect gate, an invalid
// configuration) is never reclassified.
func skippedJudgeStop(ctx context.Context, cause error, reason string) judge.Stop {
	stop := judge.Stop{Cause: causeForReason(reason), Detail: reason}
	if stop.Cause != judge.CauseExecutionError || cause == nil {
		return stop
	}
	ctxErr := ctx.Err()
	if ctxErr == nil || !errors.Is(cause, ctxErr) {
		return stop
	}
	return judge.ClassifyStop(cause, judge.StopObservation{
		DeadlineSource: judge.DeadlineSource(ctx),
		SignalObserved: SignalObserved(ctx),
	})
}

func causeForReason(reason string) judge.Cause {
	switch reason {
	case "gate_failed":
		return judge.CauseExpectGate
	case "session_resumption_unsupported", "invalid_judge_config":
		return judge.CauseContractError
	default:
		return judge.CauseExecutionError
	}
}

// foldGroupState derives plan-level state from the members: completion folds over
// member completions by the fixed rank, and the cause is the first reason that
// actually ended the plan. A per-member judge_declined is a scoring reason rather
// than a plan terminator, so it does not become the group cause. Members without
// execution metadata (legacy judges with no metric registry) contribute nothing,
// and an empty result leaves both values unset so the artifact omits them.
// envelopeHasScores reports whether a collected envelope carries at least one
// usable score, which is what lets a deadline-terminated member stay accepted.
func envelopeHasScores(env *judge.MetricsEnvelope) bool {
	if env == nil {
		return false
	}
	for _, metric := range env.Metrics {
		if metric.Availability == judge.Available {
			return true
		}
	}
	return false
}

func foldGroupState(outcomes []JudgeOutcome) (judge.Completion, judge.Cause) {
	completions := make([]judge.Completion, 0, len(outcomes))
	cause := judge.CauseNone
	for _, outcome := range outcomes {
		if outcome.Execution == nil {
			continue
		}
		completions = append(completions, outcome.Execution.Completion)
		if cause == judge.CauseNone && outcome.Execution.Cause != judge.CauseNone &&
			outcome.Execution.Cause != judge.CauseJudgeDeclined {
			cause = outcome.Execution.Cause
		}
	}
	if len(completions) == 0 {
		return "", judge.CauseNone
	}
	completion, err := judge.FoldGroup(completions)
	if err != nil {
		return "", cause
	}
	return completion, cause
}
