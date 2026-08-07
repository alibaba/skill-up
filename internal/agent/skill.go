package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// ListSkillFiles returns a list of files to sync for a skill,
// applying include patterns followed by exclude patterns. The evals directory
// is always excluded because it belongs to the evaluation harness.
func ListSkillFiles(sourceDir string, include, exclude []string) ([]string, error) {
	if err := validateSkillFilePatterns(include); err != nil {
		return nil, err
	}
	if err := validateSkillFilePatterns(exclude); err != nil {
		return nil, err
	}

	selector := skillFileSelector{sourceDir: sourceDir, include: include, exclude: exclude}
	if err := filepath.Walk(sourceDir, selector.visit); err != nil {
		return nil, err
	}

	return selector.files, nil
}

type skillFileSelector struct {
	sourceDir string
	include   []string
	exclude   []string
	files     []string
}

func (s *skillFileSelector) visit(path string, info os.FileInfo, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	rel, err := filepath.Rel(s.sourceDir, path)
	if err != nil {
		return err
	}
	relSlash := filepath.ToSlash(rel)
	if relSlash == "evals" {
		return filepath.SkipDir
	}

	excluded, err := matchesSkillPatterns(s.exclude, relSlash)
	if err != nil {
		return err
	}
	if !excluded && info.IsDir() {
		excluded, err = matchesSkillDirectory(s.exclude, relSlash)
		if err != nil {
			return err
		}
	}
	if excluded {
		if info.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}
	if info.IsDir() {
		s.files = append(s.files, rel)
		return nil
	}

	included, err := s.isIncluded(relSlash)
	if err != nil {
		return err
	}
	if included {
		s.files = append(s.files, rel)
	}
	return nil
}

func (s *skillFileSelector) isIncluded(rel string) (bool, error) {
	if len(s.include) == 0 {
		return true, nil
	}
	return matchesSkillPatterns(s.include, rel)
}

func validateSkillFilePatterns(patterns []string) error {
	for _, pattern := range patterns {
		if !doublestar.ValidatePattern(pattern) {
			return fmt.Errorf("invalid skill file pattern %q", pattern)
		}
	}
	return nil
}

func matchesSkillPatterns(patterns []string, rel string) (bool, error) {
	for _, pattern := range patterns {
		matched, err := doublestar.Match(pattern, rel)
		if err != nil {
			return false, fmt.Errorf("invalid skill file pattern %q: %w", pattern, err)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func matchesSkillDirectory(patterns []string, rel string) (bool, error) {
	for _, pattern := range patterns {
		if !strings.HasSuffix(pattern, "/**") {
			continue
		}
		matched, err := doublestar.Match(strings.TrimSuffix(pattern, "/**"), rel)
		if err != nil {
			return false, fmt.Errorf("invalid skill file pattern %q: %w", pattern, err)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

// installSkill uploads a skill directory to the target path,
// applying its configured include and exclude patterns.
// target is relative to workspace, runtime handles path resolution.
func installSkill(ctx context.Context, rt Runtime, source, target string, include, exclude []string) error { // nolint: unparam // ctx required by interface
	files, err := ListSkillFiles(source, include, exclude)
	if err != nil {
		return err
	}

	for _, file := range files {
		srcPath := filepath.Join(source, file)
		info, err := os.Stat(srcPath)
		if err != nil {
			return err
		}

		if info.IsDir() {
			continue
		}

		relDstPath := filepath.Join(target, file)
		if err := rt.UploadFile(ctx, srcPath, relDstPath); err != nil {
			return err
		}
	}

	return nil
}
