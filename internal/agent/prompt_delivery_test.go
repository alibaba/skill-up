package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/platform"
	"github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

func TestDeliverPrompt_InlineBelowThreshold(t *testing.T) {
	t.Setenv(envPromptInlineMaxBytes, "16")
	rt := &promptDeliveryTestRuntime{workspace: t.TempDir()}

	cmd, meta, err := deliverPrompt(context.Background(), rt, ExecOptions{ArtifactDir: t.TempDir()}, "short", promptCommandBuilder{
		Inline: func(prompt string) string { return "run " + shellQuote(prompt) },
		StdinFile: func(path string) string {
			return "cat " + shellQuote(path) + " | run"
		},
	})
	if err != nil {
		t.Fatalf("deliverPrompt returned error: %v", err)
	}
	if cmd != "run 'short'" {
		t.Fatalf("unexpected inline command: %q", cmd)
	}
	if meta.Mode != "inline" {
		t.Fatalf("mode = %q, want inline", meta.Mode)
	}
	if rt.uploadedTarget != "" {
		t.Fatalf("did not expect upload for inline prompt, got %q", rt.uploadedTarget)
	}
}

func TestDeliverPrompt_FileAboveThreshold(t *testing.T) {
	t.Setenv(envPromptInlineMaxBytes, "16")
	artifactDir := t.TempDir()
	workspace := t.TempDir()
	rt := &promptDeliveryTestRuntime{workspace: workspace}
	instruction := strings.Repeat("x", 64)

	cmd, meta, err := deliverPrompt(context.Background(), rt, ExecOptions{ArtifactDir: artifactDir}, instruction, promptCommandBuilder{
		Inline: func(prompt string) string { return "run " + shellQuote(prompt) },
		StdinFile: func(path string) string {
			return "cat " + shellQuote(path) + " | run"
		},
	})
	if err != nil {
		t.Fatalf("deliverPrompt returned error: %v", err)
	}
	if meta.Mode != "file" {
		t.Fatalf("mode = %q, want file", meta.Mode)
	}
	if strings.Contains(cmd, instruction) {
		t.Fatalf("file-mode command should not contain full instruction: %q", cmd)
	}
	if !strings.Contains(cmd, ".skill-up/prompts/prompt.txt") {
		t.Fatalf("file-mode command should reference runtime prompt path: %q", cmd)
	}
	if meta.PromptPath != filepath.Join(artifactDir, "prompt.txt") {
		t.Fatalf("PromptPath = %q", meta.PromptPath)
	}
	data, err := os.ReadFile(meta.PromptPath)
	if err != nil {
		t.Fatalf("read prompt artifact: %v", err)
	}
	if string(data) != instruction {
		t.Fatalf("prompt artifact mismatch")
	}
	if rt.uploadedContent != instruction {
		t.Fatalf("uploaded content mismatch")
	}
}

func TestDeliverPrompt_RequiresInlineBuilder(t *testing.T) {
	t.Setenv(envPromptInlineMaxBytes, "16")
	rt := &promptDeliveryTestRuntime{workspace: t.TempDir()}

	_, _, err := deliverPrompt(context.Background(), rt, ExecOptions{}, "short", promptCommandBuilder{})
	if err == nil {
		t.Fatal("expected missing inline builder error")
	}
	if !strings.Contains(err.Error(), "inline command builder") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeliverPrompt_RejectsOversizedPromptWithoutStdinFileBuilder(t *testing.T) {
	t.Setenv(envPromptInlineMaxBytes, "16")
	rt := &promptDeliveryTestRuntime{workspace: t.TempDir()}
	instruction := strings.Repeat("x", 64)

	cmd, meta, err := deliverPrompt(context.Background(), rt, ExecOptions{}, instruction, promptCommandBuilder{
		Inline: func(prompt string) string { return "run " + shellQuote(prompt) },
	})
	if err == nil {
		t.Fatal("expected oversized prompt error")
	}
	if cmd != "" {
		t.Fatalf("unexpected command on error: %q", cmd)
	}
	if meta == nil || meta.Mode != "inline" {
		t.Fatalf("expected inline metadata on error, got %#v", meta)
	}
	if !strings.Contains(err.Error(), "stdin file command builder") {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt.uploadedTarget != "" {
		t.Fatalf("did not expect upload on error, got %q", rt.uploadedTarget)
	}
}

type promptDeliveryTestRuntime struct {
	shell           platform.Shell
	workspace       string
	uploadedTarget  string
	uploadedContent string
}

func (r *promptDeliveryTestRuntime) Create(context.Context) error { return nil }
func (r *promptDeliveryTestRuntime) Close() error                 { return nil }
func (r *promptDeliveryTestRuntime) Start(context.Context) error  { return nil }
func (r *promptDeliveryTestRuntime) Stop(context.Context) error   { return nil }
func (r *promptDeliveryTestRuntime) UploadFile(_ context.Context, sourcePath, targetPath string) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	r.uploadedTarget = targetPath
	r.uploadedContent = string(data)
	return nil
}
func (r *promptDeliveryTestRuntime) UploadDir(context.Context, string, string) error { return nil }
func (r *promptDeliveryTestRuntime) DownloadFile(context.Context, string, string) error {
	return nil
}
func (r *promptDeliveryTestRuntime) DownloadDir(context.Context, string, string) error { return nil }
func (r *promptDeliveryTestRuntime) Exists(context.Context, string) (bool, error) {
	return false, runtime.ErrFileNotFound
}

func (r *promptDeliveryTestRuntime) ReadFile(context.Context, string) ([]byte, error) {
	return nil, runtime.ErrFileNotFound
}

func (r *promptDeliveryTestRuntime) Exec(context.Context, string, runtime.ExecOptions) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, nil
}
func (r *promptDeliveryTestRuntime) MergeEnv(map[string]string)   {}
func (r *promptDeliveryTestRuntime) Workspace() string            { return r.workspace }
func (r *promptDeliveryTestRuntime) RequiresProcessSandbox() bool { return false }
func (r *promptDeliveryTestRuntime) Shell() platform.Shell {
	if r.shell.GOOS != "" {
		return r.shell
	}
	return platform.Shell{GOOS: platform.GOOSLinux, Family: platform.ShellPOSIX}
}

func TestDeliverPrompt_WindowsUsesFile(t *testing.T) {
	for _, threshold := range []string{"", "65536"} {
		t.Run("threshold="+threshold, func(t *testing.T) {
			t.Setenv(envPromptInlineMaxBytes, threshold)
			for _, prompt := range []string{"short", strings.Repeat("agent's output\n", 1000)} {
				rt := &promptDeliveryTestRuntime{
					workspace: t.TempDir(),
					shell:     platform.Shell{GOOS: platform.GOOSWindows, Family: platform.ShellPOSIX, BashPath: "bash.exe"},
				}
				cmd, meta, err := deliverPrompt(context.Background(), rt, ExecOptions{}, prompt, promptCommandBuilder{
					Inline:    func(string) string { t.Fatal("Windows prompt must not be in argv"); return "" },
					StdinFile: func(path string) string { return "cat " + shellQuote(path) + " | run" },
				})
				if err != nil {
					t.Fatal(err)
				}
				if meta.Mode != promptDeliveryModeFile || meta.InlineMaxBytes != 0 || rt.uploadedContent != prompt || strings.Contains(cmd, prompt) {
					t.Fatalf("Windows file delivery failed: metadata=%+v command=%q", meta, cmd)
				}
			}
		})
	}
}

func TestResumedPromptDelivery(t *testing.T) {
	for _, engine := range []string{"claude_code", "qodercli", "qodercn"} {
		for _, tc := range []struct {
			name, goos, threshold, prompt, mode string
			failUpload                          bool
		}{
			{"inline", platform.GOOSLinux, "1024", "follow up", "inline", false},
			{"large", platform.GOOSLinux, "16", strings.Repeat("agent's reply\n", 3000), "file", false},
			{"windows", platform.GOOSWindows, "65536", "follow up", "file", false},
			{"upload failure", platform.GOOSLinux, "0", "follow up", "file", true},
		} {
			t.Run(engine+"/"+tc.name, func(t *testing.T) {
				t.Setenv(envPromptInlineMaxBytes, tc.threshold)
				var ag SessionResumer = NewClaudeCodeAgent(Config{ModelName: "model-a"})
				if engine != "claude_code" {
					edition := qoderEditionGlobal
					if engine == "qodercn" {
						edition = qoderEditionCN
					}
					ag = NewQoderCLIAgent(Config{ModelName: "model-a", Kwargs: map[string]string{KwargEdition: edition}})
				}
				rt := &resumedPromptRuntime{
					promptDeliveryTestRuntime: promptDeliveryTestRuntime{
						workspace: filepath.Join(t.TempDir(), "workspace with spaces"),
						shell:     platform.Shell{GOOS: tc.goos, Family: platform.ShellPOSIX, BashPath: "bash.exe"},
					},
					failUpload: tc.failUpload,
				}
				artifactDir := t.TempDir()
				result, err := ag.RunTurn(context.Background(), rt, ExecOptions{ArtifactDir: artifactDir},
					transcript.Message{Role: transcript.RoleUser, Content: tc.prompt}, "existing-session")
				if tc.failUpload {
					if err == nil || result == nil || result.ExitCode != 1 || result.SessionID != "existing-session" || rt.command != "" {
						t.Fatalf("upload failure: result=%+v err=%v command=%q", result, err, rt.command)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				assertResumedPromptDelivery(t, rt, result, engine, tc.mode, tc.prompt, artifactDir)
			})
		}
	}
}

func assertResumedPromptDelivery(t *testing.T, rt *resumedPromptRuntime, result *SessionResult, engine, mode, prompt, artifactDir string) {
	t.Helper()
	if result.SessionID != "existing-session" || result.PromptDelivery == nil || result.PromptDelivery.Mode != mode {
		t.Fatalf("resume metadata: session=%q delivery=%+v", result.SessionID, result.PromptDelivery)
	}
	resumeFlag := " -r 'existing-session'"
	binary := engine
	if engine == "claude_code" {
		binary = "claude"
		resumeFlag = " --resume existing-session"
	}
	if !strings.Contains(rt.command, resumeFlag) || !strings.Contains(rt.command, " --model 'model-a'") || !strings.Contains(rt.command, binary+" ") {
		t.Fatalf("resume identity lost: %s", rt.command)
	}
	if mode == "inline" {
		if result.Artifacts != nil && slices.Contains(result.Artifacts.GeneratedFileSources, filepath.Join(rt.Workspace(), ".skill-up", "prompts", "prompt.txt")) {
			t.Fatal("inline delivery excluded a file it did not write")
		}
		if !strings.Contains(rt.command, shellQuote(prompt)) || rt.uploadedTarget != "" {
			t.Fatalf("incorrect inline delivery: %s", rt.command)
		}
		return
	}
	if result.Artifacts == nil || !slices.Contains(result.Artifacts.GeneratedFileSources, rt.uploadedTarget) {
		t.Fatalf("workspace diff exclusions omit the delivered prompt %q", rt.uploadedTarget)
	}
	assertResumedPromptFile(t, rt, engine, prompt, artifactDir)
}

func assertResumedPromptFile(t *testing.T, rt *resumedPromptRuntime, engine, prompt, artifactDir string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(artifactDir, "prompt.txt"))
	if err != nil || string(data) != prompt || rt.uploadedContent != prompt {
		t.Fatalf("prompt bytes not preserved: %v", err)
	}
	if !strings.HasPrefix(rt.command, "cat "+shellQuote(rt.uploadedTarget)+" | ") || strings.Contains(rt.command, prompt) {
		t.Fatalf("incorrect file delivery: %s", rt.command)
	}
	if engine != "claude_code" && !strings.HasSuffix(rt.command, " -p '-'") {
		t.Fatalf("Qoder must read stdin: %s", rt.command)
	}
}

type resumedPromptRuntime struct {
	promptDeliveryTestRuntime

	command    string
	failUpload bool
}

func (r *resumedPromptRuntime) UploadFile(ctx context.Context, source, target string) error {
	if r.failUpload {
		return errors.New("upload failed")
	}
	return r.promptDeliveryTestRuntime.UploadFile(ctx, source, target)
}

func (r *resumedPromptRuntime) Exec(_ context.Context, command string, _ runtime.ExecOptions) (runtime.ExecResult, error) {
	if strings.Contains(command, "--permission-mode") {
		r.command = command
		return runtime.ExecResult{Stdout: `{"type":"result","result":"OK"}`}, nil
	}
	return runtime.ExecResult{}, nil
}
