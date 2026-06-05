package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// localTransport runs a custom engine as a command inside the current runtime
// via runtime.Exec. It owns the local-specific concerns: resolving the
// input/output file paths, writing the session input, clearing a stale output
// file, building and executing the command, and reading the result payload from
// the output file or stdout. The shared session preparation and result assembly
// live on CustomAgent, reused here via the back-reference.
type localTransport struct {
	a *CustomAgent
}

// run executes the prepared local command and returns a transportOutcome for
// CustomAgent.assembleResult. A setup failure (nil local block, unresolvable
// I/O path, command-render or input-write error) is returned as run's error so
// the run is abandoned; an execution-phase failure is carried in
// transportOutcome.execErr so a partial result can still be assembled.
func (t *localTransport) run(ctx context.Context, rt Runtime, opts ExecOptions, prep *customRunPrep) (*transportOutcome, error) {
	a := t.a
	custom := prep.custom
	// Guard against a nil local block: validation skips engine.custom for
	// built-in engines, so a `--engine` override can reach here unvalidated.
	if custom.Local == nil {
		return nil, errors.New("engine.custom.local.command is required when transport is local")
	}

	// Resolve the I/O paths with the full variable set (so they may use
	// ${kwargs.<key>}), then expose the resolved paths to command rendering.
	inputFile, outputFile, err := resolveCustomIOFiles(rt, custom, prep.vars)
	if err != nil {
		return nil, err
	}
	prep.vars["input_file"], prep.vars["output_file"] = inputFile, outputFile

	if err := persistRuntimeArtifact(ctx, rt, inputFile, string(prep.sessJSON)); err != nil {
		return nil, fmt.Errorf("write session input: %w", err)
	}

	// Remove any stale output file so a result left by a fixture or a previous
	// run is never mistaken for this invocation's output. Only an explicitly
	// configured output_file is cleared — the default ${output_file} path is
	// left untouched, as it may be ordinary fixture input the agent reads.
	// Also skip the clear when output_file resolves to the same path as
	// input_file, so the SessionInput just written is not self-deleted.
	clearedStaleOutput := false
	if custom.Local.OutputFile != "" && filepath.Clean(outputFile) != filepath.Clean(inputFile) {
		clearedStaleOutput = a.clearStaleOutputFile(ctx, rt, outputFile)
	}

	cmd, execOpts, err := a.buildLocalExec(ctx, rt, opts, custom, prep.vars, prep.timeoutSec)
	if err != nil {
		return nil, err
	}

	// Enforce the custom timeout via a context deadline: some runtimes
	// (e.g. NoneRuntime) do not honor ExecOptions.TimeoutSec directly.
	execCtx := ctx
	if prep.timeoutSec > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(prep.timeoutSec)*time.Second)
		defer cancel()
	}

	result, execErr := rt.Exec(execCtx, cmd, execOpts)

	// The command may already have emitted a valid result even when execErr
	// is non-nil — a timeout, exec.ErrWaitDelay (a backgrounded child kept
	// stdout open past WaitDelay), or a non-zero exit after printing output.
	// Always attempt the read so judges and expect checks can inspect the
	// partial answer; if nothing was produced, raw stays empty and the
	// parse-error path in assembleResult takes over.
	readCtx := ctx
	if execErr != nil {
		// ctx may have been canceled (timeout/cancel) or unrelated to the
		// failure; use a fresh context so a partial result is still
		// recoverable on remote runtimes whose DownloadFile honors ctx.
		var cancel context.CancelFunc
		readCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), customArtifactReadTimeout)
		defer cancel()
	}
	raw, outputFileProduced := a.readRawResult(readCtx, rt, custom, result, outputFile)

	// The framework-written input file is always recorded; the output file only
	// when it was produced by this run or cleared before it (the "produced or
	// cleared" rule the diff collector relies on).
	usedOutputFile := outputFileProduced || clearedStaleOutput
	frameworkFiles := make([]string, 0, 2)
	if inputFile != "" {
		frameworkFiles = append(frameworkFiles, inputFile)
	}
	if usedOutputFile && outputFile != "" {
		frameworkFiles = append(frameworkFiles, outputFile)
	}

	return &transportOutcome{
		raw:            raw,
		stdout:         result.Stdout,
		exitCode:       result.ExitCode,
		stderr:         result.Stderr,
		execErr:        execErr,
		frameworkFiles: frameworkFiles,
	}, nil
}
