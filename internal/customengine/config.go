// Package customengine defines dependency-free configuration and parsing
// primitives shared by the config loader and custom agent transports.
package customengine

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
