package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeDockerCall is one captured invocation of the docker CLI.
type fakeDockerCall struct {
	args []string
}

// fakeDockerResponse drives what the fake CLI returns for a given call.
type fakeDockerResponse struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
}

// newFakeDocker returns a dockerCommandRunner that records every call and
// answers each according to the supplied script (in order). The first arg
// passed to docker (the subcommand) is asserted against script[i].match
// when match is non-empty, so a typo in the implementation is caught loudly.
type scriptedCall struct {
	match    string // expected first arg (subcommand); empty = any
	response fakeDockerResponse
}

type fakeDocker struct {
	mu     sync.Mutex
	calls  []fakeDockerCall
	script []scriptedCall
	idx    int
	t      *testing.T
}

func newFakeDocker(t *testing.T, script []scriptedCall) *fakeDocker {
	t.Helper()
	return &fakeDocker{script: script, t: t}
}

func (f *fakeDocker) runner() dockerCommandRunner {
	return func(_ context.Context, name string, args ...string) (string, string, int, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if name != "docker" {
			f.t.Fatalf("unexpected docker binary name %q", name)
		}
		f.calls = append(f.calls, fakeDockerCall{args: append([]string(nil), args...)})
		if f.idx >= len(f.script) {
			f.t.Fatalf("unexpected extra docker call #%d: %v", f.idx, args)
		}
		step := f.script[f.idx]
		f.idx++
		if step.match != "" && (len(args) == 0 || args[0] != step.match) {
			f.t.Fatalf("expected docker subcommand %q at step %d, got %v", step.match, f.idx-1, args)
		}
		return step.response.stdout, step.response.stderr, step.response.exitCode, step.response.err
	}
}

func (f *fakeDocker) callArgs(i int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i].args
}

func newDockerRuntimeForTest(t *testing.T, cfg Config, fd *fakeDocker) *DockerRuntime {
	t.Helper()
	if cfg.Image == "" {
		cfg.Image = "alpine:3.20"
	}
	r, err := NewDockerRuntime(cfg)
	if err != nil {
		t.Fatalf("NewDockerRuntime: %v", err)
	}
	r.run = fd.runner()
	return r
}

// createScript returns the canonical scripted-call sequence that Create
// performs (docker create → docker start → docker exec mkdir -p workspace).
// Test scripts prepend this before the steps they actually want to exercise.
// id is the container ID echoed back by the fake create call.
func createScript(id string) []scriptedCall {
	return []scriptedCall{
		{match: "create", response: fakeDockerResponse{stdout: id + "\n"}},
		{match: "start", response: fakeDockerResponse{}},
		{match: "exec", response: fakeDockerResponse{}}, // workspace mkdir -p
	}
}

// createCallCount is how many scripted calls createScript consumes; tests
// add this to local offsets so they reference the right post-Create call.
const createCallCount = 3

func TestNewDockerRuntime_RequiresImage(t *testing.T) {
	t.Parallel()
	if _, err := NewDockerRuntime(Config{}); err == nil {
		t.Fatal("expected error when image is missing")
	}
}

func TestNewDockerRuntime_RejectsAllowDeclared(t *testing.T) {
	t.Parallel()
	_, err := NewDockerRuntime(Config{Image: "alpine:3.20", NetworkPolicy: "allow_declared"})
	if err == nil || !strings.Contains(err.Error(), "allow_declared") {
		t.Fatalf("expected allow_declared rejection, got %v", err)
	}
}

func TestNewDockerRuntime_RequiresAbsoluteWorkspace(t *testing.T) {
	t.Parallel()
	_, err := NewDockerRuntime(Config{Image: "alpine:3.20", WorkspaceMount: "rel/path"})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected absolute-path rejection, got %v", err)
	}
}

func TestDockerRuntime_RequiresProcessSandboxFalse(t *testing.T) {
	t.Parallel()
	r, err := NewDockerRuntime(Config{Image: "alpine:3.20"})
	if err != nil {
		t.Fatalf("NewDockerRuntime: %v", err)
	}
	if r.RequiresProcessSandbox() {
		t.Fatal("docker runtime should not require additional process sandbox")
	}
}

func TestDockerRuntime_WorkspaceDefault(t *testing.T) {
	t.Parallel()
	r, err := NewDockerRuntime(Config{Image: "alpine:3.20"})
	if err != nil {
		t.Fatalf("NewDockerRuntime: %v", err)
	}
	if r.Workspace() != dockerDefaultWorkspace {
		t.Fatalf("Workspace = %q, want %q", r.Workspace(), dockerDefaultWorkspace)
	}
}

func TestDockerRuntime_CreateAndCloseDeletesContainer(t *testing.T) {
	t.Parallel()
	script := append(createScript("deadbeef"),
		scriptedCall{match: "rm", response: fakeDockerResponse{}},
	)
	fd := newFakeDocker(t, script)
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20", Delete: true}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.containerID != "deadbeef" {
		t.Fatalf("containerID = %q, want %q", r.containerID, "deadbeef")
	}
	if !r.started {
		t.Fatal("Create should leave container started")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	rmArgs := fd.callArgs(createCallCount)
	if rmArgs[0] != "rm" || rmArgs[1] != "-f" || rmArgs[2] != "deadbeef" {
		t.Fatalf("unexpected rm args: %v", rmArgs)
	}
}

func TestDockerRuntime_CreatePassesNetworkAndEnvAndImage(t *testing.T) {
	t.Parallel()
	fd := newFakeDocker(t, createScript("abc"))
	r := newDockerRuntimeForTest(t, Config{
		Image:         "ubuntu:22.04",
		NetworkPolicy: "deny_all",
		Env:           map[string]string{"FOO": "bar"},
	}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	args := fd.callArgs(0)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--network none") {
		t.Errorf("create should set --network none for deny_all; got %v", args)
	}
	if !strings.Contains(joined, "--env FOO=bar") {
		t.Errorf("create should pass --env FOO=bar; got %v", args)
	}
	// Default entrypoint must be passed via the `--entrypoint sleep`
	// flag (otherwise it lands in CMD and gets ignored when the image
	// has its own ENTRYPOINT), and `infinity` must appear after the
	// image as the CMD arg.
	if !strings.Contains(joined, "--entrypoint sleep") {
		t.Errorf("expected --entrypoint sleep for default entrypoint; got %v", args)
	}
	imgIdx := indexOf(args, "ubuntu:22.04")
	if imgIdx < 0 {
		t.Fatalf("image not in args: %v", args)
	}
	if imgIdx+1 >= len(args) || args[imgIdx+1] != "infinity" {
		t.Errorf("expected `infinity` as CMD after image; got tail %v", args[imgIdx:])
	}
	if !strings.Contains(joined, "--workdir "+dockerDefaultWorkspace) {
		t.Errorf("create should set --workdir to workspace; got %v", args)
	}
}

func TestDockerRuntime_CreateOmitsNetworkArgsWithoutPolicy(t *testing.T) {
	t.Parallel()
	fd := newFakeDocker(t, createScript("abc"))
	r := newDockerRuntimeForTest(t, Config{Image: "ubuntu:22.04"}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if strings.Contains(strings.Join(fd.callArgs(0), " "), "--network") {
		t.Errorf("no network flag expected when NetworkPolicy is unset; got %v", fd.callArgs(0))
	}
}

func TestDockerRuntime_CreateUsesCustomEntrypoint(t *testing.T) {
	t.Parallel()
	fd := newFakeDocker(t, createScript("abc"))
	r := newDockerRuntimeForTest(t, Config{
		Image:      "alpine:3.20",
		Entrypoint: []string{"/usr/bin/env", "tail", "-f", "/dev/null"},
	}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	args := fd.callArgs(0)
	// User-supplied Entrypoint[0] becomes --entrypoint, remaining
	// elements become CMD args after the image. This is what actually
	// overrides the image's ENTRYPOINT — appending positional args
	// after the image only sets CMD, which is masked when the image
	// declares ENTRYPOINT.
	if !strings.Contains(strings.Join(args, " "), "--entrypoint /usr/bin/env") {
		t.Errorf("expected --entrypoint /usr/bin/env; got %v", args)
	}
	imgIdx := indexOf(args, "alpine:3.20")
	if imgIdx < 0 {
		t.Fatalf("image not in args: %v", args)
	}
	tail := args[imgIdx+1:]
	want := []string{"tail", "-f", "/dev/null"}
	if len(tail) != len(want) {
		t.Fatalf("CMD tail = %v, want %v", tail, want)
	}
	for i, v := range want {
		if tail[i] != v {
			t.Fatalf("CMD tail = %v, want %v", tail, want)
		}
	}
}

// Single-element Entrypoint should produce --entrypoint X with no trailing
// CMD args after the image. This is the common "I just want to override
// the image's entrypoint with a long-lived sleep binary" case.
func TestDockerRuntime_CreateUsesSingleArgCustomEntrypoint(t *testing.T) {
	t.Parallel()
	fd := newFakeDocker(t, createScript("abc"))
	r := newDockerRuntimeForTest(t, Config{
		Image:      "alpine:3.20",
		Entrypoint: []string{"/bin/cat"},
	}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	args := fd.callArgs(0)
	if !strings.Contains(strings.Join(args, " "), "--entrypoint /bin/cat") {
		t.Errorf("expected --entrypoint /bin/cat; got %v", args)
	}
	imgIdx := indexOf(args, "alpine:3.20")
	if imgIdx == -1 || imgIdx != len(args)-1 {
		t.Errorf("image should be the last arg with no CMD; got args %v", args)
	}
}

func TestDockerRuntime_CreateFailsSurfacesStderr(t *testing.T) {
	t.Parallel()
	fd := newFakeDocker(t, []scriptedCall{
		{match: "create", response: fakeDockerResponse{stderr: "image not found", exitCode: 1}},
	})
	r := newDockerRuntimeForTest(t, Config{Image: "missing:tag"}, fd)
	err := r.Create(context.Background())
	if err == nil || !strings.Contains(err.Error(), "image not found") {
		t.Fatalf("expected stderr in error, got %v", err)
	}
}

// If `docker create` succeeds but `docker start` fails, Create must rm -f
// the half-created container so it doesn't leak on the host.
func TestDockerRuntime_CreateRollsBackOnStartFailure(t *testing.T) {
	t.Parallel()
	fd := newFakeDocker(t, []scriptedCall{
		{match: "create", response: fakeDockerResponse{stdout: "abc\n"}},
		{match: "start", response: fakeDockerResponse{stderr: "no permission", exitCode: 125}},
		{match: "rm", response: fakeDockerResponse{}}, // cleanup
	})
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20"}, fd)
	err := r.Create(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no permission") {
		t.Fatalf("expected start failure, got %v", err)
	}
	// Runtime state was reset so a retry is possible.
	if r.containerID != "" {
		t.Fatalf("containerID should be cleared on rollback, got %q", r.containerID)
	}
	if r.started {
		t.Fatal("started should be false after rollback")
	}
}

// Close must keep r.containerID set when `docker rm -f` errors, so the
// caller can retry Close after a transient daemon hiccup instead of
// silently leaking the container on the host.
func TestDockerRuntime_CloseKeepsContainerIDOnRmFailure(t *testing.T) {
	t.Parallel()
	script := append(createScript("abc"),
		scriptedCall{match: "rm", response: fakeDockerResponse{stderr: "daemon busy", exitCode: 1}},
		scriptedCall{match: "rm", response: fakeDockerResponse{}}, // retry succeeds
	)
	fd := newFakeDocker(t, script)
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20", Delete: true}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.Close(); err == nil {
		t.Fatal("expected Close to surface rm failure")
	}
	if r.containerID != "abc" {
		t.Fatalf("containerID should be retained on rm failure, got %q", r.containerID)
	}
	// Retrying Close after the transient error must actually invoke rm
	// again and clear the handle on success.
	if err := r.Close(); err != nil {
		t.Fatalf("retry Close: %v", err)
	}
	if r.containerID != "" {
		t.Fatalf("containerID should be cleared after successful retry, got %q", r.containerID)
	}
}

// If the workspace mkdir after start fails, Create must also rm -f the
// running container — partial state on the host is worse than no state.
func TestDockerRuntime_CreateRollsBackOnWorkspaceMkdirFailure(t *testing.T) {
	t.Parallel()
	fd := newFakeDocker(t, []scriptedCall{
		{match: "create", response: fakeDockerResponse{stdout: "abc\n"}},
		{match: "start", response: fakeDockerResponse{}},
		{match: "exec", response: fakeDockerResponse{stderr: "read-only fs", exitCode: 1}},
		{match: "rm", response: fakeDockerResponse{}}, // cleanup
	})
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20"}, fd)
	err := r.Create(context.Background())
	if err == nil || !strings.Contains(err.Error(), "read-only fs") {
		t.Fatalf("expected mkdir failure, got %v", err)
	}
	if r.containerID != "" {
		t.Fatalf("containerID should be cleared on rollback, got %q", r.containerID)
	}
}

func TestDockerRuntime_CloseSkipsRmWhenDeleteFalse(t *testing.T) {
	t.Parallel()
	script := append(createScript("abc"),
		scriptedCall{match: "stop", response: fakeDockerResponse{}},
	)
	fd := newFakeDocker(t, script)
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20", Delete: false}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	stop := fd.callArgs(createCallCount)
	if stop[0] != "stop" {
		t.Fatalf("expected stop, got %v", stop)
	}
}

func TestDockerRuntime_StartStopAndIdempotency(t *testing.T) {
	t.Parallel()
	// Create already starts; further Start calls are no-ops. Then Stop,
	// followed by Close which rm -f's the (stopped) container.
	script := append(createScript("abc"),
		scriptedCall{match: "stop", response: fakeDockerResponse{}},
		scriptedCall{match: "rm", response: fakeDockerResponse{}},
	)
	fd := newFakeDocker(t, script)
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20", Delete: true}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Already started by Create; this Start must be a no-op (consumes no
	// extra scripted call).
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start (idempotent after Create): %v", err)
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Second Stop is also a no-op when not started.
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("Stop (idempotent): %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestDockerRuntime_StartFailsBeforeCreate(t *testing.T) {
	t.Parallel()
	fd := newFakeDocker(t, nil)
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20"}, fd)
	if err := r.Start(context.Background()); err == nil {
		t.Fatal("Start before Create should error")
	}
}

func TestDockerRuntime_UploadFileEnsuresParentDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	if err := os.WriteFile(src, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := append(createScript("abc"),
		scriptedCall{match: "exec", response: fakeDockerResponse{}}, // upload's mkdir -p
		scriptedCall{match: "cp", response: fakeDockerResponse{}},
	)
	fd := newFakeDocker(t, script)
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20"}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.UploadFile(context.Background(), src, "sub/dst.txt"); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	mkdir := fd.callArgs(createCallCount)
	if mkdir[0] != "exec" || mkdir[len(mkdir)-3] != "mkdir" || mkdir[len(mkdir)-2] != "-p" {
		t.Fatalf("expected exec mkdir -p, got %v", mkdir)
	}
	if mkdir[len(mkdir)-1] != dockerDefaultWorkspace+"/sub" {
		t.Fatalf("mkdir target = %q, want %q", mkdir[len(mkdir)-1], dockerDefaultWorkspace+"/sub")
	}
	cp := fd.callArgs(createCallCount + 1)
	want := "abc:" + dockerDefaultWorkspace + "/sub/dst.txt"
	if cp[len(cp)-1] != want {
		t.Fatalf("cp dest = %q, want %q", cp[len(cp)-1], want)
	}
	if cp[len(cp)-2] != src {
		t.Fatalf("cp src = %q, want %q", cp[len(cp)-2], src)
	}
}

func TestDockerRuntime_DownloadFileWritesLocalDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dst := filepath.Join(tmp, "nested", "out.txt")
	script := append(createScript("abc"),
		scriptedCall{match: "cp", response: fakeDockerResponse{}},
	)
	fd := newFakeDocker(t, script)
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20"}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.DownloadFile(context.Background(), "/abs/file.txt", dst); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(dst)); err != nil {
		t.Fatalf("target dir was not created: %v", err)
	}
	cp := fd.callArgs(createCallCount)
	if cp[len(cp)-2] != "abc:/abs/file.txt" {
		t.Fatalf("cp src = %q, want %q", cp[len(cp)-2], "abc:/abs/file.txt")
	}
	if cp[len(cp)-1] != dst {
		t.Fatalf("cp dst = %q, want %q", cp[len(cp)-1], dst)
	}
}

func TestDockerRuntime_UploadDirUsesDotSyntax(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	script := append(createScript("abc"),
		scriptedCall{match: "exec", response: fakeDockerResponse{}}, // ensure dest dir
		scriptedCall{match: "cp", response: fakeDockerResponse{}},
	)
	fd := newFakeDocker(t, script)
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20"}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.UploadDir(context.Background(), src, "into"); err != nil {
		t.Fatalf("UploadDir: %v", err)
	}
	cp := fd.callArgs(createCallCount + 1)
	wantSrc := src + string(filepath.Separator) + "."
	if cp[len(cp)-2] != wantSrc {
		t.Fatalf("cp src = %q, want %q", cp[len(cp)-2], wantSrc)
	}
	wantDst := "abc:" + dockerDefaultWorkspace + "/into"
	if cp[len(cp)-1] != wantDst {
		t.Fatalf("cp dst = %q, want %q", cp[len(cp)-1], wantDst)
	}
}

func TestDockerRuntime_DownloadDirUsesDotSyntax(t *testing.T) {
	t.Parallel()
	dst := t.TempDir()
	script := append(createScript("abc"),
		scriptedCall{match: "cp", response: fakeDockerResponse{}},
	)
	fd := newFakeDocker(t, script)
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20"}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.DownloadDir(context.Background(), "outputs", dst); err != nil {
		t.Fatalf("DownloadDir: %v", err)
	}
	cp := fd.callArgs(createCallCount)
	wantSrc := "abc:" + dockerDefaultWorkspace + "/outputs/."
	if cp[len(cp)-2] != wantSrc {
		t.Fatalf("cp src = %q, want %q", cp[len(cp)-2], wantSrc)
	}
	if cp[len(cp)-1] != dst {
		t.Fatalf("cp dst = %q, want %q", cp[len(cp)-1], dst)
	}
}

func TestDockerRuntime_ExecPassesCwdEnvAndCommand(t *testing.T) {
	t.Parallel()
	script := append(createScript("abc"),
		scriptedCall{match: "exec", response: fakeDockerResponse{stdout: "ok", exitCode: 0}},
	)
	fd := newFakeDocker(t, script)
	r := newDockerRuntimeForTest(t, Config{
		Image: "alpine:3.20",
		Env:   map[string]string{"GLOBAL": "yes"},
	}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	res, err := r.Exec(context.Background(), "echo hi", ExecOptions{
		Cwd: "sub",
		Env: map[string]string{"FOO": "bar"},
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 0 || res.Stdout != "ok" {
		t.Fatalf("unexpected result %+v", res)
	}
	args := fd.callArgs(createCallCount)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--workdir "+dockerDefaultWorkspace+"/sub") {
		t.Errorf("expected workdir join under workspace; got %v", args)
	}
	if !strings.Contains(joined, "--env FOO=bar") {
		t.Errorf("expected --env FOO=bar; got %v", args)
	}
	if !strings.Contains(joined, "--env GLOBAL=yes") {
		t.Errorf("expected --env GLOBAL=yes from cfg.Env; got %v", args)
	}
	// Command must be the last token, after `sh -c` (not bash — many
	// base images don't ship bash). All shell snippets the rest of the
	// codebase emits are POSIX-compatible.
	if args[len(args)-3] != "sh" || args[len(args)-2] != "-c" || args[len(args)-1] != "echo hi" {
		t.Fatalf("expected ... sh -c \"echo hi\" at tail, got %v", args)
	}
}

func TestDockerRuntime_ExecRejectsWorkdirEscape(t *testing.T) {
	t.Parallel()
	fd := newFakeDocker(t, createScript("abc"))
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20"}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err := r.Exec(context.Background(), "id", ExecOptions{Cwd: "../../etc"})
	if err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("expected workspace-escape error, got %v", err)
	}
}

func TestDockerRuntime_ExecPropagatesNonZeroExit(t *testing.T) {
	t.Parallel()
	script := append(createScript("abc"),
		scriptedCall{match: "exec", response: fakeDockerResponse{stderr: "boom", exitCode: 42}},
	)
	fd := newFakeDocker(t, script)
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20"}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	res, err := r.Exec(context.Background(), "false", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: unexpected error %v", err)
	}
	if res.ExitCode != 42 {
		t.Fatalf("ExitCode = %d, want 42", res.ExitCode)
	}
	if res.Stderr != "boom" {
		t.Fatalf("Stderr = %q, want %q", res.Stderr, "boom")
	}
}

// Exec must surface docker-layer failures as errors (container vanished,
// daemon refused the exec), not as ordinary non-zero exits — otherwise the
// evaluator routes them through the normal FAIL path and skips its
// infra-fault retry handling.
func TestDockerRuntime_ExecSurfacesLayerFailureAsError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		exit     int
		stderr   string
		wantErr  bool
		wantText string
	}{
		{"exit 125 is daemon-layer", 125, "", true, "layer failure"},
		{"daemon refused prefix", 1, "Error response from daemon: No such exec instance", true, "layer failure"},
		{"no such container", 1, "Error: No such container: abc", true, "layer failure"},
		{"OCI runtime failed", 126, "OCI runtime exec failed: cannot exec in stopped container", true, "layer failure"},
		{"container not running (daemon-prefixed)", 1, "Error response from daemon: Container abc is not running", true, "layer failure"},
		{"plain non-zero is NOT layer error", 1, "assertion failed", false, ""},
		{"common 2 (misuse of shell builtins) is NOT layer", 2, "syntax error", false, ""},
		{"127 cmd-not-found stays as cmd error", 127, "sh: bogus: not found", false, ""},
		// Regression guards against the previous over-broad substring
		// matching: user scripts that happen to print "is not running"
		// or "OCI runtime" must NOT be misclassified as infra faults.
		{"user script saying redis is not running", 1, "redis is not running on port 6379", false, ""},
		{"user script with OCI runtime in plain text", 1, "checking OCI runtime version output: skipped", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := append(createScript("abc"),
				scriptedCall{match: "exec", response: fakeDockerResponse{stderr: tc.stderr, exitCode: tc.exit}},
			)
			fd := newFakeDocker(t, script)
			r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20"}, fd)
			if err := r.Create(context.Background()); err != nil {
				t.Fatalf("Create: %v", err)
			}
			res, err := r.Exec(context.Background(), "x", ExecOptions{})
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got err=%v", tc.wantErr, err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantText)
			}
			if res.ExitCode != tc.exit {
				t.Errorf("ExitCode = %d, want %d", res.ExitCode, tc.exit)
			}
		})
	}
}

// Concurrent Close during in-flight Exec/Upload/Download must not race on
// r.containerID. Run with -race to actually catch the regression: with the
// old `r.containerID` reads outside the mutex, the data-race detector
// flags the test; with snapshotContainerID under the lock, it passes.
//
// We use a permissive runner here (not the scripted one) because Exec and
// Close fire in a non-deterministic order, so the call sequence isn't
// known up front.
func TestDockerRuntime_ExecRaceWithCloseIsSafe(t *testing.T) {
	t.Parallel()

	// Permissive runner: any subcommand is fine, return success. We
	// just need the calls to happen so the goroutines actually exercise
	// snapshotContainerID under contention.
	var callCount int64
	r, err := NewDockerRuntime(Config{Image: "alpine:3.20", Delete: true})
	if err != nil {
		t.Fatal(err)
	}
	r.run = func(_ context.Context, _ string, args ...string) (string, string, int, error) {
		atomic.AddInt64(&callCount, 1)
		// `docker create` is expected to echo the id on stdout.
		if len(args) > 0 && args[0] == "create" {
			return "abc\n", "", 0, nil
		}
		return "", "", 0, nil
	}
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Spawn many concurrent Exec/Upload + a Close. With the old
	// outside-the-lock reads, -race would flag this; with the snapshot
	// pattern, every goroutine reads the id under mu and then proceeds
	// with the local copy.
	const workers = 16
	var wg sync.WaitGroup
	wg.Add(workers + 1)
	for range workers {
		go func() {
			defer wg.Done()
			_, _ = r.Exec(context.Background(), "echo hi", ExecOptions{})
		}()
	}
	go func() {
		defer wg.Done()
		_ = r.Close()
	}()
	wg.Wait()

	// Sanity check: at least some calls actually ran (Create plus
	// either Close or some Execs). The exact number is racy because
	// Execs that lose the race to Close return early from snapshot.
	if atomic.LoadInt64(&callCount) < 3 {
		t.Fatalf("expected at least 3 docker calls (create+start+mkdir), got %d", callCount)
	}
}

// rollbackRemove must NOT block forever when the parent context has a
// deadline and the cleanup docker call hangs. We can't easily simulate
// a hung docker without time injection, but we can confirm the cleanup
// runs on a context detached from the parent (a canceled parent must
// not cancel the rollback).
func TestDockerRuntime_RollbackUsesDetachedTimeout(t *testing.T) {
	t.Parallel()

	// Parent context that's already canceled when Create's rollback
	// fires. If rollbackRemove inherited the parent context's
	// cancellation, the rm call would never reach the runner — but
	// our script REQUIRES the rm call to happen (script consumed in
	// order). The runner asserts unexpected leftover steps.
	parentCtx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately so Create's parent is dead before start fails.

	fd := newFakeDocker(t, []scriptedCall{
		{match: "create", response: fakeDockerResponse{stdout: "abc\n"}},
		{match: "start", response: fakeDockerResponse{stderr: "boom", exitCode: 125}},
		// This rm MUST still run despite the parent context being
		// canceled — that's the contract.
		{match: "rm", response: fakeDockerResponse{}},
	})
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20"}, fd)
	if err := r.Create(parentCtx); err == nil {
		t.Fatal("expected Create to error on start failure")
	}
	// Verify both create AND rm fired (script fully consumed). If the
	// rollback got skipped, the fake docker test helper would have
	// triggered an "unexpected extra call" failure during the test —
	// but it would NOT have flagged a missed call, so re-check here.
	if got := len(fd.calls); got != 3 {
		t.Fatalf("expected 3 docker calls (create, start, rm), got %d: %v", got, fd.calls)
	}
}

// When rollbackRemove itself fails (daemon down, rm times out), Create must
// retain containerID so the caller can still reach the container via Close.
func TestDockerRuntime_CreateRetainsIDWhenRollbackFails(t *testing.T) {
	t.Parallel()
	fd := newFakeDocker(t, []scriptedCall{
		{match: "create", response: fakeDockerResponse{stdout: "abc\n"}},
		{match: "start", response: fakeDockerResponse{stderr: "no cgroups", exitCode: 125}},
		{match: "rm", response: fakeDockerResponse{stderr: "daemon down", exitCode: 1}},
	})
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20"}, fd)
	err := r.Create(context.Background())
	if err == nil {
		t.Fatal("expected Create to fail")
	}
	if r.containerID != "abc" {
		t.Fatalf("containerID should be retained when rollback fails, got %q", r.containerID)
	}
}

// dockerCLIErr formats cleanly whether or not a wrapped error is present.
// Without this, `%w, nil` renders as `%!w(<nil>)` and pollutes diagnostics.
func TestDockerCLIErr_FormattingWithAndWithoutWrap(t *testing.T) {
	t.Parallel()
	// No wrapped err — must not contain %!w or <nil>.
	clean := dockerCLIErr("boom\n", 7, nil, "docker something failed for %s", "x")
	got := clean.Error()
	if strings.Contains(got, "%!w") || strings.Contains(got, "<nil>") {
		t.Fatalf("nil-wrap leaked: %q", got)
	}
	if !strings.Contains(got, "docker something failed for x") || !strings.Contains(got, "exit=7") || !strings.Contains(got, "boom") {
		t.Errorf("unexpected message: %q", got)
	}

	// With a wrapped err — errors.Is must find it.
	wrapped := dockerCLIErr("ignored", 0, context.Canceled, "docker did fail")
	if !errors.Is(wrapped, context.Canceled) {
		t.Fatalf("expected wrapped err to be unwrappable to context.Canceled, got %v", wrapped)
	}
}

func TestDockerRuntime_ExecSurfacesStartupErrorWhenNoExit(t *testing.T) {
	t.Parallel()
	bad := errors.New("dial unix /var/run/docker.sock: no such file")
	script := append(createScript("abc"),
		scriptedCall{match: "exec", response: fakeDockerResponse{exitCode: -1, err: bad}},
	)
	fd := newFakeDocker(t, script)
	r := newDockerRuntimeForTest(t, Config{Image: "alpine:3.20"}, fd)
	if err := r.Create(context.Background()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err := r.Exec(context.Background(), "true", ExecOptions{})
	if err == nil || !strings.Contains(err.Error(), "docker exec failed") {
		t.Fatalf("expected wrapped docker exec error, got %v", err)
	}
}

func TestDockerRuntime_NewRuntimeWiresDocker(t *testing.T) {
	t.Parallel()
	rt, err := NewRuntime(Config{Type: "docker", Image: "alpine:3.20"})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if _, ok := rt.(*DockerRuntime); !ok {
		t.Fatalf("NewRuntime returned %T, want *DockerRuntime", rt)
	}
}

func indexOf(ss []string, want string) int {
	for i, v := range ss {
		if v == want {
			return i
		}
	}
	return -1
}

// Sanity check that the dockerCommandRunner signature actually behaves the
// way the production helper does — exec error with no real exit becomes -1
// + the error from classifyExecError. This guards against accidental
// reshuffling of the contract DockerRuntime relies on.
func TestRunDockerCommand_NonexistentBinarySurfaces(t *testing.T) {
	t.Parallel()
	_, _, exit, err := runDockerCommand(context.Background(), "/nonexistent/skill-up-docker-fake-binary", "version")
	if err == nil {
		t.Fatal("expected error when running nonexistent binary")
	}
	if exit != -1 {
		t.Fatalf("exitCode = %d, want -1", exit)
	}
	if !strings.Contains(err.Error(), "no such file") && !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "executable file not found") {
		// Different OSes phrase this differently; just make sure we got
		// *some* useful diagnostic.
		t.Logf("note: runDockerCommand surfaced error: %v", err)
	}
}

// Ensure dockerContainerName returns unique, prefixed names.
func TestDockerContainerName(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for i := range 32 {
		n, err := dockerContainerName()
		if err != nil {
			t.Fatalf("name: %v", err)
		}
		if !strings.HasPrefix(n, "skill-up-") {
			t.Fatalf("name %q missing prefix", n)
		}
		if seen[n] {
			t.Fatalf("duplicate name %q on iter %d", n, i)
		}
		seen[n] = true
	}
}

// overlayEnvList must respect callEnv-wins ordering.
func TestOverlayEnvList_CallEnvWins(t *testing.T) {
	t.Parallel()
	got := overlayEnvList(map[string]string{"A": "1", "B": "2"}, map[string]string{"B": "override"})
	// build a map for assertion since order is non-deterministic
	m := map[string]string{}
	for _, kv := range got {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	if m["A"] != "1" || m["B"] != "override" {
		t.Fatalf("unexpected env overlay: %v", m)
	}
}

// Compile-time check: DockerRuntime satisfies Runtime.
var _ Runtime = (*DockerRuntime)(nil)
