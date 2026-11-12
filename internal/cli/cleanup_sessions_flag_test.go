package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestEvaluateOptionsCleanupSessionsExplicitFalse(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}
	cmd.Flags().Bool("no-delete", false, "")
	cmd.Flags().Bool("cleanup-sessions", false, "")
	cmd.Flags().String("output-dir", "", "")
	cmd.Flags().String("workspace", "", "")
	cmd.Flags().Int("iteration", 0, "")
	cmd.Flags().StringArray("format", nil, "")

	if err := cmd.Flags().Set("cleanup-sessions", "false"); err != nil {
		t.Fatalf("set cleanup-sessions=false: %v", err)
	}

	opts, err := evaluateOptionsFromFlags(cmd)
	if err != nil {
		t.Fatalf("evaluateOptionsFromFlags failed: %v", err)
	}
	if opts.CleanupSessions {
		t.Fatal("CleanupSessions = true, want false for an explicit --cleanup-sessions=false")
	}
	if !opts.CleanupSessionsSet {
		t.Fatal("CleanupSessionsSet = false, want true: an explicit false must override the config")
	}
}
