// Package cli — report_debug.go implements a debug subcommand for the report module.
//
// Usage:
//
//	skill-up debug report <input.json> --format json
//	skill-up debug report <input.json> --format junit
//	skill-up debug report <input.json> --format html
//
// This command reads a JSON file conforming to report.Input,
// generates a report in the chosen format, and writes it to the current directory.
//
// This is a debug tool for quick report module validation.
// It is intentionally decoupled from the main evaluation pipeline.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/report"
)

// ---------------------------------------------------------------------------
// Cobra command
// ---------------------------------------------------------------------------

var debugReportCmd = &cobra.Command{
	Use:   "report <input.json>",
	Short: "Generate report from an Input JSON file",
	Long: `Debug command for report module validation.

Reads a JSON file containing a complete Input structure,
generates a report in the chosen format, and writes it to the current directory.

Supported formats:
  - json:  machine-readable JSON (fact source)
  - junit: JUnit XML (CI gate)
  - html:  human-readable HTML summary

Output files:
  - json  → report.json
  - junit → report.xml
  - html  → report.html`,
	Args: cobra.ExactArgs(1),
	RunE: runReportDebug,
}

func init() {
	debugReportCmd.Flags().String("format", "json", "Report format: json, junit, html")
}

func runReportDebug(cmd *cobra.Command, args []string) error {
	inputPath := args[0]
	format, _ := cmd.Flags().GetString("format")

	// 1. Read and parse input JSON
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("read input file %q: %w", inputPath, err)
	}

	var input report.Input
	if err := json.Unmarshal(data, &input); err != nil {
		return fmt.Errorf("parse input JSON: %w", err)
	}

	// 2. Build reporter (output to current directory)
	r, outputPath, err := buildReporter(format, ".")
	if err != nil {
		return err
	}

	// 3. Generate report
	ctx := context.Background()
	if err := r.Write(ctx, input); err != nil {
		return fmt.Errorf("generate %s report: %w", format, err)
	}

	// 4. Print summary to stderr
	absPath, err := filepath.Abs(outputPath)
	if err != nil {
		absPath = outputPath
	}
	passRate := input.OverallPassRate()
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[report] %s format — %d cases (pass_rate: %.0f%%)\n",
		format, len(input.CaseResults), passRate*100)
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[output] %s\n", absPath)

	return nil
}
