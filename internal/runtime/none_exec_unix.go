//go:build unix

package runtime

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup starts cmd in its own process group and overrides the
// context-cancellation handler to kill the whole group. Without this,
// exec.CommandContext only kills the direct child, so a timed-out command's
// descendants could keep running and mutate the workspace after the agent was
// supposed to have stopped.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// The process is a group leader (Setpgid), so a negative PID signals
		// the whole group. Fall back to killing just the process on failure.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
