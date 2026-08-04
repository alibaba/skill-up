package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	_ "unsafe"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/credential"
	"github.com/alibaba/skill-up/internal/evaluator"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

//go:linkname uiOutput github.com/alibaba/skill-up/internal/ui.Output
var uiOutput io.Writer

func TestRunner_InitWorkspace_UsesExplicitRunNumber(t *testing.T) {
	t.Parallel()

	r := NewRunner(&config.EvalConfig{}, nil, nil, credential.AgentInitParams{})
	tmpDir := t.TempDir()

	if err := r.InitWorkspace(tmpDir, "test-skill", 99); err != nil {
		t.Fatalf("InitWorkspace failed: %v", err)
	}

	if got := r.workspace.IterationNum; got != 99 {
		t.Fatalf("IterationNum = %d, want 99", got)
	}
	if got := r.workspace.IterationDir(); got != filepath.Join(tmpDir, "iteration-99") {
		t.Fatalf("IterationDir = %q, want %q", got, filepath.Join(tmpDir, "iteration-99"))
	}
}

func TestRunner_InitWorkspace_DefaultRunKeepsOtherIterations(t *testing.T) {
	t.Parallel()

	r := NewRunner(&config.EvalConfig{}, nil, nil, credential.AgentInitParams{})
	tmpDir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(tmpDir, "iteration-2"), 0o755); err != nil {
		t.Fatalf("create iteration-2: %v", err)
	}

	if err := r.InitWorkspace(tmpDir, "test-skill", 1); err != nil {
		t.Fatalf("InitWorkspace failed: %v", err)
	}

	if got := r.workspace.IterationNum; got != 1 {
		t.Fatalf("IterationNum = %d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "iteration-2")); err != nil {
		t.Fatalf("existing iteration-2 should remain: %v", err)
	}
}

func TestRunner_InitWorkspace_ExplicitRunCleansOnlyRequestedIteration(t *testing.T) {
	t.Parallel()

	r := NewRunner(&config.EvalConfig{}, nil, nil, credential.AgentInitParams{})
	tmpDir := t.TempDir()

	staleFile := filepath.Join(tmpDir, "iteration-99", "stale.txt")
	if err := os.MkdirAll(filepath.Dir(staleFile), 0o755); err != nil {
		t.Fatalf("create iteration-99: %v", err)
	}
	if err := os.WriteFile(staleFile, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "iteration-1"), 0o755); err != nil {
		t.Fatalf("create iteration-1: %v", err)
	}

	if err := r.InitWorkspace(tmpDir, "test-skill", 99); err != nil {
		t.Fatalf("InitWorkspace failed: %v", err)
	}

	if _, err := os.Stat(staleFile); !os.IsNotExist(err) {
		t.Fatalf("stale file should be removed, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "iteration-1")); err != nil {
		t.Fatalf("iteration-1 should remain: %v", err)
	}
}

func TestRunner_Evaluate_DefaultsZeroIterationToOne(t *testing.T) {
	t.Parallel()

	evalDir := t.TempDir()
	evalsDir := filepath.Join(evalDir, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatalf("mkdir evals: %v", err)
	}

	loader := config.NewLoader(filepath.Join(evalsDir, "eval.yaml"))
	r := NewRunner(&config.EvalConfig{
		Environment: config.Environment{Type: "none"},
		Cases:       config.CasesConfig{Parallelism: 1},
	}, loader, nil, credential.AgentInitParams{})

	ag := &runnerTestAgent{}
	results, err := r.Evaluate(context.Background(), []*config.CaseConfig{{ID: "case-1", Title: "Case 1"}}, ag, EvaluateOptions{
		OutputDir:       filepath.Join(evalDir, "workspace"),
		Iteration:       0,
		DeleteWorkspace: false,
	})
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if got := r.workspace.IterationNum; got != 1 {
		t.Fatalf("IterationNum = %d, want 1", got)
	}
}

func TestRunner_Evaluate_DefaultWorkspaceIsSiblingOfSkillDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	skillDir := filepath.Join(root, "my-skill")
	evalsDir := filepath.Join(skillDir, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatalf("mkdir evals: %v", err)
	}

	loader := config.NewLoader(filepath.Join(evalsDir, "eval.yaml"))
	r := NewRunner(&config.EvalConfig{
		Environment: config.Environment{Type: "none"},
		Cases:       config.CasesConfig{Parallelism: 1},
	}, loader, nil, credential.AgentInitParams{})

	if _, err := r.Evaluate(context.Background(), []*config.CaseConfig{{ID: "case-1", Title: "Case 1"}}, &runnerTestAgent{}, EvaluateOptions{}); err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	want := filepath.Join(root, "my-skill-workspace")
	if got := r.workspace.RootDir; got != want {
		t.Fatalf("workspace RootDir = %q, want %q (sibling of skill dir, not inside it)", got, want)
	}
}

func TestRunner_Evaluate_RunsMultipleIterations(t *testing.T) {
	evalDir := t.TempDir()
	evalsDir := filepath.Join(evalDir, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatalf("mkdir evals: %v", err)
	}

	loader := config.NewLoader(filepath.Join(evalsDir, "eval.yaml"))
	r := NewRunner(&config.EvalConfig{
		Environment: config.Environment{Type: "none"},
		Cases:       config.CasesConfig{Parallelism: 1},
	}, loader, nil, credential.AgentInitParams{})

	ag := &runnerTestAgent{}
	workspaceRoot := filepath.Join(evalDir, "workspace")
	results, err := r.Evaluate(context.Background(), []*config.CaseConfig{{ID: "case-1", Title: "Case 1"}}, ag, EvaluateOptions{
		OutputDir:       workspaceRoot,
		Iteration:       2,
		DeleteWorkspace: false,
	})
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, runNumber := range []int{1, 2} {
		resultPath := filepath.Join(workspaceRoot, fmt.Sprintf("iteration-%d", runNumber), "result.json")
		if _, err := os.Stat(resultPath); err != nil {
			t.Fatalf("missing result.json for run %d: %v", runNumber, err)
		}
	}
	if got := r.workspace.IterationNum; got != 2 {
		t.Fatalf("IterationNum = %d, want 2 after final run", got)
	}
}

func TestFormatIterationStabilitySummary(t *testing.T) {
	t.Parallel()

	summary := stabilityCaseSummary{
		caseID: "case_a",
		trials: 3,
		counts: map[string]int{
			"PASS": 2,
			"FAIL": 1,
		},
	}

	if got, want := formatIterationStabilitySummary(summary), "case_a: 3 trials, 2 PASS, 1 FAIL -> flaky"; got != want {
		t.Fatalf("formatIterationStabilitySummary() = %q, want %q", got, want)
	}
}

func TestFormatIterationStabilitySummaryIncludesUnknownStatuses(t *testing.T) {
	t.Parallel()

	summary := stabilityCaseSummary{
		caseID: "case_b",
		trials: 3,
		counts: map[string]int{
			"PASS":    1,
			"UNKNOWN": 1,
			"Z_OTHER": 1,
		},
	}

	want := "case_b: 3 trials, 1 PASS, 1 UNKNOWN, 1 Z_OTHER -> flaky"
	if got := formatIterationStabilitySummary(summary); got != want {
		t.Fatalf("formatIterationStabilitySummary() = %q, want %q", got, want)
	}
}

func TestRunner_Evaluate_MultipleIterationsPrintStabilitySummaryAndDoNotWriteFiles(t *testing.T) {
	evalDir := t.TempDir()
	evalsDir := filepath.Join(evalDir, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatalf("mkdir evals: %v", err)
	}

	loader := config.NewLoader(filepath.Join(evalsDir, "eval.yaml"))
	r := NewRunner(&config.EvalConfig{
		Environment: config.Environment{Type: "none"},
		Cases:       config.CasesConfig{Parallelism: 1},
	}, loader, nil, credential.AgentInitParams{})

	origNewEvaluator := newEvaluator
	t.Cleanup(func() { newEvaluator = origNewEvaluator })

	statuses := []judge.Status{judge.StatusPass, judge.StatusFail, judge.StatusPass}
	callCount := 0
	newEvaluator = func(opts evaluator.EvalOptions) evaluator.Evaluator {
		status := statuses[callCount]
		callCount++
		return evaluatorStub{
			evaluateAll: func(context.Context, []*config.CaseConfig) ([]evaluator.EvalResult, error) {
				return []evaluator.EvalResult{
					{
						CaseID:        "case_a",
						CaseName:      "Case A",
						Status:        status,
						Configuration: "with_skill",
						SessionResult: &agent.SessionResult{FinalMessage: "ok", ExitCode: 0},
						Grading:       &judge.Result{Status: status},
					},
					{
						CaseID:        "case_a",
						CaseName:      "Case A baseline",
						Status:        judge.StatusError,
						Configuration: "without_skill",
						SessionResult: &agent.SessionResult{FinalMessage: "baseline", ExitCode: 0},
						Grading:       &judge.Result{Status: judge.StatusError},
					},
				}, nil
			},
		}
	}

	var output bytes.Buffer
	captureUIOutput(t, &output)

	workspaceRoot := filepath.Join(evalDir, "workspace")
	if _, err := r.Evaluate(context.Background(), []*config.CaseConfig{{ID: "case_a", Title: "Case A"}}, &runnerTestAgent{}, EvaluateOptions{
		OutputDir:       workspaceRoot,
		Iteration:       3,
		DeleteWorkspace: false,
	}); err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	rendered := output.String()
	if !strings.Contains(rendered, "case_a: 3 trials, 2 PASS, 1 FAIL -> flaky") {
		t.Fatalf("stability summary missing with_skill results, output:\n%s", rendered)
	}
	if strings.Contains(rendered, "ERROR") {
		t.Fatalf("stability summary should exclude baseline ERROR result, output:\n%s", rendered)
	}

	for _, filename := range []string{"stability.json", "stability.md"} {
		if err := assertFileAbsentUnder(t, workspaceRoot, filename); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunner_Evaluate_AutoIterationAppendsWithoutStabilitySummary(t *testing.T) {
	evalDir := t.TempDir()
	evalsDir := filepath.Join(evalDir, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatalf("mkdir evals: %v", err)
	}
	workspaceRoot := filepath.Join(evalDir, "workspace")
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "iteration-1"), 0o755); err != nil {
		t.Fatalf("mkdir existing iteration: %v", err)
	}

	loader := config.NewLoader(filepath.Join(evalsDir, "eval.yaml"))
	r := NewRunner(&config.EvalConfig{
		Environment: config.Environment{Type: "none"},
		Cases:       config.CasesConfig{Parallelism: 1},
	}, loader, nil, credential.AgentInitParams{})

	var output bytes.Buffer
	captureUIOutput(t, &output)

	if _, err := r.Evaluate(context.Background(), []*config.CaseConfig{{ID: "case_a", Title: "Case A"}}, &runnerTestAgent{}, EvaluateOptions{
		OutputDir:       workspaceRoot,
		Iteration:       0,
		DeleteWorkspace: false,
	}); err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	if got := r.workspace.IterationNum; got != 2 {
		t.Fatalf("IterationNum = %d, want 2", got)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, "iteration-2", "result.json")); err != nil {
		t.Fatalf("auto iteration should append result.json under iteration-2: %v", err)
	}
	if strings.Contains(output.String(), "Iteration stability summary") {
		t.Fatalf("auto iteration should not print historical stability summary, output:\n%s", output.String())
	}
}

func captureUIOutput(t *testing.T, output *bytes.Buffer) {
	t.Helper()

	origOutput := uiOutput
	uiOutput = output
	t.Cleanup(func() { uiOutput = origOutput })
}

func assertFileAbsentUnder(t *testing.T, root, filename string) error {
	t.Helper()

	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() != filename {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		if strings.Contains(rel, string(filepath.Separator)) {
			return fmt.Errorf("%s should not be written under workspace, found at %s", filename, rel)
		}
		return fmt.Errorf("%s should not be written at workspace root", filename)
	})
}

func TestRunner_Evaluate_ReturnsPartialResultsWhenLaterRunFails(t *testing.T) {
	evalDir := t.TempDir()
	evalsDir := filepath.Join(evalDir, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatalf("mkdir evals: %v", err)
	}

	loader := config.NewLoader(filepath.Join(evalsDir, "eval.yaml"))
	r := NewRunner(&config.EvalConfig{
		Environment: config.Environment{Type: "none"},
		Cases:       config.CasesConfig{Parallelism: 1},
	}, loader, nil, credential.AgentInitParams{})

	origNewEvaluator := newEvaluator
	t.Cleanup(func() { newEvaluator = origNewEvaluator })

	callCount := 0
	wantErr := errors.New("boom on second run")
	newEvaluator = func(opts evaluator.EvalOptions) evaluator.Evaluator {
		callCount++
		runNumber := callCount
		return evaluatorStub{
			evaluateAll: func(context.Context, []*config.CaseConfig) ([]evaluator.EvalResult, error) {
				if runNumber == 2 {
					return nil, wantErr
				}
				return []evaluator.EvalResult{{
					CaseID:        "case-1",
					CaseName:      "Case 1",
					Status:        judge.StatusPass,
					Configuration: "with_skill",
					SessionResult: &agent.SessionResult{FinalMessage: "ok", ExitCode: 0},
					Grading: &judge.Result{
						Status:           judge.StatusPass,
						AssertionResults: []judge.AssertionResult{{Text: "ok", Passed: true}},
						Summary:          judge.ResultSummary{Passed: 1, Total: 1, PassRate: 1},
					},
				}}, nil
			},
		}
	}

	workspaceRoot := filepath.Join(evalDir, "workspace")
	results, err := r.Evaluate(context.Background(), []*config.CaseConfig{{ID: "case-1", Title: "Case 1"}}, &runnerTestAgent{}, EvaluateOptions{
		OutputDir:       workspaceRoot,
		Iteration:       2,
		DeleteWorkspace: false,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 partial result from completed runs, got %d", len(results))
	}
	if results[0].CaseID != "case-1" {
		t.Fatalf("unexpected partial result: %+v", results[0])
	}
	if _, statErr := os.Stat(filepath.Join(workspaceRoot, "iteration-1", "result.json")); statErr != nil {
		t.Fatalf("expected first run artifact to exist: %v", statErr)
	}
}

func TestNextIterationNumber_ReturnsOneWhenEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if got := nextIterationNumber(dir); got != 1 {
		t.Fatalf("nextIterationNumber = %d, want 1", got)
	}
}

func TestNextIterationNumber_ReturnsMaxPlusOne(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"iteration-1", "iteration-3", "iteration-2"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	if got := nextIterationNumber(dir); got != 4 {
		t.Fatalf("nextIterationNumber = %d, want 4", got)
	}
}

func TestNextIterationNumber_IgnoresPrefixOnlyMatches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// iteration-5-backup should NOT be counted; only iteration-2 is valid.
	for _, name := range []string{"iteration-5-backup", "iteration-2", "iteration-abc", "other-dir"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	if got := nextIterationNumber(dir); got != 3 {
		t.Fatalf("nextIterationNumber = %d, want 3 (should ignore iteration-5-backup)", got)
	}
}

func TestRunner_Evaluate_AutoIncrementsIteration(t *testing.T) {
	evalDir := t.TempDir()
	evalsDir := filepath.Join(evalDir, "evals")
	if err := os.MkdirAll(evalsDir, 0o755); err != nil {
		t.Fatalf("mkdir evals: %v", err)
	}

	workspaceRoot := filepath.Join(evalDir, "workspace")
	loader := config.NewLoader(filepath.Join(evalsDir, "eval.yaml"))
	newRunner := func() *Runner {
		return NewRunner(&config.EvalConfig{
			Environment: config.Environment{Type: "none"},
			Cases:       config.CasesConfig{Parallelism: 1},
		}, loader, nil, credential.AgentInitParams{})
	}

	opts := EvaluateOptions{OutputDir: workspaceRoot, Iteration: 0, DeleteWorkspace: false}
	cases := []*config.CaseConfig{{ID: "case-1", Title: "Case 1"}}

	for wantIter := 1; wantIter <= 3; wantIter++ {
		r := newRunner()
		if _, err := r.Evaluate(context.Background(), cases, &runnerTestAgent{}, opts); err != nil {
			t.Fatalf("run %d: Evaluate failed: %v", wantIter, err)
		}
		if got := r.workspace.IterationNum; got != wantIter {
			t.Fatalf("run %d: IterationNum = %d, want %d", wantIter, got, wantIter)
		}
	}
}

type evaluatorStub struct {
	evaluateAll func(context.Context, []*config.CaseConfig) ([]evaluator.EvalResult, error)
}

func (s evaluatorStub) EvaluateAll(ctx context.Context, cases []*config.CaseConfig) ([]evaluator.EvalResult, error) {
	return s.evaluateAll(ctx, cases)
}

type runnerTestAgent struct{}

func (a *runnerTestAgent) Name() string { return "test-agent" }
func (a *runnerTestAgent) Install(context.Context, agent.Runtime) error {
	return nil
}

func (a *runnerTestAgent) InstallMCP(context.Context, agent.Runtime, runtime.MCPConfig) error {
	return nil
}

func (a *runnerTestAgent) InstallSkill(context.Context, agent.Runtime, runtime.SkillConfig) error {
	return nil
}

func (a *runnerTestAgent) Run(context.Context, agent.Runtime, agent.ExecOptions, []transcript.Message) (*agent.SessionResult, error) {
	return &agent.SessionResult{FinalMessage: "ok", ExitCode: 0}, nil
}
func (a *runnerTestAgent) Check(context.Context, agent.Runtime) error { return nil }
func (a *runnerTestAgent) CheckCredentials(context.Context) error     { return nil }
