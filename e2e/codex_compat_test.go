//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alibaba/skill-up/internal/agent"
	"github.com/alibaba/skill-up/internal/credential"
	evalruntime "github.com/alibaba/skill-up/internal/runtime"
	"github.com/alibaba/skill-up/pkg/transcript"
)

const (
	codexFirstPrompt  = "Remember COMPAT_USER_ONE. Reply COMPAT_ASSISTANT_ONE."
	codexSecondPrompt = "This is COMPAT_USER_TWO. Recall the previous exchange."
)

// TestCodexCLICompatibility exercises a real, preinstalled CLI against a local
// provider. Run separately with each CLI version and its supported wire API.
func TestCodexCLICompatibility(t *testing.T) {
	if os.Getenv("SKILL_UP_CODEX_COMPAT_E2E") != "1" {
		t.Skip("set SKILL_UP_CODEX_COMPAT_E2E=1 with a preinstalled Codex CLI")
	}
	wireAPI := codexCompatibilityWireAPI(t)
	for _, homeMode := range []string{evalruntime.AgentHomeInherited, evalruntime.AgentHomeTemporary} {
		t.Run(homeMode, func(t *testing.T) {
			runCodexCLICompatibility(t, wireAPI, homeMode)
		})
	}
}

func runCodexCLICompatibility(t *testing.T, wireAPI, homeMode string) {
	rt, ctx := codexCompatibilityRuntime(t, homeMode)
	var mu sync.Mutex
	var requests [][]codexCompatMessage
	var requestErrors []error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messages, err := decodeCodexCompatRequest(r, wireAPI)
		mu.Lock()
		requests = append(requests, messages)
		if err != nil {
			requestErrors = append(requestErrors, err)
		}
		mu.Unlock()
		reply := "COMPAT_ASSISTANT_ONE"
		if codexContainsMessage(messages, "user", codexSecondPrompt) {
			reply = "COMPAT_ASSISTANT_TWO"
		}
		codexCompatResponse(w, r, reply)
	}))
	defer server.Close()
	params := agent.ResolveAdapterConfig(credential.ResolvedAgentConfig{
		Engine: "codex", Provider: "compat", Model: "gpt-5.4",
		BaseURL: server.URL + "/v1", APIKey: "local-fixture-key",
		Kwargs: map[string]string{"wire_api": wireAPI, "bypass_sandbox": "true"},
	}, nil)
	ag := agent.NewCodexAgent(agent.Config{
		ModelProvider: params.AppliedProvider, ModelName: params.AppliedModel,
		BaseURL: params.AppliedBaseURL, APIKey: params.AppliedAPIKey, Kwargs: params.Kwargs,
	})
	sessionID := ""
	for i, prompt := range []string{codexFirstPrompt, codexSecondPrompt} {
		artifactDir := t.TempDir()
		result, err := ag.RunTurn(ctx, rt, agent.ExecOptions{ArtifactDir: artifactDir},
			transcript.Message{Role: transcript.RoleUser, Content: prompt, Turn: i + 1}, sessionID)
		if err != nil {
			t.Fatalf("turn %d: %v", i+1, err)
		}
		want := []string{"COMPAT_ASSISTANT_ONE", "COMPAT_ASSISTANT_TWO"}[i]
		if result.FinalMessage != want || result.SessionID == "" {
			t.Fatalf("turn %d: final=%q session=%q", i+1, result.FinalMessage, result.SessionID)
		}
		if sessionID != "" && result.SessionID != sessionID {
			t.Fatalf("resumed session changed: %q -> %q", sessionID, result.SessionID)
		}
		sessionID = result.SessionID
		if homeMode == evalruntime.AgentHomeTemporary {
			checkCodexTemporaryHome(t, rt, artifactDir, sessionID, prompt, want)
		} else if _, err := os.Stat(filepath.Join(os.Getenv("CODEX_HOME"), "auth.json")); !os.IsNotExist(err) {
			t.Fatalf("inherited mode must not create an API-key login: %v", err)
		}
	}
	mu.Lock()
	recordedRequests := append([][]codexCompatMessage(nil), requests...)
	recordedErrors := append([]error(nil), requestErrors...)
	mu.Unlock()
	checkCodexCompatRequests(t, recordedRequests, recordedErrors)

	checkCodexOldResumeRejected(t, ctx, sessionID)
	mu.Lock()
	count := len(requests)
	mu.Unlock()
	if count != len(recordedRequests) {
		t.Fatal("pre-fix invocation unexpectedly reached the provider")
	}
}

func checkCodexTemporaryHome(t *testing.T, rt agent.Runtime, artifactDir, sessionID, prompt, reply string) {
	t.Helper()
	home := filepath.Dir(rt.Workspace())
	authData, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		t.Fatalf("read temporary Codex login: %v", err)
	}
	var auth struct {
		APIKey string `json:"OPENAI_API_KEY"`
	}
	if err := json.Unmarshal(authData, &auth); err != nil || auth.APIKey != "local-fixture-key" {
		t.Fatal("temporary Codex home does not contain the configured API-key login")
	}
	files, err := filepath.Glob(filepath.Join(artifactDir, "rollout-*"+sessionID+".jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("expected one collected session JSONL for %s, got %v: %v", sessionID, files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil || !bytes.Contains(data, []byte(prompt)) || !bytes.Contains(data, []byte(reply)) {
		t.Fatalf("collected session %s: read=%v, prompt present=%t, reply present=%t",
			files[0], err, bytes.Contains(data, []byte(prompt)), bytes.Contains(data, []byte(reply)))
	}
	sourceFiles, err := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", filepath.Base(files[0])))
	if err != nil || len(sourceFiles) != 1 {
		t.Fatalf("collected session must originate in the temporary home: %v, %v", sourceFiles, err)
	}
	source, err := os.ReadFile(sourceFiles[0])
	if err != nil || !bytes.Equal(data, source) {
		t.Fatalf("collected session %s differs from temporary-home session %s: %v", files[0], sourceFiles[0], err)
	}
	entries, err := os.ReadDir(os.Getenv("CODEX_HOME"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary mode wrote to the inherited Codex home: %v, %v", entries, err)
	}
	entries, err = os.ReadDir(os.Getenv("HOME"))
	if err != nil || len(entries) != 1 || entries[0].Name() != ".codex" {
		t.Fatalf("temporary mode wrote to the inherited home: %v, %v", entries, err)
	}
}

func checkCodexCompatRequests(t *testing.T, recordedRequests [][]codexCompatMessage, recordedErrors []error) {
	t.Helper()
	if len(recordedErrors) != 0 {
		t.Fatalf("invalid provider requests: %v", recordedErrors)
	}
	if len(recordedRequests) != 2 {
		t.Fatalf("want one request per turn, got %d", len(recordedRequests))
	}
	if !codexContainsMessage(recordedRequests[0], "user", codexFirstPrompt) ||
		codexContainsMessage(recordedRequests[0], "user", codexSecondPrompt) {
		t.Fatal("first request does not contain only the first user turn")
	}
	if err := checkCodexResumeHistory(recordedRequests[1]); err != nil {
		t.Fatal(err)
	}
}

func checkCodexOldResumeRejected(t *testing.T, ctx context.Context, sessionID string) {
	t.Helper()
	if os.Getenv("SKILL_UP_CODEX_EXPECTED_VERSION") != "0.80.0" {
		return
	}
	// Keep a real-parser negative control for the original 0.80.0 failure.
	cmd := exec.CommandContext(ctx, "codex", "exec", "resume", "--output-last-message",
		filepath.Join(t.TempDir(), "last-message"), sessionID, codexSecondPrompt)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "unexpected argument '--output-last-message'") {
		t.Fatalf("old argument order must fail at CLI parsing: err=%v output=%s", err, output)
	}
	t.Log("negative control: pre-fix argument order rejected by real Codex 0.80.0")
}

// TestCodexDashScopeMultiTurn is deliberately separate from the deterministic
// fixture: success here requires real inference and consumes provider quota.
func TestCodexDashScopeMultiTurn(t *testing.T) {
	if os.Getenv("SKILL_UP_CODEX_LIVE_E2E") != "1" {
		t.Skip("set SKILL_UP_CODEX_LIVE_E2E=1 to use real DashScope inference")
	}
	key := os.Getenv("DASHSCOPE_API_KEY")
	if key == "" {
		t.Fatal("DASHSCOPE_API_KEY is required")
	}
	wireAPI := codexCompatibilityWireAPI(t)
	rt, ctx := codexCompatibilityRuntime(t, evalruntime.AgentHomeInherited)
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	}
	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "qwen3.6-plus"
	}
	ag := agent.NewCodexAgent(agent.Config{
		ModelProvider: "dashscope", ModelName: model, BaseURL: baseURL, APIKey: key,
		Kwargs: map[string]string{"wire_api": wireAPI, "bypass_sandbox": "true"},
	})
	// The second prompt never repeats the nonce, so a fresh session cannot pass.
	nonce := fmt.Sprintf("MEMORY_%d", time.Now().UnixNano())
	sessionID := ""
	for i, prompt := range []string{
		"Remember this exact token: " + nonce + ". Reply only ACK. Do not use tools.",
		"What exact token did I ask you to remember? Reply only that token. Do not use tools.",
	} {
		result, err := ag.RunTurn(ctx, rt, agent.ExecOptions{ArtifactDir: t.TempDir()},
			transcript.Message{Role: transcript.RoleUser, Content: prompt, Turn: i + 1}, sessionID)
		if err != nil {
			t.Fatalf("live turn %d: %v", i+1, err)
		}
		want := "ACK"
		if i == 1 {
			want = nonce
		}
		if strings.TrimSpace(result.FinalMessage) != want || result.SessionID == "" {
			t.Fatalf("live turn %d: unexpected answer or missing session", i+1)
		}
		if sessionID != "" && result.SessionID != sessionID {
			t.Fatal("live resume changed session")
		}
		sessionID = result.SessionID
	}
	t.Logf("real DashScope two-turn recall passed: model=%s wire_api=%s", model, wireAPI)
}

func codexCompatibilityWireAPI(t *testing.T) string {
	t.Helper()
	wireAPI := os.Getenv("SKILL_UP_CODEX_WIRE_API")
	if wireAPI != "chat" && wireAPI != "responses" {
		t.Fatal("SKILL_UP_CODEX_WIRE_API must be chat or responses")
	}
	return wireAPI
}

func codexCompatibilityRuntime(t *testing.T, homeMode string) (agent.Runtime, context.Context) {
	t.Helper()
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", codexHome)
	rt, err := evalruntime.NewRuntime(evalruntime.Config{Type: "none", Delete: true, AgentHome: homeMode})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	if err := rt.Create(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if homeMode == evalruntime.AgentHomeTemporary {
		result, err := rt.Exec(ctx, `printf '%s\n%s' "$HOME" "$CODEX_HOME"`, agent.ExecOptions{})
		want := filepath.Dir(rt.Workspace())
		if err != nil || result.ExitCode != 0 || result.Stdout != want+"\n"+want {
			t.Fatalf("runtime did not redirect HOME and CODEX_HOME: err=%v output=%q", err, result.Stdout)
		}
	}
	version, err := rt.Exec(ctx, "codex --version", agent.ExecOptions{})
	if err != nil || version.ExitCode != 0 {
		t.Fatalf("Codex unavailable: err=%v exit=%d stderr=%s", err, version.ExitCode, version.Stderr)
	}
	expected := os.Getenv("SKILL_UP_CODEX_EXPECTED_VERSION")
	if expected == "" || strings.TrimSpace(version.Stdout) != "codex-cli "+expected {
		t.Fatalf("expected pinned Codex %q, got %q", expected, strings.TrimSpace(version.Stdout))
	}
	t.Logf("CLI=%s", strings.TrimSpace(version.Stdout))
	return rt, ctx
}

type codexCompatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (m codexCompatMessage) text() string {
	var text string
	if json.Unmarshal(m.Content, &text) == nil {
		return text
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		parts = append(parts, block.Text)
	}
	return strings.Join(parts, "\n")
}

func decodeCodexCompatRequest(r *http.Request, wireAPI string) ([]codexCompatMessage, error) {
	path, field := "/v1/chat/completions", "messages"
	if wireAPI == "responses" {
		path, field = "/v1/responses", "input"
	}
	if r.Method != http.MethodPost || r.URL.Path != path {
		return nil, fmt.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode provider body: %w", err)
	}
	var messages []codexCompatMessage
	if err := json.Unmarshal(body[field], &messages); err != nil {
		return nil, fmt.Errorf("decode %s: %w", field, err)
	}
	return messages, nil
}

func codexContainsMessage(messages []codexCompatMessage, role, text string) bool {
	for _, message := range messages {
		if message.Role == role && strings.Contains(message.text(), text) {
			return true
		}
	}
	return false
}

func checkCodexResumeHistory(messages []codexCompatMessage) error {
	want := []struct{ role, text string }{
		{"user", codexFirstPrompt},
		{"assistant", "COMPAT_ASSISTANT_ONE"},
		{"user", codexSecondPrompt},
	}
	next := 0
	for _, message := range messages {
		if message.Role == want[next].role && strings.Contains(message.text(), want[next].text) {
			next++
			if next == len(want) {
				return nil
			}
		}
	}
	return fmt.Errorf("resumed request missing ordered history at %s %q", want[next].role, want[next].text)
}

func TestCodexResumeHistoryValidation(t *testing.T) {
	for _, wireAPI := range []string{"chat", "responses"} {
		t.Run(wireAPI, func(t *testing.T) {
			messages := []map[string]any{
				{"role": "user", "content": codexFirstPrompt},
				{"role": "assistant", "content": "COMPAT_ASSISTANT_ONE"},
				{"role": "user", "content": codexSecondPrompt},
			}
			path, field := "/v1/chat/completions", "messages"
			if wireAPI == "responses" {
				path, field = "/v1/responses", "input"
				for _, message := range messages {
					message["content"] = []map[string]any{{"type": "input_text", "text": message["content"]}}
				}
			}
			for _, tc := range []struct {
				name    string
				indices []int
				wantErr bool
			}{
				{"complete", []int{0, 1, 2}, false},
				{"fresh-session", []int{2}, true},
				{"missing-user", []int{1, 2}, true},
				{"missing-assistant", []int{0, 2}, true},
				{"missing-current-turn", []int{0, 1}, true},
				{"reordered", []int{1, 0, 2}, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var selected []map[string]any
					for _, index := range tc.indices {
						selected = append(selected, messages[index])
					}
					body, err := json.Marshal(map[string]any{field: selected})
					if err != nil {
						t.Fatal(err)
					}
					request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(string(body)))
					decoded, err := decodeCodexCompatRequest(request, wireAPI)
					if err != nil {
						t.Fatal(err)
					}
					if err := checkCodexResumeHistory(decoded); (err != nil) != tc.wantErr {
						t.Fatalf("history validation: err=%v wantErr=%v", err, tc.wantErr)
					}
				})
			}
		})
	}
}

func codexCompatResponse(w http.ResponseWriter, r *http.Request, reply string) {
	w.Header().Set("Content-Type", "text/event-stream")
	if strings.HasSuffix(r.URL.Path, "/chat/completions") {
		_, _ = fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-compat\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":%q},\"finish_reason\":null}]}\n\n", reply)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-compat\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/responses") {
		http.Error(w, "unexpected path", http.StatusNotFound)
		return
	}
	message := map[string]any{
		"id": "msg_compat", "type": "message", "role": "assistant", "status": "completed",
		"content": []any{map[string]any{"type": "output_text", "text": reply, "annotations": []any{}}},
	}
	response := map[string]any{
		"id": "resp_compat", "object": "response", "status": "completed",
		"output": []any{message}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 2, "total_tokens": 12},
	}
	events := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_compat", "status": "in_progress", "output": []any{}}},
		{"type": "response.output_item.added", "output_index": 0, "item": message},
		{"type": "response.output_text.delta", "item_id": "msg_compat", "output_index": 0, "content_index": 0, "delta": reply},
		{"type": "response.output_item.done", "output_index": 0, "item": message},
		{"type": "response.completed", "response": response},
	}
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			http.Error(w, "cannot encode fixture event", http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
	}
}
