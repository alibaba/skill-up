package userconfig

import (
	"context"
	"maps"
	"os"
	"slices"
	"testing"
)

func TestApplyEnv_SetsTelemetry(t *testing.T) {
	for _, k := range []string{envOtelServiceName, envOtelTracesExporter, envOtelExporterOTLPTracesEndpt, envOtelExporterOTLPTracesProto, envOtelResourceAttributes, envOtelLogUserPrompts, envOtelLogToolContent, envOtelLogToolDetails} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}

	c := Config{Telemetry: Telemetry{
		ServiceName:    "svc",
		TracesExporter: "otlp",
		Traces:         TracesConfig{Endpoint: "http://x", Protocol: "grpc"},
		ResourceAttributes: map[string]string{
			"b.key": "2",
			"a.key": "1",
		},
		Verbose: new(true),
	}}
	applied, err := ApplyEnv(c)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv(envOtelServiceName) != "svc" {
		t.Errorf("svc not set")
	}
	if got := os.Getenv(envOtelResourceAttributes); got != "a.key=1,b.key=2" {
		t.Errorf("resource attributes wrong: %q", got)
	}
	if os.Getenv(envOtelLogUserPrompts) != "1" {
		t.Errorf("verbose flag not set")
	}
	if !slices.Contains(applied, envOtelServiceName) {
		t.Errorf("applied list missing service name: %v", applied)
	}
}

func TestApplyEnv_RespectsExisting(t *testing.T) {
	t.Setenv(envOtelServiceName, "preexisting")
	c := Config{Telemetry: Telemetry{ServiceName: "from-config"}}
	applied, err := ApplyEnv(c)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv(envOtelServiceName) != "preexisting" {
		t.Errorf("preexisting clobbered")
	}
	if slices.Contains(applied, envOtelServiceName) {
		t.Errorf("should not report applied: %v", applied)
	}
}

func TestApplyEnv_ArbitraryEnv(t *testing.T) {
	_ = os.Unsetenv("MY_KEY")
	c := Config{Env: map[string]string{"MY_KEY": "v"}}
	if _, err := ApplyEnv(c); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("MY_KEY") != "v" {
		t.Errorf("MY_KEY not set")
	}
}

func TestApplyEnv_ExpandsPlaceholders(t *testing.T) {
	t.Setenv("SKILL_UP_TEST_SOURCE", "example-value")
	t.Setenv("SKILL_UP_TEST_EMPTY", "")
	t.Setenv("SKILL_UP_TEST_NESTED", "${SKILL_UP_TEST_SOURCE}")
	t.Setenv("SKILL_UP_TEST_ABSENT", "")
	_ = os.Unsetenv("SKILL_UP_TEST_ABSENT")
	for _, tc := range []struct{ name, value, want string }{
		{"embedded", "authorization=${SKILL_UP_TEST_SOURCE}", "authorization=example-value"},
		{"repeated", "${SKILL_UP_TEST_SOURCE}/${SKILL_UP_TEST_SOURCE}", "example-value/example-value"},
		{"unset", "${SKILL_UP_TEST_ABSENT}", "${SKILL_UP_TEST_ABSENT}"},
		{"empty", "${SKILL_UP_TEST_EMPTY}", ""},
		{"mixed", "${SKILL_UP_TEST_SOURCE}/${SKILL_UP_TEST_ABSENT}", "example-value/${SKILL_UP_TEST_ABSENT}"},
		{"single pass", "${SKILL_UP_TEST_NESTED}", "${SKILL_UP_TEST_SOURCE}"},
		{"bare reference", "$SKILL_UP_TEST_SOURCE", "$SKILL_UP_TEST_SOURCE"},
		{"shell syntax", "${SKILL_UP_TEST_ABSENT:-fallback}", "${SKILL_UP_TEST_ABSENT:-fallback}"},
		{"unclosed", "${SKILL_UP_TEST_SOURCE", "${SKILL_UP_TEST_SOURCE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const target = "SKILL_UP_TEST_TARGET"
			t.Setenv(target, "")
			_ = os.Unsetenv(target)
			var cfg Config
			cfg.overlay(Config{Env: map[string]string{target: tc.value}}, "user")
			applied, err := ApplyEnv(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got, exists := os.LookupEnv(target); !exists || got != tc.want {
				t.Fatalf("value = %q (set=%t), want %q", got, exists, tc.want)
			}
			if !slices.Contains(applied, target) || cfg.Env[target] != tc.value {
				t.Fatal("applied keys or original config changed unexpectedly")
			}
		})
	}
}

func TestApplyEnv_ExpansionPreservesPrecedence(t *testing.T) {
	const source, target = "SKILL_UP_TEST_A", "SKILL_UP_TEST_Z"
	t.Setenv(source, "")
	_ = os.Unsetenv(source)
	t.Setenv(target, "")
	_ = os.Unsetenv(target)
	var cfg Config
	cfg.overlay(Config{Env: map[string]string{source: "from-config", target: "${" + source + "}"}}, "user")
	if _, err := ApplyEnv(cfg); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(target); got != "${"+source+"}" {
		t.Fatalf("config defaults must not become expansion sources: %q", got)
	}
	t.Setenv(source, "from-shell")
	for _, existing := range []string{"shell-target", ""} {
		t.Setenv(target, existing)
		if _, err := ApplyEnv(cfg); err != nil {
			t.Fatal(err)
		}
		if got := os.Getenv(target); got != existing {
			t.Fatalf("existing value overwritten: %q", got)
		}
	}
}

func TestApplyEnv_WithoutSourceKeepsPlaceholders(t *testing.T) {
	const target = "SKILL_UP_TEST_TARGET"
	t.Setenv("SKILL_UP_TEST_SOURCE", "example-value")
	t.Setenv(target, "")
	_ = os.Unsetenv(target)
	cfg := Config{Env: map[string]string{target: "${SKILL_UP_TEST_SOURCE}"}}
	if _, err := ApplyEnv(cfg); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(target); got != cfg.Env[target] {
		t.Fatalf("config without a trusted source expanded: %q", got)
	}
}

func TestMergeKwargs_EvalWins(t *testing.T) {
	c := Config{RuntimeKwargs: map[string]Kwargs{
		"opensandbox": {"base_url": "from-config", "extensions": "ext"},
	}}
	merged := MergeKwargs(c, "opensandbox", map[string]string{"base_url": "from-eval"})
	if merged["base_url"] != "from-eval" {
		t.Errorf("eval should win: %q", merged["base_url"])
	}
	if merged["extensions"] != "ext" {
		t.Errorf("missing key should be filled: %q", merged["extensions"])
	}
}

func TestMergeKwargs_UnknownEnvType(t *testing.T) {
	c := Config{RuntimeKwargs: map[string]Kwargs{"custom": {"image": "x"}}}
	merged := MergeKwargs(c, "opensandbox", map[string]string{"base_url": "u"})
	if merged["base_url"] != "u" {
		t.Errorf("eval kwarg lost: %+v", merged)
	}
	if _, ok := merged["image"]; ok {
		t.Errorf("unrelated kwarg leaked: %+v", merged)
	}
}

func TestMergeKwargs_NilEval(t *testing.T) {
	c := Config{RuntimeKwargs: map[string]Kwargs{"opensandbox": {"base_url": "u"}}}
	merged := MergeKwargs(c, "opensandbox", nil)
	if merged["base_url"] != "u" {
		t.Errorf("user kwarg should fill: %+v", merged)
	}
}

func TestMergeKwargs_PreservesInputs(t *testing.T) {
	t.Parallel()

	userKwargs := Kwargs{"shared": "user", "user-only": "default"}
	evalKwargs := map[string]string{"shared": "eval", "eval-only": "explicit"}
	c := Config{RuntimeKwargs: map[string]Kwargs{"opensandbox": userKwargs}}
	merged := MergeKwargs(c, "opensandbox", evalKwargs)
	want := map[string]string{"shared": "eval", "user-only": "default", "eval-only": "explicit"}
	if !maps.Equal(merged, want) {
		t.Fatalf("merged = %v, want %v", merged, want)
	}
	merged["shared"] = "changed"
	if userKwargs["shared"] != "user" || evalKwargs["shared"] != "eval" {
		t.Fatal("merged map aliases an input")
	}
}

func TestConfigContextRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := Config{Env: map[string]string{"A": "B"}}
	sources := []Source{{Kind: "project", Path: ".skill-up.yaml"}}
	ctx := WithContext(context.Background(), cfg, sources)

	gotCfg, gotSources, ok := FromContext(ctx)
	if !ok {
		t.Fatal("FromContext ok = false, want true")
	}
	if gotCfg.Env["A"] != "B" {
		t.Fatalf("config from context = %#v", gotCfg)
	}
	if len(gotSources) != 1 || gotSources[0].Kind != "project" {
		t.Fatalf("sources from context = %#v", gotSources)
	}
	if _, _, ok := FromContext(context.Background()); ok {
		t.Fatal("FromContext on empty context = true, want false")
	}
}

func TestApplyEnv_PassesThroughSkillUpEnvFromUserEnvMap(t *testing.T) {
	// Project-routing attribute envs are not in the schema -- downstream
	// vendor bundles set them through the generic env: map. Verify pass-through.
	const key = "SKILL_UP_OTEL_RESOURCE_ATTRIBUTES"
	t.Setenv(key, "")
	_ = os.Unsetenv(key)

	c := Config{Env: map[string]string{key: "telemetry.project.id=744,telemetry.component=cli"}}
	if _, err := ApplyEnv(c); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(key); got != "telemetry.project.id=744,telemetry.component=cli" {
		t.Errorf("%s = %q", key, got)
	}
}

func TestApplyEnv_VerboseFalseExplicitDoesNotSetLogEnvs(t *testing.T) {
	for _, k := range []string{envOtelLogUserPrompts, envOtelLogToolContent, envOtelLogToolDetails} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	c := Config{Telemetry: Telemetry{Verbose: new(false)}}
	if _, err := ApplyEnv(c); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv(envOtelLogUserPrompts); v != "" {
		t.Errorf("verbose=false should not set %s, got %q", envOtelLogUserPrompts, v)
	}
}
