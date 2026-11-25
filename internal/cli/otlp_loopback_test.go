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
)

const (
	otlpLoopbackToken    = "synthetic-token"
	otlpLoopbackTokenEnv = "SKILL_UP_TEST_OTLP_TOKEN"
	otlpTracesPath       = "/v1/traces"
	otlpMetricsPath      = "/v1/metrics"
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

	const (
		toTrusted = "trusted"
		toProject = "project"
	)
	type export struct {
		collector     string
		path          string
		authorization string
	}
	for _, tc := range []struct {
		name    string
		user    string
		project string
		// want lists every export path that must arrive, with the header
		// value each request on that path must carry. Any other export
		// fails the test.
		want []export
	}{
		{
			name: "project traces endpoint gets no expanded header",
			user: `env:
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_HEADERS: authorization=${SKILL_UP_TEST_OTLP_TOKEN}
`,
			project: `telemetry:
  traces:
    endpoint: PROJECT/v1/traces
`,
			want: []export{{toProject, otlpTracesPath, ""}},
		},
		{
			name: "trusted endpoint keeps the header beside ordinary project defaults",
			user: `env:
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_ENDPOINT: TRUSTED
  OTEL_EXPORTER_OTLP_HEADERS: authorization=${SKILL_UP_TEST_OTLP_TOKEN}
`,
			project: `env:
  SKILL_UP_TEST_PROJECT_DEFAULT: project
`,
			want: []export{{toTrusted, otlpTracesPath, otlpLoopbackToken}},
		},
		{
			name: "project metrics endpoint with metrics off keeps the traces header",
			user: `env:
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: TRUSTED/v1/traces
  OTEL_EXPORTER_OTLP_TRACES_HEADERS: authorization=${SKILL_UP_TEST_OTLP_TOKEN}
`,
			project: `env:
  OTEL_EXPORTER_OTLP_METRICS_ENDPOINT: PROJECT/v1/metrics
`,
			want: []export{{toTrusted, otlpTracesPath, otlpLoopbackToken}},
		},
		{
			name: "mixed signals send the header to the trusted route only",
			user: `env:
  OTEL_METRICS_EXPORTER: otlp
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: TRUSTED/v1/traces
  OTEL_EXPORTER_OTLP_TRACES_HEADERS: authorization=${SKILL_UP_TEST_OTLP_TOKEN}
  OTEL_EXPORTER_OTLP_METRICS_HEADERS: authorization=${SKILL_UP_TEST_OTLP_TOKEN}
`,
			project: `env:
  OTEL_EXPORTER_OTLP_METRICS_ENDPOINT: PROJECT/v1/metrics
`,
			want: []export{
				{toTrusted, otlpTracesPath, otlpLoopbackToken},
				{toProject, otlpMetricsPath, ""},
			},
		},
		{
			name: "project TLS roots withhold the header on a trusted endpoint",
			user: `env:
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: TRUSTED/v1/traces
  OTEL_EXPORTER_OTLP_TRACES_HEADERS: authorization=${SKILL_UP_TEST_OTLP_TOKEN}
`,
			project: `env:
  SSL_CERT_FILE: project-roots.pem
`,
			want: []export{{toTrusted, otlpTracesPath, ""}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			collectors := map[string]*otlpCollector{
				toTrusted: newOTLPCollector(t),
				toProject: newOTLPCollector(t),
			}
			placeholders := strings.NewReplacer(
				"TRUSTED", collectors[toTrusted].URL,
				"PROJECT", collectors[toProject].URL,
			)

			for _, key := range []string{
				"OTEL_TRACES_EXPORTER", "OTEL_METRICS_EXPORTER",
				"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_HEADERS",
				"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "OTEL_EXPORTER_OTLP_TRACES_HEADERS",
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "OTEL_EXPORTER_OTLP_METRICS_HEADERS",
				"SSL_CERT_FILE", "SSL_CERT_DIR", "SKILL_UP_TEST_PROJECT_DEFAULT",
			} {
				t.Setenv(key, "")
				if err := os.Unsetenv(key); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv(otlpLoopbackTokenEnv, otlpLoopbackToken)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			userPath := filepath.Join(t.TempDir(), "user.yaml")
			t.Setenv("SKILL_UP_CONFIG", userPath)
			t.Chdir(t.TempDir())
			if err := os.WriteFile(userPath, []byte(placeholders.Replace(tc.user)), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(".skill-up.yaml", []byte(placeholders.Replace(tc.project)), 0o600); err != nil {
				t.Fatal(err)
			}

			// ExecuteArgs flushes both exporters before it returns.
			if _, err := captureStdout(t, func() error {
				return ExecuteArgs("test-version", []string{"--version"})
			}); err != nil {
				t.Fatal(err)
			}

			expected := map[string]map[string]string{toTrusted: {}, toProject: {}}
			for _, want := range tc.want {
				expected[want.collector][want.path] = want.authorization
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
		})
	}

	// Leave the cached exporter settings disabled for the tests that follow.
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	if _, _, err := observability.Init(context.Background(), "test-version"); err != nil {
		t.Fatal(err)
	}
}
