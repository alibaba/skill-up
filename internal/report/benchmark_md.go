// Package report — benchmark_md.go generates human-readable Markdown benchmark reports.
//
// Format matches demo/chinese-jokes-workspace/iteration-1/benchmark.md.
package report

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// WriteBenchmarkMarkdown writes a Markdown benchmark report to the given writer.
func WriteBenchmarkMarkdown(w io.Writer, bm *AnthropicBenchmark) error {
	var sb strings.Builder

	fmt.Fprintf(&sb, "# Skill Benchmark: %s\n\n", bm.Metadata.SkillName)
	fmt.Fprintf(&sb, "**Date**: %s\n", bm.Metadata.Timestamp)
	fmt.Fprintf(&sb, "**Evals**: %s (%d runs each per configuration)\n\n",
		strings.Join(collectEvalNames(bm.Runs), ", "),
		bm.Metadata.RunsPerConfiguration)

	writeBenchmarkSummary(&sb, bm)
	writeBenchmarkPerCase(&sb, bm.Runs)

	_, err := w.Write([]byte(sb.String()))
	return err
}

// collectEvalNames returns the unique eval names in the order they first appear.
func collectEvalNames(runs []BenchmarkRun) []string {
	evalNames := make([]string, 0)
	seen := map[string]bool{}
	for _, r := range runs {
		if seen[r.EvalName] {
			continue
		}
		evalNames = append(evalNames, r.EvalName)
		seen[r.EvalName] = true
	}
	return evalNames
}

// writeBenchmarkSummary appends the Summary table for the benchmark to sb.
func writeBenchmarkSummary(sb *strings.Builder, bm *AnthropicBenchmark) {
	sb.WriteString("## Summary\n\n")

	ws := bm.RunSummary.WithSkill.PassRate
	if bm.RunSummary.WithoutSkill == nil {
		sb.WriteString("| Metric | With Skill |\n")
		sb.WriteString("|--------|------------|\n")
		fmt.Fprintf(sb, "| Pass Rate | %.0f%% ± %.0f%% |\n",
			ws.Mean*100, ws.StdDev*100)
		return
	}

	sb.WriteString("| Metric | With Skill | Without Skill | Delta |\n")
	sb.WriteString("|--------|-----------|--------------|-------|\n")

	wos := bm.RunSummary.WithoutSkill.PassRate
	deltaStr := ""
	if bm.RunSummary.Delta != nil {
		deltaStr = bm.RunSummary.Delta.PassRate
	}

	fmt.Fprintf(sb, "| Pass Rate | %.0f%% ± %.0f%% | %.0f%% ± %.0f%% | %s |\n",
		ws.Mean*100, ws.StdDev*100,
		wos.Mean*100, wos.StdDev*100,
		deltaStr)
}

// writeBenchmarkPerCase appends the Per-Case Results section to sb.
func writeBenchmarkPerCase(sb *strings.Builder, runs []BenchmarkRun) {
	if len(runs) == 0 {
		return
	}

	sb.WriteString("\n## Per-Case Results\n\n")
	for _, r := range runs {
		writeBenchmarkRun(sb, r)
	}
}

// writeBenchmarkRun appends a single run section (header + expectations table) to sb.
func writeBenchmarkRun(sb *strings.Builder, r BenchmarkRun) {
	fmt.Fprintf(sb, "### %s (%s)\n\n", r.EvalName, r.Configuration)
	fmt.Fprintf(sb, "- **Pass Rate**: %.0f%% (%d/%d)\n",
		r.Result.PassRate*100, r.Result.Passed, r.Result.Total)

	writeBenchmarkExpectations(sb, r.Expectations)
	sb.WriteString("\n")
}

// writeBenchmarkExpectations appends the expectation rows table for a run to sb.
func writeBenchmarkExpectations(sb *strings.Builder, expectations []AnthropicExpectation) {
	if len(expectations) == 0 {
		return
	}

	sb.WriteString("\n| Expectation | Result | Evidence |\n")
	sb.WriteString("|-------------|--------|----------|\n")
	for _, e := range expectations {
		icon := "✅"
		if !e.Passed {
			icon = "❌"
		}
		fmt.Fprintf(sb, "| %s | %s | %s |\n", e.Text, icon, e.Evidence)
	}
}

// WriteBenchmarkMarkdownFile writes the benchmark Markdown report to a file.
func WriteBenchmarkMarkdownFile(path string, bm *AnthropicBenchmark) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create benchmark md file: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close benchmark md file: %w", cerr)
		}
	}()

	return WriteBenchmarkMarkdown(f, bm)
}
