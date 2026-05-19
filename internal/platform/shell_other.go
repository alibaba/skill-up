//go:build !windows

package platform

import (
	"context"
	"os/exec"
)

// NewShellCmd builds an *exec.Cmd that runs command through the host shell.
// The caller is responsible for setting Dir, Env, and the output streams.
//
// On POSIX hosts the shell is bash when available, otherwise sh.
func NewShellCmd(ctx context.Context, command string) *exec.Cmd {
	shell := "sh"
	if bash, ok := DiscoverBash(); ok {
		shell = bash
	}
	return exec.CommandContext(ctx, shell, "-c", command)
}
