//go:build windows

package shellquote

// Quote returns a representation of s safe for the host shell (Windows).
func Quote(s string) string { return QuoteWindows(s) }
