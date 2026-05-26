package skillup

import "testing"

func TestExecuteDelegatesToCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := Execute("test-version", []string{"--version"}); err != nil {
		t.Fatalf("Execute --version returned error: %v", err)
	}
}
