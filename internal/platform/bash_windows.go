//go:build windows

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
// the SKILL_UP_BASH override, PATH (excluding the WSL shim under System32),
// then well-known Git Bash locations.
func DiscoverBash() (string, bool) {
	if v := os.Getenv(BashEnvOverride); v != "" {
		if isRegularFile(v) && !isWSLBash(v) {
			return v, true
		}
	}
	if p, err := exec.LookPath("bash"); err == nil && !isWSLBash(p) {
		return p, true
	}
	for _, p := range knownWindowsBashPaths {
		if isRegularFile(p) {
			return p, true
		}
	}
	return "", false
}

// isWSLBash reports whether p is the WSL bash shim shipped with Windows. The
// shim lives under %SystemRoot%\System32, which is on PATH by default on every
// Windows host, so PATH-based discovery would otherwise prefer it. The shim
// expects Linux-format paths (`/mnt/<drive>/...`) and silently fails on the
// Windows host paths we pass through, so we treat it as "no bash found" and
// fall through to the known Git Bash locations.
func isWSLBash(p string) bool {
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	system32 := windowsSystem32Dir()
	if system32 == "" {
		return false
	}
	return strings.EqualFold(filepath.Dir(abs), system32)
}

func windowsSystem32Dir() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		return filepath.Join(root, "System32")
	}
	// Fall back to the convention; SystemRoot is set on every modern Windows.
	return `C:\Windows\System32`
}

func isRegularFile(p string) bool {
	//nolint:gosec // p is a known bash install location or a user-supplied override
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
