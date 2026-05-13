package config

import (
	"testing"
	"time"
)

func TestDefaultEvalConfig(t *testing.T) { //nolint:cyclop,gocyclo // exhaustive default-config field assertions
	t.Parallel()

	cfg := DefaultEvalConfig()
	if cfg == nil {
		t.Fatal("DefaultEvalConfig() returned nil")
	}

	// Verify schema version
	if cfg.SchemaVersion != "v1alpha1" {
		t.Errorf("SchemaVersion = %q, want %q", cfg.SchemaVersion, "v1alpha1")
	}

	// Verify environment defaults
	if cfg.Environment.Type != "none" {
		t.Errorf("Environment.Type = %q, want %q", cfg.Environment.Type, "none")
	}
	if cfg.Environment.WorkspaceMount != "/workspace" {
		t.Errorf("Environment.WorkspaceMount = %q, want %q", cfg.Environment.WorkspaceMount, "/workspace")
	}
	if cfg.Environment.Env == nil {
		t.Error("Environment.Env should be initialized, got nil")
	}

	// Verify engine defaults
	if cfg.Engine.Name != "claude_code" {
		t.Errorf("Engine.Name = %q, want %q", cfg.Engine.Name, "claude_code")
	}
	// Model provider/name are intentionally empty in defaults — users must configure them.
	if cfg.Engine.Model.Provider != "" {
		t.Errorf("Engine.Model.Provider = %q, want empty", cfg.Engine.Model.Provider)
	}
	if cfg.Engine.Model.Name != "" {
		t.Errorf("Engine.Model.Name = %q, want empty", cfg.Engine.Model.Name)
	}

	// Verify case defaults
	if cfg.Cases.Defaults.TimeoutSeconds != 300 {
		t.Errorf("Cases.Defaults.TimeoutSeconds = %d, want %d", cfg.Cases.Defaults.TimeoutSeconds, 300)
	}
	if cfg.Cases.Defaults.MaxTurns != 10 {
		t.Errorf("Cases.Defaults.MaxTurns = %d, want %d", cfg.Cases.Defaults.MaxTurns, 10)
	}
	if cfg.Cases.Parallelism != 1 {
		t.Errorf("Cases.Parallelism = %d, want %d", cfg.Cases.Parallelism, 1)
	}

	// Verify judge defaults (no global judge configured)
	if cfg.Judge.Type != "" {
		t.Errorf("Judge.Type = %q, want empty (no global judge)", cfg.Judge.Type)
	}

	// Verify report defaults
	if len(cfg.Report.Formats) != 1 || cfg.Report.Formats[0] != "json" {
		t.Errorf("Report.Formats = %v, want [%q]", cfg.Report.Formats, "json")
	}

	// Verify empty slices are initialized (not nil)
	if cfg.MCP.Servers == nil {
		t.Error("MCP.Servers should be initialized, got nil")
	}
	if cfg.Skills == nil {
		t.Error("Skills should be initialized, got nil")
	}
	if cfg.Cases.Files == nil {
		t.Error("Cases.Files should be initialized, got nil")
	}
	if cfg.Report.Artifacts == nil {
		t.Error("Report.Artifacts should be initialized, got nil")
	}
}

func TestEnvironmentToRuntimeConfig_OpenSandboxFields(t *testing.T) {
	t.Parallel()

	env := Environment{
		Type:                  "opensandbox",
		Image:                 "ubuntu:24.04",
		SandboxTemplate:       "basic-template",
		WorkspaceMount:        "/work",
		Env:                   map[string]string{"A": "B"},
		SetupSteps:            []SetupStep{{Run: "echo setup"}},
		UseServerProxy:        true,
		ReadyTimeoutSeconds:   3,
		SandboxTimeoutSeconds: 120,
		Entrypoint:            []string{"tail", "-f", "/dev/null"},
		Metadata:              map[string]string{"case": "one"},
		Kwargs: map[string]string{
			"base_url":   "https://sandbox.example.test",
			"extensions": `{"template":"basic"}`,
		},
	}

	rtCfg := env.ToRuntimeConfig()
	if rtCfg.Type != "opensandbox" || rtCfg.Image != "ubuntu:24.04" || rtCfg.SandboxTemplate != "basic-template" {
		t.Fatalf("runtime config image fields mismatch: %+v", rtCfg)
	}
	if rtCfg.Kwargs["base_url"] != "https://sandbox.example.test" || !rtCfg.UseServerProxy {
		t.Fatalf("runtime config connection fields mismatch: %+v", rtCfg)
	}
	if rtCfg.ReadyTimeout != 3*time.Second || rtCfg.SandboxTimeout != 120*time.Second {
		t.Fatalf("runtime config timeouts mismatch: ready=%s sandbox=%s", rtCfg.ReadyTimeout, rtCfg.SandboxTimeout)
	}
	if len(rtCfg.Entrypoint) != 3 || rtCfg.Entrypoint[0] != "tail" {
		t.Fatalf("runtime config entrypoint mismatch: %+v", rtCfg.Entrypoint)
	}
	if rtCfg.Kwargs["extensions"] != `{"template":"basic"}` || rtCfg.Metadata["case"] != "one" {
		t.Fatalf("runtime config maps mismatch: kwargs=%v metadata=%v", rtCfg.Kwargs, rtCfg.Metadata)
	}
}

func TestEnvironmentToRuntimeConfig_OpenSandboxKwargs(t *testing.T) {
	t.Parallel()

	rtCfg := Environment{
		Type: "opensandbox",
		Kwargs: map[string]string{
			"base_url":   "https://custom-sandbox.example.test",
			"extensions": `{"profile":"ci"}`,
		},
	}.ToRuntimeConfig()

	if rtCfg.Kwargs["base_url"] != "https://custom-sandbox.example.test" {
		t.Fatalf("Kwargs = %#v, want explicit base_url", rtCfg.Kwargs)
	}
	if rtCfg.Kwargs["extensions"] != `{"profile":"ci"}` {
		t.Fatalf("Kwargs = %#v, want explicit extensions", rtCfg.Kwargs)
	}
}

func TestDefaultEvalConfig_ReturnsNewInstance(t *testing.T) {
	t.Parallel()

	cfg1 := DefaultEvalConfig()
	cfg2 := DefaultEvalConfig()

	// Mutate cfg1 and verify cfg2 is not affected
	cfg1.Engine.Name = "modified"
	if cfg2.Engine.Name == "modified" {
		t.Error("DefaultEvalConfig() should return independent instances")
	}
}
