package report

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/judge"
)

func TestGroupedEvaluationReportsKeepJudgeIdentity(t *testing.T) {
	t.Parallel()
	outcomes := []judge.Outcome{
		{ID: "functional", Type: "script", Status: judge.StatusFail, Result: judge.NewResult([]judge.AssertionResult{{Text: "check", Passed: false, Evidence: "exit 1"}}, 1, 1)},
		{ID: "semantic", Type: "agent_judge", Status: judge.StatusPass, Result: judge.NewResult([]judge.AssertionResult{{Text: "quality", Passed: true, Evidence: "clear"}}, 1, 1)},
	}
	cr := CaseResult{CaseID: "case", Status: judge.StatusFail, JudgeResults: outcomes, Grading: judge.NewResult([]judge.AssertionResult{{Text: "functional", Passed: false}, {Text: "semantic", Passed: true}}, 1, 1)}
	in := Input{SkillName: "sample", CaseResults: []CaseResult{cr}}
	for name, reporter := range map[string]Reporter{
		"json":     &JSONReporter{OutputPath: filepath.Join(t.TempDir(), "report.json")},
		"markdown": &MarkdownReporter{OutputPath: filepath.Join(t.TempDir(), "report.md")},
		"junit":    &JUnitReporter{OutputPath: filepath.Join(t.TempDir(), "report.xml")},
		"html":     &HTMLReporter{OutputPath: filepath.Join(t.TempDir(), "report.html")},
	} {
		if err := reporter.Write(context.Background(), in); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var path string
		switch r := reporter.(type) {
		case *JSONReporter:
			path = r.OutputPath
		case *MarkdownReporter:
			path = r.OutputPath
		case *JUnitReporter:
			path = r.OutputPath
		case *HTMLReporter:
			path = r.OutputPath
		}
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), "functional") || !strings.Contains(string(data), "semantic") {
			t.Fatalf("%s missing grouped judge identity: %v", name, err)
		}
	}
}
