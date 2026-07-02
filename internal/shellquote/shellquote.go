// Package shellquote quotes strings for safe inclusion in shell command lines.
package shellquote

import "strings"

// QuotePOSIX returns a POSIX shell single-quoted representation of s.
func QuotePOSIX(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// QuoteWindows returns a representation of s safe to pass as a single argument
// on a Windows command line, following the CommandLineToArgvW parsing rules:
// the argument is wrapped in double quotes; any run of backslashes immediately
// preceding a double quote (or the closing quote) is doubled; interior double
// quotes are escaped as \".
//
// The result is always wrapped, even when s has no whitespace or metacharacter
// triggers: when NoneRuntime.Exec routes through bash on Windows (the default
// when Git Bash is discoverable), an unquoted backslash-bearing path such as
// `C:\tmp\script.cmd` would have its backslashes stripped by bash and reach
// the downstream `cmd /c` / `powershell -File` as `C:tmpscript.cmd`. Wrapping
// in double quotes keeps backslashes literal under both bash and cmd, and is
// equally safe for CommandLineToArgvW consumers.
func QuoteWindows(s string) string {
	if s == "" {
		return `""`
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for i := range len(s) {
		switch c := s[i]; c {
		case '\\':
			backslashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteByte('"')
			backslashes = 0
		default:
			b.WriteString(strings.Repeat(`\`, backslashes))
			backslashes = 0
			b.WriteByte(c)
		}
	}
	// Double trailing backslashes so they do not escape the closing quote.
	b.WriteString(strings.Repeat(`\`, backslashes*2))
	b.WriteByte('"')
	return b.String()
}
