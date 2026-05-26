package observability

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func resetEnvConfigCache(t *testing.T) {
	t.Helper()
	envConfigCache.Lock()
	envConfigCache.initialized = false
	envConfigCache.config = envConfig{}
	envConfigCache.Unlock()
	t.Cleanup(func() {
		envConfigCache.Lock()
		envConfigCache.initialized = false
		envConfigCache.config = envConfig{}
		envConfigCache.Unlock()
	})
}

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

func TestInitWithoutExportersIsNoopAndCachesEnvironment(t *testing.T) {
	resetEnvConfigCache(t)
	t.Setenv(envOtelEndpoint, "")
	t.Setenv(envOtelTracesEndpoint, "")
	t.Setenv(envOtelTracesExporter, "")
	t.Setenv(envOtelMetricsExporter, "")
	t.Setenv(envTraceTopology, "single")

	ctx, shutdown, err := Init(context.Background(), "dev")
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if ctx == nil || shutdown == nil {
		t.Fatalf("Init returned nil ctx or shutdown: ctx=%v shutdown=%v", ctx, shutdown)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("noop shutdown returned error: %v", err)
	}
	if TracingEnabled() {
		t.Fatal("TracingEnabled() = true, want false after cached no-exporter init")
	}
	if LinkedTraceTopologyEnabled() {
		t.Fatal("LinkedTraceTopologyEnabled() = true, want false for SKILL_UP_TRACE_TOPOLOGY=single")
	}
}

func TestInitReturnsUnsupportedTraceProtocolError(t *testing.T) {
	resetEnvConfigCache(t)
	t.Setenv(envOtelTracesExporter, "otlp")
	t.Setenv(envOtelTracesProtocol, "ftp")

	_, shutdown, err := Init(context.Background(), "dev")
	if err == nil || !strings.Contains(err.Error(), "unsupported OTLP trace protocol") {
		t.Fatalf("Init error = %v, want unsupported trace protocol", err)
	}
	if shutdown != nil {
		t.Fatalf("shutdown = %v, want nil on init failure", shutdown)
	}
}

func TestInitWithConsoleMetricsExporterInstallsShutdown(t *testing.T) {
	resetEnvConfigCache(t)
	t.Setenv(envOtelEndpoint, "")
	t.Setenv(envOtelTracesEndpoint, "")
	t.Setenv(envOtelTracesExporter, "none")
	t.Setenv(envOtelMetricsExporter, "console")

	ctx, shutdown, err := Init(context.Background(), "dev")
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if ctx == nil || shutdown == nil {
		t.Fatalf("Init returned nil ctx or shutdown: ctx=%v shutdown=%v", ctx, shutdown)
	}
	if !metricsEnabledFromEnv() {
		t.Fatal("metricsEnabledFromEnv() = false, want cached true")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown returned error: %v", err)
	}
}

func TestMetricExporterValidation(t *testing.T) {
	t.Setenv(envOtelMetricsExporter, "console")
	exp, err := newMetricExporter(context.Background())
	if err != nil {
		t.Fatalf("console metric exporter returned error: %v", err)
	}
	if exp == nil {
		t.Fatal("console metric exporter is nil")
	}

	t.Setenv(envOtelMetricsExporter, "custom")
	if _, err := newMetricExporter(context.Background()); err == nil || !strings.Contains(err.Error(), "unsupported OTEL_METRICS_EXPORTER") {
		t.Fatalf("newMetricExporter error = %v, want unsupported exporter", err)
	}

	t.Setenv(envOtelMetricsExporter, "otlp")
	t.Setenv(envOtelMetricsProtocol, "ftp")
	if _, err := newMetricExporter(context.Background()); err == nil || !strings.Contains(err.Error(), "unsupported OTLP metrics protocol") {
		t.Fatalf("newMetricExporter error = %v, want unsupported metrics protocol", err)
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

func TestAgentEnvUsesCurrentEnvForEnablementAndAllowlist(t *testing.T) {
	t.Setenv(envOtelEndpoint, "http://parent-collector:4318")

	if env := AgentEnv(context.Background(), map[string]string{envOtelTracesExporter: "none"}, nil); env != nil {
		t.Fatalf("AgentEnv with current OTEL_TRACES_EXPORTER=none = %v, want nil", env)
	}

	env := AgentEnv(context.Background(), map[string]string{
		envOtelEndpoint:       "http://child-collector:4318",
		envOtelTracesExporter: "otlp",
	}, map[string]string{"skill_up.engine": "codex"})
	if env[envOtelEndpoint] != "http://child-collector:4318" {
		t.Fatalf("endpoint = %q, want current env override", env[envOtelEndpoint])
	}
	if !strings.Contains(env[envOtelResourceAttrs], "skill_up.engine=codex") {
		t.Fatalf("resource attrs = %q, want engine attr", env[envOtelResourceAttrs])
	}
}

func TestContextAttributeAccessorsReturnCopies(t *testing.T) {
	agentAttrs := map[string]string{"skill_up.case.id": "case-a"}
	ctx := ContextWithAgentAttributes(context.Background(), agentAttrs)
	agentAttrs["skill_up.case.id"] = "mutated"

	gotAgent := AgentAttributesFromContext(ctx)
	gotAgent["skill_up.case.id"] = "changed"
	if again := AgentAttributesFromContext(ctx); again["skill_up.case.id"] != "case-a" {
		t.Fatalf("agent attrs were mutable through accessor: %v", again)
	}

	spanAttrs := map[string]string{"telemetry.component": "cli"}
	ctx = ContextWithSpanAttributes(ctx, spanAttrs)
	spanAttrs["telemetry.component"] = "mutated"
	gotSpan := SpanAttributesFromContext(ctx)
	gotSpan["telemetry.component"] = "changed"
	if again := SpanAttributesFromContext(ctx); again["telemetry.component"] != "cli" {
		t.Fatalf("span attrs were mutable through accessor: %v", again)
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

func TestSpanAttributeProcessorLifecycleMethods(t *testing.T) {
	processor := newSpanAttributeProcessor(nil)
	processor.OnEnd(nil)
	if err := processor.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	if err := processor.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush returned error: %v", err)
	}
}

func TestShutdownAllJoinsErrors(t *testing.T) {
	errOne := errors.New("one")
	errTwo := errors.New("two")
	err := shutdownAll(
		context.Background(),
		func(context.Context) error { return nil },
		func(context.Context) error { return errOne },
		func(context.Context) error { return errTwo },
	)
	if !errors.Is(err, errOne) || !errors.Is(err, errTwo) {
		t.Fatalf("shutdownAll error = %v, want joined one and two", err)
	}
}

func TestMetricRecordingHelpersUseInitializedInstruments(t *testing.T) {
	orig := instruments
	instruments = newMetricInstruments()
	t.Cleanup(func() { instruments = orig })

	ctx := context.Background()
	RecordRunStarted(ctx, "codex", "gpt-5", 2)
	RecordCaseCompleted(ctx, "codex", "PASS", 123)
	RecordRuntimeExec(ctx, 0, 45)
	if !instruments.ensure() {
		t.Fatal("metric instruments failed to initialize")
	}
	if instruments.runCount() == nil || instruments.caseRunCount() == nil ||
		instruments.caseCount() == nil || instruments.caseDuration() == nil ||
		instruments.runtimeExecCount() == nil || instruments.runtimeExecDuration() == nil {
		t.Fatal("expected all metric instruments to be initialized")
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
