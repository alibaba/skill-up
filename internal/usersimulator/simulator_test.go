package usersimulator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alibaba/skill-up/internal/config"
	"github.com/alibaba/skill-up/internal/credential"
)

func TestParseDecision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		raw       string
		policy    ResponsePolicy
		wantError bool
	}{
		{"reply", `{"action":"reply","message":"staging"}`, MustReply, false},
		{"fenced reply", "```json\n{\"action\":\"reply\",\"message\":\"staging\"}\n```", MustReply, false},
		{"fenced stop", "```\n{\"action\":\"stop\",\"reason\":\"goal reached\"}\n```", MayStop, false},
		{"fenced unknown field", "```json\n{\"action\":\"reply\",\"message\":\"yes\",\"extra\":true}\n```", MayStop, true},
		{"fenced trailing text", "```json\n{\"action\":\"reply\",\"message\":\"yes\"}\n``` extra", MayStop, true},
		{"fenced stop in fixed turn", "```json\n{\"action\":\"stop\",\"reason\":\"goal reached\"}\n```", MustReply, true},
		{"stop", `{"action":"stop","reason":"goal reached"}`, MayStop, false},
		{"stop in fixed turn", `{"action":"stop","reason":"goal reached"}`, MustReply, true},
		{"empty reply", `{"action":"reply","message":" "}`, MayStop, true},
		{"extra field", `{"action":"reply","message":"yes","permission":"deploy"}`, MayStop, true},
		{"trailing JSON", `{"action":"reply","message":"yes"}{}`, MayStop, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseDecision(test.raw, test.policy)
			if (err != nil) != test.wantError {
				t.Fatalf("ParseDecision error = %v, want error %t", err, test.wantError)
			}
		})
	}
}

func TestNewUsesResolvedProviderConnection(t *testing.T) {
	var seen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer simulator-key" {
			t.Errorf("unexpected simulator request path=%q authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Unrelated-Secret") != "" || r.Header.Get("Openai-Organization") != "" || r.Header.Get("Openai-Project") != "" {
			t.Error("inherited ambient OpenAI headers")
		}
		seen = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"test","object":"chat.completion","created":0,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"{\"action\":\"reply\",\"message\":\"staging\"}"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	t.Setenv("SIMULATION_API_KEY", "simulator-key")
	t.Setenv("SIMULATION_BASE_URL", server.URL+"/v1")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "X-Unrelated-Secret: wrong-secret")
	t.Setenv("OPENAI_ORG_ID", "wrong-org")
	t.Setenv("OPENAI_PROJECT_ID", "wrong-project")
	t.Setenv("OPENAI_API_KEY", "wrong-key")
	t.Setenv("OPENAI_BASE_URL", "https://wrong.example.invalid/v1")
	resolver := credential.NewResolver("")
	sim, err := New(config.UserSimulatorModel{Provider: "simulation", Protocol: "openai", Model: "test-model"}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := sim.Next(context.Background(), "staging only", "answer question", []Exchange{{User: "environment?", Agent: "Which one?"}}, MustReply)
	if err != nil {
		t.Fatal(err)
	}
	if !seen || decision.Message != "staging" {
		t.Fatalf("seen=%t decision=%+v", seen, decision)
	}
	if _, err := New(config.UserSimulatorModel{Provider: "missing", Protocol: "openai", Model: "test-model"}, resolver); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("missing credential error = %v", err)
	}
}

func TestAnthropicConnectionAndDecisions(t *testing.T) {
	tests := []struct {
		name, stop, content string
		policy              ResponsePolicy
		wantError           bool
	}{
		{"reply", "end_turn", `[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"{\"action\":\"reply\","},{"type":"text","text":"\"message\":\"staging\"}"}]`, MustReply, false},
		{"stop", "end_turn", `[{"type":"text","text":"{\"action\":\"stop\",\"reason\":\"done\"}"}]`, MayStop, false},
		{"forbidden stop", "end_turn", `[{"type":"text","text":"{\"action\":\"stop\",\"reason\":\"done\"}"}]`, MustReply, true},
		{"truncated", "max_tokens", `[{"type":"text","text":"{\"action\":\"reply\",\"message\":\"staging\"}"}]`, MustReply, true},
		{"tool use", "end_turn", `[{"type":"tool_use","id":"test","name":"deploy","input":{}}]`, MustReply, true},
		{"empty", "end_turn", `[]`, MustReply, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != "simulator-key" || r.Header.Get("Authorization") != "" || r.Header.Get("Anthropic-Version") != "2023-06-01" {
					t.Errorf("unexpected Anthropic request path or credentials")
				}
				if r.Header.Get("X-Unrelated-Secret") != "" {
					t.Error("inherited ambient Anthropic headers")
				}
				checkAnthropicRequestBody(t, r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"id":"test","type":"message","role":"assistant","model":"test-model","content":%s,"stop_reason":%q,"usage":{"input_tokens":10,"output_tokens":10}}`, tt.content, tt.stop)
			}))
			defer server.Close()
			t.Setenv("SIMULATION_API_KEY", "simulator-key")
			t.Setenv("SIMULATION_BASE_URL", server.URL)
			t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Unrelated-Secret: wrong-secret")
			t.Setenv("ANTHROPIC_API_KEY", "")
			t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
			t.Setenv("ANTHROPIC_BASE_URL", "https://wrong.example.invalid")
			sim, err := New(config.UserSimulatorModel{Provider: "simulation", Protocol: "anthropic", Model: "test-model"}, credential.NewResolver(""))
			if err != nil {
				t.Fatal(err)
			}
			decision, err := sim.Next(context.Background(), "staging only", "answer question", []Exchange{{User: "environment?", Agent: "Which one?"}}, tt.policy)
			if (err != nil) != tt.wantError {
				t.Fatalf("decision=%+v error=%v, wantError=%t", decision, err, tt.wantError)
			}
			if !tt.wantError && tt.policy == MustReply && decision.Message != "staging" {
				t.Fatalf("decision = %+v", decision)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := sim.Next(ctx, "", "", nil, MustReply); err == nil {
				t.Fatal("cancelled context accepted")
			}
		})
	}
}

func checkAnthropicRequestBody(t *testing.T, r *http.Request) {
	t.Helper()
	var body struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Thinking  struct {
			Type string `json:"type"`
		} `json:"thinking"`
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Error(err)
	}
	if body.Model != "test-model" || body.MaxTokens != 1024 || body.Thinking.Type != "disabled" || len(body.System) != 1 || len(body.Messages) != 1 || body.Messages[0].Role != "user" {
		t.Errorf("unexpected Anthropic body: %+v", body)
	}
	if len(body.Messages) == 1 && len(body.Messages[0].Content) == 1 {
		for _, want := range []string{"staging only", "answer question", "Which one?"} {
			if !strings.Contains(body.Messages[0].Content[0].Text, want) {
				t.Errorf("request missing %q", want)
			}
		}
	}
}
