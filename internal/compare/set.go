package compare

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"strings"
	"text/tabwriter"

	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
)

// RunInput combines a source artifact with its recorded measurements.
type RunInput struct {
	Source         Source
	Input          report.Input
	MissingMetrics []string
}

// RunSummary contains a run's identity and primary-case metrics.
type RunSummary struct {
	Source  Source     `json:"source"`
	Metrics RunMetrics `json:"metrics"`
}

// CaseOutcome retains the source result represented by one matrix cell.
type CaseOutcome struct {
	Title         string       `json:"title"`
	Status        judge.Status `json:"status"`
	Configuration string       `json:"configuration,omitempty"`
}

// CaseRow aligns outcomes to Runs; a nil outcome means the case is absent.
type CaseRow struct {
	CaseID   string         `json:"case_id"`
	Outcomes []*CaseOutcome `json:"outcomes"`
}

// Set compares every candidate independently against the first, baseline run.
// Its schema is identical for two inputs and for larger collections.
type Set struct {
	inputs      []report.Input
	Baseline    Source       `json:"baseline"`
	Runs        []RunSummary `json:"runs"`
	CaseMatrix  []CaseRow    `json:"case_matrix"`
	Comparisons []Result     `json:"comparisons"`
	Gates       GateResult   `json:"gates"`
}

// BuildSet compares two or more runs, preserving argument order and identity.
func BuildSet(runs []RunInput, options Options) (Set, error) {
	if len(runs) < 2 {
		return Set{}, errors.New("compare requires at least two result files")
	}
	set := Set{Baseline: runs[0].Source, Comparisons: make([]Result, 0, len(runs)-1), Gates: GateResult{Passed: true, Failures: []string{}}}
	for index, run := range runs {
		set.inputs = append(set.inputs, run.Input)
		source := run.Source
		source.ID = index + 1
		metrics := collectRunMetrics(run.Input)
		metrics.Missing = run.MissingMetrics
		set.Runs = append(set.Runs, RunSummary{Source: source, Metrics: metrics})
	}
	set.Baseline = set.Runs[0].Source
	for index, candidate := range runs[1:] {
		options.OldMissingMetrics, options.NewMissingMetrics = runs[0].MissingMetrics, candidate.MissingMetrics
		result := Compare(runs[0].Input, candidate.Input, options)
		result.Baseline, result.Candidate = set.Baseline, set.Runs[index+1].Source
		set.Comparisons = append(set.Comparisons, result)
		for _, failure := range result.Gates.Failures {
			set.Gates.Failures = append(set.Gates.Failures, fmt.Sprintf("%s (%s): %s", candidate.Source.Label, candidate.Source.Path, failure))
		}
	}
	set.Gates.Passed = len(set.Gates.Failures) == 0
	set.CaseMatrix = buildCaseMatrix(runs)
	return set, nil
}

func buildCaseMatrix(runs []RunInput) []CaseRow {
	rows := make([]CaseRow, 0)
	indices := make(map[string]int)
	for runIndex, run := range runs {
		for _, cr := range run.Input.PrimaryCaseResults() {
			index, exists := indices[cr.CaseID]
			if !exists {
				index = len(rows)
				indices[cr.CaseID] = index
				rows = append(rows, CaseRow{CaseID: cr.CaseID, Outcomes: make([]*CaseOutcome, len(runs))})
			}
			rows[index].Outcomes[runIndex] = &CaseOutcome{Title: cr.Title, Status: cr.Status, Configuration: cr.Configuration}
		}
	}
	return rows
}

type overviewRow struct {
	Name   string
	Values []string
}

type pairView struct {
	Left, Right int
	View        comparisonView
}

type setView struct {
	Pairs       []pairView
	Set         Set
	Metrics     []overviewRow
	Matrix      []overviewRow
	Comparisons []comparisonView
}

func newSetView(set Set) setView {
	view := setView{Set: set}
	for runIndex, run := range set.Runs {
		rows := newComparisonView(Result{Run: RunComparison{Old: run.Metrics, New: run.Metrics}}).Metrics
		for rowIndex, row := range rows {
			if runIndex == 0 {
				view.Metrics = append(view.Metrics, overviewRow{Name: row.Name})
			}
			view.Metrics[rowIndex].Values = append(view.Metrics[rowIndex].Values, row.Old)
		}
	}
	for _, row := range set.CaseMatrix {
		values := make([]string, len(row.Outcomes))
		for i, outcome := range row.Outcomes {
			values[i] = "absent"
			if outcome != nil {
				values[i] = string(outcome.Status)
			}
		}
		view.Matrix = append(view.Matrix, overviewRow{Name: row.CaseID, Values: values})
	}
	for _, result := range set.Comparisons {
		view.Comparisons = append(view.Comparisons, newComparisonView(result))
	}
	return view
}

func newHTMLSetView(set Set) setView {
	view := newSetView(set)
	for left := range set.inputs {
		for right := range set.inputs {
			if left == right {
				continue
			}
			options := Options{OldMissingMetrics: set.Runs[left].Metrics.Missing, NewMissingMetrics: set.Runs[right].Metrics.Missing}
			result := Compare(set.inputs[left], set.inputs[right], options)
			result.Baseline, result.Candidate = set.Runs[left].Source, set.Runs[right].Source
			pair := newComparisonView(result)
			pair.Interactive = true
			view.Pairs = append(view.Pairs, pairView{Left: left, Right: right, View: pair})
		}
	}
	return view
}

// RenderSet emits one report containing all runs and baseline comparisons.
func RenderSet(set Set, format string) ([]byte, error) {
	switch format {
	case jsonFormat:
		data, err := json.MarshalIndent(set, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode comparison set: %w", err)
		}
		return append(data, '\n'), nil
	case "html":
		var b bytes.Buffer
		if err := comparisonTemplate.ExecuteTemplate(&b, "set", newHTMLSetView(set)); err != nil {
			return nil, fmt.Errorf("render HTML comparisons: %w", err)
		}
		runs := make([]report.ComparisonRun, len(set.Runs))
		for i, run := range set.Runs {
			runs[i].Label = run.Source.Label
			if i < len(set.inputs) {
				runs[i].Input = set.inputs[i]
			}
		}
		return report.RenderComparisonHTML(runs, template.HTML(b.String())) //nolint:gosec // html/template escaped all source content.
	case "markdown":
		return []byte(renderSetMarkdown(set)), nil
	case "text":
		return []byte(renderSetText(set)), nil
	default:
		return nil, fmt.Errorf("unsupported comparison format %q", format)
	}
}

func renderSetMarkdown(set Set) string {
	view := newSetView(set)
	var b strings.Builder
	fmt.Fprintf(&b, "# Evaluation comparison: %d runs\n\nReference for changes: **%s**. Every other run is compared independently against this reference.\n\n", len(set.Runs), markdownCell(set.Baseline.Label))
	b.WriteString("## Run overview\n\n")
	writeSetMarkdownTable(&b, "Metric", set.Runs, view.Metrics)
	b.WriteString("\n## Case matrix\n\nAbsent means that the case was not present in that run.\n\n")
	writeSetMarkdownTable(&b, "Case ID", set.Runs, view.Matrix)
	for _, result := range set.Comparisons {
		b.WriteString("\n---\n\n")
		for line := range strings.SplitSeq(RenderMarkdown(result), "\n") {
			if strings.HasPrefix(line, "#") {
				b.WriteByte('#')
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	b.WriteString("\n## Overall gates\n\n")
	if set.Gates.Passed {
		b.WriteString("Passed (configured checks only).\n")
	} else {
		for _, failure := range set.Gates.Failures {
			fmt.Fprintf(&b, "- %s\n", markdownCell(failure))
		}
	}
	return b.String()
}

func writeSetMarkdownTable(b *strings.Builder, firstHeading string, runs []RunSummary, rows []overviewRow) {
	fmt.Fprintf(b, "| %s |", firstHeading)
	for _, run := range runs {
		fmt.Fprintf(b, " %s |", markdownCell(run.Source.Label))
	}
	b.WriteString("\n|---|")
	for range runs {
		b.WriteString("---|")
	}
	b.WriteByte('\n')
	for _, row := range rows {
		fmt.Fprintf(b, "| %s |", markdownCell(row.Name))
		for _, value := range row.Values {
			fmt.Fprintf(b, " %s |", markdownCell(value))
		}
		b.WriteByte('\n')
	}
}

func renderSetText(set Set) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Run overview (%d runs; reference: %s)\n", len(set.Runs), set.Baseline.Label)
	view := newSetView(set)
	writeSetTextTable(&b, "Metric", set.Runs, view.Metrics)
	b.WriteString("\nCase matrix (absent = case not present in run)\n")
	writeSetTextTable(&b, "Case ID", set.Runs, view.Matrix)
	for _, result := range set.Comparisons {
		b.WriteByte('\n')
		b.WriteString(RenderText(result))
	}
	if set.Gates.Passed {
		b.WriteString("\nOverall gates: passed\n")
	} else {
		b.WriteString("\nOverall gates: failed\n")
		for _, failure := range set.Gates.Failures {
			fmt.Fprintf(&b, "- %s\n", failure)
		}
	}
	return b.String()
}

func writeSetTextTable(b *strings.Builder, firstHeading string, runs []RunSummary, rows []overviewRow) {
	w := tabwriter.NewWriter(b, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprint(w, firstHeading)
	for _, run := range runs {
		_, _ = fmt.Fprintf(w, "\t%s", run.Source.Label)
	}
	_, _ = fmt.Fprintln(w)
	for _, row := range rows {
		_, _ = fmt.Fprint(w, row.Name)
		for _, value := range row.Values {
			_, _ = fmt.Fprintf(w, "\t%s", value)
		}
		_, _ = fmt.Fprintln(w)
	}
	_ = w.Flush()
}
