package transcript

import "testing"

func TestTranscriptQueries(t *testing.T) {
	t.Parallel()

	tr := Transcript{
		{Role: RoleUser, Content: "first prompt", Turn: 1},
		{Role: RoleToolCall, Content: "call one", Turn: 1, ToolCall: &ToolCallInfo{Name: "read_file"}},
		{Role: RoleAssistant, Content: "draft", Turn: 1},
		{Role: RoleToolResult, Content: "result", Turn: 1, ToolResult: &ToolResultInfo{Status: "ok"}},
		{Role: RoleUser, Content: "follow up", Turn: 2},
		{Role: RoleAssistant, Content: "final", Turn: 2},
	}

	if got := tr.FinalAssistantMessage(); got != "final" {
		t.Fatalf("FinalAssistantMessage() = %q, want final", got)
	}

	calls := tr.ToolCalls()
	if len(calls) != 1 || calls[0].ToolCall == nil || calls[0].ToolCall.Name != "read_file" {
		t.Fatalf("ToolCalls() = %#v, want read_file call", calls)
	}

	turnOne := tr.MessagesForTurn(1)
	if len(turnOne) != 4 {
		t.Fatalf("MessagesForTurn(1) returned %d message(s), want 4", len(turnOne))
	}

	assistantTurnOne := tr.AssistantMessagesForTurn(1)
	if len(assistantTurnOne) != 1 || assistantTurnOne[0].Content != "draft" {
		t.Fatalf("AssistantMessagesForTurn(1) = %#v, want draft", assistantTurnOne)
	}
}

func TestTranscriptQueriesReturnEmptyWhenAbsent(t *testing.T) {
	t.Parallel()

	tr := Transcript{{Role: RoleUser, Content: "prompt", Turn: 1}}
	if got := tr.FinalAssistantMessage(); got != "" {
		t.Fatalf("FinalAssistantMessage() = %q, want empty", got)
	}
	if got := tr.ToolCalls(); got != nil {
		t.Fatalf("ToolCalls() = %#v, want nil", got)
	}
	if got := tr.MessagesForTurn(99); got != nil {
		t.Fatalf("MessagesForTurn(99) = %#v, want nil", got)
	}
	if got := tr.AssistantMessagesForTurn(1); got != nil {
		t.Fatalf("AssistantMessagesForTurn(1) = %#v, want nil", got)
	}
}
