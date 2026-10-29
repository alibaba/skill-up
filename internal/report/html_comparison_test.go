package report

import (
	"context"
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
