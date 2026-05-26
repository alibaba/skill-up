package shellquote

import "testing"

func TestQuote(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain", input: "hello", want: "'hello'"},
		{name: "spaces", input: "hello world", want: "'hello world'"},
		{name: "single quote", input: "can't", want: "'can'\\''t'"},
		{name: "empty", input: "", want: "''"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Quote(tt.input); got != tt.want {
				t.Fatalf("Quote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
