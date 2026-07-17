package judge

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	evalruntime "github.com/alibaba/skill-up/internal/runtime"
)

// DefaultScriptTimeout is the default timeout for script execution (30s).
const DefaultScriptTimeout = 30 * time.Second

// ScriptJudge runs a user-provided evaluation script in the case workspace.
//
// Script contract (design doc):
//   - Working directory: case workspace root
//   - Environment variables: EVAL_TRANSCRIPT_PATH, EVAL_FINAL_MESSAGE, EVAL_EXIT_CODE
//   - Exit code 0 → PASS, non-0 → FAIL
//   - stdout → evaluation evidence, stderr → debug info
type ScriptJudge struct {
	// ScriptPath is the absolute or workspace-relative path to the evaluation script.
	ScriptPath string

	// TimeoutSeconds overrides DefaultScriptTimeout. 0 means use default.
	TimeoutSeconds int

	// TranscriptPath is the absolute path to the transcript.json file.
	// Injected as EVAL_TRANSCRIPT_PATH environment variable.
	TranscriptPath string

	// Runtime executes the script in the same environment as the evaluated agent.
	// When nil, ScriptJudge creates a none runtime so local execution uses the same path.
	Runtime evalruntime.Runtime
}

// Evaluate implements the Judge interface by executing the configured script.
func (j *ScriptJudge) Evaluate(ctx context.Context, in Input) (*Result, error) {
	if j.TranscriptPath == "" {
		log.Println("warning: ScriptJudge.TranscriptPath is empty; EVAL_TRANSCRIPT_PATH will be unset")
	}

	timeout := DefaultScriptTimeout
	if j.TimeoutSeconds > 0 {
		timeout = time.Duration(j.TimeoutSeconds) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	rt, cleanup, err := j.runtime(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return j.evaluateInRuntime(ctx, rt, in, timeout)
}

func (j *ScriptJudge) runtime(ctx context.Context) (evalruntime.Runtime, func(), error) {
	if j.Runtime != nil {
		return j.Runtime, func() {}, nil
	}
	rt, err := evalruntime.NewRuntime(evalruntime.Config{Type: "none", Delete: true})
	if err != nil {
		return nil, nil, fmt.Errorf("script execution failed: create none runtime: %w", err)
	}
	if err := rt.Create(ctx); err != nil {
		return nil, nil, fmt.Errorf("script execution failed: create none runtime workspace: %w", err)
	}
	return rt, func() { _ = rt.Close() }, nil
}

func (j *ScriptJudge) evaluateInRuntime(ctx context.Context, rt evalruntime.Runtime, in Input, timeout time.Duration) (*Result, error) {
	shell := rt.Shell()
	targetGOOS := shell.GOOS
	plan, err := planScript(j.ScriptPath, shell)
	if err != nil {
		return nil, fmt.Errorf("script execution failed: %w", err)
	}

	remoteDir := judgeTempDir(targetGOOS, rt.Workspace())
	remoteScript := joinForGOOS(targetGOOS, remoteDir, plan.uploadName)
	if err := rt.UploadFile(ctx, j.ScriptPath, remoteScript); err != nil {
		return nil, fmt.Errorf("script execution failed: upload script judge: %w", err)
	}
	defer func() {
		_, _ = rt.Exec(context.WithoutCancel(ctx), plan.cleanupCommand(remoteDir), evalruntime.ExecOptions{})
	}()

	remoteTranscript := ""
	if j.TranscriptPath != "" {
		remoteTranscript = joinForGOOS(targetGOOS, remoteDir, "transcript.json")
		if err := rt.UploadFile(ctx, j.TranscriptPath, remoteTranscript); err != nil {
			return nil, fmt.Errorf("script execution failed: upload script judge transcript: %w", err)
		}
	}

	command := plan.command(remoteScript)
	cwd := in.WorkspacePath
	if cwd == "" {
		cwd = rt.Workspace()
	}
	// Translate the transcript path into the script interpreter's preferred
	// form (e.g. forward slashes for `.sh` running under Git Bash so POSIX
	// tools can `cat "$EVAL_TRANSCRIPT_PATH"`). Every plan returned by
	// planScript sets envPath (identity on POSIX, ToSlash on Windows .sh),
	// so the function is always safe to invoke.
	transcriptEnv := remoteTranscript
	if remoteTranscript != "" {
		transcriptEnv = plan.envPath(remoteTranscript)
	}
	result, err := rt.Exec(ctx, command, evalruntime.ExecOptions{
		Cwd:        cwd,
		TimeoutSec: int(timeout.Seconds()),
		Env: map[string]string{
			"EVAL_TRANSCRIPT_PATH": transcriptEnv,
			"EVAL_FINAL_MESSAGE":   in.FinalMessage,
			"EVAL_EXIT_CODE":       strconv.Itoa(in.ExitCode),
		},
	})
	evidence := strings.TrimSpace(result.Stdout)
	debugInfo := strings.TrimSpace(result.Stderr)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return j.buildResult(false, "script timed out after "+timeout.String(), debugInfo, in), nil
		}
		if result.ExitCode == 0 {
			return nil, fmt.Errorf("script execution failed: %w", err)
		}
	}
	if result.ExitCode != 0 {
		detail := evidence
		if detail == "" {
			detail = fmt.Sprintf("script exited with code %d", result.ExitCode)
		}
		return j.buildResult(false, detail, debugInfo, in), nil
	}

	if evidence == "" {
		evidence = "script passed (exit code 0)"
	}
	return j.buildResult(true, evidence, debugInfo, in), nil
}

// buildResult creates a judge Result from script execution outcome.
func (j *ScriptJudge) buildResult(passed bool, evidence, debugInfo string, in Input) *Result {
	if debugInfo != "" {
		evidence = evidence + " [stderr: " + debugInfo + "]"
	}

	assertion := AssertionResult{
		Text:     "script: " + j.ScriptPath,
		Passed:   passed,
		Evidence: evidence,
	}

	return NewResult([]AssertionResult{assertion}, in.TurnsExecuted, in.TurnsTotal)
}
