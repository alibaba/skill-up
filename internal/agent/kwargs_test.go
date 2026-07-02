package agent

import (
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/logging"
)

func TestEngineKwargBool(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		kw   map[string]string
		key  string
		want bool
	}{
		{name: "nil map", kw: nil, key: KwargBypassSandbox, want: false},
		{name: "missing key", kw: map[string]string{"other": "true"}, key: KwargBypassSandbox, want: false},
		{name: "true lowercase", kw: map[string]string{KwargBypassSandbox: "true"}, key: KwargBypassSandbox, want: true},
		{name: "True mixed case", kw: map[string]string{KwargBypassSandbox: "True"}, key: KwargBypassSandbox, want: true},
		{name: "TRUE upper", kw: map[string]string{KwargBypassSandbox: "TRUE"}, key: KwargBypassSandbox, want: true},
		{name: "numeric one", kw: map[string]string{KwargBypassSandbox: "1"}, key: KwargBypassSandbox, want: true},
		{name: "t short", kw: map[string]string{KwargBypassSandbox: "t"}, key: KwargBypassSandbox, want: true},
		{name: "false", kw: map[string]string{KwargBypassSandbox: "false"}, key: KwargBypassSandbox, want: false},
		{name: "zero", kw: map[string]string{KwargBypassSandbox: "0"}, key: KwargBypassSandbox, want: false},
		{name: "empty string", kw: map[string]string{KwargBypassSandbox: ""}, key: KwargBypassSandbox, want: false},
		{name: "whitespace trimmed", kw: map[string]string{KwargBypassSandbox: "  true  "}, key: KwargBypassSandbox, want: true},
		{name: "garbage", kw: map[string]string{KwargBypassSandbox: "garbage"}, key: KwargBypassSandbox, want: false},
		{name: "yes not accepted", kw: map[string]string{KwargBypassSandbox: "yes"}, key: KwargBypassSandbox, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := EngineKwargBool(tc.kw, tc.key); got != tc.want {
				t.Errorf("EngineKwargBool(%v, %q) = %v, want %v", tc.kw, tc.key, got, tc.want)
			}
		})
	}
}

func TestLogUnknownEngineKwargs(t *testing.T) {
	// Not parallel: captureStdout redirects package-level log output, and
	// logging verbosity is a process-global setting.
	logging.SetVerbosity(1)
	defer logging.SetVerbosity(0)

	cases := []struct {
		name       string
		engineName string
		kwargs     map[string]string
		wantLogs   []string // substrings that must appear
		notLogs    []string // substrings that must NOT appear
	}{
		{
			name:       "nil kwargs emits nothing",
			engineName: "codex",
			kwargs:     nil,
		},
		{
			name:       "empty kwargs emits nothing",
			engineName: "codex",
			kwargs:     map[string]string{},
		},
		{
			name:       "all known keys emit nothing",
			engineName: "codex",
			kwargs:     map[string]string{KwargBypassSandbox: "true"},
			notLogs:    []string{"not a recognised"},
		},
		{
			name:       "unknown key triggers DEBUG",
			engineName: "codex",
			kwargs:     map[string]string{"bypas_sandbox": "true"}, // typo: missing s
			wantLogs:   []string{`bypas_sandbox`, `codex`, "not a recognised"},
		},
		{
			name:       "mixed: known stays quiet, unknown logs",
			engineName: "claude_code",
			kwargs:     map[string]string{KwargBypassSandbox: "true", "extra_args": "--foo"},
			wantLogs:   []string{`extra_args`, `claude_code`, "not a recognised"},
			notLogs:    []string{`bypass_sandbox`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				logUnknownEngineKwargs(tc.engineName, tc.kwargs)
			})
			for _, want := range tc.wantLogs {
				if !strings.Contains(out, want) {
					t.Errorf("expected log to contain %q, got: %s", want, out)
				}
			}
			for _, no := range tc.notLogs {
				if strings.Contains(out, no) {
					t.Errorf("expected log NOT to contain %q, got: %s", no, out)
				}
			}
			if len(tc.wantLogs) == 0 && len(tc.notLogs) == 0 && out != "" {
				t.Errorf("expected no log output, got: %s", out)
			}
		})
	}
}
