package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
)

const testEvalPath = "../../examples/code-stats/evals/eval.yaml"

func TestValidateCommandChecksMockedFixtures(t *testing.T) {
	for _, scope := range []string{"eval", "case"} {
		for _, tc := range []struct {
			name      string
			fixture   string
			wantError bool
		}{
			{"missing", "", true},
			{"malformed YAML", "tools: [", true},
			{"wrong type", "tools: []", true},
			{"missing response", "tools: {lookup: {description: Find item}}", true},
			{"invalid schema", "tools: {lookup: {input_schema: 1, response: null}}", true},
			{"duplicate tool", "tools: {lookup: {response: null}}\ntool_responses: {lookup: ok}", true},
			{"legacy", "tool_responses: {lookup: {default: ok}}", false},
			{"explicit null", "tools: {lookup: {response: null}}", false},
		} {
			t.Run(scope+"/"+tc.name, func(t *testing.T) {
				root := t.TempDir()
				writeAutoModeSkill(t, root)
				evals := filepath.Join(root, "evals")
				if err := os.MkdirAll(evals, 0o755); err != nil {
					t.Fatal(err)
				}
				evalDoc := "schema_version: v1alpha1\nenvironment: {type: none}\nengine: {name: claude_code}\nskills: []\ncases: {files: [evals/case.yaml]}\njudge: {type: rule_based}\n"
				caseDoc := "id: smoke\ninput: {prompt: task}\n"
				mcpDoc := "mcp:\n  servers:\n    - name: shop\n      mode: mocked\n      config_ref: evals/mock.yaml\n"
				if scope == "eval" {
					evalDoc += mcpDoc
				} else {
					caseDoc += mcpDoc
				}
				files := map[string]string{"eval.yaml": evalDoc, "case.yaml": caseDoc}
				if tc.fixture != "" {
					files["mock.yaml"] = tc.fixture
				}
				for name, content := range files {
					if err := os.WriteFile(filepath.Join(evals, name), []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				err := validateCmd.RunE(newRunPhaseTestCommand(t), []string{filepath.Join(evals, "eval.yaml")})
				if (err != nil) != tc.wantError {
					t.Fatalf("error = %v, wantError = %t", err, tc.wantError)
				}
				if err != nil && (!strings.Contains(err.Error(), scope) || !strings.Contains(err.Error(), "shop")) {
					t.Fatalf("error must identify scope and server: %v", err)
				}
			})
		}
	}
}

func TestValidateMockedFixturesSkipsRealServers(t *testing.T) {
	result := &config.EvalResult{Eval: &config.EvalConfig{MCP: config.MCPConfig{Servers: []config.MCPServer{
		{Name: "real", Mode: "real", ConfigRef: "missing-real-config.yaml"},
		{Name: "filesystem", Mode: "mocked"},
	}}}}
	if err := validateMockedFixtures(t.TempDir(), result); err != nil {
		t.Fatal(err)
	}
}

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
