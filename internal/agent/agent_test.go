package agent

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/alibaba/skill-up/internal/agentkind"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/platform"
	"github.com/alibaba/skill-up/internal/runtime"
)

// modelAuto is the QoderCLI "auto" model tier, shared across agent tests.
const modelAuto = "auto"

func TestListSkillFiles_ExcludesEvals(t *testing.T) {
	t.Parallel()

	// Create temp dir with evals and regular files
	dir := t.TempDir()

	// Create regular files (should be included)
	subdir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "README.md"), []byte("readme"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create evals directory (should be excluded)
	evalsDir := filepath.Join(dir, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evalsDir, "test.yaml"), []byte("test: true"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create nested evals (should be excluded)
	nestedEvals := filepath.Join(dir, "evals", "nested", "data")
	if err := os.MkdirAll(nestedEvals, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedEvals, "file.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	files, err := ListSkillFiles(dir)
	if err != nil {
		t.Fatalf("ListSkillFiles failed: %v", err)
	}

	// Check included files
	fileSet := make(map[string]bool)
	for _, f := range files {
		fileSet[f] = true
	}

	if !fileSet["SKILL.md"] {
		t.Error("SKILL.md should be included")
	}
	if !fileSet[filepath.Join("subdir", "README.md")] {
		t.Error("subdir/README.md should be included")
	}

	// Check excluded files
	if fileSet["evals/test.yaml"] {
		t.Error("evals/test.yaml should be excluded")
	}
	if fileSet[filepath.Join("evals", "nested", "data", "file.txt")] {
		t.Error("evals/nested/data/file.txt should be excluded")
	}
}

func TestInstallSkill_PreservesExecutableScripts(t *testing.T) {
	t.Parallel()
	if goruntime.GOOS == "windows" {
		t.Skip("Unix file modes are not meaningful on Windows")
	}

	// A skill that ships an executable helper script alongside SKILL.md.
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# Skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(src, "scripts", "run.sh")
	//nolint:gosec // fixture must be executable to verify permission preservation
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	rt := &runtime.NoneRuntime{}
	if err := rt.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	if err := installSkill(context.Background(), rt, src, "skill"); err != nil {
		t.Fatalf("installSkill failed: %v", err)
	}

	info, err := os.Stat(filepath.Join(rt.Workspace(), "skill", "scripts", "run.sh"))
	if err != nil {
		t.Fatalf("installed script should exist: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("installed script lost its executable bit: mode = %o", info.Mode().Perm())
	}
}

func TestListSkillFiles_EmptyDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files, err := ListSkillFiles(dir)
	if err != nil {
		t.Fatalf("ListSkillFiles failed: %v", err)
	}
	// Empty dir still returns ["."]
	if len(files) != 1 || files[0] != "." {
		t.Errorf("expected [.], got %v", files)
	}
}

func TestDetectAgent_QoderCLI(t *testing.T) {
	t.Parallel()

	cfg := Config{Name: agentkind.QoderCLIAlias}
	agent, err := DetectAgent(agentkind.QoderCLIAlias, cfg)
	if err != nil {
		t.Fatalf("DetectAgent failed: %v", err)
	}
	if agent.Name() != agentkind.QoderCLIAlias {
		t.Errorf("expected %s, got %s", agentkind.QoderCLIAlias, agent.Name())
	}
}

func TestDetectAgent_QoderCLILegacyAlias(t *testing.T) {
	t.Parallel()

	cfg := Config{Name: agentkind.QoderCLI}
	agent, err := DetectAgent(agentkind.QoderCLI, cfg)
	if err != nil {
		t.Fatalf("DetectAgent failed: %v", err)
	}
	if agent.Name() != agentkind.QoderCLI {
		t.Errorf("expected legacy alias %s, got %s", agentkind.QoderCLI, agent.Name())
	}
}

func TestDetectAgent_ClaudeCode(t *testing.T) {
	t.Parallel()

	cfg := Config{Name: "claude-code"}
	agent, err := DetectAgent("claude-code", cfg)
	if err != nil {
		t.Fatalf("DetectAgent failed: %v", err)
	}
	if agent.Name() != "claude-code" {
		t.Errorf("expected claude-code, got %s", agent.Name())
	}
}

func TestDetectAgent_Codex(t *testing.T) {
	t.Parallel()

	cfg := Config{Name: "codex"}
	agent, err := DetectAgent("codex", cfg)
	if err != nil {
		t.Fatalf("DetectAgent failed: %v", err)
	}
	if agent.Name() != "codex" {
		t.Errorf("expected codex, got %s", agent.Name())
	}
}

func TestDetectAgent_QwenCode(t *testing.T) {
	t.Parallel()

	for _, name := range []string{agentkind.QwenCode, agentkind.QwenCodeAlias, agentkind.QwenAlias} {
		cfg := Config{Name: name}
		ag, err := DetectAgent(name, cfg)
		if err != nil {
			t.Fatalf("DetectAgent(%q) failed: %v", name, err)
		}
		if _, ok := ag.(*QwenCodeAgent); !ok {
			t.Fatalf("DetectAgent(%q) returned %T, want *QwenCodeAgent", name, ag)
		}
		if ag.Name() != name {
			t.Errorf("expected %s, got %s", name, ag.Name())
		}
	}
}

func TestNewBaseAgent_PreservesExplicitConfig(t *testing.T) {
	base := NewBaseAgent(Config{
		EnvVars: map[string]string{"AGENT_TEST_FLAG": "1"},
		APIKey:  "explicit-key",
		BaseURL: "https://explicit.example.com/v1",
	})

	if got := base.Cfg.APIKey; got != "explicit-key" {
		t.Fatalf("APIKey = %q, want explicit value", got)
	}
	if got := base.Cfg.BaseURL; got != "https://explicit.example.com/v1" {
		t.Fatalf("BaseURL = %q, want explicit value", got)
	}
	if got := base.Cfg.EnvVars["AGENT_TEST_FLAG"]; got != "1" {
		t.Fatalf("AGENT_TEST_FLAG = %q, want preserved runtime env", got)
	}
}

func TestBaseAgentMergeExecOptionsEnvMergesRuntimeAndTelemetry(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://process-collector:4318")
	t.Setenv("SKILL_UP_AGENT_OTEL_RESOURCE_ATTRIBUTES", "telemetry.project.id=745")

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}))

	base := NewBaseAgent(Config{
		Name:          "codex",
		ModelProvider: "openai",
		ModelName:     "gpt-5.4",
	})
	opts := base.mergeExecOptionsEnv(
		ctx,
		ExecOptions{Env: map[string]string{
			"AGENT_TEST_FLAG":             "call",
			"OTEL_EXPORTER_OTLP_ENDPOINT": "http://call-collector:4318",
		}},
		map[string]string{
			"AGENT_TEST_FLAG": "base",
			"BASE_ONLY":       "1",
		},
		base.buildAgentObservabilityAttrs(map[string]string{"skill_up.case.id": "case-1"}),
	)

	if got := opts.Env["AGENT_TEST_FLAG"]; got != "call" {
		t.Fatalf("AGENT_TEST_FLAG = %q, want call env to override base env", got)
	}
	if got := opts.Env["BASE_ONLY"]; got != "1" {
		t.Fatalf("BASE_ONLY = %q, want preserved base env", got)
	}
	if _, ok := opts.Env["PATH"]; ok {
		t.Fatalf("PATH should not be injected by mergeExecOptionsEnv; PATH now flows from runtime baseline via probeAndMergePATH. got %q", opts.Env["PATH"])
	}
	if got := opts.Env["OTEL_EXPORTER_OTLP_ENDPOINT"]; got != "http://call-collector:4318" {
		t.Fatalf("OTEL_EXPORTER_OTLP_ENDPOINT = %q, want call env to override process env", got)
	}
	if opts.Env["TRACEPARENT"] == "" {
		t.Fatalf("expected TRACEPARENT in merged env, got %v", opts.Env)
	}
	resourceAttrs := opts.Env["OTEL_RESOURCE_ATTRIBUTES"]
	for _, want := range []string{
		"telemetry.project.id=745",
		"skill_up.engine=codex",
		"skill_up.model=openai/gpt-5.4",
		"skill_up.case.id=case-1",
		"skill_up.parent_trace_id=4bf92f3577b34da6a3ce929d0e0e4736",
		"skill_up.parent_span_id=00f067aa0ba902b7",
	} {
		if !strings.Contains(resourceAttrs, want) {
			t.Fatalf("expected resource attrs to contain %q, got %q", want, resourceAttrs)
		}
	}
}

func TestMergeExecOptionsEnv_PreservesConfiguredPATH(t *testing.T) {
	t.Parallel()

	base := NewBaseAgent(Config{Name: "codex"})
	opts := base.mergeExecOptionsEnv(
		context.Background(),
		ExecOptions{Env: map[string]string{"PATH": "/call/bin"}},
		map[string]string{"PATH": "/agent/bin"},
		nil,
	)

	if got := opts.Env["PATH"]; got != "/call/bin" {
		t.Fatalf("PATH = %q, want call env to override defaults", got)
	}
}

// probeMergeTestRuntime captures probe Exec calls and MergeEnv calls so
// TestProbeAndMergePATH can verify the helper's wiring without dragging
// in a full agent test fixture.
type probeMergeTestRuntime struct {
	probeCmd    string
	probeStdout string
	probeExit   int
	probeErr    error
	merged      map[string]string
}

func (r *probeMergeTestRuntime) Create(context.Context) error                     { return nil }
func (r *probeMergeTestRuntime) Close() error                                     { return nil }
func (r *probeMergeTestRuntime) Start(context.Context) error                      { return nil }
func (r *probeMergeTestRuntime) Stop(context.Context) error                       { return nil }
func (r *probeMergeTestRuntime) UploadFile(context.Context, string, string) error { return nil }
func (r *probeMergeTestRuntime) UploadDir(context.Context, string, string) error  { return nil }
func (r *probeMergeTestRuntime) DownloadFile(context.Context, string, string) error {
	return nil
}
func (r *probeMergeTestRuntime) DownloadDir(context.Context, string, string) error { return nil }
func (r *probeMergeTestRuntime) Workspace() string                                 { return "" }
func (r *probeMergeTestRuntime) RequiresProcessSandbox() bool                      { return false }
func (r *probeMergeTestRuntime) Exec(_ context.Context, cmd string, _ ExecOptions) (ExecResult, error) {
	r.probeCmd = cmd
	return ExecResult{Stdout: r.probeStdout, ExitCode: r.probeExit}, r.probeErr
}

func (r *probeMergeTestRuntime) MergeEnv(env map[string]string) {
	if r.merged == nil {
		r.merged = make(map[string]string, len(env))
	}
	maps.Copy(r.merged, env)
}
func (r *probeMergeTestRuntime) TargetGOOS() string { return platform.GOOSLinux }

func TestProbeAndMergePATH_HappyPath(t *testing.T) {
	t.Parallel()
	base := NewBaseAgent(Config{Name: "claude-code"})
	rt := &probeMergeTestRuntime{probeStdout: "  /resolved/bin:/usr/bin\n"}

	base.probeAndMergePATH(context.Background(), rt, `printf '%s' "$HOME/.local/bin:$PATH"`)

	if rt.probeCmd != `printf '%s' "$HOME/.local/bin:$PATH"` {
		t.Fatalf("probe cmd = %q, want the supplied probeCmd verbatim", rt.probeCmd)
	}
	if got := rt.merged["PATH"]; got != "/resolved/bin:/usr/bin" {
		t.Fatalf("merged PATH = %q, want trimmed probe stdout", got)
	}
}

func TestProbeAndMergePATH_SkipsMergeOnProbeFailure(t *testing.T) {
	t.Parallel()
	base := NewBaseAgent(Config{Name: "claude-code"})
	rt := &probeMergeTestRuntime{probeExit: 127, probeStdout: "garbage"}

	base.probeAndMergePATH(context.Background(), rt, `printf '%s' "$HOME/.local/bin:$PATH"`)

	if rt.merged != nil {
		t.Fatalf("MergeEnv should not have been called on probe failure; got %+v", rt.merged)
	}
}

func TestProbeAndMergePATH_SkipsMergeOnEmptyStdout(t *testing.T) {
	t.Parallel()
	base := NewBaseAgent(Config{Name: "claude-code"})
	rt := &probeMergeTestRuntime{probeStdout: "   \n"} // whitespace only

	base.probeAndMergePATH(context.Background(), rt, `printf '%s' "$HOME/.local/bin:$PATH"`)

	if rt.merged != nil {
		t.Fatalf("MergeEnv should not have been called on empty probe stdout; got %+v", rt.merged)
	}
}

func TestDetectAgentWithInitParams_SetsTypedCredentialFields(t *testing.T) {
	t.Parallel()

	ag, err := DetectAgentWithInitParams("codex", credential.AgentInitParams{
		Provider: "openai",
		Model:    "gpt-5.4",
		APIKey:   "openai-test-token",
		BaseURL:  "https://openai.example.com/v1",
	}, nil)
	if err != nil {
		t.Fatalf("DetectAgentWithInitParams failed: %v", err)
	}

	codexAgent, ok := ag.(*CodexAgent)
	if !ok {
		t.Fatalf("expected *CodexAgent, got %T", ag)
	}
	if got := codexAgent.Cfg.APIKey; got != "openai-test-token" {
		t.Fatalf("APIKey = %q, want openai-test-token", got)
	}
	if got := codexAgent.Cfg.BaseURL; got != "https://openai.example.com/v1" {
		t.Fatalf("BaseURL = %q, want explicit value", got)
	}
	if got := codexAgent.Cfg.ModelName; got != "gpt-5.4" {
		t.Fatalf("ModelName = %q, want gpt-5.4", got)
	}
	if got := codexAgent.Cfg.ModelProvider; got != "openai" {
		t.Fatalf("ModelProvider = %q, want openai", got)
	}
}

func TestDetectAgentWithInitParams_QoderMapsAPIKeyToRuntimeEnv(t *testing.T) {
	token := "qoder-runtime-token" //nolint:gosec // test credential, not real
	t.Setenv(credential.EnvQoderPersonalAccessToken, token)

	ag, err := DetectAgentWithInitParams("qoder-cli", credential.AgentInitParams{
		Provider: "qoder",
		Model:    "auto",
	}, nil)
	if err != nil {
		t.Fatalf("DetectAgentWithInitParams failed: %v", err)
	}

	qoderAgent, ok := ag.(*QoderCLIAgent)
	if !ok {
		t.Fatalf("expected *QoderCLIAgent, got %T", ag)
	}
	if got := qoderAgent.Cfg.EnvVars[credential.EnvQoderPersonalAccessToken]; got != token {
		t.Fatalf("%s = %q, want %q", credential.EnvQoderPersonalAccessToken, got, token)
	}
}

func TestDetectAgentWithInitParams_QoderIgnoresParamsAPIKey(t *testing.T) {
	t.Parallel()

	ag, err := DetectAgentWithInitParams("qoder-cli", credential.AgentInitParams{ //nolint:gosec // test dummy key
		Provider: "anthropic",
		Model:    "auto",
		APIKey:   "sk-ant-should-not-appear",
	}, nil)
	if err != nil {
		t.Fatalf("DetectAgentWithInitParams failed: %v", err)
	}

	qoderAgent, ok := ag.(*QoderCLIAgent)
	if !ok {
		t.Fatalf("expected *QoderCLIAgent, got %T", ag)
	}
	if got := qoderAgent.Cfg.EnvVars[credential.EnvQoderPersonalAccessToken]; got != "" {
		t.Fatalf("expected empty token in EnvVars, got %q (params.APIKey should not leak)", got)
	}
}

func TestDetectAgent_Unsupported(t *testing.T) {
	t.Parallel()

	cfg := Config{Name: "unknown"}
	_, err := DetectAgent("unknown-agent", cfg)
	if err == nil {
		t.Fatal("expected error for unsupported agent")
	}
	var unsupportedErr *UnsupportedAgentError
	if !errors.As(err, &unsupportedErr) {
		t.Errorf("expected UnsupportedAgentError, got %T", err)
	}
}

func TestUnsupportedAgentError(t *testing.T) {
	t.Parallel()

	err := &UnsupportedAgentError{Name: "test-agent"}
	if err.Error() != `unsupported agent "test-agent": missing engine.custom` {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestDetectAgentWithInitParams_StripsAutoForNonQoderEngines(t *testing.T) {
	t.Parallel()

	ag, err := DetectAgentWithInitParams("claude-code", credential.AgentInitParams{
		Model: "auto",
	}, nil)
	if err != nil {
		t.Fatalf("DetectAgentWithInitParams failed: %v", err)
	}

	ccAgent, ok := ag.(*ClaudeCodeAgent)
	if !ok {
		t.Fatalf("expected *ClaudeCodeAgent, got %T", ag)
	}
	if got := ccAgent.Cfg.ModelName; got != "" {
		t.Fatalf("ModelName = %q, want empty (auto should be stripped for claude-code)", got)
	}
}

func TestDetectAgentWithInitParams_PreservesAutoForQoderCLI(t *testing.T) {
	t.Parallel()

	ag, err := DetectAgentWithInitParams("qoder-cli", credential.AgentInitParams{
		Model: "auto",
	}, nil)
	if err != nil {
		t.Fatalf("DetectAgentWithInitParams failed: %v", err)
	}

	qoderAgent, ok := ag.(*QoderCLIAgent)
	if !ok {
		t.Fatalf("expected *QoderCLIAgent, got %T", ag)
	}
	if got := qoderAgent.Cfg.ModelName; got != "auto" {
		t.Fatalf("ModelName = %q, want auto (should be preserved for qoder-cli)", got)
	}
}

func TestDetectAgentWithInitParams_ForwardsKwargs(t *testing.T) {
	t.Parallel()

	kwargs := map[string]string{KwargBypassSandbox: "true", "future_key": "x"}
	ag, err := DetectAgentWithInitParams("codex", credential.AgentInitParams{
		Provider: "openai",
		Model:    "gpt-5.4",
	}, kwargs)
	if err != nil {
		t.Fatalf("DetectAgentWithInitParams failed: %v", err)
	}

	codexAgent, ok := ag.(*CodexAgent)
	if !ok {
		t.Fatalf("expected *CodexAgent, got %T", ag)
	}
	if got := codexAgent.Cfg.Kwargs[KwargBypassSandbox]; got != "true" {
		t.Fatalf("Cfg.Kwargs[%s] = %q, want true", KwargBypassSandbox, got)
	}
	if got := codexAgent.Cfg.Kwargs["future_key"]; got != "x" {
		t.Fatalf("Cfg.Kwargs[future_key] = %q, want x", got)
	}
}

func TestExtractSessionIDFromPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "jsonl extension", path: "/home/user/.qoder/projects/ws/abc-123.jsonl", want: "abc-123"},
		{name: "uuid format", path: "/home/user/.claude/projects/ws/550e8400-e29b-41d4-a716-446655440000.jsonl", want: "550e8400-e29b-41d4-a716-446655440000"},
		{name: "no extension", path: "/path/to/session-id", want: "session-id"},
		{name: "bare filename", path: "my-session.jsonl", want: "my-session"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractSessionIDFromPath(tt.path)
			if got != tt.want {
				t.Fatalf("extractSessionIDFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
