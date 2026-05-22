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
	entry := r.cfg.Entrypoint
	if len(entry) == 0 {
		entry = []string{"sleep", "infinity"}
	}
	args = append(args, r.cfg.Image)
	args = append(args, entry...)

	stdout, stderr, exitCode, err := r.run(ctx, r.cli, args...)
	if err != nil || exitCode != 0 {
		return fmt.Errorf("docker create failed (exit=%d): %s: %w", exitCode, strings.TrimSpace(stderr), err)
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
		// a more informative error to return.
		_, _, _, _ = r.run(context.WithoutCancel(ctx), r.cli, "rm", "-f", id)
		r.containerID = ""
		return fmt.Errorf("docker start %s failed (exit=%d): %s: %w", id, startExit, strings.TrimSpace(startStderr), startErr)
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
		_, _, _, _ = r.run(context.WithoutCancel(ctx), r.cli, "rm", "-f", id)
		r.containerID = ""
		r.started = false
		return fmt.Errorf("docker exec mkdir -p %s failed (exit=%d): %s: %w", r.workspace, mkExit, strings.TrimSpace(mkStderr), mkErr)
	}
	return nil
}

// Close removes the container (when cfg.Delete is true) or just stops it.
func (r *DockerRuntime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.containerID == "" {
		return nil
	}
	ctx := context.Background()
	id := r.containerID
	r.containerID = ""
	r.started = false

	if !r.cfg.Delete {
		logging.Debugf("DockerRuntime.Close: skipping rm, container preserved: %s", id)
		// best-effort stop; ignore error so the user can still attach.
		_, _, _, _ = r.run(ctx, r.cli, "stop", "--time", "5", id)
		return nil
	}
	_, stderr, exitCode, err := r.run(ctx, r.cli, "rm", "-f", id)
	if err != nil || exitCode != 0 {
		return fmt.Errorf("docker rm -f %s failed (exit=%d): %s: %w", id, exitCode, strings.TrimSpace(stderr), err)
	}
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
		return fmt.Errorf("docker start %s failed (exit=%d): %s: %w", r.containerID, exitCode, strings.TrimSpace(stderr), err)
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
		return fmt.Errorf("docker stop %s failed (exit=%d): %s: %w", r.containerID, exitCode, strings.TrimSpace(stderr), err)
	}
	r.started = false
	return nil
}

// UploadFile copies a single file from the host into the container.
// targetPath may be relative to the workspace or an absolute container path.
func (r *DockerRuntime) UploadFile(ctx context.Context, sourcePath, targetPath string) error {
	if err := r.ensureCreated(); err != nil {
		return err
	}
	target := r.remotePath(targetPath)
	if err := r.ensureRemoteDir(ctx, path.Dir(target)); err != nil {
		return err
	}
	_, stderr, exitCode, err := r.run(ctx, r.cli, "cp", sourcePath, r.containerID+":"+target)
	if err != nil || exitCode != 0 {
		return fmt.Errorf("docker cp %s -> %s failed (exit=%d): %s: %w", sourcePath, target, exitCode, strings.TrimSpace(stderr), err)
	}
	return nil
}

// UploadDir recursively copies a directory tree from the host into the container.
func (r *DockerRuntime) UploadDir(ctx context.Context, sourceDir, targetDir string) error {
	if err := r.ensureCreated(); err != nil {
		return err
	}
	target := r.remotePath(targetDir)
	if err := r.ensureRemoteDir(ctx, target); err != nil {
		return err
	}
	// `docker cp <srcDir>/. <ctr>:<dst>` copies contents into dst, which
	// matches the UploadDir contract (host srcDir tree → container dst tree).
	src := strings.TrimRight(sourceDir, string(filepath.Separator)) + string(filepath.Separator) + "."
	_, stderr, exitCode, err := r.run(ctx, r.cli, "cp", src, r.containerID+":"+target)
	if err != nil || exitCode != 0 {
		return fmt.Errorf("docker cp -r %s -> %s failed (exit=%d): %s: %w", sourceDir, target, exitCode, strings.TrimSpace(stderr), err)
	}
	return nil
}

// DownloadFile copies a single file from the container to the host.
func (r *DockerRuntime) DownloadFile(ctx context.Context, sourcePath, targetPath string) error {
	if err := r.ensureCreated(); err != nil {
		return err
	}
	source := r.remotePath(sourcePath)
	if err := os.MkdirAll(filepath.Dir(targetPath), dockerDirMode); err != nil {
		return fmt.Errorf("docker runtime: create local target dir: %w", err)
	}
	_, stderr, exitCode, err := r.run(ctx, r.cli, "cp", r.containerID+":"+source, targetPath)
	if err != nil || exitCode != 0 {
		return fmt.Errorf("docker cp %s -> %s failed (exit=%d): %s: %w", source, targetPath, exitCode, strings.TrimSpace(stderr), err)
	}
	return nil
}

// DownloadDir recursively copies a directory from the container to the host.
func (r *DockerRuntime) DownloadDir(ctx context.Context, sourceDir, targetDir string) error {
	if err := r.ensureCreated(); err != nil {
		return err
	}
	source := r.remotePath(sourceDir)
	if err := os.MkdirAll(targetDir, dockerDirMode); err != nil {
		return fmt.Errorf("docker runtime: create local target dir: %w", err)
	}
	// `<ctr>:<srcDir>/.` → host targetDir copies contents into targetDir.
	src := strings.TrimRight(source, "/") + "/."
	_, stderr, exitCode, err := r.run(ctx, r.cli, "cp", r.containerID+":"+src, targetDir)
	if err != nil || exitCode != 0 {
		return fmt.Errorf("docker cp -r %s -> %s failed (exit=%d): %s: %w", source, targetDir, exitCode, strings.TrimSpace(stderr), err)
	}
	return nil
}

// Exec runs a bash command inside the container.
func (r *DockerRuntime) Exec(ctx context.Context, command string, opts ExecOptions) (ExecResult, error) {
	if err := r.ensureCreated(); err != nil {
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
	args = append(args, r.containerID, "bash", "-c", command)
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

	// docker exec returns its own non-zero exit when the container is gone
	// or the command failed to start; surface the error to callers without
	// mistaking it for a script-level non-zero exit.
	if err != nil && exitCode <= 0 {
		return result, fmt.Errorf("docker exec failed: %w", err)
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

func (r *DockerRuntime) ensureCreated() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.containerID == "" {
		return errors.New("docker runtime: container not created (call Create first)")
	}
	return nil
}

func (r *DockerRuntime) ensureRemoteDir(ctx context.Context, dir string) error {
	if dir == "" || dir == "/" {
		return nil
	}
	_, stderr, exitCode, err := r.run(ctx, r.cli, "exec", r.containerID, "mkdir", "-p", dir)
	if err != nil || exitCode != 0 {
		return fmt.Errorf("docker exec mkdir -p %s failed (exit=%d): %s: %w", dir, exitCode, strings.TrimSpace(stderr), err)
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

func dockerContainerName() (string, error) {
	buf := make([]byte, dockerNameRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "skill-up-" + hex.EncodeToString(buf), nil
}

// overlayEnvList returns just the union of persistentEnv and callEnv as
// KEY=VALUE strings (callEnv wins on key conflicts). Variable expansion uses
// the host environment, mirroring mergeEnv's semantics for opts.Env values
// that reference $VAR — so users can pass `KEY=${HOST_VAR}` and have it
// resolve before the value lands inside the container.
func overlayEnvList(persistentEnv, callEnv map[string]string) []string {
	overlay := mergeEnvMaps(persistentEnv, callEnv)
	if len(overlay) == 0 {
		return nil
	}
	baseEnv := envMapFromList(os.Environ())
	expanded := expandEnvMap(baseEnv, overlay)
	out := make([]string, 0, len(expanded))
	for k, v := range expanded {
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
