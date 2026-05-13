// Package ui provides user-facing status output for the CLI.
//
// The ui layer is separate from the logging layer: ui messages are always
// visible and show concise milestone status with emoji indicators, while
// logging messages (slog-based) are developer-oriented and only shown with
// --verbose.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Output is the writer used by all ui functions. Override it in tests
// or to redirect progress output (e.g. to stderr or io.Discard).
var Output io.Writer = os.Stdout

// Step prints a phase-start line with an emoji prefix.
// Example: "📋 Loading configuration...".
func Step(emoji, message string) {
	mustPrintf("%s %s\n", emoji, message)
}

// Stepf prints a phase-start line with an emoji prefix and format args.
func Stepf(emoji, format string, args ...any) {
	Step(emoji, fmt.Sprintf(format, args...))
}

// Status prints a status line with an emoji prefix.
// Example: "✅ Loaded 3 case(s)".
func Status(emoji, message string) {
	mustPrintf("%s %s\n", emoji, message)
}

// Statusf prints a status line with an emoji prefix and format args.
func Statusf(emoji, format string, args ...any) {
	Status(emoji, fmt.Sprintf(format, args...))
}

// CaseStart prints a case-in-progress line.
// Example: "   ⏳ [1/3] test-1: basic test...".
func CaseStart(index, total int, caseID, title string) {
	mustPrintf("   ⏳ [%d/%d] %s: %s...\n", index, total, caseID, title)
}

// CaseComplete prints a case-completed line with a status emoji.
// Example: "   ✅ [1/3] test-1: PASS (100.0%)".
func CaseComplete(index, total int, caseID, status string, passRate float64) {
	emoji := statusEmoji(status)
	if passRate >= 0 {
		mustPrintf("   %s [%d/%d] %s: %s (%.1f%%)\n", emoji, index, total, caseID, status, passRate*100)
	} else {
		mustPrintf("   %s [%d/%d] %s: %s\n", emoji, index, total, caseID, status)
	}
}

// Summary prints the final summary line.
// Example: "📋 Results: 2 passed, 1 failed, 0 errors".
func Summary(passed, failed, errored int) {
	mustPrintf("📋 Results: %d passed, %d failed, %d errors\n", passed, failed, errored)
}

// Separator prints a visual separator line.
func Separator() {
	mustPrintf("\n%s\n", strings.Repeat("─", 40))
}

// Blank prints an empty line for visual spacing.
func Blank() {
	mustPrintf("\n")
}

func statusEmoji(status string) string {
	switch strings.ToUpper(status) {
	case "PASS":
		return "✅"
	case "FAIL":
		return "❌"
	case "ERROR":
		return "⚠️"
	case "SKIP":
		return "⏭️"
	default:
		return "❓"
	}
}

// mu protects Output writes so concurrent goroutines (e.g. observer
// callbacks in EvaluateAll) do not interleave partial lines.
var mu sync.Mutex

func mustPrintf(format string, args ...any) {
	mu.Lock()
	_, _ = fmt.Fprintf(Output, format, args...)
	mu.Unlock()
}
