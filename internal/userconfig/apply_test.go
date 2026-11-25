package userconfig

import (
	"context"
	"maps"
	"os"
	"slices"
	"testing"
)

func TestApplyEnv_SetsTelemetry(t *testing.T) {
	for _, k := range []string{envOtelServiceName, envOtelTracesExporter, envOtelExporterOTLPTracesEndpt, envOtelExporterOTLPTracesProto, envOtelResourceAttributes, envOtelLogUserPrompts, envOtelLogToolContent, envOtelLogToolDetails} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}

	c := Config{Telemetry: Telemetry{
		ServiceName:    "svc",
		TracesExporter: "otlp",
		Traces:         TracesConfig{Endpoint: "http://x", Protocol: "grpc"},
		ResourceAttributes: map[string]string{
			"b.key": "2",
			"a.key": "1",
		},
		Verbose: new(true),
	}}
	applied, err := ApplyEnv(c)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv(envOtelServiceName) != "svc" {
		t.Errorf("svc not set")
	}
	if got := os.Getenv(envOtelResourceAttributes); got != "a.key=1,b.key=2" {
		t.Errorf("resource attributes wrong: %q", got)
	}
	if os.Getenv(envOtelLogUserPrompts) != "1" {
		t.Errorf("verbose flag not set")
	}
	if !slices.Contains(applied, envOtelServiceName) {
		t.Errorf("applied list missing service name: %v", applied)
	}
}

func TestApplyEnv_RespectsExisting(t *testing.T) {
	t.Setenv(envOtelServiceName, "preexisting")
	c := Config{Telemetry: Telemetry{ServiceName: "from-config"}}
	applied, err := ApplyEnv(c)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv(envOtelServiceName) != "preexisting" {
		t.Errorf("preexisting clobbered")
	}
	if slices.Contains(applied, envOtelServiceName) {
		t.Errorf("should not report applied: %v", applied)
	}
}

func TestApplyEnv_ArbitraryEnv(t *testing.T) {
	_ = os.Unsetenv("MY_KEY")
	c := Config{Env: map[string]string{"MY_KEY": "v"}}
	if _, err := ApplyEnv(c); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("MY_KEY") != "v" {
		t.Errorf("MY_KEY not set")
	}
}

func TestApplyEnv_ExpandsPlaceholders(t *testing.T) {
	t.Setenv("SKILL_UP_TEST_SOURCE", "example-value")
	t.Setenv("SKILL_UP_TEST_EMPTY", "")
	t.Setenv("SKILL_UP_TEST_NESTED", "${SKILL_UP_TEST_SOURCE}")
	t.Setenv("SKILL_UP_TEST_ABSENT", "")
	_ = os.Unsetenv("SKILL_UP_TEST_ABSENT")
	for _, tc := range []struct{ name, value, want string }{
		{"embedded", "authorization=${SKILL_UP_TEST_SOURCE}", "authorization=example-value"},
		{"repeated", "${SKILL_UP_TEST_SOURCE}/${SKILL_UP_TEST_SOURCE}", "example-value/example-value"},
		{"unset", "${SKILL_UP_TEST_ABSENT}", "${SKILL_UP_TEST_ABSENT}"},
		{"empty", "${SKILL_UP_TEST_EMPTY}", ""},
		{"mixed", "${SKILL_UP_TEST_SOURCE}/${SKILL_UP_TEST_ABSENT}", "example-value/${SKILL_UP_TEST_ABSENT}"},
		{"single pass", "${SKILL_UP_TEST_NESTED}", "${SKILL_UP_TEST_SOURCE}"},
		{"bare reference", "$SKILL_UP_TEST_SOURCE", "$SKILL_UP_TEST_SOURCE"},
		{"shell syntax", "${SKILL_UP_TEST_ABSENT:-fallback}", "${SKILL_UP_TEST_ABSENT:-fallback}"},
		{"unclosed", "${SKILL_UP_TEST_SOURCE", "${SKILL_UP_TEST_SOURCE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const target = "SKILL_UP_TEST_TARGET"
			t.Setenv(target, "")
			_ = os.Unsetenv(target)
			var cfg Config
			cfg.overlay(Config{Env: map[string]string{target: tc.value}}, "user")
			applied, err := ApplyEnv(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got, exists := os.LookupEnv(target); !exists || got != tc.want {
				t.Fatalf("value = %q (set=%t), want %q", got, exists, tc.want)
			}
			if !slices.Contains(applied, target) || cfg.Env[target] != tc.value {
				t.Fatal("applied keys or original config changed unexpectedly")
			}
		})
	}
}

func TestApplyEnv_ExpansionPreservesPrecedence(t *testing.T) {
	const source, target = "SKILL_UP_TEST_A", "SKILL_UP_TEST_Z"
	t.Setenv(source, "")
	_ = os.Unsetenv(source)
	t.Setenv(target, "")
	_ = os.Unsetenv(target)
	var cfg Config
	cfg.overlay(Config{Env: map[string]string{source: "from-config", target: "${" + source + "}"}}, "user")
	if _, err := ApplyEnv(cfg); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(target); got != "${"+source+"}" {
		t.Fatalf("config defaults must not become expansion sources: %q", got)
	}
	t.Setenv(source, "from-shell")
	for _, existing := range []string{"shell-target", ""} {
		t.Setenv(target, existing)
		if _, err := ApplyEnv(cfg); err != nil {
			t.Fatal(err)
		}
		if got := os.Getenv(target); got != existing {
			t.Fatalf("existing value overwritten: %q", got)
		}
	}
}

func TestApplyEnv_WithoutSourceKeepsPlaceholders(t *testing.T) {
	const target = "SKILL_UP_TEST_TARGET"
	t.Setenv("SKILL_UP_TEST_SOURCE", "example-value")
	t.Setenv(target, "")
	_ = os.Unsetenv(target)
	cfg := Config{Env: map[string]string{target: "${SKILL_UP_TEST_SOURCE}"}}
	if _, err := ApplyEnv(cfg); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(target); got != cfg.Env[target] {
		t.Fatalf("config without a trusted source expanded: %q", got)
	}
}

func TestMergeKwargs_EvalWins(t *testing.T) {
	c := Config{RuntimeKwargs: map[string]Kwargs{
		"opensandbox": {"base_url": "from-config", "extensions": "ext"},
	}}
	merged := MergeKwargs(c, "opensandbox", map[string]string{"base_url": "from-eval"})
	if merged["base_url"] != "from-eval" {
		t.Errorf("eval should win: %q", merged["base_url"])
	}
	if merged["extensions"] != "ext" {
		t.Errorf("missing key should be filled: %q", merged["extensions"])
	}
}

func TestMergeKwargs_UnknownEnvType(t *testing.T) {
	c := Config{RuntimeKwargs: map[string]Kwargs{"custom": {"image": "x"}}}
	merged := MergeKwargs(c, "opensandbox", map[string]string{"base_url": "u"})
	if merged["base_url"] != "u" {
		t.Errorf("eval kwarg lost: %+v", merged)
	}
	if _, ok := merged["image"]; ok {
		t.Errorf("unrelated kwarg leaked: %+v", merged)
	}
}

func TestMergeKwargs_NilEval(t *testing.T) {
	c := Config{RuntimeKwargs: map[string]Kwargs{"opensandbox": {"base_url": "u"}}}
	merged := MergeKwargs(c, "opensandbox", nil)
	if merged["base_url"] != "u" {
		t.Errorf("user kwarg should fill: %+v", merged)
	}
}

func TestMergeKwargs_PreservesInputs(t *testing.T) {
	t.Parallel()

	userKwargs := Kwargs{"shared": "user", "user-only": "default"}
	evalKwargs := map[string]string{"shared": "eval", "eval-only": "explicit"}
	c := Config{RuntimeKwargs: map[string]Kwargs{"opensandbox": userKwargs}}
	merged := MergeKwargs(c, "opensandbox", evalKwargs)
	want := map[string]string{"shared": "eval", "user-only": "default", "eval-only": "explicit"}
	if !maps.Equal(merged, want) {
		t.Fatalf("merged = %v, want %v", merged, want)
	}
	merged["shared"] = "changed"
	if userKwargs["shared"] != "user" || evalKwargs["shared"] != "eval" {
		t.Fatal("merged map aliases an input")
	}
}

func TestConfigContextRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := Config{Env: map[string]string{"A": "B"}}
	sources := []Source{{Kind: "project", Path: ".skill-up.yaml"}}
	ctx := WithContext(context.Background(), cfg, sources)

	gotCfg, gotSources, ok := FromContext(ctx)
	if !ok {
		t.Fatal("FromContext ok = false, want true")
	}
	if gotCfg.Env["A"] != "B" {
		t.Fatalf("config from context = %#v", gotCfg)
	}
	if len(gotSources) != 1 || gotSources[0].Kind != "project" {
		t.Fatalf("sources from context = %#v", gotSources)
	}
	if _, _, ok := FromContext(context.Background()); ok {
		t.Fatal("FromContext on empty context = true, want false")
	}
}

func TestApplyEnv_PassesThroughSkillUpEnvFromUserEnvMap(t *testing.T) {
	// Project-routing attribute envs are not in the schema -- downstream
	// vendor bundles set them through the generic env: map. Verify pass-through.
	const key = "SKILL_UP_OTEL_RESOURCE_ATTRIBUTES"
	t.Setenv(key, "")
	_ = os.Unsetenv(key)

	c := Config{Env: map[string]string{key: "telemetry.project.id=744,telemetry.component=cli"}}
	if _, err := ApplyEnv(c); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(key); got != "telemetry.project.id=744,telemetry.component=cli" {
		t.Errorf("%s = %q", key, got)
	}
}

func TestApplyEnv_VerboseFalseExplicitDoesNotSetLogEnvs(t *testing.T) {
	for _, k := range []string{envOtelLogUserPrompts, envOtelLogToolContent, envOtelLogToolDetails} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	c := Config{Telemetry: Telemetry{Verbose: new(false)}}
	if _, err := ApplyEnv(c); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv(envOtelLogUserPrompts); v != "" {
		t.Errorf("verbose=false should not set %s, got %q", envOtelLogUserPrompts, v)
	}
}

func TestApplyEnv_ExpandedOTLPHeaderRouting(t *testing.T) {
	const (
		generic    = "OTEL_EXPORTER_OTLP_HEADERS"
		traces     = "OTEL_EXPORTER_OTLP_TRACES_HEADERS"
		metrics    = "OTEL_EXPORTER_OTLP_METRICS_HEADERS"
		logs       = "OTEL_EXPORTER_OTLP_LOGS_HEADERS"
		endpoint   = "OTEL_EXPORTER_OTLP_ENDPOINT"
		metricDest = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
		protocol   = "OTEL_EXPORTER_OTLP_PROTOCOL"
		logsCert   = "OTEL_EXPORTER_OTLP_LOGS_CERTIFICATE"
		proxy      = "https_proxy"
		certFile   = "SSL_CERT_FILE"
		certDir    = "SSL_CERT_DIR"
		fixture    = "SKILL_UP_TEST_HEADER_VALUE"
		kept       = "authorization=fixture"
	)
	tracesDest := envOtelExporterOTLPTracesEndpt
	for _, tt := range []struct {
		name       string
		project    Config
		explicit   Config
		inherited  map[string]string
		header     string
		wantHeader string
		wantSet    bool
	}{
		{name: "ordinary project config", project: Config{Env: map[string]string{"SKILL_UP_TEST_OTHER": "literal"}}, header: generic, wantHeader: "authorization=fixture", wantSet: true},
		{name: "generic endpoint", project: Config{Env: map[string]string{endpoint: "https://project.invalid"}}, header: traces},
		{name: "metrics endpoint", project: Config{Env: map[string]string{metricDest: "https://project.invalid"}}, header: metrics},
		{name: "structured endpoint", project: Config{Telemetry: Telemetry{Traces: TracesConfig{Endpoint: "https://project.invalid"}}}, header: generic},
		{name: "project proxy", project: Config{Env: map[string]string{proxy: "https://project.invalid"}}, header: logs},
		{name: "trusted explicit wins", project: Config{Env: map[string]string{endpoint: "https://project.invalid"}}, explicit: Config{Env: map[string]string{endpoint: "https://trusted.invalid"}}, header: generic, wantHeader: "authorization=fixture", wantSet: true},
		{name: "process overrides project endpoint", project: Config{Env: map[string]string{endpoint: "https://project.invalid"}}, inherited: map[string]string{endpoint: "https://trusted.invalid"}, header: generic, wantHeader: "authorization=fixture", wantSet: true},
		{name: "process header untouched", project: Config{Env: map[string]string{endpoint: "https://project.invalid"}}, inherited: map[string]string{generic: "existing=literal"}, header: generic, wantHeader: "existing=literal", wantSet: true},
		{name: "structured config wins over env", project: Config{Env: map[string]string{envOtelExporterOTLPTracesEndpt: "https://project.invalid"}}, explicit: Config{Telemetry: Telemetry{Traces: TracesConfig{Endpoint: "https://trusted.invalid"}}}, header: generic, wantHeader: "authorization=fixture", wantSet: true},
		{name: "another signal's endpoint keeps a signal header", project: Config{Env: map[string]string{metricDest: "https://project.invalid"}}, header: traces, wantHeader: kept, wantSet: true},
		{name: "another signal's endpoint withholds the shared header", project: Config{Env: map[string]string{metricDest: "https://project.invalid"}}, header: generic},
		{name: "another signal's certificate keeps a signal header", project: Config{Env: map[string]string{logsCert: "project.pem"}}, header: metrics, wantHeader: kept, wantSet: true},
		{name: "structured traces endpoint keeps the metrics header", project: Config{Telemetry: Telemetry{Traces: TracesConfig{Endpoint: "https://project.invalid"}}}, header: metrics, wantHeader: kept, wantSet: true},
		{name: "structured traces endpoint withholds the traces header", project: Config{Telemetry: Telemetry{Traces: TracesConfig{Endpoint: "https://project.invalid"}}}, header: traces},
		{name: "trusted signal endpoint shields its header from the shared project endpoint", project: Config{Env: map[string]string{endpoint: "https://project.invalid"}}, explicit: Config{Env: map[string]string{tracesDest: "https://trusted.invalid"}}, header: traces, wantHeader: kept, wantSet: true},
		{name: "process signal endpoint shields its header from the shared project endpoint", project: Config{Env: map[string]string{endpoint: "https://project.invalid"}}, inherited: map[string]string{tracesDest: "https://trusted.invalid"}, header: traces, wantHeader: kept, wantSet: true},
		{name: "empty process signal endpoint falls back to the shared project endpoint", project: Config{Env: map[string]string{endpoint: "https://project.invalid"}}, inherited: map[string]string{tracesDest: ""}, header: traces},
		{name: "trusted signal endpoint does not cover the shared project protocol", project: Config{Env: map[string]string{protocol: "http/protobuf"}}, explicit: Config{Env: map[string]string{tracesDest: "https://trusted.invalid"}}, header: traces},
		{name: "project certificate file", project: Config{Env: map[string]string{certFile: "project.pem"}}, header: traces},
		{name: "project certificate directory", project: Config{Env: map[string]string{certDir: "project-certs"}}, explicit: Config{Env: map[string]string{endpoint: "https://trusted.invalid"}}, header: generic},
		{name: "process certificate file overrides the project one", project: Config{Env: map[string]string{certFile: "project.pem"}}, inherited: map[string]string{certFile: "trusted.pem"}, header: traces, wantHeader: kept, wantSet: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{
				generic, traces, metrics, logs, endpoint, metricDest, protocol, logsCert, proxy,
				certFile, certDir, tracesDest, "SKILL_UP_TEST_OTHER",
			} {
				t.Setenv(key, "")
				if err := os.Unsetenv(key); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv(fixture, "fixture")
			for key, value := range tt.inherited {
				t.Setenv(key, value)
			}
			var cfg Config
			cfg.overlay(Config{Env: map[string]string{tt.header: "authorization=${" + fixture + "}", "OTEL_EXPORTER_OTLP_COMPRESSION": "gzip"}}, sourceUser)
			cfg.overlay(tt.project, sourceProject)
			cfg.overlay(tt.explicit, sourceExplicit)
			if _, err := ApplyEnv(cfg); err != nil {
				t.Fatal(err)
			}
			if got, set := os.LookupEnv(tt.header); got != tt.wantHeader || set != tt.wantSet {
				t.Errorf("header = %q, set=%t; want %q, set=%t", got, set, tt.wantHeader, tt.wantSet)
			}
		})
	}
}
