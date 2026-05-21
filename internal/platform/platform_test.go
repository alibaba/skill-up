package platform

import (
	"context"
	"testing"
)

func TestHost(t *testing.T) {
	shell := Host()
	if shell.Cmd == nil {
		t.Fatal("Host returned a HostShell with nil Cmd")
	}
	if shell.Quote == nil {
		t.Fatal("Host returned a HostShell with nil Quote")
	}
	cmd := shell.Cmd(context.Background(), "echo hi")
	if cmd == nil {
		t.Fatal("HostShell.Cmd returned nil")
	}
	if cmd.Path == "" {
		t.Fatal("HostShell.Cmd produced a command with no executable path")
	}
	// On POSIX hosts bash should be discoverable (PATH has it); IsBash
	// being false would point at a misconfigured runner.
	if !shell.IsBash {
		t.Logf("note: HostShell.IsBash is false (no bash discovered); the cmd fallback is exercised")
	}
}
