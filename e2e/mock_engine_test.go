//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMockEngine_LongPrompts(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		prompt  string
		want    string
		attempt string
	}{
		{
			name:    "initial judge",
			prompt:  "## Criteria\n[criterion-1] Check marker\n## Required Response Format (JSON)\n",
			want:    `"criterion_id":"criterion-1","evidence"`,
			attempt: "initial\n",
		},
		{
			name:    "correction judge",
			prompt:  "## Agent Judge Output Correction\n{\"criterion_id\":\"criterion-1\"}\n",
			want:    `"criterion_id":"criterion-1","passed":true`,
			attempt: "correction\n",
		},
		{name: "null", prompt: "NULL\n", want: "null pointer bug"},
		{name: "review", prompt: "REVIEW\n", want: "Code review complete"},
		{name: "hello", prompt: "HELLO\n", want: "Hello!"},
		{name: "bug", prompt: "BUG\n", want: "Bug analysis complete"},
		{name: "error", prompt: "ERROR\n", want: "Error analysis:"},
		{name: "fail", prompt: "FAIL\n", want: "Analysis indicates LGTM"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stateFile := filepath.Join(t.TempDir(), "attempts.txt")
			cmd := exec.CommandContext(t.Context(), "bash", filepath.Join(getMockEngineTestdataDir(), "engine.sh"))
			cmd.Env = append(os.Environ(), "MOCK_JUDGE_CORRECTION_RETRY=1", "MOCK_JUDGE_STATE_FILE="+stateFile)
			// Exceed the pipe buffer so early grep matches cannot depend on scheduling.
			cmd.Stdin = strings.NewReader(tt.prompt + strings.Repeat("padding\n", 128*1024))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("mock engine failed: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), tt.want) {
				t.Fatalf("expected %q in response, got %.200s", tt.want, out)
			}
			if tt.attempt != "" {
				state, err := os.ReadFile(stateFile)
				if err != nil || string(state) != tt.attempt {
					t.Fatalf("expected attempt %q, got %q (error: %v)", tt.attempt, state, err)
				}
			}
		})
	}
}
