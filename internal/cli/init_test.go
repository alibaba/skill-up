package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newInitCmdForTest() *cobra.Command {
	cmd := &cobra.Command{Use: "init", RunE: runInit}
	cmd.Flags().Bool("local", false, "")
	cmd.Flags().Bool("print", false, "")
	cmd.Flags().Bool("force", false, "")
	cmd.Flags().String(configFlagName, "", "")
	return cmd
}

func TestInit_TemplateContainsAllSections(t *testing.T) {
	t.Parallel()
	want := []string{
		"schema_version: v1alpha1",
		"kind: SkillEvalConfig",
		"telemetry:",
		"traces:",
		"# resource_attributes:",
		"# env:",
		"# runtime_kwargs:",
	}
	for _, w := range want {
		if !strings.Contains(initTemplateContent, w) {
			t.Errorf("template missing %q", w)
		}
	}
}

func TestInit_Print(t *testing.T) {
	t.Parallel()
	cmd := newInitCmdForTest()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--print"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "schema_version: v1alpha1") {
		t.Errorf("print output missing schema_version: %q", out.String())
	}
}

func TestInit_WriteAndForce(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	target := filepath.Join(tmp, "config.yaml")

	cmd := newInitCmdForTest()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target not created: %v", err)
	}

	cmd2 := newInitCmdForTest()
	cmd2.SetArgs([]string{"--config", target})
	cmd2.SetOut(&bytes.Buffer{})
	cmd2.SetErr(&bytes.Buffer{})
	cmd2.SilenceUsage = true
	cmd2.SilenceErrors = true
	if err := cmd2.Execute(); err == nil {
		t.Fatal("expected error on second write without --force")
	}

	cmd3 := newInitCmdForTest()
	cmd3.SetArgs([]string{"--config", target, "--force"})
	cmd3.SetOut(&bytes.Buffer{})
	if err := cmd3.Execute(); err != nil {
		t.Fatalf("force write: %v", err)
	}
}

func TestInit_LocalConflictsWithConfig(t *testing.T) {
	t.Parallel()
	cmd := newInitCmdForTest()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--local", "--config", "/tmp/x.yaml"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected mutual exclusion error")
	}
}
