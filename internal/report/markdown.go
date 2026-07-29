package report

import (
	"context"
	"fmt"
	"html"
	"io"
	"strings"

	"github.com/alibaba/skill-up/internal/judge"
)

// MarkdownReporter writes concise human-readable Markdown summaries.
type MarkdownReporter struct {
	// OutputPath is the file path to write the Markdown report.
	// If empty, writes to stdout.
	OutputPath string
}

// Write implements the Reporter interface.
func (r *MarkdownReporter) Write(_ context.Context, in Input) error {
	var sb strings.Builder

	writeMarkdownHeader(&sb, in)
	writeMarkdownSummary(&sb, in)
	writeMarkdownCases(&sb, in)
	writeMarkdownFailureDetails(&sb, in)

	return writeToOutput(r.OutputPath, "markdown report", func(w io.Writer) error {
		if _, err := w.Write([]byte(sb.String())); err != nil {
			return fmt.Errorf("write markdown report: %w", err)
		}
		return nil
	})
}

func writeMarkdownHeader(sb *strings.Builder, in Input) {
	fmt.Fprintf(sb, "# Skill Report: %s\n\n", markdownText(in.SkillName))
	wroteMetadata := false
	if in.EngineName != "" {
		fmt.Fprintf(sb, "- **Engine**: %s\n", markdownText(in.EngineName))
		wroteMetadata = true
	}
	if in.ModelName != "" {
		fmt.Fprintf(sb, "- **Model**: %s\n", markdownText(in.ModelName))
		wroteMetadata = true
	}
	if wroteMetadata {
		sb.WriteString("\n")
	}
}

func writeMarkdownSummary(sb *strings.Builder, in Input) {
	sb.WriteString("## Summary\n\n")
	sb.WriteString("| Metric | Value |\n")
	sb.WriteString("|---|---:|\n")
	fmt.Fprintf(sb, "| Total Cases | %d |\n", len(in.CaseResults))
	fmt.Fprintf(sb, "| Passed | %d |\n", markdownCountByStatus(in, judge.StatusPass))
	fmt.Fprintf(sb, "| Failed | %d |\n", markdownCountByStatus(in, judge.StatusFail))
	fmt.Fprintf(sb, "| Errors | %d |\n", markdownCountByStatus(in, judge.StatusError))
	fmt.Fprintf(sb, "| Skipped | %d |\n", markdownCountByStatus(in, judge.StatusSkip))
	fmt.Fprintf(sb, "| Pass Rate | %.1f%% |\n", in.OverallPassRate()*100)
	fmt.Fprintf(sb, "| Duration | %s |\n", markdownDuration(in.TotalDuration().Milliseconds()))
	fmt.Fprintf(sb, "| Total Tokens | %d |\n\n", in.TotalTokens)
}

func writeMarkdownCases(sb *strings.Builder, in Input) {
	sb.WriteString("## Cases\n\n")
	sb.WriteString("| Case | Title | Status | Duration | Turns |\n")
	sb.WriteString("|---|---|---|---:|---:|\n")
	for _, cr := range in.CaseResults {
		fmt.Fprintf(sb, "| %s | %s | %s | %s | %d |\n",
			markdownTableCell(cr.CaseID),
			markdownTableCell(cr.Title),
			markdownTableCell(string(cr.Status)),
			markdownDuration(cr.DurationMs),
			cr.Turns,
		)
	}
	sb.WriteString("\n")
}

func writeMarkdownFailureDetails(sb *strings.Builder, in Input) {
	var details strings.Builder
	for _, cr := range in.CaseResults {
		if cr.Status != judge.StatusFail && cr.Status != judge.StatusError {
			continue
		}
		writeMarkdownCaseDetails(&details, cr)
	}
	if details.Len() == 0 {
		return
	}
	sb.WriteString("## Failure and Error Details\n\n")
	sb.WriteString(details.String())
}

func writeMarkdownCaseDetails(sb *strings.Builder, cr CaseResult) {
	fmt.Fprintf(sb, "### %s\n\n", markdownText(cr.CaseID))
	if cr.Error != "" {
		fmt.Fprintf(sb, "- Error: %s\n", markdownText(cr.Error))
	}
	if cr.Grading != nil && cr.Grading.ErrorReason != nil && *cr.Grading.ErrorReason != "" {
		fmt.Fprintf(sb, "- Error Reason: %s\n", markdownText(*cr.Grading.ErrorReason))
	}
	if cr.Grading != nil {
		for _, ar := range cr.Grading.AssertionResults {
			if ar.Passed {
				continue
			}
			fmt.Fprintf(sb, "- Failed: %s\n", markdownText(ar.Text))
			if ar.Evidence != "" {
				fmt.Fprintf(sb, "  - Evidence: %s\n", markdownText(ar.Evidence))
			}
		}
	}
	for _, tr := range cr.TurnResults {
		if tr.Status == "" || tr.Status == "completed" {
			continue
		}
		fmt.Fprintf(sb, "- Turn %d: status=%s", tr.TurnNumber, markdownText(tr.Status))
		if tr.Reason != "" {
			fmt.Fprintf(sb, " reason=%s", markdownText(tr.Reason))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
}

func markdownCountByStatus(in Input, status judge.Status) int {
	count := 0
	for _, cr := range in.CaseResults {
		if cr.Status == status {
			count++
		}
	}
	return count
}

func markdownDuration(ms int64) string {
	return fmt.Sprintf("%.1fs", float64(ms)/1000.0)
}

func markdownTableCell(s string) string {
	s = markdownText(s)
	s = strings.ReplaceAll(s, "|", `\|`)
	if s == "" {
		return "-"
	}
	return s
}

func markdownText(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return html.EscapeString(strings.TrimSpace(s))
}
