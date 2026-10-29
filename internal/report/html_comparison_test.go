package report

import (
	"context"
	"encoding/json"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/judge"
)

func TestComparisonExtendsOriginalCaseDetails(t *testing.T) {
	t.Parallel()
	input := Input{SkillName: "example", CaseResults: []CaseResult{
		{CaseID: "shared", Status: judge.StatusPass, Configuration: "with_skill", Prompt: "original prompt", Response: "original response"},
		{CaseID: "shared", Status: judge.StatusFail, Configuration: "without_skill", Response: "reference response"},
	}}
	runs := []ComparisonRun{{Label: "first", Input: input}, {Label: "</script><script>alert(1)</script>", Input: input}}
	output, err := RenderComparisonHTML(runs, template.HTML("<p>Comparison marker</p>"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Comparison marker", "comparison-run", "original prompt", "original response", "reference response", "Formal Grades", "Your Feedback", "panel-benchmark"} {
		if !strings.Contains(string(output), want) {
			t.Errorf("original report feature missing: %s", want)
		}
	}
	if strings.Contains(string(output), "</script><script>alert(1)") {
		t.Fatal("run label escaped its JSON script context")
	}
	path := filepath.Join(t.TempDir(), "report.html")
	if err := (&HTMLReporter{OutputPath: path}).Write(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	legacy, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), "comparison-run") || strings.Contains(string(legacy), "Comparison marker") {
		t.Fatal("comparison controls leaked into legacy report")
	}
}

func TestCrossFileDetailsUseCanonicalPrimaryResults(t *testing.T) {
	t.Parallel()
	const (
		withSkill    = "with_skill"
		withoutSkill = "without_skill"
	)
	input := Input{CaseResults: []CaseResult{
		{CaseID: "shared", Configuration: withSkill, Status: judge.StatusPass, Response: "with skill answer"},
		{CaseID: "shared", Configuration: "custom", Status: judge.StatusFail, Response: "custom answer"},
		{CaseID: "shared", Configuration: withoutSkill, Status: judge.StatusFail, Response: "without skill answer"},
		{CaseID: "fallback", Configuration: withoutSkill, Status: judge.StatusPass},
	}}
	output, err := RenderComparisonHTML([]ComparisonRun{{Label: "input.json", Input: input}}, "")
	if err != nil {
		t.Fatal(err)
	}
	_, assignment, found := strings.Cut(string(output), "var comparisonRuns = ")
	if !found {
		t.Fatal("comparison payload missing")
	}
	var runs []struct {
		Primary []embeddedCase `json:"primary_cases"`
	}
	if err := json.NewDecoder(strings.NewReader(assignment)).Decode(&runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || len(runs[0].Primary) != 2 {
		t.Fatalf("incorrect primary payload: %+v", runs)
	}
	if cr := runs[0].Primary[0]; cr.Configuration != withSkill || cr.Response != "with skill answer" {
		t.Fatalf("interactive viewer did not prefer with_skill: %+v", cr)
	}
	if runs[0].Primary[1].Configuration != withoutSkill {
		t.Fatal("without_skill-only fallback lost")
	}
}
