package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/alibaba/skill-up/internal/credential"
)

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

	cfg := Config{Name: qoderCLIEngineAlias}
	agent, err := DetectAgent(qoderCLIEngineAlias, cfg)
	if err != nil {
		t.Fatalf("DetectAgent failed: %v", err)
	}
	if agent.Name() != qoderCLIEngineAlias {
		t.Errorf("expected %s, got %s", qoderCLIEngineAlias, agent.Name())
	}
}

func TestDetectAgent_QoderCLILegacyAlias(t *testing.T) {
	t.Parallel()

	cfg := Config{Name: qoderCLIEngineName}
	agent, err := DetectAgent(qoderCLIEngineName, cfg)
	if err != nil {
		t.Fatalf("DetectAgent failed: %v", err)
	}
	if agent.Name() != qoderCLIEngineName {
		t.Errorf("expected legacy alias %s, got %s", qoderCLIEngineName, agent.Name())
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
	if got := opts.Env["PATH"]; got != agentExecutablePath {
		t.Fatalf("PATH = %q, want agent executable path", got)
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
	if err.Error() != "unsupported agent: test-agent" {
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
