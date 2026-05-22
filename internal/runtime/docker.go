package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/observability"
)

const (
	dockerDefaultWorkspace = "/workspace"
	dockerDirMode          = 0o755
	dockerNameRandomBytes  = 6
	// dockerCleanupTimeout bounds best-effort rollback / preserve-path
	// stops so a hung daemon can't keep the runtime mutex held forever.
	// Long enough for a normal `docker rm -f` (a few seconds) plus some
	// margin for an overloaded daemon; short enough that Create or Close
	// still returns to its caller within a useful window even on a stuck
	// daemon.
	dockerCleanupTimeout = 30 * time.Second
)

// dockerCommandRunner is the seam unit tests use to capture or fake the
// invocations DockerRuntime issues against the `docker` CLI. Production code
// uses runDockerCommand, which shells out to whatever `docker` binary is on
// PATH (or the override in DockerRuntime.cli).
type dockerCommandRunner func(ctx context.Context, name string, args ...string) (stdout, stderr string, exitCode int, err error)

// DockerRuntime executes commands inside a local Docker container.
//
// Lifecycle:
//   - Create: docker create with the configured image, entrypoint sleep loop,
//     workspace dir, optional --network=none, optional env from cfg.Env.
//   - Start: docker start.
//   - Stop:  docker stop --time=5.
//   - Close: docker rm -f (skipped when cfg.Delete is false; the container
//     is just stopped so the user can inspect it).
//
// All cross-environment access flows through `docker exec` / `docker cp`, so
// no host bind mount is required. Callers that pass a host-absolute path to
// UploadFile/DownloadFile get a sandboxed copy of that path inside the
// container's workspace tree — the runtime never writes to host paths from
// inside the container.
type DockerRuntime struct {
	cfg       Config
	workspace string

	// cli is the command name used to invoke docker. Defaults to "docker"
	// but tests inject alternatives (e.g. a fake binary on PATH).
	cli string
	// run is the function used to run docker commands. Tests override this
	// to avoid spawning real processes.
	run dockerCommandRunner

	mu          sync.Mutex
	containerID string // set after Create
	started     bool
}

// NewDockerRuntime constructs a DockerRuntime from the shared Config. The
// returned runtime is inert until Create() runs.
func NewDockerRuntime(cfg Config) (*DockerRuntime, error) {
	if strings.TrimSpace(cfg.Image) == "" {
		return nil, errors.New("docker runtime requires environment.image")
	}
	if policy := strings.TrimSpace(strings.ToLower(cfg.NetworkPolicy)); policy == "allow_declared" {
		// allow_declared needs FQDN-level egress filtering. Implementing
		// that for the local docker runtime requires either an egress
		// proxy sidecar or iptables rules in the container — both out of
		// scope for the initial cut. Refuse loudly instead of silently
		// allowing all egress, which would violate the user's policy.
		return nil, errors.New("docker runtime: network_policy=allow_declared is not yet supported; use deny_all or run on opensandbox")
	}
	workspace := strings.TrimSpace(cfg.WorkspaceMount)
	if workspace == "" {
		workspace = dockerDefaultWorkspace
	}
	if !path.IsAbs(workspace) {
		return nil, fmt.Errorf("docker runtime: workspace_mount must be absolute, got %q", workspace)
	}
	return &DockerRuntime{
		cfg:       cfg,
		workspace: path.Clean(workspace),
		cli:       "docker",
		run:       runDockerCommand,
	}, nil
}

// Create provisions the container and starts it so subsequent Exec / Upload /
// Download calls work without an explicit Start. The evaluator does not call
// Start between Create and the first Exec, so Create must leave the runtime
// fully ready (mirrors OpenSandboxRuntime, where Create both provisions and
// connects the sandbox).
func (r *DockerRuntime) Create(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.containerID != "" {
		return nil
	}

	name, err := dockerContainerName()
	if err != nil {
		return fmt.Errorf("docker runtime: generate container name: %w", err)
	}

	args := []string{
		"create",
		"--name", name,
		"--workdir", r.workspace,
	}
	if policy := strings.TrimSpace(strings.ToLower(r.cfg.NetworkPolicy)); policy == "deny_all" {
		args = append(args, "--network", "none")
	}
	for k, v := range r.cfg.Env {
		args = append(args, "--env", k+"="+v)
	}
	// Persistent entrypoint so exec calls can attach. `sleep infinity` is
	// supported in busybox 1.30+, Alpine 3.10+, Debian/Ubuntu coreutils,
	// and most distroless bases; users with stricter base images can
	// override via environment.entrypoint.
	//
	// Docker treats positional args after the image as CMD, NOT as an
	// ENTRYPOINT override. If the image already declares an ENTRYPOINT
	// (e.g. `python`, `java -jar app.jar`), our `sleep infinity` would
	// arrive as arguments to that entrypoint and the container would
	// exit immediately. Using `--entrypoint` explicitly replaces the
	// image's entrypoint with the user's chosen binary, and any
	// remaining args go into the CMD slot.
	entry := r.cfg.Entrypoint
	if len(entry) == 0 {
		entry = []string{"sleep", "infinity"}
	}
	args = append(args, "--entrypoint", entry[0])
	args = append(args, r.cfg.Image)
	if len(entry) > 1 {
		args = append(args, entry[1:]...)
	}

	stdout, stderr, exitCode, err := r.run(ctx, r.cli, args...)
	if err != nil || exitCode != 0 {
		return dockerCLIErr(stderr, exitCode, err, "docker create failed")
	}
	id := strings.TrimSpace(stdout)
	if id == "" {
		// `docker create --name X` echoes the long ID on success; if it
		// doesn't, fall back to the name we chose so Close can still
		// reach the container.
		id = name
	}
	r.containerID = id

	// Start the container so Exec / docker cp work immediately. If start
	// fails, tear down the half-created container to avoid leaking it on
	// the host; otherwise the error returned to the caller would leave a
	// "Created"-state container hanging around forever.
	_, startStderr, startExit, startErr := r.run(ctx, r.cli, "start", id)
	if startErr != nil || startExit != 0 {
		// best-effort cleanup; ignore failure here since we already have
		// a more informative error to return. Bounded by dockerCleanupTimeout
		// so a hung daemon can't keep r.mu held forever — without the
		// timeout, context.WithoutCancel drops the parent deadline and a
		// stuck docker would deadlock Create indefinitely.
		r.rollbackRemove(ctx, id)
		r.containerID = ""
		return dockerCLIErr(startStderr, startExit, startErr, "docker start %s failed", id)
	}
	r.started = true

	// Defensively create the workspace dir. `--workdir` creates the dir
	// for `docker run`, but in the `create + start` two-step the
	// directory is only guaranteed to exist when the first exec runs
	// with `--workdir` — and if it doesn't, that first exec errors out
	// with an opaque "no such file or directory" message. mkdir -p
	// here surfaces any real failure (read-only fs, permissions) right
	// at Create() time instead.
	if _, mkStderr, mkExit, mkErr := r.run(ctx, r.cli, "exec", id, "mkdir", "-p", r.workspace); mkErr != nil || mkExit != 0 {
		r.rollbackRemove(ctx, id)
		r.containerID = ""
		r.started = false
		return dockerCLIErr(mkStderr, mkExit, mkErr, "docker exec mkdir -p %s failed", r.workspace)
	}
	return nil
}

// rollbackRemove tears down a half-created container after a failure in
// Create. Detached from the caller's context so cancellation/deadline of
// the original call doesn't skip cleanup, but bounded by dockerCleanupTimeout
// so a wedged daemon still releases the runtime mutex within a usable
// window. The cleanup is best-effort: any error is swallowed because we
// already have a more informative one to return to the caller.
func (r *DockerRuntime) rollbackRemove(_ context.Context, id string) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), dockerCleanupTimeout)
	defer cancel()
	_, _, _, _ = r.run(cleanupCtx, r.cli, "rm", "-f", id)
}

// Close removes the container (when cfg.Delete is true) or just stops it.
//
// On the rm path the container handle is kept until rm -f reports success —
// if the daemon hiccups, the caller can re-invoke Close on the same runtime
// to retry cleanup. The preserve path (cfg.Delete=false) intentionally
// clears the handle because the user has opted out of cleanup; the
// container is theirs to manage from there.
func (r *DockerRuntime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.containerID == "" {
		return nil
	}
	ctx := context.Background()
	id := r.containerID

	if !r.cfg.Delete {
		logging.Debugf("DockerRuntime.Close: skipping rm, container preserved: %s", id)
		// best-effort stop; ignore error so the user can still attach.
		_, _, _, _ = r.run(ctx, r.cli, "stop", "--time", "5", id)
		r.containerID = ""
		r.started = false
		return nil
	}
	_, stderr, exitCode, err := r.run(ctx, r.cli, "rm", "-f", id)
	if err != nil || exitCode != 0 {
		// Leave r.containerID / r.started intact so the caller can retry
		// Close after the transient docker / daemon error clears.
		return dockerCLIErr(stderr, exitCode, err, "docker rm -f %s failed", id)
	}
	r.containerID = ""
	r.started = false
	return nil
}

// Start starts the container if it is not already running.
func (r *DockerRuntime) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.containerID == "" {
		return errors.New("docker runtime: Start called before Create")
	}
	if r.started {
		return nil
	}
	_, stderr, exitCode, err := r.run(ctx, r.cli, "start", r.containerID)
	if err != nil || exitCode != 0 {
		return dockerCLIErr(stderr, exitCode, err, "docker start %s failed", r.containerID)
	}
	r.started = true
	return nil
}

// Stop stops the container with a graceful timeout. Idempotent.
func (r *DockerRuntime) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.containerID == "" || !r.started {
		return nil
	}
	_, stderr, exitCode, err := r.run(ctx, r.cli, "stop", "--time", "5", r.containerID)
	if err != nil || exitCode != 0 {
		return dockerCLIErr(stderr, exitCode, err, "docker stop %s failed", r.containerID)
	}
	r.started = false
	return nil
}

// UploadFile copies a single file from the host into the container.
// targetPath may be relative to the workspace or an absolute container path.
func (r *DockerRuntime) UploadFile(ctx context.Context, sourcePath, targetPath string) error {
	id, err := r.snapshotContainerID()
	if err != nil {
		return err
	}
	target := r.remotePath(targetPath)
	if err := r.ensureRemoteDir(ctx, id, path.Dir(target)); err != nil {
		return err
	}
	_, stderr, exitCode, err := r.run(ctx, r.cli, "cp", sourcePath, id+":"+target)
	if err != nil || exitCode != 0 {
		return dockerCLIErr(stderr, exitCode, err, "docker cp %s -> %s failed", sourcePath, target)
	}
	return nil
}

// UploadDir recursively copies a directory tree from the host into the container.
func (r *DockerRuntime) UploadDir(ctx context.Context, sourceDir, targetDir string) error {
	id, err := r.snapshotContainerID()
	if err != nil {
		return err
	}
	target := r.remotePath(targetDir)
	if err := r.ensureRemoteDir(ctx, id, target); err != nil {
		return err
	}
	// `docker cp <srcDir>/. <ctr>:<dst>` copies contents into dst, which
	// matches the UploadDir contract (host srcDir tree → container dst tree).
	src := strings.TrimRight(sourceDir, string(filepath.Separator)) + string(filepath.Separator) + "."
	_, stderr, exitCode, err := r.run(ctx, r.cli, "cp", src, id+":"+target)
	if err != nil || exitCode != 0 {
		return dockerCLIErr(stderr, exitCode, err, "docker cp -r %s -> %s failed", sourceDir, target)
	}
	return nil
}

// DownloadFile copies a single file from the container to the host.
func (r *DockerRuntime) DownloadFile(ctx context.Context, sourcePath, targetPath string) error {
	id, err := r.snapshotContainerID()
	if err != nil {
		return err
	}
	source := r.remotePath(sourcePath)
	if err := os.MkdirAll(filepath.Dir(targetPath), dockerDirMode); err != nil {
		return fmt.Errorf("docker runtime: create local target dir: %w", err)
	}
	_, stderr, exitCode, err := r.run(ctx, r.cli, "cp", id+":"+source, targetPath)
	if err != nil || exitCode != 0 {
		return dockerCLIErr(stderr, exitCode, err, "docker cp %s -> %s failed", source, targetPath)
	}
	return nil
}

// DownloadDir recursively copies a directory from the container to the host.
func (r *DockerRuntime) DownloadDir(ctx context.Context, sourceDir, targetDir string) error {
	id, err := r.snapshotContainerID()
	if err != nil {
		return err
	}
	source := r.remotePath(sourceDir)
	if err := os.MkdirAll(targetDir, dockerDirMode); err != nil {
		return fmt.Errorf("docker runtime: create local target dir: %w", err)
	}
	// `<ctr>:<srcDir>/.` → host targetDir copies contents into targetDir.
	src := strings.TrimRight(source, "/") + "/."
	_, stderr, exitCode, err := r.run(ctx, r.cli, "cp", id+":"+src, targetDir)
	if err != nil || exitCode != 0 {
		return dockerCLIErr(stderr, exitCode, err, "docker cp -r %s -> %s failed", source, targetDir)
	}
	return nil
}

// Exec runs a bash command inside the container.
func (r *DockerRuntime) Exec(ctx context.Context, command string, opts ExecOptions) (ExecResult, error) {
	id, err := r.snapshotContainerID()
	if err != nil {
		return ExecResult{}, err
	}

	ctx, span := observability.Tracer().Start(ctx, "runtime.exec")
	defer span.End()
	startTime := time.Now()

	if opts.TimeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.TimeoutSec)*time.Second)
		defer cancel()
	}

	args := []string{"exec"}
	cwd := opts.Cwd
	if cwd == "" {
		cwd = r.workspace
	}
	if !path.IsAbs(cwd) {
		cwd = path.Join(r.workspace, cwd)
	}
	args = append(args, "--workdir", cwd)
	// mergeEnv covers cfg.Env + opts.Env, but here we want to keep the
	// container's own env (PATH, HOME, ...) intact and just layer the
	// caller-supplied vars on top — so pass only the merged overlay to
	// docker, not the full host env.
	for _, kv := range overlayEnvList(r.cfg.Env, opts.Env) {
		args = append(args, "--env", kv)
	}
	// Use `sh -c` rather than `bash -c` so the docker runtime works on
	// minimal base images (alpine, distroless, busybox, plain debian
	// without bash). All shell snippets the rest of the codebase ships
	// (setup_steps, judge scripts, agent commands) are POSIX-compatible.
	args = append(args, id, "sh", "-c", command)
	span.SetAttributes(
		attribute.String("process.command", command),
		attribute.String("process.cwd", cwd),
		attribute.String("runtime.type", "docker"),
	)

	stdout, stderr, exitCode, err := r.run(ctx, r.cli, args...)
	result := ExecResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
	}
	span.SetAttributes(attribute.Int("process.exit_code", result.ExitCode))
	observability.RecordRuntimeExec(ctx, result.ExitCode, time.Since(startTime).Milliseconds())

	switch {
	case result.ExitCode == -1 && ctx.Err() != nil:
		logging.ErrorContextf(ctx, "docker exec killed by context (%v); command: %s", ctx.Err(), maskCommand(command))
		if result.Stderr != "" {
			logNonZeroStderr(ctx, result.ExitCode, result.Stderr)
		}
		return result, ctx.Err()
	case result.ExitCode != 0:
		logNonZeroExit(ctx, result.ExitCode, command)
		if result.Stderr != "" {
			logNonZeroStderr(ctx, result.ExitCode, result.Stderr)
		}
	case result.Stderr != "":
		logging.WarnContextf(ctx, "stderr: %s", result.Stderr)
	}

	// Two distinct failure modes need to surface as errors so the
	// evaluator routes them through its infra-retry path instead of the
	// ordinary FAIL path:
	//
	//   1. r.run itself returned err — docker CLI couldn't run, or the
	//      parent context was cancelled / timed out.
	//   2. docker exec returned a non-zero exit AND stderr looks like a
	//      daemon / OCI runtime fault (missing container, runtime exec
	//      failed, etc) — see dockerExecLayerError. A user command that
	//      legitimately exits non-zero must NOT be wrapped; the
	//      evaluator inspects ExitCode for those.
	if err != nil {
		return result, dockerCLIErr(result.Stderr, result.ExitCode, err, "docker exec failed")
	}
	if result.ExitCode != 0 && dockerExecLayerError(result.Stderr, result.ExitCode) {
		return result, dockerCLIErr(result.Stderr, result.ExitCode, nil, "docker exec layer failure")
	}
	return result, nil
}

// Workspace returns the in-container workspace path. Unlike NoneRuntime, this
// path is not directly accessible from the host — callers must use
// UploadFile/DownloadFile to move data in and out.
func (r *DockerRuntime) Workspace() string {
	return r.workspace
}

// RequiresProcessSandbox reports that container isolation already constrains
// agent execution; agents do not need to enable their own process sandbox.
func (r *DockerRuntime) RequiresProcessSandbox() bool {
	return false
}

// snapshotContainerID returns the current container id under the mutex,
// so callers can use the captured local value through the rest of their
// method without racing a concurrent Close. The lock is released on
// return — `docker exec` / `docker cp` may take many seconds and holding
// the lock for their duration would serialise every operation on the
// runtime. If Close fires while exec/cp is in flight, the captured id
// becomes stale and docker returns a "No such container" error, which
// dockerExecLayerError catches and surfaces as a layer fault — much
// better than a panic on a half-cleared field.
func (r *DockerRuntime) snapshotContainerID() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.containerID == "" {
		return "", errors.New("docker runtime: container not created (call Create first)")
	}
	return r.containerID, nil
}

// ensureRemoteDir takes an explicit container id rather than reading
// r.containerID so callers can keep using the snapshot captured under the
// mutex at the top of their method. Avoids a second lock-and-read race.
func (r *DockerRuntime) ensureRemoteDir(ctx context.Context, id, dir string) error {
	if dir == "" || dir == "/" {
		return nil
	}
	_, stderr, exitCode, err := r.run(ctx, r.cli, "exec", id, "mkdir", "-p", dir)
	if err != nil || exitCode != 0 {
		return dockerCLIErr(stderr, exitCode, err, "docker exec mkdir -p %s failed", dir)
	}
	return nil
}

// remotePath returns p if absolute, otherwise joins it under r.workspace.
func (r *DockerRuntime) remotePath(p string) string {
	c := path.Clean(p)
	if path.IsAbs(c) {
		return c
	}
	return path.Join(r.workspace, c)
}

// dockerCLIErr formats a "docker CLI invocation failed" error. The CLI exits
// non-zero with err==nil for ordinary process failures (runDockerCommand
// returns err only when docker itself couldn't run), so a blind `%w, err`
// renders `%!w(<nil>)` and breaks errors.Unwrap downstream. This helper
// conditionally appends `: %w` only when there's a real error to wrap.
func dockerCLIErr(stderr string, exitCode int, err error, format string, args ...any) error {
	cleanStderr := strings.TrimSpace(stderr)
	args = append(args, exitCode, cleanStderr)
	if err == nil {
		return fmt.Errorf(format+" (exit=%d): %s", args...)
	}
	return fmt.Errorf(format+" (exit=%d): %s: %w", append(args, err)...)
}

// dockerExecLayerError reports whether stderr from a non-zero `docker exec`
// looks like a daemon / OCI runtime fault rather than the user's command
// exiting non-zero on its own. These need to surface as Exec errors so the
// evaluator's infra-fault retry path triggers instead of treating them as
// ordinary FAIL results.
//
// docker exec exit-code conventions:
//
//	125 — daemon-level error (couldn't accept the exec request at all)
//	126 — container exists but the command cannot be invoked
//	127 — command not found inside the container
//
// Plus daemon-emitted stderr that means "this never reached the user's
// command". Match prefixes only (not bare substrings) so a user script
// that legitimately prints e.g. `redis is not running` and exits 1 isn't
// misclassified as an infra fault.
//
//	"Error response from daemon:"  — daemon refused (covers the
//	                                  stopped-container variant
//	                                  "Error response from daemon:
//	                                  Container X is not running")
//	"Error: No such container"     — container vanished
//	"OCI runtime exec failed:"     — runtime couldn't set up the
//	                                  process (always emitted with the
//	                                  trailing colon by runc/crun)
//
// 125 alone is unambiguous; the prefixes catch cases where the daemon
// mapped its failure to a different exit code.
func dockerExecLayerError(stderr string, exitCode int) bool {
	if exitCode == 125 {
		return true
	}
	s := strings.TrimSpace(stderr)
	switch {
	case strings.HasPrefix(s, "Error response from daemon:"),
		strings.HasPrefix(s, "Error: No such container"),
		strings.HasPrefix(s, "OCI runtime exec failed:"):
		return true
	}
	return false
}

func dockerContainerName() (string, error) {
	buf := make([]byte, dockerNameRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "skill-up-" + hex.EncodeToString(buf), nil
}

// overlayEnvList returns just the union of persistentEnv and callEnv as
// KEY=VALUE strings (callEnv wins on key conflicts). Values are passed
// LITERALLY — no host-env expansion happens here.
//
// Expanding `$VAR` against the host's environment before passing the value
// to `docker --env` is dangerous: it silently rewrites container-relative
// variables like PATH/HOME/USER with whatever the host's value happens to
// be, which breaks command lookup inside otherwise valid images (e.g.
// passing `PATH=$PATH:/tooling` would clobber the image's PATH with the
// macOS `/opt/homebrew/...`).
//
// Users who genuinely need a host value forwarded into the container
// should write the literal expansion themselves at the call site, or use
// a setup_step that sources it inside the container.
func overlayEnvList(persistentEnv, callEnv map[string]string) []string {
	overlay := mergeEnvMaps(persistentEnv, callEnv)
	if len(overlay) == 0 {
		return nil
	}
	out := make([]string, 0, len(overlay))
	for k, v := range overlay {
		out = append(out, k+"="+v)
	}
	return out
}

// runDockerCommand executes the docker CLI in a child process and returns
// stdout, stderr, exit code, and an error if the process could not be run.
// A non-zero docker exit code is NOT wrapped in err — callers inspect
// exitCode directly so they can distinguish "command ran and failed" from
// "could not run docker at all".
func runDockerCommand(ctx context.Context, name string, args ...string) (stdout, stderr string, exitCode int, err error) {
	//nolint:gosec // name is the docker CLI binary; args are constructed from validated config.
	cmd := exec.CommandContext(ctx, name, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	code, classified := classifyExecError(ctx, runErr)
	return outBuf.String(), errBuf.String(), code, classified
}
