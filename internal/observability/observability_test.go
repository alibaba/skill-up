package observability

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestTracingEnabledFromEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	if tracingEnabledFromEnv() {
		t.Fatal("expected tracing disabled without endpoint or exporter")
	}

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4317")
	if !tracingEnabledFromEnv() {
		t.Fatal("expected tracing enabled with OTLP endpoint")
	}

	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	if tracingEnabledFromEnv() {
		t.Fatal("expected OTEL_TRACES_EXPORTER=none to disable tracing")
	}
}

func TestMetricsEnabledFromEnv(t *testing.T) {
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	if metricsEnabledFromEnv() {
		t.Fatal("expected metrics disabled without exporter")
	}

	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	if metricsEnabledFromEnv() {
		t.Fatal("expected metrics disabled with OTEL_METRICS_EXPORTER=none")
	}

	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	if !metricsEnabledFromEnv() {
		t.Fatal("expected metrics enabled with OTEL_METRICS_EXPORTER=otlp")
	}
}

func TestNormalizeProtocol(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "grpc", want: "grpc"},
		{input: " GRPC ", want: "grpc"},
		{input: "http", want: "http/protobuf"},
		{input: "http/protobuf", want: "http/protobuf"},
	}

	for _, tt := range tests {
		if got := normalizeProtocol(tt.input); got != tt.want {
			t.Fatalf("normalizeProtocol(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNewResourceIncludesEnvAttributes(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "custom-skill-up")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=ci,service.namespace=eval")
	t.Setenv(envSkillUpResourceAttrs, "telemetry.project.id=744,telemetry.component=cli")

	res, err := newResource(context.Background(), "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}

	attrs := map[attribute.Key]string{}
	for _, attr := range res.Attributes() {
		attrs[attr.Key] = attr.Value.AsString()
	}

	want := map[attribute.Key]string{
		"service.name":           "custom-skill-up",
		"service.version":        "v1.2.3",
		"deployment.environment": "ci",
		"service.namespace":      "eval",
		"telemetry.project.id":   "744",
		"telemetry.component":    "cli",
	}
	for key, value := range want {
		if attrs[key] != value {
			t.Fatalf("resource attribute %s = %q, want %q", key, attrs[key], value)
		}
	}
}

func TestNewResourceOverridesOTelResourceAttributesWithSkillUpAttributes(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "telemetry.project.id=base")
	t.Setenv(envSkillUpResourceAttrs, "telemetry.project.id=900")

	res, err := newResource(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}

	attrs := map[attribute.Key]string{}
	for _, attr := range res.Attributes() {
		attrs[attr.Key] = attr.Value.AsString()
	}
	if attrs["telemetry.project.id"] != "900" {
		t.Fatalf("expected skill-up resource attributes to override OTel attributes, got %q", attrs["telemetry.project.id"])
	}
}

func TestAgentEnvInjectsTraceContextAndResourceAttrs(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "x-scope=skill-up")
	t.Setenv("OTEL_LOG_TOOL_DETAILS", "1")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=ci,telemetry.project.id=744")
	t.Setenv(envSkillUpAgentResourceAttrs, "telemetry.project.id=745,telemetry.component=agent")

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}))

	env := AgentEnv(ctx, nil, map[string]string{
		"skill_up.engine": "claude-code",
		"unsafe":          "a,b=c",
	})

	if env["TRACEPARENT"] == "" {
		t.Fatalf("expected TRACEPARENT, got env=%v", env)
	}
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://collector:4318" {
		t.Fatalf("expected endpoint to be propagated, got %q", env["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
	if env["OTEL_EXPORTER_OTLP_TRACES_HEADERS"] != "x-scope=skill-up" {
		t.Fatalf("expected trace headers to be propagated, got %q", env["OTEL_EXPORTER_OTLP_TRACES_HEADERS"])
	}
	if env["OTEL_LOG_TOOL_DETAILS"] != "1" {
		t.Fatalf("expected tool detail flag to be propagated, got %q", env["OTEL_LOG_TOOL_DETAILS"])
	}
	resourceAttrs := env["OTEL_RESOURCE_ATTRIBUTES"]
	for _, want := range []string{
		"deployment.environment=ci",
		"telemetry.project.id=745",
		"telemetry.component=agent",
		"skill_up.engine=claude-code",
		"skill_up.parent_trace_id=4bf92f3577b34da6a3ce929d0e0e4736",
		"skill_up.parent_span_id=00f067aa0ba902b7",
		"unsafe=a_b_c",
	} {
		if !strings.Contains(resourceAttrs, want) {
			t.Fatalf("expected resource attrs to contain %q, got %q", want, resourceAttrs)
		}
	}
	if strings.Contains(resourceAttrs, "telemetry.project.id=744") {
		t.Fatalf("expected agent resource attributes to replace parent project id, got %q", resourceAttrs)
	}
}

func TestAgentEnvUsesConfiguredAgentResourceAttributes(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318")
	t.Setenv(envSkillUpAgentResourceAttrs, "telemetry.project.id=901")

	env := AgentEnv(context.Background(), nil, nil)
	resourceAttrs := env["OTEL_RESOURCE_ATTRIBUTES"]
	if !strings.Contains(resourceAttrs, "telemetry.project.id=901") {
		t.Fatalf("expected configured agent resource attributes, got %q", resourceAttrs)
	}
}

func TestAgentEnvInjectsConfiguredAgentSpanAttributesAsBaggage(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318")
	t.Setenv(envSkillUpAgentSpanAttrs, "telemetry.project.id=745")

	env := AgentEnv(context.Background(), nil, nil)
	if !strings.Contains(env["BAGGAGE"], "telemetry.project.id=745") {
		t.Fatalf("expected configured agent span attributes in baggage, got %q", env["BAGGAGE"])
	}
}

func TestSpanAttributeProcessorAddsConfiguredAndContextAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(newSpanAttributeProcessor(map[string]string{
			"telemetry.component": "cli",
		})),
		sdktrace.WithSpanProcessor(recorder),
	)
	ctx := ContextWithSpanAttributes(context.Background(), map[string]string{
		"telemetry.project.id": "745",
	})
	_, span := provider.Tracer("test").Start(ctx, "test-span")
	span.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected one span, got %d", len(spans))
	}
	attrs := map[attribute.Key]string{}
	for _, attr := range spans[0].Attributes() {
		attrs[attr.Key] = attr.Value.AsString()
	}
	if attrs["telemetry.component"] != "cli" {
		t.Fatalf("expected configured span attribute, got %q", attrs["telemetry.component"])
	}
	if attrs["telemetry.project.id"] != "745" {
		t.Fatalf("expected context span attribute, got %q", attrs["telemetry.project.id"])
	}
}

func TestStartLinkedRootSpanCreatesIndependentTraceWithLink(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	ctx, parent := Tracer().Start(context.Background(), "parent")
	parentCtx := parent.SpanContext()
	childCtx, child := StartLinkedRootSpan(ctx, "child")
	childCtxSpan := trace.SpanContextFromContext(childCtx)
	child.End()
	parent.End()

	if childCtxSpan.TraceID() == parentCtx.TraceID() {
		t.Fatalf("expected linked span to start an independent trace, got %s", childCtxSpan.TraceID())
	}

	var childSpan sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == "child" {
			childSpan = span
			break
		}
	}
	if childSpan == nil {
		t.Fatal("expected child span to be recorded")
	}
	if childSpan.Parent().IsValid() {
		t.Fatalf("expected child span to have no parent, got %s", childSpan.Parent().SpanID())
	}
	links := childSpan.Links()
	if len(links) != 1 {
		t.Fatalf("expected one span link, got %d", len(links))
	}
	if links[0].SpanContext.TraceID() != parentCtx.TraceID() || links[0].SpanContext.SpanID() != parentCtx.SpanID() {
		t.Fatalf("unexpected link: got %s/%s want %s/%s",
			links[0].SpanContext.TraceID(), links[0].SpanContext.SpanID(),
			parentCtx.TraceID(), parentCtx.SpanID())
	}
}

func TestLinkedTraceTopologyCanBeDisabled(t *testing.T) {
	t.Setenv(envTraceTopology, "single")

	ctx, span := StartLinkedRootSpan(context.Background(), "child")
	defer span.End()
	if !trace.SpanContextFromContext(ctx).IsValid() {
		t.Fatal("expected span context")
	}
	if LinkedTraceTopologyEnabled() {
		t.Fatal("expected single topology to disable linked traces")
	}
}
