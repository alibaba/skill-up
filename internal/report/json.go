package report

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// JSONReporter writes machine-readable JSON results.
// JSON is the "fact source" — JUnit and HTML are derived views.
type JSONReporter struct {
	// OutputPath is the file path to write the JSON report.
	// If empty, writes to stdout.
	OutputPath string
}

// Write implements the Reporter interface.
func (r *JSONReporter) Write(_ context.Context, in Input) error {
	data, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}
	data = append(data, '\n')

	return writeToOutput(r.OutputPath, "json report", func(w io.Writer) error {
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("write json report: %w", err)
		}
		return nil
	})
}
