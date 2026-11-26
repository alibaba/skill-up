package evaluator

import (
	"path/filepath"
	"strings"
)

// outputPathWithinRoot reports whether target is lexically contained by root.
// Callers use this before removing or creating output directories so a
// malformed case ID or subpath cannot redirect filesystem operations outside
// the configured output directory.
func outputPathWithinRoot(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (e *defaultEvaluator) outputPathAllowed(target string) bool {
	// Some evaluator tests and internal callers use a caller-owned artifact
	// directory when no output directory was configured. Preserve that mode,
	// while enforcing containment whenever the evaluator has an output root.
	if e.outputDir == "" {
		return target != ""
	}
	return outputPathWithinRoot(e.outputDir, target)
}
