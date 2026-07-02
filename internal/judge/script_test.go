package judge

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/platform"
	evalruntime "github.com/alibaba/skill-up/internal/runtime"
)

// scriptJudgeCase parametrises the common "write script → run judge → assert
// status & evidence" flow so individual test bodies stay focused on what's
// unique. Each case carries both a POSIX (.sh) and a Windows (.cmd) script
// body; runScriptJudgeCase picks the one matching the host OS, since these
// tests exercise the none runtime, which executes on the host.
type scriptJudgeCase struct {
	posixScript   string
	windowsScript string
	input         Input
	expectStatus  Status
	evidenceMatch string
}

// script returns the upload name and body appropriate for the host OS.
func (tc scriptJudgeCase) script() (name, body string) {
	if runtime.GOOS == platform.GOOSWindows {
		return "check.cmd", tc.windowsScript
	}
	return "check.sh", tc.posixScript
}

func runScriptJudgeCase(t *testing.T, tc scriptJudgeCase) {
	t.Helper()
	dir := t.TempDir()
	name, body := tc.script()
	script := writeScript(t, dir, name, body)
	in := tc.input
	if in.WorkspacePath == "" {
		in.WorkspacePath = dir
	}
	j := &ScriptJudge{ScriptPath: script}
	r, err := j.Evaluate(context.Background(), in)
	assertNoError(t, err)
	assertStatus(t, r, tc.expectStatus)
	if tc.evidenceMatch != "" && !strings.Contains(r.AssertionResults[0].Evidence, tc.evidenceMatch) {
		t.Fatalf("expected evidence to contain %q, got: %s", tc.evidenceMatch, r.AssertionResults[0].Evidence)
	}
}

// ---------------------------------------------------------------------------
// Script passes (exit 0)
// ---------------------------------------------------------------------------

func TestScriptJudge_Pass(t *testing.T) {
	runScriptJudgeCase(t, scriptJudgeCase{
		posixScript:   "#!/bin/sh\necho \"all checks passed\"\nexit 0\n",
		windowsScript: "@echo off\r\necho all checks passed\r\nexit /b 0\r\n",
		input: Input{
			FinalMessage: "test output",
			ExitCode:     0,
		},
		expectStatus:  StatusPass,
		evidenceMatch: "all checks passed",
	})
}

func TestScriptJudge_Pass_EmptyStdout(t *testing.T) {
	runScriptJudgeCase(t, scriptJudgeCase{
		posixScript:   "#!/bin/sh\nexit 0\n",
		windowsScript: "@echo off\r\nexit /b 0\r\n",
		expectStatus:  StatusPass,
		evidenceMatch: "script passed",
	})
}

// ---------------------------------------------------------------------------
// Script fails (non-zero exit)
// ---------------------------------------------------------------------------

func TestScriptJudge_Fail_NonZeroExit(t *testing.T) {
	runScriptJudgeCase(t, scriptJudgeCase{
		posixScript:   "#!/bin/sh\necho \"review quality below threshold\"\nexit 1\n",
		windowsScript: "@echo off\r\necho review quality below threshold\r\nexit /b 1\r\n",
		expectStatus:  StatusFail,
		evidenceMatch: "review quality below threshold",
	})
}

func TestScriptJudge_Fail_NonZeroExit_EmptyStdout(t *testing.T) {
	runScriptJudgeCase(t, scriptJudgeCase{
		posixScript:   "#!/bin/sh\nexit 2\n",
		windowsScript: "@echo off\r\nexit /b 2\r\n",
		expectStatus:  StatusFail,
		evidenceMatch: "exited with code 2",
	})
}

// ---------------------------------------------------------------------------
// Script with stderr
// ---------------------------------------------------------------------------

func TestScriptJudge_StderrCaptured(t *testing.T) {
	runScriptJudgeCase(t, scriptJudgeCase{
		posixScript:   "#!/bin/sh\necho \"passed\"\necho \"debug info here\" >&2\nexit 0\n",
		windowsScript: "@echo off\r\necho passed\r\necho debug info here 1>&2\r\nexit /b 0\r\n",
		expectStatus:  StatusPass,
		evidenceMatch: "debug info here",
	})
}

// ---------------------------------------------------------------------------
// Script timeout
// ---------------------------------------------------------------------------

func TestScriptJudge_Timeout(t *testing.T) {
	if runtime.GOOS == platform.GOOSWindows {
		// Windows has no POSIX process-group equivalent: killing cmd.exe
		// when its `ping -n 11` child is still running leaves the child
		// alive, holding the t.TempDir as cwd, which then fails RemoveAll
		// cleanup at test end. The cancellation path itself works (covered
		// by TestNoneRuntime_ExecReturnsContextErrorOnTimeout); this test
		// stays POSIX-only.
		t.Skip("POSIX process-group kill semantics; no Windows equivalent")
	}
	dir := t.TempDir()
	name, body := "slow.sh", "#!/bin/sh\nsleep 10\nexit 0\n"
	script := writeScript(t, dir, name, body)
	j := &ScriptJudge{
		ScriptPath:     script,
		TimeoutSeconds: 1,
	}
	r, err := j.Evaluate(context.Background(), Input{WorkspacePath: dir})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
	if !strings.Contains(r.AssertionResults[0].Evidence, "timed out") {
		t.Fatalf("expected timeout evidence, got: %s", r.AssertionResults[0].Evidence)
	}
}

// ---------------------------------------------------------------------------
// Script not found → error
// ---------------------------------------------------------------------------

func TestScriptJudge_ScriptNotFound(t *testing.T) {
	dir := t.TempDir()
	j := &ScriptJudge{ScriptPath: filepath.Join(dir, "nonexistent.sh")}
	_, err := j.Evaluate(context.Background(), Input{WorkspacePath: dir})
	if err == nil {
		t.Fatal("expected error when script does not exist")
	}
	if !strings.Contains(err.Error(), "script execution failed") {
		t.Fatalf("expected script execution error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Environment variables injected
// ---------------------------------------------------------------------------

func TestScriptJudge_EnvVarsInjected(t *testing.T) {
	dir := t.TempDir()
	name, body := "check_env.sh", "#!/bin/sh\n"+
		"echo \"transcript=$EVAL_TRANSCRIPT_PATH final=$EVAL_FINAL_MESSAGE exit=$EVAL_EXIT_CODE\"\n"+
		"exit 0\n"
	if runtime.GOOS == platform.GOOSWindows {
		name, body = "check_env.cmd", "@echo off\r\n"+
			"echo transcript=%EVAL_TRANSCRIPT_PATH% final=%EVAL_FINAL_MESSAGE% exit=%EVAL_EXIT_CODE%\r\n"+
			"exit /b 0\r\n"
	}
	script := writeScript(t, dir, name, body)
	j := &ScriptJudge{
		ScriptPath:     script,
		TranscriptPath: filepath.Join(dir, "transcript.json"),
	}
	if err := os.WriteFile(j.TranscriptPath, []byte(`[]`), 0o600); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}
	r, err := j.Evaluate(context.Background(), Input{
		WorkspacePath: dir,
		FinalMessage:  "hello world",
		ExitCode:      42,
	})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
	ev := r.AssertionResults[0].Evidence
	if !strings.Contains(ev, "transcript=") || !strings.Contains(ev, "skill-up-judge-") {
		t.Fatalf("EVAL_TRANSCRIPT_PATH not injected, evidence: %s", ev)
	}
	if !strings.Contains(ev, "final=hello world") {
		t.Fatalf("EVAL_FINAL_MESSAGE not injected, evidence: %s", ev)
	}
	if !strings.Contains(ev, "exit=42") {
		t.Fatalf("EVAL_EXIT_CODE not injected, evidence: %s", ev)
	}
}

func TestScriptJudge_EvaluatesInRuntime(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "check_runtime.sh", `#!/bin/sh
echo "cwd=$(pwd) transcript=$EVAL_TRANSCRIPT_PATH final=$EVAL_FINAL_MESSAGE exit=$EVAL_EXIT_CODE"
exit 0
`)
	rt := &scriptJudgeRuntime{
		workspace: "/runtime/workspace",
		result: evalruntime.ExecResult{
			Stdout:   "runtime judge passed\n",
			ExitCode: 0,
		},
	}
	j := &ScriptJudge{
		ScriptPath:     script,
		TranscriptPath: filepath.Join(dir, "transcript.json"),
		Runtime:        rt,
	}
	if err := os.WriteFile(j.TranscriptPath, []byte(`[]`), 0o600); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	r, err := j.Evaluate(context.Background(), Input{
		WorkspacePath: rt.workspace,
		FinalMessage:  "hello from agent",
		ExitCode:      42,
		TurnsExecuted: 1,
		TurnsTotal:    1,
	})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
	if !strings.Contains(r.AssertionResults[0].Evidence, "runtime judge passed") {
		t.Fatalf("expected runtime stdout evidence, got: %s", r.AssertionResults[0].Evidence)
	}
	if len(rt.uploads) != 2 {
		t.Fatalf("expected script and transcript uploads, got %#v", rt.uploads)
	}
	if rt.cwd != rt.workspace {
		t.Fatalf("runtime cwd = %q, want workspace", rt.cwd)
	}
	if rt.env["EVAL_FINAL_MESSAGE"] != "hello from agent" || rt.env["EVAL_EXIT_CODE"] != "42" {
		t.Fatalf("runtime env = %#v", rt.env)
	}
	if rt.env["EVAL_TRANSCRIPT_PATH"] == "" {
		t.Fatalf("expected remote transcript path in env")
	}
}

type scriptJudgeRuntime struct {
	workspace string
	result    evalruntime.ExecResult
	uploads   []string
	cwd       string
	env       map[string]string
}

func (r *scriptJudgeRuntime) Create(context.Context) error { return nil }
func (r *scriptJudgeRuntime) Close() error                 { return nil }
func (r *scriptJudgeRuntime) Start(context.Context) error  { return nil }
func (r *scriptJudgeRuntime) Stop(context.Context) error   { return nil }
func (r *scriptJudgeRuntime) Workspace() string            { return r.workspace }
func (r *scriptJudgeRuntime) RequiresProcessSandbox() bool { return true }
func (r *scriptJudgeRuntime) MergeEnv(_ map[string]string) {}

func (r *scriptJudgeRuntime) TargetGOOS() string { return "linux" }

func (r *scriptJudgeRuntime) UploadFile(_ context.Context, _, targetPath string) error {
	r.uploads = append(r.uploads, targetPath)
	return nil
}

func (r *scriptJudgeRuntime) UploadDir(context.Context, string, string) error    { return nil }
func (r *scriptJudgeRuntime) DownloadFile(context.Context, string, string) error { return nil }
func (r *scriptJudgeRuntime) DownloadDir(context.Context, string, string) error  { return nil }
func (r *scriptJudgeRuntime) Exec(_ context.Context, _ string, opts evalruntime.ExecOptions) (evalruntime.ExecResult, error) {
	if opts.Cwd != "" {
		r.cwd = opts.Cwd
	}
	if opts.Env != nil {
		r.env = opts.Env
	}
	return r.result, nil
}
