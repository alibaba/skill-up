//go:build unix

package runtime

import (
	"os/exec"
	"syscall"
	"time"
)

// noneExecKillGrace is how long a canceled process group is given to exit after
// SIGTERM before it is escalated to SIGKILL. It must stay comfortably below
// noneExecWaitDelay (2s) so the escalation fires — and the group is gone —
// before Exec's WaitDelay force-closes the stdio pipes and returns. One second
// is enough for a shell and its children to run their trap handlers, release
// locks, and exit, while still keeping a timed-out Exec well within its
// deadline.
const noneExecKillGrace = 1 * time.Second

// configureProcessGroup starts cmd in its own process group and overrides the
// context-cancellation handler to gracefully terminate the whole group, then
// escalate to a hard kill if it does not exit promptly. Without the group,
// exec.CommandContext only kills the direct child, so a timed-out command's
// descendants could keep running and mutate the workspace after the agent was
// supposed to have stopped.
//
// On cancellation the group is signaled with SIGTERM first, giving children a
// chance to release locks and clean up, then SIGKILL after noneExecKillGrace if
// the group has not exited. done must be closed by the caller once cmd.Wait has
// returned so the escalation timer can be stopped before the PID is recycled.
func configureProcessGroup(cmd *exec.Cmd, done <-chan struct{}) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pid := cmd.Process.Pid
		// The process is a group leader (Setpgid), so a negative PID signals
		// the whole group. Fall back to signaling just the process on failure.
		if err := signalGroupOrProcess(cmd, pid, syscall.SIGTERM); err != nil {
			return err
		}
		// Escalate to SIGKILL out of band so Cancel returns immediately and
		// os/exec can start its WaitDelay timer. The goroutine exits as soon as
		// the process is reaped (done closed), so it cannot signal a recycled
		// PID.
		go func() {
			timer := time.NewTimer(noneExecKillGrace)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
				_ = signalGroupOrProcess(cmd, pid, syscall.SIGKILL)
			}
		}()
		return nil
	}
}

// signalGroupOrProcess sends sig to the whole process group (negative PID),
// falling back to signaling just the process if the group signal fails.
func signalGroupOrProcess(cmd *exec.Cmd, pid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pid, sig); err != nil {
		return cmd.Process.Signal(sig)
	}
	return nil
}
