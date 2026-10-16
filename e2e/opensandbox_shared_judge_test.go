//go:build e2e

package e2e

import (
	"os"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
)

func TestOpenSandboxSharedJudgeEnvironment(t *testing.T) {
	if os.Getenv("SKILL_UP_OPENSANDBOX_JUDGE_E2E") != "1" {
		t.Skip("set SKILL_UP_OPENSANDBOX_JUDGE_E2E=1 with an OpenSandbox service")
	}
	if os.Getenv("OPENSANDBOX_API_KEY") == "" || os.Getenv("OPENSANDBOX_BASE_URL") == "" || os.Getenv("OPENSANDBOX_IMAGE") == "" {
		t.Fatal("OPENSANDBOX_API_KEY, OPENSANDBOX_BASE_URL and OPENSANDBOX_IMAGE are required")
	}
	runSharedJudgeEnvironment(t, config.Environment{Type: "opensandbox", Image: os.Getenv("OPENSANDBOX_IMAGE"), NetworkPolicy: "deny_all", ReadyTimeoutSeconds: 60})
}
