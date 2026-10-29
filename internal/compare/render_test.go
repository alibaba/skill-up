package compare

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
)

func TestShareableReportsIncludeTransitionsAndCoverage(t *testing.T) {
	t.Parallel()
	oldInput, newInput := compareFixture()
	oldInput.CaseResults = append(oldInput.CaseResults, report.CaseResult{CaseID: "removed", Status: judge.StatusPass})
	newInput.CaseResults[0].Status = judge.StatusFail
	newInput.CaseResults = append(newInput.CaseResults, report.CaseResult{CaseID: "added", Status: judge.StatusPass})
	result := Compare(oldInput, newInput, Options{FailOnRegression: true})
	result.Baseline = Source{Label: "Previous Skill", Path: "old.json"}
	result.Candidate = Source{Label: "Revised Skill", Path: "new.json"}
	for _, format := range []string{"html", "markdown"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			data, err := Render(result, format)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"Previous Skill", "Revised Skill", "old.json", "new.json", "Run summary", "Metadata differences", "Regressed (1)", "Fixed (1)", "Added (1)", "Removed (1)", "Different case sets", "1 case(s) regressed", "unknown"} {
				if !strings.Contains(string(data), want) {
					t.Errorf("%s missing %q", format, want)
				}
			}
		})
	}
	if result.Coverage != (Coverage{Shared: 2, Added: 1, Removed: 1}) {
		t.Fatalf("coverage = %+v", result.Coverage)
	}
}

func TestReportsEscapeUntrustedSourceText(t *testing.T) {
	t.Parallel()
	input := report.Input{CaseResults: []report.CaseResult{{CaseID: "case", Title: "</td><script>alert(1)</script>\n| [link](https://example.com) `code` \\|", Status: judge.StatusPass}}}
	result := Compare(input, input, Options{})
	result.Baseline.Label = "</title><script>alert(2)</script>"
	for _, format := range []string{"html", "markdown"} {
		data, err := Render(result, format)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, unsafe := range []string{"<script>alert(1)", "<script>alert(2)", "[link](https://example.com)", "`code`"} {
			// Markdown markup is plain text within an HTML table cell.
			if format == "html" && unsafe[0] != '<' {
				continue
			}
			if strings.Contains(text, unsafe) {
				t.Errorf("%s contains unescaped %q", format, unsafe)
			}
		}
		if !strings.Contains(text, "&lt;script&gt;alert(1)") {
			t.Errorf("%s did not retain escaped title", format)
		}
	}
}

func TestMissingMeasurementsStayUnknownInEveryFormat(t *testing.T) {
	t.Parallel()
	oldInput, newInput := compareFixture()
	options := Options{OldMissingMetrics: []string{"total_tokens", "input_tokens"}}
	limit := 20.0
	options.MaxTokenIncreasePercent = &limit
	result := Compare(oldInput, newInput, options)
	if result.Gates.Passed || !strings.Contains(result.Gates.Failures[0], "unknown") {
		t.Fatalf("gate accepted unknown tokens: %+v", result.Gates)
	}
	for _, format := range []string{"json", "html", "markdown", "text"} {
		data, err := Render(result, format)
		if err != nil {
			t.Fatal(err)
		}
		if format == "json" {
			var output struct {
				Run struct {
					Old   map[string]*float64 `json:"old"`
					New   map[string]*float64 `json:"new"`
					Delta map[string]*float64 `json:"delta"`
				} `json:"run"`
			}
			if err := json.Unmarshal(data, &output); err != nil {
				t.Fatal(err)
			}
			if output.Run.Old["total_tokens"] != nil || output.Run.Delta["total_tokens"] != nil || output.Run.New["total_tokens"] == nil {
				t.Fatalf("missing JSON metrics were coerced to measured zero: %s", data)
			}
		} else if !strings.Contains(string(data), "unknown") {
			t.Errorf("%s hides missing measurements", format)
		}
	}
}

func TestMissingMetricsUsePrimaryVariantAndPreserveMeasuredZero(t *testing.T) {
	t.Parallel()
	data := []byte(`{"total_tokens":0,"case_results":[{"case_id":"a","configuration":"without_skill"},{"case_id":"a","configuration":"with_skill","input_tokens":0,"output_tokens":0}]}`)
	var input report.Input
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	if got := MissingMetrics(data, input); len(got) != 0 {
		t.Fatalf("zero measurements marked unknown: %v", got)
	}
	if got := MissingMetrics([]byte(`{"case_results":[{"case_id":"a"}]}`), report.Input{CaseResults: []report.CaseResult{{CaseID: "a"}}}); len(got) != 3 {
		t.Fatalf("missing fields = %v", got)
	}
}

func TestRecordedConfigurationAndPromptDifferences(t *testing.T) {
	t.Parallel()
	oldInput, newInput := compareFixture()
	oldInput.ObservedConfiguration = &report.AgentConfiguration{Model: "observed-old", Version: "1"}
	newInput.ObservedConfiguration = &report.AgentConfiguration{Model: "observed-new", Version: "2"}
	oldInput.CaseResults[1].Prompt = "Old task"
	newInput.CaseResults[0].Prompt = "New task"
	result := Compare(oldInput, newInput, Options{})
	if !result.Metadata.ObservedConfiguration.Changed {
		t.Fatal("observed model change not surfaced")
	}
	if len(result.Cases.Unchanged) != 1 || result.Cases.Unchanged[0].PromptChanged == nil || !*result.Cases.Unchanged[0].PromptChanged {
		t.Fatal("same outcome hid prompt difference")
	}
	for _, format := range []string{"html", "markdown", "text"} {
		data, err := Render(result, format)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "observed-old") || !strings.Contains(string(data), "observed-new") {
			t.Errorf("%s hid observed configuration", format)
		}
	}
}
