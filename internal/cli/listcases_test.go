package cli

import (
	"os"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
)

func TestListCasesCommand_ValidConfig(t *testing.T) {
	if _, err := os.Stat(testEvalPath); os.IsNotExist(err) {
		t.Skip("examples/code-stats/evals/eval.yaml not found")
	}

	t.Parallel()

	loader := config.NewLoader(testEvalPath)
	result, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if len(result.Cases) == 0 {
		t.Error("expected at least one case")
	}

	// Verify case structure
	for _, c := range result.Cases {
		if c.ID == "" {
			t.Error("case ID should not be empty")
		}
	}
}

func TestListCasesCommand_CaseFields(t *testing.T) {
	if _, err := os.Stat(testEvalPath); os.IsNotExist(err) {
		t.Skip("examples/code-stats/evals/eval.yaml not found")
	}

	t.Parallel()

	loader := config.NewLoader(testEvalPath)
	result, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if len(result.Cases) == 0 {
		t.Skip("no cases to test")
	}

	c := result.Cases[0]
	if c.ID == "" {
		t.Error("case ID should not be empty")
	}

	// Title can be empty, defaults to ID
	if c.Title == "" {
		t.Log("case title is empty (will default to ID)")
	}

	// Tag defaults to "functional_test" if empty
	if c.Tag == "" {
		t.Log("case tag is empty (will default to functional_test)")
	}

	// Input should have either Prompt or Turns
	if c.Input.Prompt == "" && len(c.Input.Turns) == 0 {
		t.Error("case input should have either Prompt or Turns")
	}
}

func TestListCasesCommand_NonexistentPath(t *testing.T) {
	t.Parallel()

	evalPath := "/nonexistent/path/eval.yaml"
	loader := config.NewLoader(evalPath)
	_, err := loader.LoadAll()
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}
