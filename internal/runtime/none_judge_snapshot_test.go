package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type cancelingJudgeReader struct {
	cancel context.CancelFunc
	read   bool
}

func (r *cancelingJudgeReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	p[0] = 'x'
	r.cancel()
	return 1, nil
}

func TestJudgeSnapshotCopyStopsBetweenChunks(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	_, err := io.CopyBuffer(&out, judgeCancelReader{reader: &cancelingJudgeReader{cancel: cancel}, check: ctx.Err}, make([]byte, 32*1024))
	if !errors.Is(err, context.Canceled) || out.String() != "x" {
		t.Fatalf("copy error=%v output=%q", err, out.String())
	}
}

func TestJudgeSnapshotForksPreserveInputsAndIsolateWrites(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	input := filepath.Join(workspace, "result.txt")
	if err := os.WriteFile(input, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("result.txt", filepath.Join(workspace, "alias")); err != nil {
		t.Fatal(err)
	}
	rt, err := NewRuntime(Config{Type: "none", WorkspaceDir: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	noneRT, ok := rt.(*NoneRuntime)
	if !ok {
		t.Fatalf("runtime type = %T", rt)
	}
	snapshot, err := noneRT.CaptureJudgeSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close() //nolint:errcheck
	first, cleanupFirst, err := snapshot.Fork(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first.Workspace(), "result.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanupFirst()
	second, cleanupSecond, err := snapshot.Fork(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupSecond()
	assertOriginalJudgeInput(t, filepath.Join(second.Workspace(), "alias"))
	info, err := os.Stat(filepath.Join(second.Workspace(), "result.txt"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", info, err)
	}
	assertOriginalJudgeInput(t, input)
}

func assertOriginalJudgeInput(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatalf("judge input %s = %q, %v", path, data, err)
	}
}

func TestJudgeSnapshotRejectsExternalSymlink(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(workspace, "external")); err != nil {
		t.Fatal(err)
	}
	rt, err := NewRuntime(Config{Type: "none", WorkspaceDir: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	noneRT, ok := rt.(*NoneRuntime)
	if !ok {
		t.Fatalf("runtime type = %T", rt)
	}
	if snapshot, err := noneRT.CaptureJudgeSnapshot(context.Background()); err == nil {
		_ = snapshot.Close()
		t.Fatal("external symlink accepted")
	}
}

func TestJudgeSnapshotPreservesModificationTimesAndFreshness(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	buildDir := filepath.Join(workspace, "build")
	if err := os.Mkdir(buildDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Lexical copy order puts the output before its source: assigning copy-time
	// mtimes would make a previously fresh build output appear stale.
	paths := []string{"build/z-source", "build/a-output", "build", "."}
	base := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	want := make(map[string]time.Time)
	for i, rel := range paths {
		path := filepath.Join(workspace, rel)
		if i < 2 {
			if err := os.WriteFile(path, []byte("build input"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		modified := base.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		want[rel] = info.ModTime()
	}
	rt := &NoneRuntime{workspace: workspace, cfg: Config{Type: "none"}}
	snapshot, err := rt.CaptureJudgeSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close() //nolint:errcheck
	for range 2 {
		fork, cleanup, err := snapshot.Fork(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, rel := range paths {
			info, err := os.Stat(filepath.Join(fork.Workspace(), rel))
			if err != nil || !info.ModTime().Equal(want[rel]) {
				cleanup()
				t.Fatalf("fork mtime for %s: info=%v error=%v, want %v", rel, info, err, want[rel])
			}
		}
		if err := os.Chtimes(filepath.Join(fork.Workspace(), "build/z-source"), time.Now(), time.Now()); err != nil {
			cleanup()
			t.Fatal(err)
		}
		cleanup()
	}
}
