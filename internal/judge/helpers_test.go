package judge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// ---------------------------------------------------------------------------
// Shared test helpers
// ---------------------------------------------------------------------------

// assertNoError fails the test immediately if err is non-nil.
func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// assertStatus fails the test if the result status does not match expected.
func assertStatus(t *testing.T, r *Result, expected Status) {
	t.Helper()
	if r.Status != expected {
		t.Fatalf("expected status %s, got %s (assertions: %+v)", expected, r.Status, r.AssertionResults)
	}
}

// intPtr returns a pointer to v.
func intPtr(v int) *int { return &v }

// writeScript creates a temporary executable script and returns its path.
func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil { //nolint:gosec // scripts need execute permission
		t.Fatalf("failed to write script: %v", err)
	}
	return path
}

// buildMockAgentOutput creates a JSON string simulating agent judge output.
func buildMockAgentOutput(results []CriterionResult) string {
	resp := struct {
		Results []CriterionResult `json:"results"`
	}{Results: results}
	data, err := json.Marshal(resp)
	if err != nil {
		return ""
	}
	return string(data)
}

// ---------------------------------------------------------------------------
// Shared mock types
// ---------------------------------------------------------------------------

// mockJudgeTestAgent is a fake Agent for testing AgentJudge.
type mockJudgeTestAgent struct {
	output    string
	err       error
	runResult *agent.SessionResult

	// runDelay simulates a slow agent call by blocking on either the ctx
	// deadline or the duration expiring, whichever happens first. Used by
	// timeout tests; leave zero for the immediate-return default.
	runDelay time.Duration

	// observedDeadline captures whatever deadline the agent received on its
	// last Run call, so tests can assert that AgentJudge applied
	// TimeoutSeconds correctly. ok mirrors ctx.Deadline()'s second return.
	observedDeadline   time.Time
	observedDeadlineOK bool
}

func (m *mockJudgeTestAgent) Name() string { return "mock-judge-agent" }

func (m *mockJudgeTestAgent) Install(_ context.Context, _ runtime.Runtime) error {
	return nil
}

func (m *mockJudgeTestAgent) InstallMCP(_ context.Context, _ runtime.Runtime, _ runtime.MCPConfig) error {
	return nil
}

func (m *mockJudgeTestAgent) InstallSkill(_ context.Context, _ runtime.Runtime, _ runtime.SkillConfig) error {
	return nil
}

func (m *mockJudgeTestAgent) Check(_ context.Context, _ runtime.Runtime) error { return nil }

func (m *mockJudgeTestAgent) CheckCredentials(_ context.Context) error { return nil }

func (m *mockJudgeTestAgent) Run(ctx context.Context, _ runtime.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
	m.observedDeadline, m.observedDeadlineOK = ctx.Deadline()
	if m.runDelay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.runDelay):
		}
	}
	if m.runResult != nil {
		return m.runResult, m.err
	}
	return &agent.SessionResult{FinalMessage: m.output}, m.err
}

// mockJudgeTestRuntime is a minimal Runtime for testing.
type mockJudgeTestRuntime struct{}

func (m *mockJudgeTestRuntime) Create(_ context.Context) error                    { return nil }
func (m *mockJudgeTestRuntime) Close() error                                      { return nil }
func (m *mockJudgeTestRuntime) Workspace() string                                 { return "/tmp/test" }
func (m *mockJudgeTestRuntime) RequiresProcessSandbox() bool                      { return true }
func (m *mockJudgeTestRuntime) Start(_ context.Context) error                     { return nil }
func (m *mockJudgeTestRuntime) Stop(_ context.Context) error                      { return nil }
func (m *mockJudgeTestRuntime) UploadFile(_ context.Context, _, _ string) error   { return nil }
func (m *mockJudgeTestRuntime) UploadDir(_ context.Context, _, _ string) error    { return nil }
func (m *mockJudgeTestRuntime) DownloadFile(_ context.Context, _, _ string) error { return nil }
func (m *mockJudgeTestRuntime) DownloadDir(_ context.Context, _, _ string) error  { return nil }
func (m *mockJudgeTestRuntime) Exec(_ context.Context, _ string, _ runtime.ExecOptions) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, nil
}

func (m *mockJudgeTestRuntime) ExecWithStdin(_ context.Context, _ string, _ string, _ runtime.ExecOptions) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, nil
}
