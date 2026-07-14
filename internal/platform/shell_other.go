//go:build !windows

package platform

import (
	"context"
	"os/exec"
	"runtime"
	"sync"
)

// Host returns the descriptor of the shell NoneRuntime.Exec will use on the
// current host: its target command language, how to launch a command, and any
// extra environment variables the shell needs to behave predictably.
//
// Centralizing these details lets callers select quoting from Target without
// re-deriving the shell choice independently.
//
// On POSIX the shell is bash when discoverable, sh otherwise. POSIX
// single-quoting is correct in both cases.
//
// The result is cached for the process lifetime; see shell_windows.go for
// the rationale.
var hostShell = sync.OnceValue(buildHostShell)

// Host returns the cached HostShell descriptor for the current POSIX host.
func Host() HostShell { return hostShell() }

func buildHostShell() HostShell {
	bash, hasBash := DiscoverBash()
	shell := "sh"
	if hasBash {
		shell = bash
	}
	return HostShell{
		Target: Shell{
			GOOS:     runtime.GOOS,
			Family:   ShellPOSIX,
			BashPath: bash,
		},
		Cmd: func(ctx context.Context, command string) *exec.Cmd {
			return exec.CommandContext(ctx, shell, "-c", command)
		},
	}
}
