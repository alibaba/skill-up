package platform

import "testing"

func TestCleanWorkspaceRelPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{"", "."},
		{".", "."},
		{"./", "."},
		{"/", "/"},
		{`\`, "/"},
		{`.\`, "."},
		{"dir/file.txt", "dir/file.txt"},
		{`dir\file.txt`, "dir/file.txt"},
		{"foo/../bar", "bar"},
	}
	for _, tt := range tests {
		if got := CleanWorkspaceRelPath(tt.in); got != tt.want {
			t.Errorf("CleanWorkspaceRelPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

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
