package evaluator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/judge"
)

type stopCase struct {
	name          string
	build         func(t *testing.T) (parent, member context.Context)
	memberTimeout bool
	want          judge.Stop
}

func expiredCaseDeadlineIntoMember(source string) func(*testing.T) (context.Context, context.Context) {
	return func(t *testing.T) (context.Context, context.Context) {
		t.Helper()
		parent := expiredDeadlineContext(source)
		member, cancel := context.WithCancel(parent)
		t.Cleanup(cancel)
		return parent, member
	}
}

func stopCases() []stopCase {
	return []stopCase{
		{
			name:          "no cancellation",
			build:         func(*testing.T) (context.Context, context.Context) { return context.Background(), context.Background() },
			memberTimeout: false,
			want:          judge.Stop{Cause: judge.CauseNone},
		},
		{
			name: "member-level deadline names its own knob",
			build: func(t *testing.T) (context.Context, context.Context) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
				t.Cleanup(cancel)
				<-ctx.Done()
				return context.Background(), ctx
			},
			memberTimeout: true,
			want:          judge.Stop{Cause: judge.CauseDeadlineExpiry, DeadlineSource: judgeTimeoutSource, Detail: "context deadline exceeded"},
		},
		{
			name:          "a case deadline firing through the member is not called a judge timeout",
			build:         expiredCaseDeadlineIntoMember("case.constraints.timeout_seconds"),
			memberTimeout: true,
			want:          judge.Stop{Cause: judge.CauseDeadlineExpiry, DeadlineSource: "case.constraints.timeout_seconds", Detail: "context deadline exceeded"},
		},
		{
			name: "inactive parent deadline metadata does not mask a judge timeout",
			build: func(t *testing.T) (context.Context, context.Context) {
				t.Helper()
				parent := ContextWithDeadlineSource(context.Background(), "case.constraints.timeout_seconds")
				member, cancel := context.WithTimeout(parent, 10*time.Millisecond)
				t.Cleanup(cancel)
				<-member.Done()
				return parent, member
			},
			memberTimeout: true,
			want:          judge.Stop{Cause: judge.CauseDeadlineExpiry, DeadlineSource: judgeTimeoutSource, Detail: "context deadline exceeded"},
		},
		{
			name: "a parent deadline with an unattributed origin stays unknown",
			build: func(t *testing.T) (context.Context, context.Context) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
				t.Cleanup(cancel)
				<-ctx.Done()
				return ctx, ctx
			},
			memberTimeout: true,
			want:          judge.Stop{Cause: judge.CauseDeadlineExpiry, DeadlineSource: judge.CancelUnspecified, Detail: "context deadline exceeded"},
		},
		{
			name: "cancellation without an observed signal stays unknown",
			build: func(*testing.T) (context.Context, context.Context) {
				return canceledContext(), canceledContext()
			},
			want: judge.Stop{Cause: judge.CauseCancellation, CancelSource: judge.CancelUnspecified, Detail: "context canceled"},
		},
		{
			name: "cancellation with an observed signal is attributed",
			build: func(*testing.T) (context.Context, context.Context) {
				return canceledWithSignalContext(), canceledWithSignalContext()
			},
			want: judge.Stop{Cause: judge.CauseCancellation, CancelSource: judge.CancelOSSignal, Detail: "context canceled"},
		},
	}
}

func TestMemberStopAttributesSources(t *testing.T) {
	cases := stopCases()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent, member := tc.build(t)
			memberDeadlineSource := ""
			if tc.memberTimeout {
				memberDeadlineSource = judgeTimeoutSource
			}
			got := memberStop(parent, member, memberDeadlineSource)
			if got != tc.want {
				t.Fatalf("memberStop = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func expiredDeadlineContext(source string) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	<-ctx.Done()
	return ContextWithDeadlineSource(ctx, source)
}

// canceledContext returns a context whose Err is already context.Canceled. cancel()
// sets Err synchronously, so there is nothing to wait for here.
func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func canceledWithSignalContext() context.Context {
	observer := &SignalObserver{}
	observer.MarkSignalObserved()
	ctx, cancel := context.WithCancel(ContextWithSignalObserver(context.Background(), observer))
	cancel()
	return ctx
}

func TestRecordMemberContextErrorKeepsAcceptedResults(t *testing.T) {
	cases := []struct {
		name       string
		accepted   bool
		start      judge.Status
		wantStatus judge.Status
		wantErr    bool
	}{
		{"accepted PASS survives a later cancellation", true, judge.StatusPass, judge.StatusPass, true},
		{"accepted FAIL survives a later cancellation", true, judge.StatusFail, judge.StatusFail, true},
		{"unaccepted PASS is escalated to ERROR", false, judge.StatusPass, judge.StatusError, true},
		{"an existing ERROR is left alone", false, judge.StatusError, judge.StatusError, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			outcome := JudgeOutcome{Status: tc.start}
			decision := MemberNotAccepted
			if tc.accepted {
				decision = MemberAccepted
			}
			err := recordMemberContextError(ctx, &outcome, decision)
			if outcome.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s", outcome.Status, tc.wantStatus)
			}
			if gotErr := err != nil; gotErr != tc.wantErr {
				t.Fatalf("returned error = %v, want err %v", err, tc.wantErr)
			}
			if tc.accepted && outcome.Error != "" {
				t.Fatalf("an accepted member must not gain an error string, got %q", outcome.Error)
			}
		})
	}
}

func TestCompletionForContract(t *testing.T) {
	env := func(states ...judge.Availability) *judge.MetricsEnvelope {
		ids := []string{"correctness", "completeness", "instruction_following"}
		out := &judge.MetricsEnvelope{}
		for i, s := range states {
			out.Metrics = append(out.Metrics, judge.MetricResult{MetricID: ids[i], Availability: s})
		}
		return out
	}
	cases := []struct {
		name    string
		env     *judge.MetricsEnvelope
		planned []judge.MetricSlot
		want    judge.Completion
	}{
		{"all available", env(judge.Available, judge.Available, judge.Available), []judge.MetricSlot{{ID: "correctness", Required: true}, {ID: "completeness"}, {ID: "instruction_following", Required: true}}, judge.CompletionFinished},
		{"optional omitted is complete", env(judge.Available, judge.Missing, judge.Available), []judge.MetricSlot{{ID: "correctness", Required: true}, {ID: "completeness"}, {ID: "instruction_following", Required: true}}, judge.CompletionFinished},
		{"required SKIP is incomplete", env(judge.Declined, judge.Available, judge.Available), []judge.MetricSlot{{ID: "correctness", Required: true}, {ID: "completeness"}, {ID: "instruction_following", Required: true}}, judge.CompletionIncomplete},
		{"cancelled slot is incomplete", env(judge.NotProduced, judge.Available, judge.Available), []judge.MetricSlot{{ID: "correctness", Required: true}, {ID: "completeness"}, {ID: "instruction_following", Required: true}}, judge.CompletionIncomplete},
		{"optional-only missing is complete", &judge.MetricsEnvelope{Metrics: []judge.MetricResult{{MetricID: "completeness", Availability: judge.Missing}}}, []judge.MetricSlot{{ID: "completeness"}}, judge.CompletionFinished},
		{"optional-only empty output is complete", &judge.MetricsEnvelope{Metrics: []judge.MetricResult{{MetricID: "completeness", Availability: judge.Missing}, {MetricID: "another_optional", Availability: judge.Missing}}}, []judge.MetricSlot{{ID: "completeness"}, {ID: "another_optional"}}, judge.CompletionFinished},
		{"thresholded unavailable is incomplete", env(judge.Available, judge.Available, judge.Missing), []judge.MetricSlot{{ID: "correctness", Required: true}, {ID: "completeness"}, {ID: "instruction_following", MinScore: new(0.7)}}, judge.CompletionIncomplete},
		{"an errored metric fails the contract", env(judge.Available, judge.Errored, judge.Available), []judge.MetricSlot{{ID: "correctness", Required: true}, {ID: "completeness"}, {ID: "instruction_following", Required: true}}, judge.CompletionFailed},
		{"an errored metric outranks an omission", env(judge.Missing, judge.Errored, judge.Available), []judge.MetricSlot{{ID: "correctness", Required: true}, {ID: "completeness"}, {ID: "instruction_following", Required: true}}, judge.CompletionFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := completionForContract(tc.env, tc.planned); got != tc.want {
				t.Fatalf("completion = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestPreventedMetricsKeepsEveryPlannedSlot(t *testing.T) {
	registry := []config.MetricConfig{{ID: "correctness", Required: true}, {ID: "completeness"}}
	env := preventedMetrics("j1", registry)
	if env == nil {
		t.Fatal("expected an envelope for a configured registry")
	}
	if len(env.Metrics) != 2 || env.Coverage.Planned != 2 || env.Coverage.NotProduced != 2 {
		t.Fatalf("envelope = %+v", env)
	}
	for _, metric := range env.Metrics {
		if metric.Availability != judge.NotProduced || metric.Value != nil {
			t.Fatalf("slot %s = %+v, want NOT_PRODUCED with no value", metric.MetricID, metric)
		}
	}
	if env.Quality != judge.QualityNotAssessed {
		t.Fatalf("quality = %s, want NOT_ASSESSED without thresholds", env.Quality)
	}
}

func TestPreventedMetricsWithThresholdsIsUnknown(t *testing.T) {
	registry := []config.MetricConfig{{ID: "correctness", MinScore: new(0.7)}}
	env := preventedMetrics("j1", registry)
	if env.Quality != judge.QualityUnknown {
		t.Fatalf("quality = %s, want UNKNOWN when a thresholded metric was never produced", env.Quality)
	}
}

func TestNoRegistryMeansNoEnvelope(t *testing.T) {
	if got := preventedMetrics("j1", nil); got != nil {
		t.Fatalf("legacy judge gained an envelope: %+v", got)
	}
}

func TestStopDetailKeepsLegacyGateReason(t *testing.T) {
	cases := []struct {
		stop judge.Stop
		want string
	}{
		{judge.Stop{Cause: judge.CauseExpectGate}, "gate_failed"},
		{judge.Stop{Cause: judge.CauseCancellation}, "cancelled before invocation"},
		{judge.Stop{Cause: judge.CauseDeadlineExpiry}, "case deadline expired"},
		{judge.Stop{Cause: judge.CauseNone}, ""},
		{judge.Stop{Cause: judge.CauseExecutionError, Detail: "boom"}, "boom"},
	}
	for _, tc := range cases {
		if got := stopDetail(tc.stop); got != tc.want {
			t.Fatalf("stopDetail(%s) = %q, want %q", tc.stop.Cause, got, tc.want)
		}
	}
}

func TestSkippedJudgesRetainSlotsPerCause(t *testing.T) {
	members := []config.JudgeConfig{
		{ID: "a", Type: "agent_judge", Metrics: []config.MetricConfig{{ID: "correctness", Required: true}}},
		{ID: "b", Type: "script", Metrics: []config.MetricConfig{{ID: "completeness"}}},
	}
	cases := []struct {
		reason string
		want   judge.Cause
	}{
		{"gate_failed", judge.CauseExpectGate},
		{"agent_execution_failed", judge.CauseExecutionError},
		{"invalid_judge_config", judge.CauseContractError},
		{"session_resumption_unsupported", judge.CauseContractError},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			result := &EvalResult{}
			recordSkippedJudges(result, config.JudgePlan{Judges: members, Multi: true}, tc.reason)
			if len(result.JudgeResults) != 2 {
				t.Fatalf("outcomes = %d, want 2", len(result.JudgeResults))
			}
			for _, outcome := range result.JudgeResults {
				if outcome.Status != judge.StatusSkip || outcome.SkipReason != tc.reason {
					t.Fatalf("compat fields changed: %+v", outcome)
				}
				if outcome.Execution == nil || outcome.Execution.Completion != judge.CompletionInterrupted ||
					outcome.Execution.Cause != tc.want || outcome.Execution.Stage != judge.StageNotInvoked {
					t.Fatalf("execution = %+v, want interrupted/not-invoked/%s", outcome.Execution, tc.want)
				}
				if outcome.Metrics == nil || outcome.Metrics.Coverage.Planned != 1 ||
					outcome.Metrics.Metrics[0].Availability != judge.NotProduced {
					t.Fatalf("metrics = %+v, want one NOT_PRODUCED slot", outcome.Metrics)
				}
			}
			if result.GroupCompletion != judge.CompletionInterrupted || result.GroupCause != tc.want {
				t.Fatalf("group state on the %s short circuit = %s/%s, want INTERRUPTED/%s", tc.reason, result.GroupCompletion, result.GroupCause, tc.want)
			}
		})
	}
}

func TestSkippedJudgesAreNoopForSingleJudgePlans(t *testing.T) {
	result := &EvalResult{}
	recordSkippedJudges(result, config.JudgePlan{Judges: []config.JudgeConfig{{ID: "a"}}}, "gate_failed")
	if len(result.JudgeResults) != 0 {
		t.Fatalf("single-judge plan gained member outcomes: %+v", result.JudgeResults)
	}
}

// TestMetadataSurvivesNilResult pins the rule that execution facts and planned
// slots are not stored only inside judge.Result.
func TestMetadataSurvivesNilResult(t *testing.T) {
	member := config.JudgeConfig{ID: "slow", Type: "script", Metrics: []config.MetricConfig{{ID: "correctness", Required: true}}}
	stop := judge.Stop{Cause: judge.CauseCancellation, CancelSource: judge.CancelUnspecified, Detail: "context canceled"}
	graded := EvalResult{Status: judge.StatusError, Grading: nil, Error: context.Canceled}
	outcome := JudgeOutcome{
		ID: member.ID, Type: member.Type, Status: graded.Status, Result: graded.Grading,
		Metrics:   gradedMetrics(member, graded, MemberNotAccepted),
		Execution: memberExecution(graded, stop, MemberNotAccepted, judge.SlotsFromConfig(member.Metrics)),
	}
	if outcome.Result != nil {
		t.Fatal("no Result may be fabricated to carry metadata")
	}
	if outcome.Execution.Completion != judge.CompletionInterrupted || outcome.Execution.Stage != judge.StageStarted {
		t.Fatalf("execution = %+v", outcome.Execution)
	}
	if outcome.Metrics == nil || outcome.Metrics.Metrics[0].Availability != judge.NotProduced {
		t.Fatalf("metrics = %+v", outcome.Metrics)
	}
	encoded, err := json.Marshal(outcome)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"completion_state":"INTERRUPTED"`, `"termination_cause":"cancellation"`, `"NOT_PRODUCED"`, `"correctness"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("persisted JSON missing %s: %s", want, encoded)
		}
	}
	var decoded JudgeOutcome
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Execution.Cause != judge.CauseCancellation || decoded.Execution.CancelSource != judge.CancelUnspecified {
		t.Fatalf("round-trip lost cause/source: %+v", decoded.Execution)
	}
	if decoded.Metrics.Quality != judge.QualityNotAssessed || decoded.Metrics.Coverage.Planned != 1 {
		t.Fatalf("round-trip lost derived state: %+v", decoded.Metrics)
	}
}

func TestAcceptedResultKeepsScoresAfterLaterStop(t *testing.T) {
	member := config.JudgeConfig{ID: "j1", Type: "agent_judge", Metrics: []config.MetricConfig{{ID: "correctness"}, {ID: "completeness", Required: true}}}
	env := judge.MaterializeSlots("j1", judge.SlotsFromConfig(member.Metrics), []judge.EmittedMetric{
		{MetricID: "correctness", Availability: "AVAILABLE", Value: new(0.8)},
	}, judge.ProblemNone, judge.MetricProduced)
	graded := EvalResult{Status: judge.StatusPass, Grading: &judge.Result{Status: judge.StatusPass, Metrics: env}}
	stop := judge.Stop{Cause: judge.CauseCancellation, CancelSource: judge.CancelOSSignal}
	outcome := JudgeOutcome{Status: graded.Status, Result: graded.Grading}
	execution := memberExecution(graded, stop, MemberAccepted, judge.SlotsFromConfig(member.Metrics))
	if execution.Completion != judge.CompletionIncomplete {
		t.Fatalf("completion = %s, want INCOMPLETE because one required slot was never reached", execution.Completion)
	}
	if execution.Cause != judge.CauseCancellation || execution.CancelSource != judge.CancelOSSignal {
		t.Fatalf("the later stop must stay visible: %+v", execution)
	}
	if outcome.Result.Metrics.Metrics[0].Availability != judge.Available {
		t.Fatal("an accepted score was downgraded")
	}
	if got := recordMemberContextError(canceledPlain(), &outcome, MemberAccepted); got == nil {
		t.Fatal("expected the cancellation error to still be reported")
	}
	if outcome.Status != judge.StatusPass {
		t.Fatalf("accepted verdict was rewritten: %s", outcome.Status)
	}
}

func TestTimeoutRemainsVisibleOnAcceptedResult(t *testing.T) {
	member := config.JudgeConfig{ID: "j1", Type: "agent_judge", Metrics: []config.MetricConfig{{ID: "correctness"}}}
	env := judge.MaterializeSlots("j1", judge.SlotsFromConfig(member.Metrics), []judge.EmittedMetric{
		{MetricID: "correctness", Availability: "AVAILABLE", Value: new(0.9)},
	}, judge.ProblemNone, judge.MetricProduced)
	graded := EvalResult{Status: judge.StatusPass, Grading: &judge.Result{Status: judge.StatusPass, Metrics: env}}
	stop := judge.Stop{Cause: judge.CauseDeadlineExpiry, DeadlineSource: judgeTimeoutSource, Detail: "context deadline exceeded"}
	execution := memberExecution(graded, stop, MemberAccepted, judge.SlotsFromConfig(member.Metrics))
	if execution.Timeout == nil || !execution.Timeout.Occurred || execution.Timeout.Source != judgeTimeoutSource {
		t.Fatalf("timeout metadata missing: %+v", execution)
	}
	if execution.Completion != judge.CompletionFinished {
		t.Fatalf("completion = %s, want COMPLETED for a fully scored accepted result", execution.Completion)
	}
}

func canceledPlain() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestSignalObserverReportsOnlyObservedSignals(t *testing.T) {
	observer := &SignalObserver{}
	ctx := ContextWithSignalObserver(context.Background(), observer)
	if SignalObserved(ctx) {
		t.Fatal("an un-triggered observer must not claim a signal")
	}
	observer.MarkSignalObserved()
	if !SignalObserved(ctx) {
		t.Fatal("an observed signal must be reported")
	}
	if SignalObserved(context.Background()) {
		t.Fatal("a context without an observer reports no signal")
	}
	var nilObserver *SignalObserver
	if nilObserver.Observed() {
		t.Fatal("nil observer must not report a signal")
	}
	nilObserver.MarkSignalObserved()
}

func TestGroupFoldOverMemberOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		states  []judge.Completion
		want    judge.Completion
		wantErr bool
	}{
		{"completed plus pending cancelled", []judge.Completion{judge.CompletionFinished, judge.CompletionInterrupted}, judge.CompletionInterrupted, false},
		{"failed plus pending cancelled keeps FAILED", []judge.Completion{judge.CompletionFailed, judge.CompletionInterrupted}, judge.CompletionFailed, false},
		{"incomplete plus pending cancelled keeps INCOMPLETE", []judge.Completion{judge.CompletionIncomplete, judge.CompletionInterrupted}, judge.CompletionIncomplete, false},
		{"empty group is refused", nil, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			completions := make([]judge.Completion, 0, len(tc.states))
			completions = append(completions, tc.states...)
			got, err := judge.FoldGroup(completions)
			if tc.wantErr {
				if !errors.Is(err, judge.ErrEmptyGroup) {
					t.Fatalf("want ErrEmptyGroup, got %v", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("fold = %s (%v), want %s", got, err, tc.want)
			}
		})
	}
}
