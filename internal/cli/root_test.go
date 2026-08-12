package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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

func TestRootCommandIncludesCompare(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"compare"})
	if err != nil {
		t.Fatalf("find compare command: %v", err)
	}
	if command != compareCmd {
		t.Fatalf("compare command = %p, want registered compare command %p", command, compareCmd)
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
