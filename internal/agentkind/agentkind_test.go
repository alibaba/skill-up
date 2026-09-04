package agentkind

import "testing"

func TestVersionContract(t *testing.T) {
	t.Parallel()

	if !SupportsVersion(Codex) || SupportsVersion(QoderCLI) {
		t.Fatal("unexpected built-in version capabilities")
	}
	for _, version := range []string{"1.2.3", "v1.2.3", "1.2.3-beta.1+build.4"} {
		if !IsExactVersion(version) {
			t.Errorf("IsExactVersion(%q) = false, want true", version)
		}
	}
	for _, version := range []string{"latest", "^1.2.3", "1.2", "1.2.3.4", "01.2.3", "1.2.3-01", "1.2.3-foo..bar"} {
		if IsExactVersion(version) {
			t.Errorf("IsExactVersion(%q) = true, want false", version)
		}
	}
}
