package judge

// ===========================================================================
// e2e_test.go — end-to-end integration tests for the judge module
//
// Purpose:
//   This file is not a duplicate of the unit tests; instead it simulates
//   realistic scenarios where the Runner layer drives the judge module,
//   verifying the full evaluation pipeline:
//
//       Expect pre-check → Judge evaluation → Result artifact
//
//   Each test case constructs an input that is "as close as possible to a
//   real YAML configuration", describing a concrete business scenario, so
//   that by reading the test cases the reader can understand:
//     1. What the evaluation layer does
//     2. How the modules cooperate
//     3. What the resulting grading.json looks like
//
// Layout:
//   Scenario 1~3: Full pipeline (Expect + Judge interaction)
//   Scenario 4~5: Realistic business scenarios (code review / trigger test)
//   Scenario 6:   Judge polymorphism (same input, different strategies, different results)
//   Scenario 7:   grading.json serialization format validation
//   Scenario 8:   ScriptJudge real-script integration
// ===========================================================================

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// ---------------------------------------------------------------------------
// Test helper: simulate the Runner layer's Expect→Judge pipeline
// ---------------------------------------------------------------------------

// runEvalPipeline simulates the Runner's evaluation flow for a single case.
// This is exactly the core logic that runner.go will invoke:
//
//  1. Run the Expect pre-check; on failure, return FAIL immediately
//     (short-circuit; the Judge is not called).
//  2. After Expect passes, invoke Judge.Evaluate to perform the real evaluation.
//  3. Return the final grading Result.
//
// This function is the "system under test" for the e2e tests.
func runEvalPipeline(
	ctx context.Context,
	expect *config.Expect,
	judge Judge,
	input Input,
) (*Result, error) {
	// Step 1: Expect pre-check (zero-cost, deterministic).
	er := CheckExpect(expect, input)
	if !er.Passed {
		// Short-circuit: expect failed; build a FAIL Result directly from
		// the expect failure information.
		// Key semantics: the judge is NOT called here (saves LLM tokens).
		assertions := er.ToAssertionResults()
		return NewResult(assertions, input.TurnsExecuted, input.TurnsTotal), nil
	}

	// Step 2: Expect passed → enter the formal Judge evaluation.
	return judge.Evaluate(ctx, input)
}

// ---------------------------------------------------------------------------
// Scenario 1: Full pipeline — Expect passes + RuleBasedJudge passes → PASS
//
// Background: the agent successfully completes a code review whose output
// contains the expected keywords and exits with code 0.
// Expectation: Expect passes → all RuleBasedJudge success rules match → PASS.
// Why this is meaningful: verifies the forward Expect→Judge interaction and
// that Result.Summary is computed correctly.
// ---------------------------------------------------------------------------

func TestE2E_ExpectPass_RuleBasedPass(t *testing.T) {
	dir := t.TempDir()
	// Simulate the agent producing a review report file in the workspace.
	if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte("Found null pointer dereference issue"), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Configure Expect: output must contain "bug", exit code 0, review.md must exist.
	expectCfg := &config.Expect{
		MustContain: []string{"bug"},
		FilesExist:  []string{"review.md"},
	}

	// Configure Judge (rule_based): output must contain "null" and "bug",
	// and must not contain "LGTM".
	judge := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{
			{OutputContains: &config.OutputContainsRule{
				All: []string{"null", "bug"},
				Not: []string{"LGTM"},
			}},
		},
	})

	input := Input{
		CaseID:        "code-review-basic",
		FinalMessage:  "Found a null pointer bug; suggest adding a nil check at line 42",
		ExitCode:      0,
		WorkspacePath: dir,
		TurnsExecuted: 3,
		TurnsTotal:    3,
	}

	result, err := runEvalPipeline(context.Background(), expectCfg, judge, input)
	assertNoError(t, err)
	assertStatus(t, result, StatusPass)

	// Verify Summary correctly reflects the evaluation result.
	if result.Summary.Passed != 1 || result.Summary.Failed != 0 {
		t.Fatalf("expected 1 passed 0 failed, got passed=%d failed=%d",
			result.Summary.Passed, result.Summary.Failed)
	}
	if result.Summary.PassRate != 1.0 {
		t.Fatalf("expected pass_rate 1.0, got %f", result.Summary.PassRate)
	}
	// Verify turn information is propagated correctly.
	if result.TurnsExecuted != 3 || result.TurnsTotal != 3 {
		t.Fatalf("turns mismatch: executed=%d total=%d", result.TurnsExecuted, result.TurnsTotal)
	}
}

// ---------------------------------------------------------------------------
// Scenario 2: Full pipeline — Expect fails → short-circuit, judge not called
//
// Background: the agent exits with a non-zero code and the expect exit-code
// check fails.
// Expectation: Expect fails → return FAIL immediately; panicJudge is never
// called.
// Why this is meaningful: proves the "short-circuit semantics" — when expect
// fails, the judge is skipped entirely. This is the core mechanism for
// reducing token cost.
// ---------------------------------------------------------------------------

// panicJudge is a Judge implementation that must never be called.
// If Evaluate is invoked, the test panics, proving the short-circuit failed.
type panicJudge struct{}

func (p *panicJudge) Evaluate(_ context.Context, _ Input) (*Result, error) {
	panic("BUG: Judge should NOT be called when Expect fails — short-circuit broken!")
}

func TestE2E_ExpectFail_ShortCircuit_JudgeNeverCalled(t *testing.T) {
	// Expect requires exit code 2, but the actual exit code is 1, and the
	// output does not contain "success".
	expectCfg := &config.Expect{
		ExitCode:    intPtr(2),
		MustContain: []string{"success"},
	}

	input := Input{
		CaseID:       "exit-code-mismatch",
		FinalMessage: "process crashed",
		ExitCode:     1,
	}

	// Use panicJudge: panics if the Judge is called.
	result, err := runEvalPipeline(context.Background(), expectCfg, &panicJudge{}, input)
	assertNoError(t, err)
	assertStatus(t, result, StatusFail)

	// Verify the failure originates from expect, not from judge.
	if len(result.AssertionResults) < 2 {
		t.Fatalf("expected at least 2 expect failures, got %d", len(result.AssertionResults))
	}
	for _, ar := range result.AssertionResults {
		if !strings.HasPrefix(ar.Text, "expect.") {
			t.Fatalf("failure should come from expect layer, got: %s", ar.Text)
		}
		if ar.Passed {
			t.Fatal("all assertions should be failed")
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario 3: Full pipeline — Expect passes + RuleBasedJudge fails → FAIL
//
// Background: the agent exits cleanly, but the output content does not match
// the Judge's success rules.
// Expectation: Expect passes → RuleBasedJudge success rules unsatisfied → FAIL.
// Why this is meaningful: shows that the judge layer can independently mark a
// case as failed even when expect passes. The two layers do their own jobs and
// do not replace each other.
// ---------------------------------------------------------------------------

func TestE2E_ExpectPass_RuleBasedFail(t *testing.T) {
	expectCfg := &config.Expect{}

	judge := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{
			{OutputContains: &config.OutputContainsRule{All: []string{"bug", "fix"}}},
		},
	})

	input := Input{
		CaseID:       "output-incomplete",
		FinalMessage: "Review complete, found one bug",
		ExitCode:     0,
	}

	result, err := runEvalPipeline(context.Background(), expectCfg, judge, input)
	assertNoError(t, err)
	assertStatus(t, result, StatusFail)

	// The failure reason should be that "fix" is missing from output_contains.all.
	if len(result.AssertionResults) != 1 {
		t.Fatalf("expected 1 assertion, got %d", len(result.AssertionResults))
	}
	ar := result.AssertionResults[0]
	if ar.Passed {
		t.Fatal("assertion should be failed")
	}
	if !strings.Contains(ar.Evidence, "fix") {
		t.Fatalf("evidence should mention missing keyword 'fix', got: %s", ar.Evidence)
	}
}

// ---------------------------------------------------------------------------
// Scenario 4: Realistic business — multi-turn code review + tool calls + multiple rules
//
// Simulated scenario: the agent performs a code review task.
//   - Turn 1: user submits code
//   - Turn 1: agent invokes the read_file tool to read the code
//   - Turn 1: agent identifies a null-pointer issue
//   - Turn 2: user asks for a fix suggestion
//   - Turn 2: agent provides a concrete fix
//
// Judge configuration:
//   - Success[0]: output contains both "null" and "bug" (all)
//   - Success[1]: agent invoked the read_file tool
//   - Success[2]: turn-2 response contains a fix suggestion
//   - Failure[0]: output must not contain "LGTM" (a generic "looks good"
//     reply should fail the case)
//
// Why this is meaningful: this is a complete scenario close to a real
// eval.yaml configuration; it verifies the AND logic across multiple success
// rules, the priority of failure rules, correct transcript parsing for the
// tool_called rule, and turn-granular assertions.
// ---------------------------------------------------------------------------

func TestE2E_RealisticCodeReview_MultiRule(t *testing.T) {
	// Build a multi-turn Transcript.
	tr := transcript.Transcript{
		// Turn 1: user submits the code review request.
		{Role: transcript.RoleUser, Turn: 1, Content: "Please review this Go code"},
		// Turn 1: agent invokes the read_file tool.
		{Role: transcript.RoleToolCall, Turn: 1, ToolCall: &transcript.ToolCallInfo{
			ID:   "call_001",
			Name: "read_file",
			Arguments: map[string]any{
				"path": "main.go",
			},
		}},
		// Turn 1: tool returns the source content.
		{Role: transcript.RoleToolResult, Turn: 1, ToolResult: &transcript.ToolResultInfo{
			CallID:  "call_001",
			Status:  "success",
			Content: "func process(p *User) { fmt.Println(p.Name) }",
		}},
		// Turn 1: agent analyzes and reports the null-pointer issue.
		{
			Role: transcript.RoleAssistant, Turn: 1,
			Content: "Found a null pointer bug: function process does not check if parameter p is nil",
		},
		// Turn 2: user asks for a fix suggestion.
		{Role: transcript.RoleUser, Turn: 2, Content: "How should this be fixed?"},
		// Turn 2: agent provides the fix suggestion.
		{
			Role: transcript.RoleAssistant, Turn: 2,
			Content: "Suggest adding a nil check at the function entry: if p == nil { return }",
		},
	}

	// Configure Expect (lightweight gatekeeper).
	expectCfg := &config.Expect{
		MustContain: []string{"nil"},
	}

	// Configure Judge (rule_based, multiple rules with AND logic).
	exitCode := 0
	judge := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{
			// Rule 1: final output contains key information.
			{OutputContains: &config.OutputContainsRule{All: []string{"nil"}}},
			// Rule 2: agent must have invoked the read_file tool.
			{ToolCalled: &config.ToolCalledRule{Name: "read_file"}},
			// Rule 3: exit code is 0.
			{ExitCode: &exitCode},
		},
		Failure: []config.Rule{
			// Failure rule: any "LGTM"-style reply should fail the case.
			{OutputContains: &config.OutputContainsRule{Any: []string{"LGTM", "no changes needed"}}},
		},
	})

	input := Input{
		CaseID:        "code-review-null-check",
		Transcript:    tr,
		FinalMessage:  tr.FinalAssistantMessage(), // "Suggest adding a nil check at the function entry: if p == nil { return }"
		ExitCode:      0,
		TurnsExecuted: 2,
		TurnsTotal:    2,
	}

	result, err := runEvalPipeline(context.Background(), expectCfg, judge, input)
	assertNoError(t, err)
	assertStatus(t, result, StatusPass)

	// Verify all 3 success rules pass.
	if result.Summary.Total != 3 {
		t.Fatalf("expected 3 assertions, got %d", result.Summary.Total)
	}
	if result.Summary.Passed != 3 {
		t.Fatalf("expected all 3 passed, got %d passed %d failed",
			result.Summary.Passed, result.Summary.Failed)
	}
	if result.Summary.PassRate != 1.0 {
		t.Fatalf("expected pass_rate 1.0, got %f", result.Summary.PassRate)
	}

	// Verify each assertion has meaningful evidence.
	for i, ar := range result.AssertionResults {
		if ar.Evidence == "" {
			t.Fatalf("assertion %d (%s) has empty evidence", i, ar.Text)
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario 5: Realistic business — failure-rule priority (trigger test)
//
// Simulated scenario: the agent's output simultaneously satisfies a success
// rule and a failure rule.
//   - Output contains "review" (success rule would pass)
//   - But also contains "LGTM" (failure rule would hit)
//
// Expectation: failure rules have higher priority — even if a success rule
// would otherwise pass, any matched failure rule causes the overall result to
// be FAIL.
//
// Why this is meaningful: verifies the core design decision of
// RuleBasedJudge — failure short-circuit takes priority. This prevents the
// agent from "cheating" with generic catch-all replies such as "LGTM".
// ---------------------------------------------------------------------------

func TestE2E_FailureRulePriority_OverridesSuccess(t *testing.T) {
	expectCfg := &config.Expect{}

	judge := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{
			{OutputContains: &config.OutputContainsRule{All: []string{"review"}}},
		},
		Failure: []config.Rule{
			{OutputContains: &config.OutputContainsRule{Any: []string{"LGTM", "no issues"}}},
		},
	})

	input := Input{
		CaseID:       "trigger-test-lgtm",
		FinalMessage: "LGTM, review looks good",
		ExitCode:     0,
	}

	result, err := runEvalPipeline(context.Background(), expectCfg, judge, input)
	assertNoError(t, err)
	assertStatus(t, result, StatusFail)

	// Verify the failure originates from the failure rule.
	if len(result.AssertionResults) == 0 {
		t.Fatal("expected at least 1 assertion from failure rule")
	}
	ar := result.AssertionResults[0]
	if !strings.HasPrefix(ar.Text, "failure:") {
		t.Fatalf("expected failure rule assertion, got: %s", ar.Text)
	}
	if ar.Passed {
		t.Fatal("failure rule assertion should be marked as not-passed")
	}
	if !strings.Contains(ar.Evidence, "failure rule matched") {
		t.Fatalf("evidence should indicate failure rule matched, got: %s", ar.Evidence)
	}
}

// ---------------------------------------------------------------------------
// Scenario 6: Judge polymorphism — same Input, different strategies, different results
//
// Background: the same agent output can be evaluated by different strategies.
// Goal: prove the polymorphism of the Judge interface — RuleBasedJudge and
// AgentJudge accept the same Input but produce different results based on
// their own strategies.
//
// Why this is meaningful: verifies the pluggable design of the evaluation
// layer — the same data can be processed by different evaluators, and every
// evaluator's output follows the unified Result format.
// ---------------------------------------------------------------------------

func TestE2E_JudgePolymorphism_SameInputDifferentStrategies(t *testing.T) {
	input := Input{
		CaseID:        "polymorphism-test",
		FinalMessage:  "Found a null pointer bug, but did not provide a specific fix",
		ExitCode:      0,
		TurnsExecuted: 2,
		TurnsTotal:    3,
	}

	// Strategy A: rule_based — lenient (any output containing "bug" passes).
	lenientJudge := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{
			{OutputContains: &config.OutputContainsRule{All: []string{"bug"}}},
		},
	})

	// Strategy B: rule_based — strict (must contain both "bug" and "fix suggestion").
	strictJudge := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{
			{OutputContains: &config.OutputContainsRule{All: []string{"bug", "fix suggestion"}}},
		},
	})

	// Strategy C: agent_judge — uses a mock Agent (simulating partial pass).
	agentOutput := buildMockAgentOutput([]CriterionResult{
		{Criterion: "identified the bug", Passed: true, Evidence: "output explicitly mentions null pointer bug"},
		{Criterion: "provided fix suggestion", Passed: false, Evidence: "output says 'no specific fix provided'"},
	})
	mockAg := &mockJudgeTestAgent{output: agentOutput}
	mockRt := &mockJudgeTestRuntime{}
	agentJudge := NewAgentJudge(mockAg, mockRt, "gpt-5.4", []string{"identified the bug", "provided fix suggestion"}, nil)

	ctx := context.Background()

	// Run all three strategies.
	resultA, err := lenientJudge.Evaluate(ctx, input)
	assertNoError(t, err)
	assertStatus(t, resultA, StatusPass)

	resultB, err := strictJudge.Evaluate(ctx, input)
	assertNoError(t, err)
	assertStatus(t, resultB, StatusFail)

	resultC, err := agentJudge.Evaluate(ctx, input)
	assertNoError(t, err)
	assertStatus(t, resultC, StatusFail) // 1/2 = 0.5 < 0.7 threshold

	// Verify the Result format is identical across strategies.
	for name, r := range map[string]*Result{
		"lenient": resultA, "strict": resultB, "agent": resultC,
	} {
		if r.Status == "" {
			t.Fatalf("[%s] status should not be empty", name)
		}
		if r.TurnsExecuted != 2 || r.TurnsTotal != 3 {
			t.Fatalf("[%s] turns mismatch: executed=%d total=%d", name, r.TurnsExecuted, r.TurnsTotal)
		}
		// Every strategy should produce at least 1 assertion.
		if len(r.AssertionResults) == 0 {
			t.Fatalf("[%s] expected at least 1 assertion result", name)
		}
		// Every assertion must have Text and Evidence.
		for i, ar := range r.AssertionResults {
			if ar.Text == "" {
				t.Fatalf("[%s] assertion %d has empty text", name, i)
			}
			if ar.Evidence == "" {
				t.Fatalf("[%s] assertion %d has empty evidence", name, i)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario 7: grading.json serialization format validation
//
// Goal: verify that Result, when serialized to JSON, matches the grading.json
// schema defined by the design doc. This is the core data contract for
// evaluation artifacts.
//
// Why this is meaningful: other systems (CI, dashboards, benchmark
// aggregation) all rely on the precise format of grading.json. This test
// ensures the serialized artifact contains every required field with the
// right names and types (in particular pass_rate as a float and skip_reason
// as null).
// ---------------------------------------------------------------------------

func TestE2E_GradingJSON_SerializationFormat(t *testing.T) { //nolint:cyclop,gocyclo // e2e test requires many serialisation-shape assertions
	// Simulate a partially passed evaluation result.
	result := NewResult([]AssertionResult{
		{Text: "output_contains{all:[null bug]}", Passed: true, Evidence: "all keywords found"},
		{Text: "exit_code: 0", Passed: true, Evidence: "exit_code is 0 as expected"},
		{Text: "tool_called: read_file", Passed: false, Evidence: "tool \"read_file\" was not called"},
	}, 3, 5)

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}
	content := string(data)

	// --- Verify all required fields exist. ---

	requiredFields := []string{
		`"status"`,
		`"turns_executed"`,
		`"turns_total"`,
		`"assertion_results"`,
		`"summary"`,
	}
	for _, field := range requiredFields {
		if !strings.Contains(content, field) {
			t.Fatalf("grading.json missing required field %s\n%s", field, content)
		}
	}

	// skip_reason and error_reason should be omitted for non-skip/non-error results.
	if strings.Contains(content, `"skip_reason"`) {
		t.Fatalf("non-skip result should not contain skip_reason\n%s", content)
	}
	if strings.Contains(content, `"error_reason"`) {
		t.Fatalf("non-error result should not contain error_reason\n%s", content)
	}

	// --- Verify each element in assertion_results contains text/passed/evidence. ---

	assertionFields := []string{`"text"`, `"passed"`, `"evidence"`}
	for _, field := range assertionFields {
		if !strings.Contains(content, field) {
			t.Fatalf("assertion_results missing field %s\n%s", field, content)
		}
	}

	// --- Verify summary contains the statistics fields. ---

	summaryFields := []string{`"passed"`, `"failed"`, `"total"`, `"pass_rate"`}
	for _, field := range summaryFields {
		if !strings.Contains(content, field) {
			t.Fatalf("summary missing field %s\n%s", field, content)
		}
	}

	// --- Verify the JSON can be deserialized back. ---

	var parsed Result
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}

	// Verify the round-tripped values.
	if parsed.Status != StatusFail {
		t.Fatalf("expected FAIL status (2 passed, 1 failed), got %s", parsed.Status)
	}
	if parsed.SkipReason != nil {
		t.Fatalf("skip_reason should be null for non-skip result, got %v", parsed.SkipReason)
	}
	if parsed.TurnsExecuted != 3 || parsed.TurnsTotal != 5 {
		t.Fatalf("turns mismatch after roundtrip")
	}
	if parsed.Summary.Passed != 2 || parsed.Summary.Failed != 1 || parsed.Summary.Total != 3 {
		t.Fatalf("summary mismatch: %+v", parsed.Summary)
	}
	// pass_rate should be 2/3 ≈ 0.6667.
	if parsed.Summary.PassRate < 0.666 || parsed.Summary.PassRate > 0.667 {
		t.Fatalf("expected pass_rate ~0.6667, got %f", parsed.Summary.PassRate)
	}
}

// TestE2E_GradingJSON_SkipResult_Format verifies the serialization format of
// the SKIP status.
// skip_reason should be a non-null string, not null.
func TestE2E_GradingJSON_SkipResult_Format(t *testing.T) {
	result := NewSkipResult("post_condition not met", 1, 3)
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, `"status": "SKIP"`) {
		t.Fatalf("expected SKIP status in json\n%s", content)
	}
	if !strings.Contains(content, "post_condition not met") {
		t.Fatalf("expected skip_reason in json\n%s", content)
	}
	// assertion_results should be either an empty array or null.
	var parsed Result
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(parsed.AssertionResults) != 0 {
		t.Fatalf("skip result should have 0 assertions, got %d", len(parsed.AssertionResults))
	}
}

// ---------------------------------------------------------------------------
// Scenario 8: ScriptJudge real-script end-to-end
//
// Simulated scenario: ScriptJudge runs a real evaluation script.
// The script reads the EVAL_FINAL_MESSAGE environment variable and checks
// whether it contains the expected keyword.
//
// Why this is meaningful: verifies the full ScriptJudge contract — env-var
// injection, working-directory setup, exit-code semantics, and stdout being
// used as evidence. This is the integration point between the script judge
// and the runner.
// ---------------------------------------------------------------------------

func TestE2E_ScriptJudge_FullPipeline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping script test on windows")
	}

	dir := t.TempDir()

	// Create the evaluation script: check whether EVAL_FINAL_MESSAGE
	// contains "bug".
	scriptContent := `#!/bin/sh
# Evaluation script: check whether the agent output identifies the bug.
# Reads the EVAL_FINAL_MESSAGE env var (injected by ScriptJudge).
if echo "$EVAL_FINAL_MESSAGE" | grep -q "bug"; then
  echo "Agent correctly identified the bug"
  exit 0
else
  echo "Agent failed to identify the bug"
  exit 1
fi
`
	scriptPath := writeScript(t, dir, "eval_check.sh", scriptContent)

	expectCfg := &config.Expect{}

	scriptJudge := &ScriptJudge{
		ScriptPath:     scriptPath,
		TranscriptPath: filepath.Join(dir, "transcript.json"),
	}
	if err := os.WriteFile(scriptJudge.TranscriptPath, []byte(`[]`), 0o600); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	// Case A: agent output contains "bug" → PASS.
	inputPass := Input{
		CaseID:        "script-e2e-pass",
		FinalMessage:  "Found a null pointer bug",
		ExitCode:      0,
		WorkspacePath: dir,
		TurnsExecuted: 2,
		TurnsTotal:    2,
	}
	result, err := runEvalPipeline(context.Background(), expectCfg, scriptJudge, inputPass)
	assertNoError(t, err)
	assertStatus(t, result, StatusPass)
	if !strings.Contains(result.AssertionResults[0].Evidence, "correctly identified") {
		t.Fatalf("expected script stdout as evidence, got: %s", result.AssertionResults[0].Evidence)
	}

	// Case B: agent output does not contain "bug" → FAIL.
	inputFail := Input{
		CaseID:        "script-e2e-fail",
		FinalMessage:  "The code looks fine",
		ExitCode:      0,
		WorkspacePath: dir,
		TurnsExecuted: 2,
		TurnsTotal:    2,
	}
	result2, err := runEvalPipeline(context.Background(), expectCfg, scriptJudge, inputFail)
	assertNoError(t, err)
	assertStatus(t, result2, StatusFail)
	if !strings.Contains(result2.AssertionResults[0].Evidence, "failed to identify") {
		t.Fatalf("expected failure evidence from script, got: %s", result2.AssertionResults[0].Evidence)
	}
}

// ---------------------------------------------------------------------------
// Scenario 9: AgentJudge full pipeline + Expect interaction
//
// Simulated scenario: after Expect passes, AgentJudge (Agent-as-Judge)
// performs the evaluation. The mock agent returns a partial-pass result to
// verify the pass_threshold logic.
//
// Why this is meaningful: verifies the full Expect + AgentJudge interaction
// and the core threshold-driven status decision.
// ---------------------------------------------------------------------------

func TestE2E_AgentJudge_WithExpect_ThresholdDecision(t *testing.T) {
	expectCfg := &config.Expect{
		MustContain: []string{"bug"},
	}

	// Mock Agent: 2 of 3 criteria passed → pass_rate = 2/3 ≈ 0.667.
	agentOutput := buildMockAgentOutput([]CriterionResult{
		{Criterion: "identified the bug type", Passed: true, Evidence: "output mentions null pointer"},
		{Criterion: "located the bug position", Passed: true, Evidence: "output mentions line 42"},
		{Criterion: "provided fix suggestion", Passed: false, Evidence: "output did not provide fix code"},
	})
	mockAg := &mockJudgeTestAgent{output: agentOutput}
	mockRt := &mockJudgeTestRuntime{}

	input := Input{
		CaseID:        "agent-judge-e2e",
		FinalMessage:  "Found null pointer bug at line 42",
		ExitCode:      0,
		TurnsExecuted: 3,
		TurnsTotal:    3,
	}

	// threshold 0.7 → 0.667 < 0.7 → FAIL.
	judge1 := NewAgentJudge(mockAg, mockRt, "gpt-5.4",
		[]string{"identified the bug type", "located the bug position", "provided fix suggestion"}, nil)
	result1, err := runEvalPipeline(context.Background(), expectCfg, judge1, input)
	assertNoError(t, err)
	assertStatus(t, result1, StatusFail)
	if result1.Summary.Passed != 2 || result1.Summary.Failed != 1 {
		t.Fatalf("expected 2 passed 1 failed, got %+v", result1.Summary)
	}

	// threshold 0.6 → 0.667 >= 0.6 → PASS.
	threshold := 0.6
	judge2 := NewAgentJudge(mockAg, mockRt, "gpt-5.4",
		[]string{"identified the bug type", "located the bug position", "provided fix suggestion"}, &threshold)
	result2, err := runEvalPipeline(context.Background(), expectCfg, judge2, input)
	assertNoError(t, err)
	assertStatus(t, result2, StatusPass)

	// Verify evidence is preserved.
	for _, ar := range result2.AssertionResults {
		if ar.Evidence == "" {
			t.Fatalf("evidence should not be empty for criterion: %s", ar.Text)
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario 10: Expect + filesystem checks + golden_file
//
// Simulated scenario: the agent is asked to generate an output file in a
// specific format; the Expect layer simultaneously checks file existence,
// file content, and a golden-file match.
//
// Why this is meaningful: verifies cooperation between filesystem-related
// expect rules in a real workspace, and covers the "golden_file is relative
// to skill root" resolution semantics.
// ---------------------------------------------------------------------------

func TestE2E_Expect_FileSystemChecks_GoldenFile(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "sample-skill")
	if err := os.MkdirAll(filepath.Join(skillDir, "evals", "fixtures", "golden"), 0o755); err != nil {
		t.Fatalf("failed to create golden dir: %v", err)
	}
	dir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create workspace dir: %v", err)
	}

	// Simulate the file produced by the agent.
	if err := os.WriteFile(filepath.Join(dir, "output.json"), []byte(`{"count": 42, "language": "Go"}`), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	// Golden file (expected output).
	if err := os.WriteFile(filepath.Join(skillDir, "evals", "fixtures", "golden", "expected.json"), []byte(`{"count": 42, "language": "Go"}`), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	expectCfg := &config.Expect{
		FilesExist:    []string{"output.json"},
		FilesNotExist: []string{"temp.log", "debug.txt"},
		GoldenFile:    "evals/fixtures/golden/expected.json",
	}

	// Use the golden-file content as FinalMessage (simulating that the
	// agent output is exactly the file content).
	input := Input{
		CaseID:        "golden-file-e2e",
		FinalMessage:  `{"count": 42, "language": "Go"}`,
		SkillDir:      skillDir,
		WorkspacePath: dir,
	}

	// Use a minimal Judge (no rules → defaults to PASS).
	trivialJudge := NewRuleBasedJudge(config.JudgeConfig{})

	result, err := runEvalPipeline(context.Background(), expectCfg, trivialJudge, input)
	assertNoError(t, err)
	assertStatus(t, result, StatusPass)
}

// TestE2E_Expect_GoldenFile_Mismatch verifies the failure behavior when the
// golden file does not match.
func TestE2E_Expect_GoldenFile_Mismatch(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "sample-skill")
	if err := os.MkdirAll(filepath.Join(skillDir, "evals", "fixtures", "golden"), 0o755); err != nil {
		t.Fatalf("failed to create golden dir: %v", err)
	}
	dir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create workspace dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "evals", "fixtures", "golden", "expected.json"), []byte(`{"count": 42}`), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	expectCfg := &config.Expect{
		GoldenFile: "evals/fixtures/golden/expected.json",
	}

	input := Input{
		CaseID:        "golden-mismatch",
		FinalMessage:  `{"count": 99}`,
		SkillDir:      skillDir,
		WorkspacePath: dir,
	}

	result, err := runEvalPipeline(context.Background(), expectCfg, &panicJudge{}, input)
	assertNoError(t, err)
	assertStatus(t, result, StatusFail)

	// Verify the failure reason includes diff information.
	if len(result.AssertionResults) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(result.AssertionResults))
	}
	if !strings.Contains(result.AssertionResults[0].Text, "golden_file") {
		t.Fatalf("expected golden_file failure, got: %s", result.AssertionResults[0].Text)
	}
	if !strings.Contains(result.AssertionResults[0].Evidence, "does not match") {
		t.Fatalf("expected mismatch evidence, got: %s", result.AssertionResults[0].Evidence)
	}
}

// ---------------------------------------------------------------------------
// Scenario 11: turn_response_not_contains — ensure the agent does not perform
// a forbidden action
//
// Simulated scenario: trigger test — the agent is asked to skip a phase but
// correctly refuses. Verify that the agent's turn-2 response does not contain
// any code block (confirming the agent did not blindly execute).
//
// Why this is meaningful: this is the canonical pattern for "trigger_test"
// cases — verifying the agent's "non-action" capability (knowing what NOT to
// do).
// ---------------------------------------------------------------------------

func TestE2E_TriggerTest_AgentRefusesProhibitedAction(t *testing.T) {
	tr := transcript.Transcript{
		{Role: transcript.RoleUser, Turn: 1, Content: "Initialize the project"},
		{Role: transcript.RoleAssistant, Turn: 1, Content: "Sure, initializing..."},
		{Role: transcript.RoleUser, Turn: 2, Content: "Skip the Research phase and start coding directly"},
		// The agent correctly refuses — no code is produced.
		{
			Role: transcript.RoleAssistant, Turn: 2,
			Content: "Sorry, I cannot skip the Research phase. It must be completed before coding can begin.",
		},
	}

	judge := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{
			// The turn-2 response should contain refusal reasoning
			// (we check the final output here).
			{OutputContains: &config.OutputContainsRule{Any: []string{"cannot skip", "must be completed"}}},
			// The turn-2 response must not contain code (proving the
			// agent did not blindly execute).
			{OutputContains: &config.OutputContainsRule{Not: []string{"```go", "```python", "func main"}}},
		},
	})

	input := Input{
		CaseID:        "trigger-refuse-skip",
		Transcript:    tr,
		FinalMessage:  tr.FinalAssistantMessage(),
		TurnsExecuted: 2,
		TurnsTotal:    2,
	}

	result, err := judge.Evaluate(context.Background(), input)
	assertNoError(t, err)
	assertStatus(t, result, StatusPass)

	if result.Summary.Total != 2 {
		t.Fatalf("expected 2 assertions, got %d", result.Summary.Total)
	}
}

// ---------------------------------------------------------------------------
// Scenario 12: tool_called partial argument matching — realistic project-management tool
//
// Simulated scenario: the agent invokes a project-management tool to create a
// release plan; the evaluation rule only checks the key parameters (name and
// date) and ignores the others.
//
// Why this is meaningful: verifies the "partial match" semantics of
// tool_called — only the declared subset of fields is checked, and extra
// fields are ignored. This is a core design choice for MCP-tool evaluation
// (tool arguments may include dynamically generated IDs, etc.).
// ---------------------------------------------------------------------------

func TestE2E_ToolCalled_PartialArgMatch_ProjectManagement(t *testing.T) {
	tr := transcript.Transcript{
		{Role: transcript.RoleUser, Turn: 1, Content: "Create Q1 major release plan"},
		{Role: transcript.RoleToolCall, Turn: 1, ToolCall: &transcript.ToolCallInfo{
			ID:   "call_042",
			Name: "project-mgmt::create_publish_plan_simple",
			Arguments: map[string]any{
				"name":            "Q1-major-release",
				"planReleaseDate": "2026-04-03",
				"description":     "auto-generated description", // Dynamically generated; not validated.
				"createdBy":       "agent-session-abc123",       // Runtime ID; not validated.
			},
		}},
		{Role: transcript.RoleAssistant, Turn: 1, Content: "Release plan Q1-major-release has been created"},
	}

	judge := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{
			{ToolCalled: &config.ToolCalledRule{
				Name: "project-mgmt::create_publish_plan_simple",
				Args: map[string]any{
					"name":            "Q1-major-release",
					"planReleaseDate": "2026-04-03",
					// Note: description and createdBy are intentionally not checked.
				},
			}},
		},
	})

	input := Input{
		CaseID:     "tool-partial-match",
		Transcript: tr,
	}

	result, err := judge.Evaluate(context.Background(), input)
	assertNoError(t, err)
	assertStatus(t, result, StatusPass)

	// Verify the evidence confirms the parameter match.
	if !strings.Contains(result.AssertionResults[0].Evidence, "matching args") {
		t.Fatalf("expected partial match evidence, got: %s", result.AssertionResults[0].Evidence)
	}
}
