//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/report"
)

// TestUserSimulator_DashScope exercises the SDK simulator and a real resumed
// OpenCode conversation together, using the runnable DashScope example.
func TestUserSimulator_DashScope(t *testing.T) {
	runUserSimulatorDashScope(t, "eval.yaml", "https://dashscope.aliyuncs.com/compatible-mode/v1")
}

// TestUserSimulator_DashScopeAnthropic uses Messages for the simulator while
// retaining Chat Completions for the agent; both cases verify session continuity.
func TestUserSimulator_DashScopeAnthropic(t *testing.T) {
	runUserSimulatorDashScope(t, "eval-anthropic.yaml", "https://dashscope.aliyuncs.com/apps/anthropic")
}

func runUserSimulatorDashScope(t *testing.T, evalFile, simulatorBaseURL string) {
	t.Helper()
	skipIfNotFullE2E(t)
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode not installed locally")
	}
	key := os.Getenv("DASHSCOPE_API_KEY")
	if key == "" {
		t.Skip("DASHSCOPE_API_KEY not set")
	}
	evalPath := filepath.Join(getProjectRoot(), "examples", "user-simulator", "evals", evalFile)
	outputDir := t.TempDir()
	preserveWorkspaceArtifacts(t, outputDir)
	result := Run(t, RunConfig{
		Env: []string{
			"SIMULATION_API_KEY=" + key,
			"SIMULATION_BASE_URL=" + simulatorBaseURL,
		},
		Timeout: 12 * time.Minute,
	}, "run", evalPath, "--output-dir", outputDir)
	if result.ExitCode != 0 {
		t.Fatalf("DashScope user simulation failed: exit=%d\nstdout=%s\nstderr=%s", result.ExitCode, result.Stdout, result.Stderr)
	}
	data, err := os.ReadFile(filepath.Join(outputDir, "iteration-1", "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rpt report.Input
	if err := json.Unmarshal(data, &rpt); err != nil {
		t.Fatal(err)
	}
	if len(rpt.CaseResults) != 2 {
		t.Fatalf("case count = %d, want 2", len(rpt.CaseResults))
	}
	for _, c := range rpt.CaseResults {
		t.Run(c.CaseID, func(t *testing.T) {
			if c.Status != "PASS" {
				t.Fatalf("case status = %s, want PASS", c.Status)
			}
			turns := c.TurnResults
			switch c.CaseID {
			case "mixed-turns":
				if len(turns) != 3 || turns[0].Source != "fixed" || turns[1].Source != "simulated" || turns[2].Source != "fixed" {
					t.Fatalf("mixed turn sources = %+v", turns)
				}
			case "autonomous-turns":
				if len(turns) < 2 || len(turns) > 6 || turns[0].Source != "fixed" || turns[len(turns)-1].StopReason == "" {
					t.Fatalf("autonomous turns or stop reason = %+v", turns)
				}
				for _, turn := range turns[1:] {
					if turn.Source != "simulated" {
						t.Fatalf("autonomous reply source = %s", turn.Source)
					}
				}
			default:
				t.Fatalf("unexpected case %q", c.CaseID)
			}
			var sessionID string
			for i := range turns {
				path := filepath.Join(outputDir, "iteration-1", c.CaseID, "with_skill", "outputs", "agent", "run", fmt.Sprintf("turn-%d", i+1), "stdout.jsonl")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				decoder := json.NewDecoder(bytes.NewReader(data))
				for {
					var event struct {
						SessionID string `json:"sessionID"`
					}
					if err := decoder.Decode(&event); err == io.EOF {
						break
					} else if err != nil {
						t.Fatal(err)
					}
					if event.SessionID == "" {
						continue
					}
					found = true
					if sessionID == "" {
						sessionID = event.SessionID
					} else if event.SessionID != sessionID {
						t.Fatalf("turn %d changed session: got %s, want %s", i+1, event.SessionID, sessionID)
					}
				}
				if !found {
					t.Fatalf("turn %d has no session evidence", i+1)
				}
			}
		})
	}
}

// TestSkillUpper_UserSimulator_DashScope verifies the canonical Skill can scaffold
// mixed and autonomous evals; the configured agent_judge inspects generated files.
func TestSkillUpper_UserSimulator_DashScope(t *testing.T) {
	skipIfNotFullE2E(t)
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode not installed locally")
	}
	if os.Getenv("DASHSCOPE_API_KEY") == "" {
		t.Skip("DASHSCOPE_API_KEY not set")
	}
	root := getProjectRoot()
	outputDir := t.TempDir()
	preserveWorkspaceArtifacts(t, outputDir)
	result := Run(t, RunConfig{
		Env: []string{
			"DASHSCOPE_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1",
			"PATH=" + filepath.Dir(binaryPath) + string(os.PathListSeparator) + os.Getenv("PATH"),
		},
		Timeout: 12 * time.Minute,
	}, "run", filepath.Join(root, "skills", "skill-upper", "evals", "eval-dashscope.yaml"),
		"--output-dir", outputDir)
	if result.ExitCode != 0 {
		t.Fatalf("Skill scaffolding failed: exit=%d\nstdout=%s\nstderr=%s", result.ExitCode, result.Stdout, result.Stderr)
	}
	data, err := os.ReadFile(filepath.Join(outputDir, "iteration-1", "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rpt report.Input
	if err := json.Unmarshal(data, &rpt); err != nil {
		t.Fatal(err)
	}
	if len(rpt.CaseResults) != 1 {
		t.Fatalf("case count = %d, want 1", len(rpt.CaseResults))
	}
	c := rpt.CaseResults[0]
	if c.CaseID != "scaffold-with-user-simulator" || c.Status != "PASS" || c.Grading == nil {
		t.Fatalf("scaffolding result = %+v", c)
	}
}
