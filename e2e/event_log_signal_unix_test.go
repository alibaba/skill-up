//go:build e2e && unix

package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestEventLogSignalsProduceCancelledFinalEvent(t *testing.T) {
	tests := []struct {
		name   string
		signal os.Signal
	}{
		{name: "SIGINT", signal: os.Interrupt},
		{name: "SIGTERM", signal: syscall.SIGTERM},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testEventLogSignalProducesCancelledFinalEvent(t, tt.signal)
		})
	}
}

func testEventLogSignalProducesCancelledFinalEvent(t *testing.T, terminationSignal os.Signal) {
	t.Helper()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), `---
name: event-log-signal-test
description: Event log signal handling fixture.
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
    - evals/cases/slow.yaml
  defaults:
    timeout_seconds: 120
    max_turns: 1
  parallelism: 1
judge:
  type: rule_based
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "slow.yaml"), `id: slow-case
title: Slow event case
input:
  prompt: Wait for cancellation.
`)

	eventPath := filepath.Join(dir, "events.jsonl")
	outputDir := filepath.Join(dir, "artifacts")
	futureArtifacts := []string{
		filepath.Join(outputDir, "iteration-2", "keep.txt"),
		filepath.Join(outputDir, "iteration-3", "keep.txt"),
	}
	for _, artifactPath := range futureArtifacts {
		if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
			t.Fatalf("create future iteration directory: %v", err)
		}
		if err := os.WriteFile(artifactPath, []byte("keep"), 0o600); err != nil {
			t.Fatalf("write future iteration artifact: %v", err)
		}
	}
	cmd := exec.Command(
		binaryPath,
		"run", filepath.Join(dir, "evals", "eval.yaml"),
		"--iteration", "3",
		"--output-dir", outputDir,
		"--event-log", eventPath,
	)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"SKILL_UP_CONFIG_DIR="+t.TempDir(),
		"NO_COLOR=1",
	)
	cmd.Env = append(cmd.Env, mockEngineEnv(t, "MOCK_TIMEOUT=60")...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
		close(waitCh)
	}()

	if err := waitForEventOrProcessExit(eventPath, "case_started", waitCh, 10*time.Second); err != nil {
		_ = cmd.Process.Kill()
		select {
		case <-waitCh:
		case <-time.After(5 * time.Second):
		}
		t.Fatalf("%v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if err := cmd.Process.Signal(terminationSignal); err != nil {
		_ = cmd.Process.Kill()
		<-waitCh
		t.Fatal(err)
	}

	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-waitCh
		t.Fatal("skill-up did not exit after receiving a termination signal")
	}
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("process error = %v, want graceful exit code 1\nstdout:\n%s\nstderr:\n%s", waitErr, stdout.String(), stderr.String())
	}

	events, err := readCompleteEventLog(eventPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("event log is empty")
	}
	last := events[len(events)-1]
	if last.Event != "run_finished" || !last.LastEvent || last.Payload["status"] != "CANCELLED" {
		t.Fatalf("last event = %+v, want final CANCELLED run_finished", last)
	}
	for _, event := range events {
		if (event.Event == "iteration_started" || event.Event == "case_started") && event.Payload["iteration"] != float64(1) {
			t.Fatalf("event emitted for a future iteration after cancellation: %+v", event)
		}
	}
	for _, artifactPath := range futureArtifacts {
		if data, err := os.ReadFile(artifactPath); err != nil || string(data) != "keep" {
			t.Fatalf("future iteration artifact changed: path=%s data=%q err=%v", artifactPath, data, err)
		}
	}
}

func TestEventLogSignalDuringBaselineReturnsNonZero(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), `---
name: event-log-baseline-signal-test
description: Event log baseline cancellation fixture.
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
    - evals/cases/baseline.yaml
  defaults:
    timeout_seconds: 120
    max_turns: 1
  parallelism: 1
judge:
  type: rule_based
`)
	writeFile(t, filepath.Join(dir, "evals", "cases", "baseline.yaml"), `id: baseline-case
title: Baseline cancellation case
input:
  prompt: Find the null pointer bug.
expect:
  must_contain:
    - "null"
`)

	eventPath := filepath.Join(dir, "events.jsonl")
	cmd := exec.Command(
		binaryPath,
		"run", filepath.Join(dir, "evals", "eval.yaml"),
		"--baseline",
		"--parallelism", "1",
		"--output-dir", filepath.Join(dir, "artifacts"),
		"--event-log", eventPath,
	)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"SKILL_UP_CONFIG_DIR="+t.TempDir(),
		"NO_COLOR=1",
	)
	cmd.Env = append(cmd.Env, baselineSignalEngineEnv(t)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
		close(waitCh)
	}()

	err := waitForMatchingEventOrProcessExit(
		eventPath,
		"without_skill case_started",
		func(event eventLogRecord) bool {
			return event.Event == "case_started" && event.Payload["configuration"] == "without_skill"
		},
		waitCh,
		10*time.Second,
	)
	if err != nil {
		_ = cmd.Process.Kill()
		select {
		case <-waitCh:
		case <-time.After(5 * time.Second):
		}
		t.Fatalf("%v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		_ = cmd.Process.Kill()
		<-waitCh
		t.Fatal(err)
	}

	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-waitCh
		t.Fatal("skill-up did not exit after cancelling the baseline task")
	}
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("process error = %v, want graceful exit code 1\nstdout:\n%s\nstderr:\n%s", waitErr, stdout.String(), stderr.String())
	}

	events, err := readCompleteEventLog(eventPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("event log is empty")
	}
	last := events[len(events)-1]
	if last.Event != "run_finished" || !last.LastEvent || last.Payload["status"] != "CANCELLED" {
		t.Fatalf("last event = %+v, want final CANCELLED run_finished", last)
	}
	withSkillPassed := false
	for _, event := range events {
		if event.Event == "case_completed" &&
			event.Payload["configuration"] == "with_skill" &&
			event.Payload["status"] == "PASS" {
			withSkillPassed = true
			break
		}
	}
	if !withSkillPassed {
		t.Fatal("with_skill task did not pass before baseline cancellation")
	}
}

func waitForEventOrProcessExit(path, eventName string, waitCh <-chan error, timeout time.Duration) error {
	return waitForMatchingEventOrProcessExit(
		path,
		eventName,
		func(event eventLogRecord) bool { return event.Event == eventName },
		waitCh,
		timeout,
	)
}

func waitForMatchingEventOrProcessExit(
	path string,
	description string,
	matches func(eventLogRecord) bool,
	waitCh <-chan error,
	timeout time.Duration,
) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-waitCh:
			return fmt.Errorf("process exited before %s: %w", description, err)
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for %s", description)
		case <-ticker.C:
			events, err := readCompleteEventLog(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			for _, event := range events {
				if matches(event) {
					return nil
				}
			}
		}
	}
}

func baselineSignalEngineEnv(t *testing.T) []string {
	t.Helper()

	home, binDir := mockEngineHome(t)
	enginePath := filepath.Join(binDir, "qodercli")
	if err := os.Remove(enginePath); err != nil {
		t.Fatalf("remove qodercli symlink: %v", err)
	}
	engineScript := `#!/bin/bash
set -euo pipefail
state_file="${MOCK_STATE_FILE:?}"
call_number=1
if [[ -f "$state_file" ]]; then
  call_number=$(( $(<"$state_file") + 1 ))
fi
printf '%s\n' "$call_number" > "$state_file"
if (( call_number > 1 )); then
  sleep 60
fi
printf '%s\n' 'Mock response contains null'
`
	if err := os.WriteFile(enginePath, []byte(engineScript), 0o755); err != nil {
		t.Fatalf("write baseline signal engine: %v", err)
	}
	return []string{
		"PATH=" + binDir + ":" + os.Getenv("PATH"),
		"HOME=" + home,
		"MOCK_STATE_FILE=" + filepath.Join(t.TempDir(), "calls"),
	}
}

func readCompleteEventLog(path string) ([]eventLogRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := bytes.Split(data, []byte("\n"))
	events := make([]eventLogRecord, 0, len(lines)-1)
	for i, line := range lines[:len(lines)-1] {
		if len(line) == 0 {
			continue
		}
		var event eventLogRecord
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("parse complete event line %d: %w", i+1, err)
		}
		events = append(events, event)
	}
	return events, nil
}
