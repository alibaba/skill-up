package compare

import (
	"fmt"
	"strings"
)

// RenderText renders a human-readable comparison result.
func RenderText(result Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Run summary\n")
	fmt.Fprintf(&b, "pass rate: %.2f%% -> %.2f%% (%+.2f%%)\n", result.Run.Old.PassRate*100, result.Run.New.PassRate*100, result.Run.Delta.PassRate*100)
	fmt.Fprintf(&b, "cases: %d -> %d (%+d)\n", result.Run.Old.CaseCount, result.Run.New.CaseCount, result.Run.Delta.CaseCount)
	fmt.Fprintf(&b, "passed: %d -> %d (%+d)\n", result.Run.Old.PassCount, result.Run.New.PassCount, result.Run.Delta.PassCount)
	fmt.Fprintf(&b, "total tokens: %d -> %d (%+d)\n", result.Run.Old.TotalTokens, result.Run.New.TotalTokens, result.Run.Delta.TotalTokens)
	fmt.Fprintf(&b, "input tokens: %d -> %d (%+d)\n", result.Run.Old.InputTokens, result.Run.New.InputTokens, result.Run.Delta.InputTokens)
	fmt.Fprintf(&b, "output tokens: %d -> %d (%+d)\n", result.Run.Old.OutputTokens, result.Run.New.OutputTokens, result.Run.Delta.OutputTokens)
	fmt.Fprintf(&b, "duration ms: %d -> %d (%+d)\n", result.Run.Old.DurationMs, result.Run.New.DurationMs, result.Run.Delta.DurationMs)

	b.WriteString("\nMetadata differences\n")
	writeMetadataDiffs(&b, result.Metadata)

	b.WriteString("\nCase transitions\n")
	writeTransitions(&b, "fixed", result.Cases.Fixed)
	writeTransitions(&b, "regressed", result.Cases.Regressed)
	writeTransitions(&b, "changed", result.Cases.Changed)
	writeTransitions(&b, "unchanged", result.Cases.Unchanged)
	writeTransitions(&b, "added", result.Cases.Added)
	writeTransitions(&b, "removed", result.Cases.Removed)

	if result.Gates.Passed {
		b.WriteString("\nGates: passed\n")
	} else {
		b.WriteString("\nGates: failed\n")
		for _, failure := range result.Gates.Failures {
			fmt.Fprintf(&b, "- %s\n", failure)
		}
	}
	return b.String()
}

func writeMetadataDiffs(b *strings.Builder, metadata MetadataDiff) {
	writeChangedField(b, "skill name", metadata.SkillName)
	writeChangedField(b, "schema version", metadata.SchemaVersion)
	writeChangedField(b, "engine name", metadata.EngineName)
	writeChangedField(b, "model name", metadata.ModelName)
	writeChangedField(b, "start time", metadata.StartTime)
	writeChangedField(b, "end time", metadata.EndTime)
}

func writeChangedField[T comparable](b *strings.Builder, label string, diff FieldDiff[T]) {
	if diff.Changed {
		fmt.Fprintf(b, "%s: %v -> %v\n", label, diff.Old, diff.New)
	}
}

func writeTransitions(b *strings.Builder, label string, transitions []CaseTransition) {
	fmt.Fprintf(b, "%s (%d):", label, len(transitions))
	for _, transition := range transitions {
		fmt.Fprintf(b, " %s (%s)", transition.CaseID, transitionStatus(transition))
	}
	b.WriteByte('\n')
}

func transitionStatus(transition CaseTransition) string {
	switch {
	case transition.OldStatus == "":
		return fmt.Sprintf("-> %s", transition.NewStatus)
	case transition.NewStatus == "":
		return fmt.Sprintf("%s ->", transition.OldStatus)
	default:
		return fmt.Sprintf("%s -> %s", transition.OldStatus, transition.NewStatus)
	}
}
