package shellquote

import "testing"

func TestQuotePOSIX(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", "''"},
		{"plain", "'plain'"},
		{"with space", "'with space'"},
		{"it's", `'it'\''s'`},
	}
	for _, tt := range tests {
		if got := QuotePOSIX(tt.in); got != tt.want {
			t.Errorf("QuotePOSIX(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestQuoteWindows(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"empty", "", `""`},
		// QuoteWindows always wraps in double quotes so bash (which strips
		// unquoted backslashes) does not mangle paths like `C:\tmp\file`.
		{"plain", "plain", `"plain"`},
		{"backslash path no space", `C:\tmp\s.ps1`, `"C:\tmp\s.ps1"`},
		{"space", `C:\Program Files\s.exe`, `"C:\Program Files\s.exe"`},
		{"interior quote", `a"b`, `"a\"b"`},
		{"trailing backslash with space", `a b\`, `"a b\\"`},
		{"backslash before quote", `a\"b`, `"a\\\"b"`},
		{"cmd metachar", "a&b", `"a&b"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := QuoteWindows(tt.in); got != tt.want {
				t.Errorf("QuoteWindows(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestQuoteFor(t *testing.T) {
	if got := QuoteFor("windows", "a b"); got != `"a b"` {
		t.Errorf("QuoteFor(windows) = %q", got)
	}
	if got := QuoteFor("linux", "a b"); got != "'a b'" {
		t.Errorf("QuoteFor(linux) = %q", got)
	}
}
