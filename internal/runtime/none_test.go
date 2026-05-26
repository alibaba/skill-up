package runtime

import (
	"bytes"
	"context"
	"errors"
	"os"
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
	if !rt.RequiresProcessSandbox() {
		t.Fatal("NoneRuntime should require process sandboxing")
	}
}

func TestNewRuntimeErrors(t *testing.T) {
	t.Parallel()

	if _, err := NewRuntime(Config{Type: "unknown"}); err == nil || !strings.Contains(err.Error(), "unknown runtime type") {
		t.Fatalf("NewRuntime unknown error = %v", err)
	}
	if _, err := NewRuntime(Config{Type: "docker"}); err == nil || !strings.Contains(err.Error(), "requires environment.image") {
		t.Fatalf("NewRuntime docker validation error = %v", err)
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

func TestNoneRuntime_ExecLogsDeadlineDelta_OnTimeout(t *testing.T) {
	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	output := captureStdout(t, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, _ = rt.Exec(ctx, "sleep 1", ExecOptions{})
	})

	if !strings.Contains(output, "command killed by context (context deadline exceeded") {
		t.Fatalf("expected timeout reason in log, got: %s", output)
	}
	if !strings.Contains(output, "deadline elapsed by") {
		t.Fatalf("expected deadline-elapsed annotation in log, got: %s", output)
	}
}

func TestNoneRuntime_ExecOmitsDeadlineDelta_OnManualCancel(t *testing.T) {
	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	output := captureStdout(t, func() {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()
		_, _ = rt.Exec(ctx, "sleep 1", ExecOptions{})
	})

	if !strings.Contains(output, "command killed by context (context canceled") {
		t.Fatalf("expected cancel reason in log, got: %s", output)
	}
	// A manual cancel has no deadline; the negative-elapsed annotation must
	// not appear (it would read "deadline elapsed by -…").
	if strings.Contains(output, "deadline elapsed by") {
		t.Fatalf("must not annotate deadline on manual cancel, got: %s", output)
	}
}

func TestNoneRuntime_ExecKillsDescendantsOnTimeout(t *testing.T) {
	t.Parallel()

	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// A backgrounded descendant tries to write a marker 3s in; if the process
	// group is killed on timeout it never runs.
	_, err := rt.Exec(ctx, "(sleep 3 && touch leaked.txt) & sleep 10", ExecOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}

	// Wait past the descendant's would-be write time.
	time.Sleep(4 * time.Second)
	if _, statErr := os.Stat(filepath.Join(rt.Workspace(), "leaked.txt")); statErr == nil {
		t.Fatal("descendant process survived the timeout and wrote leaked.txt")
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

	// Many CLIs (build tools, test runners) write the actual failure
	// context to stdout, not stderr. Ensure that on non-zero exit we
	// surface stdout so users don't see only an exit code with no
	// explanation.
	stdoutOnExit1 := captureStdout(t, func() {
		_, _ = rt.Exec(context.Background(), "echo build-failed-on-stdout; exit 1", ExecOptions{})
	})
	if !strings.Contains(stdoutOnExit1, "stdout: build-failed-on-stdout") {
		t.Fatalf("expected stdout to be logged on exit 1, got %q", stdoutOnExit1)
	}

	stdoutOnExit5 := captureStdout(t, func() {
		_, _ = rt.Exec(context.Background(), "echo build-failed-on-stdout; exit 5", ExecOptions{})
	})
	if !strings.Contains(stdoutOnExit5, "stdout: build-failed-on-stdout") {
		t.Fatalf("expected stdout to be logged on exit 5, got %q", stdoutOnExit5)
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

// TestNoneRuntime_ForwardsEnvLiterally verifies that NoneRuntime forwards
// $-bearing env values literally — matching the docker and opensandbox
// runtimes. Callers that want shell expansion must resolve the value first
// (see internal/agent.probeAndMergePATH) or prepend `export X=...` to the
// command themselves. The runtime never expands.
//
// printf '%s' "$VAR" prints whatever string the runtime handed to the
// shell. If the runtime pre-expanded $CUSTOM_BIN / $PATH the child would
// see "/agent/bin:..." instead of the literal "$CUSTOM_BIN:$PATH".
func TestNoneRuntime_ForwardsEnvLiterally(t *testing.T) {
	rt := &NoneRuntime{cfg: Config{
		Env: map[string]string{
			"CUSTOM_BIN":      "/agent/bin",
			"LITERAL_PATH":    "$CUSTOM_BIN:$PATH",
			"ALSO_UNEXPANDED": "${HOME}/foo",
		},
	}}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	result, err := rt.Exec(context.Background(), `printf '%s\n%s\n' "$LITERAL_PATH" "$ALSO_UNEXPANDED"`, ExecOptions{})
	if err != nil {
		t.Fatalf("Exec returned error: %v", err)
	}
	want := "$CUSTOM_BIN:$PATH\n${HOME}/foo\n"
	if result.Stdout != want {
		t.Fatalf("env values should be passed literally; got %q want %q", result.Stdout, want)
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

func TestNoneRuntime_MergeEnv(t *testing.T) {
	t.Parallel()
	rt := &NoneRuntime{}
	rt.MergeEnv(map[string]string{"FOO": "1", "BAR": "2"})
	if got := rt.cfg.Env["FOO"]; got != "1" {
		t.Fatalf("FOO = %q, want 1", got)
	}
	if got := rt.cfg.Env["BAR"]; got != "2" {
		t.Fatalf("BAR = %q, want 2", got)
	}
	// Second call overlays; later keys win.
	rt.MergeEnv(map[string]string{"BAR": "overridden", "BAZ": "3"})
	if got := rt.cfg.Env["BAR"]; got != "overridden" {
		t.Fatalf("BAR after overlay = %q, want overridden", got)
	}
	if got := rt.cfg.Env["BAZ"]; got != "3" {
		t.Fatalf("BAZ = %q, want 3", got)
	}
	if got := rt.cfg.Env["FOO"]; got != "1" {
		t.Fatalf("FOO after overlay = %q, want 1 preserved", got)
	}
	// Empty map is a no-op.
	rt.MergeEnv(nil)
	rt.MergeEnv(map[string]string{})
	if len(rt.cfg.Env) != 3 {
		t.Fatalf("env length = %d, want 3 after no-op calls", len(rt.cfg.Env))
	}
}

func TestNoneRuntime_MergeEnv_VisibleToSubsequentExec(t *testing.T) {
	t.Parallel()
	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	rt.MergeEnv(map[string]string{"SKILL_UP_MERGE_TEST": "visible"})

	res, err := rt.Exec(context.Background(), `printf '%s' "$SKILL_UP_MERGE_TEST"`, ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.Stdout != "visible" {
		t.Fatalf("Exec saw SKILL_UP_MERGE_TEST=%q, want visible", res.Stdout)
	}
}

func TestNoneRuntime_Exec_WaitDelayWithCleanExitIsSuccess(t *testing.T) {
	// A process that exits 0 but leaves a background child holding stdout
	// past WaitDelay must surface as a successful exec — not a hard error.
	// Before the fix, classifyExecError returned (-1, exec.ErrWaitDelay)
	// and built-in agents were reported as failed even after producing
	// valid output.
	rt := &NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	result, err := rt.Exec(context.Background(), `echo hello; (sleep 30 &); exit 0`, ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: %v (WaitDelay must not surface as a hard error)", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "hello") {
		t.Fatalf("Stdout = %q, want it to contain the pre-WaitDelay output", result.Stdout)
	}
}
