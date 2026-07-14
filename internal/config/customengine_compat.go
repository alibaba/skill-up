package config

import "github.com/alibaba/skill-up/internal/customengine"

// CustomEngineConfig is retained as a compatibility alias; new code should
// use customengine.Config.
type CustomEngineConfig = customengine.Config

// CustomLocalConfig is retained as a compatibility alias; new code should use
// customengine.LocalConfig.
type CustomLocalConfig = customengine.LocalConfig

// CustomHTTPConfig is retained as a compatibility alias; new code should use
// customengine.HTTPConfig.
type CustomHTTPConfig = customengine.HTTPConfig

// CustomHTTPFile is retained as a compatibility alias; new code should use
// customengine.HTTPFile.
type CustomHTTPFile = customengine.HTTPFile

// TemplateToken is retained as a compatibility alias; new code should use
// customengine.TemplateToken.
type TemplateToken = customengine.TemplateToken

// ParseTemplateToken forwards to the canonical custom-engine parser. New code
// should use customengine.ParseTemplateToken.
func ParseTemplateToken(inner string) TemplateToken {
	return customengine.ParseTemplateToken(inner)
}

// WorkspaceRelPathSafe forwards to the canonical custom-engine path check. New
// code should use customengine.WorkspaceRelPathSafe.
func WorkspaceRelPathSafe(p string) bool {
	return customengine.WorkspaceRelPathSafe(p)
}
