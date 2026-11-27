package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/userconfig"
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

func TestLoadUserConfigPreservesProjectDefaultsWithoutApplyingExpandedOTLPHeaders(t *testing.T) {
	t.Setenv("SKILL_UP_TEST_HEADER_VALUE", "synthetic-value")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	userPath := filepath.Join(t.TempDir(), "user.yaml")
	t.Setenv("SKILL_UP_CONFIG", userPath)
	if err := os.WriteFile(userPath, []byte(`env:
  OTEL_EXPORTER_OTLP_HEADERS: authorization=${SKILL_UP_TEST_HEADER_VALUE}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".skill-up.yaml", []byte(`telemetry:
  traces:
    endpoint: https://project.example.invalid
env:
  SKILL_UP_TEST_PROJECT_DEFAULT: project
`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "SKILL_UP_TEST_PROJECT_DEFAULT"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	cfg, _, err := loadUserConfig([]string{"--version"})
	if err != nil {
		t.Fatal(err)
	}
	// Inspect defaults before SDK initialization. No collector or transport needed.
	if _, err := userconfig.ApplyEnv(cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := os.LookupEnv("OTEL_EXPORTER_OTLP_HEADERS"); ok {
		t.Error("a newly expanded header default was applied with project routing")
	}
	if got := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"); got != "https://project.example.invalid" {
		t.Errorf("project route default = %q", got)
	}
	if got := os.Getenv("SKILL_UP_TEST_PROJECT_DEFAULT"); got != "project" {
		t.Errorf("ordinary project default = %q", got)
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
	const projectContents = `env:
  OTEL_EXPORTER_OTLP_ENDPOINT: https://collector.example.invalid
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_RESOURCE_ATTRIBUTES: source=${SKILL_UP_TEST_SECRET}
`
	if err := os.WriteFile(project, []byte(projectContents), 0o600); err != nil {
		t.Fatal(err)
	}
	copySource := filepath.Join(t.TempDir(), "copy.yaml")
	if err := os.WriteFile(copySource, []byte(strings.Replace(projectContents, "source=", "copy-only=", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		args       []string
		want       string
		wantOutput string
	}{
		{"automatic", []string{"--version"}, "source=${SKILL_UP_TEST_SECRET}", "test-version"},
		{"explicit", []string{"--version", "--config", project}, "source=test-token", "test-version"},
		{"explicit equals", []string{"--version", "--config=" + project}, "source=test-token", "test-version"},
		{"init source", []string{"init", "--config", copySource, "--print"}, "source=${SKILL_UP_TEST_SECRET}", "copy-only=${SKILL_UP_TEST_SECRET}"},
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
