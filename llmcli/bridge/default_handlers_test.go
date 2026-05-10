package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/llmclient"
)

const okJSON = `{"ok":true}`

type scriptedBackend struct {
	events []llmclient.Event
	ready  llmclient.ReadyReport
}

func (b scriptedBackend) Stream(context.Context, *llmclient.Context, ...llmclient.Option) (<-chan llmclient.Event, error) {
	ch := make(chan llmclient.Event, len(b.events))
	for i := range b.events {
		ch <- b.events[i]
	}
	close(ch)
	return ch, nil
}

func (scriptedBackend) Name() string { return "scripted" }
func (b scriptedBackend) Available() bool {
	return b.ready.State == llmclient.ReadyOK
}
func (b scriptedBackend) Ready(context.Context) llmclient.ReadyReport { return b.ready }
func (scriptedBackend) Close() error                                  { return nil }

func TestRegisterBuiltinHandlersReadyOp(t *testing.T) {
	resetRegistryForTest()
	RegisterBuiltinHandlers()

	in := strings.NewReader(`{"request_id":"r1","op":"ready"}` + "\n")
	var out bytes.Buffer
	backend := scriptedBackend{ready: llmclient.ReadyReport{State: llmclient.ReadyOK, Detail: "usable"}}

	if err := Run(context.Background(), Config{In: in, Out: &out, Backend: backend, DisableLockfile: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var result struct {
		Backend string `json:"backend"`
		State   string `json:"state"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Backend != "scripted" || result.State != "ok" || result.Detail != "usable" {
		t.Fatalf("ready result = %#v", result)
	}
}

func TestRegisterBuiltinHandlersTextOp(t *testing.T) {
	resetRegistryForTest()
	RegisterBuiltinHandlers()

	in := strings.NewReader(`{"request_id":"r1","op":"text","context":{"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}}` + "\n")
	var out bytes.Buffer
	backend := scriptedBackend{events: []llmclient.Event{
		{Type: llmclient.EventTextDelta, Delta: "hi"},
		{Type: llmclient.EventTextDelta, Delta: " there"},
		{Type: llmclient.EventDone, Reason: llmclient.StopEndTurn},
	}}

	if err := Run(context.Background(), Config{In: in, Out: &out, Backend: backend, DisableLockfile: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var result struct {
		Text   string `json:"text"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Text != "hi there" || result.Reason != string(llmclient.StopEndTurn) {
		t.Fatalf("text result = %#v", result)
	}
}

func TestRegisterBuiltinHandlersJSONOp(t *testing.T) {
	resetRegistryForTest()
	RegisterBuiltinHandlers()

	in := strings.NewReader(`{"request_id":"r1","op":"json","context":{"messages":[{"role":"user","content":[{"type":"text","text":"json"}]}]}}` + "\n")
	var out bytes.Buffer
	backend := scriptedBackend{events: []llmclient.Event{
		{Type: llmclient.EventTextDelta, Delta: okJSON},
		{Type: llmclient.EventDone, Reason: llmclient.StopEndTurn},
	}}

	if err := Run(context.Background(), Config{In: in, Out: &out, Backend: backend, DisableLockfile: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var result struct {
		Raw   string          `json:"raw"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Raw != okJSON || string(result.Value) != okJSON {
		t.Fatalf("json result = raw %q value %s", result.Raw, result.Value)
	}
}

func TestRegisterBuiltinHandlersStructuredOp(t *testing.T) {
	resetRegistryForTest()
	RegisterBuiltinHandlers()

	in := strings.NewReader(`{"request_id":"r1","op":"structured","context":{"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}}` + "\n")
	var out bytes.Buffer
	backend := scriptedBackend{events: []llmclient.Event{
		{Type: llmclient.EventStart, SessionID: "sess-123"},
		{Type: llmclient.EventTextDelta, Delta: "hi", ContentIndex: 0},
		{
			Type: llmclient.EventDone,
			Message: &llmclient.AssistantMessage{
				Role: "assistant",
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "hi"},
				},
				Cost: &llmclient.Cost{TotalUSD: 0.005},
			},
			Usage:  &llmclient.Usage{InputTokens: 10, OutputTokens: 2},
			Reason: llmclient.StopEndTurn,
		},
	}}

	if err := Run(context.Background(), Config{In: in, Out: &out, Backend: backend, DisableLockfile: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	var result struct {
		Message   llmclient.AssistantMessage `json:"message"`
		Usage     *llmclient.Usage           `json:"usage,omitempty"`
		Cost      *llmclient.Cost            `json:"cost,omitempty"`
		SessionID string                     `json:"session_id,omitempty"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.SessionID != "sess-123" {
		t.Errorf("SessionID = %q, want %q", result.SessionID, "sess-123")
	}
	if result.Message.Role != "assistant" {
		t.Errorf("Message.Role = %q, want assistant", result.Message.Role)
	}
	if result.Cost == nil || result.Cost.TotalUSD != 0.005 {
		t.Errorf("Cost = %+v, want TotalUSD=0.005", result.Cost)
	}
	if result.Usage == nil || result.Usage.InputTokens != 10 {
		t.Errorf("Usage = %+v, want InputTokens=10", result.Usage)
	}
}
