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
// On Windows the shell is cmd.exe. The command is wrapped in a single outer
// pair of double quotes and passed verbatim via SysProcAttr.CmdLine: `cmd /c`
// strips exactly that outer pair, leaving the inner command — which may itself
// contain quoted paths — for cmd to parse. This bypasses Go's argv escaping,
// which otherwise mangles embedded quotes for cmd.exe.
//
// Note: cmd.exe is not a POSIX shell, so bash-style command strings (the agent
// nvm/Node bootstrap) do not run natively on Windows. That remains a
// documented limitation; the script judge composes cmd-compatible commands.
func NewShellCmd(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /c "` + command + `"`}
	return cmd
}
