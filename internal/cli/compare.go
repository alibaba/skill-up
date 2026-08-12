package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/spf13/cobra"

	comparepkg "github.com/alibaba/skill-up/internal/compare"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
)

var compareCmd = &cobra.Command{
	Use:   "compare <old-result.json> <new-result.json>",
	Short: "Compare two offline evaluation results",
	Args:  cobra.ExactArgs(2),
	RunE:  runCompare,
}

func init() {
	compareCmd.Flags().String("format", "text", "Output format: text, json")
	compareCmd.Flags().Bool("fail-on-regression", false, "Fail when any case regresses")
	compareCmd.Flags().Float64("max-token-increase-percent", 0, "Maximum allowed total token increase percentage")
}

func runCompare(cmd *cobra.Command, args []string) error {
	format, err := cmd.Flags().GetString("format")
	if err != nil {
		return fmt.Errorf("get format flag: %w", err)
	}
	if format != "text" && format != jsonFormat {
		return fmt.Errorf("unsupported compare format %q; supported formats: text, json", format)
	}

	oldInput, err := loadCompareInput("old", args[0])
	if err != nil {
		return err
	}
	newInput, err := loadCompareInput("new", args[1])
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

	result := comparepkg.Compare(oldInput, newInput, options)
	if format == jsonFormat {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		if err := encoder.Encode(result); err != nil {
			return fmt.Errorf("write JSON comparison: %w", err)
		}
	} else if _, err := fmt.Fprint(cmd.OutOrStdout(), comparepkg.RenderText(result)); err != nil {
		return fmt.Errorf("write text comparison: %w", err)
	}

	if !result.Gates.Passed {
		return fmt.Errorf("comparison gates failed: %v", result.Gates.Failures)
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

func loadCompareInput(role, path string) (report.Input, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return report.Input{}, fmt.Errorf("read %s result %q: %w", role, path, err)
	}

	var input report.Input
	if err := json.Unmarshal(data, &input); err != nil {
		return report.Input{}, fmt.Errorf("parse %s result %q: %w", role, path, err)
	}
	if err := validateCompareInput(input); err != nil {
		return report.Input{}, fmt.Errorf("validate %s result %q: %w", role, path, err)
	}
	return input, nil
}

func validateCompareInput(input report.Input) error {
	if input.SkillName == "" || input.SchemaVersion == "" || input.EngineName == "" || input.StartTime.IsZero() || input.EndTime.IsZero() {
		return errors.New("missing required result metadata")
	}
	primary := input.PrimaryCaseResults()
	if len(primary) == 0 {
		return errors.New("no primary case results")
	}
	for _, result := range input.CaseResults {
		if strings.TrimSpace(result.CaseID) == "" {
			return errors.New("case result has an empty case ID")
		}
		if !isCompareStatus(result.Status) {
			return fmt.Errorf("case %q has invalid status %q", result.CaseID, result.Status)
		}
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
