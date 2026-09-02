//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEventLog_FullLifecycle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), `---
name: event-log-test
description: Event log end-to-end fixture.
---
`)
	writeFile(t, filepath.Join(dir, "evals", "eval.yaml"), `schema_version: v1alpha1
environment:
  type: none
skills:
  - source: local_path
    path: .
engine:
  name: qoder-cli
  model:
    provider: qoder
    name: auto
cases:
  files:
    - evals/cases/pass.yaml
  defaults:
    timeout_seconds: 30
    max_turns: 1
  parallelism: 2
judge:
  type: rule_based
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "pass.yaml"), `id: pass-case
title: Passing event case
input:
  prompt: Find the null pointer bug.
expect:
  must_contain:
    - "null"
`)

	outputDir := filepath.Join(dir, "artifacts")
	result := Run(t, RunConfig{
		Env:     mockEngineEnv(t),
		WorkDir: dir,
		Timeout: 60 * time.Second,
	},
		"run", filepath.Join(dir, "evals", "eval.yaml"),
		"--iteration", "2",
		"--baseline",
		"--output-dir", outputDir,
		"--no-delete",
		"--event-log", "events.jsonl",
		"--event-attribute", "com.example.build_id=build-1",
		"--event-attribute", "com.example.retry=2",
	)
	if result.ExitCode != 0 {
		t.Fatalf("event-enabled run failed: exit=%d\nstdout:\n%s\nstderr:\n%s", result.ExitCode, result.Stdout, result.Stderr)
	}

	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatal("event log does not end with a newline")
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	events := make([]eventLogRecord, len(lines))
	for i, line := range lines {
		if err := json.Unmarshal(line, &events[i]); err != nil {
			t.Fatalf("event line %d is invalid JSON: %v\n%s", i+1, err, line)
		}
	}
	if len(events) < 11 {
		t.Fatalf("event count = %d, want a complete lifecycle", len(events))
	}

	invocationID := events[0].InvocationID
	if invocationID == "" {
		t.Fatal("invocation_id is empty")
	}
	lastCount := 0
	for i, event := range events {
		if event.ProtocolVersion != 1 || event.EventVersion != 1 {
			t.Errorf("event %d versions = %d/%d", i+1, event.ProtocolVersion, event.EventVersion)
		}
		if event.SequenceNumber != uint64(i+1) {
			t.Errorf("event %d sequence = %d", i+1, event.SequenceNumber)
		}
		if event.InvocationID != invocationID {
			t.Errorf("event %d invocation = %q, want %q", i+1, event.InvocationID, invocationID)
		}
		if event.Attributes["com.example.build_id"] != "build-1" || event.Attributes["com.example.retry"] != "2" {
			t.Errorf("event %d attributes = %v", i+1, event.Attributes)
		}
		if event.LastEvent {
			lastCount++
			if i != len(events)-1 {
				t.Errorf("event %d is marked last before end of stream", i+1)
			}
		}
	}
	if lastCount != 1 {
		t.Fatalf("last_event count = %d, want 1", lastCount)
	}

	if events[0].Event != "run_started" {
		t.Fatalf("first event = %q, want run_started", events[0].Event)
	}
	assertPayloadNumber(t, events[0].Payload, "task_total", 4)
	assertPayloadNumber(t, events[0].Payload, "iterations_total", 2)
	if events[1].Event != "run_progress" || events[1].Payload["phase"] != "preparing" {
		t.Fatalf("second event = %q payload=%v, want preparing progress", events[1].Event, events[1].Payload)
	}

	startedTasks := make(map[string]eventLogRecord)
	completedTasks := make(map[string]eventLogRecord)
	iterationStarted := 0
	iterationCompleted := 0
	seenExecuting := false
	seenFinalizing := false
	for _, event := range events {
		switch event.Event {
		case "run_progress":
			seenExecuting = seenExecuting || event.Payload["phase"] == "executing"
			seenFinalizing = seenFinalizing || event.Payload["phase"] == "finalizing"
		case "iteration_started":
			iterationStarted++
		case "iteration_completed":
			iterationCompleted++
		case "case_started":
			startedTasks[event.Payload["task_id"].(string)] = event
		case "case_completed":
			completedTasks[event.Payload["task_id"].(string)] = event
		}
	}
	if !seenExecuting || !seenFinalizing {
		t.Fatalf("progress phases missing: executing=%t finalizing=%t", seenExecuting, seenFinalizing)
	}
	if iterationStarted != 2 || iterationCompleted != 2 {
		t.Fatalf("iteration events = started:%d completed:%d, want 2/2", iterationStarted, iterationCompleted)
	}
	if len(startedTasks) != 4 || len(completedTasks) != 4 {
		t.Fatalf("case events = started:%d completed:%d, want 4/4", len(startedTasks), len(completedTasks))
	}
	for taskID, completed := range completedTasks {
		if _, ok := startedTasks[taskID]; !ok {
			t.Errorf("completed task %q has no start event", taskID)
		}
		if completed.Payload["status"] != "PASS" {
			t.Errorf("task %q status = %v, want PASS", taskID, completed.Payload["status"])
		}
	}

	last := events[len(events)-1]
	if last.Event != "run_finished" || last.Payload["status"] != "COMPLETED" {
		t.Fatalf("last event = %q payload=%v, want completed run_finished", last.Event, last.Payload)
	}
	assertPayloadNumber(t, last.Payload, "completed_tasks", 4)
	assertPayloadNumber(t, last.Payload, "passed", 4)
	for _, iteration := range []string{"iteration-1", "iteration-2"} {
		if _, err := os.Stat(filepath.Join(outputDir, iteration, "result.json")); err != nil {
			t.Errorf("missing %s report: %v", iteration, err)
		}
	}
}

type eventLogRecord struct {
	ProtocolVersion uint64            `json:"protocol_version"`
	EventVersion    uint64            `json:"event_version"`
	SequenceNumber  uint64            `json:"sequence_number"`
	InvocationID    string            `json:"invocation_id"`
	Event           string            `json:"event"`
	LastEvent       bool              `json:"last_event"`
	Attributes      map[string]string `json:"attributes"`
	Payload         map[string]any    `json:"payload"`
}

func assertPayloadNumber(t *testing.T, payload map[string]any, name string, want float64) {
	t.Helper()
	if got := payload[name]; got != want {
		t.Fatalf("payload %s = %v, want %v", name, got, want)
	}
}
