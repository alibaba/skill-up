// Package userconfig defines the schema and merge semantics for skill-up's
// optional user-level configuration file (typically
// ~/.config/skill-up/config.yaml).
//
// The config provides defaults for OpenTelemetry-related environment variables
// and per-environment.type runtime kwargs that the `run` command consumes. The
// embedded defaults are intentionally empty: skill-up ships with no
// alibaba-specific (or any other vendor-specific) defaults; downstream
// consumers maintain their own config file.
//
// Discovery and precedence are handled by LoadEffective in loader.go.
package userconfig

import (
	"fmt"
	"maps"
	"strings"
)

// Config is the on-disk schema for a skill-up user-config file.
type Config struct {
	SchemaVersion string            `yaml:"schema_version,omitempty"`
	Kind          string            `yaml:"kind,omitempty"`
	Telemetry     Telemetry         `yaml:"telemetry,omitempty"`
	Env           map[string]string `yaml:"env,omitempty"`
	RuntimeKwargs map[string]Kwargs `yaml:"runtime_kwargs,omitempty"`
}

// Telemetry holds OpenTelemetry-related defaults. Each non-empty field maps to
// a standard OTEL_* environment variable applied only when the corresponding
// variable is currently unset in the process environment.
type Telemetry struct {
	ServiceName        string            `yaml:"service_name,omitempty"`
	TracesExporter     string            `yaml:"traces_exporter,omitempty"`
	Traces             TracesConfig      `yaml:"traces,omitempty"`
	ResourceAttributes map[string]string `yaml:"resource_attributes,omitempty"`
	// Verbose, when explicitly set to true, enables OTEL_LOG_USER_PROMPTS,
	// OTEL_LOG_TOOL_CONTENT, and OTEL_LOG_TOOL_DETAILS. A pointer is used so
	// higher-precedence layers can both enable (true) and disable (false) it;
	// a nil pointer means "inherit from lower-precedence layers".
	Verbose *bool `yaml:"verbose,omitempty"`
}

// TracesConfig holds OTLP traces transport defaults.
type TracesConfig struct {
	Endpoint string `yaml:"endpoint,omitempty"`
	Protocol string `yaml:"protocol,omitempty"`
}

// Kwargs is a map of runtime-kwarg key/value pairs for one environment type.
type Kwargs map[string]string

// Validate returns nil for an entirely empty Config. It only flags malformed
// values when fields are explicitly set.
func (c *Config) Validate() error {
	if c == nil {
		return nil
	}
	if p := strings.TrimSpace(c.Telemetry.Traces.Protocol); p != "" {
		switch p {
		case "grpc", "http/protobuf":
		default:
			return fmt.Errorf("invalid telemetry.traces.protocol %q: must be \"grpc\" or \"http/protobuf\"", p)
		}
	}
	return nil
}

// overlay copies non-zero fields from other onto c. Maps are merged key-wise
// with other winning on conflicts.
func (c *Config) overlay(other Config) {
	if other.SchemaVersion != "" {
		c.SchemaVersion = other.SchemaVersion
	}
	if other.Kind != "" {
		c.Kind = other.Kind
	}

	if other.Telemetry.ServiceName != "" {
		c.Telemetry.ServiceName = other.Telemetry.ServiceName
	}
	if other.Telemetry.TracesExporter != "" {
		c.Telemetry.TracesExporter = other.Telemetry.TracesExporter
	}
	if other.Telemetry.Traces.Endpoint != "" {
		c.Telemetry.Traces.Endpoint = other.Telemetry.Traces.Endpoint
	}
	if other.Telemetry.Traces.Protocol != "" {
		c.Telemetry.Traces.Protocol = other.Telemetry.Traces.Protocol
	}
	if other.Telemetry.Verbose != nil {
		v := *other.Telemetry.Verbose
		c.Telemetry.Verbose = &v
	}
	c.Telemetry.ResourceAttributes = mergeStringMap(c.Telemetry.ResourceAttributes, other.Telemetry.ResourceAttributes)
	c.Env = mergeStringMap(c.Env, other.Env)

	if len(other.RuntimeKwargs) > 0 {
		if c.RuntimeKwargs == nil {
			c.RuntimeKwargs = map[string]Kwargs{}
		}
		for envType, kw := range other.RuntimeKwargs {
			existing := c.RuntimeKwargs[envType]
			merged := mergeStringMap(existing, kw)
			c.RuntimeKwargs[envType] = merged
		}
	}
}

func mergeStringMap(base, other map[string]string) map[string]string {
	if len(other) == 0 {
		return base
	}
	if base == nil {
		base = make(map[string]string, len(other))
	}
	maps.Copy(base, other)
	return base
}
