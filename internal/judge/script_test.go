package judge

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	evalruntime "github.com/alibaba/skill-up/internal/runtime"
)

const osWindows = "windows"

// scriptJudgeCase parametrises the common "write script → run judge →
// assert status & evidence" flow so individual test bodies stay focused on
// what's unique. Extracted to silence dupl on near-identical pass/fail cases.
type scriptJudgeCase struct {
	scriptName    string
	scriptBody    string
	input         Input
	expectStatus  Status
	evidenceMatch string
}

func runScriptJudgeCase(t *testing.T, tc scriptJudgeCase) {
	t.Helper()
	if runtime.GOOS == osWindows {
		t.Skip("skipping on windows")
	}
	dir := t.TempDir()
	script := writeScript(t, dir, tc.scriptName, tc.scriptBody)
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
		scriptName: "check.sh",
		scriptBody: "#!/bin/sh\necho \"all checks passed\"\nexit 0\n",
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
		scriptName:    "check.sh",
		scriptBody:    "#!/bin/sh\nexit 0\n",
		expectStatus:  StatusPass,
		evidenceMatch: "script passed",
	})
}

// ---------------------------------------------------------------------------
// Script fails (non-zero exit)
// ---------------------------------------------------------------------------

func TestScriptJudge_Fail_NonZeroExit(t *testing.T) {
	runScriptJudgeCase(t, scriptJudgeCase{
		scriptName:    "check.sh",
		scriptBody:    "#!/bin/sh\necho \"review quality below threshold\"\nexit 1\n",
		expectStatus:  StatusFail,
		evidenceMatch: "review quality below threshold",
	})
}

func TestScriptJudge_Fail_NonZeroExit_EmptyStdout(t *testing.T) {
	runScriptJudgeCase(t, scriptJudgeCase{
		scriptName:    "check.sh",
		scriptBody:    "#!/bin/sh\nexit 2\n",
		expectStatus:  StatusFail,
		evidenceMatch: "exited with code 2",
	})
}

// ---------------------------------------------------------------------------
// Script with stderr
// ---------------------------------------------------------------------------

func TestScriptJudge_StderrCaptured(t *testing.T) {
	runScriptJudgeCase(t, scriptJudgeCase{
		scriptName:    "check.sh",
		scriptBody:    "#!/bin/sh\necho \"passed\"\necho \"debug info here\" >&2\nexit 0\n",
		expectStatus:  StatusPass,
		evidenceMatch: "debug info here",
	})
}

// ---------------------------------------------------------------------------
// Script timeout
// ---------------------------------------------------------------------------

func TestScriptJudge_Timeout(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("skipping on windows")
	}
	dir := t.TempDir()
	script := writeScript(t, dir, "slow.sh", "#!/bin/sh\nsleep 10\nexit 0\n")
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
	if runtime.GOOS == osWindows {
		t.Skip("skipping on windows")
	}
	dir := t.TempDir()
	script := writeScript(t, dir, "check_env.sh", `#!/bin/sh
echo "transcript=$EVAL_TRANSCRIPT_PATH final=$EVAL_FINAL_MESSAGE exit=$EVAL_EXIT_CODE"
exit 0
`)
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
	if !strings.Contains(ev, "transcript=/tmp/skill-up-judge-") {
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
	if runtime.GOOS == osWindows {
		t.Skip("skipping on windows")
	}
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
