package platform

import (
	"path"
	"strings"
)

// CleanWorkspaceRelPath returns the host-independent clean form of a
// workspace-relative path.
//
// Eval configs are authored once and may run on any host, so the path grammar
// must not depend on the host separator. Both '/' and '\' are treated as
// separators; this also makes Windows root spellings such as ".\" and "\" mean
// the same thing on Linux and macOS.
func CleanWorkspaceRelPath(rel string) string {
	return path.Clean(strings.ReplaceAll(rel, `\`, "/"))
}

// IsWorkspaceRootPath reports whether rel addresses the workspace root itself:
// "", ".", "./", "/", "\", ".\" and any other path that cleans to "." or "/".
func IsWorkspaceRootPath(rel string) bool {
	clean := CleanWorkspaceRelPath(rel)
	return clean == "." || clean == "/"
}
