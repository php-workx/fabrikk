package llmclient_test

import (
	"context"
	"errors"
	"testing"

	"github.com/php-workx/fabrikk/llmclient"
)

type jsonCollectBackend struct {
	responses []string
	inputs    []*llmclient.Context
}

func (b *jsonCollectBackend) Stream(_ context.Context, input *llmclient.Context, _ ...llmclient.Option) (<-chan llmclient.Event, error) {
	b.inputs = append(b.inputs, input)
	ch := make(chan llmclient.Event, 4)
	idx := len(b.inputs) - 1
	if idx < len(b.responses) {
		ch <- llmclient.Event{Type: llmclient.EventTextDelta, Delta: b.responses[idx]}
	}
	ch <- llmclient.Event{Type: llmclient.EventDone, Reason: llmclient.StopEndTurn}
	close(ch)
	return ch, nil
}

func (b *jsonCollectBackend) Name() string    { return "json-collect" }
func (b *jsonCollectBackend) Available() bool { return true }
func (b *jsonCollectBackend) Ready(context.Context) llmclient.ReadyReport {
	return llmclient.ReadyReport{State: llmclient.ReadyOK}
}
func (b *jsonCollectBackend) Close() error { return nil }

func TestCollectJSONParsesCodeFencedJSONWithoutRetry(t *testing.T) {
	backend := &jsonCollectBackend{responses: []string{"```json\n{\"ok\":true}\n```"}}
	var out struct {
		OK bool `json:"ok"`
	}

	raw, err := llmclient.CollectJSON(context.Background(), backend, &llmclient.Context{}, &out)
	if err != nil {
		t.Fatalf("CollectJSON returned error: %v", err)
	}
	if raw != "```json\n{\"ok\":true}\n```" {
		t.Errorf("raw = %q", raw)
	}
	if !out.OK {
		t.Fatal("out.OK = false, want true")
	}
	if len(backend.inputs) != 1 {
		t.Fatalf("Stream calls = %d, want 1", len(backend.inputs))
	}
}

func TestCollectJSONRetriesOnceWithAssistantRawAndDeveloperRepair(t *testing.T) {
	base := &llmclient.Context{
		SystemPrompt: "return JSON",
		Messages: []llmclient.Message{{
			Role:    llmclient.RoleUser,
			Content: []llmclient.ContentBlock{{Type: llmclient.ContentText, Text: "go"}},
		}},
		Metadata: map[string]string{"cwd": "/tmp/repo"},
	}
	backend := &jsonCollectBackend{responses: []string{"not json", "{\"ok\":true}"}}
	var out struct {
		OK bool `json:"ok"`
	}

	raw, err := llmclient.CollectJSON(context.Background(), backend, base, &out)
	if err != nil {
		t.Fatalf("CollectJSON returned error: %v", err)
	}
	if raw != "{\"ok\":true}" {
		t.Errorf("raw = %q, want second response", raw)
	}
	if !out.OK {
		t.Fatal("out.OK = false, want true")
	}
	if len(backend.inputs) != 2 {
		t.Fatalf("Stream calls = %d, want 2", len(backend.inputs))
	}
	retry := backend.inputs[1]
	if got := len(retry.Messages); got != 3 {
		t.Fatalf("retry message count = %d, want 3", got)
	}
	if retry.Messages[1].Role != llmclient.RoleAssistant || retry.Messages[1].Content[0].Text != "not json" {
		t.Fatalf("retry assistant message = %#v", retry.Messages[1])
	}
	if retry.Messages[2].Role != llmclient.RoleDeveloper {
		t.Fatalf("retry repair role = %q, want developer", retry.Messages[2].Role)
	}
	if base.Messages[len(base.Messages)-1].Role != llmclient.RoleUser {
		t.Fatal("CollectJSON mutated base messages")
	}
	if retry.Metadata["cwd"] != "/tmp/repo" {
		t.Fatalf("retry metadata = %v", retry.Metadata)
	}
}

func TestCollectJSONParsesEmbeddedJSONObjectWithoutRetry(t *testing.T) {
	backend := &jsonCollectBackend{responses: []string{"Sure:\n\n{\"ok\":true}\n\nDone."}}
	var out struct {
		OK bool `json:"ok"`
	}

	raw, err := llmclient.CollectJSON(context.Background(), backend, &llmclient.Context{}, &out)
	if err != nil {
		t.Fatalf("CollectJSON returned error: %v", err)
	}
	if raw != "Sure:\n\n{\"ok\":true}\n\nDone." {
		t.Errorf("raw = %q", raw)
	}
	if !out.OK {
		t.Fatal("out.OK = false, want true")
	}
	if len(backend.inputs) != 1 {
		t.Fatalf("Stream calls = %d, want 1", len(backend.inputs))
	}
}

func TestCollectJSONParsesEmbeddedJSONArrayWithoutRetry(t *testing.T) {
	backend := &jsonCollectBackend{responses: []string{"commands:\n[{\"cmd\":\"ls\"}]\n"}}
	var out []struct {
		Cmd string `json:"cmd"`
	}

	_, err := llmclient.CollectJSON(context.Background(), backend, &llmclient.Context{}, &out)
	if err != nil {
		t.Fatalf("CollectJSON returned error: %v", err)
	}
	if len(out) != 1 || out[0].Cmd != "ls" {
		t.Fatalf("out = %#v", out)
	}
	if len(backend.inputs) != 1 {
		t.Fatalf("Stream calls = %d, want 1", len(backend.inputs))
	}
}

func TestCollectJSONEmbeddedScannerIgnoresBracesInStrings(t *testing.T) {
	backend := &jsonCollectBackend{responses: []string{"prefix {not json} {\"text\":\"brace } inside\"} suffix"}}
	var out struct {
		Text string `json:"text"`
	}

	_, err := llmclient.CollectJSON(context.Background(), backend, &llmclient.Context{}, &out)
	if err != nil {
		t.Fatalf("CollectJSON returned error: %v", err)
	}
	if out.Text != "brace } inside" {
		t.Fatalf("out.Text = %q", out.Text)
	}
	if len(backend.inputs) != 1 {
		t.Fatalf("Stream calls = %d, want 1", len(backend.inputs))
	}
}

func TestCollectJSONReturnsBadRequestAfterSecondParseFailure(t *testing.T) {
	backend := &jsonCollectBackend{responses: []string{"not json", "still not json"}}
	var out map[string]interface{}

	raw, err := llmclient.CollectJSON(context.Background(), backend, &llmclient.Context{}, &out)
	if raw != "still not json" {
		t.Errorf("raw = %q, want second response", raw)
	}
	var eventErr *llmclient.EventErrorError
	if !errors.As(err, &eventErr) {
		t.Fatalf("error type = %T, want *EventErrorError", err)
	}
	if eventErr.Type != llmclient.ErrTypeBadRequest {
		t.Fatalf("error type = %q, want %q", eventErr.Type, llmclient.ErrTypeBadRequest)
	}
	if len(backend.inputs) != 2 {
		t.Fatalf("Stream calls = %d, want 2", len(backend.inputs))
	}
}
