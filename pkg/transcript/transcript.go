package transcript

import "time"

// Role represents the role of a message sender in a transcript.
type Role string

// Role constants for message roles.
const (
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleToolCall   Role = "tool_call"
	RoleToolResult Role = "tool_result"
	RoleError      Role = "error"
)

// Message represents a single message in a transcript.
type Message struct {
	Role       Role            `json:"role"`
	Content    string          `json:"content,omitempty"`
	Turn       int             `json:"turn,omitempty"`
	ToolCall   *ToolCallInfo   `json:"tool_call,omitempty"`
	ToolResult *ToolResultInfo `json:"tool_result,omitempty"`
	Timestamp  *time.Time      `json:"timestamp,omitempty"`
}

// ToolCallInfo contains information about a tool call.
type ToolCallInfo struct {
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ToolResultInfo contains information about a tool result.
type ToolResultInfo struct {
	CallID     string `json:"call_id,omitempty"`
	Status     string `json:"status,omitempty"`
	Content    any    `json:"content,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

// Transcript is a sequence of messages representing a conversation.
type Transcript []Message

// FinalAssistantMessage returns the last assistant message content.
func (t Transcript) FinalAssistantMessage() string {
	for i := len(t) - 1; i >= 0; i-- {
		if t[i].Role == RoleAssistant {
			return t[i].Content
		}
	}

	return ""
}

// ToolCalls returns all tool call messages in the transcript.
func (t Transcript) ToolCalls() []Message {
	var calls []Message
	for _, m := range t {
		if m.Role == RoleToolCall {
			calls = append(calls, m)
		}
	}

	return calls
}

// MessagesForTurn returns all messages for a specific turn.
func (t Transcript) MessagesForTurn(turn int) []Message {
	var msgs []Message
	for _, m := range t {
		if m.Turn == turn {
			msgs = append(msgs, m)
		}
	}

	return msgs
}

// AssistantMessagesForTurn returns assistant messages for a specific turn.
func (t Transcript) AssistantMessagesForTurn(turn int) []Message {
	var msgs []Message
	for _, m := range t {
		if m.Role == RoleAssistant && m.Turn == turn {
			msgs = append(msgs, m)
		}
	}

	return msgs
}
