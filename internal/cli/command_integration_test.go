package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = w

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&buf, r)
		done <- copyErr
	}()

	runErr := fn()
	_ = w.Close()
	os.Stdout = orig
	if copyErr := <-done; copyErr != nil {
		t.Fatalf("copy stdout: %v", copyErr)
	}
	_ = r.Close()

	return buf.String(), runErr
}

func TestValidateCommandRunEReportsLoadedCases(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	output, err := captureStdout(t, func() error {
		return validateCmd.RunE(cmd, []string{testEvalPath})
	})
	if err != nil {
		t.Fatalf("validate RunE returned error: %v", err)
	}
	if !strings.Contains(output, "eval.yaml is valid") || !strings.Contains(output, "loaded 3 case(s)") {
		t.Fatalf("validate output = %q, want success with case count", output)
	}
}

func TestValidateCommandRunEWrapsLoadErrors(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	_, err := captureStdout(t, func() error {
		return validateCmd.RunE(cmd, []string{"/does/not/exist/eval.yaml"})
	})
	if err == nil || !strings.Contains(err.Error(), "failed to load config") {
		t.Fatalf("validate error = %v, want failed to load config", err)
	}
}

func TestListCasesCommandRunEPrintsDefaultsAndTruncatesPrompt(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	output, err := captureStdout(t, func() error {
		return listCasesCmd.RunE(cmd, []string{testEvalPath})
	})
	if err != nil {
		t.Fatalf("list-cases RunE returned error: %v", err)
	}
	for _, want := range []string{
		"ID",
		"Title",
		"Tag",
		"analyze-directory",
		"functional_test",
		"Analyze the current directory using the code-st...",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("list-cases output missing %q:\n%s", want, output)
		}
	}
}
