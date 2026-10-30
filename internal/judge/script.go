package judge

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
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
	// TimeoutIsError marks a timed-out script as an execution error.
	TimeoutIsError bool
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

	// MetricSlots is the planned named-metric registry for this judge. Empty means
	// the judge produces no metrics envelope at all.
	MetricSlots []MetricSlot
	// MetricJudgeID is the stable configured judge identifier recorded in the
	// metrics envelope. Empty preserves the standalone "script" identifier.
	MetricJudgeID string

	// MetricsResultPath is the runner-assigned host path for this invocation's
	// structured result. Set only when the script runs against the local runtime.
	MetricsResultPath string

	// MetricsInvocationID binds the collected file to this exact invocation, so a
	// leftover file from an earlier attempt is never accepted.
	MetricsInvocationID string
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

	remoteDir := judgeTempDir(targetGOOS)
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
	env := map[string]string{
		"EVAL_TRANSCRIPT_PATH": transcriptEnv,
		"EVAL_FINAL_MESSAGE":   in.FinalMessage,
		"EVAL_EXIT_CODE":       strconv.Itoa(in.ExitCode),
	}
	if len(j.MetricSlots) > 0 && j.MetricsResultPath != "" {
		env["EVAL_METRICS_RESULT_PATH"] = j.MetricsResultPath
		env["EVAL_METRICS_INVOCATION_ID"] = j.MetricsInvocationID
		env["EVAL_METRICS_TEMPLATE"] = MetricsPayloadTemplate(j.MetricsInvocationID)
	}
	result, err := rt.Exec(ctx, command, evalruntime.ExecOptions{
		Cwd:        cwd,
		TimeoutSec: int(timeout.Seconds()),
		Env:        env,
	})
	evidence := strings.TrimSpace(result.Stdout)
	debugInfo := strings.TrimSpace(result.Stderr)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return j.timeoutResult(ctx, timeout, debugInfo, in)
		}
		if result.ExitCode == 0 {
			return nil, fmt.Errorf("script execution failed: %w", err)
		}
	}
	if result.ExitCode != 0 {
		return j.unsuccessfulExitResult(evidence, debugInfo, result.ExitCode, in), nil
	}

	if evidence == "" {
		evidence = "script passed (exit code 0)"
	}
	res := j.buildResult(true, evidence, debugInfo, in)
	j.attachMetrics(res, MetricProduced)
	return res, nil
}

func (j *ScriptJudge) timeoutResult(ctx context.Context, timeout time.Duration, debugInfo string, in Input) (*Result, error) {
	source := "judge.timeout_seconds"
	execution := &ExecutionMetadata{
		Stage: StageStarted, Completion: CompletionInterrupted,
		Cause: CauseDeadlineExpiry, DeadlineSource: source,
		Timeout: &TimeoutMetadata{Occurred: true, Source: source},
	}
	// Keep a trustworthy result written before the deadline.
	if j.TimeoutIsError {
		timeoutErr := fmt.Errorf("script timed out after %s: %w", timeout, ctx.Err())
		res := NewErrorResult(timeoutErr, in.TurnsExecuted, in.TurnsTotal)
		res.Execution = execution
		j.attachMetrics(res, MetricPrevented)
		return res, timeoutErr
	}
	res := j.buildResult(false, "script timed out after "+timeout.String(), debugInfo, in)
	res.Execution = execution
	j.attachMetrics(res, MetricPrevented)
	return res, nil
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

// unsuccessfulExitResult maps a nonzero script exit onto the legacy FAIL verdict.
// With a metric registry configured, the execution record names the exit as the
// reason the case failed and no score the script wrote can be accepted: an
// unsuccessful process must not be reported as omitted scoring output.
func (j *ScriptJudge) unsuccessfulExitResult(evidence, debugInfo string, exitCode int, in Input) *Result {
	detail := evidence
	if detail == "" {
		detail = fmt.Sprintf("script exited with code %d", exitCode)
	}
	res := j.buildResult(false, detail, debugInfo, in)
	if len(j.MetricSlots) == 0 {
		return res
	}
	res.Execution = &ExecutionMetadata{
		Stage:      StageFinished,
		Completion: CompletionFailed,
		Cause:      CauseProcessExit,
		Detail:     detail,
	}
	res.Metrics = j.declinedMetrics()
	return res
}

// producerID is the judge identity recorded in a metric envelope: the configured
// id, falling back to the judge type when a judge was built without one.
func (j *ScriptJudge) producerID() string {
	if j.MetricJudgeID != "" {
		return j.MetricJudgeID
	}
	return "script"
}

// declinedMetrics materializes every planned slot as NOT_PRODUCED for a script
// that exited unsuccessfully. The result file is deliberately not read: it stays
// a diagnostic artifact, and its presence is noted so the run is explainable
// without pretending a failed process produced a score.
func (j *ScriptJudge) declinedMetrics() *MetricsEnvelope {
	judgeID := j.producerID()
	env := MaterializeSlots(judgeID, j.MetricSlots, nil, ProblemNone, MetricPrevented)
	if j.MetricsResultPath != "" {
		if info, err := os.Lstat(j.MetricsResultPath); err == nil && info.Mode().IsRegular() {
			env.AddDiagnostic("result_file_not_accepted")
		}
	}
	return env
}

// attachMetrics collects this invocation's structured metric result file and
// materializes one slot per planned registry entry onto the judge result.
//
// The rules it enforces: the path must resolve to a regular, non-symlink file of
// an acceptable size, bound to this invocation id, and schema-valid. A
// whole-envelope defect poisons every slot; a single bad entry poisons only its
// own slot and independently valid siblings stay AVAILABLE. A file that never
// appeared while evaluation was interrupted yields NOT_PRODUCED, and one that
// never appeared after a completed run yields a rejected payload rather than a
// fabricated zero.
func (j *ScriptJudge) attachMetrics(res *Result, production MetricProduction) {
	if res == nil || len(j.MetricSlots) == 0 {
		return
	}
	judgeID := j.producerID()
	if j.MetricsResultPath == "" {
		// The channel is not available for this judge (a non-local runtime, or a
		// judge built without one). Report that honestly rather than implying a
		// stale file was found.
		res.Metrics = MaterializeSlots(judgeID, j.MetricSlots, nil, ProblemChannelUnsupported, production)
		return
	}
	info, err := os.Lstat(j.MetricsResultPath)
	if errors.Is(err, os.ErrNotExist) && production == MetricPrevented {
		// Nothing was ever produced because evaluation was interrupted: the slots
		// are NOT_PRODUCED, not a rejected payload.
		res.Metrics = MaterializeSlots(judgeID, j.MetricSlots, nil, ProblemNone, MetricPrevented)
		return
	}
	if err != nil {
		problem := ProblemUnboundResult
		if !os.IsNotExist(err) {
			problem = ProblemUnparseable
		}
		res.Metrics = MaterializeSlots(judgeID, j.MetricSlots, nil, problem, production)
		return
	}
	if !info.Mode().IsRegular() {
		// A symlink, directory or device node is never a collected result.
		res.Metrics = MaterializeSlots(judgeID, j.MetricSlots, nil, ProblemUnboundResult, production)
		return
	}
	if info.Size() > MaxMetricsPayloadBytes {
		// Bound the read before it happens: an oversized file must not be loaded
		// into memory just to be rejected afterwards.
		res.Metrics = MaterializeSlots(judgeID, j.MetricSlots, nil, ProblemSizeLimit, production)
		return
	}
	raw, err := os.ReadFile(j.MetricsResultPath)
	if err != nil {
		res.Metrics = MaterializeSlots(judgeID, j.MetricSlots, nil, ProblemUnparseable, production)
		return
	}
	emitted, payloadProblem := ParseMetricsPayload(raw, j.MetricsInvocationID)
	res.Metrics = MaterializeSlots(judgeID, j.MetricSlots, emitted, payloadProblem, MetricProduced)
}
