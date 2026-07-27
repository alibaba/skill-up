package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alibaba/skill-up/internal/judge"
)

func TestConvertToAnthropicGrading(t *testing.T) {
	t.Parallel()
	t.Run("full result", func(t *testing.T) {
		t.Parallel()
		result := judge.NewResult([]judge.AssertionResult{
			{Text: "response shows empathy for user fatigue", Passed: true, Evidence: "opening expresses empathy for tiredness"},
			{Text: "response includes a complete joke", Passed: true, Evidence: "contains a programmer interview joke"},
			{Text: "response uses emoji", Passed: false, Evidence: "no emoji found"},
		}, 1, 1)

		grading := ConvertToAnthropicGrading(result)

		if len(grading.Expectations) != 3 {
			t.Fatalf("expected 3 expectations, got %d", len(grading.Expectations))
		}
		if grading.Expectations[0].Text != "response shows empathy for user fatigue" {
			t.Errorf("unexpected text: %s", grading.Expectations[0].Text)
		}
		if !grading.Expectations[0].Passed {
			t.Error("expected first expectation to pass")
		}
		if grading.Expectations[2].Passed {
			t.Error("expected third expectation to fail")
		}
		if grading.Summary.Passed != 2 {
			t.Errorf("expected 2 passed, got %d", grading.Summary.Passed)
		}
		if grading.Summary.Failed != 1 {
			t.Errorf("expected 1 failed, got %d", grading.Summary.Failed)
		}
		if grading.Summary.Total != 3 {
			t.Errorf("expected 3 total, got %d", grading.Summary.Total)
		}
	})

	t.Run("nil result", func(t *testing.T) {
		t.Parallel()
		grading := ConvertToAnthropicGrading(nil)
		if len(grading.Expectations) != 0 {
			t.Errorf("expected 0 expectations, got %d", len(grading.Expectations))
		}
		if grading.Summary.Total != 0 {
			t.Errorf("expected 0 total, got %d", grading.Summary.Total)
		}
	})

	t.Run("empty assertions", func(t *testing.T) {
		t.Parallel()
		result := judge.NewResult(nil, 0, 0)
		grading := ConvertToAnthropicGrading(result)
		if len(grading.Expectations) != 0 {
			t.Errorf("expected 0 expectations, got %d", len(grading.Expectations))
		}
	})

	t.Run("judge context metadata", func(t *testing.T) {
		t.Parallel()
		result := judge.NewResult([]judge.AssertionResult{
			{Text: "check", Passed: true, Evidence: "ok"},
		}, 1, 1)
		result.JudgeContext = &judge.ContextMetadata{
			Profile:        "minimal",
			PromptDelivery: "file",
			PromptBytes:    512,
		}

		grading := ConvertToAnthropicGrading(result)
		if grading.JudgeContext == nil {
			t.Fatal("expected judge context metadata")
		}
		if grading.JudgeContext.Profile != "minimal" || grading.JudgeContext.PromptDelivery != "file" {
			t.Fatalf("unexpected judge context: %#v", grading.JudgeContext)
		}
	})
}

func TestConvertToAnthropicGrading_JSONFormat(t *testing.T) {
	t.Parallel()
	// Verify JSON output matches demo/grading.json format.
	result := judge.NewResult([]judge.AssertionResult{
		{Text: "response shows empathy for user fatigue (before the joke)", Passed: true, Evidence: "opening expresses empathy for tiredness"},
		{Text: "response includes a complete joke", Passed: true, Evidence: "contains a programmer interview joke"},
		{Text: "response ends with an encouraging or caring message", Passed: true, Evidence: "closing contains encouraging words"},
		{Text: "joke content is positive and uplifting", Passed: true, Evidence: "self-deprecating programmer humor"},
		{Text: "response uses emoji", Passed: true, Evidence: "uses 😄 😂 💪"},
	}, 1, 1)

	grading := ConvertToAnthropicGrading(result)

	data, err := json.MarshalIndent(grading, "", "  ")
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	// Verify key JSON fields exist.
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if _, ok := parsed["expectations"]; !ok {
		t.Error("missing 'expectations' field")
	}
	if _, ok := parsed["summary"]; !ok {
		t.Error("missing 'summary' field")
	}

	summary, ok := parsed["summary"].(map[string]any)
	if !ok {
		t.Fatal("summary is not map[string]any")
	}
	passRate, ok := summary["pass_rate"].(float64)
	if !ok {
		t.Fatal("pass_rate is not float64")
	}
	if passRate != 1.0 {
		t.Errorf("expected pass_rate 1.0, got %v", summary["pass_rate"])
	}
}

func TestWriteGradingJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "grading.json")

	grading := &AnthropicGrading{
		Expectations: []AnthropicExpectation{
			{Text: "test assertion", Passed: true, Evidence: "evidence here"},
		},
		Summary: AnthropicSummary{Passed: 1, Failed: 0, Total: 1, PassRate: 1.0},
	}

	if err := WriteGradingJSON(path, grading); err != nil {
		t.Fatalf("WriteGradingJSON error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file error: %v", err)
	}

	var loaded AnthropicGrading
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(loaded.Expectations) != 1 {
		t.Errorf("expected 1 expectation, got %d", len(loaded.Expectations))
	}
	if loaded.Summary.PassRate != 1.0 {
		t.Errorf("expected pass_rate 1.0, got %f", loaded.Summary.PassRate)
	}
}

func TestWriteEvalMetadata(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "eval_metadata.json")

	meta := &EvalMetadata{
		EvalID:   1,
		EvalName: "bored-coding",
		Prompt:   "I'm so bored, I've been coding all afternoon and my brain is about to explode",
		Assertions: []string{
			"response shows empathy for user fatigue (before the joke)",
			"response includes a complete joke",
		},
	}

	if err := WriteEvalMetadata(path, meta); err != nil {
		t.Fatalf("WriteEvalMetadata error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file error: %v", err)
	}

	var loaded EvalMetadata
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if loaded.EvalID != 1 {
		t.Errorf("expected eval_id 1, got %d", loaded.EvalID)
	}
	if loaded.EvalName != "bored-coding" {
		t.Errorf("expected eval_name 'bored-coding', got %s", loaded.EvalName)
	}
	if len(loaded.Assertions) != 2 {
		t.Errorf("expected 2 assertions, got %d", len(loaded.Assertions))
	}

	// Verify JSON field names match Anthropic format.
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	for _, key := range []string{"eval_id", "eval_name", "prompt", "assertions"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("missing JSON field %q", key)
		}
	}
}
