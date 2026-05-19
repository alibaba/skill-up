//go:build !windows

package platform

import (
	"os"
	"os/exec"
)

// DiscoverBash locates a bash interpreter on non-Windows hosts: the
// SKILL_UP_BASH override first, then PATH.
func DiscoverBash() (string, bool) {
	if v := os.Getenv(BashEnvOverride); v != "" {
		//nolint:gosec // v is an explicit user-supplied bash override path
		if info, err := os.Stat(v); err == nil && !info.IsDir() {
			return v, true
		}
	}
	p, err := exec.LookPath("bash")
	if err != nil {
		return "", false
	}
	return p, true
}
