package llmclient_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/php-workx/fabrikk/llmclient"
)

// stubBackend is a minimal Backend implementation used for compile-time and
// interface-assignment tests. It is intentionally unexported and lives only in
// the test binary.
type stubBackend struct{}

func (s *stubBackend) Stream(_ context.Context, _ *llmclient.Context, _ ...llmclient.Option) (<-chan llmclient.Event, error) {
	ch := make(chan llmclient.Event)
	close(ch)
	return ch, nil
}

func (s *stubBackend) Name() string    { return "stub" }
func (s *stubBackend) Available() bool { return false }
func (s *stubBackend) Close() error    { return nil }

// TestBackendInterfaceAssignment verifies at compile time that a concrete type
// can be assigned to llmclient.Backend.
func TestBackendInterfaceAssignment(t *testing.T) {
	var b llmclient.Backend = &stubBackend{}
	if b.Name() != "stub" {
		t.Errorf("expected name %q, got %q", "stub", b.Name())
	}
}

func TestEventJSONShape(t *testing.T) {
	ev := llmclient.Event{
		Type:         llmclient.EventTextDelta,
		ContentIndex: 1,
		Delta:        "hello",
		SessionID:    "sess-1",
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal Event: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	checkKey(t, m, "type", string(llmclient.EventTextDelta))
	checkKey(t, m, "delta", "hello")
	checkKey(t, m, "sessionId", "sess-1")
	// contentIndex 1 is non-zero, so it must be present.
	if _, ok := m["contentIndex"]; !ok {
		t.Error("expected key 'contentIndex' in JSON output")
	}
}

func TestEventDoneJSONShape(t *testing.T) {
	ev := llmclient.Event{
		Type:   llmclient.EventDone,
		Reason: llmclient.StopEndTurn,
		Usage: &llmclient.Usage{
			InputTokens:  100,
			OutputTokens: 200,
		},
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal done Event: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	checkKey(t, m, "type", "done")
	checkKey(t, m, "reason", "end_turn")

	usage, ok := m["usage"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'usage' object in JSON")
	}
	// JSON numbers unmarshal as float64.
	if got := usage["inputTokens"]; got != float64(100) {
		t.Errorf("usage.inputTokens: want 100, got %v", got)
	}
	if got := usage["outputTokens"]; got != float64(200) {
		t.Errorf("usage.outputTokens: want 200, got %v", got)
	}
}

func TestEventErrorJSONShape(t *testing.T) {
	ev := llmclient.Event{
		Type:         llmclient.EventError,
		ErrorMessage: "subprocess exited with code 1",
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal error Event: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	checkKey(t, m, "type", "error")
	checkKey(t, m, "errorMessage", "subprocess exited with code 1")
}

func TestEventStartWithFidelityJSONShape(t *testing.T) {
	ev := llmclient.Event{
		Type:      llmclient.EventStart,
		SessionID: "sess-xyz",
		Fidelity: &llmclient.Fidelity{
			Streaming:   llmclient.StreamingStructured,
			ToolControl: llmclient.ToolControlBuiltIn,
			OptionResults: map[llmclient.OptionName]llmclient.OptionResult{
				llmclient.OptionModel: llmclient.OptionApplied,
			},
		},
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal start Event: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	checkKey(t, m, "type", "start")
	checkKey(t, m, "sessionId", "sess-xyz")

	fid, ok := m["fidelity"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'fidelity' object in JSON")
	}
	checkKey(t, fid, "streaming", "structured")
	checkKey(t, fid, "toolControl", "built_in_cli_tools")

	optRes, ok := fid["optionResults"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'optionResults' in fidelity JSON")
	}
	checkKey(t, optRes, "model", "applied")
}

func TestToolCallJSONShape(t *testing.T) {
	tc := llmclient.ToolCall{
		ID:        "toolu_01ABC",
		Name:      "Read",
		Arguments: map[string]interface{}{"path": "/tmp/foo"},
	}

	data, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("marshal ToolCall: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	checkKey(t, m, "id", "toolu_01ABC")
	checkKey(t, m, "name", "Read")

	args, ok := m["arguments"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'arguments' object in JSON")
	}
	checkKey(t, args, "path", "/tmp/foo")
}

func TestAssistantMessageJSONShape(t *testing.T) {
	msg := llmclient.AssistantMessage{
		ID:         "msg-1",
		Role:       "assistant",
		StopReason: llmclient.StopEndTurn,
		Model:      "claude-opus-4-5",
		Content: []llmclient.ContentBlock{
			{Type: llmclient.ContentText, Text: "Hello!"},
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal AssistantMessage: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	checkKey(t, m, "id", "msg-1")
	checkKey(t, m, "role", "assistant")
	checkKey(t, m, "stopReason", "end_turn")
	checkKey(t, m, "model", "claude-opus-4-5")
}

func TestUsageOmitsZeroOptionalFields(t *testing.T) {
	u := llmclient.Usage{
		InputTokens:  10,
		OutputTokens: 5,
	}

	data, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal Usage: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	if _, ok := m["cacheReadTokens"]; ok {
		t.Error("cacheReadTokens should be omitted when zero")
	}
	if _, ok := m["cacheWriteTokens"]; ok {
		t.Error("cacheWriteTokens should be omitted when zero")
	}
}

func TestContextJSONRoundTrip(t *testing.T) {
	ctx := llmclient.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "Hello!"},
				},
			},
		},
		Tools: []llmclient.Tool{
			{
				Name:        "Bash",
				Description: "Run a shell command",
				InputSchema: map[string]interface{}{"type": "object"},
			},
		},
	}

	data, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("marshal Context: %v", err)
	}

	var decoded llmclient.Context
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal Context: %v", err)
	}

	if decoded.SystemPrompt != ctx.SystemPrompt {
		t.Errorf("SystemPrompt: want %q, got %q", ctx.SystemPrompt, decoded.SystemPrompt)
	}
	if len(decoded.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(decoded.Messages))
	}
	if decoded.Messages[0].Role != llmclient.RoleUser {
		t.Errorf("message role: want %q, got %q", llmclient.RoleUser, decoded.Messages[0].Role)
	}
	if len(decoded.Tools) != 1 || decoded.Tools[0].Name != "Bash" {
		t.Errorf("tools mismatch after round-trip")
	}
}

func TestMessageRoleConstants(t *testing.T) {
	tests := []struct {
		role llmclient.MessageRole
		json string
	}{
		{llmclient.RoleUser, "user"},
		{llmclient.RoleAssistant, "assistant"},
		{llmclient.RoleToolResult, "toolResult"},
		{llmclient.RoleDeveloper, "developer"},
	}

	for _, tt := range tests {
		if string(tt.role) != tt.json {
			t.Errorf("MessageRole %q: expected JSON value %q, got %q", tt.role, tt.json, string(tt.role))
		}
	}
}

func TestEventTypeConstants(t *testing.T) {
	tests := []struct {
		ev   llmclient.EventType
		want string
	}{
		{llmclient.EventStart, "start"},
		{llmclient.EventTextStart, "text_start"},
		{llmclient.EventTextDelta, "text_delta"},
		{llmclient.EventTextEnd, "text_end"},
		{llmclient.EventThinkingStart, "thinking_start"},
		{llmclient.EventThinkingDelta, "thinking_delta"},
		{llmclient.EventThinkingEnd, "thinking_end"},
		{llmclient.EventToolCallStart, "toolcall_start"},
		{llmclient.EventToolCallDelta, "toolcall_delta"},
		{llmclient.EventToolCallEnd, "toolcall_end"},
		{llmclient.EventDone, "done"},
		{llmclient.EventError, "error"},
	}

	for _, tt := range tests {
		if string(tt.ev) != tt.want {
			t.Errorf("EventType %q: expected %q, got %q", tt.ev, tt.want, string(tt.ev))
		}
	}
}

func TestStopReasonConstants(t *testing.T) {
	tests := []struct {
		sr   llmclient.StopReason
		want string
	}{
		{llmclient.StopEndTurn, "end_turn"},
		{llmclient.StopMaxTokens, "max_tokens"},
		{llmclient.StopToolUse, "tool_use"},
		{llmclient.StopCancelled, "cancelled"},
	}

	for _, tt := range tests {
		if string(tt.sr) != tt.want {
			t.Errorf("StopReason %q: expected %q, got %q", tt.sr, tt.want, string(tt.sr))
		}
	}
}

// checkKey asserts that m[key] == want, printing a test failure otherwise.
func checkKey(t *testing.T, m map[string]interface{}, key, want string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("expected key %q in JSON object, not found", key)
		return
	}
	if got, ok := v.(string); !ok || got != want {
		t.Errorf("key %q: want %q, got %v", key, want, v)
	}
}
