package compare

import (
	"fmt"
	"strings"
)

// RenderText renders a human-readable comparison result.
func RenderText(result Result) string {
	var b strings.Builder
	view := newComparisonView(result)
	fmt.Fprintf(&b, "%s -> %s\n", result.Baseline.Label, result.Candidate.Label)
	fmt.Fprintf(&b, "Sources: %s -> %s\n", result.Baseline.Path, result.Candidate.Path)
	b.WriteString("Run summary\n")
	for _, row := range view.Metrics {
		fmt.Fprintf(&b, "%s: %s -> %s (%s)\n", row.Name, row.Old, row.New, row.Delta)
	}
	fmt.Fprintf(&b, "Shared cases: %d; added: %d; removed: %d\n", result.Coverage.Shared, result.Coverage.Added, result.Coverage.Removed)
	if result.Coverage.Added+result.Coverage.Removed > 0 {
		b.WriteString("Different case sets: run-level deltas include unpaired cases.\n")
	}

	b.WriteString("\nMetadata differences\n")
	for _, row := range view.Metadata {
		fmt.Fprintf(&b, "%s: %s -> %s (%s)\n", row.Name, row.Old, row.New, row.Delta)
	}

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
