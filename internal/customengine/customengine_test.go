package customengine

import (
	"net/http"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConfigPreservesYAMLSchema(t *testing.T) {
	var cfg Config
	err := yaml.Unmarshal([]byte(`
transport: http
timeout_seconds: 30
response_format: text
http:
  url: https://example.test/agent
  method: POST
  files:
    - path: artifacts/*.json
      required: false
  request_body: ${session_input}
`), &cfg)
	if err != nil {
		t.Fatalf("unmarshal Config: %v", err)
	}
	if cfg.Transport != "http" || cfg.TimeoutSeconds != 30 || cfg.ResponseFormat != "text" {
		t.Fatalf("top-level fields = %+v", cfg)
	}
	if cfg.HTTP == nil || cfg.HTTP.URL != "https://example.test/agent" || cfg.HTTP.Method != http.MethodPost {
		t.Fatalf("HTTP = %+v", cfg.HTTP)
	}
	if len(cfg.HTTP.Files) != 1 || cfg.HTTP.Files[0].Path != "artifacts/*.json" {
		t.Fatalf("HTTP.Files = %+v", cfg.HTTP.Files)
	}
	if cfg.HTTP.Files[0].Required == nil || *cfg.HTTP.Files[0].Required {
		t.Fatalf("HTTP.Files[0].Required = %v, want false", cfg.HTTP.Files[0].Required)
	}
	if cfg.HTTP.RequestBody != "${session_input}" {
		t.Fatalf("HTTP.RequestBody = %#v", cfg.HTTP.RequestBody)
	}
}

func TestParseTemplateToken(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want TemplateToken
	}{
		{name: "plain", in: "FOO", want: TemplateToken{Name: "FOO"}},
		{name: "default", in: "FOO:-bar", want: TemplateToken{Name: "FOO", Default: "bar", HasDefault: true}},
		{name: "error", in: "FOO?set FOO", want: TemplateToken{Name: "FOO", ErrMsg: "set FOO", HasErrForm: true}},
		{name: "default precedence", in: "FOO:-a?b", want: TemplateToken{Name: "FOO", Default: "a?b", HasDefault: true}},
		{name: "dotted name", in: "kwargs.profile", want: TemplateToken{Name: "kwargs.profile"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseTemplateToken(tt.in); got != tt.want {
				t.Fatalf("ParseTemplateToken(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestTemplateTokenRequiredErr(t *testing.T) {
	tests := []struct {
		name string
		tok  TemplateToken
		want string
	}{
		{name: "explicit", tok: TemplateToken{Name: "FOO", ErrMsg: "set FOO"}, want: "set FOO"},
		{name: "empty", tok: TemplateToken{Name: "FOO"}, want: "FOO is required"},
		{name: "whitespace", tok: TemplateToken{Name: "FOO", ErrMsg: "  "}, want: "FOO is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tok.RequiredErr(); got == nil || got.Error() != tt.want {
				t.Fatalf("RequiredErr() = %v, want %q", got, tt.want)
			}
		})
	}
}

func TestWorkspaceRelPathSafe(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "artifact.json", want: true},
		{path: "artifacts/*.json", want: true},
		{path: "**/*", want: true},
		{path: ".", want: true},
		{path: "", want: false},
		{path: "/etc/passwd", want: false},
		{path: "../secret", want: false},
		{path: "artifacts/../secret", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := WorkspaceRelPathSafe(tt.path); got != tt.want {
				t.Fatalf("WorkspaceRelPathSafe(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
