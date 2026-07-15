//go:build windows

package platform

import (
	"context"
	"os/exec"
	"sync"
	"syscall"
)

// Host returns the descriptor of the shell NoneRuntime.Exec will use on the
// current Windows host.
//
// We prefer a discoverable bash (Git Bash via DiscoverBash) and fall back to
// cmd.exe when none is available. Choosing bash whenever possible keeps the
// many internal POSIX command strings -- agent CLI templates with single
// quotes, `set -eu` git fixtures, workspace-diff `if ...; then` pipelines --
// working on a Windows host. The bash leg also injects MSYS_NO_PATHCONV /
// MSYS2_ARG_CONV_EXCL so MSYS does not rewrite `/x`-shaped argv entries
// before they reach native Windows binaries like cmd.exe or powershell.
//
// The cmd fallback uses SysProcAttr.CmdLine with `cmd /d /s /c "<command>"`:
// `/d` disables HKLM/HKCU AutoRun and `/s` forces the deterministic
// strip-first-and-last-quote rule for the wrapping. Inside the wrapping the
// command must be CommandLineToArgvW-quoted, hence the QuoteWindows quoter.
// cmd.exe is not a POSIX shell, so bash-style command strings (the agent
// nvm/Node bootstrap) do not run natively on Windows under the cmd
// fallback; that remains a documented limitation in docs/guide/windows.md.
//
// The result is cached for the process lifetime: PATH / SKILL_UP_BASH are
// documented as read-once at startup (see BashEnvOverride), and the host's
// bash install does not change while skill-up is running. Caching avoids
// repeating LookPath/Stat work on every NoneRuntime.Exec.
var hostShell = sync.OnceValue(buildHostShell)

// Host returns the cached HostShell descriptor for the current Windows host.
func Host() HostShell { return hostShell() }

func buildHostShell() HostShell {
	bash, hasBash := DiscoverBash()
	if hasBash {
		return HostShell{
			Target: Shell{
				GOOS:     GOOSWindows,
				Family:   ShellPOSIX,
				BashPath: bash,
			},
			Cmd: func(ctx context.Context, command string) *exec.Cmd {
				return exec.CommandContext(ctx, bash, "-c", command)
			},
			Env: []string{
				"MSYS_NO_PATHCONV=1",
				"MSYS2_ARG_CONV_EXCL=*",
			},
		}
	}
	return HostShell{
		Target: Shell{GOOS: GOOSWindows, Family: ShellCmd},
		Cmd: func(ctx context.Context, command string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, "cmd")
			cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /d /s /c "` + command + `"`}
			return cmd
		},
	}
}
