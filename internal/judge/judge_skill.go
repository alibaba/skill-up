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
		infos = append(infos, SkillInfo{
			Source: ref.Source,
			Path:   ref.Path,
			Target: ref.Target,
			Name:   filepath.Base(filepath.Clean(ref.Path)),
		})
	}
	return infos
}
