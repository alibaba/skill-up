package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const levelTrace = slog.Level(-8)

const envOtelLogMessage = "SKILL_UP_OTEL_LOG_MESSAGE"

var (
	programLevel           = new(slog.LevelVar)
	logger                 = newLogger()
	outputMu               sync.Mutex
	output                 io.Writer = os.Stdout
	includeLogMessageValue           = sync.OnceValue(includeLogMessageFromEnv)
)

func init() {
	programLevel.Set(slog.LevelInfo)
}

// SetVerbosity configures the minimum enabled level based on CLI verbosity.
// 0 => info and above, 1 => debug and above, 2+ => trace and above.
func SetVerbosity(verbosity int) {
	switch {
	case verbosity >= 2:
		programLevel.Set(levelTrace)
	case verbosity == 1:
		programLevel.Set(slog.LevelDebug)
	default:
		programLevel.Set(slog.LevelInfo)
	}
}

// VerboseEnabled reports whether verbose logging is enabled.
func VerboseEnabled() bool {
	return programLevel.Level() <= slog.LevelDebug
}

// Infof prints an info-level log line. Info is always visible by default.
func Infof(format string, args ...any) {
	InfoContextf(context.TODO(), format, args...)
}

// InfoContextf prints an info-level log line with context.
func InfoContextf(ctx context.Context, format string, args ...any) {
	logf(ctx, slog.LevelInfo, format, args...)
}

// Debugf prints a debug-level log line when verbose logging is enabled.
func Debugf(format string, args ...any) {
	DebugContextf(context.TODO(), format, args...)
}

// DebugContextf prints a debug-level log line with context.
func DebugContextf(ctx context.Context, format string, args ...any) {
	logf(ctx, slog.LevelDebug, format, args...)
}

// Tracef prints a trace-level log line when trace logging is enabled.
func Tracef(format string, args ...any) {
	TraceContextf(context.TODO(), format, args...)
}

// TraceContextf prints a trace-level log line with context.
func TraceContextf(ctx context.Context, format string, args ...any) {
	logf(ctx, levelTrace, format, args...)
}

// Warnf prints a warning-level log line.
func Warnf(format string, args ...any) {
	WarnContextf(context.TODO(), format, args...)
}

// WarnContextf prints a warning-level log line with context.
func WarnContextf(ctx context.Context, format string, args ...any) {
	logf(ctx, slog.LevelWarn, format, args...)
}

// Errorf prints an error-level log line.
func Errorf(format string, args ...any) {
	ErrorContextf(context.TODO(), format, args...)
}

// ErrorContextf prints an error-level log line with context.
func ErrorContextf(ctx context.Context, format string, args ...any) {
	logf(ctx, slog.LevelError, format, args...)
}

func logf(ctx context.Context, level slog.Level, format string, args ...any) {
	if !logger.Enabled(ctx, level) {
		return
	}
	msg := fmt.Sprintf(format, args...)
	recordSpanEvent(ctx, level, msg)
	logger.Log(ctx, level, msg)
}

func newLogger() *slog.Logger {
	handler := slog.NewTextHandler(stdoutWriter{}, &slog.HandlerOptions{
		Level: programLevel,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			switch attr.Key {
			case slog.TimeKey:
				return slog.Attr{}
			case slog.LevelKey:
				attr.Value = slog.StringValue(normalizeLevel(attr.Value.String()))
			}
			return attr
		},
	})
	return slog.New(handler)
}

func normalizeLevel(level string) string {
	switch strings.ToUpper(level) {
	case "DEBUG-4":
		return "TRACE"
	case "WARN":
		return "WARNING"
	default:
		return strings.ToUpper(level)
	}
}

func recordSpanEvent(ctx context.Context, level slog.Level, msg string) {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("log.severity", normalizeLevel(level.String())),
	}
	if includeLogMessage() {
		attrs = append(attrs, attribute.String("log.message", msg))
	}
	span.AddEvent("skill_up.log", trace.WithAttributes(attrs...))
}

func includeLogMessage() bool {
	return includeLogMessageValue()
}

func includeLogMessageFromEnv() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(envOtelLogMessage)))
	return value != "0" && value != "false"
}

type stdoutWriter struct{}

func (stdoutWriter) Write(p []byte) (int, error) {
	outputMu.Lock()
	defer outputMu.Unlock()

	return output.Write(p)
}

// SetOutputForTest redirects package log output and returns a restore function.
func SetOutputForTest(w io.Writer) func() {
	outputMu.Lock()
	orig := output
	output = w
	outputMu.Unlock()

	return func() {
		outputMu.Lock()
		output = orig
		outputMu.Unlock()
	}
}
