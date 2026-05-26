package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/observability"
)

const (
	noneDirMode  = 0o755
	noneFileMode = 0o600
)

// pathInWorkspaceOrAbs returns p if it is an absolute host path, otherwise filepath.Join(r.workspace, p).
// Clean(".") is "." which resolves under the workspace root (same as DownloadFile sourcePath rules).
func (r *NoneRuntime) pathInWorkspaceOrAbs(p string) string {
	c := filepath.Clean(p)
	if filepath.IsAbs(c) {
		return c
	}
	return filepath.Join(r.workspace, c)
}

// NoneRuntime executes commands directly on the host with an isolated temp workspace.
type NoneRuntime struct {
	cfg       Config
	workspace string
}

// MergeEnv layers entries into the runtime's persistent env baseline. See
// Runtime.MergeEnv for the contract (callers MUST sequence MergeEnv before
// any concurrent Exec; the intended use is a one-time setup step).
func (r *NoneRuntime) MergeEnv(env map[string]string) {
	if len(env) == 0 {
		return
	}
	if r.cfg.Env == nil {
		r.cfg.Env = make(map[string]string, len(env))
	}
	maps.Copy(r.cfg.Env, env)
}

// Create allocates the temporary workspace used by the runtime.
func (r *NoneRuntime) Create(ctx context.Context) error {
	dir, err := os.MkdirTemp("", "skill-up-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	r.workspace = dir

	return nil
}

// Close removes the temporary workspace.
func (r *NoneRuntime) Close() error {
	if r.workspace == "" {
		return nil
	}
	if !r.cfg.Delete {
		logging.Debugf("NoneRuntime.Close: skipping cleanup, workspace preserved at %s", r.workspace)
		return nil
	}
	return os.RemoveAll(r.workspace)
}

// Start is a no-op for the none runtime.
func (r *NoneRuntime) Start(ctx context.Context) error {
	return nil
}

// Stop is a no-op for the none runtime.
func (r *NoneRuntime) Stop(ctx context.Context) error {
	return nil
}

// UploadFile copies a single file into the runtime workspace.
// targetPath may be relative to the workspace or an absolute host path (written as-is).
func (r *NoneRuntime) UploadFile(ctx context.Context, sourcePath, targetPath string) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to read source file %s: %w", sourcePath, err)
	}
	target := r.pathInWorkspaceOrAbs(targetPath)
	if err := os.MkdirAll(filepath.Dir(target), noneDirMode); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	//nolint:gosec // target is joined with workspace, writing within sandbox is expected behavior
	return os.WriteFile(target, data, noneFileMode)
}

// UploadDir recursively copies a directory into the runtime workspace.
// targetDir may be relative to the workspace or an absolute host path (tree rooted there).
func (r *NoneRuntime) UploadDir(ctx context.Context, sourceDir, targetDir string) error {
	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		target := filepath.Join(r.pathInWorkspaceOrAbs(targetDir), rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if err := os.MkdirAll(filepath.Dir(target), noneDirMode); err != nil {
			return err
		}
		//nolint:gosec // path comes from Walk on user-provided sourceDir, TOCTOU risk is acceptable for CLI tool
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		//nolint:gosec // target is joined with workspace, using info.Mode() preserves source permissions
		//nolint:gosec // G703: target is joined with workspace, not user-controlled
		return os.WriteFile(target, data, info.Mode())
	})
}

// DownloadFile copies a single file from the runtime workspace to the host.
func (r *NoneRuntime) DownloadFile(ctx context.Context, sourcePath, targetPath string) error {
	source := r.pathInWorkspaceOrAbs(sourcePath)
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("failed to read source file %s: %w", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), noneDirMode); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	//nolint:gosec // targetPath is user-specified download destination
	return os.WriteFile(targetPath, data, noneFileMode)
}

// DownloadDir recursively copies a directory from the runtime workspace to the host.
// sourceDir is relative to the workspace root, or an absolute host path (same rule as DownloadFile).
func (r *NoneRuntime) DownloadDir(ctx context.Context, sourceDir, targetDir string) error {
	source := r.pathInWorkspaceOrAbs(sourceDir)

	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(targetDir, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if err := os.MkdirAll(filepath.Dir(target), noneDirMode); err != nil {
			return err
		}
		//nolint:gosec // path comes from Walk on workspace, TOCTOU risk is acceptable for CLI tool
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		//nolint:gosec // target is joined with user-specified targetDir, mode from source workspace file
		return os.WriteFile(target, data, info.Mode())
	})
}

// Exec runs a shell command in the runtime workspace or the provided working directory.
func (r *NoneRuntime) Exec(ctx context.Context, command string, opts ExecOptions) (ExecResult, error) {
	ctx, span := observability.Tracer().Start(ctx, "runtime.exec")
	defer span.End()
	startTime := time.Now()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	} else {
		cmd.Dir = r.workspace
	}
	span.SetAttributes(
		attribute.String("process.command", command),
		attribute.String("process.cwd", cmd.Dir),
	)

	env := mergeEnv(r.cfg.Env, opts.Env)
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	exitCode, execErr := classifyExecError(ctx, cmd.Run())
	result := ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}
	span.SetAttributes(attribute.Int("process.exit_code", result.ExitCode))
	observability.RecordRuntimeExec(ctx, result.ExitCode, time.Since(startTime).Milliseconds())

	switch {
	case result.ExitCode == -1 && ctx.Err() != nil:
		// Process was killed because the parent context was canceled or
		// hit its deadline. -1 is a sentinel, not a real exit code, and
		// attaching the full command source ("exited with code -1: set
		// -e ...") misleads readers into chasing a script-level failure.
		// When this was specifically a deadline (not a manual cancel),
		// include "deadline elapsed by Xs" so the surrounding logs make
		// the timeout obvious and the higher layer's error wrapper can be
		// correlated with this line.
		reason := ctx.Err().Error()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			if deadline, ok := ctx.Deadline(); ok {
				reason = fmt.Sprintf("%s (deadline elapsed by %s)", reason, time.Since(deadline).Round(time.Millisecond))
			}
		}
		logging.ErrorContextf(ctx, "command killed by context (%s); command: %s", reason, maskCommand(command))
		if result.Stderr != "" {
			logNonZeroStderr(ctx, result.ExitCode, result.Stderr)
		}
		if result.Stdout != "" {
			logNonZeroStdout(ctx, result.ExitCode, result.Stdout)
		}
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

	return result, execErr
}

// classifyExecError translates a *exec.Cmd Run error into the (exitCode, error)
// pair that callers expose through ExecResult.
//
// Precedence (matches the legacy inline behaviour):
//
//	nil err                            → (0, nil)
//	*exec.ExitError + ctx.Err() == nil → (exitCode, nil)
//	*exec.ExitError + ctx.Err() != nil → (exitCode, ctxErr)   // process was killed by ctx
//	non-ExitError                      → (-1, ctxErr or err)
func classifyExecError(ctx context.Context, runErr error) (int, error) {
	if runErr == nil {
		return 0, nil
	}
	ctxErr := ctx.Err()

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode(), ctxErr
	}
	if ctxErr != nil {
		return -1, ctxErr
	}
	return -1, runErr
}

func maskCommand(cmd string) string {
	const maxRunes = 200
	runes := []rune(cmd)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return cmd
}

func logNonZeroExit(ctx context.Context, exitCode int, command string) {
	msg := "command exited with code %d: %s"
	if exitCode == 1 {
		logging.WarnContextf(ctx, msg, exitCode, maskCommand(command))
		return
	}
	logging.ErrorContextf(ctx, msg, exitCode, maskCommand(command))
}

func logNonZeroStderr(ctx context.Context, exitCode int, stderr string) {
	if exitCode == 1 {
		logging.WarnContextf(ctx, "stderr: %s", stderr)
		return
	}
	logging.ErrorContextf(ctx, "stderr: %s", stderr)
}

// logNonZeroStdout surfaces captured stdout when a command exits non-zero.
// Many tools write diagnostic context (build output, test failures, traces)
// to stdout rather than stderr, so dropping it on failure leaves the user
// with only the exit code and an empty stderr line.
func logNonZeroStdout(ctx context.Context, exitCode int, stdout string) {
	if exitCode == 1 {
		logging.WarnContextf(ctx, "stdout: %s", stdout)
		return
	}
	logging.ErrorContextf(ctx, "stdout: %s", stdout)
}

// Workspace returns the runtime workspace path on the host.
func (r *NoneRuntime) Workspace() string {
	return r.workspace
}

// RequiresProcessSandbox keeps local agent execution constrained.
func (r *NoneRuntime) RequiresProcessSandbox() bool {
	return true
}
