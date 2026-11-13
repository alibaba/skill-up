package evaluator

import (
	"context"
	"sync"
)

// Execution-provenance context values used to classify why a judge stopped.
// They exist so a cause is never reconstructed from an error string, and so an
// unattributable cancellation stays "unknown" instead of being guessed as
// user-initiated.

type (
	signalObserverKey struct{}
	deadlineSourceKey struct{}
)

// SignalObserver records whether this process actually observed an interrupt.
// The flag is set by the signal handler path, never inferred afterwards.
type SignalObserver struct {
	mu    sync.Mutex
	fired bool
}

// MarkSignalObserved is called from the interrupt delivery path.
func (o *SignalObserver) MarkSignalObserved() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.fired = true
}

// Observed reports whether a signal was seen. A false result with no observer
// attached means the source is unknown, not that no signal occurred.
func (o *SignalObserver) Observed() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.fired
}

// ContextWithSignalObserver attaches the observer to ctx for downstream
// classification.
func ContextWithSignalObserver(ctx context.Context, observer *SignalObserver) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, signalObserverKey{}, observer)
}

// SignalObserved reports whether a process interrupt was observed for this ctx.
func SignalObserved(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	observer, ok := ctx.Value(signalObserverKey{}).(*SignalObserver)
	return ok && observer.Observed()
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
