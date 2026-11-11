package judge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

type fileJudgeTestAgent struct {
	mockJudgeTestAgent

	calls   int
	paths   []string
	outputs []string
	chat    string
}

func (a *fileJudgeTestAgent) Run(_ context.Context, _ agent.Runtime, _ agent.ExecOptions, messages []transcript.Message) (*agent.SessionResult, error) {
	prompt := messages[len(messages)-1].Content
	marker := "- Edit the pre-created JSON template at "
	start := strings.LastIndex(prompt, marker)
	quoted := strings.SplitN(prompt[start+len(marker):], " using your file tools", 2)[0]
	path, err := strconv.Unquote(quoted)
	if err != nil {
		return nil, err
	}
	a.paths = append(a.paths, path)
	output := a.outputs[min(a.calls, len(a.outputs)-1)]
	a.calls++
	switch output {
	case "unchanged":
	case "missing":
		err = os.Remove(path)
	default:
		err = os.WriteFile(path, []byte(output), 0o600)
	}
	chat := a.chat
	if chat == "" {
		chat = "## Review complete\nBased on my analysis, see the result file."
	}
	return &agent.SessionResult{FinalMessage: chat}, err
}

func TestAgentJudgeFileDelivery(t *testing.T) {
	valid := `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":["observed"],"failures":[]}]}`
	failed := `{"results":[{"criterion_id":"criterion-1","passed":false,"evidence":["missing"],"failures":["not implemented"]}]}`
	for _, tt := range []struct {
		name    string
		outputs []string
		calls   int
		status  Status
		wantErr bool
	}{
		{"prose chat ignored", []string{valid}, 1, StatusPass, false},
		{"valid fail does not retry", []string{failed}, 1, StatusFail, false},
		{"unchanged template corrected", []string{"unchanged", valid}, 2, StatusPass, false},
		{"missing file corrected", []string{"missing", valid}, 2, StatusPass, false},
		{"malformed file corrected", []string{"not JSON", valid}, 2, StatusPass, false},
		{"unchanged twice rejected", []string{"unchanged"}, 2, "", true},
		{"valid chat cannot replace missing file", []string{"missing"}, 2, "", true},
		{"fenced file rejected", []string{"```json\n" + valid + "\n```"}, 2, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			rt, err := runtime.NewRuntime(runtime.Config{Type: "none"})
			assertNoError(t, err)
			assertNoError(t, rt.Create(ctx))
			t.Cleanup(func() { assertNoError(t, rt.Close()) })
			ag := &fileJudgeTestAgent{outputs: tt.outputs}
			if tt.name == "valid chat cannot replace missing file" {
				ag.chat = valid
			}
			j, err := NewJudge(config.JudgeConfig{Type: "agent_judge", Criteria: []string{"observed requirement"}, Output: &config.JudgeOutputConfig{Mode: agentJudgeFileMode}}, ag, rt)
			assertNoError(t, err)
			artifacts := t.TempDir()
			result, err := j.Evaluate(ctx, Input{FinalMessage: "review material", ArtifactDir: artifacts})
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				assertStatus(t, result, tt.status)
			}
			if ag.calls != tt.calls {
				t.Fatalf("calls %d, want %d", ag.calls, tt.calls)
			}
			if len(ag.paths) == 2 && ag.paths[0] == ag.paths[1] {
				t.Fatal("retry reused output path")
			}
			if tt.outputs[0] != "missing" {
				if _, err := os.Stat(filepath.Join(artifacts, "result-attempt-1.json")); err != nil {
					t.Fatal(err)
				}
			}
			expectedChat := ag.chat
			if expectedChat == "" {
				expectedChat = "## Review complete\nBased on my analysis, see the result file."
			}
			assertFileContents(t, filepath.Join(artifacts, agentJudgeRawResponseAttempt1), expectedChat)
		})
	}
}

func TestJudgeOutputConfigMerge(t *testing.T) {
	global := config.JudgeConfig{Type: "agent_judge", Output: &config.JudgeOutputConfig{Mode: agentJudgeFileMode}}
	for _, tt := range []struct {
		name  string
		local config.JudgeConfig
		want  string
	}{
		{"inherits", config.JudgeConfig{Type: "agent_judge"}, agentJudgeFileMode},
		{"overrides", config.JudgeConfig{Type: "agent_judge", Output: &config.JudgeOutputConfig{Mode: "final_message"}}, "final_message"},
		{"empty object resets", config.JudgeConfig{Type: "agent_judge", Output: &config.JudgeOutputConfig{}}, ""},
		{"other judge", config.JudgeConfig{Type: "rule_based"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeJudgeConfig(global, tt.local)
			mode := ""
			if got.Output != nil {
				mode = got.Output.Mode
			}
			if mode != tt.want {
				t.Fatalf("mode %q, want %q", mode, tt.want)
			}
		})
	}
}

type resumedFileJudgeTestAgent struct {
	fileJudgeTestAgent
}

func (a *resumedFileJudgeTestAgent) Run(ctx context.Context, rt agent.Runtime, opts agent.ExecOptions, messages []transcript.Message) (*agent.SessionResult, error) {
	result, err := a.fileJudgeTestAgent.Run(ctx, rt, opts, messages)
	result.SessionID = "file-judge-session"
	return result, err
}

func (a *resumedFileJudgeTestAgent) RunTurn(ctx context.Context, rt agent.Runtime, opts agent.ExecOptions, message transcript.Message, sessionID string) (*agent.SessionResult, error) {
	return a.Run(ctx, rt, opts, []transcript.Message{message})
}

func TestAgentJudgeFileDeliveryResumedCorrection(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none"})
	assertNoError(t, err)
	assertNoError(t, rt.Create(ctx))
	t.Cleanup(func() { assertNoError(t, rt.Close()) })
	ag := &resumedFileJudgeTestAgent{fileJudgeTestAgent: fileJudgeTestAgent{outputs: []string{"unchanged", `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":["observed"],"failures":[]}]}`}}}
	j := NewAgentJudge(ag, rt, "performance", []string{"observed requirement"}, nil, 0)
	j.OutputMode = agentJudgeFileMode
	result, err := j.Evaluate(ctx, Input{ArtifactDir: t.TempDir()})
	assertNoError(t, err)
	assertStatus(t, result, StatusPass)
	if ag.calls != 2 || ag.paths[0] == ag.paths[1] {
		t.Fatalf("expected resumed correction with fresh output path: %#v", ag.paths)
	}
}

type errorFileJudgeTestAgent struct {
	fileJudgeTestAgent

	failCall int
	runErr   error
}

func (a *errorFileJudgeTestAgent) Run(ctx context.Context, rt agent.Runtime, opts agent.ExecOptions, messages []transcript.Message) (*agent.SessionResult, error) {
	result, err := a.fileJudgeTestAgent.Run(ctx, rt, opts, messages)
	result.FinalMessage = ""
	if a.calls == a.failCall {
		err = errors.Join(err, a.runErr)
	}
	return result, err
}

func TestAgentJudgeFileRecoveryWithoutChat(t *testing.T) {
	valid := `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":["observed"],"failures":[]}]}`
	for _, tt := range []struct {
		name     string
		outputs  []string
		failCall int
		runErr   error
		wantErr  bool
	}{
		{"adapter error after valid file", []string{valid}, 1, errors.New("exit status 1"), false},
		{"deadline after valid file", []string{valid}, 1, context.DeadlineExceeded, false},
		{"correction error after valid file", []string{"unchanged", valid}, 2, errors.New("exit status 1"), false},
		{"explicit cancellation", []string{valid}, 1, context.Canceled, true},
		{"model fallback cannot recover", []string{valid}, 1, agent.ErrRequestedModelUnavailable, true},
		{"adapter error with invalid file", []string{"unchanged"}, 1, errors.New("exit status 1"), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			rt, err := runtime.NewRuntime(runtime.Config{Type: "none"})
			assertNoError(t, err)
			assertNoError(t, rt.Create(ctx))
			t.Cleanup(func() { assertNoError(t, rt.Close()) })
			ag := &errorFileJudgeTestAgent{fileJudgeTestAgent: fileJudgeTestAgent{outputs: tt.outputs}, failCall: tt.failCall, runErr: tt.runErr}
			j := NewAgentJudge(ag, rt, "performance", []string{"observed requirement"}, nil, 0)
			j.OutputMode = agentJudgeFileMode
			result, err := j.Evaluate(ctx, Input{ArtifactDir: t.TempDir()})
			if (err != nil) != tt.wantErr {
				t.Fatalf("error %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				assertStatus(t, result, StatusPass)
				if result.Recovery == nil || !result.Recovery.Recovered {
					t.Fatal("missing recovery disclosure")
				}
			}
			if ag.calls != tt.failCall {
				t.Fatalf("calls %d, want %d", ag.calls, tt.failCall)
			}
		})
	}
}

type contextCheckingDownloadRuntime struct {
	runtime.Runtime
}

func (r *contextCheckingDownloadRuntime) DownloadFile(ctx context.Context, source, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.Runtime.DownloadFile(ctx, source, target)
}

func TestAgentJudgeFileRecoveryTransferContext(t *testing.T) {
	ctx := context.Background()
	rt, err := runtime.NewRuntime(runtime.Config{Type: "none"})
	assertNoError(t, err)
	assertNoError(t, rt.Create(ctx))
	t.Cleanup(func() { assertNoError(t, rt.Close()) })
	j := NewAgentJudge(nil, &contextCheckingDownloadRuntime{Runtime: rt}, "performance", []string{"observed requirement"}, nil, 0)
	j.OutputMode = agentJudgeFileMode
	file, err := j.prepareOutputFile(ctx, t.TempDir(), 1)
	assertNoError(t, err)
	output := `{"results":[{"criterion_id":"criterion-1","passed":true,"evidence":["observed"],"failures":[]}]}`
	assertNoError(t, os.WriteFile(file.runtimePath, []byte(output), 0o600))
	expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel()
	_, err = j.readJudgeResults(expired, &agent.SessionResult{}, file, context.DeadlineExceeded)
	assertNoError(t, err)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = j.readJudgeResults(canceled, &agent.SessionResult{}, file, context.Canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("explicit cancellation lost: %v", err)
	}
}

func TestAgentJudgeDoesNotRecoverModelFallback(t *testing.T) {
	if canRecoverAgentJudgeResult(agent.ErrRequestedModelUnavailable, &agent.SessionResult{FinalMessage: `{"results":[]}`}) {
		t.Fatal("a model fallback must not become a recovered verdict")
	}
}
