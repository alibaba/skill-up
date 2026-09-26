package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/logging"
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

		fmt.Printf("✓ eval.yaml is valid (loaded %d case(s))\n", len(result.Cases)) //nolint:forbidigo

		// Once the eval config is valid, check the content-level integrity of
		// the surrounding skill (SKILL.md frontmatter and attachment paths it
		// cites). Findings are warnings so existing CI keeps its exit code;
		// --strict promotes them to a validation failure.
		strict, _ := cmd.Flags().GetBool("strict")
		warnings := config.CheckSkillIntegrity(loader.SkillDir())
		for _, warning := range warnings {
			logging.Warnf("skill integrity: %s", warning)
		}
		switch {
		case strict && len(warnings) > 0:
			return fmt.Errorf("skill integrity check failed with %d warning(s); run without --strict to tolerate them", len(warnings))
		case len(warnings) > 0:
			logging.Warnf("skill integrity: %d issue(s) found; re-run with --strict to fail validation on them", len(warnings))
		}

		return nil
	},
}

func init() {
	validateCmd.Flags().Bool("strict", false, "Fail validation when the surrounding skill has SKILL.md integrity warnings (missing/empty frontmatter fields, dangling references/, assets/, scripts/ paths)")
}
