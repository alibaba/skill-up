//go:build windows

package platform

import (
	"os"
	"os/exec"
)

// knownWindowsBashPaths lists the default Git Bash install locations checked
// after BashEnvOverride and PATH.
//
// WSL's C:\Windows\System32\bash.exe is intentionally excluded: it expects
// Linux-format paths (/mnt/c/...), so script-judge commands built from
// Windows host paths would fail with file-not-found even though discovery
// succeeded. Users who want to drive skill-up through WSL bash can point
// SKILL_UP_BASH at it explicitly after arranging path translation upstream.
var knownWindowsBashPaths = []string{
	`C:\Program Files\Git\bin\bash.exe`,
	`C:\Program Files (x86)\Git\bin\bash.exe`,
}

// DiscoverBash locates a bash interpreter on Windows. It checks, in order:
// the SKILL_UP_BASH override, PATH, then well-known Git Bash / WSL locations.
func DiscoverBash() (string, bool) {
	if v := os.Getenv(BashEnvOverride); v != "" {
		if isRegularFile(v) {
			return v, true
		}
	}
	if p, err := exec.LookPath("bash"); err == nil {
		return p, true
	}
	for _, p := range knownWindowsBashPaths {
		if isRegularFile(p) {
			return p, true
		}
	}
	return "", false
}

func isRegularFile(p string) bool {
	//nolint:gosec // p is a known bash install location or a user-supplied override
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
