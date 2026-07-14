package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/logging"
	"github.com/alibaba/skill-up/pkg/transcript"
)

var logCaptureMu sync.Mutex

// ---------------------------------------------------------------------------
// All criteria pass
// ---------------------------------------------------------------------------

func TestAgentJudge_AllPass(t *testing.T) {
	output := buildMockAgentOutput([]CriterionResult{
		{Criterion: "output identifies the bug", Passed: true, Evidence: "output contains 'null pointer'"},
		{Criterion: "no false positives", Passed: true, Evidence: "no false positives found"},
		{Criterion: "actionable suggestion", Passed: true, Evidence: "suggests specific fix at line 42"},
	})
	ag := &mockJudgeTestAgent{output: output}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{"output identifies the bug", "no false positives", "actionable suggestion"}, nil, 0)
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
	if r.JudgeSession == nil {
		t.Fatal("expected JudgeSession from judge agent run (distinct from main eval agent)")
	}
	if r.JudgeSession.FinalMessage != output {
		t.Fatalf("JudgeSession.FinalMessage: got %q want judge agent output", r.JudgeSession.FinalMessage)
	}
	if r.Summary.PassRate != 1.0 {
		t.Fatalf("expected pass_rate 1.0, got %f", r.Summary.PassRate)
	}
	if r.Summary.Total != 3 {
		t.Fatalf("expected 3 assertions, got %d", r.Summary.Total)
	}
	if r.JudgeContext == nil || r.JudgeContext.Manifest == nil {
		t.Fatal("expected judge context metadata")
	}
	if r.JudgeContext.Profile != "standard" {
		t.Fatalf("expected standard judge context profile, got %q", r.JudgeContext.Profile)
	}
}

// ---------------------------------------------------------------------------
// Partial pass — above threshold
// ---------------------------------------------------------------------------

func TestAgentJudge_PartialPass_AboveThreshold(t *testing.T) {
	output := buildMockAgentOutput([]CriterionResult{
		{Criterion: "c1", Passed: true, Evidence: "yes"},
		{Criterion: "c2", Passed: true, Evidence: "yes"},
		{Criterion: "c3", Passed: false, Evidence: "not found"},
	})
	ag := &mockJudgeTestAgent{output: output}
	rt := &mockJudgeTestRuntime{}

	// 2/3 = 0.667, threshold default 0.7 → FAIL
	j := NewAgentJudge(ag, rt, "test-model", []string{"c1", "c2", "c3"}, nil, 0)
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail) // 0.667 < 0.7

	// With lower threshold 0.6 → PASS
	threshold := 0.6
	j2 := NewAgentJudge(ag, rt, "test-model", []string{"c1", "c2", "c3"}, &threshold, 0)
	r2, err := j2.Evaluate(context.Background(), Input{FinalMessage: "test"})
	assertNoError(t, err)
	assertStatus(t, r2, StatusPass) // 0.667 >= 0.6
}

// ---------------------------------------------------------------------------
// All fail — below threshold
// ---------------------------------------------------------------------------

func TestAgentJudge_AllFail(t *testing.T) {
	output := buildMockAgentOutput([]CriterionResult{
		{Criterion: "c1", Passed: false, Evidence: "not found"},
		{Criterion: "c2", Passed: false, Evidence: "missing"},
	})
	ag := &mockJudgeTestAgent{output: output}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{"c1", "c2"}, nil, 0)
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
	if r.Summary.PassRate != 0 {
		t.Fatalf("expected pass_rate 0, got %f", r.Summary.PassRate)
	}
}

// ---------------------------------------------------------------------------
// Threshold exactly met
// ---------------------------------------------------------------------------

func TestAgentJudge_ThresholdExactlyMet(t *testing.T) {
	output := buildMockAgentOutput([]CriterionResult{
		{Criterion: "c1", Passed: true, Evidence: "yes"},
		{Criterion: "c2", Passed: false, Evidence: "no"},
	})
	ag := &mockJudgeTestAgent{output: output}
	rt := &mockJudgeTestRuntime{}

	// 1/2 = 0.5, threshold = 0.5 → PASS (>=)
	threshold := 0.5
	j := NewAgentJudge(ag, rt, "test-model", []string{"c1", "c2"}, &threshold, 0)
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

// ---------------------------------------------------------------------------
// Agent returns error
// ---------------------------------------------------------------------------

func TestAgentJudge_AgentError(t *testing.T) {
	ag := &mockJudgeTestAgent{err: errors.New("API rate limit exceeded")}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{"c1"}, nil, 0)
	_, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	if err == nil {
		t.Fatal("expected error from agent")
	}
	if !strings.Contains(err.Error(), "agent_judge agent call failed") {
		t.Fatalf("expected wrapped error, got: %v", err)
	}
}

func TestAgentJudge_AgentError_PreservesSession(t *testing.T) {
	session := &agent.SessionResult{
		FinalMessage: "API Error: 400 rate limit",
		Artifacts: &agent.SessionArtifacts{
			GeneratedFiles: []string{"stdout.json"},
		},
	}
	ag := &mockJudgeTestAgent{
		err:       errors.New("API rate limit exceeded"),
		runResult: session,
	}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{"c1"}, nil, 0)
	_, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	if err == nil {
		t.Fatal("expected error from agent")
	}
	if got := SessionResultFromError(err); got != session {
		t.Fatalf("expected preserved session result, got %#v", got)
	}
}

func TestAgentJudge_RecoversTimedOutSessionWithValidJSON(t *testing.T) {
	output := buildMockAgentOutput([]CriterionResult{
		{Criterion: "c1", Passed: true, Evidence: "yes"},
	})
	session := &agent.SessionResult{FinalMessage: output, ExitCode: -1}
	ag := &mockJudgeTestAgent{
		err:       context.DeadlineExceeded,
		runResult: session,
	}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{"c1"}, nil, 0)
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
	if r.JudgeSession != session {
		t.Fatalf("expected recovered judge session, got %#v", r.JudgeSession)
	}
}

func TestAgentJudge_RecoveryLogsWarning(t *testing.T) {
	output := buildMockAgentOutput([]CriterionResult{
		{Criterion: "c1", Passed: true, Evidence: "yes"},
	})
	session := &agent.SessionResult{FinalMessage: output, ExitCode: -1}
	ag := &mockJudgeTestAgent{
		err:       context.DeadlineExceeded,
		runResult: session,
	}
	rt := &mockJudgeTestRuntime{}

	captured := captureLogOutput(t, func() {
		j := NewAgentJudge(ag, rt, "test-model", []string{"c1"}, nil, 0)
		_, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
		assertNoError(t, err)
	})
	if !strings.Contains(captured, "recovering judge output despite agent error") {
		t.Fatalf("expected recovery warning log, got %q", captured)
	}
}

func TestAgentJudge_CanceledSessionDoesNotRecover(t *testing.T) {
	output := buildMockAgentOutput([]CriterionResult{
		{Criterion: "c1", Passed: true, Evidence: "yes"},
	})
	session := &agent.SessionResult{FinalMessage: output, ExitCode: -1}
	ag := &mockJudgeTestAgent{
		err:       context.Canceled,
		runResult: session,
	}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{"c1"}, nil, 0)
	_, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	if err == nil {
		t.Fatal("expected error for canceled judge run")
	}
	if !strings.Contains(err.Error(), "agent_judge agent call failed") {
		t.Fatalf("expected wrapped agent_judge error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Non-zero exit code with valid JSON → recovers result
// ---------------------------------------------------------------------------

func TestAgentJudge_RecoversNonZeroExitWithValidJSON(t *testing.T) {
	output := buildMockAgentOutput([]CriterionResult{
		{Criterion: "c1", Passed: false, Evidence: "not found"},
	})
	session := &agent.SessionResult{FinalMessage: output, ExitCode: 1}
	ag := &mockJudgeTestAgent{
		err:       errors.New("agent run failed (exit 1)"),
		runResult: session,
	}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{"c1"}, nil, 0)
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
	if r.JudgeSession != session {
		t.Fatalf("expected recovered judge session, got %#v", r.JudgeSession)
	}
}

// ---------------------------------------------------------------------------
// Empty criteria → default PASS
// ---------------------------------------------------------------------------

func TestAgentJudge_EmptyCriteria(t *testing.T) {
	ag := &mockJudgeTestAgent{}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{}, nil, 0)
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	assertNoError(t, err)
	assertStatus(t, r, StatusPass)
}

// ---------------------------------------------------------------------------
// Evidence preserved in results
// ---------------------------------------------------------------------------

func TestAgentJudge_EvidencePreserved(t *testing.T) {
	output := buildMockAgentOutput([]CriterionResult{
		{Criterion: "identified the bug", Passed: true, Evidence: "final_message contains 'null pointer at line 42'"},
	})
	ag := &mockJudgeTestAgent{output: output}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{"identified the bug"}, nil, 0)
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	assertNoError(t, err)
	if len(r.AssertionResults) != 1 {
		t.Fatalf("expected 1 assertion, got %d", len(r.AssertionResults))
	}
	ar := r.AssertionResults[0]
	if ar.Text != "identified the bug" {
		t.Fatalf("expected criterion text preserved, got: %s", ar.Text)
	}
	if !strings.Contains(ar.Evidence, "null pointer at line 42") {
		t.Fatalf("expected evidence preserved, got: %s", ar.Evidence)
	}
}

// ---------------------------------------------------------------------------
// Custom pass_threshold
// ---------------------------------------------------------------------------

func TestAgentJudge_CustomThreshold(t *testing.T) {
	output := buildMockAgentOutput([]CriterionResult{
		{Criterion: "c1", Passed: true, Evidence: "yes"},
		{Criterion: "c2", Passed: true, Evidence: "yes"},
		{Criterion: "c3", Passed: true, Evidence: "yes"},
		{Criterion: "c4", Passed: false, Evidence: "no"},
	})
	ag := &mockJudgeTestAgent{output: output}
	rt := &mockJudgeTestRuntime{}

	// 3/4 = 0.75
	// threshold 0.8 → FAIL
	threshold := 0.8
	j := NewAgentJudge(ag, rt, "test-model", []string{"c1", "c2", "c3", "c4"}, &threshold, 0)
	r, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)

	// threshold 0.75 → PASS
	threshold2 := 0.75
	j2 := NewAgentJudge(ag, rt, "test-model", []string{"c1", "c2", "c3", "c4"}, &threshold2, 0)
	r2, err := j2.Evaluate(context.Background(), Input{FinalMessage: "test"})
	assertNoError(t, err)
	assertStatus(t, r2, StatusPass)
}

// ---------------------------------------------------------------------------
// buildJudgePrompt
// ---------------------------------------------------------------------------

func TestBuildJudgePrompt_ContainsAllParts(t *testing.T) {
	materialized := &MaterializedContext{
		Materials: []ContextMaterial{
			{
				ContextMaterialManifest: ContextMaterialManifest{
					Key:           "final_message",
					Mode:          "include",
					Path:          "/tmp/final_message.txt",
					OriginalBytes: len("Agent found a bug at line 42"),
				},
				InlineContent: "Agent found a bug at line 42",
			},
			{
				ContextMaterialManifest: ContextMaterialManifest{
					Key:           "workspace_diff",
					Mode:          "file_ref",
					Path:          "/tmp/workspace.diff",
					OriginalBytes: len("diff --git a/main.go"),
				},
			},
			{
				ContextMaterialManifest: ContextMaterialManifest{
					Key:           "transcript",
					Mode:          "file_ref",
					Path:          "/tmp/transcript.json",
					OriginalBytes: len("review"),
				},
			},
		},
	}
	prompt := buildJudgePrompt(
		context.Background(),
		[]string{"criterion A", "criterion B"},
		materialized,
	)

	checks := []string{
		"criterion A",
		"criterion B",
		"Agent found a bug at line 42",
		"/tmp/workspace.diff",
		"/tmp/transcript.json",
		"\"passed\"",
		"\"evidence\"",
	}
	for _, c := range checks {
		if !strings.Contains(prompt, c) {
			t.Errorf("prompt missing expected content %q", c)
		}
	}
}

func TestBuildJudgePromptWithSkills_RequiresJudgeSkillUse(t *testing.T) {
	materialized := &MaterializedContext{
		Materials: []ContextMaterial{
			{
				ContextMaterialManifest: ContextMaterialManifest{
					Key:           "final_message",
					Mode:          "include",
					OriginalBytes: len("Agent final message"),
				},
				InlineContent: "Agent final message",
			},
		},
	}
	prompt := buildJudgePrompt(
		context.Background(),
		[]string{"criterion A"},
		materialized,
		[]SkillInfo{
			{Source: "local_path", Path: "evals/fixtures/judge-skill", Target: "~/.claude/skills/judge-skill", Name: "judge-skill"},
			{Source: "local_path", Path: "evals/fixtures/security-judge", Name: "security-judge"},
		},
	)

	checks := []string{
		"## Mandatory Judge Skill Use",
		"You MUST use the installed judge Skill(s)",
		"evals/fixtures/judge-skill",
		"target: ~/.claude/skills/judge-skill",
		"evals/fixtures/security-judge",
	}
	for _, c := range checks {
		if !strings.Contains(prompt, c) {
			t.Fatalf("prompt missing %q:\n%s", c, prompt)
		}
	}
	if strings.Contains(prompt, "SKILL.md") || strings.Contains(prompt, "references/") {
		t.Fatalf("prompt should list identifiers only, got:\n%s", prompt)
	}
}

func TestAgentJudge_EvaluatePassesJudgeSkillsInPrompt(t *testing.T) {
	output := buildMockAgentOutput([]CriterionResult{
		{Criterion: "criterion A", Passed: true, Evidence: "used rubric"},
	})
	ag := &mockJudgeTestAgent{output: output}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(
		ag,
		rt,
		"test-model",
		[]string{"criterion A"},
		nil,
		0,
		[]SkillInfo{{Source: "local_path", Path: "evals/fixtures/judge-skill", Name: "judge-skill"}},
	)
	_, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	assertNoError(t, err)

	if len(ag.lastMessages) != 1 {
		t.Fatalf("lastMessages length = %d, want 1", len(ag.lastMessages))
	}
	if !strings.Contains(ag.lastMessages[0].Content, "evals/fixtures/judge-skill") {
		t.Fatalf("judge prompt missing skill path:\n%s", ag.lastMessages[0].Content)
	}
}

func TestBuildContextMetadata_UsesManifestMaterializedDir(t *testing.T) {
	materialized := &MaterializedContext{
		Dir: "/tmp/skill-up/run/context",
		Manifest: ContextManifest{
			Profile:         "standard",
			MaterializedDir: judgeContextArtifactDir,
		},
	}

	metadata := buildContextMetadata(nil, materialized, "prompt")
	if metadata.MaterializedDir != judgeContextArtifactDir {
		t.Fatalf("materialized_dir = %q, want %q", metadata.MaterializedDir, judgeContextArtifactDir)
	}
}

func TestMaterializeJudgeContext_TranscriptMarshalFailure_ReturnsError(t *testing.T) {
	_, err := MaterializeJudgeContext(context.Background(), &mockJudgeTestRuntime{}, nil, Input{
		Transcript: transcript.Transcript{
			{
				Role: transcript.RoleToolResult,
				ToolResult: &transcript.ToolResultInfo{
					Status:  "success",
					Content: func() {},
				},
			},
		},
	}, t.TempDir())
	if err == nil {
		t.Fatal("expected marshal error")
	}
	if !strings.Contains(err.Error(), "marshal transcript") {
		t.Fatalf("expected transcript marshal error, got %v", err)
	}
}

func captureLogOutput(t *testing.T, fn func()) string {
	t.Helper()

	logCaptureMu.Lock()
	defer logCaptureMu.Unlock()

	var buf bytes.Buffer
	restoreOutput := logging.SetOutputForTest(&buf)

	fn()

	restoreOutput()
	return buf.String()
}

// ---------------------------------------------------------------------------
// Partial results — agent returns fewer criteria than configured
// ---------------------------------------------------------------------------

func TestAgentJudge_PartialResults_ReturnsError(t *testing.T) {
	// 3 criteria configured, agent only returns 1 with passed=true.
	// Without the count check this would give pass_rate=1.0 → PASS (wrong).
	output := buildMockAgentOutput([]CriterionResult{
		{Criterion: "c1", Passed: true, Evidence: "found it"},
	})
	ag := &mockJudgeTestAgent{output: output}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{"c1", "c2", "c3"}, nil, 0)
	_, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	if err == nil {
		t.Fatal("expected error for partial criterion results")
	}
	if !strings.Contains(err.Error(), "expected 3 criterion results, got 1") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Empty results — agent returns no criteria
// ---------------------------------------------------------------------------

func TestAgentJudge_EmptyResults_ReturnsError(t *testing.T) {
	output := buildMockAgentOutput([]CriterionResult{})
	ag := &mockJudgeTestAgent{output: output}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{"c1", "c2"}, nil, 0)
	_, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	if err == nil {
		t.Fatal("expected error for empty criterion results")
	}
	if !strings.Contains(err.Error(), "expected 2 criterion results, got 0") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Empty evidence — agent omits evidence for a criterion
// ---------------------------------------------------------------------------

func TestAgentJudge_EmptyEvidence_ReturnsError(t *testing.T) {
	output := buildMockAgentOutput([]CriterionResult{
		{Criterion: "c1", Passed: true, Evidence: "ok"},
		{Criterion: "c2", Passed: true, Evidence: ""},
	})
	ag := &mockJudgeTestAgent{output: output}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{"c1", "c2"}, nil, 0)
	_, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	if err == nil {
		t.Fatal("expected error for empty evidence")
	}
	if !strings.Contains(err.Error(), "empty evidence") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TimeoutSeconds wiring
// ---------------------------------------------------------------------------

func TestAgentJudge_TimeoutSeconds_AppliesDeadline(t *testing.T) {
	ag := &mockJudgeTestAgent{
		output:   buildMockAgentOutput([]CriterionResult{{Criterion: "c1", Passed: true, Evidence: "ok"}}),
		runDelay: 500 * time.Millisecond,
	}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{"c1"}, nil, 1) // 1s budget should beat the 500ms delay
	deadline, ok := assertEvaluatesWithDeadline(t, j, ag)
	if !ok {
		t.Fatalf("expected agent ctx to have a deadline when TimeoutSeconds=1")
	}
	if d := time.Until(deadline); d <= 0 || d > time.Second {
		t.Fatalf("deadline %v not within (0,1s] from now", d)
	}
}

func TestAgentJudge_TimeoutSeconds_ZeroDoesNotShortenParentCtx(t *testing.T) {
	ag := &mockJudgeTestAgent{output: buildMockAgentOutput([]CriterionResult{{Criterion: "c1", Passed: true, Evidence: "ok"}})}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{"c1"}, nil, 0)
	if _, err := j.Evaluate(context.Background(), Input{FinalMessage: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ag.observedDeadlineOK {
		t.Fatalf("expected no deadline on inner ctx when TimeoutSeconds=0, got %v", ag.observedDeadline)
	}
}

func TestAgentJudge_TimeoutSeconds_DeadlineKillsSlowAgent(t *testing.T) {
	ag := &mockJudgeTestAgent{
		output:   buildMockAgentOutput([]CriterionResult{{Criterion: "c1", Passed: true, Evidence: "ok"}}),
		runDelay: 2 * time.Second,
	}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{"c1"}, nil, 1) // 1s budget, agent takes 2s
	start := time.Now()
	_, err := j.Evaluate(context.Background(), Input{FinalMessage: "x"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected error when agent exceeds judge timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected wrapped context.DeadlineExceeded, got %v", err)
	}
	if !strings.Contains(err.Error(), "judge timeout 1s via judge.timeout_seconds") {
		t.Fatalf("expected judge timeout annotation in error, got %v", err)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("Evaluate ran %v, expected to abort near the 1s deadline", elapsed)
	}
}

func TestAgentJudge_TimeoutSeconds_ParentTimeoutNotAnnotatedAsJudgeTimeout(t *testing.T) {
	ag := &mockJudgeTestAgent{
		output:   buildMockAgentOutput([]CriterionResult{{Criterion: "c1", Passed: true, Evidence: "ok"}}),
		runDelay: 2 * time.Second,
	}
	rt := &mockJudgeTestRuntime{}

	// Judge has a generous 10s budget; the parent ctx is the binding deadline at 200ms.
	j := NewAgentJudge(ag, rt, "test-model", []string{"c1"}, nil, 10)
	parentCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := j.Evaluate(parentCtx, Input{FinalMessage: "x"})
	if err == nil {
		t.Fatalf("expected error when parent ctx expires")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected wrapped context.DeadlineExceeded, got %v", err)
	}
	if strings.Contains(err.Error(), "judge.timeout_seconds") {
		t.Fatalf("parent-ctx deadline must not be labelled as judge timeout, got %v", err)
	}
}

func assertEvaluatesWithDeadline(t *testing.T, j *AgentJudge, ag *mockJudgeTestAgent) (time.Time, bool) {
	t.Helper()
	if _, err := j.Evaluate(context.Background(), Input{FinalMessage: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return ag.observedDeadline, ag.observedDeadlineOK
}

// ---------------------------------------------------------------------------
// extractJSON tests
// ---------------------------------------------------------------------------

func TestExtractJSON_DirectParse(t *testing.T) {
	input := `{"results": [{"criterion": "c1", "passed": true, "evidence": "ok"}]}`
	var resp judgeResponse
	err := extractJSON(input, &resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
}

func TestExtractJSON_MarkdownCodeBlock(t *testing.T) {
	input := "Here is the evaluation:\n```json\n{\"results\": [{\"criterion\": \"c1\", \"passed\": true, \"evidence\": \"ok\"}]}\n```\nDone."
	var resp judgeResponse
	err := extractJSON(input, &resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
}

func TestExtractJSON_BraceFinding(t *testing.T) {
	input := "Some text before {\"results\": [{\"criterion\": \"c1\", \"passed\": false, \"evidence\": \"no\"}]} and after"
	var resp judgeResponse
	err := extractJSON(input, &resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
	if resp.Results[0].Passed {
		t.Fatal("expected passed=false")
	}
}

func TestExtractJSON_BracesInsideStrings(t *testing.T) {
	// JSON where evidence contains braces — should not break brace matching.
	input := `Some text {"results": [{"criterion": "c1", "passed": true, "evidence": "expected { but got }"}]} after`
	var resp judgeResponse
	err := extractJSON(input, &resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
	if resp.Results[0].Evidence != "expected { but got }" {
		t.Fatalf("evidence mismatch: %q", resp.Results[0].Evidence)
	}
}

func TestExtractJSON_EscapedQuotesInStrings(t *testing.T) {
	// JSON with escaped quotes inside string values.
	input := `{"results": [{"criterion": "c1", "passed": true, "evidence": "value with \"nested\" braces { }"}]}`
	var resp judgeResponse
	err := extractJSON(input, &resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
}

func TestExtractJSON_NestedObjectsInsideJSON(t *testing.T) {
	input := `prefix {"results": [{"criterion": "c1", "passed": true, "evidence": "see nested object {\"path\":\"/tmp\",\"ok\":true}"}]} suffix`
	var resp judgeResponse
	err := extractJSON(input, &resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
	if !strings.Contains(resp.Results[0].Evidence, `{"path":"/tmp","ok":true}`) {
		t.Fatalf("unexpected evidence: %q", resp.Results[0].Evidence)
	}
}

func TestExtractJSON_NoJSON(t *testing.T) {
	input := "This output has no JSON at all"
	var resp judgeResponse
	err := extractJSON(input, &resp)
	if err == nil {
		t.Fatal("expected error for no JSON")
	}
}

func TestExtractJSON_SingleQuoteStrings(t *testing.T) {
	// LLM sometimes outputs Python-style pseudo-JSON with single-quoted strings
	// containing braces. The brace finder should skip content inside single quotes.
	input := `Some text {'key': 'value with { and }'} then {"results": [{"criterion": "c1", "passed": true, "evidence": "ok"}]}`
	var resp judgeResponse
	err := extractJSON(input, &resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
}

func TestExtractJSON_SingleQuotedPseudoJSONWithEscapedQuote(t *testing.T) {
	// Python-style pseudo-JSON is not valid JSON, but the scanner should still
	// skip over it and find the later valid JSON payload.
	input := "prefix {'note': 'it\\'s tricky { here }'} middle " +
		`{"results": [{"criterion": "c1", "passed": true, "evidence": "ok"}]}`
	var resp judgeResponse
	err := extractJSON(input, &resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
}

func TestFindJSONObjectEnd_NestedObjectAndEscapedQuotes(t *testing.T) {
	input := `prefix {"outer":{"inner":"value with \"quote\" and { brace }"},"ok":true} suffix`
	start := strings.Index(input, "{")
	if start < 0 {
		t.Fatal("expected opening brace")
	}

	end, ok := findJSONObjectEnd(input, start)
	if !ok {
		t.Fatal("expected to find JSON object end")
	}

	got := input[start : end+1]
	want := `{"outer":{"inner":"value with \"quote\" and { brace }"},"ok":true}`
	if got != want {
		t.Fatalf("unexpected JSON object: got %q want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// repairJSONQuotes — fixes unescaped double-quotes inside string values
// ---------------------------------------------------------------------------

func TestRepairJSONQuotes_AlreadyValid(t *testing.T) {
	input := `{"results": [{"criterion": "c1", "passed": true, "evidence": "ok"}]}`
	got := repairJSONQuotes(input)
	if got != input {
		t.Fatalf("valid JSON should be unchanged, got %q", got)
	}
}

func TestRepairJSONQuotes_UnescapedQuotesInEvidence(t *testing.T) {
	// Simulate LLM output where evidence contains unescaped double-quotes:
	// "evidence": "output is "hello world" which fails"
	input := `{"results": [{"criterion": "c1", "passed": false, "evidence": "output is "hello world" which fails"}]}`
	var resp judgeResponse
	// Direct parse should fail on the malformed JSON.
	if err := json.Unmarshal([]byte(input), &resp); err == nil {
		t.Fatal("expected malformed JSON to fail direct parse")
	}
	// extractJSON should repair and succeed.
	if err := extractJSON(input, &resp); err != nil {
		t.Fatalf("extractJSON should repair malformed quotes: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
	if resp.Results[0].Passed {
		t.Fatal("expected passed=false")
	}
	if !strings.Contains(resp.Results[0].Evidence, "hello world") {
		t.Fatalf("expected evidence to contain 'hello world', got %q", resp.Results[0].Evidence)
	}
}

func TestRepairJSONQuotes_ChineseTextWithQuotes(t *testing.T) {
	// Real-world scenario: Chinese evidence text with unescaped quotes.
	input := `{"results": [{"criterion": "\u5305\u542b\u5929\u6c14", "passed": false, "evidence": "\u8f93\u51fa\u5185\u5bb9"\u4eca\u5929\u5929\u6c14\u70ed\u6b7b\u4e86"\u5305\u542b\u4e86\u5929\u6c14"}]}`
	var resp judgeResponse
	if err := extractJSON(input, &resp); err != nil {
		t.Fatalf("extractJSON should handle Chinese text with unescaped quotes: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
	if resp.Results[0].Passed {
		t.Fatal("expected passed=false")
	}
}

func TestRepairJSONQuotes_MultipleResults(t *testing.T) {
	input := `{"results": [
		{"criterion": "c1", "passed": true, "evidence": "found "the bug" in code"},
		{"criterion": "c2", "passed": false, "evidence": "no "fix" suggested"}
	]}`
	var resp judgeResponse
	if err := extractJSON(input, &resp); err != nil {
		t.Fatalf("extractJSON should repair multiple results: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	if !resp.Results[0].Passed {
		t.Fatal("expected first result passed=true")
	}
	if resp.Results[1].Passed {
		t.Fatal("expected second result passed=false")
	}
}
