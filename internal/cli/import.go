package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/alibaba/skill-up/internal/config"
)

const (
	importDirMode         = 0o755
	importFileMode        = 0o600
	localPathSkillSource  = "local_path"
	currentDirectorySkill = "."
)

var importCmd = &cobra.Command{
	Use:   "import [path to evals.json]",
	Short: "Import Anthropic evals.json format to skill-up case.yaml files",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		evalsPath := args[0]

		data, err := os.ReadFile(evalsPath)
		if err != nil {
			return fmt.Errorf("failed to read evals.json: %w", err)
		}

		var evalsData anthropicEvals
		if err := json.Unmarshal(data, &evalsData); err != nil {
			return fmt.Errorf("failed to parse evals.json: %w", err)
		}

		if evalsData.SkillName == "" {
			return errors.New("evals.json missing required field: skill_name")
		}

		// Determine output directory
		outputDir, _ := cmd.Flags().GetString("output")
		if outputDir == "" {
			outputDir = filepath.Dir(evalsPath)
		}
		casesDir := filepath.Join(outputDir, "cases")
		if err := os.MkdirAll(casesDir, importDirMode); err != nil {
			return fmt.Errorf("failed to create cases directory: %w", err)
		}

		// Convert each eval entry to a CaseConfig
		caseIDs := make([]string, 0, len(evalsData.Evals))
		for _, eval := range evalsData.Evals {
			caseCfg := convertEvalToCase(eval, evalsData.SkillName)
			caseIDs = append(caseIDs, caseCfg.ID)

			// Marshal to YAML
			yamlData, err := yaml.Marshal(caseCfg)
			if err != nil {
				return fmt.Errorf("failed to marshal case %d: %w", eval.ID, err)
			}

			// Write to cases/{id}.yaml
			caseFileName := caseCfg.ID + ".yaml"
			casePath := filepath.Join(casesDir, caseFileName)
			if err := os.WriteFile(casePath, yamlData, importFileMode); err != nil {
				return fmt.Errorf("failed to write case file %s: %w", casePath, err)
			}

			fmt.Printf("Imported case %d -> %s\n", eval.ID, casePath) //nolint:forbidigo
		}

		// Generate or update eval.yaml
		evalCfg := generateEvalConfig(caseIDs, outputDir)
		evalYaml, err := yaml.Marshal(evalCfg)
		if err != nil {
			return fmt.Errorf("failed to marshal eval.yaml: %w", err)
		}

		evalYamlPath := filepath.Join(outputDir, "eval.yaml")
		if err := os.WriteFile(evalYamlPath, evalYaml, importFileMode); err != nil {
			return fmt.Errorf("failed to write eval.yaml: %w", err)
		}

		fmt.Printf("\nGenerated eval.yaml -> %s\n", evalYamlPath)      //nolint:forbidigo
		fmt.Printf("Total: %d cases imported\n", len(evalsData.Evals)) //nolint:forbidigo

		return nil
	},
}

func init() {
	importCmd.Flags().String("output", "", "Output directory (default: same as evals.json)")
}

// anthropicEvals represents the Anthropic evals.json format.
type anthropicEvals struct {
	SkillName string          `json:"skill_name"`
	Evals     []anthropicEval `json:"evals"`
}

type anthropicEval struct {
	ID             int      `json:"id"`
	Prompt         string   `json:"prompt"`
	ExpectedOutput string   `json:"expected_output"`
	Files          []string `json:"files"`
	Expectations   []string `json:"expectations"`
}

// convertEvalToCase converts an Anthropic eval entry to a skill-up CaseConfig.
func convertEvalToCase(eval anthropicEval, skillName string) *config.CaseConfig {
	caseID := importedCaseID(eval.ID)

	// Build files context
	files := make(map[string]string)
	for _, f := range eval.Files {
		files[f] = f // placeholder - actual file content would need to be read
	}

	// Since expectations are natural language, use agent_judge
	judgeCfg := config.JudgeConfig{
		Type:     "agent_judge",
		Model:    "anthropic/claude-sonnet-4-6",
		Criteria: eval.Expectations,
	}

	return &config.CaseConfig{
		ID:          caseID,
		Title:       fmt.Sprintf("%s - %d", skillName, eval.ID),
		Description: eval.ExpectedOutput,
		Tag:         "functional_test",
		Input: config.Input{
			Prompt: eval.Prompt,
		},
		Context: config.Context{
			Files: files,
		},
		Expect: config.Expect{},
		Judge:  judgeCfg,
	}
}

// generateEvalConfig creates a basic EvalConfig for imported cases.
// outputDir is the directory where cases were written, used to compute
// case file paths relative to the skill root directory.
func generateEvalConfig(caseIDs []string, outputDir string) *config.EvalConfig {
	cfg := config.DefaultEvalConfig()

	// Resolve the relative prefix from skill root to the output directory.
	// The loader resolves case paths relative to the skill root (where SKILL.md lives),
	// so we need to compute the relative path from skill root to the output directory.
	relPrefix := computeOutputRelPrefix(outputDir)

	caseFiles := make([]string, len(caseIDs))
	for i, caseID := range caseIDs {
		caseFiles[i] = filepath.Join(relPrefix, "cases", caseID+".yaml")
	}

	cfg.Cases.Files = caseFiles
	cfg.Skills = []config.SkillRef{
		{Source: localPathSkillSource, Path: currentDirectorySkill},
	}
	cfg.Judge.Type = "agent_judge"
	if cfg.Engine.Model.Provider != "" && cfg.Engine.Model.Name != "" {
		cfg.Judge.Model = cfg.Engine.Model.Provider + "/" + cfg.Engine.Model.Name
	}
	cfg.Judge.Criteria = []string{
		"Imported Anthropic eval cases define their own case-level criteria.",
	}
	cfg.Report.Formats = []string{"json", "html"}
	cfg.Report.Artifacts = []string{"transcript"}

	return cfg
}

func importedCaseID(evalID int) string {
	return fmt.Sprintf("case-%d", evalID)
}

// computeOutputRelPrefix determines the relative path prefix from the skill root
// to the output directory. It reuses config.FindSkillDir to walk upward from
// outputDir looking for SKILL.md. If found, it returns the relative path from
// skill root to outputDir; otherwise falls back to the basename of outputDir
// (e.g. "evals").
func computeOutputRelPrefix(outputDir string) string {
	absOut, err := filepath.Abs(outputDir)
	if err != nil {
		return filepath.Base(outputDir)
	}

	skillDir, fallback := config.FindSkillDir(absOut)
	if fallback {
		return filepath.Base(absOut)
	}

	rel, err := filepath.Rel(skillDir, absOut)
	if err != nil {
		return filepath.Base(absOut)
	}
	return rel
}
