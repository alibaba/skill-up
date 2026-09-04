package agent

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/platform"
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
	if ag.Cfg.VersionCmd != "qodercli --version" {
		t.Errorf("expected VersionCmd 'qodercli --version', got %s", ag.Cfg.VersionCmd)
	}

	if ag.Cfg.SkillPath != ".qoder/skills" {
		t.Errorf("expected SkillPath '.qoder/skills', got %s", ag.Cfg.SkillPath)
	}
}

func TestNewQoderCLIAgent_CNEdition(t *testing.T) {
	t.Parallel()

	ag := NewQoderCLIAgent(Config{Kwargs: map[string]string{KwargEdition: qoderEditionCN}})
	if ag.profile.edition != qoderEditionCN {
		t.Fatalf("edition = %q, want %q", ag.profile.edition, qoderEditionCN)
	}
	if ag.Cfg.CheckCmd != "command -v qodercn" {
		t.Fatalf("CheckCmd = %q, want qodercn", ag.Cfg.CheckCmd)
	}
	if ag.Cfg.VersionCmd != "qodercn --version" {
		t.Fatalf("VersionCmd = %q, want qodercn", ag.Cfg.VersionCmd)
	}
	if ag.Cfg.RunCmd != `qodercn -p "%s" 2>&1` {
		t.Fatalf("RunCmd = %q, want qodercn command", ag.Cfg.RunCmd)
	}
	if ag.Cfg.SkillPath != ".qoder/skills" {
		t.Fatalf("SkillPath = %q, want shared project-level .qoder/skills", ag.Cfg.SkillPath)
	}
}

func TestNewQoderCLIAgent_DoesNotEnforceUnsupportedVersion(t *testing.T) {
	t.Parallel()

	ag := NewQoderCLIAgent(Config{Version: "1.2.3"})
	if ag.Cfg.Version != "" {
		t.Fatalf("Version = %q, want unsupported version constraint omitted", ag.Cfg.Version)
	}
	if ag.Cfg.VersionCmd != "qodercli --version" {
		t.Fatalf("VersionCmd = %q, want static version observation", ag.Cfg.VersionCmd)
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

func TestQoderCLIInstall_CNDefaultCommand(t *testing.T) {
	t.Parallel()

	profile := qoderProfileForKwargs(map[string]string{KwargEdition: qoderEditionCN})
	cmd := defaultQoderCLIInstallCmdForProfile(profile)
	for _, want := range []string{
		"command -v qodercn",
		"curl -fsSL https://static.qoder.com.cn/qoder-cli-cn/install.sh | bash",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("CN install command missing %q:\n%s", want, cmd)
		}
	}
}

func TestQoderCLIInstall_CNAddsDispatcherToPATH(t *testing.T) {
	t.Parallel()

	rt := &qoderTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			ExitCode: 0,
		},
	}
	ag := NewQoderCLIAgent(Config{
		Kwargs: map[string]string{KwargEdition: qoderEditionCN},
	})

	if err := ag.Install(context.Background(), rt); err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if got := rt.mergedEnv["PATH"]; !strings.Contains(got, "/.qoder-cn/entry:") {
		t.Fatalf("CN runtime PATH = %q, want .qoder-cn/entry", got)
	}
}

//nolint:dupl // mirrors TestQwenCodeInstall_UsesDefaultCommand; the probe→install→PATH lifecycle is intentionally identical across CLI agents.
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
	if rt.lastExecEnv[qoderExposeTokenUsageEnv] != qoderExposeTokenUsageEnabled {
		t.Fatalf("expected %s to default to true, got %q", qoderExposeTokenUsageEnv, rt.lastExecEnv[qoderExposeTokenUsageEnv])
	}
}

func TestQoderCLIRun_CNEditionUsesCNCommandAndEnv(t *testing.T) {
	t.Parallel()

	rt := &qoderTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			Stdout:   `{"type":"result","subtype":"success","result":"OK"}`,
			ExitCode: 0,
		},
	}
	ag := NewQoderCLIAgent(Config{
		Kwargs: map[string]string{KwargEdition: qoderEditionCN},
		EnvVars: map[string]string{
			credential.EnvQoderCNPersonalAccessToken: "cn-token",
		},
	})

	if _, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{
		Role: transcript.RoleUser, Content: "hello", Turn: 1,
	}}); err != nil {
		t.Fatalf("run qodercn: %v", err)
	}
	if !strings.Contains(rt.agentCommand, "qodercn --permission-mode=bypass_permissions --output-format json") {
		t.Fatalf("expected qodercn command, got %q", rt.agentCommand)
	}
	if got := rt.lastExecEnv[credential.EnvQoderCNPersonalAccessToken]; got != "cn-token" {
		t.Fatalf("%s = %q, want configured CN token", credential.EnvQoderCNPersonalAccessToken, got)
	}
	if got := rt.lastExecEnv[qoderCNExposeTokenUsageEnv]; got != qoderExposeTokenUsageEnabled {
		t.Fatalf("%s = %q, want true", qoderCNExposeTokenUsageEnv, got)
	}
	if _, exists := rt.lastExecEnv[qoderExposeTokenUsageEnv]; exists {
		t.Fatalf("global %s must not be injected for CN", qoderExposeTokenUsageEnv)
	}
}

func TestQoderCLIRun_PreservesExplicitTokenUsageSetting(t *testing.T) {
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
			qoderExposeTokenUsageEnv: "false",
		},
	})

	_, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{
		Role:    transcript.RoleUser,
		Content: "hello",
		Turn:    1,
	}})
	if err != nil {
		t.Fatalf("run qodercli: %v", err)
	}
	if rt.lastExecEnv[qoderExposeTokenUsageEnv] != "false" {
		t.Fatalf("expected explicit %s setting to be preserved, got %q", qoderExposeTokenUsageEnv, rt.lastExecEnv[qoderExposeTokenUsageEnv])
	}
}

func TestQoderCLIRun_ParsesJSONUsage(t *testing.T) {
	t.Parallel()

	rt := &qoderTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			Stdout: `{"type":"result","subtype":"success","result":"OK.","usage":{"input_tokens":100,"cache_read_input_tokens":20,"cache_creation_input_tokens":3,"output_tokens":7},"session_id":"qoder-session-json"}`,
		},
	}
	ag := NewQoderCLIAgent(Config{})

	result, err := ag.Run(context.Background(), rt, ExecOptions{}, []transcript.Message{{
		Role:    transcript.RoleUser,
		Content: "hello",
		Turn:    1,
	}})
	if err != nil {
		t.Fatalf("run qodercli: %v", err)
	}
	if result.InputTokens != 123 || result.OutputTokens != 7 {
		t.Fatalf("tokens = %d/%d, want 123/7", result.InputTokens, result.OutputTokens)
	}
	if result.FinalMessage != "OK." {
		t.Fatalf("FinalMessage = %q, want OK.", result.FinalMessage)
	}
	if result.SessionID != "qoder-session-json" {
		t.Fatalf("SessionID = %q, want qoder-session-json", result.SessionID)
	}
	if got := result.Transcript.FinalAssistantMessage(); got != "OK." {
		t.Fatalf("final transcript message = %q, want OK.", got)
	}
}

func TestBuildQoderRunCmd_WithModel(t *testing.T) {
	t.Parallel()

	cmd := buildQoderRunCmd("hello", "auto")

	if !strings.Contains(cmd, "qodercli --permission-mode=bypass_permissions --output-format json --model 'auto' -p 'hello'") {
		t.Fatalf("expected qoder command to include model flag, got %q", cmd)
	}
}

func TestBuildQoderRunCmd_WithoutModel(t *testing.T) {
	t.Parallel()

	cmd := buildQoderRunCmd("hello", "")
	if !strings.Contains(cmd, "--output-format json") {
		t.Fatalf("expected qoder command to request JSON output, got %q", cmd)
	}
	if strings.Contains(cmd, "--model") {
		t.Fatalf("expected qoder command to omit model flag, got %q", cmd)
	}
}

func TestBuildQoderResumeCmd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		instruction string
		model       string
		sessionID   string
		wantParts   []string
		wantAbsent  []string
	}{
		{
			name:        "with model",
			instruction: "continue",
			model:       modelAuto,
			sessionID:   "session-abc",
			wantParts:   []string{"--permission-mode=bypass_permissions", "--output-format json", "--model 'auto'", "-r 'session-abc'", "-p 'continue'"},
		},
		{
			name:        "without model",
			instruction: "next",
			model:       "",
			sessionID:   "sid-xyz",
			wantParts:   []string{"-r 'sid-xyz'", "-p 'next'"},
			wantAbsent:  []string{"--model"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := buildQoderResumeCmd(tt.instruction, tt.model, tt.sessionID)
			for _, part := range tt.wantParts {
				if !strings.Contains(cmd, part) {
					t.Fatalf("expected command to contain %q, got %q", part, cmd)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(cmd, absent) {
					t.Fatalf("expected command to NOT contain %q, got %q", absent, cmd)
				}
			}
		})
	}
}

func TestBuildQoderResumeCmd_CNEdition(t *testing.T) {
	t.Parallel()

	cmd := buildQoderResumeCmdForBinary("qodercn", "continue", "auto", "session-cn")
	for _, want := range []string{"qodercn ", "--model 'auto'", "-r 'session-cn'", "-p 'continue'"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("CN resume command missing %q: %q", want, cmd)
		}
	}
}

func TestQoderCLIRunTurn_FirstTurnDelegatesToRun(t *testing.T) {
	t.Parallel()

	rt := &qoderTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			Stdout:   "hello back\n",
			ExitCode: 0,
		},
	}
	ag := NewQoderCLIAgent(Config{ModelName: modelAuto})

	result, err := ag.RunTurn(context.Background(), rt, ExecOptions{}, transcript.Message{
		Role:    transcript.RoleUser,
		Content: "start",
		Turn:    1,
	}, "")
	if err != nil {
		t.Fatalf("RunTurn (first turn): %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// First turn should NOT use -r flag
	if strings.Contains(rt.agentCommand, " -r ") {
		t.Fatalf("first turn should not use -r flag: %q", rt.agentCommand)
	}
}

func TestQoderCLIRunTurn_ResumeUsesCorrectFlag(t *testing.T) {
	t.Parallel()

	rt := &qoderTestRuntime{
		workspace: t.TempDir(),
		execResult: runtime.ExecResult{
			Stdout:   "resumed answer\n",
			ExitCode: 0,
		},
	}
	ag := NewQoderCLIAgent(Config{ModelName: modelAuto})

	result, err := ag.RunTurn(context.Background(), rt, ExecOptions{}, transcript.Message{
		Role:    transcript.RoleUser,
		Content: "follow up",
		Turn:    2,
	}, "qoder-session-123")
	if err != nil {
		t.Fatalf("RunTurn (resume): %v", err)
	}
	if result.SessionID != "qoder-session-123" {
		t.Fatalf("expected SessionID = %q, got %q", "qoder-session-123", result.SessionID)
	}
	if !strings.Contains(rt.agentCommand, "-r 'qoder-session-123'") {
		t.Fatalf("expected -r flag with session ID, got %q", rt.agentCommand)
	}
	if !strings.Contains(rt.agentCommand, "-p 'follow up'") {
		t.Fatalf("expected -p flag with instruction, got %q", rt.agentCommand)
	}
	if rt.lastExecEnv[qoderExposeTokenUsageEnv] != qoderExposeTokenUsageEnabled {
		t.Fatalf("expected %s to default to true, got %q", qoderExposeTokenUsageEnv, rt.lastExecEnv[qoderExposeTokenUsageEnv])
	}
}

func TestQoderCLIAppliedModelName_AllowsSupportedModel(t *testing.T) {
	t.Parallel()

	ag := NewQoderCLIAgent(Config{ModelProvider: "anthropic", ModelName: "auto"})
	if got := ag.appliedModelName(context.Background()); got != "auto" {
		t.Fatalf("appliedModelName() = %q, want auto", got)
	}
}

func TestQoderCLIAppliedModelName_IgnoresUnsupportedConfiguredModel(t *testing.T) {
	t.Parallel()

	ag := NewQoderCLIAgent(Config{ModelProvider: "anthropic", ModelName: "claude-sonnet-4-6"})
	if got := ag.appliedModelName(context.Background()); got != "" {
		t.Fatalf("appliedModelName() = %q, want empty", got)
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
	// user prompt, tool call, tool result, assistant text. The tool_use block is
	// represented by the tool_call message only; it does not also appear as
	// assistant prose.
	if len(trans) != 4 {
		t.Fatalf("expected 4 transcript messages, got %d: %#v", len(trans), trans)
	}
	if trans[1].Role != transcript.RoleToolCall || trans[1].ToolCall == nil {
		t.Fatalf("expected tool call message, got %#v", trans[1])
	}
	if trans[1].ToolCall.Name != "mcp__agent-sandbox__create_sandbox" {
		t.Fatalf("tool call name = %q", trans[1].ToolCall.Name)
	}
	if trans[2].Role != transcript.RoleToolResult || trans[2].ToolResult == nil {
		t.Fatalf("expected tool result message, got %#v", trans[2])
	}
	if trans[2].ToolResult.CallID != "call_1" || trans[2].ToolResult.Status != "success" {
		t.Fatalf("unexpected tool result: %#v", trans[2].ToolResult)
	}
}

func TestFindQoderSessionFile(t *testing.T) {
	t.Skip("findQoderSessionFile uses rt.Workspace() which NoneRuntime cannot set to the desired test workspace")
}

func TestFindQoderSessionFile_SelectsNewestByModTime(t *testing.T) {
	if goruntime.GOOS == "windows" {
		// The workspace-key path layout embeds a Linux-style workspace path,
		// which contains a colon on Windows (e.g. `C:`) and cannot be a
		// directory component. Qoder native Windows agent execution is out of
		// scope; this test is exercised on Linux/darwin only.
		t.Skip("qoder workspace-key path layout is POSIX-only")
	}
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

func TestFindQoderCNSessionFile_SelectsCNConfigDir(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("qoder workspace-key path layout is POSIX-only")
	}
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	rt, err := runtime.NewRuntime(runtime.Config{Type: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	workspace := rt.Workspace()
	if realPath, evalErr := filepath.EvalSymlinks(workspace); evalErr == nil {
		workspace = realPath
	}
	workspaceKey := strings.ReplaceAll(workspace, "/", "-")
	projectDir := filepath.Join(tmpHome, ".qoder-cn", "projects", workspaceKey)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(projectDir, "cn-session.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	ag := NewQoderCLIAgent(Config{Kwargs: map[string]string{KwargEdition: qoderEditionCN}})
	if got := ag.findSessionFile(context.Background(), rt); got != sessionFile {
		t.Fatalf("CN session file = %q, want %q", got, sessionFile)
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
	agentCommand        string // first non-probe, non-intercepted command
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
	if command == qoderExecPathProbeCmd || command == qoderCNExecPathProbeCmd {
		stdout := r.probeResponseStdout
		if stdout == "" {
			stdout = "/fake/.local/bin:/usr/bin"
			if command == qoderCNExecPathProbeCmd {
				stdout = "/fake/.qoder-cn/entry:/fake/.local/bin:/usr/bin"
			}
		}
		return runtime.ExecResult{Stdout: stdout}, nil
	}
	// Session file lookup scripts start with printenv HOME; treat them as
	// background operations that do not overwrite the agent command.
	if strings.HasPrefix(command, "home=$(printenv HOME)") {
		return runtime.ExecResult{}, nil
	}
	r.lastCommand = command
	if r.agentCommand == "" {
		r.agentCommand = command
	}
	if strings.Contains(command, "qodercli --permission-mode=bypass_permissions") ||
		strings.Contains(command, "qodercn --permission-mode=bypass_permissions") ||
		strings.Contains(command, "qodercli -p ") ||
		strings.Contains(command, "qodercn -p ") ||
		strings.Contains(command, "qoder.com/install") ||
		strings.Contains(command, "qoder.com.cn/qoder-cli-cn/install.sh") {
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

func (r *qoderTestRuntime) Shell() platform.Shell {
	return platform.Shell{GOOS: platform.GOOSLinux, Family: platform.ShellPOSIX}
}
