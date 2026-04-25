package llmclient

// Context is the input to a single streaming request. It carries the system
// prompt, conversation history, and any tool definitions the model may call.
type Context struct {
	SystemPrompt string    `json:"systemPrompt,omitempty"`
	Messages     []Message `json:"messages"`
	Tools        []Tool    `json:"tools,omitempty"`
}

// Message is a single turn in the conversation history.
type Message struct {
	Role    MessageRole    `json:"role"`
	Content []ContentBlock `json:"content"`

	// ToolResult-specific fields (populated when Role == RoleToolResult).
	ToolCallID string `json:"toolCallId,omitempty"`
	IsError    bool   `json:"isError,omitempty"`
}

// MessageRole discriminates the participant sending a message.
type MessageRole string

// Message role constants.
const (
	RoleUser       MessageRole = "user"
	RoleAssistant  MessageRole = "assistant"
	RoleToolResult MessageRole = "toolResult"
	RoleDeveloper  MessageRole = "developer"
)

// ContentBlock is a discriminated union of text, thinking, or tool-use content.
type ContentBlock struct {
	Type ContentType `json:"type"` // "text" | "thinking" | "tool_use"

	// Text content (type "text" or "thinking").
	Text string `json:"text,omitempty"`

	// Tool-use fields (type "tool_use").
	ToolCallID string                 `json:"toolCallId,omitempty"`
	ToolName   string                 `json:"toolName,omitempty"`
	Arguments  map[string]interface{} `json:"arguments,omitempty"`
}

// ContentType identifies the kind of content in a ContentBlock.
type ContentType string

// Content type constants.
const (
	ContentText     ContentType = "text"
	ContentThinking ContentType = "thinking"
	ContentToolUse  ContentType = "tool_use"
)

// Tool is a function definition that the model may invoke during a turn.
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema,omitempty"`
}

// EventType is the discriminant field on every Event.
type EventType string

// Event type constants — every emitted event carries exactly one of these.
const (
	EventStart         EventType = "start"          // session initialized; carries Fidelity
	EventTextStart     EventType = "text_start"     // text content block begins
	EventTextDelta     EventType = "text_delta"     // incremental text chunk
	EventTextEnd       EventType = "text_end"       // text content block ends
	EventThinkingStart EventType = "thinking_start" // thinking block begins
	EventThinkingDelta EventType = "thinking_delta" // incremental thinking chunk
	EventThinkingEnd   EventType = "thinking_end"   // thinking block ends
	EventToolCallStart EventType = "toolcall_start" // tool call begins (name + partial args)
	EventToolCallDelta EventType = "toolcall_delta" // incremental tool call arguments
	EventToolCallEnd   EventType = "toolcall_end"   // tool call complete (full args)
	EventDone          EventType = "done"           // turn complete
	EventError         EventType = "error"          // unrecoverable error
)

// StopReason indicates why a model turn ended.
type StopReason string

// Stop reason constants.
const (
	StopEndTurn   StopReason = "end_turn"   // model finished normally
	StopMaxTokens StopReason = "max_tokens" // hit token limit
	StopToolUse   StopReason = "tool_use"   // model wants to call a tool
	StopCancelled StopReason = "cancelled"  // request was cancelled
)

// Event is the unified event type emitted by all backends (both llmclient and
// llmcli). It is a discriminated union on the Type field.
type Event struct {
	// Type identifies the kind of event. Required on all events.
	Type EventType `json:"type"`

	// ContentIndex tracks which content block the event belongs to. Used to
	// correlate text_start/delta/end and toolcall_start/delta/end events.
	ContentIndex int `json:"contentIndex,omitempty"`

	// Delta carries the incremental content for text_delta, thinking_delta,
	// and toolcall_delta events.
	Delta string `json:"delta,omitempty"`

	// Content carries the full text for text_end or thinking_end events.
	Content string `json:"content,omitempty"`

	// ToolCall carries the tool call for toolcall_start and toolcall_end events.
	ToolCall *ToolCall `json:"toolCall,omitempty"`

	// Partial is the partial assistant message emitted during the turn (when
	// the backend supports streaming partial messages).
	Partial *AssistantMessage `json:"partial,omitempty"`

	// Message is the complete assistant message on the done event.
	Message *AssistantMessage `json:"message,omitempty"`

	// ErrorMessage describes the error on error events.
	ErrorMessage string `json:"errorMessage,omitempty"`

	// ErrorType is a stable coarse classification for error events.
	ErrorType string `json:"errorType,omitempty"`

	// Reason is the stop reason on done events.
	Reason StopReason `json:"reason,omitempty"`

	// Usage carries token usage on done events (when the backend reports it).
	Usage *Usage `json:"usage,omitempty"`

	// SessionID is set on the start event. Some backends (Claude) return a
	// session ID that can be passed to WithSession for a follow-up turn.
	SessionID string `json:"sessionId,omitempty"`

	// Fidelity is set on the start event. It tells the caller which
	// capabilities are active for this stream and how each requested option
	// was handled.
	Fidelity *Fidelity `json:"fidelity,omitempty"`
}

// ToolCall represents a model's request to invoke a tool.
type ToolCall struct {
	ID        string                 `json:"id"`        // unique tool call ID, e.g. "toolu_01ABC"
	Name      string                 `json:"name"`      // tool name, e.g. "Read", "Bash"
	Arguments map[string]interface{} `json:"arguments"` // parsed arguments
}

// AssistantMessage is the complete response from a model turn.
// It is carried by the done event in Event.Message and by partial events in
// Event.Partial.
type AssistantMessage struct {
	ID         string         `json:"id"`
	Role       string         `json:"role"` // always "assistant"
	Content    []ContentBlock `json:"content"`
	Model      string         `json:"model,omitempty"`
	StopReason StopReason     `json:"stopReason,omitempty"`
	Usage      *Usage         `json:"usage,omitempty"`
}

// Usage carries token usage metadata when it is available from the backend.
type Usage struct {
	InputTokens      int `json:"inputTokens"`
	OutputTokens     int `json:"outputTokens"`
	CacheReadTokens  int `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int `json:"cacheWriteTokens,omitempty"`
}

// Cost carries the cost in USD for a turn, when the backend reports it (e.g.
// Claude's total_cost_usd field on the result event).
type Cost struct {
	TotalUSD float64 `json:"totalUsd"`
}

// OllamaConfig controls routing through a local or cloud Ollama instance.
// Set via WithOllama. When present, the backend injects the appropriate
// environment variables before spawning the subprocess.
type OllamaConfig struct {
	// BaseURL defaults to "http://localhost:11434" for local, or
	// "https://ollama.com" for cloud. Leave empty for the default.
	BaseURL string `json:"baseUrl,omitempty"`

	// Model is the Ollama model name, e.g. "glm-5:cloud", "qwen3.5".
	// Cloud models use the ":cloud" suffix.
	Model string `json:"model,omitempty"`

	// APIKey is required for Ollama Cloud; empty for local instances.
	APIKey string `json:"apiKey,omitempty"`
}
