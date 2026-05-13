// Package report — workspace.go manages the Anthropic-compatible iteration
// directory structure for evaluation outputs.
//
// Directory layout:
//
//	<skill-name>-workspace/
//	  iteration-<N>/
//	    benchmark.json
//	    benchmark.md
//	    report.html
//	    <case-id>/
//	      eval_metadata.json
//	      with_skill/
//	        outputs/
//	          response.md
//	        grading.json
//	      without_skill/          # optional, only when benchmark.enabled=true
//	        outputs/
//	          response.md
//	        grading.json
package report

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	dirPerm  = 0o755
	filePerm = 0o644
)

// validateCaseID checks that caseID does not contain path traversal sequences.
func validateCaseID(caseID string) error {
	cleaned := filepath.Clean(caseID)
	if filepath.IsAbs(cleaned) || strings.Contains(cleaned, "..") || strings.ContainsAny(cleaned, `/\`) {
		return fmt.Errorf("invalid case ID: %q", caseID)
	}
	return nil
}

// IterationWorkspace manages a single evaluation iteration's artifact output.
type IterationWorkspace struct {
	// RootDir is the <skill-name>-workspace directory.
	RootDir string

	// IterationNum is the iteration number (1-based).
	IterationNum int

	// SkillName is the name of the skill being evaluated.
	SkillName string
}

// NewIterationWorkspace creates a workspace for the given iteration number.
// iterNum must be >= 1.
// If outputDir is empty, defaults to "<skillName>-workspace" in the current directory.
func NewIterationWorkspace(outputDir, skillName string, iterNum int) (*IterationWorkspace, error) {
	if outputDir == "" {
		outputDir = skillName + "-workspace"
	}
	if iterNum < 1 {
		return nil, fmt.Errorf("iteration number must be >= 1, got %d", iterNum)
	}

	// Ensure root directory exists.
	if err := os.MkdirAll(outputDir, dirPerm); err != nil {
		return nil, fmt.Errorf("create workspace root %s: %w", outputDir, err)
	}

	return &IterationWorkspace{
		RootDir:      outputDir,
		IterationNum: iterNum,
		SkillName:    skillName,
	}, nil
}

// IterationDir returns the path to the current iteration directory.
func (w *IterationWorkspace) IterationDir() string {
	return filepath.Join(w.RootDir, fmt.Sprintf("iteration-%d", w.IterationNum))
}

// CaseDir returns the path to a case's directory.
func (w *IterationWorkspace) CaseDir(caseID string) string {
	return filepath.Join(w.IterationDir(), filepath.Clean(caseID))
}

// WithSkillDir returns the with_skill subdirectory for a case.
func (w *IterationWorkspace) WithSkillDir(caseID string) string {
	return filepath.Join(w.CaseDir(caseID), "with_skill")
}

// WithoutSkillDir returns the without_skill subdirectory for a case.
func (w *IterationWorkspace) WithoutSkillDir(caseID string) string {
	return filepath.Join(w.CaseDir(caseID), "without_skill")
}

// ConfigDir returns the directory for a given configuration ("with_skill" or "without_skill").
func (w *IterationWorkspace) ConfigDir(caseID, config string) string {
	return filepath.Join(w.CaseDir(caseID), config)
}

// EnsureDirs creates all necessary directory structure for the given case IDs.
func (w *IterationWorkspace) EnsureDirs(caseIDs []string) error {
	if err := w.ensureIterationDir(); err != nil {
		return err
	}
	for _, caseID := range caseIDs {
		if err := validateCaseID(caseID); err != nil {
			return err
		}
		if err := w.ensureWithSkillOutputs(caseID); err != nil {
			return err
		}
	}
	return nil
}

// EnsureDirsWithBaseline creates case directories including without_skill outputs.
func (w *IterationWorkspace) EnsureDirsWithBaseline(caseIDs []string) error {
	if err := w.EnsureDirs(caseIDs); err != nil {
		return err
	}
	for _, caseID := range caseIDs {
		if err := w.ensureWithoutSkillOutputs(caseID); err != nil {
			return err
		}
	}
	return nil
}

func (w *IterationWorkspace) ensureIterationDir() error {
	iterDir := w.IterationDir()
	if err := os.MkdirAll(iterDir, dirPerm); err != nil {
		return fmt.Errorf("create iteration dir: %w", err)
	}
	return nil
}

func (w *IterationWorkspace) ensureWithSkillOutputs(caseID string) error {
	wsOutputs := filepath.Join(w.WithSkillDir(caseID), "outputs")
	if err := os.MkdirAll(wsOutputs, dirPerm); err != nil {
		return fmt.Errorf("create with_skill outputs dir for %s: %w", caseID, err)
	}
	return nil
}

func (w *IterationWorkspace) ensureWithoutSkillOutputs(caseID string) error {
	wosOutputs := filepath.Join(w.WithoutSkillDir(caseID), "outputs")
	if err := os.MkdirAll(wosOutputs, dirPerm); err != nil {
		return fmt.Errorf("create without_skill outputs dir for %s: %w", caseID, err)
	}
	return nil
}

// WriteResponse writes the agent response to outputs/response.md.
// config is "with_skill" or "without_skill".
func (w *IterationWorkspace) WriteResponse(caseID, config, content string) error {
	if err := validateCaseID(caseID); err != nil {
		return err
	}
	dir := filepath.Join(w.ConfigDir(caseID, config), "outputs")
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create outputs dir: %w", err)
	}

	path := filepath.Join(dir, "response.md")
	if err := os.WriteFile(path, []byte(content+"\n"), filePerm); err != nil {
		return fmt.Errorf("write response %s: %w", path, err)
	}
	return nil
}

// WriteGrading writes grading.json to the config directory.
func (w *IterationWorkspace) WriteGrading(caseID, config string, grading *AnthropicGrading) error {
	if err := validateCaseID(caseID); err != nil {
		return err
	}
	path := filepath.Join(w.ConfigDir(caseID, config), "grading.json")
	return WriteGradingJSON(path, grading)
}

// WriteEvalMeta writes eval_metadata.json to the case directory.
func (w *IterationWorkspace) WriteEvalMeta(caseID string, meta *EvalMetadata) error {
	if err := validateCaseID(caseID); err != nil {
		return err
	}
	path := filepath.Join(w.CaseDir(caseID), "eval_metadata.json")
	return WriteEvalMetadata(path, meta)
}

// WriteBenchmark writes benchmark.json to the iteration directory.
func (w *IterationWorkspace) WriteBenchmark(bm *AnthropicBenchmark) error {
	path := filepath.Join(w.IterationDir(), "benchmark.json")
	return WriteAnthropicBenchmark(path, bm)
}

// WriteBenchmarkMD writes benchmark.md to the iteration directory.
func (w *IterationWorkspace) WriteBenchmarkMD(bm *AnthropicBenchmark) error {
	path := filepath.Join(w.IterationDir(), "benchmark.md")
	return WriteBenchmarkMarkdownFile(path, bm)
}

// WriteFile writes arbitrary content to a file in the iteration directory.
func (w *IterationWorkspace) WriteFile(relPath string, data []byte) error {
	// Prevent path traversal attacks.
	cleaned := filepath.Clean(relPath)
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return fmt.Errorf("invalid relative path: %s", relPath)
	}

	path := filepath.Join(w.IterationDir(), cleaned)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create dir for %s: %w", relPath, err)
	}
	if err := os.WriteFile(path, data, filePerm); err != nil {
		return fmt.Errorf("write file %s: %w", path, err)
	}
	return nil
}
