package agent

import "testing"

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
