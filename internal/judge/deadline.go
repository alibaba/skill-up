package judge

import (
	"context"
	"time"
)

type deadlineSourceKey struct{}

// JudgeTimeoutSource names the budget used for a judge's execution.
const JudgeTimeoutSource = "judge.timeout_seconds"

// WithTimeoutSource adds a timeout and records its source if it supplies the
// effective deadline. Equal deadlines keep the parent's attribution.
func WithTimeoutSource(ctx context.Context, timeout time.Duration, source string) (context.Context, context.CancelFunc, bool) {
	deadline := time.Now().Add(timeout)
	if parentDeadline, ok := ctx.Deadline(); ok && !deadline.Before(parentDeadline) {
		child, cancel := context.WithCancel(ctx)
		return child, cancel, false
	}
	child, cancel := context.WithDeadline(ctx, deadline)
	return ContextWithDeadlineSource(child, source), cancel, true
}

// ContextWithDeadlineSource names the configuration knob that armed the deadline
// carried by this context. An empty source stays unattributed downstream.
func ContextWithDeadlineSource(ctx context.Context, source string) context.Context {
	if source == "" {
		return ctx
	}
	return context.WithValue(ctx, deadlineSourceKey{}, source)
}

// DeadlineSource returns the recorded knob name, or "" when unknown.
func DeadlineSource(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	source, _ := ctx.Value(deadlineSourceKey{}).(string)
	return source
}
