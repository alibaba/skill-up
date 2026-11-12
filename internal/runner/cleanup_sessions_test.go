package runner

import (
	"testing"

	"github.com/alibaba/skill-up/internal/config"
)

func TestCleanupSessionsEnabled(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		opts EvaluateOptions
		cfg  *config.EvalConfig
		want bool
	}{
		{name: "off by default", cfg: &config.EvalConfig{}, want: false},
		{name: "flag", opts: EvaluateOptions{CleanupSessions: true}, cfg: &config.EvalConfig{}, want: true},
		{name: "eval config", cfg: &config.EvalConfig{Environment: config.Environment{CleanupSessions: true}}, want: true},
		{name: "nil config with flag", opts: EvaluateOptions{CleanupSessions: true}, want: true},
		{name: "nil config off", want: false},
	} {
		if got := cleanupSessionsEnabled(tc.opts, tc.cfg); got != tc.want {
			t.Errorf("%s: cleanupSessionsEnabled = %t, want %t", tc.name, got, tc.want)
		}
	}
}
