package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/alibaba/skill-up/internal/runtime"
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
	filteredExclude := exclude
	if _, localWorkspace := rt.(*runtime.NoneRuntime); localWorkspace {
		var alreadyInstalled bool
		var err error
		filteredExclude, alreadyInstalled, err = excludeInstallTarget(rt.Workspace(), source, target, exclude)
		if err != nil {
			return err
		}
		if alreadyInstalled {
			return nil
		}
	}
	files, err := ListSkillFiles(source, include, filteredExclude)
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

func excludeInstallTarget(workspace, source, target string, exclude []string) ([]string, bool, error) {
	sourcePath, err := filepath.Abs(filepath.Clean(source))
	if err != nil {
		return nil, false, fmt.Errorf("resolve skill source: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(sourcePath); resolveErr == nil {
		sourcePath = resolved
	}
	targetPath := filepath.Clean(target)
	if !filepath.IsAbs(targetPath) {
		workspacePath, resolveErr := filepath.EvalSymlinks(workspace)
		if resolveErr != nil {
			return nil, false, fmt.Errorf("resolve runtime workspace: %w", resolveErr)
		}
		targetPath = filepath.Join(workspacePath, targetPath)
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return nil, false, fmt.Errorf("resolve skill target: %w", err)
	}
	if resolved, resolveErr := resolvePotentialInstallPath(targetPath); resolveErr == nil {
		targetPath = resolved
	}
	if relTarget, found := relativePathByIdentity(sourcePath, targetPath); found {
		return installTargetExclusions(exclude, relTarget)
	}
	relTarget, err := filepath.Rel(sourcePath, targetPath)
	if err != nil {
		return nil, false, fmt.Errorf("resolve skill target relative to source: %w", err)
	}
	if filepath.IsAbs(relTarget) || relTarget == ".." || strings.HasPrefix(relTarget, ".."+string(filepath.Separator)) {
		return exclude, false, nil
	}
	return installTargetExclusions(exclude, relTarget)
}

func resolvePotentialInstallPath(path string) (string, error) {
	current := filepath.Clean(path)
	missing := make([]string, 0)
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return "", resolveErr
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		next := filepath.Dir(current)
		if next == current {
			return path, nil
		}
		missing = append(missing, filepath.Base(current))
		current = next
	}
}

func relativePathByIdentity(parent, child string) (string, bool) {
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return "", false
	}
	current := filepath.Clean(child)
	parts := make([]string, 0)
	for {
		if info, statErr := os.Stat(current); statErr == nil && os.SameFile(parentInfo, info) {
			for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
				parts[i], parts[j] = parts[j], parts[i]
			}
			if len(parts) == 0 {
				return ".", true
			}
			return filepath.Join(parts...), true
		}
		next := filepath.Dir(current)
		if next == current {
			return "", false
		}
		parts = append(parts, filepath.Base(current))
		current = next
	}
}

func installTargetExclusions(exclude []string, relTarget string) ([]string, bool, error) {
	if relTarget == "." {
		return exclude, true, nil
	}
	relTarget = filepath.ToSlash(relTarget)
	filtered := slices.Clone(exclude)
	return append(filtered, ".git", ".git/**", relTarget, relTarget+"/**"), false, nil
}
