package report

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"time"

	"github.com/alibaba/skill-up/internal/judge"
)

// HTMLReporter writes human-readable HTML summaries.
// Generates an interactive single-page report with case navigation,
// collapsible grading details, benchmark visualization, and feedback support.
type HTMLReporter struct {
	// OutputPath is the file path to write the HTML report.
	// If empty, writes to stdout.
	OutputPath string
}

// Write implements the Reporter interface.
func (r *HTMLReporter) Write(_ context.Context, in Input) error {
	data, err := r.buildTemplateData(in)
	if err != nil {
		return err
	}

	funcMap := SharedTemplateFuncs()
	funcMap["statusIcon"] = statusIcon

	tmpl, err := template.New("report").Funcs(funcMap).Parse(htmlTemplate)
	if err != nil {
		return fmt.Errorf("parse html template: %w", err)
	}

	return writeToOutput(r.OutputPath, "html report", func(w io.Writer) error {
		if err := tmpl.Execute(w, data); err != nil {
			return fmt.Errorf("execute html template: %w", err)
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Template data model
// ---------------------------------------------------------------------------

type htmlReportData struct {
	SkillName        string
	LogoDataURI      template.URL
	EmbeddedDataJSON template.JS
}

// -- Embedded JSON types for JavaScript consumption --

type embeddedReportData struct {
	SkillName          string           `json:"skill_name"`
	EngineName         string           `json:"engine_name"`
	ModelName          string           `json:"model_name"`
	Protocol           string           `json:"protocol"`
	RequestedModel     string           `json:"requested_model"`
	AppliedModel       string           `json:"applied_model"`
	ObservedModel      string           `json:"observed_model"`
	StartTime          string           `json:"start_time"`
	EvaluationWallTime string           `json:"evaluation_wall_time"`
	AgentTokens        int              `json:"agent_tokens"`
	JudgeTokens        int              `json:"judge_tokens"`
	OverallTokens      int              `json:"overall_tokens"`
	Summary            embeddedSummary  `json:"summary"`
	Cases              []embeddedCase   `json:"cases"`
	Benchmark          *BenchmarkResult `json:"benchmark,omitempty"`
}

type embeddedSummary struct {
	Total    int    `json:"total"`
	Passed   int    `json:"passed"`
	Failed   int    `json:"failed"`
	Skipped  int    `json:"skipped"`
	Errors   int    `json:"errors"`
	PassRate string `json:"pass_rate"`
}

type embeddedCase struct {
	ID                string            `json:"id"`
	Title             string            `json:"title,omitempty"`
	Status            string            `json:"status"`
	AgentDurationMs   int64             `json:"agent_duration_ms"`
	AgentDuration     string            `json:"agent_duration"`
	InputTokens       int               `json:"input_tokens"`
	OutputTokens      int               `json:"output_tokens"`
	AgentTokens       int               `json:"agent_tokens"`
	JudgeDurationMs   int64             `json:"judge_duration_ms"`
	JudgeDuration     string            `json:"judge_duration"`
	JudgeInputTokens  int               `json:"judge_input_tokens"`
	JudgeOutputTokens int               `json:"judge_output_tokens"`
	JudgeTokens       int               `json:"judge_tokens"`
	Turns             int               `json:"turns"`
	Error             string            `json:"error,omitempty"`
	Grading           *embeddedGrading  `json:"grading,omitempty"`
	Configuration     string            `json:"configuration,omitempty"`
	Prompt            string            `json:"prompt,omitempty"`
	Response          string            `json:"response,omitempty"`
	Baseline          *embeddedCase     `json:"baseline,omitempty"`
	TurnResults       []embeddedTurn    `json:"turn_results,omitempty"`
	JudgeSkills       []judge.SkillInfo `json:"judge_skills,omitempty"`
}

// embeddedTurn holds per-turn data for the HTML report JavaScript.
type embeddedTurn struct {
	TurnNumber int    `json:"turn_number"`
	Content    string `json:"content"`
	Response   string `json:"response"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
}

type embeddedGrading struct {
	Expectations []embeddedExpectation  `json:"expectations"`
	Summary      embeddedGradingSummary `json:"summary"`
}

type embeddedExpectation struct {
	Text     string `json:"text"`
	Passed   bool   `json:"passed"`
	Evidence string `json:"evidence"`
}

type embeddedGradingSummary struct {
	Passed   int     `json:"passed"`
	Failed   int     `json:"failed"`
	Total    int     `json:"total"`
	PassRate float64 `json:"pass_rate"`
}

type caseGroup struct {
	withSkill    *CaseResult
	withoutSkill *CaseResult
}

type caseStatusCounts struct {
	passed  int
	failed  int
	skipped int
	errored int
}

func caseResultToEmbeddedCase(cr CaseResult) embeddedCase {
	ec := embeddedCase{
		ID:                cr.CaseID,
		Title:             cr.Title,
		Status:            string(cr.Status),
		AgentDurationMs:   cr.DurationMs,
		AgentDuration:     fmt.Sprintf("%.1fs", float64(cr.DurationMs)/1000.0),
		InputTokens:       cr.InputTokens,
		OutputTokens:      cr.OutputTokens,
		AgentTokens:       cr.InputTokens + cr.OutputTokens,
		JudgeDurationMs:   cr.JudgeDurationMs,
		JudgeDuration:     fmt.Sprintf("%.1fs", float64(cr.JudgeDurationMs)/1000.0),
		JudgeInputTokens:  cr.JudgeInputTokens,
		JudgeOutputTokens: cr.JudgeOutputTokens,
		JudgeTokens:       cr.JudgeInputTokens + cr.JudgeOutputTokens,
		Turns:             cr.Turns,
		Error:             cr.Error,
		Configuration:     cr.Configuration,
		Prompt:            cr.Prompt,
		Response:          cr.Response,
		TurnResults:       caseTurnResultsToEmbedded(cr.TurnResults),
		JudgeSkills:       cr.JudgeSkills,
	}
	if cr.Grading != nil {
		eg := &embeddedGrading{
			Expectations: make([]embeddedExpectation, 0, len(cr.Grading.AssertionResults)),
			Summary: embeddedGradingSummary{
				Passed:   cr.Grading.Summary.Passed,
				Failed:   cr.Grading.Summary.Failed,
				Total:    cr.Grading.Summary.Total,
				PassRate: cr.Grading.Summary.PassRate,
			},
		}
		for _, ar := range cr.Grading.AssertionResults {
			eg.Expectations = append(eg.Expectations, embeddedExpectation{
				Text:     ar.Text,
				Passed:   ar.Passed,
				Evidence: ar.Evidence,
			})
		}
		ec.Grading = eg
	}
	return ec
}

func caseTurnResultsToEmbedded(turns []CaseTurnResult) []embeddedTurn {
	if len(turns) == 0 {
		return nil
	}
	out := make([]embeddedTurn, len(turns))
	for i, tr := range turns {
		out[i] = embeddedTurn(tr)
	}
	return out
}

func groupCaseResults(results []CaseResult) (map[string]*caseGroup, []string) {
	grouped := make(map[string]*caseGroup)
	orderedIDs := make([]string, 0, len(results))

	for i := range results {
		cr := &results[i]
		g, ok := grouped[cr.CaseID]
		if !ok {
			g = &caseGroup{}
			grouped[cr.CaseID] = g
			orderedIDs = append(orderedIDs, cr.CaseID)
		}
		if cr.Configuration == "without_skill" {
			g.withoutSkill = cr
		} else {
			g.withSkill = cr
		}
	}

	return grouped, orderedIDs
}

func primaryCaseResult(group *caseGroup) *CaseResult {
	if group.withSkill != nil {
		return group.withSkill
	}
	return group.withoutSkill
}

func countCaseStatuses(grouped map[string]*caseGroup, orderedIDs []string) caseStatusCounts {
	var counts caseStatusCounts
	for _, caseID := range orderedIDs {
		switch primaryCaseResult(grouped[caseID]).Status {
		case judge.StatusPass:
			counts.passed++
		case judge.StatusFail:
			counts.failed++
		case judge.StatusSkip:
			counts.skipped++
		case judge.StatusError:
			counts.errored++
		}
	}
	return counts
}

func buildEmbeddedCases(grouped map[string]*caseGroup, orderedIDs []string) []embeddedCase {
	cases := make([]embeddedCase, 0, len(orderedIDs))

	for _, caseID := range orderedIDs {
		g := grouped[caseID]
		primary := primaryCaseResult(g)
		ec := caseResultToEmbeddedCase(*primary)

		if g.withSkill != nil && g.withoutSkill != nil {
			baseline := caseResultToEmbeddedCase(*g.withoutSkill)
			ec.Baseline = &baseline
		}

		cases = append(cases, ec)
	}
	return cases
}

func (r *HTMLReporter) buildTemplateData(in Input) (htmlReportData, error) {
	grouped, orderedIDs := groupCaseResults(in.CaseResults)
	counts := countCaseStatuses(grouped, orderedIDs)
	cases := buildEmbeddedCases(grouped, orderedIDs)

	ed := embeddedReportData{
		SkillName:          in.SkillName,
		EngineName:         in.EngineName,
		ModelName:          in.ModelName,
		Protocol:           configurationProtocol(in),
		RequestedModel:     agentConfigurationModel(in.RequestedConfiguration),
		AppliedModel:       agentConfigurationModel(in.AppliedConfiguration),
		ObservedModel:      agentConfigurationModel(in.ObservedConfiguration),
		StartTime:          in.StartTime.Format(time.RFC3339),
		EvaluationWallTime: fmt.Sprintf("%.1fs", in.TotalDuration().Seconds()),
		AgentTokens:        in.TotalTokens,
		JudgeTokens:        in.JudgeTokens,
		OverallTokens:      in.TotalTokens + in.JudgeTokens,
		Summary: embeddedSummary{
			Total:    len(cases),
			Passed:   counts.passed,
			Failed:   counts.failed,
			Skipped:  counts.skipped,
			Errors:   counts.errored,
			PassRate: fmt.Sprintf("%.0f%%", in.OverallPassRate()*100),
		},
		Cases:     cases,
		Benchmark: in.Benchmark,
	}

	jsonBytes, err := json.Marshal(ed)
	if err != nil {
		return htmlReportData{}, fmt.Errorf("marshal embedded report data: %w", err)
	}

	return htmlReportData{
		SkillName:        in.SkillName,
		LogoDataURI:      logoDataURI,
		EmbeddedDataJSON: template.JS(jsonBytes), //nolint:gosec // trusted internal data, not user input
	}, nil
}

func configurationProtocol(in Input) string {
	if in.AppliedConfiguration != nil {
		return in.AppliedConfiguration.Protocol
	}
	if in.RequestedConfiguration != nil {
		return in.RequestedConfiguration.Protocol
	}
	return ""
}

func statusIcon(s judge.Status) template.HTML {
	switch s {
	case judge.StatusPass:
		return template.HTML("&#x2705;") // nolint:gosec
	case judge.StatusFail:
		return template.HTML("&#x274C;") // nolint:gosec
	case judge.StatusSkip:
		return template.HTML("&#x23ED;") // nolint:gosec
	case judge.StatusError:
		return template.HTML("&#x26A0;") // nolint:gosec
	default:
		return template.HTML("?") // nolint:gosec
	}
}

// ---------------------------------------------------------------------------
// HTML template (embedded from templates/report.html)
// ---------------------------------------------------------------------------

//go:embed templates/report.html
var htmlTemplate string

//go:embed templates/logo.png
var htmlLogoPNG []byte

// logoDataURI is the project logo encoded as a base64 data URI, embedded into
// the HTML report so the output file is self-contained and viewable offline.
// The input to template.URL is an embedded PNG (compile-time constant, no
// user-controlled data), so bypassing HTML auto-escaping is safe here.
var logoDataURI = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(htmlLogoPNG)) //nolint:gochecknoglobals,gosec // G203: precomputed once at init from embedded asset

// WriteHTMLReport is a convenience function that writes an HTML report to the specified path.
func WriteHTMLReport(ctx context.Context, path string, in Input) error {
	r := &HTMLReporter{OutputPath: path}
	return r.Write(ctx, in)
}
