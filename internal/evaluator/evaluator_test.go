package evaluator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/internal/platform"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

var logCaptureMu sync.Mutex

type mockAgent struct {
	name         string
	output       string
	err          error
	credErr      error
	skillErr     error
	runFunc      func(ctx context.Context, rt runtime.Runtime, opts agent.ExecOptions, messages []transcript.Message) (*agent.SessionResult, error)
	runCall      atomic.Int32
	credCall     atomic.Int32
	installCall  atomic.Int32
	mcpCall      atomic.Int32
	skillCall    atomic.Int32
	skillPath    string
	mu           sync.Mutex
	lastMessages []transcript.Message
	lastSkill    runtime.SkillConfig
	skills       []runtime.SkillConfig
}

func (m *mockAgent) Name() string { return m.name }
func (m *mockAgent) SkillPath() string {
	return m.skillPath
}

func (m *mockAgent) Install(_ context.Context, _ runtime.Runtime) error {
	m.installCall.Add(1)
	return nil
}

func (m *mockAgent) InstallMCP(_ context.Context, _ runtime.Runtime, _ runtime.MCPConfig) error {
	m.mcpCall.Add(1)
	return nil
}

func (m *mockAgent) InstallSkill(_ context.Context, _ runtime.Runtime, cfg runtime.SkillConfig) error {
	m.skillCall.Add(1)
	m.mu.Lock()
	m.lastSkill = cfg
	m.skills = append(m.skills, cfg)
	m.mu.Unlock()
	return m.skillErr
}
func (m *mockAgent) Check(_ context.Context, _ runtime.Runtime) error { return nil }
func (m *mockAgent) CheckCredentials(_ context.Context) error {
	m.credCall.Add(1)
	return m.credErr
}

func (m *mockAgent) Run(ctx context.Context, rt runtime.Runtime, opts agent.ExecOptions, messages []transcript.Message) (*agent.SessionResult, error) {
	m.runCall.Add(1)
	m.mu.Lock()
	m.lastMessages = messages
	m.mu.Unlock()
	if m.runFunc != nil {
		return m.runFunc(ctx, rt, opts, messages)
	}
	return &agent.SessionResult{FinalMessage: m.output}, m.err
}

type mockRuntime struct {
	workspace        string
	shell            platform.Shell
	downloadFileFunc func(ctx context.Context, sourcePath, targetPath string) error
	downloadFileCall atomic.Int32
	downloadDirFunc  func(ctx context.Context, sourceDir, targetDir string) error
	downloadDirCall  atomic.Int32
	execEnv          map[string]string
	execCall         atomic.Int32
	execFunc         func(ctx context.Context, command string, opts runtime.ExecOptions) (runtime.ExecResult, error)
}

func (m *mockRuntime) Create(_ context.Context) error { return nil }
func (m *mockRuntime) Close() error                   { return nil }
func (m *mockRuntime) Workspace() string              { return m.workspace }
func (m *mockRuntime) RequiresProcessSandbox() bool   { return true }
func (m *mockRuntime) MergeEnv(_ map[string]string)   {}

func (m *mockRuntime) Shell() platform.Shell {
	if m.shell.GOOS != "" {
		return m.shell
	}
	return platform.Shell{GOOS: platform.GOOSLinux, Family: platform.ShellPOSIX}
}
func (m *mockRuntime) Start(_ context.Context) error                   { return nil }
func (m *mockRuntime) Stop(_ context.Context) error                    { return nil }
func (m *mockRuntime) UploadFile(_ context.Context, _, _ string) error { return nil }
func (m *mockRuntime) UploadDir(_ context.Context, _, _ string) error  { return nil }
func (m *mockRuntime) DownloadFile(ctx context.Context, sourcePath, targetPath string) error {
	m.downloadFileCall.Add(1)
	if m.downloadFileFunc != nil {
		return m.downloadFileFunc(ctx, sourcePath, targetPath)
	}
	data, err := os.ReadFile(filepath.Join(m.workspace, sourcePath))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(targetPath, data, 0o600) // #nosec G306,G703 -- test helper writes controlled temp targets
}

func (m *mockRuntime) DownloadDir(ctx context.Context, sourceDir, targetDir string) error {
	m.downloadDirCall.Add(1)
	if m.downloadDirFunc != nil {
		return m.downloadDirFunc(ctx, sourceDir, targetDir)
	}
	source := m.workspace
	if sourceDir != "." && sourceDir != "" {
		source = filepath.Join(source, sourceDir)
	}
	return copyDir(source, targetDir)
}

func (m *mockRuntime) Exec(ctx context.Context, command string, opts runtime.ExecOptions) (runtime.ExecResult, error) {
	m.execCall.Add(1)
	if m.execFunc != nil {
		return m.execFunc(ctx, command, opts)
	}
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	} else {
		cmd.Dir = m.workspace
	}
	if len(opts.Env) > 0 {
		env := os.Environ()
		for k, v := range opts.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	if len(m.execEnv) > 0 {
		env := os.Environ()
		for k, v := range m.execEnv {
			env = append(env, k+"="+v)
		}
		for k, v := range opts.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := runtime.ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			result.ExitCode = -1
			return result, err
		}
		result.ExitCode = exitErr.ExitCode()
	}
	return result, nil
}

func copyDir(sourceDir, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	return os.CopyFS(targetDir, os.DirFS(sourceDir))
}

func newTestEvaluator(opts EvalOptions) *defaultEvaluator {
	e := NewEvaluator(opts)
	de, ok := e.(*defaultEvaluator)
	if !ok {
		panic("expected *defaultEvaluator")
	}
	return de
}

func findEndedSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	t.Fatalf("span %q not recorded: %v", name, spans)
	return nil
}

func TestProvisionMCPConfigCachesResolvedConfig(t *testing.T) {
	t.Setenv("MCP_TOKEN", "first-token")

	skillDir := t.TempDir()
	configPath := filepath.Join(skillDir, "mcp.yaml")
	if err := os.WriteFile(configPath, []byte("endpoint: https://mcp.example.test?token=${MCP_TOKEN}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	e := newTestEvaluator(EvalOptions{
		SkillDir: skillDir,
		EvalCfg: &config.EvalConfig{
			MCP: config.MCPConfig{
				Servers: []config.MCPServer{{Name: "agent-sandbox", Mode: "real", ConfigRef: "mcp.yaml"}},
			},
		},
	})

	firstCfg, firstEnv, err := e.provisionMCPConfig()
	if err != nil {
		t.Fatalf("first provision failed: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("endpoint: \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	firstCfg.Servers[0].Endpoint = "mutated"
	firstEnv["MCP_TOKEN"] = "mutated"

	secondCfg, secondEnv, err := e.provisionMCPConfig()
	if err != nil {
		t.Fatalf("second provision should use cached config: %v", err)
	}
	if got := secondCfg.Servers[0].Endpoint; got != "https://mcp.example.test?token=${MCP_TOKEN}" {
		t.Fatalf("cached endpoint = %q", got)
	}
	if got := secondEnv["MCP_TOKEN"]; got != "first-token" {
		t.Fatalf("cached env = %q", got)
	}
}

func TestProvisionMCPConfigForCaseAppliesOverrides(t *testing.T) {
	t.Parallel()

	skillDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(skillDir, "default.yaml"), []byte("tool_responses:\n  get_project:\n    default:\n      status: DEFAULT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "open.yaml"), []byte("tool_responses:\n  get_project:\n    default:\n      status: OPEN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "closed.yaml"), []byte("tool_responses:\n  get_project:\n    default:\n      status: CLOSED\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	e := newTestEvaluator(EvalOptions{
		SkillDir: skillDir,
		EvalCfg: &config.EvalConfig{
			MCP: config.MCPConfig{
				Servers: []config.MCPServer{{Name: "project-mgmt", Mode: "mocked", ConfigRef: "default.yaml"}},
			},
		},
	})

	openCase := &config.CaseConfig{
		ID: "project-open",
		MCP: config.MCPConfig{
			Servers: []config.MCPServer{{Name: "project-mgmt", Mode: "mocked", ConfigRef: "open.yaml"}},
		},
	}
	closedCase := &config.CaseConfig{
		ID: "project-closed",
		MCP: config.MCPConfig{
			Servers: []config.MCPServer{{Name: "project-mgmt", Mode: "mocked", ConfigRef: "closed.yaml"}},
		},
	}
	plainCase := &config.CaseConfig{ID: "plain"}

	openCfg, _, err := e.provisionMCPConfigForCase(openCase)
	if err != nil {
		t.Fatalf("provision open case failed: %v", err)
	}
	closedCfg, _, err := e.provisionMCPConfigForCase(closedCase)
	if err != nil {
		t.Fatalf("provision closed case failed: %v", err)
	}
	plainCfg, _, err := e.provisionMCPConfigForCase(plainCase)
	if err != nil {
		t.Fatalf("provision plain case failed: %v", err)
	}

	openScript := openCfg.Servers[0].Args[1]
	closedScript := closedCfg.Servers[0].Args[1]
	plainScript := plainCfg.Servers[0].Args[1]

	if !strings.Contains(openScript, "OPEN") {
		t.Errorf("open case script missing OPEN fixture")
	}
	if !strings.Contains(closedScript, "CLOSED") {
		t.Errorf("closed case script missing CLOSED fixture")
	}
	if !strings.Contains(plainScript, "DEFAULT") {
		t.Errorf("plain case should inherit eval-level DEFAULT fixture")
	}
	if openScript == closedScript {
		t.Error("same server name with different config_ref must yield different runtime MCP config")
	}
}

func TestProvisionMCPConfigForCaseRejectsRealOverride(t *testing.T) {
	t.Parallel()

	e := newTestEvaluator(EvalOptions{
		SkillDir: t.TempDir(),
		EvalCfg:  &config.EvalConfig{},
	})
	badCase := &config.CaseConfig{
		ID: "bad-case",
		MCP: config.MCPConfig{
			Servers: []config.MCPServer{{Name: "svc", Mode: "real"}},
		},
	}
	_, _, err := e.provisionMCPConfigForCase(badCase)
	if err == nil || !strings.Contains(err.Error(), "bad-case") {
		t.Fatalf("expected error mentioning case ID, got %v", err)
	}
}

func TestEvaluatorInputHelpers(t *testing.T) {
	t.Parallel()

	promptCfg := &config.CaseConfig{Input: config.Input{Prompt: "single prompt"}}
	prompt, turns := casePromptAndTurnsTotal(promptCfg)
	if prompt != "single prompt" || turns != 1 {
		t.Fatalf("prompt case = %q/%d, want single prompt/1", prompt, turns)
	}
	if messages := buildCaseMessages(promptCfg); len(messages) != 1 || messages[0].Role != transcript.RoleUser || messages[0].Turn != 1 {
		t.Fatalf("prompt messages = %#v, want one user message", messages)
	}

	turnCfg := &config.CaseConfig{Input: config.Input{Turns: []config.Turn{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "second"},
		{Content: "third defaults to user"},
	}}}
	prompt, turns = casePromptAndTurnsTotal(turnCfg)
	if prompt != "first" || turns != 3 {
		t.Fatalf("turn case = %q/%d, want first/3", prompt, turns)
	}
	messages := buildCaseMessages(turnCfg)
	if len(messages) != 3 || messages[1].Role != transcript.RoleAssistant || messages[2].Role != transcript.RoleUser || messages[2].Turn != 3 {
		t.Fatalf("turn messages = %#v, want preserved roles and turn numbers", messages)
	}
}

func TestExecuteCaseWarnsWhenTurnsFallbackToSingleBatch(t *testing.T) {
	ag := &mockAgent{name: "batch-only", output: "batched response"}
	rt := &mockRuntime{workspace: t.TempDir()}
	e := newTestEvaluator(EvalOptions{
		Agent:   ag,
		EvalCfg: &config.EvalConfig{},
	})
	caseCfg := &config.CaseConfig{
		ID: "turns-fallback",
		Input: config.Input{
			Turns: []config.Turn{
				{Role: "user", Content: "first"},
				{Role: "user", Content: "second"},
			},
		},
	}

	output := captureStdout(t, func() {
		_ = e.executeCase(context.Background(), caseCfg, "with_skill", rt, nil)
	})
	if !strings.Contains(output, "does not support session resumption") {
		t.Fatalf("expected fallback warning, got %q", output)
	}
	if ag.runCall.Load() != 1 {
		t.Fatalf("fallback should run the agent once, got %d calls", ag.runCall.Load())
	}
}

func TestEvaluatorRetryAndRecoveryHelpers(t *testing.T) {
	t.Parallel()

	policy := config.RetryPolicy{MaxRetries: 2, RetryOn: []string{"timeout"}}
	if !retryAllowed(policy, "TIMEOUT") {
		t.Fatal("retryAllowed should match retry reasons case-insensitively")
	}
	if retryAllowed(policy, "error") {
		t.Fatal("retryAllowed unexpectedly allowed unconfigured error reason")
	}
	if retryBackoffDelay(0) != 2*time.Second || retryBackoffDelay(2) != 4*time.Second {
		t.Fatalf("retryBackoffDelay produced unexpected values")
	}

	result := EvalResult{Status: judge.StatusError, Error: context.DeadlineExceeded}
	if reason, ok := retryReasonForResult(result); !ok || reason != "timeout" {
		t.Fatalf("retryReasonForResult(timeout) = %q/%v, want timeout true", reason, ok)
	}
	result.Error = errors.New("boom")
	if reason, ok := retryReasonForResult(result); !ok || reason != "error" {
		t.Fatalf("retryReasonForResult(error) = %q/%v, want error true", reason, ok)
	}
	result.Status = judge.StatusFail
	if _, ok := retryReasonForResult(result); ok {
		t.Fatal("retryReasonForResult should ignore non-error statuses")
	}

	timeoutResult := &EvalResult{Error: fmt.Errorf("agent failed: %w", context.DeadlineExceeded)}
	annotateCaseTimeoutError(timeoutResult, "cases.defaults.timeout_seconds", 30)
	if !strings.Contains(timeoutResult.Error.Error(), "case timeout 30s via cases.defaults.timeout_seconds") {
		t.Fatalf("annotated timeout error = %v", timeoutResult.Error)
	}
	if !errors.Is(timeoutResult.Error, context.DeadlineExceeded) {
		t.Fatalf("annotation lost DeadlineExceeded in error chain: %v", timeoutResult.Error)
	}

	nonTimeoutResult := &EvalResult{Error: errors.New("boom")}
	annotateCaseTimeoutError(nonTimeoutResult, "cases.defaults.timeout_seconds", 30)
	if nonTimeoutResult.Error.Error() != "boom" {
		t.Fatalf("non-timeout error was annotated: %v", nonTimeoutResult.Error)
	}
}

func TestEvaluatorSessionHelpers(t *testing.T) {
	t.Parallel()

	original := &agent.SessionResult{
		FinalMessage: "stale",
		Transcript: transcript.Transcript{
			{Role: transcript.RoleAssistant, Content: "fresh"},
			{Role: transcript.RoleToolCall, Content: "tool"},
		},
		Artifacts: &agent.SessionArtifacts{
			WorkspaceDiff:  "diff",
			GeneratedFiles: []string{"a.txt"},
		},
	}
	normalized := normalizeSessionResult(original)
	if normalized.FinalMessage != "fresh" {
		t.Fatalf("normalized FinalMessage = %q, want fresh", normalized.FinalMessage)
	}
	if original.FinalMessage != "stale" {
		t.Fatalf("normalizeSessionResult mutated original final message to %q", original.FinalMessage)
	}
	if sessionTranscript(original).FinalAssistantMessage() != "fresh" {
		t.Fatal("sessionTranscript did not return transcript")
	}
	if sessionWorkspaceDiff(original) != "diff" {
		t.Fatal("sessionWorkspaceDiff did not return diff")
	}
	if got := sessionGeneratedFiles(original); len(got) != 1 || got[0] != "a.txt" {
		t.Fatalf("sessionGeneratedFiles = %#v, want a.txt", got)
	}
	if normalizeSessionResult(nil) == nil {
		t.Fatal("normalizeSessionResult(nil) returned nil")
	}
	if sessionTranscript(nil) != nil || sessionWorkspaceDiff(nil) != "" || sessionGeneratedFiles(nil) != nil {
		t.Fatal("nil session helpers should return empty values")
	}
}

func TestEvaluatorConfigHelpers(t *testing.T) {
	t.Parallel()

	e := newTestEvaluator(EvalOptions{})
	if e.judgeScriptBaseDir() != "" {
		t.Fatalf("judgeScriptBaseDir without loader = %q, want empty", e.judgeScriptBaseDir())
	}

	skillDir := t.TempDir()
	rel := resolveJudgeScriptPath(skillDir, config.JudgeConfig{Type: "script", ScriptPath: "checks/pass.sh"})
	want := filepath.Join(skillDir, "checks", "pass.sh")
	if rel.ScriptPath != want {
		t.Fatalf("resolveJudgeScriptPath relative = %q, want %q", rel.ScriptPath, want)
	}
	abs := resolveJudgeScriptPath(skillDir, config.JudgeConfig{Type: "script", ScriptPath: want})
	if abs.ScriptPath != want {
		t.Fatalf("resolveJudgeScriptPath absolute = %q, want unchanged", abs.ScriptPath)
	}
	unchanged := resolveJudgeScriptPath(skillDir, config.JudgeConfig{Type: "rule_based", ScriptPath: "checks/pass.sh"})
	if unchanged.ScriptPath != "checks/pass.sh" {
		t.Fatalf("non-script path changed to %q", unchanged.ScriptPath)
	}

	if resolveExpectConfig(&config.Expect{}) != nil {
		t.Fatal("empty expect config should resolve to nil")
	}
	exitCode := 0
	if resolveExpectConfig(&config.Expect{ExitCode: &exitCode}) == nil {
		t.Fatal("expect config with exit_code should be active")
	}
	if judgeLabel(config.JudgeConfig{}) != "no_judge" || judgeLabel(config.JudgeConfig{Type: "rule_based"}) != "rule_based" {
		t.Fatal("judgeLabel returned unexpected values")
	}
}

func TestSetupCaseEnvironmentRunsSetupAndInstallsAgentMCPAndSkill(t *testing.T) {
	skillDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	evalsDir := filepath.Join(skillDir, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	evalPath := filepath.Join(evalsDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte("schema_version: v1alpha1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rt := &mockRuntime{workspace: t.TempDir()}
	ag := &mockAgent{name: "agent"}
	e := newTestEvaluator(EvalOptions{
		SkillDir: skillDir,
		Loader:   config.NewLoader(evalPath),
		EvalCfg: &config.EvalConfig{
			Environment: config.Environment{
				Type:       "opensandbox",
				SetupSteps: []config.SetupStep{{Run: "printf setup > marker.txt"}},
			},
			Skills: []config.SkillRef{{Path: ".", Target: "custom-target"}},
		},
	})

	err := e.setupCaseEnvironment(context.Background(), rt, &config.CaseConfig{ID: "case-a"}, "with_skill", ag, runtime.MCPConfig{})
	if err != nil {
		t.Fatalf("setupCaseEnvironment returned error: %v", err)
	}
	if rt.execCall.Load() != 1 {
		t.Fatalf("setup exec calls = %d, want 1", rt.execCall.Load())
	}
	if ag.installCall.Load() != 1 || ag.mcpCall.Load() != 1 || ag.skillCall.Load() != 1 {
		t.Fatalf("agent install calls install/mcp/skill = %d/%d/%d, want 1/1/1", ag.installCall.Load(), ag.mcpCall.Load(), ag.skillCall.Load())
	}
	ag.mu.Lock()
	lastSkill := ag.lastSkill
	ag.mu.Unlock()
	if lastSkill.Source != skillDir || lastSkill.Target != "custom-target" {
		t.Fatalf("last skill config = %+v, want source skill dir and custom target", lastSkill)
	}

	withoutSkillAgent := &mockAgent{name: "agent"}
	if err := e.setupCaseEnvironment(context.Background(), rt, &config.CaseConfig{ID: "case-a"}, "without_skill", withoutSkillAgent, runtime.MCPConfig{}); err != nil {
		t.Fatalf("setup without_skill returned error: %v", err)
	}
	if withoutSkillAgent.skillCall.Load() != 0 {
		t.Fatalf("without_skill installed skill %d time(s), want 0", withoutSkillAgent.skillCall.Load())
	}
}

func TestSetupCaseEnvironmentReportsSetupFailures(t *testing.T) {
	t.Parallel()

	e := newTestEvaluator(EvalOptions{EvalCfg: &config.EvalConfig{
		Environment: config.Environment{SetupSteps: []config.SetupStep{{Run: "exit 2"}}},
	}})
	rt := &mockRuntime{
		workspace: t.TempDir(),
		execFunc: func(context.Context, string, runtime.ExecOptions) (runtime.ExecResult, error) {
			return runtime.ExecResult{ExitCode: 2, Stderr: "bad setup"}, nil
		},
	}
	err := e.setupCaseEnvironment(context.Background(), rt, &config.CaseConfig{ID: "case-a"}, "with_skill", &mockAgent{name: "agent"}, runtime.MCPConfig{})
	if err == nil || !strings.Contains(err.Error(), "bad setup") {
		t.Fatalf("setup error = %v, want stderr", err)
	}

	rt.execFunc = func(context.Context, string, runtime.ExecOptions) (runtime.ExecResult, error) {
		return runtime.ExecResult{}, errors.New("boom")
	}
	err = e.setupCaseEnvironment(context.Background(), rt, &config.CaseConfig{ID: "case-a"}, "with_skill", &mockAgent{name: "agent"}, runtime.MCPConfig{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("setup exec error = %v, want boom", err)
	}
}

func TestSerializeTranscriptWritesJSONAndCleanupRemovesFile(t *testing.T) {
	t.Parallel()

	path, cleanup, err := serializeTranscript(transcript.Transcript{
		{Role: transcript.RoleUser, Content: "hello", Turn: 1},
		{Role: transcript.RoleAssistant, Content: "hi", Turn: 1},
	})
	if err != nil {
		t.Fatalf("serializeTranscript returned error: %v", err)
	}
	t.Cleanup(cleanup)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	if !strings.Contains(string(data), `"role":"assistant"`) || !strings.Contains(string(data), `"content":"hi"`) {
		t.Fatalf("serialized transcript = %s", data)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cleanup did not remove transcript file, stat err=%v", err)
	}
}

func TestArtifactOutputHelpersDownloadOnlyMissingFiles(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	e := newTestEvaluator(EvalOptions{OutputDir: outputDir})
	prepared := e.prepareOutputDir(context.Background(), "with_skill", "case-a", "agent/run")
	if prepared == "" {
		t.Fatal("prepareOutputDir returned empty path")
	}
	inside := filepath.Join(prepared, "already.txt")
	if err := os.WriteFile(inside, []byte("already"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !artifactsInDir([]string{inside}, prepared) {
		t.Fatal("artifactsInDir should accept files already in prepared directory")
	}
	if artifactInCleanDir("relative.txt", filepath.Clean(prepared)) {
		t.Fatal("relative artifact should not be treated as already copied")
	}

	rt := &mockRuntime{
		workspace: t.TempDir(),
		downloadFileFunc: func(_ context.Context, sourcePath, targetPath string) error {
			return os.WriteFile(targetPath, []byte(filepath.Base(sourcePath)), 0o600)
		},
	}
	session := &agent.SessionResult{Artifacts: &agent.SessionArtifacts{GeneratedFiles: []string{inside, "/remote/new.txt"}}}
	e.ensureArtifactsInOutputDir(context.Background(), rt, "with_skill", "case-a", "agent/run", prepared, session)
	if rt.downloadFileCall.Load() != 1 {
		t.Fatalf("download calls = %d, want only missing remote artifact", rt.downloadFileCall.Load())
	}
	data, err := os.ReadFile(filepath.Join(prepared, "new.txt"))
	if err != nil {
		t.Fatalf("downloaded artifact missing: %v", err)
	}
	if string(data) != "new.txt" {
		t.Fatalf("downloaded artifact data = %q", data)
	}

	e.downloadArtifacts(context.Background(), rt, "with_skill", "case-b", "judge/run", &agent.SessionResult{
		Artifacts: &agent.SessionArtifacts{GeneratedFiles: []string{"/remote/judge.txt"}},
	})
	if _, err := os.Stat(filepath.Join(outputDir, "case-b", "with_skill", "outputs", "judge", "run", "judge.txt")); err != nil {
		t.Fatalf("downloadArtifacts did not write judge artifact: %v", err)
	}
}

func assertEndedSpanAttr(t *testing.T, attrs []attribute.KeyValue, key, want string) {
	t.Helper()
	for _, attr := range attrs {
		if string(attr.Key) == key {
			if got := attr.Value.AsString(); got != want {
				t.Fatalf("%s = %q, want %q", key, got, want)
			}
			return
		}
	}
	t.Fatalf("missing span attr %s in %v", key, attrs)
}

func TestExecuteCase_AgentError(t *testing.T) {
	// Simulate a truly fatal agent error (nil SessionResult, e.g. process crash)
	// — distinct from a normal non-zero exit which should flow through expect/judge.
	agentWithFatalError := &mockAgent{
		name: "test",
		runFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
			return nil, errors.New("agent failed")
		},
	}
	e := newTestEvaluator(EvalOptions{
		Agent: agentWithFatalError,
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-1",
		Title: "Test Case",
		Input: config.Input{Prompt: "hello"},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: "/tmp/test"}, agentWithFatalError)

	if result.Status != judge.StatusError {
		t.Errorf("expected ERROR status, got %s", result.Status)
	}
	if result.Error == nil {
		t.Error("expected non-nil error")
	}
	if result.CaseID != "case-1" {
		t.Errorf("expected case-1, got %s", result.CaseID)
	}
}

func TestExecuteCase_PassesAgentArtifactDir(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	rt := &mockRuntime{workspace: t.TempDir()}
	ag := &mockAgent{name: "test"}
	ag.runFunc = func(_ context.Context, _ runtime.Runtime, opts agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
		wantDir := filepath.Join(outputDir, "case-artifacts", "with_skill", "outputs", "agent", "run")
		if opts.ArtifactDir != wantDir {
			t.Fatalf("ArtifactDir = %q, want %q", opts.ArtifactDir, wantDir)
		}
		artifactPath := filepath.Join(opts.ArtifactDir, "stdout.json")
		if err := os.WriteFile(artifactPath, []byte(`{"ok":true}`), 0o600); err != nil {
			t.Fatalf("write artifact: %v", err)
		}
		return &agent.SessionResult{
			FinalMessage: "ok",
			Artifacts:    &agent.SessionArtifacts{},
		}, nil
	}

	e := newTestEvaluator(EvalOptions{
		Agent:     ag,
		OutputDir: outputDir,
	})
	caseCfg := &config.CaseConfig{
		ID:    "case-artifacts",
		Title: "Artifact Dir",
		Input: config.Input{Prompt: "hello"},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", rt, nil)
	if result.Status != judge.StatusPass {
		t.Fatalf("expected PASS status, got %s", result.Status)
	}
	if rt.downloadFileCall.Load() != 0 {
		t.Fatalf("expected no DownloadFile calls for direct artifacts, got %d", rt.downloadFileCall.Load())
	}
	if _, err := os.Stat(filepath.Join(outputDir, "case-artifacts", "with_skill", "outputs", "agent", "run", "stdout.json")); err != nil {
		t.Fatalf("expected direct artifact to exist: %v", err)
	}
}

// Strict-budget regression: an agent run that returns context.DeadlineExceeded
// must surface as ERROR regardless of whether the partial sessionResult would
// have satisfied the judge. Previously this case was salvaged by re-running
// the judge against the truncated transcript; that behaviour is intentionally
// removed so the case timeout strictly bounds agent + judge together.
func TestExecuteCase_AgentTimeoutWithPartialResultStillErrors(t *testing.T) {
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test"},
		EvalCfg: &config.EvalConfig{
			Judge: config.JudgeConfig{
				Type: "rule_based",
				Success: []config.Rule{{
					OutputContains: &config.OutputContainsRule{All: []string{"hello"}},
				}},
			},
		},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-timeout-no-salvage",
		Title: "Timed-out agent is not salvaged through judge",
		Input: config.Input{Prompt: "hello"},
	}

	ag := &mockAgent{
		name: "test",
		runFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
			return &agent.SessionResult{
				FinalMessage: "hello from recovered result",
				ExitCode:     -1,
			}, context.DeadlineExceeded
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: t.TempDir()}, ag)

	if result.Status != judge.StatusError {
		t.Fatalf("expected ERROR (no salvage), got %s (err=%v)", result.Status, result.Error)
	}
	if result.Error == nil || !strings.Contains(result.Error.Error(), "agent execution failed") {
		t.Fatalf("expected agent execution failed error, got %v", result.Error)
	}
}

func TestExecuteCase_AgentTimeoutWithoutRecoveredResultErrors(t *testing.T) {
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test"},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-timeout-no-result",
		Title: "Timeout without useful result",
		Input: config.Input{Prompt: "hello"},
	}

	ag := &mockAgent{
		name: "test",
		runFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
			return &agent.SessionResult{ExitCode: -1}, context.DeadlineExceeded
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: t.TempDir()}, ag)

	if result.Status != judge.StatusError {
		t.Fatalf("expected ERROR when timed out agent has no recoverable result, got %s", result.Status)
	}
	if result.Error == nil || !strings.Contains(result.Error.Error(), "agent execution failed") {
		t.Fatalf("expected agent execution failed error, got %v", result.Error)
	}
}

func TestExecuteCase_TimeoutErrorNamesConfigKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		evalDefault int
		caseLimit   int
		wantSource  string
		wantSeconds int
	}{
		{
			name:        "case constraint wins over eval default",
			evalDefault: 60,
			caseLimit:   1,
			wantSource:  "case.constraints.timeout_seconds",
			wantSeconds: 1,
		},
		{
			name:        "eval default applies when case has none",
			evalDefault: 1,
			caseLimit:   0,
			wantSource:  "cases.defaults.timeout_seconds",
			wantSeconds: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := newTestEvaluator(EvalOptions{
				Agent: &mockAgent{name: "test"},
				EvalCfg: &config.EvalConfig{
					Cases: config.CasesConfig{
						Defaults: config.CaseDefaults{TimeoutSeconds: tt.evalDefault},
					},
				},
			})

			caseCfg := &config.CaseConfig{
				ID:          "case-timeout-annotation",
				Title:       "Timeout error names the configured knob",
				Input:       config.Input{Prompt: "hello"},
				Constraints: config.Constraints{TimeoutSeconds: tt.caseLimit},
			}

			ag := &mockAgent{
				name: "test",
				runFunc: func(ctx context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
					<-ctx.Done()
					return &agent.SessionResult{ExitCode: -1}, ctx.Err()
				},
			}

			result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: t.TempDir()}, ag)

			if result.Status != judge.StatusError {
				t.Fatalf("expected ERROR, got %s", result.Status)
			}
			if result.Error == nil {
				t.Fatal("expected non-nil error")
			}
			if !errors.Is(result.Error, context.DeadlineExceeded) {
				t.Fatalf("expected error chain to contain context.DeadlineExceeded, got %v", result.Error)
			}
			want := fmt.Sprintf("case timeout %ds via %s", tt.wantSeconds, tt.wantSource)
			if !strings.Contains(result.Error.Error(), want) {
				t.Fatalf("expected error to mention %q, got %v", want, result.Error)
			}
		})
	}
}

// Regression: a child deadline that fires while the case context still has
// budget (e.g. judge.timeout_seconds shorter than cases.defaults.timeout_seconds)
// must NOT be relabelled as a case timeout. Pointing users at the wrong YAML
// knob is the exact problem this PR's error annotation is meant to prevent.
func TestExecuteCase_ChildDeadlineNotMislabeledAsCaseTimeout(t *testing.T) {
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test"},
		EvalCfg: &config.EvalConfig{
			// Generous case budget — should never fire in this test.
			Cases: config.CasesConfig{
				Defaults: config.CaseDefaults{TimeoutSeconds: 60},
			},
		},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-child-deadline",
		Title: "Child deadline does not get a case-timeout label",
		Input: config.Input{Prompt: "hello"},
	}

	// Agent returns DeadlineExceeded immediately from a fresh, expired child
	// ctx. This simulates a tighter inner timeout (judge.timeout_seconds or
	// any other layer) firing while the case ctx is still well within budget.
	ag := &mockAgent{
		name: "test",
		runFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
			child, cancel := context.WithTimeout(context.Background(), 0)
			defer cancel()
			<-child.Done()
			return &agent.SessionResult{ExitCode: -1}, child.Err()
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: t.TempDir()}, ag)

	if result.Status != judge.StatusError {
		t.Fatalf("expected ERROR, got %s", result.Status)
	}
	if !errors.Is(result.Error, context.DeadlineExceeded) {
		t.Fatalf("expected error chain to contain context.DeadlineExceeded, got %v", result.Error)
	}
	if strings.Contains(result.Error.Error(), "case timeout") {
		t.Fatalf("case ctx did not fire, error must not be annotated with 'case timeout', got %v", result.Error)
	}
}

// Regression: a deadline supplied by the caller (e.g. an API caller wrapping
// EvaluateAll in context.WithTimeout) propagates through attemptCtx and
// makes attemptCtx.Err() == DeadlineExceeded — but the case-level timeout
// knob never actually fired. Annotating that as "case timeout via ..."
// would point users at the wrong YAML knob.
func TestExecuteCase_ParentDeadlineNotMislabeledAsCaseTimeout(t *testing.T) {
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test"},
		EvalCfg: &config.EvalConfig{
			// Generous case budget — should never be the binding deadline.
			Cases: config.CasesConfig{
				Defaults: config.CaseDefaults{TimeoutSeconds: 60},
			},
		},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-parent-deadline",
		Title: "Parent ctx deadline must not be labelled as case timeout",
		Input: config.Input{Prompt: "hello"},
	}

	// Agent blocks until ctx is done — the parent-supplied deadline below
	// (much tighter than the 60s case budget) is what will fire.
	ag := &mockAgent{
		name: "test",
		runFunc: func(ctx context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
			<-ctx.Done()
			return &agent.SessionResult{ExitCode: -1}, ctx.Err()
		},
	}

	parentCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result := e.executeCase(parentCtx, caseCfg, "with_skill", &mockRuntime{workspace: t.TempDir()}, ag)

	if result.Status != judge.StatusError {
		t.Fatalf("expected ERROR, got %s", result.Status)
	}
	if !errors.Is(result.Error, context.DeadlineExceeded) {
		t.Fatalf("expected error chain to contain context.DeadlineExceeded, got %v", result.Error)
	}
	if strings.Contains(result.Error.Error(), "case timeout") {
		t.Fatalf("parent ctx fired (case knob never bound), error must not be annotated with 'case timeout', got %v", result.Error)
	}
}

func TestExecuteCase_TimeoutWithoutJudgeIsError(t *testing.T) {
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test"},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-timeout-no-judge",
		Title: "Timed-out agent without judge stays ERROR",
		Input: config.Input{Prompt: "hello"},
	}

	ag := &mockAgent{
		name: "test",
		runFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
			return &agent.SessionResult{
				FinalMessage: "hello from recovered result",
				ExitCode:     -1,
			}, context.DeadlineExceeded
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: t.TempDir()}, ag)

	if result.Status != judge.StatusError {
		t.Fatalf("expected ERROR when recovered timeout has no judge or expect.exit_code, got %s", result.Status)
	}
	if result.Error == nil || !strings.Contains(result.Error.Error(), "agent execution failed") {
		t.Fatalf("expected agent execution failed error, got %v", result.Error)
	}
}

func TestExecuteCase_AgentCanceledWithPartialResultErrors(t *testing.T) {
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test"},
		EvalCfg: &config.EvalConfig{
			Judge: config.JudgeConfig{
				Type: "rule_based",
				Success: []config.Rule{{
					OutputContains: &config.OutputContainsRule{All: []string{"hello"}},
				}},
			},
		},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-canceled-no-recover",
		Title: "Canceled run should not recover",
		Input: config.Input{Prompt: "hello"},
	}

	ag := &mockAgent{
		name: "test",
		runFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
			return &agent.SessionResult{
				FinalMessage: "hello from canceled result",
				ExitCode:     -1,
			}, context.Canceled
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: t.TempDir()}, ag)

	if result.Status != judge.StatusError {
		t.Fatalf("expected ERROR for canceled agent run, got %s", result.Status)
	}
	if result.Error == nil || !strings.Contains(result.Error.Error(), "agent execution failed") {
		t.Fatalf("expected agent execution failed error, got %v", result.Error)
	}
}

// Strict-budget regression: when the agent run times out, agent_judge MUST
// NOT be invoked against a fresh context to salvage a verdict. The old
// behaviour (evaluator.evaluationContext rewrapping ctx with WithoutCancel)
// is intentionally gone so the case timeout strictly bounds agent + judge.
func TestExecuteCase_AgentTimeoutDoesNotInvokeAgentJudge(t *testing.T) {
	judgeRuns := atomic.Int32{}
	judgeAgent := &mockAgent{
		name: "judge",
		runFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
			judgeRuns.Add(1)
			return &agent.SessionResult{FinalMessage: `{"results":[{"criterion":"recovered","passed":true,"evidence":"ok"}]}`}, nil
		},
	}

	e := newTestEvaluator(EvalOptions{
		Agent: judgeAgent,
		EvalCfg: &config.EvalConfig{
			Engine: config.EngineConfig{Name: "mock"},
			Judge: config.JudgeConfig{
				Type:     "agent_judge",
				Criteria: []string{"recovered"},
			},
		},
	})

	origDetect := agentDetectWithInitParams
	agentDetectWithInitParams = func(_ string, _ credential.AgentInitParams, _ map[string]string) (agent.Agent, error) {
		return judgeAgent, nil
	}
	defer func() { agentDetectWithInitParams = origDetect }()

	caseCfg := &config.CaseConfig{
		ID:    "case-timeout-no-judge-salvage",
		Title: "Timed-out agent does not invoke agent_judge",
		Input: config.Input{Prompt: "hello"},
		Judge: config.JudgeConfig{
			Type:     "agent_judge",
			Criteria: []string{"recovered"},
		},
	}

	runAgent := &mockAgent{
		name: "run",
		runFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
			return &agent.SessionResult{
				FinalMessage: "hello from recovered result",
				ExitCode:     -1,
			}, context.DeadlineExceeded
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: t.TempDir()}, runAgent)

	if result.Status != judge.StatusError {
		t.Fatalf("expected ERROR (no salvage), got %s (err=%v)", result.Status, result.Error)
	}
	if got := judgeRuns.Load(); got != 0 {
		t.Fatalf("agent_judge must not be invoked when the agent run timed out, got %d runs", got)
	}
}

func TestExecuteCase_InstallsJudgeSkillsOnJudgeAgentOnly(t *testing.T) {
	skillDir := t.TempDir()
	judgeAgent := &mockAgent{
		name:   "judge",
		output: `{"results":[{"criterion":"uses rubric","passed":true,"evidence":"rubric applied"}]}`,
	}
	runAgent := &mockAgent{name: "run", output: "main response"}

	origDetect := agentDetectWithInitParams
	agentDetectWithInitParams = func(_ string, _ credential.AgentInitParams, _ map[string]string) (agent.Agent, error) {
		return judgeAgent, nil
	}
	defer func() { agentDetectWithInitParams = origDetect }()

	e := newTestEvaluator(EvalOptions{
		SkillDir: skillDir,
		Agent:    runAgent,
		EvalCfg: &config.EvalConfig{
			Engine: config.EngineConfig{Name: "mock"},
			Judge: config.JudgeConfig{
				Type:     "agent_judge",
				Model:    "judge-model",
				Criteria: []string{"uses rubric"},
				Skills: []config.SkillRef{
					{Source: "local_path", Path: "evals/fixtures/judge-skill", Target: "~/.claude/skills/judge-skill"},
					{Source: "local_path", Path: "evals/fixtures/security-judge"},
				},
			},
		},
	})

	result := e.executeCase(
		context.Background(),
		&config.CaseConfig{ID: "case-judge-skills", Input: config.Input{Prompt: "hello"}},
		"without_skill",
		&mockRuntime{workspace: t.TempDir()},
		runAgent,
	)

	if result.Status != judge.StatusPass {
		t.Fatalf("status = %s, err=%v", result.Status, result.Error)
	}
	if got := runAgent.skillCall.Load(); got != 0 {
		t.Fatalf("run agent InstallSkill calls = %d, want 0", got)
	}
	if got := judgeAgent.skillCall.Load(); got != 2 {
		t.Fatalf("judge agent InstallSkill calls = %d, want 2", got)
	}
	if len(judgeAgent.skills) != 2 {
		t.Fatalf("judge installed skills = %#v", judgeAgent.skills)
	}
	wantFirst := filepath.Join(skillDir, "evals/fixtures/judge-skill")
	if judgeAgent.skills[0].Source != wantFirst {
		t.Fatalf("first judge skill source = %q, want %q", judgeAgent.skills[0].Source, wantFirst)
	}
	if judgeAgent.skills[0].Target != "~/.claude/skills/judge-skill" {
		t.Fatalf("first judge skill target = %q", judgeAgent.skills[0].Target)
	}
	if len(result.JudgeSkills) != 2 || result.JudgeSkills[0].Path != "evals/fixtures/judge-skill" {
		t.Fatalf("result JudgeSkills = %#v", result.JudgeSkills)
	}
	if len(judgeAgent.lastMessages) != 1 || !strings.Contains(judgeAgent.lastMessages[0].Content, "Mandatory Judge Skill Use") {
		t.Fatalf("judge prompt missing mandatory skill use: %#v", judgeAgent.lastMessages)
	}
}

func TestExecuteCase_JudgeSkillInstallFailureReturnsError(t *testing.T) {
	judgeAgent := &mockAgent{
		name:     "judge",
		skillErr: errors.New("install unsupported"),
		output:   `{"results":[{"criterion":"uses rubric","passed":true,"evidence":"rubric applied"}]}`,
	}
	runAgent := &mockAgent{name: "run", output: "main response"}

	origDetect := agentDetectWithInitParams
	agentDetectWithInitParams = func(_ string, _ credential.AgentInitParams, _ map[string]string) (agent.Agent, error) {
		return judgeAgent, nil
	}
	defer func() { agentDetectWithInitParams = origDetect }()

	e := newTestEvaluator(EvalOptions{
		SkillDir: t.TempDir(),
		Agent:    runAgent,
		EvalCfg: &config.EvalConfig{
			Engine: config.EngineConfig{Name: "mock"},
			Judge: config.JudgeConfig{
				Type:     "agent_judge",
				Model:    "judge-model",
				Criteria: []string{"uses rubric"},
				Skills:   []config.SkillRef{{Source: "local_path", Path: "evals/fixtures/judge-skill"}},
			},
		},
	})

	result := e.executeCase(
		context.Background(),
		&config.CaseConfig{ID: "case-judge-skill-error", Input: config.Input{Prompt: "hello"}},
		"with_skill",
		&mockRuntime{workspace: t.TempDir()},
		runAgent,
	)

	if result.Status != judge.StatusError {
		t.Fatalf("status = %s, want ERROR", result.Status)
	}
	if result.Error == nil || !strings.Contains(result.Error.Error(), `judge.skills[0].path="evals/fixtures/judge-skill"`) {
		t.Fatalf("error = %v, want judge skill path context", result.Error)
	}
	if got := judgeAgent.runCall.Load(); got != 0 {
		t.Fatalf("judge Run calls = %d, want 0 after install failure", got)
	}
}

func TestInstallJudgeSkills_AllowsAbsolutePathWithoutSkillDir(t *testing.T) {
	absSkill := filepath.Join(t.TempDir(), "judge-skill")
	judgeAgent := &mockAgent{name: "judge"}
	e := newTestEvaluator(EvalOptions{EvalCfg: &config.EvalConfig{}})

	err := e.installJudgeSkills(context.Background(), &mockRuntime{workspace: t.TempDir()}, config.JudgeConfig{
		Type:     "agent_judge",
		Model:    "judge-model",
		Criteria: []string{"uses rubric"},
		Skills:   []config.SkillRef{{Source: "local_path", Path: absSkill}},
	}, judgeAgent)
	if err != nil {
		t.Fatalf("installJudgeSkills() error = %v, want nil", err)
	}
	if len(judgeAgent.skills) != 1 || judgeAgent.skills[0].Source != absSkill {
		t.Fatalf("judge installed skills = %#v", judgeAgent.skills)
	}
}

func TestRemoveDefaultRunSkillsBeforeJudge_RemovesOnlyDefaultTargets(t *testing.T) {
	var commands []string
	rt := &mockRuntime{
		workspace: t.TempDir(),
		execFunc: func(_ context.Context, command string, _ runtime.ExecOptions) (runtime.ExecResult, error) {
			commands = append(commands, command)
			return runtime.ExecResult{ExitCode: 0}, nil
		},
	}
	runAgent := &mockAgent{name: "run", skillPath: ".codex/skills"}
	e := newTestEvaluator(EvalOptions{
		SkillDir: t.TempDir(),
		EvalCfg: &config.EvalConfig{
			Skills: []config.SkillRef{
				{Source: "local_path", Path: "skill-under-test"},
				{Source: "local_path", Path: "custom-target-skill", Target: ".codex/skills/custom"},
			},
		},
	})

	err := e.removeDefaultRunSkillsBeforeJudge(context.Background(), rt, "with_skill", config.JudgeConfig{Type: "agent_judge"}, runAgent)
	if err != nil {
		t.Fatalf("removeDefaultRunSkillsBeforeJudge() error = %v", err)
	}
	if len(commands) != 1 {
		t.Fatalf("cleanup commands = %#v, want one default-target cleanup", commands)
	}
	if !strings.Contains(commands[0], "rm -rf -- '.codex/skills/skill-under-test'") {
		t.Fatalf("cleanup command = %q", commands[0])
	}
}

func TestExecuteCase_NoJudge_DefaultPass(t *testing.T) {
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test", output: "hello world"},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-2",
		Title: "No Judge Case",
		Input: config.Input{Prompt: "hello"},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: "/tmp/test"}, nil)

	if result.Status != judge.StatusPass {
		t.Errorf("expected PASS status, got %s", result.Status)
	}
	if result.FinalMessage != "hello world" {
		t.Errorf("expected 'hello world', got %s", result.FinalMessage)
	}
}

func TestExecuteCase_StartsAgentRunSpanInSingleTrace(t *testing.T) {
	t.Setenv("SKILL_UP_TRACE_TOPOLOGY", "single")

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	originalProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer func() {
		otel.SetTracerProvider(originalProvider)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test", output: "hello world"},
	})
	caseCfg := &config.CaseConfig{
		ID:    "case-agent-span",
		Title: "Agent Span Case",
		Input: config.Input{Prompt: "hello"},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: t.TempDir()}, nil)
	if result.Status != judge.StatusPass {
		t.Fatalf("expected PASS status, got %s: %v", result.Status, result.Error)
	}

	caseSpan := findEndedSpan(t, recorder.Ended(), "evaluator.case")
	agentSpan := findEndedSpan(t, recorder.Ended(), "agent.run")
	if agentSpan.Parent().SpanID() != caseSpan.SpanContext().SpanID() {
		t.Fatalf("agent.run parent = %s, want evaluator.case %s", agentSpan.Parent().SpanID(), caseSpan.SpanContext().SpanID())
	}
	assertEndedSpanAttr(t, agentSpan.Attributes(), "skill_up.case.id", "case-agent-span")
	assertEndedSpanAttr(t, agentSpan.Attributes(), "skill_up.case.configuration", "with_skill")
	assertEndedSpanAttr(t, agentSpan.Attributes(), "skill_up.engine", "test")
}

func TestExecuteCase_NonZeroExit_NoJudgeNoExpectExitCode_Fails(t *testing.T) {
	// Regression: agent exits non-zero with no expect.exit_code and no judge →
	// must be FAIL, not the default PASS from the j==nil branch.
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{
			name: "test",
			runFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
				return &agent.SessionResult{ExitCode: 1, FinalMessage: "some output"}, nil
			},
		},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-nonzero-nojudge",
		Title: "Non-Zero Exit No Judge",
		Input: config.Input{Prompt: "hello"},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: "/tmp/test"}, nil)

	if result.Status != judge.StatusFail {
		t.Errorf("expected FAIL for non-zero exit with no expect.exit_code or judge, got %s", result.Status)
	}
}

func TestExecuteCase_NonZeroExit_WithExpectExitCode_ProceedsToEvaluation(t *testing.T) {
	// When expect.exit_code is configured, non-zero exit should flow through to
	// expect evaluation rather than short-circuit to FAIL.
	exitCode := 1
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{
			name: "test",
			runFunc: func(_ context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
				return &agent.SessionResult{ExitCode: 1, FinalMessage: "error output"}, nil
			},
		},
	})

	caseCfg := &config.CaseConfig{
		ID:     "case-expect-exit-code",
		Title:  "Expect Exit Code",
		Input:  config.Input{Prompt: "hello"},
		Expect: config.Expect{ExitCode: &exitCode},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: "/tmp/test"}, nil)

	// expect.exit_code=1 matches actual exit 1 → PASS
	if result.Status != judge.StatusPass {
		t.Errorf("expected PASS when expect.exit_code matches actual exit code, got %s", result.Status)
	}
}

func TestExecuteCase_ExpectFail_ShortCircuit(t *testing.T) {
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test", output: "no match here"},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-3",
		Title: "Expect Fail",
		Input: config.Input{Prompt: "hello"},
		Expect: config.Expect{
			MustContain: []string{"required-keyword"},
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: "/tmp/test"}, nil)

	if result.Status != judge.StatusFail {
		t.Errorf("expected FAIL status, got %s", result.Status)
	}
	if result.ExpectResult == nil {
		t.Fatal("expected non-nil ExpectResult")
	}
	if result.ExpectResult.Passed {
		t.Error("expected expect check to fail")
	}
}

func TestExecuteCase_ExpectPass_ThenJudge(t *testing.T) {
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test", output: "required-keyword and humor"},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-4",
		Title: "Full Pipeline",
		Input: config.Input{Prompt: "tell a joke"},
		Expect: config.Expect{
			MustContain: []string{"required-keyword"},
		},
		Judge: config.JudgeConfig{
			Type: "rule_based",
			Success: []config.Rule{
				{OutputContains: &config.OutputContainsRule{All: []string{"humor"}}},
			},
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: "/tmp/test"}, nil)

	if result.Status != judge.StatusPass {
		t.Errorf("expected PASS status, got %s", result.Status)
	}
	if result.Grading == nil {
		t.Fatal("expected non-nil Grading")
	}
	if result.Grading.Summary.PassRate != 1.0 {
		t.Errorf("expected pass_rate 1.0, got %f", result.Grading.Summary.PassRate)
	}
}

func TestExecuteCase_RetryPolicyRetriesErrors(t *testing.T) {
	t.Parallel()

	runCalls := atomic.Int32{}
	ag := &mockAgent{
		name: "test",
		runFunc: func(ctx context.Context, rt runtime.Runtime, opts agent.ExecOptions, messages []transcript.Message) (*agent.SessionResult, error) {
			if runCalls.Add(1) == 1 {
				return &agent.SessionResult{ExitCode: 1}, errors.New("transient failure")
			}
			return &agent.SessionResult{FinalMessage: "retry succeeded"}, nil
		},
	}

	e := newTestEvaluator(EvalOptions{
		Agent: ag,
		EvalCfg: &config.EvalConfig{
			Cases: config.CasesConfig{
				RetryPolicy: config.RetryPolicy{
					MaxRetries: 1,
					RetryOn:    []string{"error"},
				},
			},
		},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-retry-error",
		Title: "Retry on transient error",
		Input: config.Input{Prompt: "hello"},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: t.TempDir()}, nil)

	if got := runCalls.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
	if result.Status != judge.StatusPass {
		t.Fatalf("expected PASS after retry, got %s", result.Status)
	}
	if result.FinalMessage != "retry succeeded" {
		t.Fatalf("expected retry result to be returned, got %q", result.FinalMessage)
	}
}

func TestExecuteCase_RetryPolicyDoesNotRetryUnmatchedReason(t *testing.T) {
	t.Parallel()

	runCalls := atomic.Int32{}
	ag := &mockAgent{
		name: "test",
		runFunc: func(ctx context.Context, rt runtime.Runtime, opts agent.ExecOptions, messages []transcript.Message) (*agent.SessionResult, error) {
			runCalls.Add(1)
			return &agent.SessionResult{ExitCode: 1}, errors.New("transient failure")
		},
	}

	e := newTestEvaluator(EvalOptions{
		Agent: ag,
		EvalCfg: &config.EvalConfig{
			Cases: config.CasesConfig{
				RetryPolicy: config.RetryPolicy{
					MaxRetries: 2,
					RetryOn:    []string{"timeout"},
				},
			},
		},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-retry-mismatch",
		Title: "No retry for unmatched reason",
		Input: config.Input{Prompt: "hello"},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: t.TempDir()}, nil)

	if got := runCalls.Load(); got != 1 {
		t.Fatalf("expected 1 attempt, got %d", got)
	}
	if result.Status != judge.StatusError {
		t.Fatalf("expected ERROR without retry, got %s", result.Status)
	}
}

func TestExecuteCase_RetryPolicyRetriesTimeouts(t *testing.T) {
	t.Parallel()

	runCalls := atomic.Int32{}
	ag := &mockAgent{
		name: "test",
		runFunc: func(ctx context.Context, rt runtime.Runtime, opts agent.ExecOptions, messages []transcript.Message) (*agent.SessionResult, error) {
			if runCalls.Add(1) == 1 {
				<-ctx.Done()
				return &agent.SessionResult{ExitCode: -1}, ctx.Err()
			}
			return &agent.SessionResult{FinalMessage: "timeout recovered"}, nil
		},
	}

	e := newTestEvaluator(EvalOptions{
		Agent: ag,
		EvalCfg: &config.EvalConfig{
			Cases: config.CasesConfig{
				Defaults: config.CaseDefaults{
					TimeoutSeconds: 1,
				},
				RetryPolicy: config.RetryPolicy{
					MaxRetries: 1,
					RetryOn:    []string{"timeout"},
				},
			},
		},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-retry-timeout",
		Title: "Retry on timeout",
		Input: config.Input{Prompt: "hello"},
		Constraints: config.Constraints{
			TimeoutSeconds: 1,
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: t.TempDir()}, nil)

	if got := runCalls.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
	if result.Status != judge.StatusPass {
		t.Fatalf("expected PASS after timeout retry, got %s", result.Status)
	}
}

func TestIsTimeoutError_RejectsNonDeadlineMessages(t *testing.T) {
	t.Parallel()

	if isTimeoutError(errors.New("invalid timeout_seconds value")) {
		t.Fatal("expected non-deadline timeout-like message to be ignored")
	}
}

func TestRetryBackoffDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: 2 * time.Second},
		{attempt: 1, want: 2 * time.Second},
		{attempt: 2, want: 4 * time.Second},
		{attempt: 3, want: 8 * time.Second},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("attempt_%d", tt.attempt), func(t *testing.T) {
			t.Parallel()
			if got := retryBackoffDelay(tt.attempt); got != tt.want {
				t.Fatalf("retryBackoffDelay(%d) = %s, want %s", tt.attempt, got, tt.want)
			}
		})
	}
}

func TestExecuteCase_JudgeErrorDownloadsJudgeArtifacts(t *testing.T) {
	t.Parallel()

	rt := &mockRuntime{workspace: t.TempDir()}
	ag := &mockAgent{name: "test"}
	ag.runFunc = func(_ context.Context, rt runtime.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
		callNum := ag.runCall.Load()
		switch callNum {
		case 1:
			return &agent.SessionResult{
				FinalMessage: "main result",
			}, nil
		case 2:
			if err := os.WriteFile(filepath.Join(rt.Workspace(), "judge-stdout.json"), []byte(`{"error":"rate limit"}`), 0o600); err != nil {
				t.Fatalf("write judge artifact: %v", err)
			}
			session := &agent.SessionResult{
				FinalMessage: "API Error: 400 rate limit",
				Artifacts: &agent.SessionArtifacts{
					GeneratedFiles: []string{"judge-stdout.json"},
				},
			}
			return session, errors.New("API rate limit exceeded")
		default:
			t.Fatalf("unexpected run call %d", callNum)
			return nil, nil
		}
	}

	e := newTestEvaluator(EvalOptions{
		Agent:     ag,
		OutputDir: t.TempDir(),
	})
	caseCfg := &config.CaseConfig{
		ID:    "case-judge-error",
		Title: "Judge Error",
		Input: config.Input{Prompt: "hello"},
		Judge: config.JudgeConfig{
			Type:     "agent_judge",
			Model:    "test-model",
			Criteria: []string{"criterion"},
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", rt, nil)
	if result.Status != judge.StatusError {
		t.Fatalf("expected ERROR status, got %s", result.Status)
	}
	if result.Error == nil || !strings.Contains(result.Error.Error(), "judge evaluation failed") {
		t.Fatalf("expected judge evaluation failure, got %v", result.Error)
	}
	if rt.downloadFileCall.Load() == 0 {
		t.Fatal("expected judge artifacts to be downloaded on judge error")
	}
}

func TestExecuteCase_CaseLevelJudge(t *testing.T) {
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test", output: "hello world"},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-5",
		Title: "Case Override",
		Input: config.Input{Prompt: "hello"},
		Judge: config.JudgeConfig{
			Type: "rule_based",
			Success: []config.Rule{
				{OutputContains: &config.OutputContainsRule{All: []string{"hello"}}},
			},
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: "/tmp/test"}, nil)

	if result.Status != judge.StatusPass {
		t.Errorf("expected PASS status, got %s", result.Status)
	}
}

func TestExecuteCase_LogsJudgeType(t *testing.T) {
	logging.SetVerbosity(1)
	defer logging.SetVerbosity(0)

	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test", output: "required-keyword and humor"},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-log-judge",
		Title: "Judge Logging",
		Input: config.Input{Prompt: "tell a joke"},
		Judge: config.JudgeConfig{
			Type: "rule_based",
			Success: []config.Rule{
				{OutputContains: &config.OutputContainsRule{All: []string{"humor"}}},
			},
		},
	}

	out := captureStdout(t, func() {
		result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: "/tmp/test"}, nil)
		if result.Status != judge.StatusPass {
			t.Fatalf("expected PASS status, got %s", result.Status)
		}
	})

	if !strings.Contains(out, "level=DEBUG") || !strings.Contains(out, "Judge: case case-log-judge") {
		t.Fatalf("expected judge result log, got %q", out)
	}
}

func TestEvaluateAll_ConcurrentExecution(t *testing.T) {
	ag := &mockAgent{name: "test", output: "ok"}
	e := newTestEvaluator(EvalOptions{
		Concurrency: 2,
		Agent:       ag,
		EvalCfg: &config.EvalConfig{
			Environment: config.Environment{Type: "none"},
		},
	})

	cases := []*config.CaseConfig{
		{ID: "c1", Title: "Case 1", Input: config.Input{Prompt: "p1"}},
		{ID: "c2", Title: "Case 2", Input: config.Input{Prompt: "p2"}},
	}

	results, err := e.EvaluateAll(context.Background(), cases)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if ag.runCall.Load() != 2 {
		t.Errorf("expected 2 agent calls, got %d", ag.runCall.Load())
	}
}

func TestEvaluateAll_AllCasesPass(t *testing.T) {
	ag := &mockAgent{name: "test", output: "hello world"}
	e := newTestEvaluator(EvalOptions{
		Concurrency: 2,
		Agent:       ag,
		EvalCfg: &config.EvalConfig{
			Environment: config.Environment{Type: "none"},
		},
	})

	cases := []*config.CaseConfig{
		{ID: "c1", Title: "Case 1", Input: config.Input{Prompt: "hello"}},
		{ID: "c2", Title: "Case 2", Input: config.Input{Prompt: "world"}},
	}

	results, err := e.EvaluateAll(context.Background(), cases)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != judge.StatusPass {
			t.Errorf("expected PASS status, got %s for case %s", r.Status, r.CaseID)
		}
	}
}

func TestEvaluateAll_ChecksRunnerCredentialsOnce(t *testing.T) {
	ag := &mockAgent{name: "test", output: "hello world"}
	e := newTestEvaluator(EvalOptions{
		Concurrency: 2,
		Agent:       ag,
		EvalCfg: &config.EvalConfig{
			Environment: config.Environment{Type: "none"},
		},
	})

	cases := []*config.CaseConfig{
		{ID: "c1", Title: "Case 1", Input: config.Input{Prompt: "hello"}},
		{ID: "c2", Title: "Case 2", Input: config.Input{Prompt: "world"}},
	}

	results, err := e.EvaluateAll(context.Background(), cases)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if got := ag.credCall.Load(); got != 1 {
		t.Fatalf("expected CheckCredentials to be called once, got %d", got)
	}
}

func TestExecuteCase_AgentJudgeReceivesTranscriptAndWorkspaceDiff(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	var judgePrompt string
	ag := &mockAgent{name: "test"}
	ag.runFunc = func(_ context.Context, rt runtime.Runtime, _ agent.ExecOptions, messages []transcript.Message) (*agent.SessionResult, error) {
		callNum := ag.runCall.Load()
		switch callNum {
		case 1:
			if err := os.WriteFile(filepath.Join(rt.Workspace(), "notes.txt"), []byte("after\n"), 0o600); err != nil {
				t.Fatalf("mutate workspace: %v", err)
			}
			if err := os.WriteFile(filepath.Join(rt.Workspace(), "stdout.json"), []byte("{\"artifact\":true}\n"), 0o600); err != nil {
				t.Fatalf("write generated artifact: %v", err)
			}
			return &agent.SessionResult{
				FinalMessage: "main result",
				Transcript: transcript.Transcript{
					{Role: transcript.RoleUser, Content: "inspect repo", Turn: 1},
					{Role: transcript.RoleAssistant, Content: "updated notes.txt", Turn: 1},
				},
				Artifacts: &agent.SessionArtifacts{
					GeneratedFiles: []string{"stdout.json"},
				},
			}, nil
		case 2:
			if len(messages) != 1 {
				t.Fatalf("judge run should receive a single prompt message, got %d", len(messages))
			}
			judgePrompt = messages[0].Content
			return &agent.SessionResult{
				FinalMessage: `{"results":[{"criterion":"diff included","passed":true,"evidence":"ok"}]}`,
			}, nil
		default:
			t.Fatalf("unexpected run call %d", callNum)
			return nil, nil
		}
	}

	e := newTestEvaluator(EvalOptions{
		Agent: ag,
	})
	caseCfg := &config.CaseConfig{
		ID:    "case-diff",
		Title: "Agent judge gets diff",
		Input: config.Input{Prompt: "inspect repo"},
		Context: config.Context{
			Git: &config.GitContext{Init: true},
		},
		Judge: config.JudgeConfig{
			Type:     "agent_judge",
			Criteria: []string{"diff included"},
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: workspace}, nil)

	if result.Status != judge.StatusPass {
		t.Fatalf("expected PASS status, got %s", result.Status)
	}
	if !strings.Contains(judgePrompt, "updated notes.txt") {
		t.Fatalf("judge prompt missing final message inline material: %s", judgePrompt)
	}
	assertJudgePromptReferencesMaterial(t, judgePrompt, "workspace_diff", "workspace.diff")
	assertJudgePromptReferencesMaterial(t, judgePrompt, "transcript", "transcript.json")
	if strings.Contains(judgePrompt, "stdout.json") || strings.Contains(judgePrompt, `"artifact":true`) {
		t.Fatalf("judge prompt should filter generated artifact diff: %s", judgePrompt)
	}
}

func TestSetupCaseEnvironmentAgentInstall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		runtimeType  string
		wantInstalls int32
	}{
		{"opensandbox_installs", "opensandbox", 1},
		{"none_skips", "none", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ag := &mockAgent{name: "codex"}
			rt := &mockRuntime{workspace: "/workspace"}
			eval, ok := NewEvaluator(EvalOptions{
				Agent:   ag,
				EvalCfg: &config.EvalConfig{Environment: config.Environment{Type: tt.runtimeType}},
			}).(*defaultEvaluator)
			if !ok {
				t.Fatal("NewEvaluator returned non-default evaluator")
			}

			if err := eval.setupCaseEnvironment(context.Background(), rt, &config.CaseConfig{ID: "case"}, "with_skill", ag, runtime.MCPConfig{}); err != nil {
				t.Fatalf("setupCaseEnvironment returned error: %v", err)
			}
			if got := ag.installCall.Load(); got != tt.wantInstalls {
				t.Fatalf("Install calls = %d, want %d", got, tt.wantInstalls)
			}
		})
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	logCaptureMu.Lock()
	defer logCaptureMu.Unlock()

	var buf bytes.Buffer
	restoreOutput := logging.SetOutputForTest(&buf)

	fn()

	restoreOutput()
	return buf.String()
}

func assertJudgePromptReferencesMaterial(t *testing.T, prompt, key, pathSuffix string) {
	t.Helper()
	if !strings.Contains(prompt, key) || !strings.Contains(prompt, pathSuffix) {
		t.Fatalf("judge prompt missing material reference %s/%s: %s", key, pathSuffix, prompt)
	}
}

func TestExecuteCase_AgentJudgeWithoutGitContextSkipsWorkspaceDiff(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	judgePrompt, ag := newWorkspaceDiffJudgeAgent(t, `{"results":[{"criterion":"diff omitted","passed":true,"evidence":"ok"}]}`)
	rt := &mockRuntime{workspace: workspace}

	e := newTestEvaluator(EvalOptions{Agent: ag})
	caseCfg := &config.CaseConfig{
		ID:    "case-no-git-diff",
		Title: "Agent judge without git context skips diff",
		Input: config.Input{Prompt: "inspect repo"},
		Judge: config.JudgeConfig{
			Type:     "agent_judge",
			Criteria: []string{"diff omitted"},
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", rt, nil)

	if result.Status != judge.StatusPass {
		t.Fatalf("expected PASS status, got %s", result.Status)
	}
	if strings.Contains(*judgePrompt, "diff --git") {
		t.Fatalf("judge prompt should not include workspace diff without git repo/init: %s", *judgePrompt)
	}
	if ag.runCall.Load() != 2 {
		t.Fatalf("expected main agent and judge runs, got %d", ag.runCall.Load())
	}
	if rt.execCall.Load() != 1 {
		t.Fatalf("expected a single git-context probe exec, got %d", rt.execCall.Load())
	}
}

func TestPrepareWorkspaceDiffState_GitInitFailureIsHandledInScript(t *testing.T) {
	rt := &mockRuntime{
		workspace: "/tmp/test",
		execFunc: func(_ context.Context, command string, _ runtime.ExecOptions) (runtime.ExecResult, error) {
			if strings.Contains(command, "git init -q") {
				return runtime.ExecResult{Stdout: "", Stderr: "", ExitCode: 0}, nil
			}
			t.Fatalf("unexpected command: %s", command)
			return runtime.ExecResult{}, nil
		},
	}

	state, err := prepareWorkspaceDiffState(context.Background(), rt, &config.GitContext{Init: true})
	if err != nil {
		t.Fatalf("expected git init failure to be handled in script, got error: %v", err)
	}
	if state.enabled {
		t.Fatalf("expected workspace diff to stay disabled when git init path yields no baseline")
	}
}

func TestExecuteCase_AgentJudgeWithExistingGitRepoReceivesWorkspaceDiff(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	initGitRepo(t, workspace)

	judgePrompt, ag := newWorkspaceDiffJudgeAgent(t, `{"results":[{"criterion":"diff included","passed":true,"evidence":"ok"}]}`)

	e := newTestEvaluator(EvalOptions{Agent: ag})
	caseCfg := &config.CaseConfig{
		ID:    "case-existing-git-diff",
		Title: "Agent judge with git repo gets diff",
		Input: config.Input{Prompt: "inspect repo"},
		Judge: config.JudgeConfig{
			Type:     "agent_judge",
			Criteria: []string{"diff included"},
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: workspace}, nil)

	if result.Status != judge.StatusPass {
		t.Fatalf("expected PASS status, got %s", result.Status)
	}
	assertJudgePromptReferencesMaterial(t, *judgePrompt, "workspace_diff", "workspace.diff")
}

func TestExecuteCase_AgentJudgeWithClonedGitRepoReceivesWorkspaceDiff(t *testing.T) {
	originDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(originDir, "notes.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	initGitRepo(t, originDir)

	workspace := filepath.Join(t.TempDir(), "clone")
	runCmd(t, "", "git", "clone", originDir, workspace)

	judgePrompt, ag := newWorkspaceDiffJudgeAgent(t, `{"results":[{"criterion":"diff included","passed":true,"evidence":"ok"}]}`)

	e := newTestEvaluator(EvalOptions{Agent: ag})
	caseCfg := &config.CaseConfig{
		ID:    "case-cloned-git-diff",
		Title: "Agent judge with cloned git repo gets diff",
		Input: config.Input{Prompt: "inspect repo"},
		Judge: config.JudgeConfig{
			Type:     "agent_judge",
			Criteria: []string{"diff included"},
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: workspace}, nil)

	if result.Status != judge.StatusPass {
		t.Fatalf("expected PASS status, got %s", result.Status)
	}
	assertJudgePromptReferencesMaterial(t, *judgePrompt, "workspace_diff", "workspace.diff")
}

func TestExecuteCase_AgentJudgeWithClonedGitRepoWithoutGlobalConfigReceivesWorkspaceDiff(t *testing.T) {
	originDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(originDir, "notes.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	initGitRepo(t, originDir)

	workspace := filepath.Join(t.TempDir(), "clone")
	runCmd(t, "", "git", "clone", originDir, workspace)

	judgePrompt, ag := newWorkspaceDiffJudgeAgent(t, `{"results":[{"criterion":"diff included","passed":true,"evidence":"ok"}]}`)
	homeDir := t.TempDir()

	e := newTestEvaluator(EvalOptions{Agent: ag})
	caseCfg := &config.CaseConfig{
		ID:    "case-cloned-git-diff-no-global-config",
		Title: "Agent judge with cloned git repo gets diff without global git config",
		Input: config.Input{Prompt: "inspect repo"},
		Judge: config.JudgeConfig{
			Type:     "agent_judge",
			Criteria: []string{"diff included"},
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{
		workspace: workspace,
		execEnv: map[string]string{
			"HOME":                homeDir,
			"GIT_CONFIG_NOSYSTEM": "1",
		},
	}, nil)

	if result.Status != judge.StatusPass {
		t.Fatalf("expected PASS status, got %s", result.Status)
	}
	assertJudgePromptReferencesMaterial(t, *judgePrompt, "workspace_diff", "workspace.diff")
}

func TestExecuteCase_AgentJudgeTracksWorkspaceDiffAfterAgentCommit(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	initGitRepo(t, workspace)

	var judgePrompt string
	ag := &mockAgent{name: "test"}
	ag.runFunc = func(ctx context.Context, rt runtime.Runtime, _ agent.ExecOptions, messages []transcript.Message) (*agent.SessionResult, error) {
		callNum := ag.runCall.Load()
		switch callNum {
		case 1:
			runCmdContext(ctx, t, rt.Workspace(), "git", "config", "user.name", "agent")
			runCmdContext(ctx, t, rt.Workspace(), "git", "config", "user.email", "agent@example.invalid")
			if err := os.WriteFile(filepath.Join(rt.Workspace(), "notes.txt"), []byte("after\n"), 0o600); err != nil {
				t.Fatalf("mutate workspace: %v", err)
			}
			runCmdContext(ctx, t, rt.Workspace(), "git", "add", "--all")
			runCmdContext(ctx, t, rt.Workspace(), "git", "commit", "-qm", "agent commit")
			return &agent.SessionResult{
				FinalMessage: "main result",
				Transcript: transcript.Transcript{
					{Role: transcript.RoleAssistant, Content: "updated notes.txt", Turn: 1},
				},
			}, nil
		case 2:
			judgePrompt = messages[0].Content
			return &agent.SessionResult{
				FinalMessage: `{"results":[{"criterion":"diff included","passed":true,"evidence":"ok"}]}`,
			}, nil
		default:
			t.Fatalf("unexpected run call %d", callNum)
			return nil, nil
		}
	}

	e := newTestEvaluator(EvalOptions{Agent: ag})
	caseCfg := &config.CaseConfig{
		ID:    "case-agent-commit-diff",
		Title: "Agent judge keeps baseline diff after agent commit",
		Input: config.Input{Prompt: "inspect repo"},
		Judge: config.JudgeConfig{
			Type:     "agent_judge",
			Criteria: []string{"diff included"},
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: workspace}, nil)

	if result.Status != judge.StatusPass {
		t.Fatalf("expected PASS status, got %s", result.Status)
	}
	assertJudgePromptReferencesMaterial(t, judgePrompt, "workspace_diff", "workspace.diff")
}

func TestExecuteCase_AgentJudgeWithGitWorktreeReceivesWorkspaceDiff(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "notes.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	initGitRepo(t, repoDir)

	worktreeDir := filepath.Join(t.TempDir(), "wt")
	runCmd(t, repoDir, "git", "worktree", "add", "--detach", worktreeDir, "HEAD")
	if info, err := os.Stat(filepath.Join(worktreeDir, ".git")); err != nil || info.IsDir() {
		t.Fatalf("expected worktree .git to be a file, got err=%v isDir=%v", err, err == nil && info.IsDir())
	}

	judgePrompt, ag := newWorkspaceDiffJudgeAgent(t, `{"results":[{"criterion":"diff included","passed":true,"evidence":"ok"}]}`)

	e := newTestEvaluator(EvalOptions{Agent: ag})
	caseCfg := &config.CaseConfig{
		ID:    "case-worktree-git-diff",
		Title: "Agent judge with git worktree gets diff",
		Input: config.Input{Prompt: "inspect repo"},
		Judge: config.JudgeConfig{
			Type:     "agent_judge",
			Criteria: []string{"diff included"},
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: worktreeDir}, nil)

	if result.Status != judge.StatusPass {
		t.Fatalf("expected PASS status, got %s", result.Status)
	}
	assertJudgePromptReferencesMaterial(t, *judgePrompt, "workspace_diff", "workspace.diff")
}

func TestExecuteCase_NonAgentJudgeSkipsWorkspaceSnapshot(t *testing.T) {
	workspace := t.TempDir()
	rt := &mockRuntime{workspace: workspace}
	e := newTestEvaluator(EvalOptions{
		Agent: &mockAgent{name: "test", output: "hello"},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-rule",
		Title: "Rule-based judge skips workspace snapshot",
		Input: config.Input{Prompt: "hello"},
		Judge: config.JudgeConfig{
			Type: "rule_based",
			Success: []config.Rule{
				{OutputContains: &config.OutputContainsRule{All: []string{"hello"}}},
			},
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", rt, nil)

	if result.Status != judge.StatusPass {
		t.Fatalf("expected PASS status, got %s", result.Status)
	}
	if rt.downloadDirCall.Load() != 0 {
		t.Fatalf("expected no workspace snapshot downloads, got %d", rt.downloadDirCall.Load())
	}
}

func TestExecuteCase_ScriptJudgePathResolvesFromSkillDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "sample-skill")
	evalDir := filepath.Join(skillDir, "evals")
	if err := os.MkdirAll(filepath.Join(skillDir, "evals", "fixtures", "scripts"), 0o755); err != nil {
		t.Fatalf("failed to create fixture dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# sample\n"), 0o600); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}
	scriptPath := filepath.Join(skillDir, "evals", "fixtures", "scripts", "check.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o600); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}
	if err := os.Chmod(scriptPath, 0o700); err != nil { //nolint:gosec // test script must be executable
		t.Fatalf("failed to chmod script: %v", err)
	}
	evalPath := filepath.Join(evalDir, "eval.yaml")
	if err := os.WriteFile(evalPath, []byte("schema_version: v1alpha1\n"), 0o600); err != nil {
		t.Fatalf("failed to write eval.yaml: %v", err)
	}

	loader := config.NewLoader(evalPath)
	e := newTestEvaluator(EvalOptions{
		Loader: loader,
		Agent:  &mockAgent{name: "test", output: "ok"},
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-1",
		Title: "Script judge path",
		Input: config.Input{Prompt: "hello"},
		Judge: config.JudgeConfig{
			Type:       "script",
			ScriptPath: "evals/fixtures/scripts/check.sh",
		},
	}

	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", Delete: true})
	if err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatalf("failed to create runtime: %v", err)
	}
	defer func() { _ = rt.Close() }()

	result := e.executeCase(context.Background(), caseCfg, "with_skill", rt, nil)
	if result.Status != judge.StatusPass {
		t.Fatalf("expected PASS status, got %s (err=%v)", result.Status, result.Error)
	}
}

func TestEvaluateAll_WithBaseline(t *testing.T) {
	ag := &mockAgent{name: "test", output: "ok"}
	e := newTestEvaluator(EvalOptions{
		Concurrency:  2,
		WithBaseline: true,
		Agent:        ag,
		EvalCfg: &config.EvalConfig{
			Environment: config.Environment{Type: "none"},
		},
	})

	cases := []*config.CaseConfig{
		{ID: "c1", Title: "Case 1", Input: config.Input{Prompt: "p1"}},
	}

	results, err := e.EvaluateAll(context.Background(), cases)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (with_skill + without_skill), got %d", len(results))
	}

	configs := make(map[string]bool)
	for _, r := range results {
		configs[r.Configuration] = true
	}
	if !configs["with_skill"] {
		t.Error("expected with_skill result")
	}
	if !configs["without_skill"] {
		t.Error("expected without_skill result")
	}
}

func TestResolveExpectConfig(t *testing.T) {
	tests := []struct {
		name     string
		expect   config.Expect
		expected bool
	}{
		{
			name:     "empty expect",
			expect:   config.Expect{},
			expected: false,
		},
		{
			name:     "must_contain set",
			expect:   config.Expect{MustContain: []string{"foo"}},
			expected: true,
		},
		{
			name:     "must_not_contain set",
			expect:   config.Expect{MustNotContain: []string{"bar"}},
			expected: true,
		},
		{
			name:     "exit_code set",
			expect:   config.Expect{ExitCode: func(i int) *int { return &i }(0)},
			expected: true,
		},
		{
			name:     "files_exist set",
			expect:   config.Expect{FilesExist: []string{"foo.txt"}},
			expected: true,
		},
		{
			name:     "files_not_exist set",
			expect:   config.Expect{FilesNotExist: []string{"bar.txt"}},
			expected: true,
		},
		{
			name:     "golden_file set",
			expect:   config.Expect{GoldenFile: "expected.txt"},
			expected: true,
		},
		{
			name:     "file_contains set",
			expect:   config.Expect{FileContains: []config.FileContainsCheck{{Path: "foo.txt", Content: "bar"}}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveExpectConfig(&tt.expect)
			if tt.expected && result == nil {
				t.Error("expected non-nil result")
			}
			if !tt.expected && result != nil {
				t.Error("expected nil result")
			}
		})
	}
}

func TestEvalResult_Fields(t *testing.T) {
	r := EvalResult{
		CaseID:        "test-case",
		CaseName:      "Test Case",
		Configuration: "with_skill",
		Status:        judge.StatusPass,
		Prompt:        "test prompt",
		SessionResult: &agent.SessionResult{FinalMessage: "test response", ExitCode: 0, DurationMs: 100, Turns: 1},
		TurnsTotal:    1,
		Grading: &judge.Result{
			Status: judge.StatusPass,
			Summary: judge.ResultSummary{
				Passed:   1,
				Failed:   0,
				Total:    1,
				PassRate: 1.0,
			},
		},
	}

	if r.CaseID != "test-case" {
		t.Errorf("expected CaseID 'test-case', got %s", r.CaseID)
	}
	if r.Status != judge.StatusPass {
		t.Errorf("expected Status PASS, got %s", r.Status)
	}
	if r.Configuration != "with_skill" {
		t.Errorf("expected Configuration 'with_skill', got %s", r.Configuration)
	}
	if r.Grading.Summary.PassRate != 1.0 {
		t.Errorf("expected PassRate 1.0, got %f", r.Grading.Summary.PassRate)
	}
}

func TestNormalizeSessionResult_ReturnsCopy(t *testing.T) {
	t.Parallel()

	original := &agent.SessionResult{
		FinalMessage: "agent summary",
		Transcript: transcript.Transcript{
			{Role: transcript.RoleAssistant, Content: "final assistant reply", Turn: 1},
		},
	}

	normalized := normalizeSessionResult(original)
	if normalized == original {
		t.Fatal("expected normalizeSessionResult to return a copy")
	}
	if normalized.FinalMessage != "final assistant reply" {
		t.Fatalf("expected normalized final message to be overwritten, got %q", normalized.FinalMessage)
	}
	if original.FinalMessage != "agent summary" {
		t.Fatalf("expected original final message to remain unchanged, got %q", original.FinalMessage)
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	requireGit(t)

	commands := [][]string{
		{"git", "init", "-q"},
		{"git", "config", "user.name", "skill-up-test"},
		{"git", "config", "user.email", "skill-up-test@example.invalid"},
		{"git", "add", "--all"},
		{"git", "commit", "--allow-empty", "-qm", "baseline"},
	}
	for _, args := range commands {
		runCmd(t, dir, args[0], args[1:]...)
	}
}

func newWorkspaceDiffJudgeAgent(t *testing.T, judgeResponse string) (*string, *mockAgent) {
	t.Helper()

	judgePrompt := ""
	ag := &mockAgent{name: "test"}
	ag.runFunc = func(_ context.Context, rt runtime.Runtime, _ agent.ExecOptions, messages []transcript.Message) (*agent.SessionResult, error) {
		callNum := ag.runCall.Load()
		switch callNum {
		case 1:
			if err := os.WriteFile(filepath.Join(rt.Workspace(), "notes.txt"), []byte("after\n"), 0o600); err != nil {
				t.Fatalf("mutate workspace: %v", err)
			}
			return &agent.SessionResult{
				FinalMessage: "main result",
				Transcript: transcript.Transcript{
					{Role: transcript.RoleUser, Content: "inspect repo", Turn: 1},
					{Role: transcript.RoleAssistant, Content: "updated notes.txt", Turn: 1},
				},
			}, nil
		case 2:
			judgePrompt = messages[0].Content
			return &agent.SessionResult{FinalMessage: judgeResponse}, nil
		default:
			t.Fatalf("unexpected run call %d", callNum)
			return nil, nil
		}
	}
	return &judgePrompt, ag
}

func runCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	runCmdContext(context.Background(), t, dir, name, args...)
}

func runCmdContext(ctx context.Context, t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v: %s", name, strings.Join(args, " "), err, output)
	}
}

func TestExecuteCase_InputPromptOnly(t *testing.T) {
	// When Input.Prompt is set without Turns, should create a single message
	ag := &mockAgent{name: "test", output: "response"}
	e := newTestEvaluator(EvalOptions{
		Agent: ag,
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-prompt",
		Title: "Prompt Only",
		Input: config.Input{Prompt: "hello world"},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: "/tmp/test"}, nil)

	if result.Status != judge.StatusPass {
		t.Errorf("expected PASS status, got %s", result.Status)
	}
	if len(ag.lastMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(ag.lastMessages))
	}
	if ag.lastMessages[0].Role != transcript.RoleUser {
		t.Errorf("expected RoleUser, got %s", ag.lastMessages[0].Role)
	}
	if ag.lastMessages[0].Content != "hello world" {
		t.Errorf("expected content 'hello world', got %s", ag.lastMessages[0].Content)
	}
	if ag.lastMessages[0].Turn != 1 {
		t.Errorf("expected Turn 1, got %d", ag.lastMessages[0].Turn)
	}
}

func TestExecuteCase_InputTurnsSingle(t *testing.T) {
	// When Input.Turns has one entry, should create one message
	ag := &mockAgent{name: "test", output: "response"}
	e := newTestEvaluator(EvalOptions{
		Agent: ag,
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-single-turn",
		Title: "Single Turn",
		Input: config.Input{
			Turns: []config.Turn{
				{Role: "user", Content: "first message"},
			},
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: "/tmp/test"}, nil)

	if result.Status != judge.StatusPass {
		t.Errorf("expected PASS status, got %s", result.Status)
	}
	if len(ag.lastMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(ag.lastMessages))
	}
	if ag.lastMessages[0].Content != "first message" {
		t.Errorf("expected content 'first message', got %s", ag.lastMessages[0].Content)
	}
	if ag.lastMessages[0].Turn != 1 {
		t.Errorf("expected Turn 1, got %d", ag.lastMessages[0].Turn)
	}
}

func TestExecuteCase_InputTurnsMultiple(t *testing.T) {
	// When Input.Turns has multiple entries, should create multiple messages with correct Turn numbers
	ag := &mockAgent{name: "test", output: "response"}
	e := newTestEvaluator(EvalOptions{
		Agent: ag,
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-multi-turn",
		Title: "Multiple Turns",
		Input: config.Input{
			Turns: []config.Turn{
				{Role: "user", Content: "first user message"},
				{Role: "assistant", Content: "assistant reply"},
				{Role: "user", Content: "second user message"},
			},
		},
	}

	result := e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: "/tmp/test"}, nil)

	if result.Status != judge.StatusPass {
		t.Errorf("expected PASS status, got %s", result.Status)
	}
	if len(ag.lastMessages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(ag.lastMessages))
	}

	// Verify first message (Turn 1)
	if ag.lastMessages[0].Role != transcript.RoleUser {
		t.Errorf("expected message 0 RoleUser, got %s", ag.lastMessages[0].Role)
	}
	if ag.lastMessages[0].Content != "first user message" {
		t.Errorf("expected message 0 content 'first user message', got %s", ag.lastMessages[0].Content)
	}
	if ag.lastMessages[0].Turn != 1 {
		t.Errorf("expected message 0 Turn 1, got %d", ag.lastMessages[0].Turn)
	}

	// Verify second message (Turn 2)
	if ag.lastMessages[1].Role != transcript.RoleAssistant {
		t.Errorf("expected message 1 RoleAssistant, got %s", ag.lastMessages[1].Role)
	}
	if ag.lastMessages[1].Content != "assistant reply" {
		t.Errorf("expected message 1 content 'assistant reply', got %s", ag.lastMessages[1].Content)
	}
	if ag.lastMessages[1].Turn != 2 {
		t.Errorf("expected message 1 Turn 2, got %d", ag.lastMessages[1].Turn)
	}

	// Verify third message (Turn 3)
	if ag.lastMessages[2].Role != transcript.RoleUser {
		t.Errorf("expected message 2 RoleUser, got %s", ag.lastMessages[2].Role)
	}
	if ag.lastMessages[2].Content != "second user message" {
		t.Errorf("expected message 2 content 'second user message', got %s", ag.lastMessages[2].Content)
	}
	if ag.lastMessages[2].Turn != 3 {
		t.Errorf("expected message 2 Turn 3, got %d", ag.lastMessages[2].Turn)
	}

	// Verify TurnsTotal is set correctly
	if result.TurnsTotal != 3 {
		t.Errorf("expected TurnsTotal 3, got %d", result.TurnsTotal)
	}
}

func TestExecuteCase_InputTurnsDefaultRole(t *testing.T) {
	// When Turn.Role is empty, it should default to "user"
	ag := &mockAgent{name: "test", output: "response"}
	e := newTestEvaluator(EvalOptions{
		Agent: ag,
	})

	caseCfg := &config.CaseConfig{
		ID:    "case-default-role",
		Title: "Default Role",
		Input: config.Input{
			Turns: []config.Turn{
				{Content: "message without role"},
			},
		},
	}

	_ = e.executeCase(context.Background(), caseCfg, "with_skill", &mockRuntime{workspace: "/tmp/test"}, nil)

	if len(ag.lastMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(ag.lastMessages))
	}
	if ag.lastMessages[0].Role != transcript.RoleUser {
		t.Errorf("expected default RoleUser, got %s", ag.lastMessages[0].Role)
	}
}

func TestGitInitUploader_NoGitContext(t *testing.T) {
	rt := &mockRuntime{workspace: t.TempDir()}
	uploader := &gitInitUploader{}

	caseCfg := &config.CaseConfig{
		ID:      "no-git",
		Title:   "No git context",
		Input:   config.Input{Prompt: "test"},
		Context: config.Context{},
	}

	if err := uploader.Upload(context.Background(), rt, caseCfg, "", ""); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if rt.execCall.Load() != 0 {
		t.Fatalf("expected no exec calls, got %d", rt.execCall.Load())
	}
}

func TestFixtureRegistry_UploadsContextFiles(t *testing.T) {
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", Delete: true})
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Errorf("Close failed: %v", err)
		}
	}()

	caseCfg := &config.CaseConfig{
		ID:    "context-files",
		Title: "Context files",
		Input: config.Input{Prompt: "test"},
		Context: config.Context{
			Files: map[string]string{
				"README.md":      "hello\n",
				"src/handler.go": "package handler\n",
			},
		},
	}

	if err := newFixtureRegistry().UploadAll(context.Background(), rt, caseCfg, "", ""); err != nil {
		t.Fatalf("UploadAll failed: %v", err)
	}

	readme, err := os.ReadFile(filepath.Join(rt.Workspace(), "README.md"))
	if err != nil {
		t.Fatalf("README.md was not uploaded: %v", err)
	}
	if string(readme) != "hello\n" {
		t.Fatalf("unexpected README.md content: %q", string(readme))
	}

	handler, err := os.ReadFile(filepath.Join(rt.Workspace(), "src", "handler.go"))
	if err != nil {
		t.Fatalf("src/handler.go was not uploaded: %v", err)
	}
	if string(handler) != "package handler\n" {
		t.Fatalf("unexpected handler.go content: %q", string(handler))
	}
}

func TestFixtureRegistry_ContextFilesOverrideRepoFixture(t *testing.T) {
	skillDir := t.TempDir()
	workspaceFixture := filepath.Join(skillDir, "evals", "fixtures", "workspace")
	if err := os.MkdirAll(workspaceFixture, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceFixture, "README.md"), []byte("from fixture\n"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	rt, err := runtime.NewRuntime(runtime.Config{Type: "none", Delete: true})
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Errorf("Close failed: %v", err)
		}
	}()

	caseCfg := &config.CaseConfig{
		ID:    "context-files-overrides-repo-fixture",
		Title: "Context files override repo fixture",
		Input: config.Input{Prompt: "test"},
		Context: config.Context{
			RepoFixture: "evals/fixtures/workspace",
			Files: map[string]string{
				"README.md": "from context\n",
			},
		},
	}

	if err := newFixtureRegistry().UploadAll(context.Background(), rt, caseCfg, skillDir, skillDir); err != nil {
		t.Fatalf("UploadAll failed: %v", err)
	}

	readme, err := os.ReadFile(filepath.Join(rt.Workspace(), "README.md"))
	if err != nil {
		t.Fatalf("README.md was not uploaded: %v", err)
	}
	if string(readme) != "from context\n" {
		t.Fatalf("expected context file to override repo fixture, got %q", string(readme))
	}
}

func TestContextFilesUploader_RejectsUnsafePaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "empty", path: ""},
		{name: "workspace root", path: "."},
		{name: "absolute", path: absoluteSecretPath()},
		{name: "parent traversal", path: "../secret.txt"},
		{name: "nested parent traversal", path: "fixtures/../../secret.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &mockRuntime{workspace: t.TempDir()}
			uploader := &contextFilesUploader{}
			caseCfg := &config.CaseConfig{
				ID:    "unsafe-context-file",
				Title: "Unsafe context file",
				Input: config.Input{Prompt: "test"},
				Context: config.Context{
					Files: map[string]string{tt.path: "content"},
				},
			}

			err := uploader.Upload(context.Background(), rt, caseCfg, "", "")
			if err == nil {
				t.Fatal("expected unsafe context file path to fail")
			}
		})
	}
}

func TestGitInitUploader_InitFalse(t *testing.T) {
	rt := &mockRuntime{workspace: t.TempDir()}
	uploader := &gitInitUploader{}

	caseCfg := &config.CaseConfig{
		ID:    "git-init-false",
		Title: "Git init false",
		Input: config.Input{Prompt: "test"},
		Context: config.Context{
			Git: &config.GitContext{Init: false},
		},
	}

	if err := uploader.Upload(context.Background(), rt, caseCfg, "", ""); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if rt.execCall.Load() != 0 {
		t.Fatalf("expected no exec calls, got %d", rt.execCall.Load())
	}
}

func TestGitInitUploader_RejectsShellInjectionInRemote(t *testing.T) {
	tests := []struct {
		name   string
		remote config.GitRemote
	}{
		{
			name:   "command injection via name",
			remote: config.GitRemote{Name: "origin; rm -rf /", URL: "https://example.com/x.git"},
		},
		{
			name:   "newline in url",
			remote: config.GitRemote{Name: "origin", URL: "https://example.com\nrm -rf /"},
		},
		{
			name:   "empty name",
			remote: config.GitRemote{Name: "", URL: "https://example.com/x.git"},
		},
		{
			name:   "dash-prefixed name",
			remote: config.GitRemote{Name: "--upload-pack=evil", URL: "https://example.com/x.git"},
		},
		{
			name:   "dash-prefixed url",
			remote: config.GitRemote{Name: "origin", URL: "--upload-pack=evil"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &mockRuntime{workspace: t.TempDir()}
			uploader := &gitInitUploader{}
			caseCfg := &config.CaseConfig{
				ID:    "git-injection",
				Title: "Git remote injection attempt",
				Input: config.Input{Prompt: "test"},
				Context: config.Context{
					Git: &config.GitContext{
						Init:    true,
						Remotes: []config.GitRemote{tt.remote},
					},
				},
			}

			err := uploader.Upload(context.Background(), rt, caseCfg, "", "")
			if err == nil {
				t.Fatalf("expected unsafe git remote %+v to be rejected", tt.remote)
			}
			if rt.execCall.Load() != 0 {
				t.Fatalf("expected no exec to run for unsafe remote, got %d", rt.execCall.Load())
			}
		})
	}
}

func TestGitInitUploader_QuotesSpecialCharsInRemote(t *testing.T) {
	workspace := t.TempDir()
	rt := &mockRuntime{workspace: workspace}
	uploader := &gitInitUploader{}

	// URL with single quote and $ — must be shell-quoted, not interpreted.
	trickyURL := "https://example.com/it's$HOME.git"
	caseCfg := &config.CaseConfig{
		ID:    "git-special-chars",
		Title: "Git remote with shell-meaningful chars",
		Input: config.Input{Prompt: "test"},
		Context: config.Context{
			Git: &config.GitContext{
				Init: true,
				Remotes: []config.GitRemote{
					{Name: "origin", URL: trickyURL},
				},
			},
		},
	}

	if err := uploader.Upload(context.Background(), rt, caseCfg, "", ""); err != nil {
		t.Fatalf("expected safe upload with quoted special chars, got %v", err)
	}

	result, err := rt.Exec(context.Background(), "git remote get-url origin", runtime.ExecOptions{Cwd: workspace})
	if err != nil {
		t.Fatalf("git remote get-url failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("git remote get-url exited with code %d: %s", result.ExitCode, result.Stderr)
	}
	if got := strings.TrimSpace(result.Stdout); got != trickyURL {
		t.Fatalf("remote URL = %q, want %q (expected no shell expansion)", got, trickyURL)
	}
}

func TestApplyDiffUploader_QuotesTempPath(t *testing.T) {
	// apply_diff derives tmpPath from filepath.Base(diffSource); if the YAML
	// author picks a filename with shell metacharacters, the exec must still
	// pass it as a single argument rather than let the shell interpret it.
	skillDir := t.TempDir()
	// Use a base name that contains a space and `;` to stress-test quoting.
	trickyName := "my patch;echo pwned.diff"
	diffPath := filepath.Join(skillDir, trickyName)
	if err := os.WriteFile(diffPath, []byte("not a real diff\n"), 0o600); err != nil {
		t.Fatalf("write diff: %v", err)
	}

	var seenCmd string
	rt := &mockRuntime{
		workspace: t.TempDir(),
		execFunc: func(_ context.Context, command string, _ runtime.ExecOptions) (runtime.ExecResult, error) {
			seenCmd = command
			// Pretend git apply failed so we don't need a valid diff; we only
			// care about the exact command string composed by the uploader.
			return runtime.ExecResult{ExitCode: 1, Stderr: "not a valid diff"}, nil
		},
	}

	uploader := &applyDiffUploader{}
	caseCfg := &config.CaseConfig{
		ID:    "apply-diff-quote",
		Title: "apply_diff with shell metachars",
		Input: config.Input{Prompt: "test"},
		Context: config.Context{
			Git: &config.GitContext{ApplyDiff: trickyName},
		},
	}

	if err := uploader.Upload(context.Background(), rt, caseCfg, skillDir, skillDir); err == nil {
		t.Fatalf("expected mock git apply to fail, got success")
	}
	tmpPath := filepath.Join(os.TempDir(), trickyName)
	wantCmd := "git apply -- '" + tmpPath + "'"
	if seenCmd != wantCmd {
		t.Fatalf("command = %q, want %q", seenCmd, wantCmd)
	}
}

func TestGitInitUploader_InitWithRemotes(t *testing.T) {
	workspace := t.TempDir()
	rt := &mockRuntime{workspace: workspace}
	uploader := &gitInitUploader{}

	caseCfg := &config.CaseConfig{
		ID:    "git-init-remotes",
		Title: "Git init with remotes",
		Input: config.Input{Prompt: "test"},
		Context: config.Context{
			Git: &config.GitContext{
				Init: true,
				Remotes: []config.GitRemote{
					{Name: "origin", URL: "https://github.com/example/repo.git"},
				},
			},
		},
	}

	if err := uploader.Upload(context.Background(), rt, caseCfg, "", ""); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify git repo was initialized with the remote
	result, err := rt.Exec(context.Background(), "git remote -v", runtime.ExecOptions{Cwd: workspace})
	if err != nil {
		t.Fatalf("git remote failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("git remote exited with code %d: %s", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "origin") {
		t.Fatalf("expected remote 'origin' in output, got %s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "https://github.com/example/repo.git") {
		t.Fatalf("expected remote URL in output, got %s", result.Stdout)
	}
}

// absoluteSecretPath returns a path that filepath.IsAbs reports as absolute on
// the host OS. On Windows that requires a drive letter; `\tmp\secret.txt`
// alone is considered relative.
func absoluteSecretPath() string {
	if goruntime.GOOS == "windows" {
		return `C:\tmp\secret.txt`
	}
	return "/tmp/secret.txt"
}
