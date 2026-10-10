package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/logging"
)

// noneJudgeSnapshot holds a frozen copy of a none runtime workspace.
type noneJudgeSnapshot struct {
	path string
	cfg  Config
}

// CaptureJudgeSnapshot freezes the complete workspace before any judge runs.
func (r *NoneRuntime) CaptureJudgeSnapshot(ctx context.Context) (JudgeSnapshot, error) {
	dir, err := os.MkdirTemp("", "skill-up-judge-snapshot-*")
	if err != nil {
		return nil, err
	}
	if err := copyJudgeTree(ctx, r.workspace, dir); err != nil {
		cleanupErr := removeJudgeTree(dir)
		return nil, fmt.Errorf("capture judge workspace: %w", errors.Join(err, cleanupErr))
	}
	cfg := r.cfg
	cfg.Env = maps.Clone(r.cfg.Env)
	return &noneJudgeSnapshot{path: dir, cfg: cfg}, nil
}

// Fork returns an independent runtime and cleanup function for one judge.
func (s *noneJudgeSnapshot) Fork(ctx context.Context) (Runtime, func(), error) {
	dir, err := os.MkdirTemp("", "skill-up-judge-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		if err := removeJudgeTree(dir); err != nil {
			logging.WarnContextf(ctx, "remove judge workspace: %v", err)
		}
	}
	if err := copyJudgeTree(ctx, s.path, dir); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("fork judge workspace: %w", err)
	}
	cfg := s.cfg
	cfg.Env = maps.Clone(s.cfg.Env)
	cfg.WorkspaceDir = dir
	rt, err := NewRuntime(cfg)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if err := rt.Create(ctx); err != nil {
		cleanup()
		return nil, nil, err
	}
	return rt, func() { _ = rt.Close(); cleanup() }, nil
}

// Close removes the frozen copy.
func (s *noneJudgeSnapshot) Close() error { return removeJudgeTree(s.path) }

// removeJudgeTree restores owner access before removing copied directories.
// WalkDir does not follow symlinks and visits each directory before reading it.
func removeJudgeTree(path string) error {
	err := filepath.WalkDir(path, func(dir string, entry os.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			//nolint:gosec // owner execute permission is required to traverse directories during removal
			return os.Chmod(dir, info.Mode().Perm()|0o700)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return os.RemoveAll(path)
}

func copyJudgeTree(ctx context.Context, source, target string) error {
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("resolve judge workspace root: %w", err)
	}
	source = resolved
	root, err := os.OpenRoot(target)
	if err != nil {
		return err
	}
	defer root.Close() //nolint:errcheck
	var directories []judgeDirectoryMetadata
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		out := filepath.Join(target, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			if rel == "." {
				directories = append(directories, judgeDirectoryMetadata{path: out, mode: 0o700, modified: info.ModTime()})
				return nil // retain the private 0700 temporary-directory boundary
			}
			if err := os.MkdirAll(out, 0o700); err != nil {
				return err
			}
			directories = append(directories, judgeDirectoryMetadata{path: out, mode: info.Mode().Perm(), modified: info.ModTime()})
			return nil
		case entry.Type()&os.ModeSymlink != 0:
			return copyJudgeSymlink(root, source, path, rel)
		case entry.Type().IsRegular():
			if entry.Name() == ".git" {
				return fmt.Errorf("judge snapshot rejects Git metadata pointer file: %s; use a standalone checkout with a .git directory", path)
			}
			return copyJudgeFile(ctx, path, out, info.Mode().Perm(), info.ModTime())
		default:
			return fmt.Errorf("judge snapshot cannot copy special file: %s", path)
		}
	})
	if err != nil {
		return err
	}
	return restoreJudgeDirectoryMetadata(directories)
}

type judgeDirectoryMetadata struct {
	path     string
	mode     os.FileMode
	modified time.Time
}

func restoreJudgeDirectoryMetadata(directories []judgeDirectoryMetadata) error {
	for i := len(directories) - 1; i >= 0; i-- {
		if err := os.Chtimes(directories[i].path, directories[i].modified, directories[i].modified); err != nil {
			return err
		}
		if err := os.Chmod(directories[i].path, directories[i].mode); err != nil {
			return err
		}
	}
	return nil
}

func copyJudgeSymlink(root *os.Root, source, path, rel string) error {
	link, err := os.Readlink(path)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("judge snapshot cannot resolve symlink %s: %w", path, err)
	}
	if filepath.IsAbs(link) || resolved != source && !strings.HasPrefix(resolved, source+string(filepath.Separator)) {
		return fmt.Errorf("judge snapshot rejects symlink outside workspace: %s", path)
	}
	return root.Symlink(link, rel)
}

func copyJudgeFile(ctx context.Context, source, target string, mode os.FileMode, modified time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()                                                         //nolint:errcheck
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode) //nolint:gosec // preserves source mode in a private temporary directory
	if err != nil {
		return err
	}
	if _, err := io.CopyBuffer(out, judgeCancelReader{reader: in, check: ctx.Err}, make([]byte, 32*1024)); err != nil {
		_ = out.Close()
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chtimes(target, modified, modified); err != nil {
		return err
	}
	return os.Chmod(target, mode)
}

type judgeCancelReader struct {
	reader io.Reader
	check  func() error
}

func (r judgeCancelReader) Read(p []byte) (int, error) {
	if err := r.check(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
