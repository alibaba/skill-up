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

const judgeTimeoutSource = "judge.timeout_seconds"

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
		if ctx.Err() != nil {
			stop := judge.ClassifyStop(ctx.Err(), judge.StopObservation{DeadlineSource: DeadlineSource(ctx), SignalObserved: SignalObserved(ctx)})
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
		result.Error = multiJudgeError(ctx, result.JudgeResults, causes)
	}
	result.Configuration = configName
	return *result
}

func (e *defaultEvaluator) runJudgeMember(ctx context.Context, rt runtime.Runtime, caseCfg *config.CaseConfig, configName string, member config.JudgeConfig, turnsTotal int, runAgent agent.Agent, input judge.Input, result *EvalResult) (JudgeOutcome, []error) {
	started := time.Now()
	outcome := JudgeOutcome{ID: member.ID, Type: member.Type, Artifacts: "judge/" + member.ID + "/run"}
	e.prepareOutputDir(ctx, configName, caseCfg.ID, outcome.Artifacts)
	memberCtx := ctx
	memberDeadlineSource := ""
	cancel := func() {}
	if member.TimeoutSeconds != nil && *member.TimeoutSeconds > 0 {
		memberCtx, cancel = context.WithTimeout(ctx, time.Duration(*member.TimeoutSeconds)*time.Second)
		memberDeadlineSource = judgeTimeoutSource
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
	stop := memberStop(ctx, memberCtx, memberDeadlineSource)
	decision := memberDecisionFor(graded, stop)
	outcome.Metrics = gradedMetrics(member, graded, decision)
	outcome.Execution = memberExecution(graded, stop, decision, judge.SlotsFromConfig(member.Metrics))
	if memberErr := recordMemberContextError(memberCtx, &outcome, decision); memberErr != nil {
		causes = append(causes, memberErr)
	}
	outcome.DurationMs = time.Since(started).Milliseconds()
	return outcome, causes
}

func memberDecisionFor(graded EvalResult, stop judge.Stop) MemberDecision {
	if graded.Grading == nil || graded.Status == judge.StatusError {
		return MemberNotAccepted
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

func recordMemberContextError(ctx context.Context, outcome *JudgeOutcome, decision MemberDecision) error {
	err := ctx.Err()
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

// memberStop resolves why a member stopped. A member-level deadline is only
// attributed to judge.timeout_seconds when the parent context had not itself
// expired, so a case deadline firing through the member is never mislabeled.
func memberStop(parentCtx, memberCtx context.Context, memberDeadlineSource string) judge.Stop {
	if memberCtx.Err() == nil {
		return judge.Stop{Cause: judge.CauseNone}
	}
	var source string
	if errors.Is(memberCtx.Err(), context.DeadlineExceeded) {
		if parentCtx.Err() != nil {
			source = DeadlineSource(parentCtx)
		} else {
			source = memberDeadlineSource
		}
	}
	return judge.ClassifyStop(memberCtx.Err(), judge.StopObservation{DeadlineSource: source, SignalObserved: SignalObserved(parentCtx)})
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

// memberExecution describes one member's execution facts. Accepted results report
// their own completion; a stopped member without a final outcome is INTERRUPTED
// whether or not it had started, with the stage recording which of the two it was.
func memberExecution(graded EvalResult, stop judge.Stop, decision MemberDecision, planned []judge.MetricSlot) *judge.ExecutionMetadata {
	if decision != MemberAccepted && graded.Execution != nil && graded.Execution.Stage == judge.StageFinished {
		// A completed scoring pass can still violate the metric contract. Preserve
		// its FAILED/INCOMPLETE completion rather than rewriting it as an execution
		// interruption merely because the compatibility verdict is ERROR.
		return graded.Execution
	}
	if decision == MemberAccepted {
		return acceptedMemberExecution(graded, stop, planned)
	}
	stage := judge.StageStarted
	if graded.Grading == nil && graded.Status == judge.StatusSkip {
		stage = judge.StageNotInvoked
	}
	execution := interruptedExecution(stage, stop)
	if execution.Cause == judge.CauseNone {
		execution.Cause = judge.CauseExecutionError
		if graded.Error != nil {
			execution.Detail = graded.Error.Error()
		}
		if graded.Grading != nil {
			execution.Completion = judge.CompletionFailed
		}
	}
	return execution
}

func acceptedMemberExecution(graded EvalResult, stop judge.Stop, planned []judge.MetricSlot) *judge.ExecutionMetadata {
	// A judge that reported how its own pass ended outranks a completion derived
	// from slots: a script that exited unsuccessfully keeps FAILED/process_exit
	// even though it reached a final outcome.
	if graded.Execution != nil && graded.Execution.Cause == judge.CauseProcessExit {
		return graded.Execution
	}
	completion := judge.CompletionFinished
	env := graded.Metrics
	if graded.Grading != nil && graded.Grading.Metrics != nil {
		env = graded.Grading.Metrics
	}
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

func multiJudgeError(ctx context.Context, outcomes []JudgeOutcome, causes []error) error {
	if ctx.Err() != nil {
		causes = append(causes, ctx.Err())
	}
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
			details = append(details, outcome.ID+": "+detail)
		}
	}
	return "judges failed to complete: " + strings.Join(details, "; ")
}

// recordSkippedJudges materializes every planned member as skipped. Each member
// keeps execution metadata and its registry-derived metric slots even though no
// judge ran, so a gate or execution failure never loses that information. The
// reason strings are unchanged for compatibility; the machine-readable cause is
// the new source of truth.
func recordSkippedJudges(result *EvalResult, plan config.JudgePlan, reason string) {
	cause := causeForReason(reason)
	if !plan.Multi {
		if len(plan.Judges) > 0 {
			member := plan.Judges[0]
			result.Metrics = preventedMetrics(member.ID, member.Metrics)
			if result.Metrics != nil {
				result.Execution = interruptedExecution(judge.StageNotInvoked, judge.Stop{Cause: cause, Detail: reason})
			}
		}
		return
	}
	for _, member := range plan.Judges {
		result.JudgeResults = append(result.JudgeResults, JudgeOutcome{
			ID: member.ID, Type: member.Type, Status: judge.StatusSkip, SkipReason: reason,
			Execution: interruptedExecution(judge.StageNotInvoked, judge.Stop{Cause: cause, Detail: reason}),
			Metrics:   preventedMetrics(member.ID, member.Metrics),
		})
	}
	result.GroupCompletion, result.GroupCause = foldGroupState(result.JudgeResults)
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
