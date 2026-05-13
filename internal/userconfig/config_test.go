package userconfig

import "testing"

func TestOverlay_ScalarOverwrite(t *testing.T) {
	c := Config{SchemaVersion: "v1alpha1", Telemetry: Telemetry{ServiceName: "a"}}
	c.overlay(Config{Telemetry: Telemetry{ServiceName: "b"}})
	if c.Telemetry.ServiceName != "b" {
		t.Fatalf("expected b, got %q", c.Telemetry.ServiceName)
	}
	if c.SchemaVersion != "v1alpha1" {
		t.Fatalf("schema_version overwritten by zero value")
	}
}

func TestOverlay_ZeroValueSkip(t *testing.T) {
	c := Config{Telemetry: Telemetry{ServiceName: "a", TracesExporter: "otlp"}}
	c.overlay(Config{Telemetry: Telemetry{ServiceName: ""}})
	if c.Telemetry.ServiceName != "a" {
		t.Fatalf("zero value should not overwrite, got %q", c.Telemetry.ServiceName)
	}
	if c.Telemetry.TracesExporter != "otlp" {
		t.Fatalf("traces_exporter clobbered: %q", c.Telemetry.TracesExporter)
	}
}

func TestOverlay_MapMerge(t *testing.T) {
	c := Config{Env: map[string]string{"A": "1", "B": "2"}}
	c.overlay(Config{Env: map[string]string{"B": "two", "C": "3"}})
	if c.Env["A"] != "1" || c.Env["B"] != "two" || c.Env["C"] != "3" {
		t.Fatalf("merged map wrong: %+v", c.Env)
	}
}

func TestOverlay_RuntimeKwargsMerge(t *testing.T) {
	c := Config{RuntimeKwargs: map[string]Kwargs{"opensandbox": {"base_url": "x"}}}
	c.overlay(Config{RuntimeKwargs: map[string]Kwargs{"opensandbox": {"extensions": "y"}, "custom": {"image": "ubuntu"}}})
	if c.RuntimeKwargs["opensandbox"]["base_url"] != "x" || c.RuntimeKwargs["opensandbox"]["extensions"] != "y" {
		t.Fatalf("kwarg merge wrong: %+v", c.RuntimeKwargs["opensandbox"])
	}
	if c.RuntimeKwargs["custom"]["image"] != "ubuntu" {
		t.Fatalf("new envType not added")
	}
}

func TestOverlay_VerboseExplicitOverridesEitherWay(t *testing.T) {
	tru, fls := true, false

	// Higher-precedence layer can explicitly disable verbose.
	c := Config{Telemetry: Telemetry{Verbose: &tru}}
	c.overlay(Config{Telemetry: Telemetry{Verbose: &fls}})
	if c.Telemetry.Verbose == nil || *c.Telemetry.Verbose {
		t.Fatalf("verbose=false in overlay should win, got %v", c.Telemetry.Verbose)
	}

	// A nil verbose in the overlay leaves the lower-precedence value untouched.
	c2 := Config{Telemetry: Telemetry{Verbose: &tru}}
	c2.overlay(Config{Telemetry: Telemetry{Verbose: nil}})
	if c2.Telemetry.Verbose == nil || !*c2.Telemetry.Verbose {
		t.Fatalf("nil verbose in overlay should not clear existing true, got %v", c2.Telemetry.Verbose)
	}
}

func TestValidate_Empty(t *testing.T) {
	var c Config
	if err := c.Validate(); err != nil {
		t.Fatalf("empty config should validate: %v", err)
	}
}

func TestValidate_Protocol(t *testing.T) {
	for _, p := range []string{"grpc", "http/protobuf"} {
		c := Config{Telemetry: Telemetry{Traces: TracesConfig{Protocol: p}}}
		if err := c.Validate(); err != nil {
			t.Errorf("protocol %q should validate: %v", p, err)
		}
	}
	c := Config{Telemetry: Telemetry{Traces: TracesConfig{Protocol: "tcp"}}}
	if err := c.Validate(); err == nil {
		t.Fatalf("invalid protocol should fail validation")
	}
}
