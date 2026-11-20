package platform

import "testing"

func TestIsWorkspaceRootPath(t *testing.T) {
	t.Parallel()

	for _, root := range []string{"", ".", "./", "/", `\`, `.\`, "foo/.."} {
		if !IsWorkspaceRootPath(root) {
			t.Errorf("IsWorkspaceRootPath(%q) = false, want true", root)
		}
	}
	for _, rel := range []string{"a.txt", "dir/file.txt", `dir\file.txt`, "..", "../x"} {
		if IsWorkspaceRootPath(rel) {
			t.Errorf("IsWorkspaceRootPath(%q) = true, want false", rel)
		}
	}
}
