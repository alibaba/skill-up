package customengine

import (
	"slices"
	"strings"
)

// WorkspaceRelPathSafe reports whether p is a non-empty, workspace-relative
// path or glob: not absolute and with no ".." segment.
func WorkspaceRelPathSafe(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && !slices.Contains(strings.Split(p, "/"), "..")
}
