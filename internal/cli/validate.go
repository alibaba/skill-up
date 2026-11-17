package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/mcp"
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
		// engine.custom env resolution and validation are deferred by the
		// loader (the final engine name can be changed by --engine); run them
		// here so `validate` still checks the custom engine block.
		if err := config.ResolveCustomEngineConfig(result.Eval); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}
		if err := validateMockedFixtures(loader.SkillDir(), result); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}

		fmt.Printf("✓ eval.yaml is valid (loaded %d case(s))\n", len(result.Cases)) //nolint:forbidigo

		return nil
	},
}

func validateMockedFixtures(skillDir string, result *config.EvalResult) error {
	provisioner := mcp.Provisioner{SkillDir: skillDir}
	if err := provisioner.ValidateMockedFixtures(result.Eval.MCP); err != nil {
		return fmt.Errorf("eval: %w", err)
	}
	for _, c := range result.Cases {
		if err := provisioner.ValidateMockedFixtures(c.MCP); err != nil {
			return fmt.Errorf("case %s: %w", c.ID, err)
		}
	}
	return nil
}
