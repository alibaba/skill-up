package userconfig

import (
	"bytes"
	"maps"
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

func TestLoadEffective_ProjectRequiresExplicitTrust(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	writeFile(t, filepath.Join(home, ".config", UserConfigDir, UserConfigFile), "telemetry:\n  service_name: trusted-service\n")
	writeFile(t, filepath.Join(wd, ProjectConfigFile), "telemetry:\n  service_name: project\n")

	var warnings bytes.Buffer
	cfg, sources, err := LoadEffective(LoadOptions{HomeDir: home, WorkingDir: wd, Env: emptyEnv, Warnings: &warnings})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.ServiceName != "trusted-service" {
		t.Fatalf("untrusted project must not override user defaults: %q", cfg.Telemetry.ServiceName)
	}
	if len(sources) != 2 || sources[1].Kind != sourceUser {
		t.Fatalf("ignored project must not contribute a source: %+v", sources)
	}
	if !strings.Contains(warnings.String(), "--config") {
		t.Fatalf("warning must explain explicit opt-in: %q", warnings.String())
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

func TestLoadEffective_ProjectCannotRedirectTrustedCredentials(t *testing.T) {
	for _, source := range []string{"automatic", sourceExplicit} {
		t.Run(source, func(t *testing.T) {
			testProjectEndpointTrust(t, source)
		})
	}
}

func testProjectEndpointTrust(t *testing.T, source string) {
	t.Helper()
	explicit := source == sourceExplicit
	const secret = "synthetic-token"
	const trusted = "https://trusted.example.invalid"
	const untrusted = "https://project.example.invalid"
	projectEnv := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT":         untrusted,
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":  untrusted,
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": untrusted,
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":    untrusted,
		"HTTPS_PROXY":                         untrusted,
		"http_proxy":                          untrusted,
		"OPENAI_BASE_URL":                     untrusted,
		"OPENSANDBOX_BASE_URL":                untrusted,
		"OTEL_TRACES_EXPORTER":                "otlp",
	}
	t.Setenv("SKILL_UP_TEST_SECRET", secret)
	for key := range projectEnv {
		t.Setenv(key, "")
		_ = os.Unsetenv(key)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")
	_ = os.Unsetenv("OTEL_EXPORTER_OTLP_HEADERS")
	t.Setenv("OPENSANDBOX_API_KEY", "")
	_ = os.Unsetenv("OPENSANDBOX_API_KEY")
	var warnings bytes.Buffer
	opts := writeEndpointTrustConfigs(t, trusted, untrusted, projectEnv)
	opts.Warnings = &warnings
	if explicit {
		opts.ExplicitPath = filepath.Join(opts.WorkingDir, ProjectConfigFile)
	}
	cfg, _, err := LoadEffective(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyEnv(cfg); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"); got != "authorization="+secret {
		t.Fatalf("trusted headers = %q", got)
	}
	for key, value := range projectEnv {
		want := ""
		if key == envOtelExporterOTLPTracesEndpt {
			want = trusted
		}
		if explicit {
			want = value
		}
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if got := os.Getenv("OPENSANDBOX_API_KEY"); got != secret {
		t.Fatalf("trusted sandbox key = %q", got)
	}
	wantKwargs := Kwargs{"base_url": trusted}
	if explicit {
		wantKwargs = Kwargs{"base_url": untrusted, "request_timeout_seconds": "42"}
	}
	kwargs := MergeKwargs(cfg, "opensandbox", nil)
	if !maps.Equal(kwargs, wantKwargs) {
		t.Errorf("runtime defaults = %v, want %v", kwargs, wantKwargs)
	}
	if got := warnings.Len() > 0; got == explicit {
		t.Errorf("warning present = %t for %s config: %s", got, source, &warnings)
	}
}

func writeEndpointTrustConfigs(t *testing.T, trusted, untrusted string, projectEnv map[string]string) LoadOptions {
	t.Helper()
	home, wd := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(home, ".config", UserConfigDir, UserConfigFile), `telemetry:
  traces:
    endpoint: `+trusted+`
env:
  OTEL_EXPORTER_OTLP_HEADERS: authorization=${SKILL_UP_TEST_SECRET}
  OPENSANDBOX_API_KEY: ${SKILL_UP_TEST_SECRET}
runtime_kwargs:
  opensandbox:
    base_url: `+trusted+`
`)
	var project strings.Builder
	project.WriteString("telemetry:\n  traces:\n    endpoint: " + untrusted + "\nenv:\n")
	for key, value := range projectEnv {
		project.WriteString("  " + key + ": " + value + "\n")
	}
	project.WriteString("runtime_kwargs:\n  opensandbox:\n    base_url: " + untrusted + "\n    request_timeout_seconds: 42\n")
	writeFile(t, filepath.Join(wd, ProjectConfigFile), project.String())
	return LoadOptions{HomeDir: home, WorkingDir: wd, Env: emptyEnv}
}

type envExpansionCase struct {
	name            string
	user            string
	project         string
	explicit        string
	explicitProject bool
	userEnvPath     bool
	userIsProject   bool
	want            map[string]string
}

func TestLoadEffective_EnvExpansionTrust(t *testing.T) {
	const secret = "test-token"
	const expanded = "leaked=" + secret
	const userOnly = "SKILL_UP_TEST_USER_ONLY"
	const explicitOnly = "SKILL_UP_TEST_EXPLICIT_ONLY"
	const project = `env:
  OTEL_EXPORTER_OTLP_ENDPOINT: https://collector.example.invalid
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_RESOURCE_ATTRIBUTES: leaked=${SKILL_UP_TEST_SECRET}
`
	const user = `env:
  OTEL_RESOURCE_ATTRIBUTES: leaked=${SKILL_UP_TEST_SECRET}
  SKILL_UP_TEST_USER_ONLY: leaked=${SKILL_UP_TEST_SECRET}
`
	const explicit = `env:
  SKILL_UP_TEST_EXPLICIT_ONLY: leaked=${SKILL_UP_TEST_SECRET}
`
	t.Setenv("SKILL_UP_TEST_SECRET", secret)
	for _, tc := range []envExpansionCase{
		{
			name: "automatic project is ignored", project: project,
			want: map[string]string{envOtelResourceAttributes: ""},
		},
		{
			name: "user defaults expand", user: user,
			want: map[string]string{envOtelResourceAttributes: expanded, userOnly: expanded},
		},
		{
			name: "project cannot override trusted user defaults", user: user, project: project,
			want: map[string]string{envOtelResourceAttributes: expanded, userOnly: expanded},
		},
		{
			name: "explicit config does not trust unrelated project keys", user: user, project: project, explicit: explicit,
			want: map[string]string{envOtelResourceAttributes: expanded, userOnly: expanded, explicitOnly: expanded},
		},
		{
			name: "explicitly selected project expands", project: project, explicitProject: true,
			want: map[string]string{envOtelResourceAttributes: expanded},
		},
		{
			name: "explicit key overrides project trust", project: project, explicit: user,
			want: map[string]string{envOtelResourceAttributes: expanded, userOnly: expanded},
		},
		{
			name: "user environment path expands", user: user, userEnvPath: true,
			want: map[string]string{envOtelResourceAttributes: expanded, userOnly: expanded},
		},
		{
			name: "user selected project path is trusted", project: project, userEnvPath: true, userIsProject: true,
			want: map[string]string{envOtelResourceAttributes: expanded},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testEnvExpansionTrust(t, tc)
		})
	}
}

func testEnvExpansionTrust(t *testing.T, tc envExpansionCase) {
	t.Helper()
	for _, key := range []string{envOtelResourceAttributes, "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL", "SKILL_UP_TEST_USER_ONLY", "SKILL_UP_TEST_EXPLICIT_ONLY"} {
		t.Setenv(key, "")
		_ = os.Unsetenv(key)
	}
	home, wd := t.TempDir(), t.TempDir()
	userPath := filepath.Join(home, ".config", UserConfigDir, UserConfigFile)
	if tc.userEnvPath {
		userPath = filepath.Join(t.TempDir(), "user.yaml")
	}
	projectPath := filepath.Join(wd, ProjectConfigFile)
	if tc.userIsProject {
		userPath = projectPath
	}
	writeFile(t, userPath, tc.user)
	writeFile(t, projectPath, tc.project)
	opts := LoadOptions{HomeDir: home, WorkingDir: wd, Env: emptyEnv}
	if tc.userEnvPath {
		opts.Env = func(key string) string {
			if key == ConfigEnvVar {
				return userPath
			}
			return ""
		}
	}
	if tc.explicit != "" {
		opts.ExplicitPath = filepath.Join(t.TempDir(), "explicit.yaml")
		writeFile(t, opts.ExplicitPath, tc.explicit)
	}
	if tc.explicitProject {
		opts.ExplicitPath = projectPath
	}
	cfg, _, err := LoadEffective(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyEnv(cfg); err != nil {
		t.Fatal(err)
	}
	for key, want := range tc.want {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
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
	_, _, err := LoadEffective(LoadOptions{ExplicitPath: path, HomeDir: tmp, WorkingDir: tmp, Env: emptyEnv})
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
