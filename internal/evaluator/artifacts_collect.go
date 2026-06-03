package evaluator

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/runtime"
)

const (
	// collectArtifactsSubdir is the output subpath under
	// <output-dir>/<case>/<config>/outputs/ where glob-selected workspace files
	// are mirrored (relative paths preserved).
	collectArtifactsSubdir = "workspace"
	// collectArtifactsTimeout bounds the post-run artifact collection. The case
	// context may already be done (cancelled or DeadlineExceeded), so collection
	// runs on a detached context with its own deadline.
	collectArtifactsTimeout = 60 * time.Second
)

// resolveCollectArtifacts merges the eval-level default glob list with the
// per-case list, taking the union and de-duplicating while preserving order
// (defaults first, then case-specific additions).
func resolveCollectArtifacts(evalCfg *config.EvalConfig, caseCfg *config.CaseConfig) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(patterns []string) {
		for _, p := range patterns {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	if evalCfg != nil {
		add(evalCfg.Cases.Defaults.CollectArtifacts)
	}
	if caseCfg != nil {
		add(caseCfg.CollectArtifacts)
	}
	return out
}

// collectGlobArtifacts downloads workspace files matching the case's resolved
// collect_artifacts globs into the case output directory, preserving their
// relative paths. It is read-only with respect to the workspace (it only lists
// and downloads files, never mutates git state) and never fails the case:
// every error is logged as a warning. It is invoked unconditionally after the
// agent run so artifacts are captured even when the agent failed or timed out.
func (e *defaultEvaluator) collectGlobArtifacts(ctx context.Context, rt runtime.Runtime, configName string, caseCfg *config.CaseConfig) {
	patterns := resolveCollectArtifacts(e.evalCfg, caseCfg)
	if len(patterns) == 0 {
		return
	}
	if rt == nil || e.outputDir == "" || configName == "" || caseCfg.ID == "" {
		return
	}

	// The case context may already be cancelled or past its deadline (e.g. on
	// timeout). Detach so the listing/download still runs, with a fresh bound.
	dctx := context.WithoutCancel(ctx)
	dctx, cancel := context.WithTimeout(dctx, collectArtifactsTimeout)
	defer cancel()

	files, err := listWorkspaceFiles(dctx, rt)
	if err != nil {
		logging.WarnContextf(ctx, "Evaluator: collect_artifacts: failed to list workspace for case %s: %v", caseCfg.ID, err)
		return
	}

	targetRoot := filepath.Join(e.outputDir, caseCfg.ID, configName, "outputs", collectArtifactsSubdir)
	downloaded := 0
	for _, rel := range files {
		if !matchesAnyGlob(patterns, rel) {
			continue
		}
		target := filepath.Join(targetRoot, filepath.FromSlash(rel))
		if err := rt.DownloadFile(dctx, rel, target); err != nil {
			logging.WarnContextf(ctx, "Evaluator: collect_artifacts: failed to download %q for case %s: %v", rel, caseCfg.ID, err)
			continue
		}
		downloaded++
	}
	logging.DebugContextf(ctx, "Evaluator: collect_artifacts: case %s (%s) matched/downloaded %d file(s) from %d workspace file(s) into %s", caseCfg.ID, configName, downloaded, len(files), targetRoot)
}

// listWorkspaceFiles returns the workspace-relative paths (slash-separated, no
// leading "./") of every regular file in the runtime workspace. It uses a
// portable `find . -type f` so it works uniformly across the none, docker, and
// opensandbox runtimes.
func listWorkspaceFiles(ctx context.Context, rt runtime.Runtime) ([]string, error) {
	result, err := rt.Exec(ctx, "find . -type f", runtime.ExecOptions{Cwd: rt.Workspace()})
	if err != nil {
		return nil, err
	}
	var files []string
	for line := range strings.SplitSeq(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rel := strings.TrimPrefix(line, "./")
		if rel == "" {
			continue
		}
		files = append(files, rel)
	}
	return files, nil
}

// matchesAnyGlob reports whether rel (a slash-separated relative path) matches
// any of the doublestar glob patterns.
func matchesAnyGlob(patterns []string, rel string) bool {
	for _, p := range patterns {
		if ok, err := doublestar.Match(p, rel); err == nil && ok {
			return true
		}
	}
	return false
}
