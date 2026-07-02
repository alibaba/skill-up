package config

import "testing"

func TestParseTemplateToken(t *testing.T) {
	type tc struct {
		name       string
		inner      string
		wantName   string
		wantDef    string
		wantErrMsg string
		wantHasDef bool
		wantHasErr bool
	}
	// `:-` is matched before `?`, so the "default wins"/"colon-dash anywhere
	// wins" cases assert that a `?` inside (or after the start of) a default
	// clause is kept verbatim rather than splitting an error form.
	tests := []tc{
		{name: "plain", inner: "FOO", wantName: "FOO"},
		{name: "default form", inner: "FOO:-bar", wantName: "FOO", wantDef: "bar", wantHasDef: true},
		{name: "empty default", inner: "FOO:-", wantName: "FOO", wantHasDef: true},
		{name: "error form", inner: "FOO?must set FOO", wantName: "FOO", wantErrMsg: "must set FOO", wantHasErr: true},
		{name: "error form empty message", inner: "FOO?", wantName: "FOO", wantHasErr: true},
		{name: "default wins over question mark", inner: "FOO:-a?b", wantName: "FOO", wantDef: "a?b", wantHasDef: true},
		{name: "colon-dash anywhere wins over question", inner: "FOO?a:-b", wantName: "FOO?a", wantDef: "b", wantHasDef: true},
		{name: "kwargs dotted name", inner: "kwargs.profile", wantName: "kwargs.profile"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseTemplateToken(tc.inner)
			if got.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tc.wantName)
			}
			if got.Default != tc.wantDef {
				t.Errorf("Default = %q, want %q", got.Default, tc.wantDef)
			}
			if got.ErrMsg != tc.wantErrMsg {
				t.Errorf("ErrMsg = %q, want %q", got.ErrMsg, tc.wantErrMsg)
			}
			if got.HasDefault != tc.wantHasDef {
				t.Errorf("HasDefault = %v, want %v", got.HasDefault, tc.wantHasDef)
			}
			if got.HasErrForm != tc.wantHasErr {
				t.Errorf("HasErrForm = %v, want %v", got.HasErrForm, tc.wantHasErr)
			}
		})
	}
}

func TestTemplateTokenRequiredErr(t *testing.T) {
	// An explicit message is used verbatim.
	if got := (TemplateToken{Name: "FOO", ErrMsg: "set FOO please"}).RequiredErr(); got == nil || got.Error() != "set FOO please" {
		t.Errorf("RequiredErr with message = %v, want %q", got, "set FOO please")
	}
	// A blank or whitespace-only message defaults to "<name> is required".
	for _, msg := range []string{"", "   "} {
		got := (TemplateToken{Name: "FOO", ErrMsg: msg}).RequiredErr()
		if got == nil || got.Error() != "FOO is required" {
			t.Errorf("RequiredErr(%q) = %v, want %q", msg, got, "FOO is required")
		}
	}
}
