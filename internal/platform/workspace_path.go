package platform

import (
	"path"
	"strings"
)

// IsWorkspaceRootPath reports whether rel addresses the workspace root itself:
// "", ".", "./", "/", "\", ".\" and any other path that cleans to "." or "/".
//
// Only root detection uses the separator-insensitive form. Callers pass every
// other path through unchanged, so a filename containing a backslash keeps its
// literal meaning on POSIX hosts.
func IsWorkspaceRootPath(rel string) bool {
	clean := path.Clean(strings.ReplaceAll(rel, `\`, "/"))
	return clean == "." || clean == "/"
}
