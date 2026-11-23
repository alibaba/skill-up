package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConfigFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "absent", args: []string{"run", "eval.yaml"}},
		{name: "space before command", args: []string{"--config", "trusted.yaml", "run", "eval.yaml"}, want: "trusted.yaml"},
		{name: "equals after command", args: []string{"run", "eval.yaml", "--config=trusted.yaml"}, want: "trusted.yaml"},
		{name: "literal after separator", args: []string{"run", "--", "--config=literal.yaml"}},
		{name: "second literal after separator", args: []string{"run", "--config=trusted.yaml", "--", "--config=literal.yaml"}, want: "trusted.yaml"},
		{name: "another flag consumes lookalike", args: []string{"run", "--api-key", "--config=literal.yaml", "--config", "trusted.yaml"}, want: "trusted.yaml"},
		{name: "delimiter consumed by a flag", args: []string{"run", "--api-key", "--", "--config=trusted.yaml"}, want: "trusted.yaml"},
		{name: "short option and normalizer", args: []string{"run", "-vv", "--rk", "name=value", "--config=trusted.yaml"}, want: "trusted.yaml"},
		{name: "source named init", args: []string{"--config", "init", "run"}, want: "init"},
		{name: "completion flags are invocation data", args: []string{"__complete", "run", "--config=proposed.yaml", ""}},
		{name: "init copy source before command", args: []string{"--config", "source.yaml", "init", "--print"}},
		{name: "init copy source after command", args: []string{"init", "--config=source.yaml", "--print"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveConfigFlag(tc.args)
			if err != nil || got != tc.want {
				t.Fatalf("resolveConfigFlag(%q) = %q, %v; want %q", tc.args, got, err, tc.want)
			}
		})
	}
}

func TestResolveConfigFlagRejectsRepeated(t *testing.T) {
	for _, args := range [][]string{
		{"run", "--config=one.yaml", "--config", "two.yaml"},
		{"--config", "one.yaml", "run", "--config=two.yaml"},
		{"--config=", "run", "--config=two.yaml"},
		{"init", "--print", "--config=one.yaml", "--config=two.yaml"},
	} {
		got, err := resolveConfigFlag(args)
		if err == nil || !strings.Contains(err.Error(), "--config may only be specified once") || got != "" {
			t.Errorf("resolveConfigFlag(%q) = %q, %v; want duplicate error", args, got, err)
		}
	}
}

func TestExecuteArgsRejectsRepeatedConfigBeforeApplyingDefaults(t *testing.T) {
	const marker = "SKILL_UP_TEST_CONFIG_PREFLIGHT"
	t.Setenv(marker, "")
	if err := os.Unsetenv(marker); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	user := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(user, []byte("env:\n  "+marker+": user-default\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SKILL_UP_CONFIG", user)
	err := ExecuteArgs("test-version", []string{"--config", user, "--config=" + user, "--version"})
	if err == nil || !strings.Contains(err.Error(), "--config may only be specified once") {
		t.Fatalf("ExecuteArgs = %v, want duplicate error", err)
	}
	if _, present := os.LookupEnv(marker); present {
		t.Fatalf("duplicate flag must be rejected before applying environment defaults")
	}
}
