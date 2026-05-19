package evaluator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/internal/shellquote"
)

// FixtureUploader uploads fixture files into the runtime environment.
type FixtureUploader interface {
	Upload(ctx context.Context, rt runtime.Runtime, caseCfg *config.CaseConfig, skillDir, fixtureBaseDir string) error
}

type repoFixtureUploader struct{}

func (r *repoFixtureUploader) Upload(ctx context.Context, rt runtime.Runtime, caseCfg *config.CaseConfig, skillDir, fixtureBaseDir string) error {
	if caseCfg.Context.RepoFixture == "" {
		return nil
	}

	fixtureSource := filepath.Join(fixtureBaseDir, caseCfg.Context.RepoFixture)
	if _, err := os.Stat(fixtureSource); err != nil {
		return fmt.Errorf("repo_fixture %q not found: %w", fixtureSource, err)
	}

	if err := rt.UploadDir(ctx, fixtureSource, "."); err != nil {
		return fmt.Errorf("failed to upload repo_fixture: %w", err)
	}
	logging.DebugContextf(ctx, "Uploaded repo_fixture %s to workspace root", caseCfg.Context.RepoFixture)
	return nil
}

type contextFilesUploader struct{}

func (c *contextFilesUploader) Upload(ctx context.Context, rt runtime.Runtime, caseCfg *config.CaseConfig, _ string, _ string) error {
	if len(caseCfg.Context.Files) == 0 {
		return nil
	}

	paths := make([]string, 0, len(caseCfg.Context.Files))
	for path := range caseCfg.Context.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	tmp, err := os.CreateTemp("", "skill-up-context-file-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary context file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	for _, targetPath := range paths {
		if err := validateContextFilePath(targetPath); err != nil {
			return err
		}
		if err := uploadContextFile(ctx, rt, tmp, targetPath, caseCfg.Context.Files[targetPath]); err != nil {
			return err
		}
	}

	logging.DebugContextf(ctx, "Uploaded %d context files to workspace", len(paths))
	return nil
}

func uploadContextFile(ctx context.Context, rt runtime.Runtime, tmp *os.File, targetPath, content string) error {
	tmpPath := tmp.Name()

	if err := tmp.Truncate(0); err != nil {
		return fmt.Errorf("failed to reset context file %q: %w", targetPath, err)
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek context file %q: %w", targetPath, err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		return fmt.Errorf("failed to write context file %q: %w", targetPath, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("failed to flush context file %q: %w", targetPath, err)
	}

	if err := rt.UploadFile(ctx, tmpPath, targetPath); err != nil {
		return fmt.Errorf("failed to upload context file %q: %w", targetPath, err)
	}
	return nil
}

func validateContextFilePath(path string) error {
	clean := filepath.Clean(path)
	if path == "" || clean == "." {
		return fmt.Errorf("context file path %q is empty or targets workspace root", path)
	}
	if filepath.IsAbs(clean) {
		return fmt.Errorf("context file path %q must be relative", path)
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("context file path %q escapes workspace", path)
	}
	return nil
}

type gitInitUploader struct{}

func (g *gitInitUploader) Upload(ctx context.Context, rt runtime.Runtime, caseCfg *config.CaseConfig, _ string, _ string) error {
	if caseCfg.Context.Git == nil || !caseCfg.Context.Git.Init {
		return nil
	}

	var script strings.Builder
	script.WriteString("set -eu\n")
	script.WriteString("git init -q\n")
	script.WriteString("git config user.name skill-up\n")
	script.WriteString("git config user.email skill-up@example.invalid\n")

	for _, remote := range caseCfg.Context.Git.Remotes {
		if err := validateGitRemote(remote); err != nil {
			return err
		}
		fmt.Fprintf(&script, "git remote add %s %s\n",
			shellquote.Quote(remote.Name), shellquote.Quote(remote.URL))
	}

	result, err := rt.Exec(ctx, script.String(), runtime.ExecOptions{
		Cwd: rt.Workspace(),
	})
	if err != nil {
		return fmt.Errorf("git init failed: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("git init exited with code %d: %s", result.ExitCode, result.Stderr)
	}

	logging.DebugContextf(ctx, "Initialized git repo in workspace")
	return nil
}

type gitCheckoutUploader struct{}

func (g *gitCheckoutUploader) Upload(ctx context.Context, rt runtime.Runtime, caseCfg *config.CaseConfig, _ string, _ string) error {
	if caseCfg.Context.Git == nil || caseCfg.Context.Git.Checkout == "" {
		return nil
	}

	branch := caseCfg.Context.Git.Checkout
	if err := validateGitBranch(branch); err != nil {
		return err
	}

	// Switch to the branch if it exists. On a freshly initialized repo with no
	// commits (unborn HEAD) the branch cannot exist yet, so create it. But if
	// the repo already has commits (e.g. from a repo_fixture with its own
	// .git) and the branch is missing, fail loudly: silently creating it from
	// the wrong HEAD would mask a typo and run branch-dependent evals against
	// the wrong revision. `git switch` is branch-only, so a same-named file can
	// never make it silently revert a path instead of changing branch.
	//
	// The branch name is single-quoted via shellquote for the `git switch`
	// invocations and is never interpolated into the error message (a
	// double-quoted echo would re-evaluate command substitutions); the Go
	// error below already reports the branch name safely via %q.
	quoted := shellquote.Quote(branch)
	script := fmt.Sprintf("set -eu\n"+
		"if git switch %[1]s 2>/dev/null; then :\n"+
		"elif ! git rev-parse --verify --quiet HEAD >/dev/null 2>&1; then git switch -c %[1]s\n"+
		"else echo 'configured branch does not exist in the fixture repo' >&2; exit 1\n"+
		"fi\n", quoted)

	result, err := rt.Exec(ctx, script, runtime.ExecOptions{
		Cwd: rt.Workspace(),
	})
	if err != nil {
		return fmt.Errorf("git checkout %q failed: %w", branch, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("git checkout %q exited with code %d: %s", branch, result.ExitCode, result.Stderr)
	}

	logging.DebugContextf(ctx, "Checked out git branch %s", branch)
	return nil
}

// validateGitBranch rejects branch names that would be unsafe to pass to
// `git switch`, even after shell quoting (control characters git itself
// rejects, or a leading `-` that git would treat as an option).
func validateGitBranch(branch string) error {
	if strings.ContainsAny(branch, "\n\r\x00\t ") {
		return fmt.Errorf("git checkout branch %q contains whitespace or control characters", branch)
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("git checkout branch %q must not start with '-'", branch)
	}
	return nil
}

type applyDiffUploader struct{}

func (a *applyDiffUploader) Upload(ctx context.Context, rt runtime.Runtime, caseCfg *config.CaseConfig, skillDir, fixtureBaseDir string) error {
	if caseCfg.Context.Git == nil || caseCfg.Context.Git.ApplyDiff == "" {
		return nil
	}

	diffSource := filepath.Join(skillDir, caseCfg.Context.Git.ApplyDiff)
	if _, err := os.Stat(diffSource); err != nil {
		return fmt.Errorf("apply_diff %q not found: %w", diffSource, err)
	}

	tmpPath := filepath.Join(os.TempDir(), filepath.Base(diffSource))
	if err := rt.UploadFile(ctx, diffSource, tmpPath); err != nil {
		return fmt.Errorf("failed to upload diff file: %w", err)
	}

	// Quote the tmp path and use `--` to stop git from treating the path as an option.
	result, err := rt.Exec(ctx, "git apply -- "+shellquote.Quote(tmpPath), runtime.ExecOptions{
		Cwd: rt.Workspace(),
	})
	if err != nil {
		return fmt.Errorf("failed to apply diff: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("git apply failed with exit code %d: %s", result.ExitCode, result.Stderr)
	}

	logging.DebugContextf(ctx, "Applied diff %s", caseCfg.Context.Git.ApplyDiff)
	return nil
}

// validateGitRemote rejects remote entries whose Name or URL contain characters
// that would be unsafe to pass to `git remote add`, even after shell quoting
// (e.g. control characters that git itself would reject, or a leading `-`
// that could be interpreted as a git option).
func validateGitRemote(remote config.GitRemote) error {
	if remote.Name == "" {
		return errors.New("git remote name must not be empty")
	}
	if strings.ContainsAny(remote.Name, "\n\r\x00\t ") {
		return fmt.Errorf("git remote name %q contains whitespace or control characters", remote.Name)
	}
	if strings.HasPrefix(remote.Name, "-") {
		return fmt.Errorf("git remote name %q must not start with '-'", remote.Name)
	}
	if strings.ContainsAny(remote.URL, "\n\r\x00") {
		return fmt.Errorf("git remote url for %q contains control characters", remote.Name)
	}
	if strings.HasPrefix(remote.URL, "-") {
		return fmt.Errorf("git remote url for %q must not start with '-'", remote.Name)
	}
	return nil
}

type fixtureRegistry struct {
	uploaders []FixtureUploader
}

func newFixtureRegistry() *fixtureRegistry {
	return &fixtureRegistry{
		// Order matters: the git repo must exist and be on the right branch
		// before inline context.files are written, so those files land on the
		// target branch as case overrides instead of being clobbered by (or
		// aborting) the branch switch. apply_diff runs last, on top of both.
		uploaders: []FixtureUploader{
			&repoFixtureUploader{},
			&gitInitUploader{},
			&gitCheckoutUploader{},
			&contextFilesUploader{},
			&applyDiffUploader{},
		},
	}
}

func (fr *fixtureRegistry) UploadAll(ctx context.Context, rt runtime.Runtime, caseCfg *config.CaseConfig, skillDir, fixtureBaseDir string) error {
	for _, uploader := range fr.uploaders {
		if err := uploader.Upload(ctx, rt, caseCfg, skillDir, fixtureBaseDir); err != nil {
			return err
		}
	}
	return nil
}
