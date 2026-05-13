package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// ListSkillFiles returns a list of files to sync for a skill,
// excluding the evals directory and its contents.
func ListSkillFiles(sourceDir string) ([]string, error) {
	var files []string
	if err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if rel == "evals" || strings.HasPrefix(rel, "evals/") {
			if info.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}
		files = append(files, rel)

		return nil
	}); err != nil {
		return nil, err
	}

	return files, nil
}

// installSkill uploads a skill directory to the target path,
// excluding the evals directory and its contents.
// target is relative to workspace, runtime handles path resolution.
func installSkill(ctx context.Context, rt Runtime, source, target string) error { // nolint: unparam // ctx required by interface
	files, err := ListSkillFiles(source)
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
