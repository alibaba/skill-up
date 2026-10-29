package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	comparepkg "github.com/alibaba/skill-up/internal/compare"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
)

var compareCmd = &cobra.Command{
	Use:   "compare <old-result.json> <new-result.json>",
	Short: "Compare two offline evaluation results",
	Long: `Compare existing results without running an Agent or Judge.

Examples:
  skill-up compare old.json new.json --format html --output compare.html
  skill-up compare old.json new.json --format markdown --output compare.md
  skill-up compare old.json new.json --format json --fail-on-regression`,
	Args: cobra.ExactArgs(2),
	RunE: runCompare,
}

func init() {
	configureCompareFlags(compareCmd)
}

func configureCompareFlags(cmd *cobra.Command) {
	cmd.Flags().String("format", "text", "Output format: text, json, html, markdown")
	cmd.Flags().String("output", "", "Write report to a file instead of stdout")
	cmd.Flags().String("baseline-label", "Baseline", "Label for the old run")
	cmd.Flags().String("candidate-label", "Candidate", "Label for the new run")
	cmd.Flags().Bool("fail-on-regression", false, "Fail when any case regresses")
	cmd.Flags().Float64("max-token-increase-percent", 0, "Maximum allowed total token increase percentage")
}

func runCompare(cmd *cobra.Command, args []string) error {
	format, err := cmd.Flags().GetString("format")
	if err != nil {
		return fmt.Errorf("get format flag: %w", err)
	}
	switch format {
	case "text", "json", "html", "markdown":
	default:
		return fmt.Errorf("unsupported compare format %q; supported formats: text, json, html, markdown", format)
	}

	oldInput, oldMissing, err := loadCompareInput("old", args[0])
	if err != nil {
		return err
	}
	newInput, newMissing, err := loadCompareInput("new", args[1])
	if err != nil {
		return err
	}

	failOnRegression, err := cmd.Flags().GetBool("fail-on-regression")
	if err != nil {
		return fmt.Errorf("get fail-on-regression flag: %w", err)
	}
	options := comparepkg.Options{FailOnRegression: failOnRegression, OldMissingMetrics: oldMissing, NewMissingMetrics: newMissing}
	maxTokenIncreasePercent, err := maxTokenIncreasePercent(cmd)
	if err != nil {
		return err
	}
	if maxTokenIncreasePercent != nil {
		options.MaxTokenIncreasePercent = maxTokenIncreasePercent
	}

	result := comparepkg.Compare(oldInput, newInput, options)
	baselineLabel, _ := cmd.Flags().GetString("baseline-label")
	candidateLabel, _ := cmd.Flags().GetString("candidate-label")
	result.Baseline = comparepkg.Source{Label: baselineLabel, Path: args[0]}
	result.Candidate = comparepkg.Source{Label: candidateLabel, Path: args[1]}
	data, err := comparepkg.Render(result, format)
	if err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	if err := writeComparison(cmd, args, output, data); err != nil {
		return err
	}

	if !result.Gates.Passed {
		return fmt.Errorf("comparison gates failed: %v", result.Gates.Failures)
	}
	return nil
}

func writeComparison(cmd *cobra.Command, inputs []string, output string, data []byte) error {
	if output == "" {
		if _, err := cmd.OutOrStdout().Write(data); err != nil {
			return fmt.Errorf("write comparison: %w", err)
		}
		return nil
	}
	outPath, err := filepath.Abs(output)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}
	outInfo, statErr := os.Stat(output)
	if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect output: %w", statErr)
	}
	for _, input := range inputs {
		inPath, err := filepath.Abs(input)
		if err != nil {
			return fmt.Errorf("resolve input path: %w", err)
		}
		inInfo, err := os.Stat(input)
		if err != nil {
			return fmt.Errorf("inspect input: %w", err)
		}
		if outPath == inPath || (outInfo != nil && os.SameFile(outInfo, inInfo)) {
			return errors.New("comparison output must not overwrite a source result")
		}
	}
	if err := os.WriteFile(output, data, 0o600); err != nil {
		return fmt.Errorf("write comparison %q: %w", output, err)
	}
	return nil
}

func maxTokenIncreasePercent(cmd *cobra.Command) (*float64, error) {
	if !cmd.Flags().Changed("max-token-increase-percent") {
		return nil, nil
	}
	value, err := cmd.Flags().GetFloat64("max-token-increase-percent")
	if err != nil {
		return nil, fmt.Errorf("get max-token-increase-percent flag: %w", err)
	}
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, errors.New("max-token-increase-percent must be a finite non-negative number")
	}
	return &value, nil
}

func loadCompareInput(role, path string) (report.Input, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return report.Input{}, nil, fmt.Errorf("read %s result %q: %w", role, path, err)
	}

	var input report.Input
	if err := json.Unmarshal(data, &input); err != nil {
		return report.Input{}, nil, fmt.Errorf("parse %s result %q: %w", role, path, err)
	}
	if err := validateCompareInput(input); err != nil {
		return report.Input{}, nil, fmt.Errorf("validate %s result %q: %w", role, path, err)
	}
	return input, comparepkg.MissingMetrics(data, input), nil
}

func validateCompareInput(input report.Input) error {
	if input.SkillName == "" || input.SchemaVersion == "" || input.EngineName == "" || input.StartTime.IsZero() || input.EndTime.IsZero() {
		return errors.New("missing required result metadata")
	}
	if input.EndTime.Before(input.StartTime) || input.TotalTokens < 0 {
		return errors.New("invalid run duration or total tokens")
	}
	primary := input.PrimaryCaseResults()
	if len(primary) == 0 {
		return errors.New("no primary case results")
	}
	seen := make(map[[2]string]bool)
	for _, result := range input.CaseResults {
		key := [2]string{result.CaseID, result.Configuration}
		if seen[key] {
			return fmt.Errorf("duplicate case ID %q for configuration %q", result.CaseID, result.Configuration)
		}
		seen[key] = true
		if err := validateCompareCase(result); err != nil {
			return err
		}
	}
	return nil
}

func validateCompareCase(result report.CaseResult) error {
	if result.DurationMs < 0 || result.InputTokens < 0 || result.OutputTokens < 0 {
		return fmt.Errorf("case %q has negative metrics", result.CaseID)
	}
	if strings.TrimSpace(result.CaseID) == "" {
		return errors.New("case result has an empty case ID")
	}
	if !isCompareStatus(result.Status) {
		return fmt.Errorf("case %q has invalid status %q", result.CaseID, result.Status)
	}

	return nil
}

func isCompareStatus(status judge.Status) bool {
	switch status {
	case judge.StatusPass, judge.StatusFail, judge.StatusSkip, judge.StatusError:
		return true
	default:
		return false
	}
}
