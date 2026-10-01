package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/logging"
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

	cleaned := false
	defer func() {
		if cleaned {
			return
		}
		_ = w.Close()
		os.Stdout = orig
		_ = r.Close()
	}()

	runErr := fn()
	_ = w.Close()
	os.Stdout = orig
	if copyErr := <-done; copyErr != nil {
		t.Fatalf("copy stdout: %v", copyErr)
	}
	_ = r.Close()
	cleaned = true

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
	if !strings.Contains(output, "eval.yaml is valid") || !strings.Contains(output, "loaded 4 case(s)") {
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

func TestValidateCommandSkillFlagWarnsWithoutStrict(t *testing.T) {
	var logBuf bytes.Buffer
	restore := logging.SetOutputForTest(&logBuf)
	defer restore()

	cmd := &cobra.Command{}
	cmd.Flags().String("skill", "testdata/skill-integrity/broken-skill", "")
	cmd.SetContext(context.Background())

	output, err := captureStdout(t, func() error {
		return validateCmd.RunE(cmd, nil)
	})
	if err != nil {
		t.Fatalf("validate RunE returned error: %v", err)
	}
	if strings.Contains(output, "skill content check passed") {
		t.Fatalf("validate output = %q, want no success message for a broken skill", output)
	}
	for _, want := range []string{"skill integrity", "frontmatter field 'name'", "references/missing.md", "does not exist on disk"} {
		if !strings.Contains(logBuf.String(), want) {
			t.Errorf("validate warnings missing %q:\n%s", want, logBuf.String())
		}
	}
}

func TestValidateCommandSkillFlagStrictFails(t *testing.T) {
	var logBuf bytes.Buffer
	restore := logging.SetOutputForTest(&logBuf)
	defer restore()

	cmd := &cobra.Command{}
	cmd.Flags().String("skill", "testdata/skill-integrity/broken-skill", "")
	cmd.Flags().Bool("strict", true, "")
	cmd.SetContext(context.Background())

	_, err := captureStdout(t, func() error {
		return validateCmd.RunE(cmd, nil)
	})
	if err == nil || !strings.Contains(err.Error(), "skill integrity") {
		t.Fatalf("validate error = %v, want skill integrity failure", err)
	}
}

func TestValidateCommandSkillFlagStrictAcceptsTrickyButValidSkill(t *testing.T) {
	var logBuf bytes.Buffer
	restore := logging.SetOutputForTest(&logBuf)
	defer restore()

	cmd := &cobra.Command{}
	cmd.Flags().String("skill", "testdata/skill-integrity/valid-tricky-skill", "")
	cmd.Flags().Bool("strict", true, "")
	cmd.SetContext(context.Background())

	output, err := captureStdout(t, func() error {
		return validateCmd.RunE(cmd, nil)
	})
	if err != nil {
		t.Fatalf("validate RunE returned error: %v", err)
	}
	if !strings.Contains(output, "skill content check passed") {
		t.Fatalf("validate output = %q, want success message", output)
	}
	if strings.Contains(logBuf.String(), "skill integrity") {
		t.Fatalf("validate produced unexpected skill integrity warnings:\n%s", logBuf.String())
	}
}

func TestValidateCommandSkillFlagReportsNonSkillDirectory(t *testing.T) {
	var logBuf bytes.Buffer
	restore := logging.SetOutputForTest(&logBuf)
	defer restore()

	cmd := &cobra.Command{}
	cmd.Flags().String("skill", "testdata/skill-integrity", "")
	cmd.SetContext(context.Background())

	_, err := captureStdout(t, func() error {
		return validateCmd.RunE(cmd, nil)
	})
	if err != nil {
		t.Fatalf("validate RunE returned error: %v, err = want nil for a non-skill dir without --strict", err)
	}
	if !strings.Contains(logBuf.String(), "no SKILL.md found") {
		t.Fatalf("validate warnings missing \"no SKILL.md found\":\n%s", logBuf.String())
	}
}

func TestValidateCommandSkillFlagRejectsEvalPathArg(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("skill", "testdata/skill-integrity/broken-skill", "")
	cmd.SetContext(context.Background())

	_, err := captureStdout(t, func() error {
		return validateCmd.RunE(cmd, []string{"testdata/skill-integrity/broken-skill/evals/eval.yaml"})
	})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("validate error = %v, want mutual-exclusion failure", err)
	}
}

func TestValidateCommandStrictWithoutSkillFlagFails(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("strict", true, "")
	cmd.SetContext(context.Background())

	_, err := captureStdout(t, func() error {
		return validateCmd.RunE(cmd, []string{testEvalPath})
	})
	if err == nil || !strings.Contains(err.Error(), "--strict only applies") {
		t.Fatalf("validate error = %v, want --strict without --skill failure", err)
	}
}

func TestValidateCommandEvalValidationSkipsSkillCheck(t *testing.T) {
	var logBuf bytes.Buffer
	restore := logging.SetOutputForTest(&logBuf)
	defer restore()

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	output, err := captureStdout(t, func() error {
		return validateCmd.RunE(cmd, []string{"testdata/skill-integrity/broken-skill/evals/eval.yaml"})
	})
	if err != nil {
		t.Fatalf("validate RunE returned error: %v", err)
	}
	if !strings.Contains(output, "eval.yaml is valid") {
		t.Fatalf("validate output = %q, want success message", output)
	}
	if strings.Contains(logBuf.String(), "skill integrity") {
		t.Fatalf("eval validation must not run the skill content check:\n%s", logBuf.String())
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
