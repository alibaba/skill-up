package evalevent

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestPayloadValidation(t *testing.T) {
	t.Parallel()

	validTask := TaskFields{
		TaskID:        "task-1",
		Iteration:     1,
		CaseID:        "目录/用例%1",
		Configuration: ConfigurationWithSkill,
		TaskIndex:     1,
		TaskTotal:     2,
		Title:         "Basic flow",
	}
	invalidRate := math.NaN()
	tests := []struct {
		name    string
		payload Payload
		wantErr string
	}{
		{name: "run started", payload: RunStartedPayload{Engine: "qoder-cli", SkillName: "skill", TaskTotal: 2, IterationsTotal: 1}},
		{name: "run started empty engine", payload: RunStartedPayload{SkillName: "skill", TaskTotal: 2, IterationsTotal: 1}, wantErr: "engine"},
		{name: "progress", payload: RunProgressPayload{Phase: RunPhaseExecuting, TaskTotal: 2, CompletedTasks: 1, RunningTasks: 1, ResultCounts: ResultCounts{Passed: 1}}},
		{name: "progress count mismatch", payload: RunProgressPayload{Phase: RunPhaseExecuting, TaskTotal: 2, CompletedTasks: 1}, wantErr: "result count sum"},
		{name: "progress exceeds total", payload: RunProgressPayload{Phase: RunPhaseExecuting, TaskTotal: 1, RunningTasks: 2}, wantErr: "exceeds task_total"},
		{name: "iteration started", payload: IterationStartedPayload{Iteration: 3}},
		{name: "case started", payload: CaseStartedPayload{TaskFields: validTask}},
		{name: "case invalid configuration", payload: CaseStartedPayload{TaskFields: TaskFields{TaskID: "task", Iteration: 1, CaseID: "case", Configuration: "other", TaskIndex: 1, TaskTotal: 1}}, wantErr: "configuration"},
		{name: "case complete", payload: CaseCompletedPayload{TaskFields: validTask, CompletedTasks: 1, Status: CaseStatusPass, DurationMS: 12}},
		{name: "case invalid pass rate", payload: CaseCompletedPayload{TaskFields: validTask, CompletedTasks: 1, Status: CaseStatusPass, PassRate: &invalidRate}, wantErr: "pass_rate"},
		{name: "iteration complete", payload: IterationCompletedPayload{Iteration: 1, InvocationCompletedTasks: 2, ResultCounts: ResultCounts{Passed: 1, Failed: 1}, DurationMS: 30}},
		{name: "run finished", payload: RunFinishedPayload{Status: RunStatusCompleted, CompletedTasks: 2, ResultCounts: ResultCounts{Passed: 1, Failed: 1}, DurationMS: 30}},
		{name: "run finished mismatch", payload: RunFinishedPayload{Status: RunStatusCompleted, CompletedTasks: 2, ResultCounts: ResultCounts{Passed: 1}}, wantErr: "result count sum"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.payload.validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("validate() error = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("validate() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestEventJSONUsesNormativeEnvelopeAndOmissions(t *testing.T) {
	t.Parallel()

	event := Event{
		ProtocolVersion: 1,
		EventVersion:    1,
		SequenceNumber:  1,
		InvocationID:    "018f8f20-7a7d-7d90-a192-4f5ec8f07a2a",
		Time:            time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC),
		Type:            EventCaseCompleted,
		Payload: CaseCompletedPayload{
			TaskFields: TaskFields{
				TaskID: "task-1", Iteration: 1, CaseID: "case-1",
				Configuration: ConfigurationWithSkill, TaskIndex: 1, TaskTotal: 1, Title: "Case",
			},
			CompletedTasks: 1,
			Status:         CaseStatusSkip,
			DurationMS:     7,
		},
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	text := string(encoded)
	for _, field := range []string{`"protocol_version":1`, `"event_version":1`, `"sequence_number":1`, `"event":"case_completed"`, `"payload":{`} {
		if !strings.Contains(text, field) {
			t.Errorf("encoded event missing %s: %s", field, text)
		}
	}
	for _, omitted := range []string{`"last_event"`, `"attributes"`, `"pass_rate"`} {
		if strings.Contains(text, omitted) {
			t.Errorf("encoded event unexpectedly contains %s: %s", omitted, text)
		}
	}
}
