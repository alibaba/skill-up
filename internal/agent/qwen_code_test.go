package agent

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/platform"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

func TestNewQwenCodeAgent(t *testing.T) {
	t.Parallel()

	ag := NewQwenCodeAgent(Config{})

	if ag.Name() != "qwen_code" {
		t.Fatalf("expected name qwen_code, got %s", ag.Name())
	}
	if ag.Cfg.CheckCmd != "command -v qwen" {
		t.Fatalf("expected qwen check cmd, got %s", ag.Cfg.CheckCmd)
	}
	if ag.Cfg.SkillPath != ".qwen/skills" {
		t.Fatalf("expected qwen skill path, got %s", ag.Cfg.SkillPath)
	}
}

func TestQwenCodeCheckCredentials(t *testing.T) {
	t.Parallel()

	ag := NewQwenCodeAgent(Config{})
	if err := ag.CheckCredentials(context.Background()); err != nil {
		t.Fatalf("expected missing OPENAI_API_KEY to be informational only, got %v", err)
	}

	ag = NewQwenCodeAgent(Config{APIKey: "qwen-test-token"})
	if err := ag.CheckCredentials(context.Background()); err != nil {
		t.Fatalf("expected OPENAI_API_KEY to be accepted, got %v", err)
	}
}

func TestQwenCodeInstall_DefaultCommand(t *testing.T) {
	t.Parallel()

	cmd := defaultQwenCodeInstallCmd()
	for _, want := range []string{
		"if command -v qwen >/dev/null 2>&1; then exit 0; fi",
		"npm install -g '@qwen-code/qwen-code'",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("install command missing %q:\n%s", want, cmd)
		}
	}
}

//nolint:dupl // mirrors TestQoderCLIInstall_UsesDefaultCommand; the probe→install→PATH lifecycle is intentionally identical across CLI agents.
func TestQwenCodeInstall_UsesDefaultCommand(t *testing.T) {
	t.Parallel()

	rt := &qwenTestRuntime{
		workspace:  t.TempDir(),
		execResult: runtime.ExecResult{ExitCode: 0},
	}
	ag := NewQwenCodeAgent(Config{})

	if err := ag.Install(context.Background(), rt); err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if !strings.Contains(rt.lastCommand, "@qwen-code/qwen-code") {
		t.Fatalf("install command does not install qwen-code package:\n%s", rt.lastCommand)
	}
	if _, ok := rt.lastExecEnv["PATH"]; ok {
		t.Fatalf("install env should not carry PATH from agent; PATH flows via runtime baseline. got %q", rt.lastExecEnv["PATH"])
	}
	if got := rt.mergedEnv["PATH"]; got == "" {
		t.Fatalf("expected probeAndMergePATH to populate runtime baseline with PATH; mergedEnv=%+v", rt.mergedEnv)
	}
}

func TestBuildQwenCodeRunCmd(t *testing.T) {
	t.Parallel()

	withModel := buildQwenCodeRunCmd("do it", "qwen3-coder-plus")
	// Instruction is piped on stdin (not -p, which upstream deprecated), and
	// the model is passed via -m.
	for _, want := range []string{"printf '%s' 'do it' | qwen --yolo", "-m 'qwen3-coder-plus'"} {
		if !strings.Contains(withModel, want) {
			t.Fatalf("run cmd missing %q:\n%s", want, withModel)
		}
	}
	if strings.Contains(withModel, "-p ") {
		t.Fatalf("expected no deprecated -p flag, got %q", withModel)
	}

	noModel := buildQwenCodeRunCmd("do it", "")
	if strings.Contains(noModel, "-m ") {
		t.Fatalf("expected no -m flag when model empty, got %q", noModel)
	}
}

func TestQwenCodeRun_BuildsCommandAndMergesEnv(t *testing.T) {
	t.Parallel()

	rt := &qwenTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			Stdout:   "all done\n",
			ExitCode: 0,
		},
	}

	ag := NewQwenCodeAgent(Config{ //nolint:gosec // test dummy key
		ModelName: "qwen3-coder-plus",
		APIKey:    "qwen-api-key",
		BaseURL:   "https://dashscope.example.com/v1",
		EnvVars:   map[string]string{"QWEN_TEST_FLAG": "cfg-flag"},
	})

	result, err := ag.Run(context.Background(), rt, ExecOptions{
		Env: map[string]string{"EXTRA_FLAG": "1"},
	}, []transcript.Message{{
		Role:    transcript.RoleUser,
		Content: "hello",
		Turn:    1,
	}})
	if err != nil {
		t.Fatalf("run qwen_code: %v", err)
	}

	if !strings.Contains(rt.runCommand, "qwen --yolo") || !strings.Contains(rt.runCommand, "-m 'qwen3-coder-plus'") {
		t.Fatalf("unexpected run command: %s", rt.runCommand)
	}
	if rt.lastExecEnv[credential.EnvOpenAIAPIKey] != "qwen-api-key" {
		t.Fatalf("expected OPENAI_API_KEY to be set, got %q", rt.lastExecEnv[credential.EnvOpenAIAPIKey])
	}
	if rt.lastExecEnv[credential.EnvOpenAIBaseURL] != "https://dashscope.example.com/v1" {
		t.Fatalf("expected OPENAI_BASE_URL to be set, got %q", rt.lastExecEnv[credential.EnvOpenAIBaseURL])
	}
	if rt.lastExecEnv[credential.EnvOpenAIModel] != "qwen3-coder-plus" {
		t.Fatalf("expected OPENAI_MODEL to mirror the model, got %q", rt.lastExecEnv[credential.EnvOpenAIModel])
	}
	if rt.lastExecEnv["QWEN_TEST_FLAG"] != "cfg-flag" {
		t.Fatalf("expected QWEN_TEST_FLAG to be merged, got %q", rt.lastExecEnv["QWEN_TEST_FLAG"])
	}
	if rt.lastExecEnv["EXTRA_FLAG"] != "1" {
		t.Fatalf("expected EXTRA_FLAG to be merged, got %q", rt.lastExecEnv["EXTRA_FLAG"])
	}
	if result.FinalMessage != "all done" {
		t.Fatalf("expected final message from stdout, got %q", result.FinalMessage)
	}
	if result.Turns != 1 {
		t.Fatalf("expected 1 turn, got %d", result.Turns)
	}
	// On a host-executing runtime (RequiresProcessSandbox=true) the "running
	// unsandboxed" notice must NOT be hidden — it is the user's signal that
	// tools run at host privilege.
	if _, ok := rt.lastExecEnv["QWEN_CODE_SUPPRESS_YOLO_WARNING"]; ok {
		t.Fatalf("expected the yolo/no-sandbox warning NOT to be suppressed on a host-executing runtime")
	}
}

// TestQwenCodeRun_SuppressesWarningOnlyWhenRuntimeIsolates checks the inverse:
// an isolating runtime (docker/opensandbox) is the sandbox, so the spurious
// "no sandbox" notice is silenced there.
func TestQwenCodeRun_SuppressesWarningOnlyWhenRuntimeIsolates(t *testing.T) {
	t.Parallel()

	rt := &qwenTestRuntime{
		workspace:        t.TempDir(),
		execResult:       runtime.ExecResult{Stdout: "ok\n", ExitCode: 0},
		noProcessSandbox: true, // runtime isolates execution
	}
	ag := NewQwenCodeAgent(Config{ModelName: "qwen3-coder-plus"})

	if _, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{
		Role: transcript.RoleUser, Content: "hi", Turn: 1,
	}}); err != nil {
		t.Fatalf("run qwen_code: %v", err)
	}
	if rt.lastExecEnv["QWEN_CODE_SUPPRESS_YOLO_WARNING"] != "1" {
		t.Fatalf("expected the notice to be suppressed on an isolating runtime, env=%v", rt.lastExecEnv)
	}
}

func TestParseQwenSessionFile(t *testing.T) {
	t.Parallel()

	const callID = "call_1" // referenced below so goconst doesn't flag the literal

	// One user turn, an assistant tool call, the tool response, then the final
	// assistant answer with usageMetadata. Mirrors qwen's Gemini-style schema
	// (role model/user, parts[], functionCall/functionResponse).
	lines := []string{
		`{"type":"user","message":{"role":"user","parts":[{"text":"List the files."}]}}`,
		`{"type":"system","subtype":"ui_telemetry","systemPayload":{}}`,
		`{"type":"assistant","model":"qwen3-coder-plus","message":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"run_shell_command","args":{"command":"ls"}}}]}}`,
		`{"type":"user","message":{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"run_shell_command","response":{"output":"a.txt\nb.txt"}}}]}}`,
		`{"type":"assistant","model":"qwen3-coder-plus","message":{"role":"model","parts":[{"text":"There are two files: a.txt and b.txt."}]},"usageMetadata":{"promptTokenCount":123,"candidatesTokenCount":45,"thoughtsTokenCount":6,"totalTokenCount":174,"cachedContentTokenCount":0}}`,
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	trans, finalMsg, inTok, outTok := parseQwenSessionFile(path)

	if finalMsg != "There are two files: a.txt and b.txt." {
		t.Fatalf("unexpected final message: %q", finalMsg)
	}
	if inTok != 123 {
		t.Fatalf("expected input tokens 123 (promptTokenCount), got %d", inTok)
	}
	if outTok != 51 { // candidates 45 + thoughts 6
		t.Fatalf("expected output tokens 51 (candidates+thoughts), got %d", outTok)
	}
	// Expect: user, tool_call, tool_result, assistant.
	roles := make([]transcript.Role, 0, len(trans))
	for _, m := range trans {
		roles = append(roles, m.Role)
	}
	want := []transcript.Role{transcript.RoleUser, transcript.RoleToolCall, transcript.RoleToolResult, transcript.RoleAssistant}
	if len(roles) != len(want) {
		t.Fatalf("expected %d messages %v, got %d: %v", len(want), want, len(roles), roles)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("message %d role = %q, want %q", i, roles[i], want[i])
		}
	}
	if tc := trans[1].ToolCall; tc == nil || tc.Name != "run_shell_command" || tc.ID != callID {
		t.Fatalf("unexpected tool call: %+v", trans[1].ToolCall)
	}
	if tr := trans[2].ToolResult; tr == nil || tr.CallID != callID || !strings.Contains(fmt.Sprint(tr.Content), "a.txt") {
		t.Fatalf("unexpected tool result: %+v", trans[2].ToolResult)
	}
}

func TestParseQwenSessionFile_Missing(t *testing.T) {
	t.Parallel()
	trans, finalMsg, inTok, outTok := parseQwenSessionFile(filepath.Join(t.TempDir(), "nope.jsonl"))
	if trans != nil || finalMsg != "" || inTok != 0 || outTok != 0 {
		t.Fatalf("expected empty result for missing file, got %v %q %d %d", trans, finalMsg, inTok, outTok)
	}
}

func TestQwenCodeMCPInstallCmd(t *testing.T) {
	t.Parallel()

	cmd, err := buildQwenCodeMCPInstallCmd(runtime.MCPServerConfig{
		Name:      "fs",
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "server-fs"},
	})
	if err != nil {
		t.Fatalf("build mcp install cmd: %v", err)
	}
	if !strings.Contains(cmd, "qwen mcp add --scope project 'fs'") {
		t.Fatalf("unexpected mcp install command: %s", cmd)
	}
}

// qwenTestRuntime is a minimal Runtime used by the Qwen Code agent tests. It
// mirrors qoderTestRuntime: it intercepts the PATH probe with a canned literal
// and records the env of the install / run command for assertions.
type qwenTestRuntime struct {
	workspace           string
	execResult          runtime.ExecResult
	lastCommand         string
	runCommand          string // the `qwen --yolo ...` invocation specifically
	lastExecEnv         map[string]string
	probeResponseStdout string
	mergedEnv           map[string]string
	// noProcessSandbox flips RequiresProcessSandbox to false, modelling an
	// isolating runtime (docker/opensandbox); the default (false) keeps it
	// true, modelling the host-executing none runtime.
	noProcessSandbox bool
}

func (r *qwenTestRuntime) Create(context.Context) error                       { return nil }
func (r *qwenTestRuntime) Close() error                                       { return nil }
func (r *qwenTestRuntime) Start(context.Context) error                        { return nil }
func (r *qwenTestRuntime) Stop(context.Context) error                         { return nil }
func (r *qwenTestRuntime) UploadFile(context.Context, string, string) error   { return nil }
func (r *qwenTestRuntime) UploadDir(context.Context, string, string) error    { return nil }
func (r *qwenTestRuntime) DownloadFile(context.Context, string, string) error { return nil }
func (r *qwenTestRuntime) DownloadDir(context.Context, string, string) error  { return nil }
func (r *qwenTestRuntime) Exec(_ context.Context, command string, opts runtime.ExecOptions) (runtime.ExecResult, error) {
	if command == qwenCodeExecPathProbeCmd {
		stdout := r.probeResponseStdout
		if stdout == "" {
			stdout = "/fake/.local/bin:/usr/bin"
		}
		return runtime.ExecResult{Stdout: stdout}, nil
	}
	r.lastCommand = command
	if strings.Contains(command, "qwen --yolo") || strings.Contains(command, "@qwen-code/qwen-code") {
		r.lastExecEnv = mapsClone(opts.Env)
	}
	if strings.Contains(command, "qwen --yolo") {
		r.runCommand = command
	}
	return r.execResult, nil
}
func (r *qwenTestRuntime) Workspace() string            { return r.workspace }
func (r *qwenTestRuntime) RequiresProcessSandbox() bool { return !r.noProcessSandbox }
func (r *qwenTestRuntime) MergeEnv(env map[string]string) {
	if r.mergedEnv == nil {
		r.mergedEnv = make(map[string]string, len(env))
	}
	maps.Copy(r.mergedEnv, env)
}

func (r *qwenTestRuntime) Shell() platform.Shell {
	return platform.Shell{GOOS: platform.GOOSLinux, Family: platform.ShellPOSIX}
}
