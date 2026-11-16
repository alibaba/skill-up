//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/agent"
	evalruntime "github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

// TestCodexCLICompatibility exercises a real, preinstalled CLI against a local
// provider. Run separately with each CLI version and its supported wire API.
func TestCodexCLICompatibility(t *testing.T) {
	if os.Getenv("SKILL_UP_CODEX_COMPAT_E2E") != "1" {
		t.Skip("set SKILL_UP_CODEX_COMPAT_E2E=1 with a preinstalled Codex CLI")
	}
	wireAPI := os.Getenv("SKILL_UP_CODEX_WIRE_API")
	if wireAPI == "" {
		wireAPI = "chat"
	}
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", codexHome)
	server := httptest.NewServer(http.HandlerFunc(codexCompatResponse))
	defer server.Close()
	rt, err := evalruntime.NewRuntime(evalruntime.Config{Type: "none", Delete: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := rt.Create(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	ag := agent.NewCodexAgent(agent.Config{
		ModelProvider: "compat", ModelName: "gpt-5.4",
		BaseURL: server.URL + "/v1", APIKey: "local-fixture-key",
		Kwargs: map[string]string{"wire_api": wireAPI, "bypass_sandbox": "true"},
	})
	version, err := rt.Exec(ctx, "codex --version", agent.ExecOptions{})
	if err != nil || version.ExitCode != 0 {
		t.Fatalf("Codex unavailable: %v", err)
	}
	t.Logf("CLI=%s wire_api=%s (local fixture, not a live model)", strings.TrimSpace(version.Stdout), wireAPI)
	sessionID := ""
	for turn := 1; turn <= 2; turn++ {
		result, err := ag.RunTurn(ctx, rt, agent.ExecOptions{ArtifactDir: t.TempDir()},
			transcript.Message{Role: transcript.RoleUser, Content: "Reply COMPAT_OK.", Turn: turn}, sessionID)
		if err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}
		if result.FinalMessage != "COMPAT_OK" || result.SessionID == "" {
			t.Fatalf("turn %d: final=%q session=%q", turn, result.FinalMessage, result.SessionID)
		}
		if sessionID != "" && result.SessionID != sessionID {
			t.Fatalf("resumed session changed: %q -> %q", sessionID, result.SessionID)
		}
		sessionID = result.SessionID
	}
}

func codexCompatResponse(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	if strings.HasSuffix(r.URL.Path, "/chat/completions") {
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-compat\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"COMPAT_OK\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-compat\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/responses") {
		http.Error(w, "unexpected path", http.StatusNotFound)
		return
	}
	message := map[string]any{
		"id": "msg_compat", "type": "message", "role": "assistant", "status": "completed",
		"content": []any{map[string]any{"type": "output_text", "text": "COMPAT_OK", "annotations": []any{}}},
	}
	response := map[string]any{
		"id": "resp_compat", "object": "response", "status": "completed",
		"output": []any{message}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 2, "total_tokens": 12},
	}
	events := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_compat", "status": "in_progress", "output": []any{}}},
		{"type": "response.output_item.added", "output_index": 0, "item": message},
		{"type": "response.output_text.delta", "item_id": "msg_compat", "output_index": 0, "content_index": 0, "delta": "COMPAT_OK"},
		{"type": "response.output_item.done", "output_index": 0, "item": message},
		{"type": "response.completed", "response": response},
	}
	for _, event := range events {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
	}
}
