package agent

import (
	"context"
	"fmt"

	"github.com/alibaba/skill-up/internal/credential"
)

func hasIsolatedHome(rt Runtime) bool {
	isolated, ok := rt.(interface{ IsolatedHome() bool })
	return ok && isolated.IsolatedHome()
}

// CheckIsolatedHome verifies supported adapters and explicit credentials without
// reading personal login state or contacting a provider.
func CheckIsolatedHome(ctx context.Context, rt Runtime, ag Agent) error {
	if !hasIsolatedHome(rt) {
		return nil
	}
	if err := requireBashTargetShell(rt); err != nil {
		return err
	}
	var opts ExecOptions
	var probe, key string
	// Test a presence marker so shell tracing cannot log credential values.
	switch a := ag.(type) {
	case *ClaudeCodeAgent:
		opts.Env = a.claudeRunEnvVars()
		probe = `test -n "${ANTHROPIC_API_KEY:+present}" || test -n "${ANTHROPIC_AUTH_TOKEN:+present}"`
		key = "ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN"
	case *CodexAgent:
		opts.Env = a.credentialEnvVars(credential.EnvOpenAIAPIKey, credential.EnvOpenAIBaseURL)
		probe = `test -n "${OPENAI_API_KEY:+present}"`
		key = "OPENAI_API_KEY"
	default:
		return fmt.Errorf("environment.isolate_home supports only claude_code and codex; got %s", ag.Name())
	}
	result, err := rt.Exec(ctx, probe, opts)
	if err != nil {
		return fmt.Errorf("check isolated-home credentials for %s: %w", ag.Name(), err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("environment.isolate_home requires %s for %s; personal CLI logins are not copied", key, ag.Name())
	}
	return nil
}

func initializeIsolatedCodexAuth(ctx context.Context, rt Runtime, opts ExecOptions) error {
	if !hasIsolatedHome(rt) {
		return nil
	}
	// The key travels through stdin, never argv; Codex stores it only in the
	// case-owned CODEX_HOME. Repeat for judges with their own credentials.
	result, err := rt.Exec(ctx, `printenv OPENAI_API_KEY | codex login --with-api-key`, opts)
	if err != nil {
		return fmt.Errorf("initialize isolated Codex API-key login: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("initialize isolated Codex API-key login: exit %d", result.ExitCode)
	}
	return nil
}
