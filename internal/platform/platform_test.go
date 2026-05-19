package platform

import (
	"context"
	"errors"
	"testing"
)

func TestLookAgentBinary_Found(t *testing.T) {
	// "go" is on PATH wherever the test suite runs.
	p, err := LookAgentBinary("go")
	if err != nil {
		t.Fatalf("LookAgentBinary(go) failed: %v", err)
	}
	if p == "" {
		t.Fatal("LookAgentBinary(go) returned an empty path")
	}
}

func TestLookAgentBinary_NotFound(t *testing.T) {
	_, err := LookAgentBinary("skill-up-no-such-binary-xyz")
	if !errors.Is(err, ErrBinaryNotFound) {
		t.Fatalf("expected ErrBinaryNotFound, got: %v", err)
	}
}

func TestNewShellCmd(t *testing.T) {
	cmd := NewShellCmd(context.Background(), "echo hi")
	if cmd == nil {
		t.Fatal("NewShellCmd returned nil")
	}
	if cmd.Path == "" {
		t.Fatal("NewShellCmd produced a command with no executable path")
	}
}
