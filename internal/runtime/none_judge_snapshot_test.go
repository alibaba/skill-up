package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
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
