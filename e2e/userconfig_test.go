//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_InitPrint(t *testing.T) {
	t.Parallel()
	Cover(t, "skill-up init --print")

	result := RunSimple(t, "init", "--print")
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", result.ExitCode, result.Stderr)
	}
	for _, want := range []string{
		"schema_version: v1alpha1",
		"kind: SkillUpConfig",
		"telemetry:",
		"# runtime_kwargs:",
	} {
		if !strings.Contains(result.Stdout, want) {
			t.Errorf("missing %q in stdout: %s", want, result.Stdout)
		}
	}
}

func TestCLI_InitWriteAndForce(t *testing.T) {
	t.Parallel()
	Cover(t, "skill-up init --local write + --force overwrite")

	tmp := t.TempDir()
	target := filepath.Join(tmp, ".skill-up.yaml")

	r1 := Run(t, RunConfig{WorkDir: tmp}, "init", "--local")
	if r1.ExitCode != 0 {
		t.Fatalf("first write failed: %s", r1.Stderr)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target not created: %v", err)
	}

	r2 := Run(t, RunConfig{WorkDir: tmp}, "init", "--local")
	if r2.ExitCode == 0 {
		t.Fatalf("second write without --force should fail; stdout: %s", r2.Stdout)
	}

	r3 := Run(t, RunConfig{WorkDir: tmp}, "init", "--local", "--force")
	if r3.ExitCode != 0 {
		t.Fatalf("force write failed: %s", r3.Stderr)
	}
}

func TestCLI_InitConfigSourceCopied(t *testing.T) {
	t.Parallel()
	Cover(t, "skill-up init --config <source> --local")

	tmp := t.TempDir()
	source := filepath.Join(tmp, "source.yaml")
	sourceContent := "# my custom header\nschema_version: v1alpha1\nkind: SkillUpConfig\ntelemetry:\n  service_name: from-source\n"
	if err := os.WriteFile(source, []byte(sourceContent), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	r := Run(t, RunConfig{WorkDir: tmp}, "init", "--config", source, "--local")
	if r.ExitCode != 0 {
		t.Fatalf("init --config <source> --local failed: %s", r.Stderr)
	}

	got, err := os.ReadFile(filepath.Join(tmp, ".skill-up.yaml"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	// The source bytes must round-trip verbatim — comments and all.
	if string(got) != sourceContent {
		t.Errorf("target content mismatch\nwant:\n%s\ngot:\n%s", sourceContent, got)
	}
}

func TestCLI_InitLocal(t *testing.T) {
	t.Parallel()
	Cover(t, "skill-up init --local")

	tmp := t.TempDir()
	result := Run(t, RunConfig{WorkDir: tmp}, "init", "--local")
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Stderr)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".skill-up.yaml")); err != nil {
		t.Fatalf("project file not created: %v", err)
	}
}

func TestCLI_ConfigNonexistent(t *testing.T) {
	t.Parallel()
	Cover(t, "skill-up --config nonexistent path")

	tmp := t.TempDir()
	bogus := filepath.Join(tmp, "missing.yaml")
	result := Run(t, RunConfig{WorkDir: tmp}, "--config", bogus, "validate")
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for missing config")
	}
	if !strings.Contains(result.Stderr, bogus) && !strings.Contains(result.Stdout, bogus) {
		t.Errorf("expected error to mention %q; stdout=%s stderr=%s", bogus, result.Stdout, result.Stderr)
	}
}

func TestCLI_UserConfigRoundTrip(t *testing.T) {
	t.Parallel()
	Cover(t, "skill-up --config round-trip")

	tmp := t.TempDir()

	// Write a template into a temp workdir via --local, then re-load that
	// file via --config when running validate against a real eval.yaml.
	if r := Run(t, RunConfig{WorkDir: tmp}, "init", "--local"); r.ExitCode != 0 {
		t.Fatalf("init --local: %s", r.Stderr)
	}
	cfgPath := filepath.Join(tmp, ".skill-up.yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("expected %s to exist: %v", cfgPath, err)
	}

	examplesDir := getExamplesDir()
	evalPath := filepath.Join(examplesDir, "eval.yaml")
	r := RunSimple(t, "--config", cfgPath, "validate", evalPath)
	if r.ExitCode != 0 {
		t.Fatalf("validate with --config failed: stdout=%s stderr=%s", r.Stdout, r.Stderr)
	}
}
