// Package report — helpers.go provides shared utility functions
// used across report writers.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// writeJSONFile marshals v as indented JSON and writes it to path.
// desc is a human-readable label used in error messages (e.g. "grading json").
func writeJSONFile(path string, v any, desc string) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", desc, err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, filePerm); err != nil {
		return fmt.Errorf("write %s %s: %w", desc, path, err)
	}
	return nil
}

// writeToOutput opens path for writing or falls back to stdout when path is empty.
func writeToOutput(path, desc string, fn func(w io.Writer) error) (err error) {
	if path == "" {
		return fn(os.Stdout)
	}

	f, ferr := os.Create(path)
	if ferr != nil {
		return fmt.Errorf("create %s file: %w", desc, ferr)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %s file: %w", desc, cerr)
		}
	}()

	return fn(f)
}
