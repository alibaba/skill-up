package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestProgressOutputFormatting(t *testing.T) {
	var out bytes.Buffer
	orig := Output
	Output = &out
	t.Cleanup(func() { Output = orig })

	Step(">", "Loading")
	Stepf(">", "Loaded %d", 2)
	Status("*", "Ready")
	Statusf("*", "Case %s", "ok")
	CaseStart(1, 3, "case-a", "Does work")
	CaseComplete(1, 3, "case-a", "PASS", 1)
	CaseComplete(2, 3, "case-b", "SKIP", -1)
	Summary(1, 0, 0)
	Separator()
	Blank()

	got := out.String()
	for _, want := range []string{
		"> Loading\n",
		"> Loaded 2\n",
		"* Ready\n",
		"* Case ok\n",
		"[1/3] case-a: Does work",
		"case-a: PASS (100.0%)",
		"case-b: SKIP\n",
		"Results: 1 passed, 0 failed, 0 errors",
		strings.Repeat("─", 40),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestStatusEmoji(t *testing.T) {
	tests := map[string]string{
		"pass":    "✅",
		"FAIL":    "❌",
		"Error":   "⚠️",
		"skip":    "⏭️",
		"unknown": "❓",
	}
	for status, want := range tests {
		if got := statusEmoji(status); got != want {
			t.Fatalf("statusEmoji(%q) = %q, want %q", status, got, want)
		}
	}
}
