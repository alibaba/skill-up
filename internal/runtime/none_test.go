package runtime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/alibaba/skill-up/internal/logging"
)

var logCaptureMu sync.Mutex

func TestNoneRuntime_CreateAndClose(t *testing.T) {
	t.Parallel()

	rt := &NoneRuntime{cfg: Config{Delete: true}}
	if rt.Workspace() != "" {
		t.Fatal("workspace should be empty before Create")
	}

	if err := rt.Create(context.Background()); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if rt.Workspace() == "" {
		t.Fatal("workspace should not be empty after Create")
	}
	if _, err := os.Stat(rt.Workspace()); err != nil {
		t.Fatalf("workspace dir should exist: %v", err)
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if _, err := os.Stat(rt.Workspace()); !os.IsNotExist(err) {
		t.Fatal("workspace dir should be removed after Close")
	}
}

func TestNoneRuntime_StartStop(t *testing.T) {
	t.Parallel()

	rt := &NoneRuntime{}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start should be no-op: %v", err)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop should be no-op: %v", err)
	}
}

func TestNoneRuntime_UploadDownloadFile(t *testing.T) {
	t.Parallel()

	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	// Create a source file
	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "input.txt")
	content := "hello world"
	if err := os.WriteFile(srcFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// Upload
	if err := rt.UploadFile(context.Background(), srcFile, "input.txt"); err != nil {
		t.Fatalf("UploadFile failed: %v", err)
	}

	// Verify uploaded
	uploaded := filepath.Join(rt.Workspace(), "input.txt")
	data, err := os.ReadFile(uploaded)
	if err != nil {
		t.Fatalf("uploaded file should exist: %v", err)
	}
	if string(data) != content {
		t.Errorf("uploaded content mismatch: got %q, want %q", string(data), content)
	}

	// Download to another location
	dlDir := t.TempDir()
	dlFile := filepath.Join(dlDir, "output.txt")
	if err := rt.DownloadFile(context.Background(), "input.txt", dlFile); err != nil {
		t.Fatalf("DownloadFile failed: %v", err)
	}

	dlData, err := os.ReadFile(dlFile)
	if err != nil {
		t.Fatalf("downloaded file should exist: %v", err)
	}
	if string(dlData) != content {
		t.Errorf("downloaded content mismatch: got %q, want %q", string(dlData), content)
	}
}

func TestNoneRuntime_UploadDownloadDir(t *testing.T) {
	t.Parallel()

	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	// Create source dir with files and subdirs
	srcDir := t.TempDir()
	subdir := filepath.Join(srcDir, "sub")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "root.txt"), []byte("root"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "nested.txt"), []byte("nested"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Upload dir
	if err := rt.UploadDir(context.Background(), srcDir, "src"); err != nil {
		t.Fatalf("UploadDir failed: %v", err)
	}

	// Verify
	if _, err := os.ReadFile(filepath.Join(rt.Workspace(), "src", "root.txt")); err != nil {
		t.Errorf("root.txt should exist: %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(rt.Workspace(), "src", "sub", "nested.txt")); err != nil {
		t.Errorf("sub/nested.txt should exist: %v", err)
	}

	// Download
	dlDir := t.TempDir()
	if err := rt.DownloadDir(context.Background(), "src", filepath.Join(dlDir, "dst")); err != nil {
		t.Fatalf("DownloadDir failed: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dlDir, "dst", "root.txt")); err != nil {
		t.Errorf("downloaded root.txt should exist: %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(dlDir, "dst", "sub", "nested.txt")); err != nil {
		t.Errorf("downloaded sub/nested.txt should exist: %v", err)
	}
}

func TestNoneRuntime_Exec(t *testing.T) {
	t.Parallel()

	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	ctx := context.Background()

	// Basic command
	result, err := rt.Exec(ctx, "echo hello", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code: got %d, want 0", result.ExitCode)
	}
	if result.Stdout == "" {
		t.Error("stdout should not be empty")
	}

	// Non-zero exit
	result, err = rt.Exec(ctx, "exit 5", ExecOptions{})
	if err != nil {
		t.Fatal("Exec should not error on non-zero exit")
	}
	if result.ExitCode != 5 {
		t.Errorf("exit code: got %d, want 5", result.ExitCode)
	}

	// With custom Cwd - verify by checking a file exists in that dir
	result, err = rt.Exec(ctx, "ls", ExecOptions{Cwd: rt.Workspace()})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Errorf("Cwd should be respected: %s", result.Stderr)
	}
}

func TestNoneRuntime_ExecReturnsContextErrorOnTimeout(t *testing.T) {
	t.Parallel()

	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, err := rt.Exec(ctx, "sleep 1", ExecOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
	if result.ExitCode != -1 {
		t.Fatalf("expected exit code -1 for canceled process, got %d", result.ExitCode)
	}
}

func TestNoneRuntime_ExecAddsSpanCommandAttrs(t *testing.T) {
	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	originalProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer func() {
		otel.SetTracerProvider(originalProvider)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

	ctx, span := provider.Tracer("test").Start(context.Background(), "root")
	result, err := rt.Exec(ctx, "pwd", ExecOptions{Cwd: rt.Workspace()})
	span.End()
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", result.ExitCode)
	}

	var execSpan sdktrace.ReadOnlySpan
	for _, ended := range recorder.Ended() {
		if ended.Name() == "runtime.exec" {
			execSpan = ended
			break
		}
	}
	if execSpan == nil {
		t.Fatalf("runtime.exec span not recorded: %v", recorder.Ended())
	}
	assertSpanAttr(t, execSpan.Attributes(), "process.command", "pwd")
	assertSpanAttr(t, execSpan.Attributes(), "process.cwd", rt.Workspace())
}

func TestNoneRuntime_ExecLogsExitCodeSeverity(t *testing.T) {
	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	logging.SetVerbosity(0)
	defer logging.SetVerbosity(0)

	warnOutput := captureStdout(t, func() {
		_, _ = rt.Exec(context.Background(), "exit 1", ExecOptions{})
	})
	if !strings.Contains(warnOutput, "level=WARNING") {
		t.Fatalf("expected warning log for exit 1, got %q", warnOutput)
	}

	errorOutput := captureStdout(t, func() {
		_, _ = rt.Exec(context.Background(), "exit 5", ExecOptions{})
	})
	if !strings.Contains(errorOutput, "level=ERROR") {
		t.Fatalf("expected error log for exit 5, got %q", errorOutput)
	}
}

func assertSpanAttr(t *testing.T, attrs []attribute.KeyValue, key, want string) {
	t.Helper()
	for _, attr := range attrs {
		if string(attr.Key) == key {
			if got := attr.Value.AsString(); got != want {
				t.Fatalf("%s = %q, want %q", key, got, want)
			}
			return
		}
	}
	t.Fatalf("missing span attr %s in %v", key, attrs)
}

func TestNoneRuntime_ExecWithEnv(t *testing.T) {
	t.Parallel()

	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	ctx := context.Background()

	// Set a custom env var
	result, err := rt.Exec(ctx, "echo $MY_VAR", ExecOptions{Env: map[string]string{"MY_VAR": "test123"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatal(result.Stderr)
	}
	if result.Stdout == "" {
		t.Fatal("stdout should not be empty")
	}
}

func TestNoneRuntime_ExecExpandsPathFromRuntimeEnv(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Dir(bashPath)
	t.Setenv("PATH", basePath)

	rt := &NoneRuntime{cfg: Config{
		Env: map[string]string{
			"CUSTOM_BIN": "/agent/bin",
			"PATH":       "$CUSTOM_BIN:$PATH",
		},
	}}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	result, err := rt.Exec(context.Background(), "printf %s \"$PATH\"", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec returned error: %v", err)
	}
	if got, want := result.Stdout, "/agent/bin:"+basePath; got != want {
		t.Fatalf("PATH = %q, want %q", got, want)
	}
}

func TestNoneRuntime_CloseWithoutCreate(t *testing.T) {
	t.Parallel()

	rt := &NoneRuntime{}
	// Close without Create should not panic or error
	if err := rt.Close(); err != nil {
		t.Fatalf("Close without Create should be safe: %v", err)
	}
}

func TestNoneRuntime_DownloadDir_AbsoluteWorkspaceSource(t *testing.T) {
	t.Parallel()

	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	if err := os.WriteFile(filepath.Join(rt.Workspace(), "marker.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	dlDir := t.TempDir()
	dst := filepath.Join(dlDir, "copy")
	if err := rt.DownloadDir(context.Background(), rt.Workspace(), dst); err != nil {
		t.Fatalf("DownloadDir with absolute workspace path: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "marker.txt"))
	if err != nil || string(data) != "x" {
		t.Fatalf("expected marker in download: %v %q", err, data)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	logCaptureMu.Lock()
	defer logCaptureMu.Unlock()

	var buf bytes.Buffer
	restoreOutput := logging.SetOutputForTest(&buf)

	fn()

	restoreOutput()
	return buf.String()
}

func TestNoneRuntime_UploadFile_AbsoluteTargetPath(t *testing.T) {
	t.Parallel()

	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	outDir := t.TempDir()
	targetAbs := filepath.Join(outDir, "deep", "out.txt")
	src := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(src, []byte("abs"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rt.UploadFile(context.Background(), src, targetAbs); err != nil {
		t.Fatalf("UploadFile absolute target: %v", err)
	}
	data, err := os.ReadFile(targetAbs)
	if err != nil || string(data) != "abs" {
		t.Fatalf("read back: %v %q", err, data)
	}
}

func TestNoneRuntime_UploadDir_AbsoluteTargetPath(t *testing.T) {
	t.Parallel()

	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}

	outRoot := t.TempDir()
	targetAbs := filepath.Join(outRoot, "tree")
	if err := rt.UploadDir(context.Background(), srcDir, targetAbs); err != nil {
		t.Fatalf("UploadDir absolute target: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(targetAbs, "a.txt"))
	if err != nil || string(data) != "a" {
		t.Fatalf("read back: %v %q", err, data)
	}
}

func TestNoneRuntime_UploadFile_NestedPath(t *testing.T) {
	t.Parallel()

	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "f.txt")
	if err := os.WriteFile(srcFile, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Upload to nested path
	if err := rt.UploadFile(context.Background(), srcFile, "a/b/c/f.txt"); err != nil {
		t.Fatalf("UploadFile with nested path failed: %v", err)
	}

	// Verify
	if _, err := os.ReadFile(filepath.Join(rt.Workspace(), "a", "b", "c", "f.txt")); err != nil {
		t.Errorf("nested file should exist: %v", err)
	}
}

func TestMaskCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"short", "echo hello", "echo hello"},
		{"exactly 200 runes", strings.Repeat("a", 200), strings.Repeat("a", 200)},
		{"201 runes", strings.Repeat("a", 201), strings.Repeat("a", 200) + "..."},
		{"chinese under limit", strings.Repeat("\u4f60", 200), strings.Repeat("\u4f60", 200)},
		{"chinese over limit", strings.Repeat("\u4f60", 201), strings.Repeat("\u4f60", 200) + "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := maskCommand(tt.input)
			if got != tt.want {
				t.Errorf("maskCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}
