// Package judge — factory.go provides a factory function to create Judge instances
// based on JudgeConfig.
package judge

import (
	"errors"
	"fmt"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/runtime"
)

// NewJudge creates a Judge instance based on the provided JudgeConfig.
//
// Case-level judge config takes precedence over global judge config.
// The caller should merge configs before calling this function.
//
// For agent_judge type, Agent and Runtime must be provided.
// For script type, ScriptPath must be non-empty.
// For rule_based type, success/failure rules are used from the config.
func NewJudge(cfg config.JudgeConfig, ag agent.Agent, rt runtime.Runtime) (Judge, error) {
	switch cfg.Type {
	case "rule_based":
		return NewRuleBasedJudge(cfg), nil

	case "script":
		if cfg.ScriptPath == "" {
			return nil, errors.New("script judge requires script_path")
		}
		return &ScriptJudge{
			ScriptPath:     cfg.ScriptPath,
			TimeoutSeconds: derefInt(cfg.TimeoutSeconds),
			Runtime:        rt,
		}, nil

	case "agent_judge":
		if ag == nil {
			return nil, errors.New("agent_judge requires an Agent")
		}
		return NewAgentJudgeWithContextAndSkills(ag, rt, cfg.Model, cfg.Criteria, cfg.PassThreshold, cfg.Context, derefInt(cfg.TimeoutSeconds), SkillInfosFromRefs(cfg.Skills)), nil

	case "":
		// No judge configured — return nil, caller should handle
		return nil, nil

	default:
		return nil, fmt.Errorf("unknown judge type: %q", cfg.Type)
	}
}

// derefInt dereferences a *int pointer, returning 0 if nil.
func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// MergeJudgeConfig merges case-level judge config over global judge config.
// Case-level fields take precedence when non-zero.
//
// When case-level specifies a Type, it is treated as a full override:
// only Model and PassThreshold fall back to global if unset.
// Other fields (Criteria, Success, Failure, ScriptPath) come from
// the case-level config exclusively — this is intentional to allow
// cases to define completely independent judge strategies.
func MergeJudgeConfig(global, caseLevel config.JudgeConfig) config.JudgeConfig {
	// If case has a type, use it entirely
	if caseLevel.Type != "" {
		merged := caseLevel
		// Fill in defaults from global if case doesn't specify
		if merged.Model == "" {
			merged.Model = global.Model
		}
		if merged.PassThreshold == nil {
			merged.PassThreshold = global.PassThreshold
		}
		if merged.TimeoutSeconds == nil {
			merged.TimeoutSeconds = global.TimeoutSeconds
		}
		merged.Context = MergeJudgeContextConfig(global.Context, caseLevel.Context)
		return merged
	}
	// No case-level judge, use global
	return global
}

// MergeJudgeContextConfig merges case-level judge.context over eval-level
// judge.context. Unset fields inherit from the global context.
func MergeJudgeContextConfig(global, caseLevel *config.JudgeContextConfig) *config.JudgeContextConfig {
	if global == nil && caseLevel == nil {
		return nil
	}
	if global == nil {
		return cloneJudgeContextConfig(caseLevel)
	}
	if caseLevel == nil {
		return cloneJudgeContextConfig(global)
	}

	merged := cloneJudgeContextConfig(global)
	if caseLevel.Profile != "" {
		merged.Profile = caseLevel.Profile
	}
	if caseLevel.FinalMessage != "" {
		merged.FinalMessage = caseLevel.FinalMessage
	}
	if caseLevel.Transcript != "" {
		merged.Transcript = caseLevel.Transcript
	}
	if caseLevel.WorkspaceDiff != "" {
		merged.WorkspaceDiff = caseLevel.WorkspaceDiff
	}
	if caseLevel.GeneratedFiles != "" {
		merged.GeneratedFiles = caseLevel.GeneratedFiles
	}
	merged.Limits = mergeJudgeContextLimits(global.Limits, caseLevel.Limits)
	if caseLevel.Attachments != nil {
		merged.Attachments = append([]config.JudgeContextAttachment(nil), caseLevel.Attachments...)
	}
	return merged
}

func mergeJudgeContextLimits(global, caseLevel *config.JudgeContextLimits) *config.JudgeContextLimits {
	if global == nil && caseLevel == nil {
		return nil
	}
	var limits config.JudgeContextLimits
	if global != nil {
		limits = *global
	}
	if caseLevel != nil {
		if caseLevel.MaxBytes > 0 {
			limits.MaxBytes = caseLevel.MaxBytes
		}
		if caseLevel.TranscriptMaxTurns > 0 {
			limits.TranscriptMaxTurns = caseLevel.TranscriptMaxTurns
		}
		if caseLevel.WorkspaceDiffMaxLines > 0 {
			limits.WorkspaceDiffMaxLines = caseLevel.WorkspaceDiffMaxLines
		}
	}
	return &limits
}

func cloneJudgeContextConfig(in *config.JudgeContextConfig) *config.JudgeContextConfig {
	if in == nil {
		return nil
	}
	out := *in
	if in.Limits != nil {
		limits := *in.Limits
		out.Limits = &limits
	}
	out.Attachments = append([]config.JudgeContextAttachment(nil), in.Attachments...)
	return &out
}
