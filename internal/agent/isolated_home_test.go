package agent

import (
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

func TestIsolatedHomeCredentials(t *testing.T) {
	for _, tc := range []struct {
		name  string
		agent Agent
		env   map[string]string
		want  string
	}{
		{"Claude configured key", NewClaudeCodeAgent(Config{APIKey: "test-key"}), nil, ""},
		{"Codex configured key", NewCodexAgent(Config{APIKey: "test-key"}), nil, ""},
		{"Claude environment token", NewClaudeCodeAgent(Config{}), map[string]string{"ANTHROPIC_AUTH_TOKEN": "test-token"}, ""},
		{"Codex environment key", NewCodexAgent(Config{}), map[string]string{"OPENAI_API_KEY": "test-key"}, ""},
		{"Claude missing key", NewClaudeCodeAgent(Config{}), nil, "personal CLI logins are not copied"},
		{"Codex missing key", NewCodexAgent(Config{}), nil, "personal CLI logins are not copied"},
		{"unsupported engine", NewQoderCLIAgent(Config{}), nil, "supports only claude_code and codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"OPENAI_API_KEY": "", "ANTHROPIC_API_KEY": "", "ANTHROPIC_AUTH_TOKEN": ""}
			maps.Copy(env, tc.env)
			rt, err := runtime.NewRuntime(runtime.Config{Type: "none", IsolateHome: true, Delete: true, Env: env})
			if err != nil {
				t.Fatal(err)
			}
			if err := requireBashTargetShell(rt); err != nil {
				t.Skip(err)
			}
			if err := rt.Create(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = rt.Close() })
			err = CheckIsolatedHome(context.Background(), rt, tc.agent)
			if tc.want == "" && err != nil {
				t.Fatal(err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestIsolatedHomeCredentialsWithShellTracing(t *testing.T) {
	const fixtureValue = "test-value-that-must-not-be-logged"
	for _, tc := range []struct{ source, key string }{
		{"inherited", "ANTHROPIC_API_KEY"},
		{"inherited", "ANTHROPIC_AUTH_TOKEN"},
		{"inherited", "OPENAI_API_KEY"},
		{"configured", "ANTHROPIC_API_KEY"},
		{"configured", "ANTHROPIC_AUTH_TOKEN"},
		{"configured", "OPENAI_API_KEY"},
	} {
		t.Run(tc.source+"/"+tc.key, func(t *testing.T) {
			env := map[string]string{"OPENAI_API_KEY": "", "ANTHROPIC_API_KEY": "", "ANTHROPIC_AUTH_TOKEN": ""}
			env[tc.key] = fixtureValue
			if tc.source == "inherited" {
				t.Setenv("SHELLOPTS", "xtrace")
			} else {
				env["SHELLOPTS"] = "xtrace"
			}
			rt, err := runtime.NewRuntime(runtime.Config{Type: "none", IsolateHome: true, Delete: true, Env: env})
			if err != nil {
				t.Fatal(err)
			}
			if err := requireBashTargetShell(rt); err != nil {
				t.Skip(err)
			}
			if err := rt.Create(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = rt.Close() })
			var ag Agent = NewClaudeCodeAgent(Config{})
			if tc.key == "OPENAI_API_KEY" {
				ag = NewCodexAgent(Config{})
			}
			output := captureStdout(t, func() {
				err = CheckIsolatedHome(context.Background(), rt, ag)
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(output, fixtureValue) {
				t.Fatal("credential value appeared in runtime logs")
			}
			if !strings.Contains(output, "+ test -n") {
				t.Fatal("credential probe did not run with shell tracing")
			}
		})
	}
}

type isolatedCodexTestRuntime struct{ codexTestRuntime }

func (*isolatedCodexTestRuntime) IsolatedHome() bool { return true }

func TestIsolatedHomeCodexInitializesAuthOnRunAndResume(t *testing.T) {
	for _, sessionID := range []string{"", "session-1"} {
		t.Run("session="+sessionID, func(t *testing.T) {
			rt := &isolatedCodexTestRuntime{codexTestRuntime: codexTestRuntime{workspace: t.TempDir(), execResult: runtime.ExecResult{Stdout: `{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"OK"}}`}}}
			ag := NewCodexAgent(Config{APIKey: "test-key"})
			_, err := ag.RunTurn(context.Background(), rt, ExecOptions{}, transcript.Message{Role: transcript.RoleUser, Content: "hi", Turn: 1}, sessionID)
			if err != nil {
				t.Fatal(err)
			}
			loginIndex, runIndex := -1, -1
			for i, command := range rt.commands {
				if strings.Contains(command, "codex login --with-api-key") {
					loginIndex = i
				}
				if strings.Contains(command, "codex exec") {
					runIndex = i
				}
				if strings.Contains(command, "test-key") {
					t.Fatal("key was placed in a command")
				}
			}
			if loginIndex < 0 || runIndex <= loginIndex {
				t.Fatalf("login must precede exec: %v", rt.commands)
			}
		})
	}
}
