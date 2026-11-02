package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/alibaba/skill-up/internal/config"
)

func TestConvertEvalToCase(t *testing.T) {
	t.Parallel()

	eval := anthropicEval{
		ID:             1,
		Prompt:         "Test prompt",
		ExpectedOutput: "Test expected output",
		Files:          []string{"file1.txt", "file2.go"},
		Expectations:   []string{"Expect 1", "Expect 2"},
	}

	result := convertEvalToCase(eval, "test-skill")

	if result.ID != "case-1" {
		t.Errorf("expected ID 'case-1', got '%s'", result.ID)
	}

	if result.Title != "test-skill - 1" {
		t.Errorf("expected Title 'test-skill - 1', got '%s'", result.Title)
	}

	if result.Description != "Test expected output" {
		t.Errorf("expected Description 'Test expected output', got '%s'", result.Description)
	}

	if result.Input.Prompt != "Test prompt" {
		t.Errorf("expected Input.Prompt 'Test prompt', got '%s'", result.Input.Prompt)
	}

	if len(result.Context.Files) != 0 {
		t.Errorf("expected no context files before they are read, got %d", len(result.Context.Files))
	}

	if result.Judge.Type != agentJudgeType {
		t.Errorf("expected Judge.Type 'agent_judge', got '%s'", result.Judge.Type)
	}

	if len(result.Judge.Criteria) != 2 {
		t.Errorf("expected 2 criteria, got %d", len(result.Judge.Criteria))
	}
}

func TestConvertEvalToCase_WithNoFiles(t *testing.T) {
	t.Parallel()

	eval := anthropicEval{
		ID:             42,
		Prompt:         "Another test",
		ExpectedOutput: "Another output",
		Files:          nil,
		Expectations:   []string{"Single expectation"},
	}

	result := convertEvalToCase(eval, "my-skill")

	if result.ID != "case-42" {
		t.Errorf("expected ID 'case-42', got '%s'", result.ID)
	}

	if len(result.Context.Files) != 0 {
		t.Errorf("expected 0 files in context, got %d", len(result.Context.Files))
	}
}

func TestConvertAnthropicEvals_ReadsFiles(t *testing.T) {
	t.Parallel()

	skillDir := t.TempDir()
	writeSkillFile(t, skillDir, "file1.txt", "alpha\n")
	writeSkillFile(t, skillDir, "dir/file2.go", "package two\n")
	evals := anthropicEvals{SkillName: "s", Evals: []anthropicEval{{ID: 1, Prompt: "p", Files: []string{"file1.txt", "dir/file2.go"}}}}

	cases, err := convertAnthropicEvals(evals, skillDir, nil)
	if err != nil {
		t.Fatalf("convertAnthropicEvals: %v", err)
	}
	if got := cases[0].Context.Files; len(got) != 2 || got["file1.txt"] != "alpha\n" || got["dir/file2.go"] != "package two\n" {
		t.Errorf("context files = %q, want the contents of file1.txt and dir/file2.go", got)
	}
}

func TestConvertAnthropicEvals_RejectsUnreadableFiles(t *testing.T) {
	t.Parallel()

	skillDir := t.TempDir()
	outside := t.TempDir()
	writeSkillFile(t, outside, "secret.txt", "outside\n")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(skillDir, "file-link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(skillDir, "dir-link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, tc := range []struct {
		file string
		want string
	}{
		{file: "evals/files/missing.csv", want: "failed to read file"},
		{file: "../outside.txt", want: "relative path inside the skill directory"},
		{file: "/etc/hosts", want: "relative path inside the skill directory"},
		{file: "file-link.txt", want: "failed to read file"},
		{file: "dir-link/secret.txt", want: "failed to read file"},
	} {
		evals := anthropicEvals{SkillName: "s", Evals: []anthropicEval{{ID: 7, Prompt: "p", Files: []string{tc.file}}}}
		if _, err := convertAnthropicEvals(evals, skillDir, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("file %q: error = %v, want one containing %q", tc.file, err, tc.want)
		}
	}
}

// A case removed by the case-name filter must not fail the run because one of
// its files is missing.
func TestConvertAnthropicEvals_SkipsFilesOfUnselectedCases(t *testing.T) {
	t.Parallel()

	skillDir := t.TempDir()
	writeSkillFile(t, skillDir, "ok.txt", "ok\n")
	evals := anthropicEvals{SkillName: "s", Evals: []anthropicEval{
		{ID: 1, Prompt: "p", Files: []string{"ok.txt"}},
		{ID: 2, Prompt: "p", Files: []string{"missing.txt"}},
	}}

	keep := importedCaseID(1)
	cases, err := convertAnthropicEvals(evals, skillDir, func(caseID string) bool { return caseID == keep })
	if err != nil {
		t.Fatalf("convertAnthropicEvals: %v", err)
	}
	if len(cases) != 1 || cases[0].ID != keep || cases[0].Context.Files["ok.txt"] != "ok\n" {
		t.Fatalf("cases = %+v, want only case-1 with ok.txt read", cases)
	}
}

// A missing file in any eval fails the import before any case file is written.
func TestImportCmd_MissingFileWritesNothing(t *testing.T) {
	t.Parallel()

	skillDir := t.TempDir()
	writeSkillFile(t, skillDir, "SKILL.md", "---\nname: s\n---\n")
	writeSkillFile(t, skillDir, "evals/files/ok.txt", "ok\n")
	writeSkillFile(t, skillDir, "evals/evals.json", `{"skill_name":"s","evals":[{"id":1,"prompt":"p","files":["evals/files/ok.txt"]},{"id":2,"prompt":"p","files":["evals/files/missing.txt"]}]}`)

	cmd := newImportCmd()
	cmd.SetArgs([]string{filepath.Join(skillDir, "evals", "evals.json")})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "missing.txt") {
		t.Fatalf("import error = %v, want one naming missing.txt", err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "evals", "cases")); !os.IsNotExist(err) {
		t.Fatalf("cases directory exists after a failed import (stat err %v)", err)
	}
}

func writeSkillFile(t *testing.T, skillDir, rel, content string) {
	t.Helper()
	target := filepath.Join(skillDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestGenerateEvalConfig(t *testing.T) {
	t.Parallel()

	result := generateEvalConfig([]string{"case-1", "case-2", "case-3"}, "/tmp/test-skill/evals")

	if result.SchemaVersion != "v1alpha1" {
		t.Errorf("expected SchemaVersion 'v1alpha1', got '%s'", result.SchemaVersion)
	}

	if result.Environment.Type != "none" {
		t.Errorf("expected Environment.Type 'none', got '%s'", result.Environment.Type)
	}

	if len(result.Skills) != 1 {
		t.Errorf("expected 1 skill, got %d", len(result.Skills))
	}

	if result.Skills[0].Source != localPathSkillSource {
		t.Errorf("expected Skill Source 'local_path', got '%s'", result.Skills[0].Source)
	}

	if result.Skills[0].Path != currentDirectorySkill {
		t.Errorf("expected Skill Path '.', got '%s'", result.Skills[0].Path)
	}

	if result.Engine.Name != testEngineClaudeCode {
		t.Errorf("expected Engine.Name 'claude_code', got '%s'", result.Engine.Name)
	}

	if result.Engine.Model.Provider != "" {
		t.Errorf("expected Model.Provider empty (no hardcoded default), got '%s'", result.Engine.Model.Provider)
	}

	if result.Engine.Model.Name != "" {
		t.Errorf("expected Model.Name empty (no hardcoded default), got '%s'", result.Engine.Model.Name)
	}

	if len(result.Cases.Files) != 3 {
		t.Errorf("expected 3 case files, got %d", len(result.Cases.Files))
	}

	expectedCases := []string{"evals/cases/case-1.yaml", "evals/cases/case-2.yaml", "evals/cases/case-3.yaml"}
	for i, expected := range expectedCases {
		if result.Cases.Files[i] != expected {
			t.Errorf("expected case file %s, got %s", expected, result.Cases.Files[i])
		}
	}

	if result.Cases.Parallelism != 1 {
		t.Errorf("expected Parallelism 1, got %d", result.Cases.Parallelism)
	}

	if result.Judge.Type != agentJudgeType {
		t.Errorf("expected Judge.Type 'agent_judge', got '%s'", result.Judge.Type)
	}
}

func TestGenerateEvalConfig_ZeroCases(t *testing.T) {
	t.Parallel()

	result := generateEvalConfig(nil, "/tmp/test-skill/evals")

	if len(result.Cases.Files) != 0 {
		t.Errorf("expected 0 case files, got %d", len(result.Cases.Files))
	}
}

func TestGenerateEvalConfig_UsesImportedCaseIDs(t *testing.T) {
	t.Parallel()

	result := generateEvalConfig([]string{"case-7", "case-42"}, "/tmp/test-skill/evals")

	expectedCases := []string{"evals/cases/case-7.yaml", "evals/cases/case-42.yaml"}
	for i, expected := range expectedCases {
		if result.Cases.Files[i] != expected {
			t.Errorf("expected case file %s, got %s", expected, result.Cases.Files[i])
		}
	}
}

func TestConvertEvalToCase_IDFormatting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id       int
		expected string
	}{
		{1, "case-1"},
		{10, "case-10"},
		{100, "case-100"},
	}

	for _, tc := range tests {
		eval := anthropicEval{ID: tc.id, Prompt: "p", ExpectedOutput: "e"}
		result := convertEvalToCase(eval, "s")
		if result.ID != tc.expected {
			t.Errorf("ID %d: expected '%s', got '%s'", tc.id, tc.expected, result.ID)
		}
	}
}

func TestImport_FromEvalsJSON(t *testing.T) {
	t.Parallel()

	evalsPath := filepath.Join("testdata", "evals.json")

	data, err := os.ReadFile(evalsPath)
	if err != nil {
		t.Fatalf("failed to read test evals.json: %v", err)
	}

	var evalsData anthropicEvals
	if err := json.Unmarshal(data, &evalsData); err != nil {
		t.Fatalf("failed to parse evals.json: %v", err)
	}

	if evalsData.SkillName != "test-skill" {
		t.Errorf("expected skill_name 'test-skill', got '%s'", evalsData.SkillName)
	}

	if len(evalsData.Evals) != 2 {
		t.Errorf("expected 2 evals, got %d", len(evalsData.Evals))
	}
}

// ---------------------------------------------------------------------------
// importCmd cobra command — end-to-end tests
// ---------------------------------------------------------------------------

// newImportCmd creates an isolated cobra command wired to importCmd.RunE for testing.
func newImportCmd() *cobra.Command {
	cmd := &cobra.Command{RunE: importCmd.RunE}
	cmd.Flags().String("output", "", "")
	return cmd
}

func makeEvalsJSON(t *testing.T, skillName string, ids []int) string {
	t.Helper()

	type evalEntry struct {
		ID             int      `json:"id"`
		Prompt         string   `json:"prompt"`
		ExpectedOutput string   `json:"expected_output"`
		Expectations   []string `json:"expectations"`
	}
	type evalsFile struct {
		SkillName string      `json:"skill_name"`
		Evals     []evalEntry `json:"evals"`
	}
	evals := make([]evalEntry, len(ids))
	for i, id := range ids {
		evals[i] = evalEntry{
			ID:             id,
			Prompt:         "prompt for case",
			ExpectedOutput: "expected output",
			Expectations:   []string{"should work"},
		}
	}
	data, err := json.Marshal(evalsFile{SkillName: skillName, Evals: evals})
	if err != nil {
		t.Fatalf("makeEvalsJSON: %v", err)
	}
	return string(data)
}

func TestImportCmd_Success(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	evalsPath := filepath.Join(dir, "evals.json")
	if err := os.WriteFile(evalsPath, []byte(makeEvalsJSON(t, "my-skill", []int{1, 2})), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newImportCmd()
	cmd.SetOut(&bytes.Buffer{})

	if err := cmd.RunE(cmd, []string{evalsPath}); err != nil {
		t.Fatalf("import command error: %v", err)
	}

	// cases/case-1.yaml and cases/case-2.yaml should be created in the same dir as evals.json
	for _, name := range []string{"case-1.yaml", "case-2.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, "cases", name)); err != nil {
			t.Errorf("expected %s to be created: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "eval.yaml")); err != nil {
		t.Errorf("expected eval.yaml to be created: %v", err)
	}
}

func TestImportCmd_CustomOutputDir(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	outDir := t.TempDir()

	evalsPath := filepath.Join(srcDir, "evals.json")
	if err := os.WriteFile(evalsPath, []byte(makeEvalsJSON(t, "my-skill", []int{1})), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newImportCmd()
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Flags().Set("output", outDir); err != nil {
		t.Fatalf("set output flag: %v", err)
	}

	if err := cmd.RunE(cmd, []string{evalsPath}); err != nil {
		t.Fatalf("import command error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "cases", "case-1.yaml")); err != nil {
		t.Errorf("expected cases/case-1.yaml in output dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "eval.yaml")); err != nil {
		t.Errorf("expected eval.yaml in output dir: %v", err)
	}
}

// TestImportCmd_OutputIsSkillRoot verifies that when --output points to the
// skill root directory (containing SKILL.md), the generated eval.yaml uses
// case paths relative to that root (e.g. "cases/case-1.yaml") rather than
// duplicating the directory name (e.g. "my-skill/cases/case-1.yaml").
func TestImportCmd_OutputIsSkillRoot(t *testing.T) {
	t.Parallel()

	// Create a temp dir that acts as the skill root.
	skillRoot := t.TempDir()

	// Place a SKILL.md so FindSkillDir recognises this directory.
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte("---\nname: test\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Write evals.json somewhere else.
	srcDir := t.TempDir()
	evalsPath := filepath.Join(srcDir, "evals.json")
	if err := os.WriteFile(evalsPath, []byte(makeEvalsJSON(t, "my-skill", []int{1, 2})), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newImportCmd()
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Flags().Set("output", skillRoot); err != nil {
		t.Fatalf("set output flag: %v", err)
	}

	if err := cmd.RunE(cmd, []string{evalsPath}); err != nil {
		t.Fatalf("import command error: %v", err)
	}

	// Read the generated eval.yaml and verify case paths.
	evalYaml, err := os.ReadFile(filepath.Join(skillRoot, "eval.yaml"))
	if err != nil {
		t.Fatalf("read eval.yaml: %v", err)
	}

	// Paths must NOT contain the skill root basename as a prefix.
	for _, bad := range []string{
		filepath.Base(skillRoot) + "/cases/",
	} {
		if bytes.Contains(evalYaml, []byte(bad)) {
			t.Errorf("eval.yaml contains doubled prefix %q:\n%s", bad, evalYaml)
		}
	}

	// Paths should be "cases/case-N.yaml" (relative to skill root).
	for _, want := range []string{"cases/case-1.yaml", "cases/case-2.yaml"} {
		if !bytes.Contains(evalYaml, []byte(want)) {
			t.Errorf("eval.yaml missing expected path %q:\n%s", want, evalYaml)
		}
	}
}

func TestComputeOutputRelPrefix_SkillRoot(t *testing.T) {
	t.Parallel()

	skillRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte("---\nname: test\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := computeOutputRelPrefix(skillRoot)
	if got != "." {
		t.Errorf("expected \".\", got %q", got)
	}
}

func TestImportCmd_MissingSkillName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	evalsPath := filepath.Join(dir, "evals.json")
	// skill_name is intentionally omitted
	if err := os.WriteFile(evalsPath, []byte(`{"evals":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newImportCmd()
	cmd.SetOut(&bytes.Buffer{})

	err := cmd.RunE(cmd, []string{evalsPath})
	if err == nil {
		t.Fatal("expected error for missing skill_name, got nil")
	}
}

func TestImportCmd_InvalidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	evalsPath := filepath.Join(dir, "evals.json")
	if err := os.WriteFile(evalsPath, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newImportCmd()
	cmd.SetOut(&bytes.Buffer{})

	err := cmd.RunE(cmd, []string{evalsPath})
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestImportCmd_MissingFile(t *testing.T) {
	t.Parallel()
	cmd := newImportCmd()
	cmd.SetOut(&bytes.Buffer{})

	err := cmd.RunE(cmd, []string{"/nonexistent/evals.json"})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

// evals.json file paths are relative to the Skill root (Anthropic skill-creator
// schema); the imported case must carry each file's bytes, binary included.
func TestImportCmd_ReadsFilesFromSkillRoot(t *testing.T) {
	t.Parallel()

	skillDir := t.TempDir()
	writeSkillFile(t, skillDir, "SKILL.md", "---\nname: csv-skill\n---\n")
	writeSkillFile(t, skillDir, "evals/files/data.csv", "a,b\n1,2\n")
	binary := string([]byte{0x00, 0xff, 0xfe, 'P', 'D', 'F'})
	writeSkillFile(t, skillDir, "evals/files/blob.bin", binary)
	evalsJSON := `{"skill_name":"csv-skill","evals":[{"id":1,"prompt":"Summarize the csv","files":["evals/files/data.csv","evals/files/blob.bin"],"expectations":["Mentions the csv"]}]}`
	evalsPath := filepath.Join(skillDir, "evals", "evals.json")
	writeSkillFile(t, skillDir, "evals/evals.json", evalsJSON)

	cmd := newImportCmd()
	cmd.SetArgs([]string{evalsPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("import: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(skillDir, "evals", "cases", "case-1.yaml"))
	if err != nil {
		t.Fatalf("read imported case: %v", err)
	}
	var caseCfg config.CaseConfig
	if err := yaml.Unmarshal(data, &caseCfg); err != nil {
		t.Fatalf("parse imported case: %v", err)
	}
	if got := caseCfg.Context.Files["evals/files/data.csv"]; got != "a,b\n1,2\n" {
		t.Errorf("data.csv content = %q, want the file's bytes", got)
	}
	if got := caseCfg.Context.Files["evals/files/blob.bin"]; got != binary {
		t.Errorf("blob.bin content = %q, want %q", got, binary)
	}
}
