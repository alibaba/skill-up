package config

import (
	"errors"
	"fmt"
	"regexp"
)

var judgeIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// JudgePlan is the resolved set of judges for one case.
type JudgePlan struct {
	Judges []JudgeConfig
	Multi  bool
}

// ResolveJudgePlan applies case-level replacement and preserves legacy judge merging.
func ResolveJudgePlan(eval *EvalConfig, caseCfg *CaseConfig) (JudgePlan, error) {
	if (eval.JudgesSet || eval.Judges != nil) && (eval.JudgeSet || eval.Judge.Type != "") {
		return JudgePlan{}, errors.New("eval cannot configure both judge and judges")
	}
	if (caseCfg.JudgesSet || caseCfg.Judges != nil) && (caseCfg.JudgeSet || caseCfg.Judge.Type != "") {
		return JudgePlan{}, errors.New("case cannot configure both judge and judges")
	}
	selected, single, err := chooseJudgeConfig(eval, caseCfg)
	if err != nil {
		return JudgePlan{}, err
	}
	if selected == nil {
		return JudgePlan{Judges: []JudgeConfig{single}}, nil
	}
	return makeJudgeListPlan(selected)
}

func chooseJudgeConfig(eval *EvalConfig, caseCfg *CaseConfig) (*[]JudgeConfig, JudgeConfig, error) {
	switch {
	case caseCfg.JudgesSet || caseCfg.Judges != nil:
		if caseCfg.Judges == nil {
			return nil, JudgeConfig{}, errors.New("judges must contain at least one member")
		}
		return caseCfg.Judges, JudgeConfig{}, nil
	case caseCfg.JudgeSet || caseCfg.Judge.Type != "":
		if eval.Judges != nil && caseCfg.Judge.Type == "" {
			return nil, JudgeConfig{}, errors.New("case judge.type is required when overriding eval judges")
		}
		legacy := caseCfg.Judge
		if eval.Judges == nil {
			legacy = mergeJudgePlanConfig(eval.Judge, legacy)
		}
		return nil, legacy, nil
	case eval.JudgesSet || eval.Judges != nil:
		if eval.Judges == nil {
			return nil, JudgeConfig{}, errors.New("judges must contain at least one member")
		}
		return eval.Judges, JudgeConfig{}, nil
	default:
		return nil, eval.Judge, nil
	}
}

func makeJudgeListPlan(selected *[]JudgeConfig) (JudgePlan, error) {
	if len(*selected) == 0 {
		return JudgePlan{}, errors.New("judges must contain at least one member")
	}
	seen := make(map[string]bool, len(*selected))
	plan := JudgePlan{Multi: true, Judges: make([]JudgeConfig, len(*selected))}
	for i, member := range *selected {
		if !judgeIDPattern.MatchString(member.ID) {
			return JudgePlan{}, fmt.Errorf("judges[%d].id must be a safe lowercase identifier", i)
		}
		if seen[member.ID] {
			return JudgePlan{}, fmt.Errorf("duplicate judge id %q", member.ID)
		}
		seen[member.ID] = true
		plan.Judges[i] = member
	}
	return plan, nil
}

func mergeJudgePlanConfig(global, local JudgeConfig) JudgeConfig {
	if local.Type == "" {
		return global
	}
	if local.Model == "" {
		local.Model = global.Model
	}
	if local.PassThreshold == nil {
		local.PassThreshold = global.PassThreshold
	}
	if local.TimeoutSeconds == nil {
		local.TimeoutSeconds = global.TimeoutSeconds
	}
	return local
}
