package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/alibaba/skill-up/internal/logging"
)

const (
	defaultPromptInlineMaxBytes = 32 * 1024
	envPromptInlineMaxBytes     = "SKILL_UP_PROMPT_INLINE_MAX_BYTES"
)

// PromptDeliveryMetadata records how an agent prompt was delivered.
type PromptDeliveryMetadata struct {
	Mode           string `json:"mode"`
	PromptBytes    int    `json:"prompt_bytes"`
	InlineMaxBytes int    `json:"inline_max_bytes"`
	PromptPath     string `json:"prompt_path,omitempty"`
	RuntimePath    string `json:"runtime_path,omitempty"`
}

type promptCommandBuilder struct {
	Inline    func(string) string
	StdinFile func(string) string
}

func deliverPrompt(ctx context.Context, rt Runtime, opts ExecOptions, instruction string, builder promptCommandBuilder) (string, *PromptDeliveryMetadata, error) {
	threshold := promptInlineMaxBytes()
	meta := &PromptDeliveryMetadata{
		Mode:           "inline",
		PromptBytes:    len([]byte(instruction)),
		InlineMaxBytes: threshold,
	}
	if len([]byte(instruction)) <= threshold || builder.StdinFile == nil {
		return builder.Inline(instruction), meta, nil
	}

	runtimePath := filepath.Join(rt.Workspace(), ".skill-up", "prompts", "prompt.txt")
	if err := persistRuntimeArtifact(ctx, rt, runtimePath, instruction); err != nil {
		return "", nil, fmt.Errorf("deliver prompt file: %w", err)
	}
	hostPath, err := mirrorPromptArtifact(opts.ArtifactDir, instruction)
	if err != nil {
		return "", nil, err
	}
	meta.Mode = "file"
	meta.PromptPath = hostPath
	meta.RuntimePath = runtimePath
	logging.InfoContextf(ctx, "prompt delivery mode=file bytes=%d threshold=%d", meta.PromptBytes, threshold)
	return builder.StdinFile(runtimePath), meta, nil
}

func promptInlineMaxBytes() int {
	raw := os.Getenv(envPromptInlineMaxBytes)
	if raw == "" {
		return defaultPromptInlineMaxBytes
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return defaultPromptInlineMaxBytes
	}
	return n
}

func mirrorPromptArtifact(artifactDir, instruction string) (string, error) {
	if artifactDir == "" {
		return "", nil
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return "", fmt.Errorf("create prompt artifact dir: %w", err)
	}
	path := filepath.Join(artifactDir, "prompt.txt")
	if err := os.WriteFile(path, []byte(instruction), 0o600); err != nil {
		return "", fmt.Errorf("write prompt artifact: %w", err)
	}
	return path, nil
}
