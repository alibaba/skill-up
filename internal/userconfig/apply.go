package userconfig

import (
	"context"
	"maps"
	"os"
	"regexp"
	"sort"
	"strings"
)

var envPlaceholderPattern = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)

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
// Only env values from the user or explicit discovery layer expand ${VAR}.
// A newly expanded OTLP header is withheld when project defaults supply
// routing or transport settings for an exporter that would carry it.
func ApplyEnv(c Config) ([]string, error) {
	pending := telemetryEnv(c.Telemetry)
	routes := projectOTLPRouting(c, pending)
	for key, value := range c.Env {
		if _, exists := os.LookupEnv(key); exists || value == "" {
			continue
		}
		if _, exists := pending[key]; exists {
			continue
		}
		if source := c.envSources[key]; source == sourceUser || source == sourceExplicit {
			var expanded bool
			value, expanded = expandEnvDefault(value)
			// Keep project routing and pre-existing headers intact, but do
			// not add credentials derived by this new expansion to it.
			if expanded && routes.withholds(c, pending, key) {
				continue
			}
		}
		pending[key] = value
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

// projectOTLPRoutes records the project defaults that will reach the process
// environment and decide where an OTLP exporter sends data or which peer it
// accepts.
type projectOTLPRoutes struct {
	// transport is set by proxy and TLS root settings, which every exporter
	// in this process and in child agents can pick up.
	transport bool
	// options maps a signal ("" for the settings shared by all signals) to
	// the endpoint, protocol and TLS options the project supplies for it.
	options map[string]map[string]bool
}

// projectOTLPRouting checks structured and arbitrary defaults before any
// environment writes. Existing process variables and structured fields retain
// their usual precedence over defaults for the same key.
func projectOTLPRouting(c Config, pending map[string]string) projectOTLPRoutes {
	routes := projectOTLPRoutes{options: map[string]map[string]bool{}}
	for key, source := range map[string]string{
		envOtelExporterOTLPTracesEndpt: c.tracesEndpointSource,
		envOtelExporterOTLPTracesProto: c.tracesProtocolSource,
	} {
		if _, exists := os.LookupEnv(key); !exists && source == sourceProject && pending[key] != "" {
			routes.add(key)
		}
	}
	for key, value := range c.Env {
		if _, exists := os.LookupEnv(key); exists || value == "" {
			continue
		}
		if _, exists := pending[key]; !exists && c.envSources[key] == sourceProject {
			routes.add(key)
		}
	}
	return routes
}

func (r *projectOTLPRoutes) add(key string) {
	key = strings.ToUpper(key)
	if isTransportDefault(key) {
		r.transport = true
		return
	}
	signal, option, ok := parseOTLPOption(key)
	if !ok || option == otlpOptionHeaders {
		return
	}
	if r.options[signal] == nil {
		r.options[signal] = map[string]bool{}
	}
	r.options[signal][option] = true
}

// withholds reports whether a newly expanded header default must not be
// applied. A signal's own header follows that signal's effective route: its
// own options, then the shared ones it does not override from a trusted
// source. The shared header can reach more than one signal's exporter, so any
// project route withholds it rather than depending on every exporter or child
// agent to resolve precedence identically.
func (r *projectOTLPRoutes) withholds(c Config, pending map[string]string, key string) bool {
	signal, option, ok := parseOTLPOption(strings.ToUpper(key))
	if !ok || option != otlpOptionHeaders {
		return false
	}
	if r.transport {
		return true
	}
	if signal == "" {
		return len(r.options) > 0
	}
	if len(r.options[signal]) > 0 {
		return true
	}
	for shared := range r.options[""] {
		if !hasTrustedSignalOption(c, pending, signal, shared) {
			return true
		}
	}
	return false
}

// hasTrustedSignalOption reports whether the signal's own option is set, so
// the shared project value does not apply to it. Project values for the
// signal's own option are recorded in projectOTLPRoutes and never reach here.
func hasTrustedSignalOption(c Config, pending map[string]string, signal, option string) bool {
	key := otlpEnvPrefix + signal + "_" + option
	// An exporter treats an empty variable as unset and falls back to the
	// shared one.
	if value, exists := os.LookupEnv(key); exists {
		return value != ""
	}
	return pending[key] != "" || c.Env[key] != ""
}

// Resolve against the process environment before installing any defaults, so
// config entries cannot become expansion sources based on map or key ordering.
func expandEnvDefault(value string) (string, bool) {
	expanded := false
	result := envPlaceholderPattern.ReplaceAllStringFunc(value, func(ref string) string {
		if replacement, exists := os.LookupEnv(ref[2 : len(ref)-1]); exists {
			expanded = true
			return replacement
		}
		return ref
	})
	return result, expanded
}

const (
	otlpEnvPrefix     = "OTEL_EXPORTER_OTLP_"
	otlpOptionHeaders = "HEADERS"
)

// otlpRouteOptions lists the option suffixes this guard reads. A longer
// suffix precedes the suffix it ends with.
var otlpRouteOptions = []string{
	"CLIENT_CERTIFICATE", "CERTIFICATE", "ENDPOINT", "PROTOCOL", "INSECURE", otlpOptionHeaders,
}

// parseOTLPOption splits an upper-case OTEL_EXPORTER_OTLP_[<SIGNAL>_]<OPTION>
// key. The signal is empty for the options shared by all signals.
func parseOTLPOption(key string) (signal, option string, ok bool) {
	rest, found := strings.CutPrefix(key, otlpEnvPrefix)
	if !found {
		return "", "", false
	}
	for _, candidate := range otlpRouteOptions {
		if rest == candidate {
			return "", candidate, true
		}
		if prefix, matched := strings.CutSuffix(rest, "_"+candidate); matched && prefix != "" {
			return prefix, candidate, true
		}
	}
	return "", "", false
}

// isTransportDefault reports whether an upper-case key changes how any HTTPS
// or gRPC client reaches or verifies its peer. Go reads SSL_CERT_FILE and
// SSL_CERT_DIR for system roots on Linux and other Unix systems.
func isTransportDefault(key string) bool {
	switch key {
	case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "GRPC_PROXY", "NO_GRPC_PROXY", "REQUEST_METHOD",
		"SSL_CERT_FILE", "SSL_CERT_DIR":
		return true
	default:
		return false
	}
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

	out := make(map[string]string, len(userKwargs))
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
