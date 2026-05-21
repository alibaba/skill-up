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
// evaluator invocation. `cmd /s` forces cmd to use the deterministic
// "strip the first and last quote" rule for the wrapping, regardless of
// how many inner quotes the command contains; without /s, certain shapes
// (notably "cmd /c <single-token-executable>") trigger cmd's "preserve
// quotes" branch and the inner command is misparsed.
func NewShellCmd(ctx context.Context, command string) *exec.Cmd {
	if bash, ok := DiscoverBash(); ok {
		return exec.CommandContext(ctx, bash, "-c", command)
	}
	cmd := exec.CommandContext(ctx, "cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /d /s /c "` + command + `"`}
	return cmd
}
