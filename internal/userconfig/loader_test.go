package userconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func emptyEnv(string) string { return "" }

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEffective_AllEmpty(t *testing.T) {
	tmp := t.TempDir()
	cfg, sources, err := LoadEffective(LoadOptions{
		WorkingDir: tmp,
		HomeDir:    tmp,
		Env:        emptyEnv,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Kind != "embed" {
		t.Fatalf("expected only embed source, got %+v", sources)
	}
	if cfg.Telemetry.ServiceName != "" {
		t.Fatalf("embed should be empty, got %q", cfg.Telemetry.ServiceName)
	}
}

func TestLoadEffective_UserLayer(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	userPath := filepath.Join(home, ".config", UserConfigDir, UserConfigFile)
	writeFile(t, userPath, "telemetry:\n  service_name: from-user\n")

	cfg, sources, err := LoadEffective(LoadOptions{HomeDir: home, WorkingDir: wd, Env: emptyEnv})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.ServiceName != "from-user" {
		t.Fatalf("expected from-user, got %q", cfg.Telemetry.ServiceName)
	}
	if len(sources) != 2 || sources[1].Kind != "user" {
		t.Fatalf("expected user source, got %+v", sources)
	}
}

func TestLoadEffective_ProjectOverridesUser(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	writeFile(t, filepath.Join(home, ".config", UserConfigDir, UserConfigFile), "telemetry:\n  service_name: user\n")
	writeFile(t, filepath.Join(wd, ProjectConfigFile), "telemetry:\n  service_name: project\n")

	cfg, _, err := LoadEffective(LoadOptions{HomeDir: home, WorkingDir: wd, Env: emptyEnv})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.ServiceName != "project" {
		t.Fatalf("project should win: %q", cfg.Telemetry.ServiceName)
	}
}

func TestLoadEffective_ExplicitMissing(t *testing.T) {
	tmp := t.TempDir()
	_, _, err := LoadEffective(LoadOptions{
		ExplicitPath: filepath.Join(tmp, "nope.yaml"),
		HomeDir:      tmp,
		WorkingDir:   tmp,
		Env:          emptyEnv,
	})
	if err == nil {
		t.Fatal("expected error for missing explicit path")
	}
}

func TestLoadEffective_ExplicitWins(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	writeFile(t, filepath.Join(home, ".config", UserConfigDir, UserConfigFile), "telemetry:\n  service_name: user\n")
	writeFile(t, filepath.Join(wd, ProjectConfigFile), "telemetry:\n  service_name: project\n")
	explicit := filepath.Join(t.TempDir(), "x.yaml")
	writeFile(t, explicit, "telemetry:\n  service_name: explicit\n")

	cfg, sources, err := LoadEffective(LoadOptions{ExplicitPath: explicit, HomeDir: home, WorkingDir: wd, Env: emptyEnv})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.ServiceName != "explicit" {
		t.Fatalf("explicit should win: %q", cfg.Telemetry.ServiceName)
	}
	last := sources[len(sources)-1]
	if last.Kind != "explicit" || last.Path != explicit {
		t.Fatalf("explicit not last source: %+v", last)
	}
}

func TestLoadEffective_EnvVarPath(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "x.yaml")
	writeFile(t, cfgPath, "telemetry:\n  service_name: env\n")

	env := func(k string) string {
		if k == ConfigEnvVar {
			return cfgPath
		}
		return ""
	}
	cfg, _, err := LoadEffective(LoadOptions{HomeDir: tmp, WorkingDir: tmp, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.ServiceName != "env" {
		t.Fatalf("got %q", cfg.Telemetry.ServiceName)
	}
}

func TestLoadEffective_CorruptFile(t *testing.T) {
	tmp := t.TempDir()
	bad := filepath.Join(tmp, "bad.yaml")
	writeFile(t, bad, "telemetry: : :\n")
	_, _, err := LoadEffective(LoadOptions{ExplicitPath: bad, HomeDir: tmp, WorkingDir: tmp, Env: emptyEnv})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadEffective_CorruptUserLayerDowngradedToWarning(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	cfgDir := filepath.Join(home, ".config", "skill-up")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cfgDir, "config.yaml"), "telemetry: : :\n")

	var warnings strings.Builder
	cfg, sources, err := LoadEffective(LoadOptions{
		HomeDir:    home,
		WorkingDir: tmp,
		Env:        emptyEnv,
		Warnings:   &warnings,
	})
	if err != nil {
		t.Fatalf("expected corrupt user layer to be a warning, got error: %v", err)
	}
	if cfg.Telemetry.ServiceName != "" {
		t.Errorf("expected empty config after skipping corrupt user layer, got %#v", cfg)
	}
	for _, s := range sources {
		if s.Kind == "user" {
			t.Errorf("user source should not be present when its file was skipped, got %#v", sources)
		}
	}
	if !strings.Contains(warnings.String(), "warning: ignoring user config") {
		t.Errorf("expected user-config warning, got %q", warnings.String())
	}
}

func TestLoadEffective_CorruptProjectLayerDowngradedToWarning(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, ".skill-up.yaml"), "telemetry: : :\n")

	var warnings strings.Builder
	if _, _, err := LoadEffective(LoadOptions{
		HomeDir:    tmp,
		WorkingDir: tmp,
		Env:        emptyEnv,
		Warnings:   &warnings,
	}); err != nil {
		t.Fatalf("expected corrupt project layer to be a warning, got error: %v", err)
	}
	if !strings.Contains(warnings.String(), "warning: ignoring project config") {
		t.Errorf("expected project-config warning, got %q", warnings.String())
	}
}

func TestLoadEffective_ValidateErrorIncludesSources(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, ".skill-up.yaml")
	writeFile(t, path, "telemetry:\n  traces:\n    protocol: tcp\n")
	_, _, err := LoadEffective(LoadOptions{HomeDir: tmp, WorkingDir: tmp, Env: emptyEnv})
	if err == nil {
		t.Fatal("expected validate error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("validate error should mention source path; got %v", err)
	}
}

func TestLoadFile(t *testing.T) {
	tmp := t.TempDir()
	valid := filepath.Join(tmp, "config.yaml")
	writeFile(t, valid, "telemetry:\n  service_name: cli\n")

	cfg, err := LoadFile(valid)
	if err != nil {
		t.Fatalf("LoadFile valid returned error: %v", err)
	}
	if cfg.Telemetry.ServiceName != "cli" {
		t.Fatalf("service name = %q, want cli", cfg.Telemetry.ServiceName)
	}

	if _, err := LoadFile(filepath.Join(tmp, "missing.yaml")); err == nil || !strings.Contains(err.Error(), "config file not found") {
		t.Fatalf("LoadFile missing error = %v", err)
	}

	invalid := filepath.Join(tmp, "invalid.yaml")
	writeFile(t, invalid, "telemetry:\n  traces:\n    protocol: tcp\n")
	if _, err := LoadFile(invalid); err == nil || !strings.Contains(err.Error(), "validate") {
		t.Fatalf("LoadFile invalid error = %v", err)
	}
}

func TestResolveConfigPaths(t *testing.T) {
	t.Parallel()

	xdg := t.TempDir()
	env := func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return xdg
		}
		return ""
	}
	if got := resolveUserConfigPath(LoadOptions{HomeDir: "/home/test"}, env); got != filepath.Join(xdg, UserConfigDir, UserConfigFile) {
		t.Fatalf("resolveUserConfigPath XDG = %q", got)
	}
	if got := resolveUserConfigPath(LoadOptions{HomeDir: "/home/test"}, emptyEnv); got != filepath.Join("/home/test", ".config", UserConfigDir, UserConfigFile) {
		t.Fatalf("resolveUserConfigPath home = %q", got)
	}
	if got := resolveProjectConfigPath(LoadOptions{WorkingDir: "/repo"}); got != filepath.Join("/repo", ProjectConfigFile) {
		t.Fatalf("resolveProjectConfigPath = %q", got)
	}
}
