package compare

import (
	"encoding/json"

	"github.com/alibaba/skill-up/internal/report"
)

// MissingMetrics identifies absent measurements in an already validated JSON input.
// Unmarshalling into report.Input alone would turn absent measurements into zero.
func MissingMetrics(data []byte, input report.Input) []string {
	var raw struct {
		TotalTokens *int `json:"total_tokens"`
		Cases       []struct {
			CaseID        string `json:"case_id"`
			Configuration string `json:"configuration"`
			Input         *int   `json:"input_tokens"`
			Output        *int   `json:"output_tokens"`
		} `json:"case_results"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return []string{"total_tokens", "input_tokens", "output_tokens", "duration_ms"}
	}
	var missing []string
	if raw.TotalTokens == nil {
		missing = append(missing, "total_tokens")
	}
	selected := make(map[string]string)
	for _, cr := range input.PrimaryCaseResults() {
		selected[cr.CaseID] = cr.Configuration
	}
	inputKnown, outputKnown := true, true
	for _, cr := range raw.Cases {
		if selected[cr.CaseID] != cr.Configuration {
			continue
		}
		inputKnown = inputKnown && cr.Input != nil
		outputKnown = outputKnown && cr.Output != nil
	}
	if !inputKnown {
		missing = append(missing, "input_tokens")
	}
	if !outputKnown {
		missing = append(missing, "output_tokens")
	}
	return missing
}
