package agent

import (
	"strings"
	"testing"
)

func TestSessionFileOwnership(t *testing.T) {
	t.Parallel()

	const workspace = "/tmp/work-a"
	// Beyond the 64 KiB the previous string search looked at, so a conflict that
	// only appears later still decides the ownership.
	padding := strings.Repeat("x", 70<<10)

	for _, tc := range []struct {
		name    string
		content string
		want    sessionOwnership
	}{
		{
			name:    "records this workspace",
			content: `{"cwd":"/tmp/work-a"}` + "\n",
			want:    sessionOwnerOurs,
		},
		{
			name:    "directories field counts as identity",
			content: `{"directories":["/tmp/work-a"]}` + "\n",
			want:    sessionOwnerOurs,
		},
		{
			// A tool argument that mentions the path is not the run's identity.
			name:    "nested tool input is ignored",
			content: `{"cwd":"/tmp/work-a","message":{"content":[{"input":{"cwd":"/tmp/other"}}]}}` + "\n",
			want:    sessionOwnerOurs,
		},
		{
			name:    "mixed workspaces are not ours",
			content: `{"cwd":"/tmp/work-a"}` + "\n" + `{"cwd":"/tmp/other"}` + "\n",
			want:    sessionOwnerOther,
		},
		{
			name:    "conflict beyond the old scan limit is still seen",
			content: `{"cwd":"/tmp/work-a","pad":"` + padding + `"}` + "\n" + `{"cwd":"/tmp/other"}` + "\n",
			want:    sessionOwnerOther,
		},
		{
			name:    "malformed line leaves ownership unknown",
			content: `{"cwd":"/tmp/work-a"}` + "\n" + "not json\n",
			want:    sessionOwnerUnknown,
		},
		{
			name:    "no identity at all",
			content: `{"type":"summary"}` + "\n",
			want:    sessionOwnerUnknown,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sessionFileOwnership([]byte(tc.content), workspace, sessionPathComparison{}); got != tc.want {
				t.Fatalf("sessionFileOwnership = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSameSessionWorkspace(t *testing.T) {
	t.Parallel()

	if !(sessionPathComparison{}).samePath("/tmp/work-a/", "/tmp/work-a") {
		t.Fatal("a trailing slash should compare equal after Clean")
	}
	if (sessionPathComparison{}).samePath("/tmp/work-b", "/tmp/work-a") {
		t.Fatal("different workspaces must not compare equal")
	}
	if !(sessionPathComparison{foldCase: true}).samePath("/TMP/Work-A", "/tmp/work-a") {
		t.Fatal("case-insensitive comparison should fold on Windows")
	}
	if (sessionPathComparison{}).samePath("/TMP/Work-A", "/tmp/work-a") {
		t.Fatal("case-sensitive comparison must not fold")
	}
}
