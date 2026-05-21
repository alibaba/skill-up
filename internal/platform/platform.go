// Package platform centralizes OS-conditional process, shell, and executable
// discovery so the rest of skill-up does not scatter runtime.GOOS branches
// across packages.
package platform

import (
	"context"
	"os/exec"
)

// BashEnvOverride is the environment variable a user may set to point at a
// specific bash interpreter, taking precedence over PATH and well-known
// install locations.
const BashEnvOverride = "SKILL_UP_BASH"

// GOOS-value constants that callers across packages compare against
// runtime.GOOS / Runtime.TargetGOOS(). Centralizing them avoids string
// literals duplicated across the OS-dispatch sites.
const (
	GOOSWindows = "windows"
	GOOSLinux   = "linux"
	GOOSDarwin  = "darwin"
)

// HostShell describes the shell that NoneRuntime.Exec will use on the
// current host. Callers that need to quote arguments for the same shell or
// inject extra environment variables it depends on should reach for Quote
// and Env rather than re-deriving them.
type HostShell struct {
	// Cmd builds an exec.Cmd configured to run `command` through the host
	// shell. The caller still sets Dir, Env (which must be merged with
	// HostShell.Env), Stdout, and Stderr.
	Cmd func(ctx context.Context, command string) *exec.Cmd
	// Quote returns the argument-quoting suitable for the host shell.
	Quote func(s string) string
	// Env lists extra environment variables the shell needs to behave
	// predictably (for example MSYS_NO_PATHCONV for Git Bash on Windows).
	// Callers append it to their own env list; nil means "no extras".
	Env []string
	// IsBash reports whether the chosen shell is bash. POSIX hosts are
	// always true; Windows is true only when DiscoverBash succeeded.
	IsBash bool
	// Bash is the discovered bash interpreter path, populated when IsBash
	// is true. Callers building "bash invokes bash" pipelines (the .sh
	// script-judge plan on Windows) read it from here so the choice of
	// bash binary matches what Cmd will launch.
	Bash string
}
