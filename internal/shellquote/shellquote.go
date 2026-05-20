// Package shellquote quotes strings for safe inclusion in shell command lines.
package shellquote

import "strings"

// QuotePOSIX returns a POSIX shell single-quoted representation of s.
func QuotePOSIX(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// windowsQuoteTriggers are the characters that force QuoteWindows to wrap its
// argument: argv-splitting whitespace and quotes, plus the cmd.exe
// metacharacters and grouping characters, so a value remains intact whether
// the consuming shell is CommandLineToArgvW or cmd /c.
//
// `%` is included so values containing `%VAR%`-shaped tokens are at least
// flagged via quoting; note that cmd expands `%VAR%` even inside double
// quotes and there is no reliable command-line escape for it, so callers
// that route through cmd must avoid percent signs in user-controlled paths.
const windowsQuoteTriggers = " \t\n\v\"&|<>^()%"

// QuoteWindows returns a representation of s safe to pass as a single argument
// on a Windows command line, following the CommandLineToArgvW parsing rules:
// the argument is wrapped in double quotes; any run of backslashes immediately
// preceding a double quote (or the closing quote) is doubled; interior double
// quotes are escaped as \". A double-quoted value is also interpreted
// identically by bash, so the result is safe under both `cmd /c` and `bash -c`.
func QuoteWindows(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, windowsQuoteTriggers) {
		return s
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

// QuoteFor quotes s for the shell of the given GOOS: Windows rules for
// "windows", POSIX rules otherwise.
func QuoteFor(goos, s string) string {
	if goos == "windows" {
		return QuoteWindows(s)
	}
	return QuotePOSIX(s)
}
