package compare

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
)

func multipleRunFixture() []RunInput {
	baseline, candidate := compareFixture()
	candidate.TotalTokens = 90
	regressed := candidate
	regressed.TotalTokens = 140
	regressed.CaseResults = []report.CaseResult{
		{CaseID: "case-1", Status: judge.StatusFail},
		{CaseID: "case-2", Status: judge.StatusFail},
		{CaseID: "candidate-only", Status: judge.StatusPass},
	}
	return []RunInput{
		{Source: Source{Label: "Baseline", Path: "base.json"}, Input: baseline},
		{Source: Source{Label: "Improved", Path: "improved.json"}, Input: candidate},
		{Source: Source{Label: "Regressed", Path: "regressed.json"}, Input: regressed},
		{Source: Source{Label: "Unknown usage", Path: "unknown.json"}, Input: candidate, MissingMetrics: []string{"total_tokens"}},
	}
}

func TestCompareRunsUsesOneBaselineAndAggregatesAllGates(t *testing.T) {
	t.Parallel()
	limit := 10.0
	set, err := BuildSet(multipleRunFixture(), Options{FailOnRegression: true, MaxTokenIncreasePercent: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Runs) != 4 || len(set.Comparisons) != 3 {
		t.Fatalf("lost runs: %+v", set)
	}
	for _, pair := range set.Comparisons {
		if pair.Baseline != set.Baseline || pair.Run.Old.TotalTokens != 100 || pair.Run.Old.CaseCount != 2 {
			t.Fatalf("candidate used a different baseline or double-counted benchmark: %+v", pair)
		}
	}
	if len(set.Comparisons[0].Cases.Fixed) != 1 || len(set.Comparisons[1].Cases.Regressed) != 1 || len(set.Comparisons[1].Cases.Unchanged) != 1 {
		t.Fatal("transitions compared adjacent candidates instead of the baseline")
	}
	if set.Gates.Passed || len(set.Gates.Failures) != 3 {
		t.Fatalf("gates = %+v", set.Gates)
	}
	for _, want := range []string{"Regressed (regressed.json)", "Unknown usage (unknown.json)"} {
		if !strings.Contains(strings.Join(set.Gates.Failures, "\n"), want) {
			t.Errorf("gate failure does not identify %s", want)
		}
	}
}

func TestCaseMatrixPreservesUnionAndSourceOrder(t *testing.T) {
	t.Parallel()
	set, err := BuildSet(multipleRunFixture(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.CaseMatrix) != 3 {
		t.Fatalf("matrix = %+v", set.CaseMatrix)
	}
	row := set.CaseMatrix[2]
	if row.CaseID != "candidate-only" || len(row.Outcomes) != 4 || row.Outcomes[0] != nil || row.Outcomes[1] != nil || row.Outcomes[2].Status != judge.StatusPass || row.Outcomes[3] != nil {
		t.Fatalf("absent cases were paired: %+v", row)
	}
	if first := set.CaseMatrix[0].Outcomes[0]; first.Status != judge.StatusPass || first.Configuration != "with_skill" {
		t.Fatalf("matrix did not use primary variant: %+v", first)
	}
}

func TestRenderSetIncludesEveryRunAndComparison(t *testing.T) {
	t.Parallel()
	runs := multipleRunFixture()
	set, err := BuildSet(runs, Options{FailOnRegression: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"text", "json", "html", "markdown"} {
		data, err := RenderSet(set, format)
		if err != nil {
			t.Fatal(err)
		}
		for _, run := range runs {
			if !strings.Contains(string(data), run.Source.Label) || !strings.Contains(string(data), run.Source.Path) {
				t.Errorf("%s lost run %s", format, run.Source.Label)
			}
		}
		if !strings.Contains(string(data), "candidate-only") {
			t.Errorf("%s lost candidate-only case", format)
		}
		if format == "json" {
			var output struct {
				Runs        []json.RawMessage `json:"runs"`
				Comparisons []json.RawMessage `json:"comparisons"`
			}
			if err := json.Unmarshal(data, &output); err != nil {
				t.Fatal(err)
			}
			if len(output.Runs) != 4 || len(output.Comparisons) != 3 {
				t.Errorf("JSON lost runs: %s", data)
			}
		} else if !strings.Contains(string(data), "Case matrix") || !strings.Contains(string(data), "absent") {
			t.Errorf("%s lost case matrix", format)
		}
	}
}

func TestCompareRunsRejectsTooFewInputs(t *testing.T) {
	t.Parallel()
	for _, runs := range [][]RunInput{nil, multipleRunFixture()[:1]} {
		if _, err := BuildSet(runs, Options{}); err == nil {
			t.Fatal("accepted fewer than two runs")
		}
	}
}

func TestSetPreservesUnknownUsage(t *testing.T) {
	t.Parallel()
	set, err := BuildSet(multipleRunFixture(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if set.Runs[3].Metrics.Missing[0] != "total_tokens" || set.Comparisons[2].Run.Delta.Missing[0] != "total_tokens" {
		t.Fatal("unknown measurements lost")
	}
}

func TestHTMLPairsUseSelectedReferenceAndKeepCIGates(t *testing.T) {
	t.Parallel()
	set, err := BuildSet(multipleRunFixture(), Options{FailOnRegression: true})
	if err != nil {
		t.Fatal(err)
	}
	view := newHTMLSetView(set)
	if len(view.Pairs) != 12 {
		t.Fatalf("got %d ordered pairs for 4 runs", len(view.Pairs))
	}
	found := false
	for _, pair := range view.Pairs {
		if pair.Left == pair.Right {
			t.Fatal("self comparison generated")
		}
		if pair.Left != 1 || pair.Right != 2 {
			continue
		}
		found = true
		result := pair.View.Result
		if result.Baseline.ID != 2 || result.Candidate.ID != 3 || result.Run.Delta.TotalTokens != 50 {
			t.Fatalf("selected reference was ignored: %+v", result.Run)
		}
		if len(result.Cases.Regressed) != 2 {
			t.Fatalf("expected two regressions against the improved run: %+v", result.Cases)
		}
		if !pair.View.Interactive {
			t.Fatal("pair not marked as interactive")
		}
	}
	if !found {
		t.Fatal("selected B to C pair missing")
	}
	if len(set.Comparisons[1].Cases.Regressed) != 1 || set.Gates.Passed {
		t.Fatal("interactive pairs changed CI reference semantics")
	}
}
