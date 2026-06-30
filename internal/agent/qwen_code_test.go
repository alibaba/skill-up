package agent

import (
	"context"
	"maps"
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
	for _, want := range []string{"qwen --yolo", "-m 'qwen3-coder-plus'", "-p 'do it'"} {
		if !strings.Contains(withModel, want) {
			t.Fatalf("run cmd missing %q:\n%s", want, withModel)
		}
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

	ag := NewQwenCodeAgent(Config{
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

	if !strings.Contains(rt.lastCommand, "qwen --yolo") || !strings.Contains(rt.lastCommand, "-m 'qwen3-coder-plus'") {
		t.Fatalf("unexpected run command: %s", rt.lastCommand)
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
	lastExecEnv         map[string]string
	probeResponseStdout string
	mergedEnv           map[string]string
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
	return r.execResult, nil
}
func (r *qwenTestRuntime) Workspace() string            { return r.workspace }
func (r *qwenTestRuntime) RequiresProcessSandbox() bool { return true }
func (r *qwenTestRuntime) MergeEnv(env map[string]string) {
	if r.mergedEnv == nil {
		r.mergedEnv = make(map[string]string, len(env))
	}
	maps.Copy(r.mergedEnv, env)
}
func (r *qwenTestRuntime) TargetGOOS() string { return platform.GOOSLinux }
