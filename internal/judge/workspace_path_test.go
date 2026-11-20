package judge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
)

// Root aliases must fail in both directions on every host. ".\" is a root
// alias on Windows and a literal filename on POSIX; the shared grammar treats
// it as root everywhere, so files_not_exist cannot accidentally pass.
func TestRuleBased_FileRules_RejectRootAliasesOnEveryHost(t *testing.T) {
	ws := t.TempDir()
	invalid := []string{"", "   ", ".", "./", `.\`, "/", `\`, "foo/.."}

	for _, path := range invalid {
		exist := NewRuleBasedJudge(config.JudgeConfig{
			Success: []config.Rule{{FilesExist: []string{path}}},
		})
		r, err := exist.Evaluate(context.Background(), Input{WorkspacePath: ws})
		assertNoError(t, err)
		assertStatus(t, r, StatusFail)

		notExist := NewRuleBasedJudge(config.JudgeConfig{
			Success: []config.Rule{{FilesNotExist: []string{path}}},
		})
		r, err = notExist.Evaluate(context.Background(), Input{WorkspacePath: ws})
		assertNoError(t, err)
		assertStatus(t, r, StatusFail)
	}
}

func TestRuleBased_FileRules_EmptySliceFails(t *testing.T) {
	ws := t.TempDir()

	r, err := NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{FilesExist: []string{}}},
	}).Evaluate(context.Background(), Input{WorkspacePath: ws})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)

	r, err = NewRuleBasedJudge(config.JudgeConfig{
		Success: []config.Rule{{FilesNotExist: []string{}}},
	}).Evaluate(context.Background(), Input{WorkspacePath: ws})
	assertNoError(t, err)
	assertStatus(t, r, StatusFail)
}

func TestRuleBased_ToolCalled_EmptyNameFails(t *testing.T) {
	for _, name := range []string{"", "   "} {
		r, err := NewRuleBasedJudge(config.JudgeConfig{
			Success: []config.Rule{{ToolCalled: &config.ToolCalledRule{Name: name}}},
		}).Evaluate(context.Background(), Input{})
		assertNoError(t, err)
		assertStatus(t, r, StatusFail)
	}
}

// The runtime and validation share the same grammar, so the expect layer
// rejects a root path too instead of silently resolving it to the workspace.
func TestCheckExpect_FilePathsUseTheSameGrammar(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "out.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{".", `.\`, "/"} {
		r := CheckExpect(&config.Expect{FilesExist: []string{path}}, Input{WorkspacePath: ws})
		if r.Passed {
			t.Errorf("expect.files_exist %q passed, want failure", path)
		}
		if len(r.Failures) == 0 || !strings.Contains(r.Failures[0].Detail, "invalid path") {
			t.Errorf("expect.files_exist %q failures = %+v, want invalid path", path, r.Failures)
		}
	}

	// A normal relative path still resolves as before.
	r := CheckExpect(&config.Expect{FilesExist: []string{"out.txt"}}, Input{WorkspacePath: ws})
	if !r.Passed {
		t.Fatalf("expect.files_exist out.txt failures = %+v, want pass", r.Failures)
	}
}
