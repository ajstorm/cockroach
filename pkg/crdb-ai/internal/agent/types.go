package agent

// ConversationMessage represents a single message in conversation history
type ConversationMessage struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"`
}

// AskRequest represents a user's question to the AI assistant
type AskRequest struct {
	Prompt  string                `json:"prompt"`
	Context map[string]string     `json:"context,omitempty"` // Optional hints like database, table
	History []ConversationMessage `json:"history,omitempty"` // Conversation history (oldest to newest, excluding current prompt)
}

// AskResponse represents the AI assistant's answer
type AskResponse struct {
	Answer          string   `json:"answer"`
	ToolsUsed       []string `json:"tools_used"`
	Recommendations []string `json:"recommendations,omitempty"`
	Confidence      string   `json:"confidence"` // high/medium/low
	TokensUsed      int      `json:"tokens_used"`
}

// StreamEventType represents the type of streaming event
type StreamEventType string

const (
	StreamEventTypeToken     StreamEventType = "token"
	StreamEventTypeToolCall  StreamEventType = "tool_call"
	StreamEventTypeThinking  StreamEventType = "thinking"   // AI is thinking (before/during OpenAI call)
	StreamEventTypeAnalyzing StreamEventType = "analyzing"  // AI is analyzing results after tools
	StreamEventTypeProgress  StreamEventType = "progress"   // Tool execution progress message
	StreamEventTypeDone      StreamEventType = "done"
	StreamEventTypeError     StreamEventType = "error"
)

// StreamEvent represents a single event in the streaming response
type StreamEvent struct {
	Type            StreamEventType `json:"type"`
	Content         string          `json:"content,omitempty"`
	ToolName        string          `json:"tool_name,omitempty"`
	ToolDescription string          `json:"tool_description,omitempty"`
	ToolArguments   string          `json:"tool_arguments,omitempty"`
	Error           error           `json:"error,omitempty"`
}

// StreamCallback is a function called for each streaming event
type StreamCallback func(event StreamEvent) error
