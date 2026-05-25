// Package cli — judge_debug.go implements a debug subcommand for the judge module.
//
// Usage:
//
//	skill-up debug judge <input.json>
//
// This command reads a JSON file containing judge input + config, runs the
// judge evaluation pipeline (Expect → Judge → Result), and writes grading.json
// to the current directory.
//
// Supported judge types:
//   - rule_based: declarative assertion rules (no external dependencies)
//   - script:     execute a local evaluation script
//   - agent_judge: uses mock_results from JSON to simulate agent output
//
// This is a debug tool for quick module validation. It is intentionally
// decoupled from the main evaluation pipeline (runner, engine, etc.).
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/judge"
	"github.com/alibaba/skill-up/internal/report"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// ---------------------------------------------------------------------------
// JSON input schema (self-contained, no coupling to runner/engine)
// ---------------------------------------------------------------------------

// judgeDebugInput is the JSON schema for the debug command input file.
// It bundles everything the judge module needs in a single file.
//
// Example JSON:
//
//	{
//	  "case_id": "test-001",
//	  "final_message": "found null pointer bug at line 42",
//	  "exit_code": 0,
//	  "skill_dir": "/path/to/skill",
//	  "workspace_path": "/tmp/workspace",
//	  "workspace_diff": "",
//	  "turns_executed": 2,
//	  "turns_total": 3,
//	  "transcript": [
//	    {"role": "user", "content": "review this code", "turn": 1},
//	    {"role": "assistant", "content": "found null pointer bug", "turn": 1}
//	  ],
//	  "expect": {
//	    "must_contain": ["bug"],
//	    "exit_code": 0
//	  },
//	  "judge": {
//	    "type": "rule_based",
//	    "success": [
//	      {"output_contains": {"all": ["null", "bug"]}}
//	    ]
//	  }
//	}
type judgeDebugInput struct {
	// --- Judge Input fields ---
	CaseID         string                `json:"case_id"`
	FinalMessage   string                `json:"final_message"`
	ExitCode       int                   `json:"exit_code"`
	SkillDir       string                `json:"skill_dir,omitempty"`
	WorkspacePath  string                `json:"workspace_path,omitempty"`
	WorkspaceDiff  string                `json:"workspace_diff,omitempty"`
	GeneratedFiles []string              `json:"generated_files,omitempty"`
	TurnsExecuted  int                   `json:"turns_executed,omitempty"`
	TurnsTotal     int                   `json:"turns_total,omitempty"`
	Transcript     transcript.Transcript `json:"transcript,omitempty"`

	// --- Evaluation config ---
	Expect *config.Expect     `json:"expect,omitempty"`
	Judge  config.JudgeConfig `json:"judge"`

	// --- agent_judge mock: inline LLM response for testing without real LLM ---
	// When judge.type == "agent_judge", provide mock_results to simulate LLM output.
	MockResults []judge.CriterionResult `json:"mock_results,omitempty"`
}

// toJudgeInput converts the debug input to a judge.Input.
func (d *judgeDebugInput) toJudgeInput() judge.Input {
	return judge.Input{
		CaseID:         d.CaseID,
		Transcript:     d.Transcript,
		FinalMessage:   d.FinalMessage,
		ExitCode:       d.ExitCode,
		SkillDir:       d.SkillDir,
		WorkspacePath:  d.WorkspacePath,
		WorkspaceDiff:  d.WorkspaceDiff,
		GeneratedFiles: d.GeneratedFiles,
		TurnsExecuted:  d.TurnsExecuted,
		TurnsTotal:     d.TurnsTotal,
	}
}

// ---------------------------------------------------------------------------
// Cobra command
// ---------------------------------------------------------------------------

var debugJudgeCmd = &cobra.Command{
	Use:   "judge <input.json>",
	Short: "Run judge module with a JSON input file",
	Long: `Debug command for judge module validation.

Reads a JSON file containing judge input data and evaluation config,
runs the full evaluation pipeline (Expect → Judge → Result),
and writes grading.json to the current directory.

Supported judge types:
  - rule_based:  declarative assertion rules (default)
  - script:      execute a local evaluation script
  - agent_judge: provide mock_results in JSON to simulate agent output`,
	Args: cobra.ExactArgs(1),
	RunE: runJudgeDebug,
}

func init() {
	debugJudgeCmd.Flags().String("output", "grading.json", "Output path for grading.json")
	debugJudgeCmd.Flags().String("report", "", "Also generate a report after judge evaluation (json, junit, html)")
}

func runJudgeDebug(cmd *cobra.Command, args []string) error {
	inputPath := args[0]

	// 1. Read and parse input JSON
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("read input file %q: %w", inputPath, err)
	}

	var input judgeDebugInput
	if err := json.Unmarshal(data, &input); err != nil {
		return fmt.Errorf("parse input JSON: %w", err)
	}

	// 2. Build judge from config.
	transcriptPath, cleanupTranscript, err := prepareDebugTranscriptFile(input)
	if err != nil {
		return err
	}
	defer cleanupTranscript()

	j, err := buildJudgeFromConfig(input.Judge, input.MockResults, transcriptPath)
	if err != nil {
		return err
	}

	judgeInput := input.toJudgeInput()
	ctx := context.Background()

	// 3. Run Expect pre-check
	result, err := evaluateJudgeWithExpect(ctx, cmd, j, input.Expect, judgeInput)
	if err != nil {
		return err
	}

	// 4. Write grading.json
	outputPath, _ := cmd.Flags().GetString("output")
	outData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	outData = append(outData, '\n')

	if err := os.WriteFile(outputPath, outData, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", outputPath, err)
	}

	// 5. Print summary to stderr
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[judge]  %s — %d/%d assertions passed (pass_rate: %.1f%%)\n",
		result.Status, result.Summary.Passed, result.Summary.Total, result.Summary.PassRate*100)
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[output] %s\n", outputPath)

	// 6. Optionally generate report (judge + report joint test)
	reportFormat, _ := cmd.Flags().GetString("report")
	if reportFormat != "" {
		if err := generateDebugReport(ctx, cmd, reportFormat, input, result); err != nil {
			return err
		}
	}

	return nil
}

// prepareDebugTranscriptFile serialises input.Transcript to a temp file when
// the configured judge is a script judge, so that EVAL_TRANSCRIPT_PATH is
// populated consistently with the real pipeline. Returns the temp file path
// (empty when not needed) and a cleanup function the caller must always defer.
func prepareDebugTranscriptFile(input judgeDebugInput) (string, func(), error) {
	noop := func() {}
	if input.Judge.Type != "script" || len(input.Transcript) == 0 {
		return "", noop, nil
	}

	tmpFile, err := os.CreateTemp("", "judge-debug-transcript-*.json")
	if err != nil {
		return "", noop, fmt.Errorf("create temp transcript file: %w", err)
	}
	cleanup := func() { _ = os.Remove(tmpFile.Name()) }

	transcriptData, err := json.Marshal(input.Transcript)
	if err != nil {
		cleanup()
		return "", noop, fmt.Errorf("marshal transcript: %w", err)
	}
	if _, err := tmpFile.Write(transcriptData); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("write transcript to temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("close temp transcript file: %w", err)
	}
	return tmpFile.Name(), cleanup, nil
}

// evaluateJudgeWithExpect runs the Expect pre-check; on failure it short-circuits
// to a synthesised Result, otherwise dispatches to the configured judge. Logs a
// one-line PASS/FAIL summary to cmd's stderr so the debug UX matches the real run.
func evaluateJudgeWithExpect(ctx context.Context, cmd *cobra.Command, j judge.Judge, expect *config.Expect, judgeInput judge.Input) (*judge.Result, error) {
	er := judge.CheckExpect(expect, judgeInput)
	if !er.Passed {
		assertions := er.ToAssertionResults()
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[expect] FAIL — %d checks failed, judge skipped\n", len(er.Failures))
		return judge.NewResult(assertions, judgeInput.TurnsExecuted, judgeInput.TurnsTotal), nil
	}

	if expect != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[expect] PASS — all pre-checks passed\n")
	}
	result, err := j.Evaluate(ctx, judgeInput)
	if err != nil {
		return nil, fmt.Errorf("judge evaluation failed: %w", err)
	}
	return result, nil
}

// generateDebugReport renders the optional report file (json/junit/html) into
// the current directory using the same Reporter machinery the real pipeline uses.
func generateDebugReport(ctx context.Context, cmd *cobra.Command, format string, input judgeDebugInput, result *judge.Result) error {
	reporter, reportPath, err := buildReporter(format, ".")
	if err != nil {
		return err
	}

	reportInput := report.Input{
		SkillName:     "judge-debug",
		SchemaVersion: "v1alpha1",
		EngineName:    "debug",
		ModelName:     "local",
		StartTime:     time.Now().Add(-time.Second),
		EndTime:       time.Now(),
		CaseResults: []report.CaseResult{
			{
				CaseID:     input.CaseID,
				Title:      "Judge debug: " + input.CaseID,
				Status:     result.Status,
				DurationMs: 1000,
				Turns:      input.TurnsExecuted,
				Grading:    result,
			},
		},
	}

	if err := reporter.Write(ctx, reportInput); err != nil {
		return fmt.Errorf("generate %s report: %w", format, err)
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[report] %s format → %s\n", format, reportPath)
	return nil
}

// buildJudgeFromConfig creates a Judge instance from JudgeConfig.
// Supports rule_based, script, and agent_judge mock mode for debug input.
func buildJudgeFromConfig(cfg config.JudgeConfig, mockResults []judge.CriterionResult, transcriptPath string) (judge.Judge, error) {
	switch cfg.Type {
	case "rule_based", "":
		return judge.NewRuleBasedJudge(cfg), nil
	case "script":
		if cfg.ScriptPath == "" {
			return nil, errors.New("script judge requires script_path")
		}
		return &judge.ScriptJudge{
			ScriptPath:     cfg.ScriptPath,
			TranscriptPath: transcriptPath,
		}, nil
	case "agent_judge":
		return buildMockAgentJudge(cfg, mockResults)
	default:
		return nil, fmt.Errorf("unsupported judge type %q (supported: rule_based, script, agent_judge)", cfg.Type)
	}
}

func buildMockAgentJudge(cfg config.JudgeConfig, mockResults []judge.CriterionResult) (judge.Judge, error) {
	if len(mockResults) == 0 {
		return nil, errors.New("agent_judge in debug mode requires mock_results in JSON input")
	}

	ag := &mockJudgeAgent{results: mockResults}
	return judge.NewAgentJudge(ag, nil, cfg.Model, cfg.Criteria, cfg.PassThreshold, 0), nil
}

// ---------------------------------------------------------------------------
// Mock agent for agent_judge debug mode
// ---------------------------------------------------------------------------

// mockJudgeAgent is used only for judge evaluation in debug mode.
// Install, InstallMCP, InstallSkill, and Check are intentional no-ops.
//
// mockJudgeAgent returns pre-configured results as JSON when Run() is called,
// allowing agent_judge threshold/result logic to be tested without a real agent.
type mockJudgeAgent struct {
	results []judge.CriterionResult
}

func (m *mockJudgeAgent) Name() string { return "mock-judge" }

func (m *mockJudgeAgent) Install(_ context.Context, _ runtime.Runtime) error {
	return nil
}

func (m *mockJudgeAgent) InstallMCP(_ context.Context, _ runtime.Runtime, _ runtime.MCPConfig) error {
	return nil
}

func (m *mockJudgeAgent) InstallSkill(_ context.Context, _ runtime.Runtime, _ runtime.SkillConfig) error {
	return nil
}

func (m *mockJudgeAgent) Check(_ context.Context, _ runtime.Runtime) error { return nil }

func (m *mockJudgeAgent) CheckCredentials(_ context.Context) error { return nil }

func (m *mockJudgeAgent) Run(_ context.Context, _ agent.Runtime, _ agent.ExecOptions, _ []transcript.Message) (*agent.SessionResult, error) {
	resp := struct {
		Results []judge.CriterionResult `json:"results"`
	}{Results: m.results}
	data, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal mock response: %w", err)
	}

	return &agent.SessionResult{
		FinalMessage: string(data),
	}, nil
}
