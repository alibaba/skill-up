package judge

import (
	"bytes"
	"context"
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
	assertMaterialPath(t, mc.Manifest.Materials, "transcript", "judge/context/transcript.json")
	if _, err := os.Stat(filepath.Join(mc.Dir, "transcript.json")); err != nil {
		t.Fatalf("transcript material missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mc.Dir, "workspace.diff")); err != nil {
		t.Fatalf("workspace diff material missing: %v", err)
	}
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
