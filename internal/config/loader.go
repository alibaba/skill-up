// Package config provides evaluation configuration loading and validation.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/alibaba/skill-up/internal/logging"
)

const (
	maxSkillDirSearchDepth = 10

	// evalsSubdir is the conventional subdirectory under the skill root
	// that contains evaluation definitions and case files.
	evalsSubdir = "evals"

	// casesSubdir is the subdirectory under evalsSubdir that holds
	// individual case YAML files.
	casesSubdir = "cases"

	// fixturesSubdir is the subdirectory (relative to eval.yaml's
	// directory) that holds fixture data for test cases.
	fixturesSubdir = "fixtures"
)

// Loader loads evaluation configuration from disk.
type Loader struct {
	evalPath string
	evalDir  string
	skillDir string
}

// NewLoader creates a new Loader for the given eval.yaml path.
// evalPath is normalized with filepath.Abs so EvalDir is absolute; otherwise a
// relative path like ./evals/eval.yaml yields Dir chains such as "." and breaks
// skill root resolution (e.g. InstallSkill target becomes ".qoder/skills/.").
func NewLoader(evalPath string) *Loader {
	absEvalPath := evalPath
	if abs, err := filepath.Abs(evalPath); err == nil {
		absEvalPath = abs
	}

	evalDir := filepath.Dir(absEvalPath)
	skillDir, fallback := FindSkillDir(evalDir)
	if fallback {
		logging.Warnf("SKILL.md not found within %d levels above %s; falling back to %s", maxSkillDirSearchDepth, evalDir, skillDir)
	}

	return &Loader{
		evalPath: absEvalPath,
		evalDir:  evalDir,
		skillDir: skillDir,
	}
}

// LoadEvalConfig loads the eval.yaml file.
func (l *Loader) LoadEvalConfig() (*EvalConfig, error) {
	data, err := os.ReadFile(l.evalPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read eval.yaml: %w", err)
	}

	var cfg EvalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse eval.yaml: %w", err)
	}
	cfg.Engine.Normalize()

	return &cfg, nil
}

// LoadCaseConfig loads a single case file.
func (l *Loader) LoadCaseConfig(casePath string) (*CaseConfig, error) {
	// casePath is resolved from the skill root so eval configs work the same
	// whether invoked from the skill directory or by passing evals/eval.yaml.
	fullPath := filepath.Join(l.skillDir, casePath)

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read case file %s: %w", casePath, err)
	}

	var cfg CaseConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse case file %s: %w", casePath, err)
	}

	// Use filename (without extension) as ID if not specified
	if cfg.ID == "" {
		cfg.ID = stripExt(filepath.Base(casePath))
	}

	return &cfg, nil
}

// LoadAllCases loads all case files referenced in the eval config.
func (l *Loader) LoadAllCases(eval *EvalConfig) ([]*CaseConfig, error) {
	cases := make([]*CaseConfig, 0, len(eval.Cases.Files))

	for _, caseFile := range eval.Cases.Files {
		cfg, err := l.LoadCaseConfig(caseFile)
		if err != nil {
			return nil, err
		}
		cases = append(cases, cfg)
	}

	return cases, nil
}

// LoadAll loads the eval config and all cases.
func (l *Loader) LoadAll() (*EvalResult, error) {
	eval, err := l.LoadEvalConfig()
	if err != nil {
		return nil, err
	}

	cases, err := l.LoadAllCases(eval)
	if err != nil {
		return nil, err
	}

	return &EvalResult{
		Eval:  eval,
		Cases: cases,
	}, nil
}

// EvalPath returns the eval.yaml path.
func (l *Loader) EvalPath() string {
	return l.evalPath
}

// EvalDir returns the directory containing eval.yaml.
func (l *Loader) EvalDir() string {
	return l.evalDir
}

// SkillDir returns the directory containing SKILL.md.
func (l *Loader) SkillDir() string {
	return l.skillDir
}

// CasesDir returns the path to the cases directory.
func (l *Loader) CasesDir() string {
	return filepath.Join(l.skillDir, evalsSubdir, casesSubdir)
}

// FixtureDir returns the path to the fixtures directory under the skill root.
func (l *Loader) FixtureDir() string {
	return filepath.Join(l.evalDir, fixturesSubdir)
}

func stripExt(filename string) string {
	ext := filepath.Ext(filename)
	if ext == "" {
		return filename
	}

	return filename[:len(filename)-len(ext)]
}

// FindSkillDir walks up from startDir looking for SKILL.md. It returns the
// directory containing SKILL.md and false on success. When SKILL.md is not
// found within maxSkillDirSearchDepth levels, it returns
// filepath.Dir(startDir) and true (fallback).
func FindSkillDir(startDir string) (string, bool) {
	dir := filepath.Clean(startDir)
	for depth := 0; depth <= maxSkillDirSearchDepth; depth++ {
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err == nil {
			return dir, false
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return filepath.Dir(startDir), true
}
