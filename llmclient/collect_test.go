package llmclient_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/llmclient"
)

func TestCollectConcatenatesTextDeltasUntilDone(t *testing.T) {
	ch := make(chan llmclient.Event, 4)
	ch <- llmclient.Event{Type: llmclient.EventTextDelta, Delta: "hello "}
	ch <- llmclient.Event{Type: llmclient.EventThinkingDelta, Delta: "ignored"}
	ch <- llmclient.Event{Type: llmclient.EventTextDelta, Delta: "world"}
	ch <- llmclient.Event{Type: llmclient.EventDone, Reason: llmclient.StopEndTurn}
	close(ch)

	text, reason, err := llmclient.Collect(context.Background(), ch)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if text != "hello world" {
		t.Errorf("text = %q, want %q", text, "hello world")
	}
	if reason != llmclient.StopEndTurn {
		t.Errorf("reason = %q, want %q", reason, llmclient.StopEndTurn)
	}
}

func TestCollectUsesTextEndContentWhenNoDeltasWereSeen(t *testing.T) {
	ch := make(chan llmclient.Event, 2)
	ch <- llmclient.Event{Type: llmclient.EventTextEnd, Content: "complete text"}
	ch <- llmclient.Event{Type: llmclient.EventDone, Reason: llmclient.StopMaxTokens}
	close(ch)

	text, reason, err := llmclient.Collect(context.Background(), ch)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if text != "complete text" {
		t.Errorf("text = %q, want %q", text, "complete text")
	}
	if reason != llmclient.StopMaxTokens {
		t.Errorf("reason = %q, want %q", reason, llmclient.StopMaxTokens)
	}
}

func TestCollectReturnsTypedEventError(t *testing.T) {
	ch := make(chan llmclient.Event, 1)
	ch <- llmclient.Event{
		Type:         llmclient.EventError,
		ErrorType:    llmclient.ErrTypeRateLimit,
		ErrorMessage: "quota exhausted",
	}
	close(ch)

	_, _, err := llmclient.Collect(context.Background(), ch)
	if err == nil {
		t.Fatal("Collect returned nil error")
	}
	var eventErr *llmclient.EventErrorError
	if !errors.As(err, &eventErr) {
		t.Fatalf("Collect error type = %T, want *EventErrorError", err)
	}
	if eventErr.Type != llmclient.ErrTypeRateLimit {
		t.Errorf("eventErr.Type = %q, want %q", eventErr.Type, llmclient.ErrTypeRateLimit)
	}
	if !strings.Contains(err.Error(), "quota exhausted") {
		t.Errorf("err.Error() = %q, want message substring", err.Error())
	}
}

func TestCollectReturnsContextErrorWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := make(chan llmclient.Event)
	text, reason, err := llmclient.Collect(ctx, ch)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect error = %v, want context.Canceled", err)
	}
	if text != "" {
		t.Errorf("text = %q, want empty", text)
	}
	if reason != "" {
		t.Errorf("reason = %q, want empty", reason)
	}
}

func TestCollectStructuredReturnsFullMessage(t *testing.T) {
	ch := make(chan llmclient.Event, 7)
	ch <- llmclient.Event{Type: llmclient.EventStart, SessionID: "sess-abc"}
	ch <- llmclient.Event{Type: llmclient.EventTextStart, ContentIndex: 0}
	ch <- llmclient.Event{Type: llmclient.EventTextDelta, Delta: "hello", ContentIndex: 0}
	ch <- llmclient.Event{Type: llmclient.EventTextEnd, Content: "hello", ContentIndex: 0}
	ch <- llmclient.Event{
		Type: llmclient.EventDone,
		Message: &llmclient.AssistantMessage{
			Role: "assistant",
			Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "hello"},
			},
		},
		Usage:  &llmclient.Usage{InputTokens: 100, OutputTokens: 50},
		Reason: llmclient.StopEndTurn,
	}
	close(ch)

	result, err := llmclient.CollectStructured(context.Background(), ch)
	if err != nil {
		t.Fatalf("CollectStructured returned error: %v", err)
	}
	if result.SessionID != "sess-abc" {
		t.Errorf("SessionID = %q, want %q", result.SessionID, "sess-abc")
	}
	if result.Message.Role != "assistant" {
		t.Errorf("Message.Role = %q, want %q", result.Message.Role, "assistant")
	}
	if len(result.Message.Content) != 1 {
		t.Fatalf("len(Message.Content) = %d, want 1", len(result.Message.Content))
	}
	if result.Message.Content[0].Text != "hello" {
		t.Errorf("Message.Content[0].Text = %q, want %q", result.Message.Content[0].Text, "hello")
	}
	if result.Usage == nil || result.Usage.InputTokens != 100 || result.Usage.OutputTokens != 50 {
		t.Errorf("Usage = %+v, want InputTokens=100, OutputTokens=50", result.Usage)
	}
}

func TestCollectStructuredIncludesToolCallsAndThinking(t *testing.T) {
	ch := make(chan llmclient.Event, 5)
	ch <- llmclient.Event{Type: llmclient.EventStart}
	ch <- llmclient.Event{Type: llmclient.EventThinkingDelta, Delta: "Let me think...", ContentIndex: 0}
	ch <- llmclient.Event{Type: llmclient.EventToolCallStart, ContentIndex: 1}
	ch <- llmclient.Event{Type: llmclient.EventToolCallDelta, Delta: `{"cmd":"ls"}`, ContentIndex: 1}
	ch <- llmclient.Event{
		Type: llmclient.EventDone,
		Message: &llmclient.AssistantMessage{
			Role: "assistant",
			Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentThinking, Text: "Let me think..."},
				{Type: llmclient.ContentToolUse, ToolCallID: "toolu_01", ToolName: "bash", Arguments: map[string]interface{}{"cmd": "ls"}},
			},
		},
		Reason: llmclient.StopToolUse,
	}
	close(ch)

	result, err := llmclient.CollectStructured(context.Background(), ch)
	if err != nil {
		t.Fatalf("CollectStructured returned error: %v", err)
	}
	if len(result.Message.Content) != 2 {
		t.Fatalf("len(Message.Content) = %d, want 2", len(result.Message.Content))
	}
	if result.Message.Content[0].Type != llmclient.ContentThinking {
		t.Errorf("Content[0].Type = %q, want %q", result.Message.Content[0].Type, llmclient.ContentThinking)
	}
	if result.Message.Content[1].Type != llmclient.ContentToolUse {
		t.Errorf("Content[1].Type = %q, want %q", result.Message.Content[1].Type, llmclient.ContentToolUse)
	}
}

func TestCollectStructuredReturnsCostWhenPresent(t *testing.T) {
	ch := make(chan llmclient.Event, 3)
	ch <- llmclient.Event{Type: llmclient.EventStart}
	ch <- llmclient.Event{Type: llmclient.EventTextDelta, Delta: "x", ContentIndex: 0}
	ch <- llmclient.Event{
		Type: llmclient.EventDone,
		Message: &llmclient.AssistantMessage{
			Role: "assistant",
			Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "x"},
			},
			Cost: &llmclient.Cost{TotalUSD: 0.003},
		},
		Reason: llmclient.StopEndTurn,
	}
	close(ch)

	result, err := llmclient.CollectStructured(context.Background(), ch)
	if err != nil {
		t.Fatalf("CollectStructured returned error: %v", err)
	}
	if result.Cost == nil || result.Cost.TotalUSD != 0.003 {
		t.Errorf("Cost = %+v, want TotalUSD=0.003", result.Cost)
	}
}

func TestCollectStructuredReturnsTypedError(t *testing.T) {
	ch := make(chan llmclient.Event, 1)
	ch <- llmclient.Event{
		Type:         llmclient.EventError,
		ErrorType:    llmclient.ErrTypeAuth,
		ErrorMessage: "invalid API key",
	}
	close(ch)

	_, err := llmclient.CollectStructured(context.Background(), ch)
	if err == nil {
		t.Fatal("CollectStructured returned nil error")
	}
	var eventErr *llmclient.EventErrorError
	if !errors.As(err, &eventErr) {
		t.Fatalf("CollectStructured error type = %T, want *EventErrorError", err)
	}
	if eventErr.Type != llmclient.ErrTypeAuth {
		t.Errorf("eventErr.Type = %q, want %q", eventErr.Type, llmclient.ErrTypeAuth)
	}
}

func TestCollectStructuredReturnsContextErrorWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := make(chan llmclient.Event)
	result, err := llmclient.CollectStructured(ctx, ch)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CollectStructured error = %v, want context.Canceled", err)
	}
	if result != nil {
		t.Errorf("result = %+v, want nil", result)
	}
}
