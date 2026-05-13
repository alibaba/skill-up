// Package cli — debug.go defines the debug subcommand group.
//
// Debug commands are temporary tools for module-level validation during development.
// They are intentionally decoupled from the main evaluation pipeline.
//
// Usage:
//
//	skill-up debug judge <input.json>
//	skill-up debug report <input.json> --format json
package cli

import "github.com/spf13/cobra"

var debugCmd = &cobra.Command{
	Use:   "debug",
	Short: "Debug commands for module-level validation",
	Long: `Debug commands for quick module validation during development.

These commands are intentionally decoupled from the main evaluation pipeline
(runner, engine, etc.) and are useful for:
  - Testing judge configurations before running full evaluations
  - Validating report format outputs
  - Debugging specific evaluation scenarios

Note: These are temporary debug tools and may change or be removed in future versions.`,
}

func init() {
	debugCmd.AddCommand(debugJudgeCmd)
	debugCmd.AddCommand(debugReportCmd)
}
