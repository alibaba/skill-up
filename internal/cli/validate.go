package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/logging"
)

var validateCmd = &cobra.Command{
	Use:   "validate [path to evals/eval.yaml]",
	Short: "Validate evaluation configuration and case files, or lint a skill's content with --skill",
	Args:  usageOnError(cobra.MaximumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		skillDir, _ := cmd.Flags().GetString("skill")
		if skillDir != "" {
			return runSkillContentCheck(cmd, args, skillDir)
		}

		strict, _ := cmd.Flags().GetBool("strict")
		if strict {
			return errors.New("--strict only applies to the --skill content check; eval config validation already fails on any error")
		}

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

		return nil
	},
}

// runSkillContentCheck lints the skill rooted at skillDir: SKILL.md
// frontmatter, attachment paths the body cites that are missing, and
// attachment files on disk the body never cites. It is a source-level check:
// it validates the directory as authored and does not see what a run
// actually installs after skills.include/skills.exclude filtering.
func runSkillContentCheck(cmd *cobra.Command, args []string, skillDir string) error {
	if len(args) > 0 {
		return errors.New("pass either --skill <dir> or a path to eval.yaml, not both")
	}

	var warnings []string
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("skill check failed: %w", err)
		}
		// CheckSkillIntegrity stays silent for non-skill directories, but a
		// directory named explicitly via --skill must not fail silently.
		warnings = []string{skillDir + ": no SKILL.md found (not a skill directory)"}
	} else {
		warnings = config.CheckSkillIntegrity(skillDir)
	}

	for _, warning := range warnings {
		logging.Warnf("skill integrity: %s", warning)
	}

	strict, _ := cmd.Flags().GetBool("strict")
	switch {
	case strict && len(warnings) > 0:
		return fmt.Errorf("skill integrity check failed with %d warning(s); run without --strict to tolerate them", len(warnings))
	case len(warnings) > 0:
		logging.Warnf("skill integrity: %d issue(s) found; re-run with --strict to fail on them", len(warnings))
	default:
		fmt.Printf("✓ skill content check passed (%s)\n", skillDir) //nolint:forbidigo
	}

	return nil
}

func init() {
	validateCmd.Flags().String("skill", "", "Lint the content integrity of the skill rooted at this directory (SKILL.md frontmatter, cited attachment paths, uncited attachment files) instead of validating an eval config")
	validateCmd.Flags().Bool("strict", false, "Fail when the --skill content check reports warnings")
}
