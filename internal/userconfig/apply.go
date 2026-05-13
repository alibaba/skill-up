package userconfig

import (
	"context"
	"maps"
	"os"
	"sort"
	"strings"
)

const (
	envOtelServiceName             = "OTEL_SERVICE_NAME"
	envOtelTracesExporter          = "OTEL_TRACES_EXPORTER"
	envOtelExporterOTLPTracesEndpt = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	envOtelExporterOTLPTracesProto = "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"
	envOtelResourceAttributes      = "OTEL_RESOURCE_ATTRIBUTES"
	envOtelLogUserPrompts          = "OTEL_LOG_USER_PROMPTS"
	envOtelLogToolContent          = "OTEL_LOG_TOOL_CONTENT"
	envOtelLogToolDetails          = "OTEL_LOG_TOOL_DETAILS"
)

// ApplyEnv writes telemetry and arbitrary env defaults from c into the process
// environment, only setting keys that are currently unset. It returns the list
// of keys that were actually applied (sorted) for logging and debugging.
func ApplyEnv(c Config) ([]string, error) {
	pending := telemetryEnv(c.Telemetry)
	for k, v := range c.Env {
		if v == "" {
			continue
		}
		if _, exists := pending[k]; !exists {
			pending[k] = v
		}
	}

	keys := make([]string, 0, len(pending))
	for k := range pending {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	applied := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, ok := os.LookupEnv(k); ok {
			continue
		}
		if err := os.Setenv(k, pending[k]); err != nil {
			return applied, err
		}
		applied = append(applied, k)
	}
	return applied, nil
}

func telemetryEnv(t Telemetry) map[string]string {
	out := map[string]string{}
	if t.ServiceName != "" {
		out[envOtelServiceName] = t.ServiceName
	}
	if t.TracesExporter != "" {
		out[envOtelTracesExporter] = t.TracesExporter
	}
	if t.Traces.Endpoint != "" {
		out[envOtelExporterOTLPTracesEndpt] = t.Traces.Endpoint
	}
	if t.Traces.Protocol != "" {
		out[envOtelExporterOTLPTracesProto] = t.Traces.Protocol
	}
	if len(t.ResourceAttributes) > 0 {
		out[envOtelResourceAttributes] = serializeResourceAttributes(t.ResourceAttributes)
	}
	if t.Verbose != nil && *t.Verbose {
		out[envOtelLogUserPrompts] = "1"
		out[envOtelLogToolContent] = "1"
		out[envOtelLogToolDetails] = "1"
	}
	return out
}

func serializeResourceAttributes(attrs map[string]string) string {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := attrs[k]
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

// MergeKwargs returns a kwargs map for envType where eval.yaml entries always
// win and user-config entries fill in any missing keys.
func MergeKwargs(c Config, envType string, evalKwargs map[string]string) map[string]string {
	userKwargs := c.RuntimeKwargs[envType]
	if len(userKwargs) == 0 && len(evalKwargs) == 0 {
		return evalKwargs
	}

	out := make(map[string]string, len(userKwargs)+len(evalKwargs))
	maps.Copy(out, userKwargs)
	maps.Copy(out, evalKwargs)
	return out
}

type ctxKey struct{}

type ctxValue struct {
	cfg     Config
	sources []Source
}

// WithContext attaches a Config and sources to ctx for downstream consumers.
func WithContext(ctx context.Context, cfg Config, sources []Source) context.Context {
	return context.WithValue(ctx, ctxKey{}, ctxValue{cfg: cfg, sources: sources})
}

// FromContext retrieves a Config attached via WithContext.
func FromContext(ctx context.Context) (Config, []Source, bool) {
	if ctx == nil {
		return Config{}, nil, false
	}
	v, ok := ctx.Value(ctxKey{}).(ctxValue)
	if !ok {
		return Config{}, nil, false
	}
	return v.cfg, v.sources, true
}
