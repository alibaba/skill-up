// Package customengine defines dependency-free configuration and parsing
// primitives shared by the config loader and custom agent transports.
package customengine

import (
	"maps"
	"slices"
)

// Config describes a user-defined agent engine that is not a
// built-in agent. It is read only when engine.name does not match a built-in
// agent. See docs/design/custom-engine.md for the full contract.
type Config struct {
	Transport      string            `yaml:"transport"` // local, http
	TimeoutSeconds int               `yaml:"timeout_seconds,omitempty"`
	ResponseFormat string            `yaml:"response_format,omitempty"` // session_result (default), text
	Env            map[string]string `yaml:"env,omitempty"`
	Kwargs         map[string]string `yaml:"kwargs,omitempty"`
	Local          *LocalConfig      `yaml:"local,omitempty"`
	HTTP           *HTTPConfig       `yaml:"http,omitempty"`
}

// LocalConfig configures the local transport: a command executed inside
// the current runtime via runtime.Exec.
type LocalConfig struct {
	Command    string   `yaml:"command"`
	Args       []string `yaml:"args,omitempty"`
	Cwd        string   `yaml:"cwd,omitempty"`
	InputFile  string   `yaml:"input_file,omitempty"`
	OutputFile string   `yaml:"output_file,omitempty"`
}

// HTTPConfig configures the http transport: a remote or local HTTP agent
// service with JSON request/response and optional multipart file upload.
type HTTPConfig struct {
	URL     string            `yaml:"url"`
	Method  string            `yaml:"method,omitempty"` // POST
	Headers map[string]string `yaml:"headers,omitempty"`
	Files   []HTTPFile        `yaml:"files,omitempty"`
	// RequestBody is an arbitrary YAML value: a map, a sequence, or a scalar
	// such as `request_body: ${session_input}`. It is rendered by the http
	// transport, which injects ${session_input} / ${messages} / ${kwargs} as
	// JSON structures. Declared as any so yaml.v3 can unmarshal a scalar.
	RequestBody any `yaml:"request_body,omitempty"`
}

// HTTPFile declares a workspace file (or glob) uploaded with an HTTP request.
type HTTPFile struct {
	Path     string `yaml:"path"`
	Required *bool  `yaml:"required,omitempty"` // defaults to true
}

// CloneConfig returns a deep copy of cfg. RequestBody is cloned according to
// the map, sequence, and scalar shapes produced by YAML decoding.
func CloneConfig(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}

	cloned := *cfg
	cloned.Env = maps.Clone(cfg.Env)
	cloned.Kwargs = maps.Clone(cfg.Kwargs)

	if cfg.Local != nil {
		local := *cfg.Local
		local.Args = slices.Clone(cfg.Local.Args)
		cloned.Local = &local
	}
	if cfg.HTTP != nil {
		http := *cfg.HTTP
		http.Headers = maps.Clone(cfg.HTTP.Headers)
		http.Files = slices.Clone(cfg.HTTP.Files)
		for i := range http.Files {
			if cfg.HTTP.Files[i].Required != nil {
				required := *cfg.HTTP.Files[i].Required
				http.Files[i].Required = &required
			}
		}
		http.RequestBody = cloneYAMLValue(cfg.HTTP.RequestBody)
		cloned.HTTP = &http
	}

	return &cloned
}

func cloneYAMLValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[key] = cloneYAMLValue(item)
		}
		return cloned
	case map[any]any:
		cloned := make(map[any]any, len(typed))
		for key, item := range typed {
			cloned[key] = cloneYAMLValue(item)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneYAMLValue(item)
		}
		return cloned
	default:
		return value
	}
}
