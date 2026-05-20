//go:build windows

package platform

import (
	"context"
	"os/exec"
	"syscall"
)

// NewShellCmd builds an *exec.Cmd that runs command through the host shell.
// The caller is responsible for setting Dir, Env, and the output streams.
//
// On Windows we prefer a discoverable bash (Git Bash via DiscoverBash) and
// fall back to cmd.exe when none is available. Choosing bash whenever
// possible keeps the many internal POSIX command strings — agent CLI
// templates with single quotes, `set -eu` git fixtures, workspace-diff
// pipelines using `if ...; then` — working on a Windows host, and gives
// percent-sign-bearing inputs the bash double-quote literal semantics (cmd
// would otherwise expand `%VAR%` mid-argument). The cmd fallback path uses
// SysProcAttr.CmdLine so cmd's outer-quote stripping leaves embedded quoted
// paths intact, and `cmd /d /c` disables HKLM/HKCU AutoRun so a host's
// `cmd.exe AutoRun` registry value cannot prepend commands to every
// evaluator invocation.
func NewShellCmd(ctx context.Context, command string) *exec.Cmd {
	if bash, ok := DiscoverBash(); ok {
		return exec.CommandContext(ctx, bash, "-c", command)
	}
	cmd := exec.CommandContext(ctx, "cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /d /c "` + command + `"`}
	return cmd
}
