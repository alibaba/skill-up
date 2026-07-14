package judge

import (
	"testing"

	"github.com/alibaba/skill-up/internal/config"
)

func TestSkillInfosFromRefs_OmitsDotName(t *testing.T) {
	t.Parallel()

	infos := SkillInfosFromRefs([]config.SkillRef{
		{Source: "local_path"},
		{Source: "local_path", Path: "."},
		{Source: "local_path", Path: "evals/fixtures/judge-skill"},
	})

	if len(infos) != 3 {
		t.Fatalf("SkillInfosFromRefs() returned %d infos, want 3", len(infos))
	}
	if infos[0].Name != "" || infos[1].Name != "" {
		t.Fatalf("dot or empty paths should not derive names: %#v", infos)
	}
	if infos[2].Name != "judge-skill" {
		t.Fatalf("third Name = %q, want judge-skill", infos[2].Name)
	}
}
