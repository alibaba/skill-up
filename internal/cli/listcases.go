package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/config"
)

var listCasesCmd = &cobra.Command{
	Use:   "list-cases [path to evals/eval.yaml]",
	Short: "List evaluation cases referenced by the config",
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

		// Print cases as a table
		const minWidth = 4
		w := tabwriter.NewWriter(os.Stdout, 0, minWidth, 2, ' ', 0) //nolint:mnd
		_, _ = fmt.Fprintln(w, "ID\tTitle\tTag\tPrompt")

		for _, c := range result.Cases {
			title := c.Title
			if title == "" {
				title = c.ID
			}
			tag := c.Tag
			if tag == "" {
				tag = "functional_test"
			}
			const promptPreviewLen = 50
			promptPreview := c.Input.Prompt
			if len(promptPreview) > promptPreviewLen {
				promptPreview = promptPreview[:promptPreviewLen-3] + "..."
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.ID, title, tag, promptPreview)
		}

		_ = w.Flush()

		return nil
	},
}
