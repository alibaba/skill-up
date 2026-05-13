package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/config"
)

var validateCmd = &cobra.Command{
	Use:   "validate [path to evals/eval.yaml]",
	Short: "Validate evaluation configuration and case files",
	Args:  usageOnError(cobra.MaximumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		evalPath, err := resolveEvalPath(cmd.Context(), args)
		if err != nil {
			return err
		}

		loader := config.NewLoader(evalPath)
		result, err := loader.LoadAll()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		validator := config.NewValidator()
		if err := validator.ValidateAll(result); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}

		fmt.Printf("✓ eval.yaml is valid (loaded %d case(s))\n", len(result.Cases)) //nolint:forbidigo

		return nil
	},
}
