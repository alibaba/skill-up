//go:build !windows

package platform

import (
	"context"
	"os/exec"

	"github.com/alibaba/skill-up/internal/shellquote"
)

// Host returns the descriptor of the shell NoneRuntime.Exec will use on the
// current host: how to launch a command, how to quote an argument for that
// shell, and any extra environment variables the shell needs to behave
// predictably.
//
// Centralizing all three lets callers (the script-judge planner especially)
// pick a quoter that matches the shell actually launched, without
// re-deriving the shell choice independently.
//
// On POSIX the shell is bash when discoverable, sh otherwise. POSIX
// single-quoting is correct in both cases.
func Host() HostShell {
	bash, hasBash := DiscoverBash()
	shell := "sh"
	if hasBash {
		shell = bash
	}
	return HostShell{
		Cmd: func(ctx context.Context, command string) *exec.Cmd {
			return exec.CommandContext(ctx, shell, "-c", command)
		},
		Quote:  shellquote.QuotePOSIX,
		IsBash: hasBash,
		Bash:   bash,
	}
}
