package cli

import (
	"testing"
)

// TestDebugCmd_SubcommandRegistration verifies that the debug command has
// exactly the expected subcommands registered (judge and report).
func TestDebugCmd_SubcommandRegistration(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		"judge":  false,
		"report": false,
	}

	for _, sub := range debugCmd.Commands() {
		name := sub.Name()
		if _, ok := want[name]; ok {
			want[name] = true
		} else {
			t.Errorf("unexpected subcommand registered: %q", name)
		}
	}

	for name, found := range want {
		if !found {
			t.Errorf("expected subcommand %q to be registered, but it was not", name)
		}
	}
}

// TestDebugCmd_HasUseLine verifies the Use field is set.
func TestDebugCmd_HasUseLine(t *testing.T) {
	t.Parallel()
	if debugCmd.Use == "" {
		t.Error("debugCmd.Use should not be empty")
	}
}

// TestDebugCmd_HasShortDescription verifies the Short description is set.
func TestDebugCmd_HasShortDescription(t *testing.T) {
	t.Parallel()
	if debugCmd.Short == "" {
		t.Error("debugCmd.Short should not be empty")
	}
}
