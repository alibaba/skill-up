package usersimulator

import (
	"context"
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
		seen = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"test","object":"chat.completion","created":0,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"{\"action\":\"reply\",\"message\":\"staging\"}"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	t.Setenv("SIMULATION_API_KEY", "simulator-key")
	t.Setenv("SIMULATION_BASE_URL", server.URL+"/v1")
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
