package logging

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestInfof_AlwaysVisible(t *testing.T) {
	SetVerbosity(0)
	defer SetVerbosity(0)

	out := captureStdout(t, func() {
		Infof("config loaded")
	})

	if !strings.Contains(out, "level=INFO") || !strings.Contains(out, "msg=\"config loaded\"") {
		t.Fatalf("expected info log, got %q", out)
	}
}

func TestDebugf_VisibleOnlyWhenVerbose(t *testing.T) {
	SetVerbosity(0)
	defer SetVerbosity(0)

	out := captureStdout(t, func() {
		Debugf("hidden")
	})
	if out != "" {
		t.Fatalf("expected no debug output without verbose, got %q", out)
	}

	SetVerbosity(1)
	out = captureStdout(t, func() {
		Debugf("shown")
	})
	if !strings.Contains(out, "level=DEBUG") || !strings.Contains(out, "msg=shown") {
		t.Fatalf("expected verbose debug log, got %q", out)
	}
}

func TestTracef_VisibleOnlyAtVerbosityTwo(t *testing.T) {
	SetVerbosity(1)
	defer SetVerbosity(0)

	out := captureStdout(t, func() {
		Tracef("hidden-trace")
	})
	if out != "" {
		t.Fatalf("expected no trace output at verbosity 1, got %q", out)
	}

	SetVerbosity(2)
	out = captureStdout(t, func() {
		Tracef("shown-trace")
	})
	if !strings.Contains(out, "level=TRACE") || !strings.Contains(out, "msg=shown-trace") {
		t.Fatalf("expected trace log at verbosity 2, got %q", out)
	}
}

func TestContextLogDoesNotIncludeTraceAttrs(t *testing.T) {
	SetVerbosity(0)
	defer SetVerbosity(0)

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
		Remote:  true,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanCtx)

	out := captureStdout(t, func() {
		InfoContextf(ctx, "config loaded")
	})

	if strings.Contains(out, "trace_id=") || strings.Contains(out, "span_id=") {
		t.Fatalf("unexpected trace attrs in log, got %q", out)
	}
}

func TestContextLogAddsSpanEvent(t *testing.T) {
	SetVerbosity(0)
	defer SetVerbosity(0)
	resetIncludeLogMessageForTest()
	t.Cleanup(resetIncludeLogMessageForTest)

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

	ctx, span := provider.Tracer("test").Start(context.Background(), "root")
	captureStdout(t, func() {
		InfoContextf(ctx, "config loaded from %s", "/tmp/eval.yaml")
	})
	span.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	events := spans[0].Events()
	if len(events) != 1 {
		t.Fatalf("span events = %d, want 1", len(events))
	}
	if events[0].Name != "skill_up.log" {
		t.Fatalf("event name = %q, want skill_up.log", events[0].Name)
	}
	assertEventAttr(t, events[0].Attributes, "log.severity", "INFO")
	assertEventAttr(t, events[0].Attributes, "log.message", "config loaded from /tmp/eval.yaml")
	assertNoEventAttr(t, events[0].Attributes, "log.template")
}

func TestContextLogCanSuppressSpanEventMessage(t *testing.T) {
	SetVerbosity(0)
	defer SetVerbosity(0)
	t.Setenv(envOtelLogMessage, "0")
	resetIncludeLogMessageForTest()
	t.Cleanup(resetIncludeLogMessageForTest)

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

	ctx, span := provider.Tracer("test").Start(context.Background(), "root")
	captureStdout(t, func() {
		InfoContextf(ctx, "config loaded from %s", "/tmp/eval.yaml")
	})
	span.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	events := spans[0].Events()
	if len(events) != 1 {
		t.Fatalf("span events = %d, want 1", len(events))
	}
	assertNoEventAttr(t, events[0].Attributes, "log.message")
}

func resetIncludeLogMessageForTest() {
	includeLogMessageValue = sync.OnceValue(includeLogMessageFromEnv)
}

func assertEventAttr(t *testing.T, attrs []attribute.KeyValue, key, want string) {
	t.Helper()
	for _, attr := range attrs {
		if string(attr.Key) == key {
			if got := attr.Value.AsString(); got != want {
				t.Fatalf("%s = %q, want %q", key, got, want)
			}
			return
		}
	}
	t.Fatalf("missing event attr %s in %v", key, attrs)
}

func assertNoEventAttr(t *testing.T, attrs []attribute.KeyValue, key string) {
	t.Helper()
	for _, attr := range attrs {
		if string(attr.Key) == key {
			t.Fatalf("unexpected event attr %s in %v", key, attrs)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	var buf bytes.Buffer
	restoreOutput := SetOutputForTest(&buf)

	fn()

	restoreOutput()
	return buf.String()
}
