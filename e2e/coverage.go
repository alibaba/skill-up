//go:build e2e

package e2e

import "testing"

// Cover is a no-op marker kept for test files that still call it. The
// historical coverage report tooling lives outside this branch.
func Cover(t *testing.T, cmd string) {
	t.Helper()
	_ = cmd
}
