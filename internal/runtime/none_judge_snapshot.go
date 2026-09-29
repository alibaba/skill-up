package runtime

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

// JudgeSnapshot holds a frozen copy of a none runtime workspace.
type JudgeSnapshot struct {
	path string
	cfg  Config
}

// CaptureJudgeSnapshot freezes the complete workspace before any judge runs.
func (r *NoneRuntime) CaptureJudgeSnapshot(ctx context.Context) (*JudgeSnapshot, error) {
	dir, err := os.MkdirTemp("", "skill-up-judge-snapshot-*")
	if err != nil {
		return nil, err
	}
	if err := copyJudgeTree(ctx, r.workspace, dir); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("capture judge workspace: %w", err)
	}
	cfg := r.cfg
	cfg.Env = maps.Clone(r.cfg.Env)
	return &JudgeSnapshot{path: dir, cfg: cfg}, nil
}

// Fork returns an independent runtime and cleanup function for one judge.
func (s *JudgeSnapshot) Fork(ctx context.Context) (Runtime, func(), error) {
	dir, err := os.MkdirTemp("", "skill-up-judge-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
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
func (s *JudgeSnapshot) Close() error { return os.RemoveAll(s.path) }

func copyJudgeTree(ctx context.Context, source, target string) error {
	root, err := os.OpenRoot(target)
	if err != nil {
		return err
	}
	defer root.Close() //nolint:errcheck
	var directories []judgeDirectoryMode
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
				return nil // retain the private 0700 temporary-directory boundary
			}
			if err := os.MkdirAll(out, 0o700); err != nil {
				return err
			}
			directories = append(directories, judgeDirectoryMode{path: out, mode: info.Mode().Perm()})
			return nil
		case entry.Type()&os.ModeSymlink != 0:
			return copyJudgeSymlink(root, source, path, rel)
		case entry.Type().IsRegular():
			return copyJudgeFile(ctx, path, out, info.Mode().Perm())
		default:
			return fmt.Errorf("judge snapshot cannot copy special file: %s", path)
		}
	})
	if err != nil {
		return err
	}
	return restoreJudgeDirectoryModes(directories)
}

type judgeDirectoryMode struct {
	path string
	mode os.FileMode
}

func restoreJudgeDirectoryModes(directories []judgeDirectoryMode) error {
	for i := len(directories) - 1; i >= 0; i-- {
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
	resolved := filepath.Clean(filepath.Join(filepath.Dir(path), link))
	if filepath.IsAbs(link) || resolved != source && !strings.HasPrefix(resolved, source+string(filepath.Separator)) {
		return fmt.Errorf("judge snapshot rejects symlink outside workspace: %s", path)
	}
	return root.Symlink(link, rel)
}

func copyJudgeFile(ctx context.Context, source, target string, mode os.FileMode) error {
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
