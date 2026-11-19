package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
)

func TestUsageOnError_PrintsUsageForParseErrors(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{
		Use:          "test [arg]",
		Args:         usageOnError(cobra.ExactArgs(1)),
		RunE:         func(_ *cobra.Command, _ []string) error { return nil },
		SilenceUsage: true,
	}

	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"a", "b"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected argument validation error")
	}
	if !strings.Contains(output.String(), "Usage:") {
		t.Fatalf("expected usage in command output, got: %s", output.String())
	}
}

func TestExecuteArgsVersionInitializesRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	output, err := captureStdout(t, func() error {
		return ExecuteArgs("test-version", []string{"--version"})
	})
	if err != nil {
		t.Fatalf("ExecuteArgs --version returned error: %v", err)
	}
	if !strings.Contains(output, "test-version") {
		t.Fatalf("version output = %q, want test-version", output)
	}
}

func TestExecuteArgsProjectCannotRedirectTrustedTelemetry(t *testing.T) {
	provider, propagator := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(provider)
		otel.SetTextMapPropagator(propagator)
	})
	var trustedRequests, projectRequests, projectCredentials atomic.Int32
	trusted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "synthetic-token" {
			t.Errorf("trusted collector received incorrect authorization header")
		}
		trustedRequests.Add(1)
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	t.Cleanup(trusted.Close)
	project := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		projectRequests.Add(1)
		if r.Header.Get("Authorization") == "synthetic-token" {
			projectCredentials.Add(1)
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	t.Cleanup(project.Close)
	for _, tc := range []struct {
		name, projectConfig string
	}{
		{"generic endpoint", "env:\n  OTEL_EXPORTER_OTLP_ENDPOINT: %s\n"},
		{"signal endpoint", "env:\n  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: %s\n"},
		{"telemetry endpoint", "telemetry:\n  traces:\n    endpoint: %s\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys := []string{"OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_HEADERS"}
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if strings.HasPrefix(strings.ToUpper(key), "OTEL_") {
					keys = append(keys, key)
				}
			}
			for _, key := range keys {
				t.Setenv(key, "")
				_ = os.Unsetenv(key)
			}
			t.Setenv("OTEL_METRICS_EXPORTER", "none")
			t.Setenv("SKILL_UP_TEST_SECRET", "synthetic-token")
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			wd := t.TempDir()
			t.Chdir(wd)
			userPath := filepath.Join(t.TempDir(), "user.yaml")
			t.Setenv("SKILL_UP_CONFIG", userPath)
			user := fmt.Sprintf(`env:
  OTEL_TRACES_EXPORTER: otlp
  OTEL_EXPORTER_OTLP_ENDPOINT: %s
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_HEADERS: authorization=${SKILL_UP_TEST_SECRET}
`, trusted.URL)
			if err := os.WriteFile(userPath, []byte(user), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(wd, ".skill-up.yaml"), fmt.Appendf(nil, tc.projectConfig, project.URL), 0o600); err != nil {
				t.Fatal(err)
			}
			trustedRequests.Store(0)
			projectRequests.Store(0)
			projectCredentials.Store(0)
			if _, err := captureStdout(t, func() error {
				return ExecuteArgs("test-version", []string{"--version"})
			}); err != nil {
				t.Fatal(err)
			}
			if got := projectRequests.Load(); got != 0 {
				t.Errorf("untrusted project collector received %d requests, %d with trusted credentials", got, projectCredentials.Load())
			}
			if got := trustedRequests.Load(); got == 0 {
				t.Error("trusted collector received no requests")
			}
		})
	}
}

func TestExecuteArgsProjectEnvTrust(t *testing.T) {
	configFlag := rootCmd.PersistentFlags().Lookup(configFlagName)
	configValue, configChanged := configFlag.Value.String(), configFlag.Changed
	printFlag := initCmd.Flags().Lookup("print")
	printValue, printChanged := printFlag.Value.String(), printFlag.Changed
	t.Cleanup(func() {
		if err := configFlag.Value.Set(configValue); err != nil {
			t.Error(err)
		}
		configFlag.Changed = configChanged
		if err := printFlag.Value.Set(printValue); err != nil {
			t.Error(err)
		}
		printFlag.Changed = printChanged
	})
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SKILL_UP_CONFIG", "")
	t.Setenv("SKILL_UP_TEST_SECRET", "test-token")
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	wd := t.TempDir()
	t.Chdir(wd)
	project := filepath.Join(wd, ".skill-up.yaml")
	if err := os.WriteFile(project, []byte(`env:
  OTEL_EXPORTER_OTLP_ENDPOINT: https://collector.example.invalid
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_RESOURCE_ATTRIBUTES: leaked=${SKILL_UP_TEST_SECRET}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		args       []string
		want       string
		wantOutput string
	}{
		{"automatic", []string{"--version"}, "", "test-version"},
		{"explicit", []string{"--version", "--config", project}, "leaked=test-token", "test-version"},
		{"explicit equals", []string{"--version", "--config=" + project}, "leaked=test-token", "test-version"},
		{"init source", []string{"init", "--config", project, "--print"}, "", "leaked=${SKILL_UP_TEST_SECRET}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_RESOURCE_ATTRIBUTES"} {
				t.Setenv(key, "")
				_ = os.Unsetenv(key)
			}
			output, err := captureStdout(t, func() error {
				return ExecuteArgs("test-version", tc.args)
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output, tc.wantOutput) {
				t.Fatalf("command output = %q, want %q", output, tc.wantOutput)
			}
			if got := os.Getenv("OTEL_RESOURCE_ATTRIBUTES"); got != tc.want {
				t.Fatalf("resource attributes = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsInitInvocation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"empty", nil, false},
		{"bare init", []string{"init"}, true},
		{"with --config space", []string{"--config", "/tmp/c.yaml", "init"}, true},
		{"with --config=", []string{"--config=/tmp/c.yaml", "init"}, true},
		{"end-of-flags before init still finds", []string{"--config", "/tmp/c.yaml", "--", "init"}, false},
		{"run command", []string{"run", "/tmp/eval.yaml"}, false},
		{"flag-only", []string{"--help"}, false},
		{"init after non-value flag", []string{"--quiet", "init"}, true},
		{"unknown -X flag followed by init", []string{"-X", "init"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isInitInvocation(tc.args); got != tc.want {
				t.Errorf("isInitInvocation(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestExtractConfigFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"absent", []string{"run", "x"}, ""},
		{"space form", []string{"--config", "/a.yaml", "run"}, "/a.yaml"},
		{"equals form", []string{"--config=/a.yaml", "run"}, "/a.yaml"},
		{"empty equals", []string{"--config=", "run"}, ""},
		{"trailing --config has no value", []string{"run", "--config"}, ""},
		{"after -- terminator is positional", []string{"--", "--config", "/a.yaml"}, ""},
		{"first wins", []string{"--config", "/first.yaml", "--config", "/second.yaml"}, "/first.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractConfigFlag(tc.args); got != tc.want {
				t.Errorf("extractConfigFlag(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}
