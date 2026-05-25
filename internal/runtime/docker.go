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
	"github.com/alibaba/skill-up/internal/platform"
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

	args := r.buildCreateArgs(name)
	stdout, stderr, exitCode, err := r.run(ctx, r.cli, args...)
	if err != nil || exitCode != 0 {
		return dockerCLIErr(stderr, exitCode, err, "docker create failed")
	}
	id := strings.TrimSpace(stdout)
	if id == "" {
		id = name
	}
	r.containerID = id

	_, startStderr, startExit, startErr := r.run(ctx, r.cli, "start", id)
	if startErr != nil || startExit != 0 {
		primary := dockerCLIErr(startStderr, startExit, startErr, "docker start %s failed", id)
		if rbErr := r.rollbackRemove(ctx, id); rbErr != nil {
			return errors.Join(primary, rbErr)
		}
		r.containerID = ""
		return primary
	}
	r.started = true

	if _, mkStderr, mkExit, mkErr := r.run(ctx, r.cli, "exec", id, "mkdir", "-p", r.workspace); mkErr != nil || mkExit != 0 {
		primary := dockerCLIErr(mkStderr, mkExit, mkErr, "docker exec mkdir -p %s failed", r.workspace)
		if rbErr := r.rollbackRemove(ctx, id); rbErr != nil {
			return errors.Join(primary, rbErr)
		}
		r.containerID = ""
		r.started = false
		return primary
	}
	return nil
}

// buildCreateArgs assembles the `docker create` argument list from the
// runtime's configuration. Extracted from Create to keep cyclomatic
// complexity manageable.
func (r *DockerRuntime) buildCreateArgs(name string) []string {
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
	entry := r.cfg.Entrypoint
	if len(entry) == 0 {
		entry = []string{"sleep", "infinity"}
	}
	args = append(args, "--entrypoint", entry[0])
	args = append(args, r.cfg.Image)
	if len(entry) > 1 {
		args = append(args, entry[1:]...)
	}
	return args
}

// rollbackRemove tears down a half-created container after a failure in
// Create. Detached from the caller's context so cancellation/deadline of
// the original call doesn't skip cleanup, but bounded by dockerCleanupTimeout
// so a wedged daemon still releases the runtime mutex within a usable
// window. Returns nil when the container was successfully removed, or an
// error if cleanup failed (in which case the caller should retain
// containerID so a subsequent Close can retry).
//
//nolint:contextcheck // Intentionally detached: parent cancellation must not skip cleanup.
func (r *DockerRuntime) rollbackRemove(_ context.Context, id string) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), dockerCleanupTimeout)
	defer cancel()
	_, stderr, exitCode, err := r.run(cleanupCtx, r.cli, "rm", "-f", id)
	if err != nil || exitCode != 0 {
		logging.DebugContextf(cleanupCtx, "DockerRuntime.rollbackRemove: rm -f %s failed (exit=%d): %s", id, exitCode, strings.TrimSpace(stderr))
		return dockerCLIErr(stderr, exitCode, err, "docker rm -f %s failed during rollback", id)
	}
	return nil
}

// Close removes the container (when cfg.Delete is true) or just stops it.
//
// On the rm path the container handle is kept until rm -f reports success —
// if the daemon hiccups, the caller can re-invoke Close on the same runtime
// to retry cleanup. The preserve path (cfg.Delete=false) intentionally
// clears the handle because the user has opted out of cleanup; the
// container is theirs to manage from there.
//
// Close uses a bounded context (dockerCleanupTimeout) so a wedged daemon
// cannot block the caller indefinitely.
func (r *DockerRuntime) Close() error {
	r.mu.Lock()
	if r.containerID == "" {
		r.mu.Unlock()
		return nil
	}
	id := r.containerID
	shouldDelete := r.cfg.Delete
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), dockerCleanupTimeout)
	defer cancel()

	if !shouldDelete {
		logging.Debugf("DockerRuntime.Close: skipping rm, container preserved: %s", id)
		_, stderr, exitCode, err := r.run(ctx, r.cli, "stop", "--time", "5", id)
		if err != nil || exitCode != 0 {
			return dockerCLIErr(stderr, exitCode, err, "docker stop %s failed (preserve path)", id)
		}
		r.mu.Lock()
		r.containerID = ""
		r.started = false
		r.mu.Unlock()
		return nil
	}
	_, stderr, exitCode, err := r.run(ctx, r.cli, "rm", "-f", id)
	r.mu.Lock()
	if err != nil || exitCode != 0 {
		r.mu.Unlock()
		return dockerCLIErr(stderr, exitCode, err, "docker rm -f %s failed", id)
	}
	r.containerID = ""
	r.started = false
	r.mu.Unlock()
	return nil
}

// Start starts the container if it is not already running.
// TargetGOOS reports the GOOS of the container's guest OS. skill-up's
// docker runtime currently provisions a Linux image, so commands executed
// via `docker exec` run on a Linux guest regardless of the host platform.
func (r *DockerRuntime) TargetGOOS() string { return platform.GOOSLinux }

// Start starts the container if it is not already running.
func (r *DockerRuntime) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.containerID == "" {
		r.mu.Unlock()
		return errors.New("docker runtime: Start called before Create")
	}
	if r.started {
		r.mu.Unlock()
		return nil
	}
	id := r.containerID
	r.mu.Unlock()

	_, stderr, exitCode, err := r.run(ctx, r.cli, "start", id)
	if err != nil || exitCode != 0 {
		return dockerCLIErr(stderr, exitCode, err, "docker start %s failed", id)
	}

	r.mu.Lock()
	r.started = true
	r.mu.Unlock()
	return nil
}

// Stop stops the container with a graceful timeout. Idempotent.
func (r *DockerRuntime) Stop(ctx context.Context) error {
	r.mu.Lock()
	if r.containerID == "" || !r.started {
		r.mu.Unlock()
		return nil
	}
	id := r.containerID
	r.mu.Unlock()

	_, stderr, exitCode, err := r.run(ctx, r.cli, "stop", "--time", "5", id)
	if err != nil || exitCode != 0 {
		return dockerCLIErr(stderr, exitCode, err, "docker stop %s failed", id)
	}

	r.mu.Lock()
	r.started = false
	r.mu.Unlock()
	return nil
}

// UploadFile copies a single file from the host into the container.
// targetPath may be relative to the workspace or an absolute container path.
func (r *DockerRuntime) UploadFile(ctx context.Context, sourcePath, targetPath string) error {
	id, err := r.snapshotContainerID()
	if err != nil {
		return err
	}
	target, err := r.remotePath(targetPath)
	if err != nil {
		return err
	}
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
	target, err := r.remotePath(targetDir)
	if err != nil {
		return err
	}
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
	source, err := r.remotePath(sourcePath)
	if err != nil {
		return err
	}
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
	source, err := r.remotePath(sourceDir)
	if err != nil {
		return err
	}
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
		if !isSubPath(r.workspace, cwd) {
			return ExecResult{}, fmt.Errorf("docker runtime: cwd %q escapes workspace %q", opts.Cwd, r.workspace)
		}
	}
	args = append(args, "--workdir", cwd)
	// Pass only the cfg.Env + opts.Env overlay to docker, not the full
	// host env, so the container's own PATH/HOME/... stay intact and
	// only caller-supplied vars layer on top. Values are forwarded
	// literally; callers that need shell expansion (e.g. an agent's
	// $HOME-referencing PATH) should resolve the value first and pass
	// the literal — see internal/agent.probeAndMergePATH.
	for _, kv := range overlayEnvList(r.cfg.Env, opts.Env) {
		args = append(args, "--env", kv)
	}
	// Use `sh -c` rather than `bash -c` so the docker runtime works on
	// minimal base images (alpine, busybox, plain debian without bash).
	// All shell snippets the rest of the codebase ships (setup_steps,
	// judge scripts, agent commands) are POSIX-compatible.
	//
	// Shell-less images (distroless, scratch + a single binary) are NOT
	// supported: skill-up's evaluation lifecycle (setup_steps, agent
	// install, MCP install, judge scripts) is shell-driven end-to-end,
	// so an image without /bin/sh isn't usable here even if Exec
	// itself bypassed `sh -c`. Pick a base image with a POSIX shell.
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

	return result, classifyExecResult(ctx, result, err, command)
}

// classifyExecResult determines whether a docker exec invocation should
// surface as an error (infra fault, context timeout) or as a normal non-zero
// exit that the evaluator scores via ExitCode alone.
func classifyExecResult(ctx context.Context, result ExecResult, err error, command string) error {
	switch {
	case result.ExitCode == -1 && ctx.Err() != nil:
		logging.ErrorContextf(ctx, "docker exec killed by context (%v); command: %s", ctx.Err(), maskCommand(command))
		if result.Stderr != "" {
			logNonZeroStderr(ctx, result.ExitCode, result.Stderr)
		}
		if result.Stdout != "" {
			logNonZeroStdout(ctx, result.ExitCode, result.Stdout)
		}
		return ctx.Err()
	case result.ExitCode != 0:
		logNonZeroExit(ctx, result.ExitCode, command)
		if result.Stderr != "" {
			logNonZeroStderr(ctx, result.ExitCode, result.Stderr)
		}
		if result.Stdout != "" {
			logNonZeroStdout(ctx, result.ExitCode, result.Stdout)
		}
	case result.Stderr != "":
		logging.WarnContextf(ctx, "stderr: %s", result.Stderr)
	}

	if err != nil {
		return dockerCLIErr(result.Stderr, result.ExitCode, err, "docker exec failed")
	}
	if result.ExitCode != 0 && dockerExecLayerError(result.Stderr) {
		return dockerCLIErr(result.Stderr, result.ExitCode, nil, "docker exec layer failure")
	}
	return nil
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

// MergeEnv layers entries into the runtime's persistent env baseline. See
// Runtime.MergeEnv for the contract. Note: the container's entrypoint (e.g.
// `sleep infinity`) is started with the env present at Create time, so
// post-Create MergeEnv calls only affect subsequent `docker exec`
// invocations, not the long-running entrypoint process.
func (r *DockerRuntime) MergeEnv(env map[string]string) {
	mergeIntoEnvBaseline(&r.cfg.Env, env)
}

// TargetGOOS reports "linux": Docker containers running skill-up evals are
// always Linux images regardless of the host OS.
func (r *DockerRuntime) TargetGOOS() string {
	return "linux"
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
// Relative inputs that escape the workspace via `..` are rejected — without
// the isSubPath check, `path.Join("/workspace", "../etc/shadow")` normalizes
// to `/etc/shadow`, letting Upload/Download cross the workspace boundary
// while callers think they passed a relative path. Mirrors the cwd-escape
// check in Exec.
func (r *DockerRuntime) remotePath(p string) (string, error) {
	c := path.Clean(p)
	if path.IsAbs(c) {
		return c, nil
	}
	joined := path.Join(r.workspace, c)
	if !isSubPath(r.workspace, joined) {
		return "", fmt.Errorf("docker runtime: relative path %q escapes workspace %q", p, r.workspace)
	}
	return joined, nil
}

// isSubPath reports whether child is equal to parent or is a subdirectory
// of parent. Both paths are cleaned before comparison.
func isSubPath(parent, child string) bool {
	p := path.Clean(parent)
	c := path.Clean(child)
	if c == p {
		return true
	}
	return strings.HasPrefix(c, p+"/")
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
// Match prefixes only (not bare substrings) so a user script that
// legitimately prints e.g. `redis is not running` and exits 1 isn't
// misclassified as an infra fault.
//
//	"Error response from daemon:"            — daemon refused (covers
//	                                            the stopped-container
//	                                            variant "Error response
//	                                            from daemon: Container
//	                                            X is not running")
//	"Error: No such container"               — container vanished
//	"OCI runtime exec failed:"               — runtime couldn't set
//	                                            up the process (always
//	                                            emitted with the
//	                                            trailing colon by
//	                                            runc/crun)
//	"Cannot connect to the Docker daemon"    — CLI couldn't reach the
//	                                            daemon at all (daemon
//	                                            down, wrong socket path,
//	                                            permission denied on
//	                                            socket); always prefix.
//	"error during connect:"                  — newer docker CLI variant
//	                                            of the same disconnect
//	                                            failure (TCP/TLS-backed
//	                                            daemons in particular).
//
// Exit code is intentionally not used: docker-container-exec(1) only
// reserves 126/127 for Docker's own use, and a user command that exits
// 125 with empty stderr is indistinguishable from a daemon failure that
// somehow emitted no stderr — be conservative and classify by stderr
// only. Real daemon errors always print one of the prefixes above.
func dockerExecLayerError(stderr string) bool {
	s := strings.TrimSpace(stderr)
	switch {
	case strings.HasPrefix(s, "Error response from daemon:"),
		strings.HasPrefix(s, "Error: No such container"),
		strings.HasPrefix(s, "OCI runtime exec failed:"),
		strings.HasPrefix(s, "Cannot connect to the Docker daemon"),
		strings.HasPrefix(s, "error during connect:"):
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
