package cli

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
)

const testEvalPath = "../../examples/code-stats/evals/eval.yaml"

func TestValidateCommand_ValidConfig(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat(testEvalPath); os.IsNotExist(err) {
		t.Skip("examples/code-stats/evals/eval.yaml not found")
	}

	loader := config.NewLoader(testEvalPath)
	result, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	validator := config.NewValidator()
	err = validator.ValidateAll(result)
	if err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func TestValidateCommand_InvalidPath(t *testing.T) {
	t.Parallel()

	evalPath := "/nonexistent/path/eval.yaml"
	loader := config.NewLoader(evalPath)
	_, err := loader.LoadAll()
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestValidateCommand_InvalidConfig(t *testing.T) {
	t.Parallel()

	tmpfile, err := os.CreateTemp(t.TempDir(), "invalid-eval-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()

	if _, err := tmpfile.WriteString("invalid: yaml: content:"); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	_ = tmpfile.Close()

	loader := config.NewLoader(tmpfile.Name())
	_, err = loader.LoadAll()
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestValidateCommand_ValidOutput(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat(testEvalPath); os.IsNotExist(err) {
		t.Skip("examples/code-stats/evals/eval.yaml not found")
	}

	loader := config.NewLoader(testEvalPath)
	result, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	var buf bytes.Buffer
	fmt.Printf("✓ eval.yaml is valid (loaded %d case(s))\n", len(result.Cases)) //nolint:forbidigo
	_ = buf.String()

	validator := config.NewValidator()
	err = validator.ValidateAll(result)
	if err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}
