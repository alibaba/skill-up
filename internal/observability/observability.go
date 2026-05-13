// Package observability wires optional OpenTelemetry tracing for skill-up.
package observability

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"sort"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	serviceName = "skill-up"
	tracerName  = "github.com/alibaba/skill-up"

	protocolGRPC         = "grpc"
	protocolHTTPProtobuf = "http/protobuf"

	envSkillUpResourceAttrs      = "SKILL_UP_OTEL_RESOURCE_ATTRIBUTES"
	envSkillUpAgentResourceAttrs = "SKILL_UP_AGENT_OTEL_RESOURCE_ATTRIBUTES"
	envSkillUpSpanAttrs          = "SKILL_UP_OTEL_SPAN_ATTRIBUTES"
	envSkillUpAgentSpanAttrs     = "SKILL_UP_AGENT_OTEL_SPAN_ATTRIBUTES"
	envTraceTopology             = "SKILL_UP_TRACE_TOPOLOGY"
	envOtelResourceAttrs         = "OTEL_RESOURCE_ATTRIBUTES"
	envOtelServiceName           = "OTEL_SERVICE_NAME"
	envOtelTracesExporter        = "OTEL_TRACES_EXPORTER"
	envOtelMetricsExporter       = "OTEL_METRICS_EXPORTER"
	envOtelEndpoint              = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOtelTracesEndpoint        = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	envOtelProtocol              = "OTEL_EXPORTER_OTLP_PROTOCOL"
	envOtelTracesProtocol        = "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"
	envOtelMetricsProtocol       = "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL"
	envOtelHeaders               = "OTEL_EXPORTER_OTLP_HEADERS"
	envOtelTracesHeaders         = "OTEL_EXPORTER_OTLP_TRACES_HEADERS"
	envOtelCompression           = "OTEL_EXPORTER_OTLP_COMPRESSION"
	envOtelTracesCompression     = "OTEL_EXPORTER_OTLP_TRACES_COMPRESSION"
	envOtelTimeout               = "OTEL_EXPORTER_OTLP_TIMEOUT"
	envOtelTracesTimeout         = "OTEL_EXPORTER_OTLP_TRACES_TIMEOUT"
	envOtelLogUserPrompts        = "OTEL_LOG_USER_PROMPTS"
	envOtelLogToolContent        = "OTEL_LOG_TOOL_CONTENT"
	envOtelLogToolDetails        = "OTEL_LOG_TOOL_DETAILS"
)

const traceTopologyLinked = "linked"

var otelEnvAllowlist = []string{
	envOtelEndpoint,
	envOtelHeaders,
	envOtelTracesEndpoint,
	envOtelTracesHeaders,
	envOtelProtocol,
	envOtelTracesProtocol,
	envOtelCompression,
	envOtelTracesCompression,
	envOtelTimeout,
	envOtelTracesTimeout,
	envOtelTracesExporter,
	envOtelServiceName,
	envOtelLogUserPrompts,
	envOtelLogToolContent,
	envOtelLogToolDetails,
}

var resourceAttrValueReplacer = strings.NewReplacer(",", "_", "=", "_", "\n", "_", "\r", "_")

// ShutdownFunc shuts down an observability provider.
type ShutdownFunc func(context.Context) error

var instruments = newMetricInstruments()

var envConfigCache = struct {
	sync.RWMutex

	initialized bool
	config      envConfig
}{}

type envConfig struct {
	tracingEnabled      bool
	metricsEnabled      bool
	linkedTraceTopology bool
}

type (
	agentAttrsContextKey struct{}
	spanAttrsContextKey  struct{}
)

// Init configures optional OpenTelemetry tracing and metrics based on standard
// environment variables. If no exporter is requested it leaves the global
// providers untouched.
func Init(ctx context.Context, version string) (context.Context, ShutdownFunc, error) {
	config := readEnvConfig()
	cacheEnvConfig(config)

	if !config.tracingEnabled && !config.metricsEnabled {
		return ctx, func(context.Context) error { return nil }, nil
	}

	res, err := newResource(ctx, version)
	if err != nil {
		return ctx, nil, err
	}

	shutdowns := make([]ShutdownFunc, 0, 2)
	if config.tracingEnabled {
		tp, err := newTracerProvider(ctx, res)
		if err != nil {
			return ctx, nil, err
		}
		otel.SetTracerProvider(tp)
		shutdowns = append(shutdowns, tp.Shutdown)
	}
	if config.metricsEnabled {
		mp, err := newMeterProvider(ctx, res)
		if err != nil {
			return ctx, nil, err
		}
		otel.SetMeterProvider(mp)
		shutdowns = append(shutdowns, mp.Shutdown)
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	ctx, span := Tracer().Start(ctx, "skill-up")
	return ctx, func(shutdownCtx context.Context) error {
		span.End()
		return shutdownAll(shutdownCtx, shutdowns...)
	}, nil
}

// Tracer returns the shared tracer used by skill-up instrumentation.
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// StartLinkedRootSpan starts a new trace root when linked topology is enabled.
// The new root span links back to the current span, preserving correlation
// without making the new operation a child in the same trace.
//
//nolint:spancheck // caller owns the returned span and must end it.
func StartLinkedRootSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	if !traceTopologyIsLinked() {
		return Tracer().Start(ctx, name)
	}

	parent := trace.SpanContextFromContext(ctx)
	if !parent.IsValid() {
		return Tracer().Start(ctx, name, trace.WithNewRoot())
	}

	ctx, span := Tracer().Start(
		ctx,
		name,
		trace.WithNewRoot(),
		trace.WithLinks(trace.Link{SpanContext: parent}),
	)
	span.SetAttributes(
		attribute.String("skill_up.linked_trace_id", parent.TraceID().String()),
		attribute.String("skill_up.linked_span_id", parent.SpanID().String()),
	)
	return ctx, span
}

// LinkedTraceTopologyEnabled reports whether agent and judge operations should
// be emitted as independent traces linked to the current case span.
func LinkedTraceTopologyEnabled() bool {
	return traceTopologyIsLinked()
}

// RecordRunStarted records the start of a skill-up run.
func RecordRunStarted(ctx context.Context, engine, model string, caseCount int) {
	if !instruments.ensure() {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("skill_up.engine", engine),
		attribute.String("skill_up.model", model),
	)
	instruments.runCount().Add(ctx, 1, attrs)
	instruments.caseCount().Record(ctx, int64(caseCount), attrs)
}

// RecordCaseCompleted records the result and duration of one evaluated case.
func RecordCaseCompleted(ctx context.Context, engine, status string, durationMs int64) {
	if !instruments.ensure() {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("skill_up.engine", engine),
		attribute.String("skill_up.case.status", status),
	)
	instruments.caseRunCount().Add(ctx, 1, attrs)
	instruments.caseDuration().Record(ctx, durationMs, attrs)
}

// RecordRuntimeExec records one runtime command execution.
func RecordRuntimeExec(ctx context.Context, exitCode int, durationMs int64) {
	if !instruments.ensure() {
		return
	}
	attrs := metric.WithAttributes(attribute.Int("process.exit_code", exitCode))
	instruments.runtimeExecCount().Add(ctx, 1, attrs)
	instruments.runtimeExecDuration().Record(ctx, durationMs, attrs)
}

// AgentEnv returns OpenTelemetry environment variables for a child agent
// process. currentEnv is the already-merged child environment and is used for
// override semantics before falling back to the parent process environment.
func AgentEnv(ctx context.Context, currentEnv map[string]string, attrs map[string]string) map[string]string {
	if !tracingEnabledForEnv(currentEnv) {
		return nil
	}
	attrs = mergeAttributeMaps(AgentAttributesFromContext(ctx), attrs)

	env := make(map[string]string)
	for _, key := range otelEnvAllowlist {
		if value := firstNonEmpty(currentEnv[key], os.Getenv(key)); value != "" {
			env[key] = value
		}
	}

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	if len(carrier) == 0 {
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		).Inject(ctx, carrier)
	}
	agentSpanAttrs := configuredAgentSpanAttributes(currentEnv)
	if len(agentSpanAttrs) > 0 {
		carrier.Set("baggage", mergeBaggageHeader(carrier.Get("baggage"), agentSpanAttrs))
	}
	for key, value := range carrier {
		if value != "" {
			env[strings.ToUpper(key)] = value
		}
	}

	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.IsValid() {
		attrs = mapsCloneString(attrs)
		attrs["skill_up.parent_trace_id"] = spanCtx.TraceID().String()
		attrs["skill_up.parent_span_id"] = spanCtx.SpanID().String()
	}
	agentResourceAttrs := parseResourceAttributes(firstNonEmpty(currentEnv[envSkillUpAgentResourceAttrs], os.Getenv(envSkillUpAgentResourceAttrs)))
	if len(attrs) > 0 || len(agentResourceAttrs) > 0 {
		env[envOtelResourceAttrs] = mergeResourceAttributes(
			firstNonEmpty(currentEnv[envOtelResourceAttrs], os.Getenv(envOtelResourceAttrs)),
			agentResourceAttrs,
			attrs,
		)
	}

	return env
}

// ContextWithConfiguredAgentSpanAttributes adds deployment-defined span
// attributes for the local spans that wrap a child agent process.
func ContextWithConfiguredAgentSpanAttributes(ctx context.Context, env map[string]string) context.Context {
	return ContextWithSpanAttributes(ctx, configuredAgentSpanAttributes(env))
}

// ContextWithSpanAttributes adds low-cardinality attributes to spans started
// under this context.
func ContextWithSpanAttributes(ctx context.Context, attrs map[string]string) context.Context {
	if len(attrs) == 0 {
		return ctx
	}
	merged := mergeAttributeMaps(SpanAttributesFromContext(ctx), attrs)
	return context.WithValue(ctx, spanAttrsContextKey{}, merged)
}

// SpanAttributesFromContext returns deployment-defined span attributes.
func SpanAttributesFromContext(ctx context.Context) map[string]string {
	return mapsCloneString(rawSpanAttributesFromContext(ctx))
}

func rawSpanAttributesFromContext(ctx context.Context) map[string]string {
	attrs, _ := ctx.Value(spanAttrsContextKey{}).(map[string]string)
	return attrs
}

// ContextWithAgentAttributes adds low-cardinality attributes that should be
// propagated to child agent processes.
func ContextWithAgentAttributes(ctx context.Context, attrs map[string]string) context.Context {
	if len(attrs) == 0 {
		return ctx
	}
	merged := mergeAttributeMaps(AgentAttributesFromContext(ctx), attrs)
	return context.WithValue(ctx, agentAttrsContextKey{}, merged)
}

// AgentAttributesFromContext returns child-process observability attributes.
func AgentAttributesFromContext(ctx context.Context) map[string]string {
	attrs, _ := ctx.Value(agentAttrsContextKey{}).(map[string]string)
	return mapsCloneString(attrs)
}

// TracingEnabled reports whether trace export is configured in the parent
// process environment.
func TracingEnabled() bool {
	return tracingEnabledFromEnv()
}

func tracingEnabledFromEnv() bool {
	if config, ok := cachedEnvConfig(); ok {
		return config.tracingEnabled
	}
	return tracingEnabledForEnv(nil)
}

func traceTopologyIsLinked() bool {
	if config, ok := cachedEnvConfig(); ok {
		return config.linkedTraceTopology
	}
	return traceTopologyIsLinkedFromEnv()
}

func readEnvConfig() envConfig {
	return envConfig{
		tracingEnabled:      tracingEnabledForEnv(nil),
		metricsEnabled:      metricsEnabledDirectFromEnv(),
		linkedTraceTopology: traceTopologyIsLinkedFromEnv(),
	}
}

func cacheEnvConfig(config envConfig) {
	envConfigCache.Lock()
	defer envConfigCache.Unlock()
	envConfigCache.config = config
	envConfigCache.initialized = true
}

func cachedEnvConfig() (envConfig, bool) {
	envConfigCache.RLock()
	defer envConfigCache.RUnlock()
	return envConfigCache.config, envConfigCache.initialized
}

func traceTopologyIsLinkedFromEnv() bool {
	topology := strings.TrimSpace(os.Getenv(envTraceTopology))
	return topology == "" || strings.EqualFold(topology, traceTopologyLinked)
}

func tracingEnabledForEnv(env map[string]string) bool {
	tracesExporter := strings.TrimSpace(os.Getenv(envOtelTracesExporter))
	if env != nil && strings.TrimSpace(env[envOtelTracesExporter]) != "" {
		tracesExporter = strings.TrimSpace(env[envOtelTracesExporter])
	}
	if strings.EqualFold(tracesExporter, "none") {
		return false
	}
	if strings.EqualFold(tracesExporter, "otlp") {
		return true
	}

	return firstNonEmpty(
		mapValue(env, envOtelEndpoint),
		mapValue(env, envOtelTracesEndpoint),
		os.Getenv(envOtelEndpoint),
		os.Getenv(envOtelTracesEndpoint),
	) != ""
}

func metricsEnabledFromEnv() bool {
	if config, ok := cachedEnvConfig(); ok {
		return config.metricsEnabled
	}
	return metricsEnabledDirectFromEnv()
}

func metricsEnabledDirectFromEnv() bool {
	exporter := strings.TrimSpace(os.Getenv(envOtelMetricsExporter))
	if exporter == "" || strings.EqualFold(exporter, "none") {
		return false
	}
	return true
}

func newTracerProvider(ctx context.Context, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	exp, err := newTraceExporter(ctx)
	if err != nil {
		return nil, err
	}

	return sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(newSpanAttributeProcessor(parseResourceAttributes(os.Getenv(envSkillUpSpanAttrs)))),
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	), nil
}

func newMeterProvider(ctx context.Context, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	exp, err := newMetricExporter(ctx)
	if err != nil {
		return nil, err
	}

	return sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)),
		sdkmetric.WithResource(res),
	), nil
}

func newTraceExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	protocol := firstNonEmpty(
		os.Getenv(envOtelTracesProtocol),
		os.Getenv(envOtelProtocol),
		protocolGRPC,
	)

	switch normalizeProtocol(protocol) {
	case protocolGRPC:
		return otlptracegrpc.New(ctx)
	case protocolHTTPProtobuf:
		return otlptracehttp.New(ctx)
	default:
		return nil, fmt.Errorf("unsupported OTLP trace protocol %q; supported values: grpc, http/protobuf", protocol)
	}
}

func newMetricExporter(ctx context.Context) (sdkmetric.Exporter, error) {
	exporter := strings.ToLower(strings.TrimSpace(os.Getenv(envOtelMetricsExporter)))
	switch exporter {
	case "otlp":
		return newOTLPMetricExporter(ctx)
	case "console":
		return stdoutmetric.New()
	default:
		return nil, fmt.Errorf("unsupported OTEL_METRICS_EXPORTER %q; supported values: otlp, console, none", exporter)
	}
}

func newOTLPMetricExporter(ctx context.Context) (sdkmetric.Exporter, error) {
	protocol := firstNonEmpty(
		os.Getenv(envOtelMetricsProtocol),
		os.Getenv(envOtelProtocol),
		protocolGRPC,
	)

	switch normalizeProtocol(protocol) {
	case protocolGRPC:
		return otlpmetricgrpc.New(ctx)
	case protocolHTTPProtobuf:
		return otlpmetrichttp.New(ctx)
	default:
		return nil, fmt.Errorf("unsupported OTLP metrics protocol %q; supported values: grpc, http/protobuf", protocol)
	}
}

func newResource(ctx context.Context, version string) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		attribute.String("service.name", firstNonEmpty(os.Getenv(envOtelServiceName), serviceName)),
	}
	for key, value := range parseResourceAttributes(os.Getenv(envSkillUpResourceAttrs)) {
		attrs = append(attrs, attribute.String(key, value))
	}
	if version != "" {
		attrs = append(attrs, attribute.String("service.version", version))
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(attrs...),
	)
	if err != nil && !errors.Is(err, resource.ErrPartialResource) {
		return nil, fmt.Errorf("failed to create OTLP resource: %w", err)
	}
	return res, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case protocolGRPC:
		return protocolGRPC
	case "http", protocolHTTPProtobuf:
		return protocolHTTPProtobuf
	default:
		return strings.ToLower(strings.TrimSpace(protocol))
	}
}

func mergeResourceAttributes(base string, attrMaps ...map[string]string) string {
	merged := parseResourceAttributes(base)

	for _, attrs := range attrMaps {
		for key, rawValue := range attrs {
			value := sanitizeResourceAttrValue(rawValue)
			if strings.TrimSpace(key) == "" || value == "" {
				continue
			}
			merged[strings.TrimSpace(key)] = value
		}
	}

	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(merged[key]) == "" {
			continue
		}
		parts = append(parts, key+"="+merged[key])
	}
	return strings.Join(parts, ",")
}

func parseResourceAttributes(raw string) map[string]string {
	result := make(map[string]string)
	for part := range strings.SplitSeq(raw, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		result[key] = value
	}
	return result
}

func configuredAgentSpanAttributes(env map[string]string) map[string]string {
	return parseResourceAttributes(firstNonEmpty(mapValue(env, envSkillUpAgentSpanAttrs), os.Getenv(envSkillUpAgentSpanAttrs)))
}

func mergeBaggageHeader(base string, attrs map[string]string) string {
	entries := make(map[string]string)
	if parsed, err := baggage.Parse(base); err == nil {
		for _, member := range parsed.Members() {
			entries[member.Key()] = member.Value()
		}
	}
	for key, value := range attrs {
		key = strings.TrimSpace(key)
		value = sanitizeResourceAttrValue(value)
		if key == "" || value == "" {
			continue
		}
		entries[key] = value
	}

	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	members := make([]baggage.Member, 0, len(keys))
	for _, key := range keys {
		member, err := baggage.NewMember(key, entries[key])
		if err != nil {
			continue
		}
		members = append(members, member)
	}
	merged, err := baggage.New(members...)
	if err != nil {
		return base
	}
	return merged.String()
}

func sanitizeResourceAttrValue(value string) string {
	return resourceAttrValueReplacer.Replace(strings.TrimSpace(value))
}

type spanAttributeProcessor struct {
	attrs []attribute.KeyValue
}

func newSpanAttributeProcessor(attrs map[string]string) spanAttributeProcessor {
	return spanAttributeProcessor{attrs: attributesFromMap(attrs)}
}

func (p spanAttributeProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	if len(p.attrs) > 0 {
		span.SetAttributes(p.attrs...)
	}
	attrs := attributesFromMap(rawSpanAttributesFromContext(ctx))
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

func (p spanAttributeProcessor) OnEnd(sdktrace.ReadOnlySpan) {}

func (p spanAttributeProcessor) Shutdown(context.Context) error {
	return nil
}

func (p spanAttributeProcessor) ForceFlush(context.Context) error {
	return nil
}

func attributesFromMap(attrs map[string]string) []attribute.KeyValue {
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]attribute.KeyValue, 0, len(keys))
	for _, key := range keys {
		value := sanitizeResourceAttrValue(attrs[key])
		if strings.TrimSpace(key) == "" || value == "" {
			continue
		}
		result = append(result, attribute.String(strings.TrimSpace(key), value))
	}
	return result
}

func mapValue(m map[string]string, key string) string {
	if m == nil {
		return ""
	}
	return m[key]
}

func mapsCloneString(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	result := make(map[string]string, len(m))
	maps.Copy(result, m)
	return result
}

func mergeAttributeMaps(base, override map[string]string) map[string]string {
	result := mapsCloneString(base)
	maps.Copy(result, override)
	return result
}

func shutdownAll(ctx context.Context, shutdowns ...ShutdownFunc) error {
	var errs []error
	for _, shutdown := range shutdowns {
		if err := shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type metricInstruments struct {
	once sync.Once
	err  error

	runCounter              metric.Int64Counter
	caseCounter             metric.Int64Counter
	caseCountHistogram      metric.Int64Histogram
	caseDurationHist        metric.Int64Histogram
	runtimeExecCounter      metric.Int64Counter
	runtimeExecDurationHist metric.Int64Histogram
}

func newMetricInstruments() *metricInstruments {
	return &metricInstruments{}
}

func (m *metricInstruments) init() {
	meter := otel.Meter(tracerName)
	m.runCounter, m.err = meter.Int64Counter("skill_up.run.count")
	if m.err != nil {
		return
	}
	m.caseCounter, m.err = meter.Int64Counter("skill_up.case.run.count")
	if m.err != nil {
		return
	}
	m.caseCountHistogram, m.err = meter.Int64Histogram("skill_up.run.case.count")
	if m.err != nil {
		return
	}
	m.caseDurationHist, m.err = meter.Int64Histogram("skill_up.case.duration")
	if m.err != nil {
		return
	}
	m.runtimeExecCounter, m.err = meter.Int64Counter("skill_up.runtime.exec.count")
	if m.err != nil {
		return
	}
	m.runtimeExecDurationHist, m.err = meter.Int64Histogram("skill_up.runtime.exec.duration")
}

func (m *metricInstruments) ensure() bool {
	m.once.Do(m.init)
	return m.err == nil
}

func (m *metricInstruments) runCount() metric.Int64Counter {
	return m.runCounter
}

func (m *metricInstruments) caseRunCount() metric.Int64Counter {
	return m.caseCounter
}

func (m *metricInstruments) caseCount() metric.Int64Histogram {
	return m.caseCountHistogram
}

func (m *metricInstruments) caseDuration() metric.Int64Histogram {
	return m.caseDurationHist
}

func (m *metricInstruments) runtimeExecCount() metric.Int64Counter {
	return m.runtimeExecCounter
}

func (m *metricInstruments) runtimeExecDuration() metric.Int64Histogram {
	return m.runtimeExecDurationHist
}
