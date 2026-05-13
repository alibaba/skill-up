package shellquote

import "strings"

// Quote returns a POSIX shell single-quoted representation of s.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
