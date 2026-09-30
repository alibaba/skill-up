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
	snapshotProvider, ok := rt.(runtime.JudgeSnapshotProvider)
	if !ok {
		result.Status = judge.StatusError
		result.Error = fmt.Errorf("multi-judge workspace isolation is not supported by %T", rt)
		return *result
	}
	snapshot, err := snapshotProvider.CaptureJudgeSnapshot(ctx)
	if err != nil {
		result.Status = judge.StatusError
		result.Error = err
		for _, member := range plan.Judges {
			result.JudgeResults = append(result.JudgeResults, JudgeOutcome{
				ID: member.ID, Type: member.Type, Status: judge.StatusSkip, SkipReason: "snapshot_failed",
			})
		}
		return *result
	}
	defer snapshot.Close() //nolint:errcheck
	result.JudgeResults = make([]JudgeOutcome, 0, len(plan.Judges))
	var causes []error
	for i, member := range plan.Judges {
		if ctx.Err() != nil {
			for _, pending := range plan.Judges[i:] {
				result.JudgeResults = append(result.JudgeResults, JudgeOutcome{
					ID: pending.ID, Type: pending.Type, Status: judge.StatusSkip, SkipReason: "case deadline or cancellation",
				})
			}
			break
		}
		started := time.Now()
		outcome := JudgeOutcome{ID: member.ID, Type: member.Type, Artifacts: "judge/" + member.ID + "/run"}
		e.prepareOutputDir(ctx, configName, caseCfg.ID, outcome.Artifacts)
		memberCtx := ctx
		cancel := func() {}
		if member.TimeoutSeconds != nil && *member.TimeoutSeconds > 0 {
			memberCtx, cancel = context.WithTimeout(ctx, time.Duration(*member.TimeoutSeconds)*time.Second)
		}
		memberRT, cleanup, forkErr := snapshot.Fork(memberCtx)
		if forkErr != nil {
			outcome.Status, outcome.Error = judge.StatusError, forkErr.Error()
			causes = append(causes, forkErr)
		} else {
			memberInput := input
			memberInput.WorkspacePath = memberRT.Workspace()
			memberResult := &EvalResult{SessionResult: result.SessionResult, CaseID: result.CaseID, CaseName: result.CaseName}
			graded := e.runJudgePhase(memberCtx, memberRT, caseCfg, configName, member, turnsTotal, runAgent, memberInput, memberResult, outcome.Artifacts, true)
			outcome.Status, outcome.Result = graded.Status, graded.Grading
			outcome.Session, outcome.Skills = graded.JudgeSession, graded.JudgeSkills
			outcome.Error = judgeOutcomeError(graded)
			if graded.Status == judge.StatusError && graded.Error != nil {
				causes = append(causes, graded.Error)
			}
			cleanup()
		}
		if memberErr := recordMemberContextError(memberCtx, &outcome); memberErr != nil {
			causes = append(causes, memberErr)
		}
		cancel()
		outcome.DurationMs = time.Since(started).Milliseconds()
		result.JudgeResults = append(result.JudgeResults, outcome)
	}
	result.Grading, result.Status = judge.DefaultOutcomeAggregator().Aggregate(result.JudgeResults, result.Turns, turnsTotal)
	if result.Status == judge.StatusError {
		result.Error = multiJudgeError(ctx, result.JudgeResults, causes)
	}
	result.Configuration = configName
	return *result
}

func recordMemberContextError(ctx context.Context, outcome *JudgeOutcome) error {
	err := ctx.Err()
	if err != nil && outcome.Status != judge.StatusError {
		outcome.Status = judge.StatusError
		outcome.Error = err.Error()
	}
	return err
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
