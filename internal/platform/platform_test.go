package platform

import (
	"context"
	"testing"
)

func TestNewShellCmd(t *testing.T) {
	cmd := NewShellCmd(context.Background(), "echo hi")
	if cmd == nil {
		t.Fatal("NewShellCmd returned nil")
	}
	if cmd.Path == "" {
		t.Fatal("NewShellCmd produced a command with no executable path")
	}
}
