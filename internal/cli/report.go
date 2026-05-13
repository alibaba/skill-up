package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
)

var reportCmd = &cobra.Command{
	Use:   "report <path to result.json>",
	Short: "Generate reports from a previous evaluation run",
	Long: `Generate reports in various formats from a prior evaluation result.

This command reads an existing result.json file and generates reports
in the specified format(s). Useful for regenerating reports after an
evaluation has completed, or generating additional report formats.

Examples:
  skill-up report my-skill-workspace/iteration-1/result.json
  skill-up report my-skill-workspace/iteration-1/result.json --format junit --format html
  skill-up report my-skill-workspace/iteration-1/result.json --format json --output-dir ./reports`,
	Args: cobra.ExactArgs(1),
	RunE: runReport,
}

func init() {
	reportCmd.Flags().StringArray("format", nil, "Report format (json, junit, html). Can be specified multiple times. Default: json")
	reportCmd.Flags().String("output-dir", "", "Directory for report outputs. Default: same directory as result.json")
}

func runReport(cmd *cobra.Command, args []string) error {
	resultPath := args[0]

	// 1. Read result.json
	data, err := os.ReadFile(resultPath)
	if err != nil {
		return fmt.Errorf("read result file: %w", err)
	}

	var input report.Input
	if err := json.Unmarshal(data, &input); err != nil {
		return fmt.Errorf("parse result file: %w", err)
	}

	// 2. Get output configuration
	formats, _ := cmd.Flags().GetStringArray("format")
	if len(formats) == 0 {
		formats = []string{"json"}
	}

	outputDir, _ := cmd.Flags().GetString("output-dir")
	if outputDir == "" {
		outputDir = filepath.Dir(resultPath)
	}

	// Ensure the output directory exists.
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory %s: %w", outputDir, err)
	}

	// 3. Generate reports
	ctx := context.Background()

	for _, format := range formats {
		reporter, outputPath, err := buildReporter(format, outputDir)
		if err != nil {
			return err
		}

		if err := reporter.Write(ctx, input); err != nil {
			return fmt.Errorf("write %s report: %w", format, err)
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Generated %s report: %s\n", format, outputPath)
	}

	// 4. Print summary
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nPass rate: %.1f%% (%d/%d cases)\n",
		input.OverallPassRate()*100,
		countByStatus(&input, judge.StatusPass),
		len(input.CaseResults))

	return nil
}

// buildReporter creates a Reporter for the given format string.
func buildReporter(format, outputDir string) (report.Reporter, string, error) {
	switch format {
	case "json":
		path := filepath.Join(outputDir, "report.json")
		return &report.JSONReporter{OutputPath: path}, path, nil
	case "junit":
		path := filepath.Join(outputDir, "report.xml")
		return &report.JUnitReporter{OutputPath: path}, path, nil
	case "html":
		path := filepath.Join(outputDir, "report.html")
		return &report.HTMLReporter{OutputPath: path}, path, nil
	default:
		return nil, "", fmt.Errorf("unsupported report format %q (supported: json, junit, html)", format)
	}
}

func countByStatus(input *report.Input, status judge.Status) int {
	count := 0
	for _, c := range input.CaseResults {
		if c.Status == status {
			count++
		}
	}
	return count
}
