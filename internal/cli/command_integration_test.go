package cli

import (
	"bytes"
	"context"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestValidateCommandUncitedWithoutSkillFlagFails(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("uncited", true, "")
	cmd.SetContext(context.Background())

	_, err := captureStdout(t, func() error {
		return validateCmd.RunE(cmd, []string{testEvalPath})
	})
	if err == nil || !strings.Contains(err.Error(), "--uncited only applies") {
		t.Fatalf("validate error = %v, want --uncited without --skill failure", err)
	}
}

// writeUncitedSkill creates a skill whose frontmatter and citations are valid
// but which carries one attachment that SKILL.md never reaches.
func writeUncitedSkill(t *testing.T) string {
	t.Helper()
	return writeSkillTree(t, map[string]string{
		"SKILL.md":             "---\nname: uncited\ndescription: Valid skill with one unreachable attachment.\n---\n\nRead [the guide](references/guide.md).\n",
		"references/guide.md":  "# Guide\n",
		"references/orphan.md": "# Never cited\n",
	})
}

// writeSkillTree materializes a skill in a new temporary directory from a map
// of slash-separated relative paths to file contents and returns the
// directory.
func writeSkillTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestValidateCommandSkillFlagStrictIgnoresUncitedFilesByDefault(t *testing.T) {
	var logBuf bytes.Buffer
	restore := logging.SetOutputForTest(&logBuf)
	defer restore()

	cmd := &cobra.Command{}
	cmd.Flags().String("skill", writeUncitedSkill(t), "")
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
	if strings.Contains(logBuf.String(), "never references") {
		t.Fatalf("validate reported uncited files without --uncited:\n%s", logBuf.String())
	}
}

func TestValidateCommandSkillFlagUncitedStrictFails(t *testing.T) {
	var logBuf bytes.Buffer
	restore := logging.SetOutputForTest(&logBuf)
	defer restore()

	cmd := &cobra.Command{}
	cmd.Flags().String("skill", writeUncitedSkill(t), "")
	cmd.Flags().Bool("uncited", true, "")
	cmd.Flags().Bool("strict", true, "")
	cmd.SetContext(context.Background())

	_, err := captureStdout(t, func() error {
		return validateCmd.RunE(cmd, nil)
	})
	if err == nil || !strings.Contains(err.Error(), "skill integrity") {
		t.Fatalf("validate error = %v, want skill integrity failure", err)
	}
	for _, want := range []string{"never references", "references/orphan.md"} {
		if !strings.Contains(logBuf.String(), want) {
			t.Errorf("validate warnings missing %q:\n%s", want, logBuf.String())
		}
	}
	if strings.Contains(logBuf.String(), "references/guide.md") {
		t.Errorf("validate reported the cited references/guide.md:\n%s", logBuf.String())
	}
}

func TestValidateCommandSkillFlagDotFollowsIndirectCitations(t *testing.T) {
	var logBuf bytes.Buffer
	restore := logging.SetOutputForTest(&logBuf)
	defer restore()

	// Regression (#281 review, 2026-11-16): `validate --skill . --uncited
	// --strict`, run from inside the skill, reported references/details.md
	// as uncited although SKILL.md reaches it through references/guide.md;
	// the absolute path passed.
	t.Chdir(writeSkillTree(t, map[string]string{
		"SKILL.md":              "---\nname: indirect-chain\ndescription: Valid skill reaching an attachment through another one.\n---\n\nRead [the guide](references/guide.md).\n",
		"references/guide.md":   "# Guide\n\nRead [details](details.md) when needed.\n",
		"references/details.md": "# Details\n",
	}))

	cmd := &cobra.Command{}
	cmd.Flags().String("skill", ".", "")
	cmd.Flags().Bool("uncited", true, "")
	cmd.Flags().Bool("strict", true, "")
	cmd.SetContext(context.Background())

	output, err := captureStdout(t, func() error {
		return validateCmd.RunE(cmd, nil)
	})
	if err != nil {
		t.Fatalf("validate RunE returned error: %v\n%s", err, logBuf.String())
	}
	if !strings.Contains(output, "skill content check passed") {
		t.Fatalf("validate output = %q, want success message", output)
	}
}

func TestValidateCommandSkillFlagStrictAcceptsAtSignFileName(t *testing.T) {
	var logBuf bytes.Buffer
	restore := logging.SetOutputForTest(&logBuf)
	defer restore()

	// Regression (#281 review, 2026-11-16): the inline-code citation
	// `assets/icon@2x.png` was cut to "assets/icon" and failed --strict
	// although the file exists.
	dir := writeSkillTree(t, map[string]string{
		"SKILL.md":           "---\nname: retina-icon\ndescription: Valid skill citing an asset with an at sign in its name.\n---\n\nUse `assets/icon@2x.png` on high-DPI screens.\n",
		"assets/icon@2x.png": "png\n",
	})

	cmd := &cobra.Command{}
	cmd.Flags().String("skill", dir, "")
	cmd.Flags().Bool("strict", true, "")
	cmd.SetContext(context.Background())

	output, err := captureStdout(t, func() error {
		return validateCmd.RunE(cmd, nil)
	})
	if err != nil {
		t.Fatalf("validate RunE returned error: %v\n%s", err, logBuf.String())
	}
	if !strings.Contains(output, "skill content check passed") {
		t.Fatalf("validate output = %q, want success message", output)
	}
}

func TestValidateCommandSkillFlagStrictCodeSpanCitations(t *testing.T) {
	// Regression (#281 review, 2026-11-18): --strict failed two valid skills.
	// The file `scripts/build.sh --output assets/generated.png` writes was
	// reported as a missing attachment, and `references/my guide.md#setup`
	// was cut to a missing "references/my". A direct citation of a file that
	// really is missing must still fail.
	for _, tc := range []struct {
		name    string
		body    string
		files   map[string]string
		missing string // the citation reported as missing; empty means the check passes
	}{
		{
			name:  "command that writes an output file",
			body:  "Run `scripts/build.sh --output assets/generated.png` to render the chart.",
			files: map[string]string{"scripts/build.sh": "#!/bin/sh\n"},
		},
		{
			name:  "anchored file name with a space",
			body:  "See `references/my guide.md#setup` before you start.",
			files: map[string]string{"references/my guide.md": "# Guide\n\n## Setup\n"},
		},
		{
			name:    "missing direct citation",
			body:    "Read `references/missing.md` first.",
			missing: "references/missing.md",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logBuf bytes.Buffer
			restore := logging.SetOutputForTest(&logBuf)
			defer restore()

			files := map[string]string{"SKILL.md": "---\nname: code-spans\ndescription: Skill citing attachments in inline code.\n---\n\n" + tc.body + "\n"}
			maps.Copy(files, tc.files)
			cmd := &cobra.Command{}
			cmd.Flags().String("skill", writeSkillTree(t, files), "")
			cmd.Flags().Bool("strict", true, "")
			cmd.SetContext(context.Background())

			output, err := captureStdout(t, func() error {
				return validateCmd.RunE(cmd, nil)
			})
			if tc.missing != "" {
				if err == nil || !strings.Contains(err.Error(), "skill integrity") {
					t.Fatalf("validate error = %v, want skill integrity failure", err)
				}
				for _, want := range []string{tc.missing, "does not exist on disk"} {
					if !strings.Contains(logBuf.String(), want) {
						t.Errorf("validate warnings missing %q:\n%s", want, logBuf.String())
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("validate RunE returned error: %v\n%s", err, logBuf.String())
			}
			if !strings.Contains(output, "skill content check passed") {
				t.Fatalf("validate output = %q, want success message", output)
			}
		})
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
		"...",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("list-cases output missing %q:\n%s", want, output)
		}
	}
}

func TestListCasesCommandRunEPreservesUnicodePrompt(t *testing.T) {
	root := t.TempDir()
	writeAutoModeSkill(t, root)
	writeAutoModeEvalYAML(t, root, "unicode-prompt")
	prompt := strings.Repeat("中", 60)
	casePath := filepath.Join(root, "evals", "cases", "unicode-prompt.yaml")
	if err := os.WriteFile(casePath, []byte("id: unicode-prompt\ninput:\n  prompt: "+prompt+"\n"), 0o600); err != nil {
		t.Fatalf("write Unicode case: %v", err)
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	output, err := captureStdout(t, func() error {
		return listCasesCmd.RunE(cmd, []string{filepath.Join(root, "evals", "eval.yaml")})
	})
	if err != nil {
		t.Fatalf("list-cases RunE returned error: %v", err)
	}
	if !utf8.ValidString(output) || !strings.Contains(output, strings.Repeat("中", 47)+"...") {
		t.Fatalf("list-cases corrupted or incorrectly truncated Unicode prompt: %q", output)
	}
}
