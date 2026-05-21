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
		"kind: SkillUpConfig",
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

func TestInit_LocalWritesTemplate(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	cmd := newInitCmdForTest()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"--local"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --local: %v", err)
	}
	target := filepath.Join(tmp, ".skill-up.yaml")
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != initTemplateContent {
		t.Errorf("expected template content, got:\n%s", got)
	}

	// Second write without --force must fail.
	cmd2 := newInitCmdForTest()
	cmd2.SetOut(&bytes.Buffer{})
	cmd2.SetErr(&bytes.Buffer{})
	cmd2.SilenceUsage = true
	cmd2.SilenceErrors = true
	cmd2.SetArgs([]string{"--local"})
	if err := cmd2.Execute(); err == nil {
		t.Fatal("expected error on second write without --force")
	}

	// With --force, succeeds.
	cmd3 := newInitCmdForTest()
	cmd3.SetOut(&bytes.Buffer{})
	cmd3.SetArgs([]string{"--local", "--force"})
	if err := cmd3.Execute(); err != nil {
		t.Fatalf("force write: %v", err)
	}
}

func TestInit_ConfigSourceCopiedToLocal(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	source := filepath.Join(tmp, "source.yaml")
	sourceContent := "# my custom comment\nschema_version: v1alpha1\nkind: SkillUpConfig\ntelemetry:\n  service_name: custom\n"
	if err := os.WriteFile(source, []byte(sourceContent), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	cmd := newInitCmdForTest()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"--config", source, "--local"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	target := filepath.Join(tmp, ".skill-up.yaml")
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	// Source bytes must be preserved verbatim (comments and all).
	if string(got) != sourceContent {
		t.Errorf("target content mismatch\nwant:\n%s\ngot:\n%s", sourceContent, got)
	}
}

func TestInit_ConfigSourcePrint(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	source := filepath.Join(tmp, "source.yaml")
	sourceContent := "schema_version: v1alpha1\nkind: SkillUpConfig\n# kept comment\n"
	if err := os.WriteFile(source, []byte(sourceContent), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	cmd := newInitCmdForTest()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", source, "--print"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --print: %v", err)
	}
	if out.String() != sourceContent {
		t.Errorf("print output mismatch\nwant:\n%s\ngot:\n%s", sourceContent, out.String())
	}
}

func TestInit_ConfigSourceMissing(t *testing.T) {
	t.Parallel()
	cmd := newInitCmdForTest()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--config", "/definitely/does/not/exist.yaml", "--print"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for missing source")
	}
}

func TestInit_ConfigSourceInvalid(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	source := filepath.Join(tmp, "bad.yaml")
	if err := os.WriteFile(source, []byte("not: [valid yaml"), 0o600); err != nil {
		t.Fatalf("write bad source: %v", err)
	}
	cmd := newInitCmdForTest()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--config", source, "--print"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for invalid source")
	}
}
