package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/pkg/transcript"
)

func TestMaterializeJudgeContext_StandardProfileUsesFileRefs(t *testing.T) {
	artifactDir := filepath.Join(t.TempDir(), "judge", "run")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}

	mc, err := MaterializeJudgeContext(context.Background(), &mockJudgeTestRuntime{}, nil, Input{
		FinalMessage:  "final answer",
		WorkspaceDiff: "diff --git a/a b/a\n",
		Transcript: transcript.Transcript{
			{Role: transcript.RoleUser, Content: "prompt", Turn: 1},
		},
	}, artifactDir)
	if err != nil {
		t.Fatalf("MaterializeJudgeContext returned error: %v", err)
	}
	if mc.Manifest.Profile != "standard" {
		t.Fatalf("profile = %q, want standard", mc.Manifest.Profile)
	}
	if mc.Manifest.MaterializedDir != judgeContextArtifactDir {
		t.Fatalf("materialized_dir = %q, want %q", mc.Manifest.MaterializedDir, judgeContextArtifactDir)
	}
	if mc.Manifest.RuntimeDir != "" {
		t.Fatalf("runtime_dir should be omitted from persisted manifest, got %q", mc.Manifest.RuntimeDir)
	}
	assertMaterialMode(t, mc.Manifest.Materials, "final_message", "include")
	assertMaterialMode(t, mc.Manifest.Materials, "transcript", "file_ref")
	assertMaterialMode(t, mc.Manifest.Materials, "workspace_diff", "file_ref")
	assertMaterialMode(t, mc.Manifest.Materials, "skill_source", "omit")
	assertMaterialMode(t, mc.Manifest.Materials, "skill_usage", "include")
	assertMaterialPath(t, mc.Manifest.Materials, "transcript", "judge/context/transcript.json")
	if _, err := os.Stat(filepath.Join(mc.Dir, "transcript.json")); err != nil {
		t.Fatalf("transcript material missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mc.Dir, "workspace.diff")); err != nil {
		t.Fatalf("workspace diff material missing: %v", err)
	}
}

func TestResolveJudgeContext_DefaultProfileDoesNotOverrideMinimalSkillSource(t *testing.T) {
	cfg := config.DefaultEvalConfig()
	if cfg.Judge.Context == nil {
		t.Fatal("default judge context is nil")
	}

	standard := resolveJudgeContext(cfg.Judge.Context)
	if standard.profile != judgeContextProfileStandard || standard.skillSourceMode != judgeContextModeFileRef {
		t.Fatalf("standard defaults = profile %q, skill_source %q; want standard, file_ref", standard.profile, standard.skillSourceMode)
	}

	cfg.Judge.Context.Profile = judgeContextProfileMinimal
	minimal := resolveJudgeContext(cfg.Judge.Context)
	if minimal.skillSourceMode != judgeContextModeOmit {
		t.Fatalf("minimal skill_source = %q, want omit", minimal.skillSourceMode)
	}
}

func TestMaterializeJudgeContext_SnapshotsEvaluatedSkillSource(t *testing.T) {
	skillDir := writeSkillSourceFixture(t)
	mc, err := MaterializeJudgeContext(context.Background(), &mockJudgeTestRuntime{}, nil, Input{
		Configuration: "with_skill",
		SkillSources:  []SkillSource{{Path: skillDir}},
		SkillUsage: &SkillUsageEvidence{
			Status:   SkillUsageTriggered,
			Reliable: true,
			Evidence: []string{"observed explicit Skill tool call"},
		},
	}, filepath.Join(t.TempDir(), "judge", "run"))
	if err != nil {
		t.Fatalf("MaterializeJudgeContext returned error: %v", err)
	}
	assertMaterialMode(t, mc.Manifest.Materials, "skill_source", "file_ref")
	assertMaterialMode(t, mc.Manifest.Materials, "skill_usage", "include")

	indexData, err := os.ReadFile(filepath.Join(mc.Dir, "skill_source", "index.json"))
	if err != nil {
		t.Fatalf("read skill source index: %v", err)
	}
	var index skillSourceIndex
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("parse skill source index: %v", err)
	}
	assertSkillSourceIndex(t, index, mc.Dir)
}

func TestCaptureSkillSources_FreezesPreExecutionContent(t *testing.T) {
	skillDir := t.TempDir()
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("# Before agent run\n"), 0o600); err != nil {
		t.Fatalf("write initial SKILL.md: %v", err)
	}
	captured, err := CaptureSkillSources([]SkillSource{{Path: skillDir}}, nil)
	if err != nil {
		t.Fatalf("CaptureSkillSources returned error: %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("# Mutated after capture\n"), 0o600); err != nil {
		t.Fatalf("mutate SKILL.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(skillDir, "output"), 0o755); err != nil {
		t.Fatalf("create post-capture output: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "output", "report.json"), []byte(`{"generated":true}`), 0o600); err != nil {
		t.Fatalf("write post-capture output: %v", err)
	}

	mc, err := MaterializeJudgeContext(context.Background(), &mockJudgeTestRuntime{}, nil, Input{
		Configuration: "with_skill",
		SkillSources:  captured,
	}, filepath.Join(t.TempDir(), "judge", "run"))
	if err != nil {
		t.Fatalf("MaterializeJudgeContext returned error: %v", err)
	}
	index := readSkillSourceIndex(t, mc.Dir)
	if len(index.Skills) != 1 || len(index.Skills[0].Files) != 1 || index.Skills[0].Files[0].Path != "SKILL.md" {
		t.Fatalf("post-capture files leaked into snapshot: %#v", index)
	}
	materialPath := index.Skills[0].Files[0].MaterialPath
	content, err := os.ReadFile(filepath.Join(mc.Dir, "skill_source", filepath.FromSlash(materialPath)))
	if err != nil {
		t.Fatalf("read captured SKILL.md: %v", err)
	}
	if string(content) != "# Before agent run\n" {
		t.Fatalf("materialized mutable source instead of snapshot: %q", content)
	}
}

func writeSkillSourceFixture(t *testing.T) string {
	t.Helper()
	skillDir := t.TempDir()
	files := map[string][]byte{
		"SKILL.md":            []byte("# Calculator\nFollow references/rules.md.\n"),
		"references/rules.md": []byte("Use one operation at a time.\n"),
		"scripts/add.py":      []byte("print(1 + 2)\n"),
		"assets/blob.bin":     {0x00, 0xff, 0x01},
		"evals/secret.yaml":   []byte("must-not-be-materialized: true\n"),
	}
	for rel, content := range files {
		path := filepath.Join(skillDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("must-not-be-read\n"), 0o600); err != nil {
		t.Fatalf("write external symlink target: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(skillDir, "linked-secret.md")); err != nil {
		t.Fatalf("create source symlink: %v", err)
	}
	return skillDir
}

func assertSkillSourceIndex(t *testing.T, index skillSourceIndex, materializedDir string) {
	t.Helper()
	if index.Configuration != "with_skill" || len(index.Skills) != 1 {
		t.Fatalf("unexpected skill source index: %#v", index)
	}
	indexed := make(map[string]skillSourceIndexFile)
	for _, file := range index.Skills[0].Files {
		indexed[file.Path] = file
		if len(file.SHA256) != 64 {
			t.Fatalf("%s has invalid SHA-256 %q", file.Path, file.SHA256)
		}
	}
	if _, ok := indexed["evals/secret.yaml"]; ok {
		t.Fatal("evals directory must not appear in the evaluated Skill source index")
	}
	if _, ok := indexed["linked-secret.md"]; ok {
		t.Fatal("symlinked files must not appear in the evaluated Skill source index")
	}
	for _, rel := range []string{"SKILL.md", "references/rules.md", "scripts/add.py"} {
		file, ok := indexed[rel]
		if !ok || file.MaterialPath == "" {
			t.Fatalf("readable source %q not materialized: %#v", rel, file)
		}
		if _, err := os.Stat(filepath.Join(materializedDir, "skill_source", filepath.FromSlash(file.MaterialPath))); err != nil {
			t.Fatalf("materialized source %q missing: %v", rel, err)
		}
	}
	if blob := indexed["assets/blob.bin"]; blob.MaterialPath != "" {
		t.Fatalf("binary asset should be indexed without copied content: %#v", blob)
	}
}

func readSkillSourceIndex(t *testing.T, materializedDir string) skillSourceIndex {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(materializedDir, "skill_source", "index.json"))
	if err != nil {
		t.Fatalf("read skill source index: %v", err)
	}
	var index skillSourceIndex
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatalf("parse skill source index: %v", err)
	}
	return index
}

func TestMaterializeJudgeContext_WithoutSkillOmitsSkillSource(t *testing.T) {
	skillDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Skill\n"), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	mc, err := MaterializeJudgeContext(context.Background(), &mockJudgeTestRuntime{}, &config.JudgeContextConfig{
		SkillSource: "file_ref",
	}, Input{
		Configuration: "without_skill",
		SkillSources:  []SkillSource{{Path: skillDir}},
	}, filepath.Join(t.TempDir(), "judge", "run"))
	if err != nil {
		t.Fatalf("MaterializeJudgeContext returned error: %v", err)
	}
	assertMaterialMode(t, mc.Manifest.Materials, "skill_source", "omit")
}

func TestMaterializeJudgeContext_UnavailableSkillSourceDoesNotFailEvaluation(t *testing.T) {
	mc, err := MaterializeJudgeContext(context.Background(), &mockJudgeTestRuntime{}, nil, Input{
		Configuration: "with_skill",
		SkillSources:  []SkillSource{{Path: filepath.Join(t.TempDir(), "missing")}},
	}, filepath.Join(t.TempDir(), "judge", "run"))
	if err != nil {
		t.Fatalf("optional Skill source snapshot must degrade gracefully: %v", err)
	}
	assertMaterialMode(t, mc.Manifest.Materials, "skill_source", "omit")
}

func TestMaterializeJudgeContext_MinimalProfileOmitsLargeMaterials(t *testing.T) {
	mc, err := MaterializeJudgeContext(context.Background(), &mockJudgeTestRuntime{}, &config.JudgeContextConfig{
		Profile: "minimal",
		Limits:  &config.JudgeContextLimits{MaxBytes: 5},
	}, Input{
		FinalMessage:  "0123456789",
		WorkspaceDiff: "diff",
		Transcript: transcript.Transcript{
			{Role: transcript.RoleUser, Content: "prompt", Turn: 1},
		},
	}, filepath.Join(t.TempDir(), "judge", "run"))
	if err != nil {
		t.Fatalf("MaterializeJudgeContext returned error: %v", err)
	}
	assertMaterialMode(t, mc.Manifest.Materials, "final_message", "truncate")
	assertMaterialMode(t, mc.Manifest.Materials, "transcript", "omit")
	assertMaterialMode(t, mc.Manifest.Materials, "workspace_diff", "omit")
	assertMaterialMode(t, mc.Manifest.Materials, "skill_source", "omit")
	var final ContextMaterial
	for _, m := range mc.Materials {
		if m.Key == "final_message" {
			final = m
		}
	}
	if final.InlineContent != "01234" || !final.Truncated {
		t.Fatalf("expected truncated final message, got content=%q truncated=%v", final.InlineContent, final.Truncated)
	}
}

func TestMaterializeJudgeContext_IncludeAutoDowngradesToFileRef(t *testing.T) {
	mc, err := MaterializeJudgeContext(context.Background(), &mockJudgeTestRuntime{}, &config.JudgeContextConfig{
		Transcript: "include",
		Limits:     &config.JudgeContextLimits{MaxBytes: 10},
	}, Input{
		FinalMessage: "ok",
		Transcript: transcript.Transcript{
			{Role: transcript.RoleUser, Content: "this is a long prompt", Turn: 1},
		},
	}, filepath.Join(t.TempDir(), "judge", "run"))
	if err != nil {
		t.Fatalf("MaterializeJudgeContext returned error: %v", err)
	}
	assertMaterialMode(t, mc.Manifest.Materials, "transcript", "file_ref")
	for _, material := range mc.Materials {
		if material.Key == "transcript" && material.InlineContent != "" {
			t.Fatalf("auto-downgraded transcript should not be inlined")
		}
	}
}

func TestMaterializeJudgeContext_AttachmentCopy(t *testing.T) {
	skillDir := t.TempDir()
	attachmentBytes := []byte{0xff, 0x00, 'a', '\n'}
	if err := os.WriteFile(filepath.Join(skillDir, "diff-result.bin"), attachmentBytes, 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}

	mc, err := MaterializeJudgeContext(context.Background(), &mockJudgeTestRuntime{}, &config.JudgeContextConfig{
		Profile: "minimal",
		Attachments: []config.JudgeContextAttachment{
			{Path: "diff-result.bin", Label: "diff_result"},
		},
	}, Input{
		SkillDir:      skillDir,
		FinalMessage:  "ok",
		WorkspacePath: t.TempDir(),
	}, filepath.Join(t.TempDir(), "judge", "run"))
	if err != nil {
		t.Fatalf("MaterializeJudgeContext returned error: %v", err)
	}
	assertMaterialMode(t, mc.Manifest.Materials, "attachment", "file_ref")
	attachmentPath := filepath.Join(mc.Dir, "attachments", "01-diff-result.bin")
	copiedBytes, err := os.ReadFile(attachmentPath)
	if err != nil {
		t.Fatalf("attachment copy missing: %v", err)
	}
	if !bytes.Equal(copiedBytes, attachmentBytes) {
		t.Fatalf("attachment bytes changed: got %v want %v", copiedBytes, attachmentBytes)
	}
	assertMaterialPath(t, mc.Manifest.Materials, "attachment", "judge/context/attachments/01-diff-result.bin")
}

func TestBuildJudgePrompt_StandardProfileDoesNotInlineLargeTranscript(t *testing.T) {
	// ~128 KB transcript body, far above any inline limit. Under the default
	// (standard) profile it must be referenced by path, never inlined, so the
	// judge prompt stays small and cannot trigger ARG_MAX (proposal R1/R4).
	marker := strings.Repeat("TRANSCRIPT-BODY-", 8000)
	mc, err := MaterializeJudgeContext(context.Background(), &mockJudgeTestRuntime{}, nil, Input{
		FinalMessage: "done",
		Transcript: transcript.Transcript{
			{Role: transcript.RoleAssistant, Content: marker, Turn: 1},
		},
	}, filepath.Join(t.TempDir(), "judge", "run"))
	if err != nil {
		t.Fatalf("MaterializeJudgeContext returned error: %v", err)
	}

	assertMaterialMode(t, mc.Manifest.Materials, "transcript", "file_ref")

	prompt := buildJudgePrompt(context.Background(), []string{"criterion A"}, mc)
	if strings.Contains(prompt, marker) {
		t.Fatal("standard-profile judge prompt must not inline the full transcript body")
	}
	if len(prompt) > 8*1024 {
		t.Fatalf("judge prompt too large: %d bytes (transcript should be referenced by path)", len(prompt))
	}
	if _, err := os.Stat(filepath.Join(mc.Dir, "transcript.json")); err != nil {
		t.Fatalf("transcript file reference missing: %v", err)
	}
}

func assertMaterialMode(t *testing.T, materials []ContextMaterialManifest, key, mode string) {
	t.Helper()
	for _, material := range materials {
		if material.Key == key {
			if material.Mode != mode {
				t.Fatalf("%s mode = %q, want %q", key, material.Mode, mode)
			}
			return
		}
	}
	t.Fatalf("material %q not found in %#v", key, materials)
}

func assertMaterialPath(t *testing.T, materials []ContextMaterialManifest, key, want string) {
	t.Helper()
	for _, material := range materials {
		if material.Key == key {
			if material.Path != want {
				t.Fatalf("%s path = %q, want %q", key, material.Path, want)
			}
			return
		}
	}
	t.Fatalf("material %q not found in %#v", key, materials)
}
