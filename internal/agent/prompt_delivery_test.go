package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/runtime"
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

type promptDeliveryTestRuntime struct {
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
func (r *promptDeliveryTestRuntime) Exec(context.Context, string, runtime.ExecOptions) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, nil
}
func (r *promptDeliveryTestRuntime) MergeEnv(map[string]string)   {}
func (r *promptDeliveryTestRuntime) Workspace() string            { return r.workspace }
func (r *promptDeliveryTestRuntime) RequiresProcessSandbox() bool { return false }
func (r *promptDeliveryTestRuntime) TargetGOOS() string           { return "linux" }
