package config

import (
	"testing"

	"github.com/alibaba/skill-up/internal/customengine"
)

func TestCustomEngineCompatibilityAliases(t *testing.T) {
	legacy := &CustomEngineConfig{
		Transport: "local",
		Local:     &CustomLocalConfig{Command: "agent"},
	}
	canonical := acceptCanonicalCustomEngineConfig(legacy)
	if canonical.Local == nil || canonical.Local.Command != "agent" {
		t.Fatalf("canonical config = %+v", canonical)
	}

	if got := ParseTemplateToken("FOO:-bar"); got != (customengine.TemplateToken{Name: "FOO", Default: "bar", HasDefault: true}) {
		t.Fatalf("ParseTemplateToken compatibility wrapper = %+v", got)
	}
	if !WorkspaceRelPathSafe("artifacts/*.json") || WorkspaceRelPathSafe("../secret") {
		t.Fatal("WorkspaceRelPathSafe compatibility wrapper changed behavior")
	}
}

func acceptCanonicalCustomEngineConfig(cfg *customengine.Config) *customengine.Config {
	return cfg
}
