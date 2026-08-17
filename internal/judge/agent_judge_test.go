package judge

import (
	"bytes"
	"context"
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
		testCriterionResult(0, true, "output contains 'null pointer'"),
		testCriterionResult(1, true, "no false positives found"),
		testCriterionResult(2, true, "suggests specific fix at line 42"),
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
		testCriterionResult(0, true, "yes"),
		testCriterionResult(1, true, "yes"),
		testCriterionResult(2, false, "not found"),
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
		testCriterionResult(0, false, "not found"),
		testCriterionResult(1, false, "missing"),
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
		testCriterionResult(0, true, "yes"),
		testCriterionResult(1, false, "no"),
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
		testCriterionResult(0, true, "yes"),
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
		testCriterionResult(0, true, "yes"),
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
		testCriterionResult(0, true, "yes"),
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
		testCriterionResult(0, false, "not found"),
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
		testCriterionResult(0, true, "final_message contains 'null pointer at line 42'"),
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
		testCriterionResult(0, true, "yes"),
		testCriterionResult(1, true, "yes"),
		testCriterionResult(2, true, "yes"),
		testCriterionResult(3, false, "no"),
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
		"[criterion-1] criterion A",
		"[criterion-2] criterion B",
		"\"criterion_id\"",
		"\"passed\"",
		"\"evidence\"",
		"\"failures\"",
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
		"MUST invoke the Skill tool for EACH installed judge Skill",
		`invoke Skill tool with name "judge-skill"`,
		`invoke Skill tool with name "security-judge"`,
		"read the full Skill body",
		"target: ~/.claude/skills/judge-skill",
	}
	for _, c := range checks {
		if !strings.Contains(prompt, c) {
			t.Fatalf("prompt missing %q:\n%s", c, prompt)
		}
	}
	if strings.Contains(prompt, "evals/fixtures/judge-skill") || strings.Contains(prompt, "evals/fixtures/security-judge") {
		t.Fatalf("prompt should use callable skill names instead of source paths, got:\n%s", prompt)
	}
}

func TestSkillIdentifier_PrefersCallableName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		skill SkillInfo
		want  string
	}{
		{name: "callable name", skill: SkillInfo{Name: "judge-skill", Path: "evals/fixtures/judge-skill", Source: "source"}, want: "judge-skill"},
		{name: "path fallback", skill: SkillInfo{Path: "evals/fixtures/judge-skill", Source: "source"}, want: "evals/fixtures/judge-skill"},
		{name: "source fallback", skill: SkillInfo{Source: "source"}, want: "source"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := skillIdentifier(tt.skill); got != tt.want {
				t.Fatalf("skillIdentifier() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAgentJudge_EvaluatePassesJudgeSkillsInPrompt(t *testing.T) {
	output := buildMockAgentOutput([]CriterionResult{
		testCriterionResult(0, true, "used rubric"),
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
	if !strings.Contains(ag.lastMessages[0].Content, `invoke Skill tool with name "judge-skill"`) {
		t.Fatalf("judge prompt missing callable skill name:\n%s", ag.lastMessages[0].Content)
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
		testCriterionResult(0, true, "found it"),
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
		testCriterionResult(0, true, "ok"),
		testCriterionResult(1, true, ""),
	})
	ag := &mockJudgeTestAgent{output: output}
	rt := &mockJudgeTestRuntime{}

	j := NewAgentJudge(ag, rt, "test-model", []string{"c1", "c2"}, nil, 0)
	_, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	if err == nil {
		t.Fatal("expected error for empty evidence")
	}
	if !strings.Contains(err.Error(), "evidence[0] is empty") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TimeoutSeconds wiring
// ---------------------------------------------------------------------------

func TestAgentJudge_TimeoutSeconds_AppliesDeadline(t *testing.T) {
	ag := &mockJudgeTestAgent{
		output:   buildMockAgentOutput([]CriterionResult{testCriterionResult(0, true, "ok")}),
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
	ag := &mockJudgeTestAgent{output: buildMockAgentOutput([]CriterionResult{testCriterionResult(0, true, "ok")})}
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
		output:   buildMockAgentOutput([]CriterionResult{testCriterionResult(0, true, "ok")}),
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
		output:   buildMockAgentOutput([]CriterionResult{testCriterionResult(0, true, "ok")}),
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
// Strict agent_judge response contract
// ---------------------------------------------------------------------------

func TestDecodeAgentJudgeResponse(t *testing.T) {
	valid := `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":["ok"],"failures":[]}]}`
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "plain JSON", input: valid},
		{name: "single JSON fence", input: "  \n```json\n" + valid + "\n```\n"},
		{name: "case insensitive fence tag", input: "```JSON\n" + valid + "\n```"},
		{name: "unknown root field", input: `{"results":[],"score":1}`, wantErr: true},
		{name: "unknown result field", input: `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":["ok"],"failures":[],"score":1}]}`, wantErr: true},
		{name: "wrong passed type", input: `{"results":[{"criterion_id":"criterion-1","passed":"true","evidence":["ok"],"failures":[]}]}`, wantErr: true},
		{name: "wrong evidence type", input: `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":"ok","failures":[]}]}`, wantErr: true},
		{name: "wrong failures type", input: `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":["ok"],"failures":"none"}]}`, wantErr: true},
		{name: "leading prose", input: "result: " + valid, wantErr: true},
		{name: "fence with surrounding prose", input: "result:\n```json\n" + valid + "\n```", wantErr: true},
		{name: "trailing prose", input: valid + " done", wantErr: true},
		{name: "second JSON value", input: valid + ` {"extra":true}`, wantErr: true},
		{name: "unclosed fence", input: "```json\n" + valid, wantErr: true},
		{name: "non JSON fence", input: "```text\n" + valid + "\n```", wantErr: true},
		{name: "nested fence", input: "```json\n" + valid + "\n```\n```json\n{}\n```", wantErr: true},
		{name: "malformed quotes", input: `{"results":[{"criterion_id":"criterion-1","passed":false,"evidence":["output is "wrong""],"failures":["mismatch"]}]}`, wantErr: true},
		{name: "no JSON", input: "no JSON here", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var response judgeResponse
			err := decodeAgentJudgeResponse(tt.input, &response)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestAgentJudge_NullOrMissingContractFieldsReturnError(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantErr string
	}{
		{name: "missing results", output: `{}`, wantErr: "results is required"},
		{name: "null results", output: `{"results":null}`, wantErr: "results is required"},
		{name: "null passed", output: `{"results":[{"criterion_id":"criterion-1","passed":null,"evidence":["ok"],"failures":[]}]}`, wantErr: "missing passed"},
		{name: "null evidence", output: `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":null,"failures":[]}]}`, wantErr: "missing evidence"},
		{name: "null failures", output: `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":["ok"],"failures":null}]}`, wantErr: "missing failures"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			judgeAgent := &mockJudgeTestAgent{output: tt.output}
			j := NewAgentJudge(judgeAgent, &mockJudgeTestRuntime{}, "test-model", []string{"configured"}, nil, 0)
			_, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateAgentJudgeResponse(t *testing.T) {
	valid := func() []CriterionResult {
		return []CriterionResult{
			testCriterionResult(0, true, "first evidence"),
			testCriterionResult(1, false, "second evidence"),
		}
	}
	tests := []struct {
		name    string
		results func() []CriterionResult
		wantErr string
	}{
		{name: "nil results", results: func() []CriterionResult { return nil }, wantErr: "results is required"},
		{name: "partial results", results: func() []CriterionResult { return valid()[:1] }, wantErr: "expected 2 criterion results"},
		{name: "missing id", results: func() []CriterionResult {
			results := valid()
			results[0].CriterionID = ""
			return results
		}, wantErr: "missing criterion_id"},
		{name: "unknown id", results: func() []CriterionResult {
			results := valid()
			results[0].CriterionID = "criterion-9"
			return results
		}, wantErr: "unknown criterion_id"},
		{name: "duplicate id", results: func() []CriterionResult {
			results := valid()
			results[1].CriterionID = results[0].CriterionID
			return results
		}, wantErr: "duplicate criterion_id"},
		{name: "missing passed", results: func() []CriterionResult {
			results := valid()
			results[0].Passed = nil
			return results
		}, wantErr: "missing passed"},
		{name: "missing evidence", results: func() []CriterionResult {
			results := valid()
			results[0].Evidence = nil
			return results
		}, wantErr: "missing evidence"},
		{name: "empty evidence", results: func() []CriterionResult {
			results := valid()
			results[0].Evidence = []string{}
			return results
		}, wantErr: "empty evidence"},
		{name: "blank evidence item", results: func() []CriterionResult {
			results := valid()
			results[0].Evidence = []string{" "}
			return results
		}, wantErr: "evidence[0] is empty"},
		{name: "missing failures", results: func() []CriterionResult {
			results := valid()
			results[0].Failures = nil
			return results
		}, wantErr: "missing failures"},
		{name: "passing result with failures", results: func() []CriterionResult {
			results := valid()
			results[0].Failures = []string{"unexpected"}
			return results
		}, wantErr: "passed but reported failures"},
		{name: "failed result without failures", results: func() []CriterionResult {
			results := valid()
			results[1].Failures = []string{}
			return results
		}, wantErr: "failed but reported no failures"},
		{name: "blank failure item", results: func() []CriterionResult {
			results := valid()
			results[1].Failures = []string{" "}
			return results
		}, wantErr: "failures[0] is empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateAgentJudgeResponse([]string{"first", "second"}, tt.results())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateAgentJudgeResponse_ExcessResults(t *testing.T) {
	results := []CriterionResult{
		testCriterionResult(0, true, "first"),
		testCriterionResult(1, true, "second"),
		testCriterionResult(2, true, "extra"),
	}
	_, err := validateAgentJudgeResponse([]string{"first", "second"}, results)
	if err == nil || !strings.Contains(err.Error(), "expected 2 criterion results") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAgentJudgeResponse_ReordersAndNormalizes(t *testing.T) {
	results := []CriterionResult{
		testCriterionResult(1, false, "  second evidence  "),
		testCriterionResult(0, true, "  first evidence  "),
	}
	results[0].Failures = []string{"  missing output  "}

	ordered, err := validateAgentJudgeResponse([]string{"configured first", "configured second"}, results)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ordered[0].CriterionID != "criterion-1" || ordered[1].CriterionID != "criterion-2" {
		t.Fatalf("unexpected order: %#v", ordered)
	}
	if ordered[0].Evidence[0] != "first evidence" || ordered[1].Failures[0] != "missing output" {
		t.Fatalf("results were not normalized: %#v", ordered)
	}
}

func TestAgentJudge_ConfiguredCriteriaRemainAuthoritative(t *testing.T) {
	results := []CriterionResult{
		testCriterionResult(1, false, "second observation"),
		testCriterionResult(0, true, "first observation"),
	}
	results[1].Evidence = []string{" first observation ", " additional detail "}
	results[0].Failures = []string{" missing requirement ", " wrong value "}

	session := &agent.SessionResult{FinalMessage: buildMockAgentOutput(results)}
	judgeAgent := &mockJudgeTestAgent{runResult: session}
	j := NewAgentJudge(judgeAgent, &mockJudgeTestRuntime{}, "test-model", []string{"configured first", "configured second"}, nil, 0)

	result, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AssertionResults[0].Text != "configured first" || result.AssertionResults[1].Text != "configured second" {
		t.Fatalf("configured criteria were not authoritative: %#v", result.AssertionResults)
	}
	if result.AssertionResults[0].Evidence != "first observation; additional detail" {
		t.Fatalf("unexpected passing evidence: %q", result.AssertionResults[0].Evidence)
	}
	if result.AssertionResults[1].Evidence != "second observation | Failures: missing requirement; wrong value" {
		t.Fatalf("unexpected failing evidence: %q", result.AssertionResults[1].Evidence)
	}
}

func TestAgentJudge_InvalidContractPreservesSession(t *testing.T) {
	session := &agent.SessionResult{
		FinalMessage: `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":["ok"],"failures":[],"score":1}]}`,
		Artifacts:    &agent.SessionArtifacts{GeneratedFiles: []string{"stdout.json"}},
	}
	j := NewAgentJudge(&mockJudgeTestAgent{runResult: session}, &mockJudgeTestRuntime{}, "test-model", []string{"configured"}, nil, 0)

	_, err := j.Evaluate(context.Background(), Input{FinalMessage: "test"})
	if err == nil {
		t.Fatal("expected strict contract error")
	}
	if got := SessionResultFromError(err); got != session {
		t.Fatalf("expected preserved judge session, got %#v", got)
	}
}
