package compare

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alibaba/skill-up/internal/report"
)

const (
	unknownValue = "unknown"
	jsonFormat   = "json"
)

//go:embed templates/compare.html
var htmlSource string

//go:embed templates/set.html
var setHTMLSource string

var comparisonTemplate = template.Must(template.New("comparison").Funcs(template.FuncMap{"promptChange": promptChange}).Parse(htmlSource + setHTMLSource))

type (
	comparisonRow struct{ Name, Old, New, Delta string }
	caseGroup     struct {
		Name  string
		Cases []CaseTransition
	}
)

type comparisonView struct {
	Interactive bool
	Result      Result
	Metrics     []comparisonRow
	Metadata    []comparisonRow
	Groups      []caseGroup
}

// Render emits the same comparison in text, JSON, HTML, or Markdown form.
func Render(result Result, format string) ([]byte, error) {
	switch format {
	case "text":
		return []byte(RenderText(result)), nil
	case jsonFormat:
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode comparison: %w", err)
		}
		return append(data, '\n'), nil
	case "markdown":
		return []byte(RenderMarkdown(result)), nil
	case "html":
		var b bytes.Buffer
		if err := comparisonTemplate.ExecuteTemplate(&b, "comparison-body", newComparisonView(result)); err != nil {
			return nil, fmt.Errorf("render HTML comparison: %w", err)
		}
		return report.RenderComparisonHTML([]report.ComparisonRun{{Label: result.Baseline.Label}}, template.HTML(b.String())) //nolint:gosec // html/template escaped all source content.
	default:
		return nil, fmt.Errorf("unsupported comparison format %q", format)
	}
}

func newComparisonView(result Result) comparisonView {
	old, candidate, delta := result.Run.Old, result.Run.New, result.Run.Delta
	view := comparisonView{
		Result: result,
		Metrics: []comparisonRow{
			{"Pass rate", fmt.Sprintf("%.2f%%", old.PassRate*100), fmt.Sprintf("%.2f%%", candidate.PassRate*100), fmt.Sprintf("%+.2f pp", delta.PassRate*100)},
			{"Cases", strconv.Itoa(old.CaseCount), strconv.Itoa(candidate.CaseCount), fmt.Sprintf("%+d", delta.CaseCount)},
			{"Passed", strconv.Itoa(old.PassCount), strconv.Itoa(candidate.PassCount), fmt.Sprintf("%+d", delta.PassCount)},
			metricRow("Total tokens (all configurations)", "total_tokens", int64(old.TotalTokens), int64(candidate.TotalTokens), result.Run),
			metricRow("Input tokens (primary cases)", "input_tokens", int64(old.InputTokens), int64(candidate.InputTokens), result.Run),
			metricRow("Output tokens (primary cases)", "output_tokens", int64(old.OutputTokens), int64(candidate.OutputTokens), result.Run),
			metricRow("Run duration (ms)", "duration_ms", old.DurationMs, candidate.DurationMs, result.Run),
		},
		Groups: []caseGroup{
			{"Regressed", result.Cases.Regressed},
			{"Fixed", result.Cases.Fixed},
			{"Changed", result.Cases.Changed},
			{"Added", result.Cases.Added},
			{"Removed", result.Cases.Removed},
			{"Unchanged", result.Cases.Unchanged},
		},
	}
	m := result.Metadata
	view.Metadata = []comparisonRow{
		metadataRow("Skill", m.SkillName), metadataRow("Schema version", m.SchemaVersion),
		metadataRow("Engine", m.EngineName), metadataRow("Model", m.ModelName),
		metadataRow("Start time", fieldDiff(formatTime(m.StartTime.Old), formatTime(m.StartTime.New))),
		metadataRow("End time", fieldDiff(formatTime(m.EndTime.Old), formatTime(m.EndTime.New))),
		metadataRow("Requested configuration", m.RequestedConfiguration),
		metadataRow("Applied configuration", m.AppliedConfiguration),
		metadataRow("Observed configuration", m.ObservedConfiguration),
	}
	return view
}

func promptChange(changed *bool) string {
	if changed == nil {
		return unknownValue
	}
	if *changed {
		return "changed"
	}
	return "unchanged"
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return unknownValue
	}
	return t.Format(time.RFC3339Nano)
}

func metricRow(label, key string, old, candidate int64, run RunComparison) comparisonRow {
	row := comparisonRow{label, strconv.FormatInt(old, 10), strconv.FormatInt(candidate, 10), fmt.Sprintf("%+d", candidate-old)}
	if slices.Contains(run.Old.Missing, key) {
		row.Old, row.Delta = unknownValue, unknownValue
	}
	if slices.Contains(run.New.Missing, key) {
		row.New, row.Delta = unknownValue, unknownValue
	}
	return row
}

func metadataRow(label string, diff FieldDiff[string]) comparisonRow {
	row := comparisonRow{Name: label, Old: knownText(diff.Old), New: knownText(diff.New), Delta: "unchanged"}
	if diff.Changed {
		row.Delta = "changed"
	}
	if diff.Old == "" || diff.New == "" {
		row.Delta = unknownValue
	}
	return row
}

func knownText(value string) string {
	if value == "" {
		return unknownValue
	}
	return value
}

// RenderMarkdown creates a shareable comparison for PR descriptions and CI artifacts.
func RenderMarkdown(result Result) string {
	view := newComparisonView(result)
	var b strings.Builder
	b.WriteString("# Evaluation comparison\n\n")
	fmt.Fprintf(&b, "**%s** → **%s**\n\n", markdownCell(result.Baseline.Label), markdownCell(result.Candidate.Label))
	fmt.Fprintf(&b, "Sources: %s → %s\n\n", markdownCell(result.Baseline.Path), markdownCell(result.Candidate.Path))
	fmt.Fprintf(&b, "Shared cases: **%d**; added: **%d**; removed: **%d**.\n\n", result.Coverage.Shared, result.Coverage.Added, result.Coverage.Removed)
	fmt.Fprintf(&b, "Comparison coverage: %d / %d reference cases; %d / %d compared cases.\n\n", result.Coverage.Shared, result.Run.Old.CaseCount, result.Coverage.Shared, result.Run.New.CaseCount)
	if result.Coverage.Added+result.Coverage.Removed > 0 {
		b.WriteString("> Different case sets: run-level deltas include unpaired cases. Only shared case IDs are classified as fixed or regressed.\n\n")
	}
	writeMarkdownRows(&b, "Run summary", view.Metrics, result)
	writeMarkdownRows(&b, "Metadata differences", view.Metadata, result)
	b.WriteString("Missing provenance is unknown. Matching case IDs do not prove identical prompts, suites, Skill revisions, or judge/rubric definitions. Scores are not regraded.\n\n")
	b.WriteString("## Case transitions\n\n")
	for _, group := range view.Groups {
		writeMarkdownGroup(&b, group)
	}
	b.WriteString("## Gates\n\n")
	if result.Gates.Passed {
		b.WriteString("Passed (configured checks only).\n")
	} else {
		b.WriteString("Failed.\n\n")
		for _, failure := range result.Gates.Failures {
			fmt.Fprintf(&b, "- %s\n", markdownCell(failure))
		}
	}
	return b.String()
}

func writeMarkdownRows(b *strings.Builder, heading string, rows []comparisonRow, result Result) {
	fmt.Fprintf(b, "## %s\n\n| Metric | %s | %s | Change |\n|---|---|---|---|\n", heading, markdownCell(result.Baseline.Label), markdownCell(result.Candidate.Label))
	for _, row := range rows {
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n", markdownCell(row.Name), markdownCell(row.Old), markdownCell(row.New), markdownCell(row.Delta))
	}
	b.WriteByte('\n')
}

func writeMarkdownGroup(b *strings.Builder, group caseGroup) {
	fmt.Fprintf(b, "### %s (%d)\n\n", group.Name, len(group.Cases))
	if len(group.Cases) == 0 {
		b.WriteString("None.\n\n")
		return
	}
	b.WriteString("| Case ID | Reference title | Compared title | Reference status | Compared status | Reference configuration | Compared configuration | Prompt |\n|---|---|---|---|---|---|---|---|\n")
	for _, cr := range group.Cases {
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n", markdownCell(cr.CaseID), markdownCell(cr.OldTitle), markdownCell(cr.NewTitle), markdownCell(string(cr.OldStatus)), markdownCell(string(cr.NewStatus)), markdownCell(cr.OldConfiguration), markdownCell(cr.NewConfiguration), promptChange(cr.PromptChanged))
	}
	b.WriteByte('\n')
}

func markdownCell(value string) string {
	// Encode all Markdown control characters, pipes, and line breaks so source
	// titles cannot add markup, links, or rows to a shared report.
	value = html.EscapeString(value)
	value = strings.NewReplacer("\r", " ", "\n", " ", "\\", "&#92;", "|", "&#124;", "`", "&#96;", "*", "&#42;", "_", "&#95;", "[", "&#91;", "]", "&#93;").Replace(value)
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}
