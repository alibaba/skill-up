//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
)

// getMultiTurnSessionTestdataDir returns e2e/testdata/multiturn-session.
func getMultiTurnSessionTestdataDir() string {
	return filepath.Join(getProjectRoot(), "e2e", "testdata", "multiturn-session")
}

// TestPipeline_MultiTurnSessionResume drives a real two-turn evaluation against
// a mock that behaves like qodercli where session state is concerned: it writes
// its transcript into $HOME/.qoder/projects/<project-key>/, answers the first
// turn through a Skill (tool call first, prose last), drops a sub-agent
// transcript inside the same project tree, and can only recall earlier context
// when it is resumed with -r.
//
// TMPDIR is pointed at a directory whose name contains an underscore, which is
// what macOS default temp directories look like — the framework has to derive
// the same project-key as the CLI for any of this to work.
//
// The case fails unless all three behaviours hold:
//   - turn 1's response is the final answer, not a "[tool: …]" placeholder,
//   - the framework picks the main session file, not the sub-agent transcript,
//   - turn 2 runs inside turn 1's session and can quote it back.
func TestPipeline_MultiTurnSessionResume(t *testing.T) {
	skipIfNoPOSIXShell(t)
	t.Parallel()

	fixtureDir := getMultiTurnSessionTestdataDir()
	evalPath := filepath.Join(fixtureDir, "evals", "eval.yaml")

	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create fake HOME bin dir: %v", err)
	}
	if err := os.Symlink(filepath.Join(fixtureDir, "session-engine.sh"), filepath.Join(binDir, "qodercli")); err != nil {
		t.Fatalf("symlink mock qodercli: %v", err)
	}

	// The workspace is created under TMPDIR, so this is how the test controls
	// the workspace path shape the project-key is derived from.
	tmpDir := filepath.Join(t.TempDir(), "skill_up_tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("create tmpdir: %v", err)
	}

	outputDir := t.TempDir()
	result := Run(t, RunConfig{
		Env: []string{
			"PATH=" + binDir + ":" + os.Getenv("PATH"),
			"HOME=" + home,
			"TMPDIR=" + tmpDir,
		},
		WorkDir: fixtureDir,
		Timeout: 120 * time.Second,
	}, "run", evalPath, "--no-delete", "--output-dir", outputDir)

	t.Logf("stdout:\n%s", result.Stdout)
	if result.Stderr != "" {
		t.Logf("stderr:\n%s", result.Stderr)
	}

	reportPath := filepath.Join(outputDir, "iteration-1", "report.json")
	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed to read report.json at %s: %v", reportPath, err)
	}
	var rpt report.Input
	if err := json.Unmarshal(reportData, &rpt); err != nil {
		t.Fatalf("failed to parse report.json: %v", err)
	}
	if len(rpt.CaseResults) != 1 {
		t.Fatalf("expected exactly 1 case result, got %d", len(rpt.CaseResults))
	}
	cr := rpt.CaseResults[0]

	if len(cr.TurnResults) != 2 {
		t.Fatalf("expected 2 turn results, got %d: %#v", len(cr.TurnResults), cr.TurnResults)
	}
	turn1, turn2 := cr.TurnResults[0], cr.TurnResults[1]

	if strings.Contains(turn1.Response, "[tool:") {
		t.Errorf("turn 1 response is a tool placeholder instead of the answer: %q", turn1.Response)
	}
	if !strings.Contains(turn1.Response, "alidocs.dingtalk.com") {
		t.Errorf("turn 1 response should carry the Skill's answer, got %q", turn1.Response)
	}
	const recallMarker = "context-recall: Explain GRADING-POLICY tiering."
	if !strings.Contains(turn2.Response, recallMarker) {
		t.Errorf("turn 2 did not resume turn 1's session (want %q), got %q", recallMarker, turn2.Response)
	}

	if cr.Grading == nil {
		t.Fatalf("expected grading to be non-nil")
	}
	if cr.Grading.Status != judge.StatusPass {
		t.Errorf("expected case status PASS, got %q (assertions: %#v)", cr.Grading.Status, cr.Grading.AssertionResults)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
}
