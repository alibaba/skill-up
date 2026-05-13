package report

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteBenchmarkMarkdown_WithBaseline(t *testing.T) {
	bm := &AnthropicBenchmark{
		Metadata: BenchmarkMetadata{
			SkillName:            "chinese-jokes",
			Timestamp:            "2026-04-08T15:20:00Z",
			RunsPerConfiguration: 1,
		},
		Runs: []BenchmarkRun{
			{
				EvalID: 1, EvalName: "bored-coding", Configuration: "with_skill", RunNumber: 1,
				Result: BenchmarkRunResult{PassRate: 1.0, Passed: 5, Total: 5},
				Expectations: []AnthropicExpectation{
					{Text: "empathy", Passed: true, Evidence: "shows empathy"},
				},
			},
			{
				EvalID: 1, EvalName: "bored-coding", Configuration: "without_skill", RunNumber: 1,
				Result: BenchmarkRunResult{PassRate: 0.4, Passed: 2, Failed: 3, Total: 5},
				Expectations: []AnthropicExpectation{
					{Text: "empathy", Passed: true, Evidence: "shows empathy"},
					{Text: "joke", Passed: false, Evidence: "no joke found"},
				},
			},
		},
		RunSummary: AnthropicRunSummary{
			WithSkill:    AnthropicStatSummary{PassRate: AnthropicStatValue{Mean: 1.0, StdDev: 0}},
			WithoutSkill: &AnthropicStatSummary{PassRate: AnthropicStatValue{Mean: 0.47, StdDev: 0.09}},
			Delta:        &AnthropicDelta{PassRate: "+0.53"},
		},
	}

	var buf bytes.Buffer
	if err := WriteBenchmarkMarkdown(&buf, bm); err != nil {
		t.Fatalf("WriteBenchmarkMarkdown error: %v", err)
	}

	md := buf.String()

	checks := []string{
		"# Skill Benchmark: chinese-jokes",
		"With Skill",
		"Without Skill",
		"Delta",
		"bored-coding",
		"+0.53",
	}
	for _, check := range checks {
		if !strings.Contains(md, check) {
			t.Errorf("markdown missing expected content: %q", check)
		}
	}
}

func TestWriteBenchmarkMarkdown_WithoutBaseline(t *testing.T) {
	bm := &AnthropicBenchmark{
		Metadata: BenchmarkMetadata{
			SkillName:            "test-skill",
			Timestamp:            "2026-01-01",
			RunsPerConfiguration: 1,
		},
		Runs: []BenchmarkRun{
			{
				EvalID: 1, EvalName: "test-case", Configuration: "with_skill", RunNumber: 1,
				Result: BenchmarkRunResult{PassRate: 1.0, Passed: 3, Total: 3},
			},
		},
		RunSummary: AnthropicRunSummary{
			WithSkill: AnthropicStatSummary{PassRate: AnthropicStatValue{Mean: 1.0}},
		},
	}

	var buf bytes.Buffer
	if err := WriteBenchmarkMarkdown(&buf, bm); err != nil {
		t.Fatalf("WriteBenchmarkMarkdown error: %v", err)
	}

	md := buf.String()
	if strings.Contains(md, "Without Skill") {
		t.Error("should not contain 'Without Skill' when no baseline")
	}
	if strings.Contains(md, "Delta") {
		t.Error("should not contain 'Delta' when no baseline")
	}
}

func TestWriteBenchmarkMarkdownFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benchmark.md")

	bm := &AnthropicBenchmark{
		Metadata: BenchmarkMetadata{SkillName: "test", Timestamp: "2026-01-01", RunsPerConfiguration: 1},
		RunSummary: AnthropicRunSummary{
			WithSkill: AnthropicStatSummary{PassRate: AnthropicStatValue{Mean: 1.0}},
		},
	}

	if err := WriteBenchmarkMarkdownFile(path, bm); err != nil {
		t.Fatalf("WriteBenchmarkMarkdownFile error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file error: %v", err)
	}
	if !strings.Contains(string(data), "# Skill Benchmark") {
		t.Error("expected markdown header")
	}
}
