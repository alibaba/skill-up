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
//
// The source directory is resolved through symlinks first: filepath.Walk does
// not follow a symlinked root, which would otherwise silently select only the
// link node itself.
func ListSkillFiles(sourceDir string, include, exclude []string) ([]string, error) {
	if err := validateSkillFilePatterns(include); err != nil {
		return nil, err
	}
	if err := validateSkillFilePatterns(exclude); err != nil {
		return nil, err
	}

	resolvedDir, err := filepath.EvalSymlinks(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve skill source %s: %w", sourceDir, err)
	}

	selector := skillFileSelector{sourceDir: resolvedDir, include: include, exclude: exclude}
	if err := filepath.Walk(resolvedDir, selector.visit); err != nil {
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

	uploaded := 0
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
		uploaded++
	}

	// A skill install that uploads nothing is never intentional: it means the
	// source was empty, unreadable, or the include/exclude filters matched
	// nothing — and silently running with_skill without the skill invalidates
	// the evaluation. Fail loudly instead.
	if uploaded == 0 {
		return fmt.Errorf("skill source %s selected 0 files (include=%v exclude=%v): refusing to install an empty skill", source, include, exclude)
	}

	return nil
}
