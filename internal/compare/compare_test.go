package compare

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
)

func compareFixture() (oldInput, newInput report.Input) {
	start := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	oldInput = report.Input{
		SkillName:     "skill-a",
		SchemaVersion: "v1alpha1",
		EngineName:    "codex",
		ModelName:     "gpt-5",
		StartTime:     start,
		EndTime:       start.Add(2 * time.Minute),
		TotalTokens:   100,
		CaseResults: []report.CaseResult{
			{CaseID: "case-1", Title: "Case One baseline", Status: judge.StatusFail, Configuration: "without_skill", InputTokens: 10, OutputTokens: 5},
			{CaseID: "case-1", Title: "Case One", Status: judge.StatusPass, Configuration: "with_skill", InputTokens: 20, OutputTokens: 10},
			{CaseID: "case-2", Title: "Case Two", Status: judge.StatusFail, InputTokens: 30, OutputTokens: 15},
		},
	}
	newInput = report.Input{
		SkillName:     "skill-a",
		SchemaVersion: "v1alpha1",
		EngineName:    "codex",
		ModelName:     "gpt-5.1",
		StartTime:     start.Add(24 * time.Hour),
		EndTime:       start.Add(24*time.Hour + 3*time.Minute),
		TotalTokens:   140,
		CaseResults: []report.CaseResult{
			{CaseID: "case-1", Title: "Case One", Status: judge.StatusPass, Configuration: "with_skill", InputTokens: 25, OutputTokens: 12},
			{CaseID: "case-2", Title: "Case Two", Status: judge.StatusPass, InputTokens: 35, OutputTokens: 18},
		},
	}
	return oldInput, newInput
}

func TestCompareRunMetricsUsePrimaryCaseResults(t *testing.T) {
	t.Parallel()
	oldInput, newInput := compareFixture()

	result := Compare(oldInput, newInput, Options{})

	if result.Run.Old.CaseCount != 2 {
		t.Fatalf("old case count should use primary results, got %d", result.Run.Old.CaseCount)
	}
	if result.Run.Old.PassCount != 1 {
		t.Fatalf("old pass count should ignore without_skill baseline failure, got %d", result.Run.Old.PassCount)
	}
	if math.Abs(result.Run.Old.PassRate-0.5) > 0.001 {
		t.Fatalf("old pass rate: want 0.5, got %f", result.Run.Old.PassRate)
	}
	if result.Run.Old.InputTokens != 50 || result.Run.Old.OutputTokens != 25 {
		t.Fatalf("old tokens should sum primary results, got input=%d output=%d", result.Run.Old.InputTokens, result.Run.Old.OutputTokens)
	}
	if result.Run.Old.TotalTokens != 100 || result.Run.New.TotalTokens != 140 || result.Run.Delta.TotalTokens != 40 {
		t.Fatalf("total token delta mismatch: old=%d new=%d delta=%d", result.Run.Old.TotalTokens, result.Run.New.TotalTokens, result.Run.Delta.TotalTokens)
	}
	if result.Run.Old.DurationMs != 120000 || result.Run.New.DurationMs != 180000 || result.Run.Delta.DurationMs != 60000 {
		t.Fatalf("duration delta mismatch: old=%d new=%d delta=%d", result.Run.Old.DurationMs, result.Run.New.DurationMs, result.Run.Delta.DurationMs)
	}
}

func TestCompareClassifiesCaseTransitionsInDeterministicOrder(t *testing.T) {
	t.Parallel()
	oldInput := report.Input{CaseResults: []report.CaseResult{
		{CaseID: "fixed", Title: "Fixed old", Status: judge.StatusFail},
		{CaseID: "regressed", Title: "Regressed old", Status: judge.StatusPass},
		{CaseID: "status-changed", Title: "Status changed old", Status: judge.StatusError},
		{CaseID: "unchanged", Title: "Unchanged old", Status: judge.StatusError},
		{CaseID: "removed", Title: "Removed", Status: judge.StatusFail},
	}}
	newInput := report.Input{CaseResults: []report.CaseResult{
		{CaseID: "regressed", Title: "Regressed new", Status: judge.StatusFail},
		{CaseID: "fixed", Title: "Fixed new", Status: judge.StatusPass},
		{CaseID: "status-changed", Title: "Status changed new", Status: judge.StatusSkip},
		{CaseID: "unchanged", Title: "Unchanged new", Status: judge.StatusError},
		{CaseID: "added-first", Title: "Added first", Status: judge.StatusPass},
		{CaseID: "added-second", Title: "Added second", Status: judge.StatusFail},
	}}

	got := Compare(oldInput, newInput, Options{}).Cases
	want := CaseTransitions{
		Fixed:     []CaseTransition{{CaseID: "fixed", OldTitle: "Fixed old", NewTitle: "Fixed new", OldStatus: judge.StatusFail, NewStatus: judge.StatusPass}},
		Regressed: []CaseTransition{{CaseID: "regressed", OldTitle: "Regressed old", NewTitle: "Regressed new", OldStatus: judge.StatusPass, NewStatus: judge.StatusFail}},
		Changed:   []CaseTransition{{CaseID: "status-changed", OldTitle: "Status changed old", NewTitle: "Status changed new", OldStatus: judge.StatusError, NewStatus: judge.StatusSkip}},
		Unchanged: []CaseTransition{{CaseID: "unchanged", OldTitle: "Unchanged old", NewTitle: "Unchanged new", OldStatus: judge.StatusError, NewStatus: judge.StatusError}},
		Added: []CaseTransition{
			{CaseID: "added-first", NewTitle: "Added first", NewStatus: judge.StatusPass},
			{CaseID: "added-second", NewTitle: "Added second", NewStatus: judge.StatusFail},
		},
		Removed: []CaseTransition{{CaseID: "removed", OldTitle: "Removed", OldStatus: judge.StatusFail}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("case transitions mismatch:\nwant: %#v\n got: %#v", want, got)
	}
}

func TestCompareIncludesMetadataDiff(t *testing.T) {
	t.Parallel()
	oldInput, newInput := compareFixture()

	got := Compare(oldInput, newInput, Options{}).Metadata
	if got.SkillName.Changed {
		t.Fatal("skill name should be unchanged")
	}
	if !got.ModelName.Changed || !got.StartTime.Changed || !got.EndTime.Changed {
		t.Fatalf("changed metadata mismatch: model=%t start=%t end=%t", got.ModelName.Changed, got.StartTime.Changed, got.EndTime.Changed)
	}
}

func TestCompareFailsRegressionAndTokenIncreaseGates(t *testing.T) {
	t.Parallel()
	limit := 20.0
	oldInput := report.Input{TotalTokens: 100, CaseResults: []report.CaseResult{{CaseID: "case-1", Status: judge.StatusPass}}}
	newInput := report.Input{TotalTokens: 140, CaseResults: []report.CaseResult{{CaseID: "case-1", Status: judge.StatusFail}}}

	got := Compare(oldInput, newInput, Options{FailOnRegression: true, MaxTokenIncreasePercent: &limit}).Gates
	if got.Passed || len(got.Failures) != 2 {
		t.Fatalf("expected both gates to fail, got %#v", got)
	}
}

func TestCompareDoesNotFailRegressionGateForChangedNonPassStatus(t *testing.T) {
	t.Parallel()
	oldInput := report.Input{CaseResults: []report.CaseResult{{CaseID: "case-1", Status: judge.StatusError}}}
	newInput := report.Input{CaseResults: []report.CaseResult{{CaseID: "case-1", Status: judge.StatusSkip}}}

	result := Compare(oldInput, newInput, Options{FailOnRegression: true})
	if !result.Gates.Passed || len(result.Gates.Failures) != 0 {
		t.Fatalf("changed non-PASS status should not fail regression gate, got %#v", result.Gates)
	}
	if len(result.Cases.Changed) != 1 {
		t.Fatalf("changed transitions = %#v, want one ERROR -> SKIP transition", result.Cases.Changed)
	}
}

func TestCompareFailsRegressionGateAboveConfiguredMaximum(t *testing.T) {
	t.Parallel()
	maxRegressions := 1
	oldInput := report.Input{CaseResults: []report.CaseResult{
		{CaseID: "case-1", Status: judge.StatusPass},
		{CaseID: "case-2", Status: judge.StatusPass},
	}}
	newInput := report.Input{CaseResults: []report.CaseResult{
		{CaseID: "case-1", Status: judge.StatusFail},
		{CaseID: "case-2", Status: judge.StatusError},
	}}

	got := Compare(oldInput, newInput, Options{MaxRegressions: &maxRegressions}).Gates
	if got.Passed || len(got.Failures) != 1 || !strings.Contains(got.Failures[0], "2 regressions exceeds maximum 1") {
		t.Fatalf("expected regression maximum gate failure, got %#v", got)
	}
}

func TestCompareFailsTokenGateWhenOldTotalTokensAreZero(t *testing.T) {
	t.Parallel()
	limit := 20.0
	oldInput := report.Input{TotalTokens: 0}
	newInput := report.Input{TotalTokens: 1, CaseResults: []report.CaseResult{{CaseID: "case-1"}}}

	got := Compare(oldInput, newInput, Options{MaxTokenIncreasePercent: &limit}).Gates
	if got.Passed || len(got.Failures) != 1 || !strings.Contains(got.Failures[0], "old total tokens is 0") {
		t.Fatalf("expected zero-token gate failure, got %#v", got)
	}
}

func TestCompareTokenGateUsesTotalTokens(t *testing.T) {
	t.Parallel()
	limit := 10.0
	oldInput := report.Input{TotalTokens: 200, CaseResults: []report.CaseResult{
		{CaseID: "case-1"},
		{CaseID: "case-2"},
	}}
	newInput := report.Input{TotalTokens: 150, CaseResults: []report.CaseResult{{CaseID: "case-1"}}}

	got := Compare(oldInput, newInput, Options{MaxTokenIncreasePercent: &limit}).Gates
	if !got.Passed || len(got.Failures) != 0 {
		t.Fatalf("expected total token gate to pass, got %#v", got)
	}
}

func TestRenderTextIncludesRunMetadataAndCaseTransitionSections(t *testing.T) {
	t.Parallel()
	oldInput, newInput := compareFixture()
	result := Compare(oldInput, newInput, Options{FailOnRegression: true})
	result.Cases = CaseTransitions{
		Fixed:     []CaseTransition{{CaseID: "fixed", OldStatus: judge.StatusFail, NewStatus: judge.StatusPass}},
		Regressed: []CaseTransition{{CaseID: "regressed", OldStatus: judge.StatusPass, NewStatus: judge.StatusFail}},
		Changed:   []CaseTransition{{CaseID: "changed", OldStatus: judge.StatusError, NewStatus: judge.StatusSkip}},
		Unchanged: []CaseTransition{{CaseID: "unchanged", OldStatus: judge.StatusError, NewStatus: judge.StatusError}},
		Added:     []CaseTransition{{CaseID: "added", NewStatus: judge.StatusPass}},
		Removed:   []CaseTransition{{CaseID: "removed", OldStatus: judge.StatusFail}},
	}
	result.Gates = GateResult{Passed: false, Failures: []string{"1 case(s) regressed"}}

	got := RenderText(result)
	for _, want := range []string{
		"Run summary",
		"pass rate: 50.00% -> 100.00% (+50.00%)",
		"total tokens: 100 -> 140 (+40)",
		"Metadata differences",
		"model name: gpt-5 -> gpt-5.1",
		"Case transitions",
		"fixed (1): fixed (FAIL -> PASS)",
		"regressed (1): regressed (PASS -> FAIL)",
		"changed (1): changed (ERROR -> SKIP)",
		"unchanged (1): unchanged (ERROR -> ERROR)",
		"added (1): added (-> PASS)",
		"removed (1): removed (FAIL ->)",
		"Gates: failed",
		"- 1 case(s) regressed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderText() missing %q:\n%s", want, got)
		}
	}
}

func TestResultJSONUsesStableFields(t *testing.T) {
	t.Parallel()
	oldInput, newInput := compareFixture()
	data, err := json.Marshal(Compare(oldInput, newInput, Options{}))
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	for _, key := range []string{"metadata", "run", "cases", "gates"} {
		if _, ok := got[key]; !ok {
			t.Errorf("JSON result missing top-level %q: %s", key, data)
		}
	}
	for _, key := range []string{"old", "new", "delta"} {
		if _, ok := got["run"].(map[string]any)[key]; !ok {
			t.Errorf("JSON run missing %q: %s", key, data)
		}
	}
	for _, key := range []string{"fixed", "regressed", "changed", "unchanged", "added", "removed"} {
		if _, ok := got["cases"].(map[string]any)[key]; !ok {
			t.Errorf("JSON cases missing %q: %s", key, data)
		}
	}
	for _, key := range []string{"passed", "failures"} {
		if _, ok := got["gates"].(map[string]any)[key]; !ok {
			t.Errorf("JSON gates missing %q: %s", key, data)
		}
	}
}

func TestResultJSONUsesEmptyArraysForEmptyCaseTransitions(t *testing.T) {
	t.Parallel()
	input := report.Input{CaseResults: []report.CaseResult{{CaseID: "case-1", Status: judge.StatusPass}}}
	data, err := json.Marshal(Compare(input, input, Options{}))
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var got struct {
		Cases map[string]json.RawMessage `json:"cases"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	for _, name := range []string{"fixed", "regressed", "changed", "added", "removed"} {
		if string(got.Cases[name]) != "[]" {
			t.Errorf("%s transition group = %s, want []", name, got.Cases[name])
		}
	}
}
