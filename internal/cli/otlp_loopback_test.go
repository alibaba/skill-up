package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"

	"github.com/alibaba/skill-up/internal/observability"
	"github.com/alibaba/skill-up/internal/userconfig"
)

const (
	otlpLoopbackHeader    = "synthetic-value"
	otlpLoopbackHeaderEnv = "SKILL_UP_TEST_OTLP_HEADER"
	otlpLoopbackEmptyEnv  = "SKILL_UP_TEST_OTLP_EMPTY"
	otlpTracesPath        = "/v1/traces"
	otlpMetricsPath       = "/v1/metrics"
	otlpTrustedCollector  = "trusted"
	otlpProjectCollector  = "project"
)

// otlpCollector is a loopback OTLP/HTTP endpoint that records the path and
// Authorization header of every export it receives.
type otlpCollector struct {
	*httptest.Server

	mu             sync.Mutex
	authorizations map[string][]string
}

func newOTLPCollector(t *testing.T) *otlpCollector {
	t.Helper()
	c := &otlpCollector{authorizations: map[string][]string{}}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Marker") != "" {
			t.Error("collector received a withheld test marker")
		}
		c.mu.Lock()
		c.authorizations[r.URL.Path] = append(c.authorizations[r.URL.Path], r.Header.Get("Authorization"))
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *otlpCollector) received() map[string][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string][]string, len(c.authorizations))
	for path, values := range c.authorizations {
		out[path] = append([]string(nil), values...)
	}
	return out
}

// otlpExport names an export path that must arrive at a collector, with the
// Authorization value every request on that path must carry.
type otlpExport struct {
	collector     string
	path          string
	authorization string
}

type otlpLoopbackCase struct {
	name               string
	user               string
	project            string
	verifyBeforeExport bool
	// want lists every expected export. Any other export fails the test.
	want []otlpExport
}

// otlpLoopbackCases name the trusted collector in the user config and the
// other one in an automatically discovered project config.
var otlpLoopbackCases = []otlpLoopbackCase{
	{
		name: "rejected traces endpoint uses shared route without expanded header",
		user: `env:
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: "https://[invalid"
  OTEL_EXPORTER_OTLP_TRACES_HEADERS: x-test-marker=${SKILL_UP_TEST_OTLP_HEADER}
`,
		project: `env:
  OTEL_EXPORTER_OTLP_ENDPOINT: {{project}}
`,
		verifyBeforeExport: true,
		want:               []otlpExport{{otlpProjectCollector, otlpTracesPath, ""}},
	},
	{
		name: "project traces endpoint gets no expanded header",
		user: `env:
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_HEADERS: authorization=${SKILL_UP_TEST_OTLP_HEADER}
`,
		project: `telemetry:
  traces:
    endpoint: {{project}}/v1/traces
`,
		want: []otlpExport{{otlpProjectCollector, otlpTracesPath, ""}},
	},
	{
		name: "trusted endpoint keeps the header beside ordinary project defaults",
		user: `env:
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_ENDPOINT: {{trusted}}
  OTEL_EXPORTER_OTLP_HEADERS: authorization=${SKILL_UP_TEST_OTLP_HEADER}
`,
		project: `env:
  SKILL_UP_TEST_PROJECT_DEFAULT: project
`,
		want: []otlpExport{{otlpTrustedCollector, otlpTracesPath, otlpLoopbackHeader}},
	},
	{
		name: "project metrics endpoint with metrics off keeps the traces header",
		user: `env:
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: {{trusted}}/v1/traces
  OTEL_EXPORTER_OTLP_TRACES_HEADERS: authorization=${SKILL_UP_TEST_OTLP_HEADER}
`,
		project: `env:
  OTEL_EXPORTER_OTLP_METRICS_ENDPOINT: {{project}}/v1/metrics
`,
		want: []otlpExport{{otlpTrustedCollector, otlpTracesPath, otlpLoopbackHeader}},
	},
	{
		name: "mixed signals send the header to the trusted route only",
		user: `env:
  OTEL_METRICS_EXPORTER: otlp
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: {{trusted}}/v1/traces
  OTEL_EXPORTER_OTLP_TRACES_HEADERS: authorization=${SKILL_UP_TEST_OTLP_HEADER}
  OTEL_EXPORTER_OTLP_METRICS_HEADERS: authorization=${SKILL_UP_TEST_OTLP_HEADER}
`,
		project: `env:
  OTEL_EXPORTER_OTLP_METRICS_ENDPOINT: {{project}}/v1/metrics
`,
		want: []otlpExport{
			{otlpTrustedCollector, otlpTracesPath, otlpLoopbackHeader},
			{otlpProjectCollector, otlpMetricsPath, ""},
		},
	},
	{
		name: "traces endpoint that expands to nothing falls back to the project endpoint",
		user: `env:
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: ${SKILL_UP_TEST_OTLP_EMPTY}
  OTEL_EXPORTER_OTLP_TRACES_HEADERS: authorization=${SKILL_UP_TEST_OTLP_HEADER}
`,
		project: `env:
  OTEL_EXPORTER_OTLP_ENDPOINT: {{project}}
`,
		want: []otlpExport{{otlpProjectCollector, otlpTracesPath, ""}},
	},
	{
		name: "blank project traces endpoint that replaces the trusted one withholds the header",
		user: `env:
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_ENDPOINT: {{trusted}}
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: {{trusted}}/custom/traces
  OTEL_EXPORTER_OTLP_TRACES_HEADERS: authorization=${SKILL_UP_TEST_OTLP_HEADER}
`,
		project: `env:
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: " "
`,
		want: []otlpExport{{otlpTrustedCollector, otlpTracesPath, ""}},
	},
	{
		name: "empty project traces endpoint that replaces the trusted one withholds the header",
		user: `env:
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_ENDPOINT: {{trusted}}
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: {{trusted}}/custom/traces
  OTEL_EXPORTER_OTLP_TRACES_HEADERS: authorization=${SKILL_UP_TEST_OTLP_HEADER}
`,
		project: `env:
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: ""
`,
		want: []otlpExport{{otlpTrustedCollector, otlpTracesPath, ""}},
	},
	{
		name: "project TLS roots withhold the header on a trusted endpoint",
		user: `env:
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: {{trusted}}/v1/traces
  OTEL_EXPORTER_OTLP_TRACES_HEADERS: authorization=${SKILL_UP_TEST_OTLP_HEADER}
`,
		project: `env:
  SSL_CERT_FILE: project-roots.pem
`,
		want: []otlpExport{{otlpTrustedCollector, otlpTracesPath, ""}},
	},
}

// TestExecuteArgsExpandedOTLPHeaderReachesOnlyTrustedRoutes runs the real
// start-up path (config discovery, env defaults, exporter initialization and
// shutdown flush) against two loopback collectors. The trusted collector is
// named by the user config; the other by an automatically discovered project
// config.
func TestExecuteArgsExpandedOTLPHeaderReachesOnlyTrustedRoutes(t *testing.T) {
	previousTracer, previousMeter := otel.GetTracerProvider(), otel.GetMeterProvider()
	t.Cleanup(func() {
		otel.SetTracerProvider(previousTracer)
		otel.SetMeterProvider(previousMeter)
	})

	for _, tc := range otlpLoopbackCases {
		t.Run(tc.name, func(t *testing.T) {
			collectors := map[string]*otlpCollector{
				otlpTrustedCollector: newOTLPCollector(t),
				otlpProjectCollector: newOTLPCollector(t),
			}
			runOTLPLoopbackCase(t, tc, collectors)
			checkOTLPExports(t, tc.want, collectors)
		})
	}

	// Leave the cached exporter settings disabled for the tests that follow.
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	if _, _, err := observability.Init(context.Background(), "test-version"); err != nil {
		t.Fatal(err)
	}
}

// runOTLPLoopbackCase writes the two config files and runs the CLI start-up
// path once. ExecuteArgs flushes both exporters before it returns.
func runOTLPLoopbackCase(t *testing.T, tc otlpLoopbackCase, collectors map[string]*otlpCollector) {
	t.Helper()
	// Telemetry settings inherited from the developer's shell must not
	// decide what the collectors see.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "OTEL_") {
			unsetForTest(t, key)
		}
	}
	for _, key := range []string{
		"OTEL_METRICS_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_HEADERS",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_HEADERS",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "SKILL_UP_TEST_PROJECT_DEFAULT",
	} {
		unsetForTest(t, key)
	}
	t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
	t.Setenv(otlpLoopbackHeaderEnv, otlpLoopbackHeader)
	t.Setenv(otlpLoopbackEmptyEnv, "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	userPath := filepath.Join(t.TempDir(), "user.yaml")
	t.Setenv("SKILL_UP_CONFIG", userPath)
	t.Chdir(t.TempDir())

	placeholders := strings.NewReplacer(
		"{{trusted}}", collectors[otlpTrustedCollector].URL,
		"{{project}}", collectors[otlpProjectCollector].URL,
	)
	if err := os.WriteFile(userPath, []byte(placeholders.Replace(tc.user)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".skill-up.yaml", []byte(placeholders.Replace(tc.project)), 0o600); err != nil {
		t.Fatal(err)
	}
	if tc.verifyBeforeExport {
		exportAfterHeaderCheck(t)
		return
	}
	if _, err := captureStdout(t, func() error {
		return ExecuteArgs("test-version", []string{"--version"})
	}); err != nil {
		t.Fatal(err)
	}
}

func exportAfterHeaderCheck(t *testing.T) {
	t.Helper()
	cfg, _, err := loadUserConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := userconfig.ApplyEnv(cfg); err != nil {
		t.Fatal(err)
	}
	if _, present := os.LookupEnv("OTEL_EXPORTER_OTLP_TRACES_HEADERS"); present {
		t.Fatal("expanded trace headers present before exporter initialization")
	}
	ctx, shutdown, err := observability.Init(context.Background(), "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

// unsetForTest removes key for the test and restores it afterwards.
func unsetForTest(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

func checkOTLPExports(t *testing.T, want []otlpExport, collectors map[string]*otlpCollector) {
	t.Helper()
	expected := map[string]map[string]string{}
	for name := range collectors {
		expected[name] = map[string]string{}
	}
	for _, export := range want {
		expected[export.collector][export.path] = export.authorization
	}
	for name, collector := range collectors {
		received := collector.received()
		for path, wantAuthorization := range expected[name] {
			if len(received[path]) == 0 {
				t.Errorf("%s collector received no export on %s", name, path)
			}
			for _, got := range received[path] {
				if got != wantAuthorization {
					t.Errorf("%s collector %s Authorization = %q, want %q", name, path, got, wantAuthorization)
				}
			}
		}
		for path := range received {
			if _, ok := expected[name][path]; !ok {
				t.Errorf("%s collector received an unexpected export on %s", name, path)
			}
		}
	}
}
