package judge

import (
	"path/filepath"

	"github.com/alibaba/skill-up/internal/config"
)

// SkillInfo describes a judge Skill configured for agent_judge.
type SkillInfo struct {
	Source string `json:"source,omitempty"`
	Path   string `json:"path,omitempty"`
	Target string `json:"target,omitempty"`
	Name   string `json:"name,omitempty"`
}

// SkillInfosFromRefs converts configured Skill refs into report-safe metadata.
func SkillInfosFromRefs(refs []config.SkillRef) []SkillInfo {
	if len(refs) == 0 {
		return nil
	}
	infos := make([]SkillInfo, 0, len(refs))
	for _, ref := range refs {
		name := skillInfoName(ref.Path)
		infos = append(infos, SkillInfo{
			Source: ref.Source,
			Path:   ref.Path,
			Target: ref.Target,
			Name:   name,
		})
	}
	return infos
}

func skillInfoName(path string) string {
	clean := filepath.Clean(path)
	if clean == "" || clean == "." {
		return ""
	}
	return filepath.Base(clean)
}
