//go:build !windows

package shellquote

// Quote returns a representation of s safe for the host shell (POSIX).
func Quote(s string) string { return QuotePOSIX(s) }
