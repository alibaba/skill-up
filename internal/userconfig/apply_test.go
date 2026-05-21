package userconfig

import (
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
		Verbose: ptrBool(true),
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

func ptrBool(b bool) *bool { return &b }

func TestApplyEnv_VerboseFalseExplicitDoesNotSetLogEnvs(t *testing.T) {
	for _, k := range []string{envOtelLogUserPrompts, envOtelLogToolContent, envOtelLogToolDetails} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	c := Config{Telemetry: Telemetry{Verbose: ptrBool(false)}}
	if _, err := ApplyEnv(c); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv(envOtelLogUserPrompts); v != "" {
		t.Errorf("verbose=false should not set %s, got %q", envOtelLogUserPrompts, v)
	}
}
