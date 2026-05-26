package agent

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

func TestNewQoderCLIAgent(t *testing.T) {
	cfg := Config{}
	ag := NewQoderCLIAgent(cfg)

	if ag.Name() != "qodercli" {
		t.Errorf("expected name 'qodercli', got %s", ag.Name())
	}

	if ag.Cfg.CheckCmd != "command -v qodercli" {
		t.Errorf("expected CheckCmd 'command -v qodercli', got %s", ag.Cfg.CheckCmd)
	}

	if ag.Cfg.SkillPath != ".qoder/skills" {
		t.Errorf("expected SkillPath '.qoder/skills', got %s", ag.Cfg.SkillPath)
	}
}

func TestQoderCLICheckCredentials(t *testing.T) {
	ag := NewQoderCLIAgent(Config{})

	t.Setenv(credential.EnvQoderPersonalAccessToken, "")
	if err := ag.CheckCredentials(context.Background()); err != nil {
		t.Errorf("expected no error when token not set, got %v", err)
	}

	t.Setenv(credential.EnvQoderPersonalAccessToken, "test-token")
	if err := ag.CheckCredentials(context.Background()); err != nil {
		t.Errorf("expected no error when token is set, got %v", err)
	}
}

func TestQoderCLICheckCredentials_LogsPresenceFromProcessEnv(t *testing.T) {
	logging.SetVerbosity(1)
	defer logging.SetVerbosity(0)

	t.Setenv(credential.EnvQoderPersonalAccessToken, "qoder-test-token-1234")
	ag := NewQoderCLIAgent(Config{})

	output := captureStdout(t, func() {
		if err := ag.CheckCredentials(context.Background()); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
	if !strings.Contains(output, `level=DEBUG msg="QODER_PERSONAL_ACCESS_TOKEN detected for qodercli (source=process_env)"`) {
		t.Fatalf("expected info log, got %q", output)
	}
	if strings.Contains(output, "qoder-test-token-1234") {
		t.Fatalf("expected process env token not to be logged, got %q", output)
	}
	if !strings.Contains(output, "source=process_env") {
		t.Fatalf("expected process_env source in log, got %q", output)
	}
}

func TestQoderCLICheckCredentials_LogsPresenceFromRuntimeEnv(t *testing.T) {
	logging.SetVerbosity(1)
	defer logging.SetVerbosity(0)

	ag := NewQoderCLIAgent(Config{
		EnvVars: map[string]string{
			credential.EnvQoderPersonalAccessToken: "runtime-token",
		},
	})

	output := captureStdout(t, func() {
		if err := ag.CheckCredentials(context.Background()); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
	if !strings.Contains(output, `level=DEBUG msg="QODER_PERSONAL_ACCESS_TOKEN detected for qodercli (source=runtime_env)"`) {
		t.Fatalf("expected info log, got %q", output)
	}
	if !strings.Contains(output, "source=runtime_env") {
		t.Fatalf("expected runtime_env source in log, got %q", output)
	}
}

func TestQoderCLICheckCredentials_LogsWarningWhenMissing(t *testing.T) {
	t.Setenv(credential.EnvQoderPersonalAccessToken, "")
	ag := NewQoderCLIAgent(Config{})

	output := captureStdout(t, func() {
		if err := ag.CheckCredentials(context.Background()); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
	if !strings.Contains(output, `level=WARNING msg="QODER_PERSONAL_ACCESS_TOKEN not set, qodercli will rely on existing login state if available"`) {
		t.Fatalf("expected warning log, got %q", output)
	}
}

func TestQoderCLIInstall_DefaultCommand(t *testing.T) {
	t.Parallel()

	cmd := defaultQoderCLIInstallCmd()
	for _, want := range []string{
		"curl -fsSL https://qoder.com/install | bash",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("install command missing %q:\n%s", want, cmd)
		}
	}
}

func TestQoderCLIInstall_UsesDefaultCommand(t *testing.T) {
	t.Parallel()

	rt := &qoderTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			ExitCode: 0,
		},
	}
	ag := NewQoderCLIAgent(Config{})

	if err := ag.Install(context.Background(), rt); err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if !strings.Contains(rt.lastCommand, "curl -fsSL https://qoder.com/install | bash") {
		t.Fatalf("install command does not run qoder installer:\n%s", rt.lastCommand)
	}
	if _, ok := rt.lastExecEnv["PATH"]; ok {
		t.Fatalf("install env should not carry PATH from agent; PATH flows via runtime baseline. got %q", rt.lastExecEnv["PATH"])
	}
	if got := rt.mergedEnv["PATH"]; got == "" {
		t.Fatalf("expected probeAndMergePATH to populate runtime baseline with PATH; mergedEnv=%+v", rt.mergedEnv)
	}
}

func TestQoderCLIRun_MergesConfiguredEnvVars(t *testing.T) {
	t.Parallel()

	rt := &qoderTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			Stdout:   "hello\n",
			ExitCode: 0,
		},
	}

	ag := NewQoderCLIAgent(Config{
		EnvVars: map[string]string{
			"QODER_TEST_FLAG": "cfg-flag",
		},
	})

	_, err := ag.Run(context.Background(), rt, ExecOptions{
		Env: map[string]string{
			"EXTRA_FLAG": "1",
		},
	}, []transcript.Message{{
		Role:    transcript.RoleUser,
		Content: "hello",
		Turn:    1,
	}})
	if err != nil {
		t.Fatalf("run qodercli: %v", err)
	}
	if rt.lastExecEnv["QODER_TEST_FLAG"] != "cfg-flag" {
		t.Fatalf("expected QODER_TEST_FLAG to be merged, got %q", rt.lastExecEnv["QODER_TEST_FLAG"])
	}
	if rt.lastExecEnv["EXTRA_FLAG"] != "1" {
		t.Fatalf("expected EXTRA_FLAG to be preserved, got %q", rt.lastExecEnv["EXTRA_FLAG"])
	}
}

func TestBuildQoderRunCmd_WithModel(t *testing.T) {
	t.Parallel()

	cmd := buildQoderRunCmd("hello", "auto")

	if !strings.Contains(cmd, "qodercli --permission-mode=bypass_permissions --model 'auto' -p 'hello'") {
		t.Fatalf("expected qoder command to include model flag, got %q", cmd)
	}
}

func TestBuildQoderRunCmd_WithoutModel(t *testing.T) {
	t.Parallel()

	cmd := buildQoderRunCmd("hello", "")
	if strings.Contains(cmd, "--model") {
		t.Fatalf("expected qoder command to omit model flag, got %q", cmd)
	}
}

func TestQoderCLIEffectiveModelName_AllowsSupportedModel(t *testing.T) {
	t.Parallel()

	ag := NewQoderCLIAgent(Config{ModelProvider: "anthropic", ModelName: "auto"})
	if got := ag.effectiveModelName(context.Background()); got != "auto" {
		t.Fatalf("effectiveModelName() = %q, want auto", got)
	}
}

func TestQoderCLIEffectiveModelName_IgnoresUnsupportedConfiguredModel(t *testing.T) {
	// Not parallel: captureStdout redirects package-level log output.

	ag := NewQoderCLIAgent(Config{ModelProvider: "anthropic", ModelName: "claude-sonnet-4-6"})
	output := captureStdout(t, func() {
		if got := ag.effectiveModelName(context.Background()); got != "" {
			t.Fatalf("effectiveModelName() = %q, want empty", got)
		}
	})
	if !strings.Contains(output, `level=WARNING msg="qodercli ignores configured model \"claude-sonnet-4-6\" and will use local qoder model settings instead"`) {
		t.Fatalf("expected warning log, got %q", output)
	}
}

// QoderCLI session JSONL uses the same line shape as Claude Code session files; both are parsed with parseSessionFile.
func TestQoderSessionJSONL_ParseSessionFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "qoder-session.jsonl")

	sessionContent := `{"uuid":"5bb63b80-c07e-452c-a2b1-b5c29e5407e7","type":"user","message":{"role":"user","content":[{"type":"text","text":"张三 25岁 男性"}],"id":"8583cd9b-3177-4ecc-a2f6-97c4bf4e518a"}}
{"uuid":"5a2c82e7-5129-43bc-ac5d-4b68e64a8c53","type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"…"}],"id":"545f9a12-dc26-4182-9c27-0a58150ae658","usage":{"input_tokens":0,"output_tokens":0}}}
{"uuid":"d9709a02-cac3-4cde-8de1-103de6b75d07","type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"您好！我看到您提供了张三的基本信息"}],"id":"545f9a12-dc26-4182-9c27-0a58150ae658","usage":{"input_tokens":1,"output_tokens":2}}}
`
	if err := os.WriteFile(sessionFile, []byte(sessionContent), 0o600); err != nil {
		t.Fatal(err)
	}

	trans, finalMsg, inputTokens, outputTokens := parseSessionFile(sessionFile)

	if len(trans) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(trans))
	}
	if trans[len(trans)-1].Role != transcript.RoleAssistant {
		t.Errorf("last message should be assistant, got %v", trans[len(trans)-1].Role)
	}
	if !strings.Contains(trans[len(trans)-1].Content, "张三") {
		t.Errorf("expected assistant text about 张三, got %q", trans[len(trans)-1].Content)
	}
	if finalMsg == "" || !strings.Contains(finalMsg, "张三") {
		t.Errorf("expected finalMsg to mention 张三, got %q", finalMsg)
	}
	if inputTokens != 1 || outputTokens != 2 {
		t.Errorf("expected last assistant usage tokens 1/2, got %d/%d", inputTokens, outputTokens)
	}
}

func TestQoderSessionJSONL_ParseMCPToolCalls(t *testing.T) {
	t.Parallel()

	sessionFile := filepath.Join(t.TempDir(), "qoder-session.jsonl")
	sessionContent := `{"type":"user","message":{"role":"user","content":"call mcp"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"mcp__agent-sandbox__create_sandbox","input":{"config":{"language":{"pythonVersion":"3.12"}}}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"{\"success\":true}","is_error":false}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}
`
	if err := os.WriteFile(sessionFile, []byte(sessionContent), 0o600); err != nil {
		t.Fatal(err)
	}

	trans, finalMsg, _, _ := parseSessionFile(sessionFile)

	if finalMsg != "done" {
		t.Fatalf("finalMsg = %q, want done", finalMsg)
	}
	if len(trans) != 5 {
		t.Fatalf("expected 5 transcript messages, got %d: %#v", len(trans), trans)
	}
	if trans[1].Role != transcript.RoleToolCall || trans[1].ToolCall == nil {
		t.Fatalf("expected tool call message, got %#v", trans[1])
	}
	if trans[1].ToolCall.Name != "mcp__agent-sandbox__create_sandbox" {
		t.Fatalf("tool call name = %q", trans[1].ToolCall.Name)
	}
	if trans[3].Role != transcript.RoleToolResult || trans[3].ToolResult == nil {
		t.Fatalf("expected tool result message, got %#v", trans[3])
	}
	if trans[3].ToolResult.CallID != "call_1" || trans[3].ToolResult.Status != "success" {
		t.Fatalf("unexpected tool result: %#v", trans[3].ToolResult)
	}
}

func TestFindQoderSessionFile(t *testing.T) {
	t.Skip("findQoderSessionFile uses rt.Workspace() which NoneRuntime cannot set to the desired test workspace")
}

func TestFindQoderSessionFile_SelectsNewestByModTime(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cfg := runtime.Config{Type: "none"}
	rt, err := runtime.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	workspace := rt.Workspace()
	realPath, err := filepath.EvalSymlinks(workspace)
	if err == nil {
		workspace = realPath
	}
	workspaceKey := strings.ReplaceAll(workspace, "/", "-")
	projectDir := filepath.Join(tmpHome, ".qoder", "projects", workspaceKey)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldFile := filepath.Join(projectDir, "older.jsonl")
	newFile := filepath.Join(projectDir, "newer.jsonl")
	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now()
	if err := os.WriteFile(oldFile, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldFile, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newFile, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newFile, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	got := findQoderSessionFile(context.Background(), rt)
	if got != newFile {
		t.Fatalf("expected newest by mtime %q, got %q", newFile, got)
	}
}

func TestFindQoderSessionFileNoProject(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfg := runtime.Config{Type: "none"}
	rt, err := runtime.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	result := findQoderSessionFile(context.Background(), rt)

	if result != "" {
		t.Errorf("expected empty string for nonexistent workspace, got %s", result)
	}
}

func TestFindQoderSessionFileSymlink(t *testing.T) {
	t.Skip("findQoderSessionFile uses rt.Workspace() which NoneRuntime cannot set to the desired test workspace")
}

type qoderTestRuntime struct {
	workspace           string
	execResult          runtime.ExecResult
	lastCommand         string
	lastExecEnv         map[string]string
	probeResponseStdout string
	mergedEnv           map[string]string
}

func (r *qoderTestRuntime) Create(context.Context) error { return nil }
func (r *qoderTestRuntime) Close() error                 { return nil }
func (r *qoderTestRuntime) Start(context.Context) error  { return nil }
func (r *qoderTestRuntime) Stop(context.Context) error   { return nil }
func (r *qoderTestRuntime) UploadFile(context.Context, string, string) error {
	return nil
}
func (r *qoderTestRuntime) UploadDir(context.Context, string, string) error { return nil }
func (r *qoderTestRuntime) DownloadFile(context.Context, string, string) error {
	return nil
}
func (r *qoderTestRuntime) DownloadDir(context.Context, string, string) error { return nil }
func (r *qoderTestRuntime) Exec(_ context.Context, command string, opts runtime.ExecOptions) (runtime.ExecResult, error) {
	// Probe calls (agent.Install via probeAndMergePATH) get a canned
	// literal PATH and are NOT recorded as a real command. Exact-match
	// the probe constant so unrelated `printf '%s' "$HOME/..."` tests
	// aren't silently intercepted.
	if command == qoderExecPathProbeCmd {
		stdout := r.probeResponseStdout
		if stdout == "" {
			stdout = "/fake/.local/bin:/usr/bin"
		}
		return runtime.ExecResult{Stdout: stdout}, nil
	}
	r.lastCommand = command
	if strings.Contains(command, "qodercli --permission-mode=bypass_permissions") ||
		strings.Contains(command, "qodercli -p ") ||
		strings.Contains(command, "qoder.com/install") {
		r.lastExecEnv = mapsClone(opts.Env)
	}
	return r.execResult, nil
}
func (r *qoderTestRuntime) Workspace() string { return r.workspace }
func (r *qoderTestRuntime) RequiresProcessSandbox() bool {
	return true
}
func (r *qoderTestRuntime) MergeEnv(env map[string]string) {
	if r.mergedEnv == nil {
		r.mergedEnv = make(map[string]string, len(env))
	}
	maps.Copy(r.mergedEnv, env)
}
