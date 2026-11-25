package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEnvironmentCleanupSessions(t *testing.T) {
	t.Parallel()

	var enabled Environment
	if err := yaml.Unmarshal([]byte("type: none\ncleanup_sessions: true\n"), &enabled); err != nil {
		t.Fatalf("unmarshal cleanup_sessions: %v", err)
	}
	if !enabled.CleanupSessions {
		t.Fatal("cleanup_sessions: true did not reach Environment.CleanupSessions")
	}

	var unset Environment
	if err := yaml.Unmarshal([]byte("type: none\n"), &unset); err != nil {
		t.Fatalf("unmarshal without cleanup_sessions: %v", err)
	}
	if unset.CleanupSessions {
		t.Fatal("cleanup_sessions must default to false")
	}
}
