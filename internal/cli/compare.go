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

const jsonFormat = "json"

var compareCmd = &cobra.Command{
	Use:   "compare <baseline-result.json> <candidate-result.json>...",
	Short: "Compare two or more offline evaluation results",
	Long: `Compare existing results without running an Agent or Judge.

The first result is the baseline; every later result is compared against it.

Examples:
  skill-up compare baseline.json skill-a.json skill-b.json --format html --output compare.html
  skill-up compare old.json new.json --format html --output compare.html
  skill-up compare old.json new.json --format markdown --output compare.md
  skill-up compare old.json new.json --format json --fail-on-regression`,
	Args: cobra.MinimumNArgs(2),
	RunE: runCompare,
}

func init() {
	configureCompareFlags(compareCmd)
}

func configureCompareFlags(cmd *cobra.Command) {
	cmd.Flags().String("format", "text", "Output format: text, json, html, markdown")
	cmd.Flags().String("output", "", "Write report to a file instead of stdout")
	cmd.Flags().String("baseline-label", "Baseline", "Label for the old run")
	cmd.Flags().StringArray("candidate-label", nil, "Candidate labels in input order; repeat once per candidate, or omit for automatic labels")
	cmd.Flags().Bool("fail-on-regression", false, "Fail when any case regresses")
	cmd.Flags().Float64("max-token-increase-percent", 0, "Maximum allowed total token increase percentage")
}

func runCompare(cmd *cobra.Command, args []string) error {
	format, err := cmd.Flags().GetString("format")
	if err != nil {
		return fmt.Errorf("get format flag: %w", err)
	}
	switch format {
	case "text", jsonFormat, "html", "markdown":
	default:
		return fmt.Errorf("unsupported compare format %q; supported formats: text, json, html, markdown", format)
	}

	if len(args) < 2 {
		return errors.New("compare requires at least two result files")
	}
	runs, err := loadBuildSet(cmd, args)
	if err != nil {
		return err
	}

	failOnRegression, err := cmd.Flags().GetBool("fail-on-regression")
	if err != nil {
		return fmt.Errorf("get fail-on-regression flag: %w", err)
	}
	options := comparepkg.Options{FailOnRegression: failOnRegression}
	maxTokenIncreasePercent, err := maxTokenIncreasePercent(cmd)
	if err != nil {
		return err
	}
	if maxTokenIncreasePercent != nil {
		options.MaxTokenIncreasePercent = maxTokenIncreasePercent
	}

	result, err := comparepkg.BuildSet(runs, options)
	if err != nil {
		return err
	}
	data, err := comparepkg.RenderSet(result, format)
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

func loadBuildSet(cmd *cobra.Command, args []string) ([]comparepkg.RunInput, error) {
	baselineLabel, _ := cmd.Flags().GetString("baseline-label")
	candidateLabels, _ := cmd.Flags().GetStringArray("candidate-label")
	if len(candidateLabels) != 0 && len(candidateLabels) != len(args)-1 {
		return nil, fmt.Errorf("--candidate-label must be omitted or supplied once per candidate: got %d labels for %d candidates", len(candidateLabels), len(args)-1)
	}
	runs := make([]comparepkg.RunInput, 0, len(args))
	for i, path := range args {
		role, label := "old baseline", baselineLabel
		if i > 0 {
			role = fmt.Sprintf("new candidate %d", i)
			label = "Candidate"
			if len(args) > 2 {
				label = fmt.Sprintf("Candidate %d", i)
			}
			if len(candidateLabels) > 0 {
				label = candidateLabels[i-1]
			}
		}
		input, missing, err := loadCompareInput(role, path)
		if err != nil {
			return nil, err
		}
		runs = append(runs, comparepkg.RunInput{Source: comparepkg.Source{Label: label, Path: path}, Input: input, MissingMetrics: missing})
	}
	return runs, nil
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
