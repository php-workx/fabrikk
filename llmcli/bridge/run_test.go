package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/llmclient"
)

type fakeBackend struct{}

func (fakeBackend) Stream(context.Context, *llmclient.Context, ...llmclient.Option) (<-chan llmclient.Event, error) {
	ch := make(chan llmclient.Event)
	close(ch)
	return ch, nil
}
func (fakeBackend) Name() string    { return "fake" }
func (fakeBackend) Available() bool { return true }
func (fakeBackend) Ready(context.Context) llmclient.ReadyReport {
	return llmclient.ReadyReport{State: llmclient.ReadyOK}
}
func (fakeBackend) Close() error { return nil }

func TestRunDispatchesRegisteredHandler(t *testing.T) {
	resetRegistryForTest()
	Register(HandlerFunc{
		Operation: "echo",
		Fn: func(_ context.Context, req Request, deps Deps) (json.RawMessage, error) {
			if deps.Backend.Name() != "fake" {
				t.Fatalf("backend = %q, want fake", deps.Backend.Name())
			}
			return req.Payload, nil
		},
	})

	in := strings.NewReader(`{"request_id":"r1","op":"echo","payload":` + okJSON + `}` + "\n")
	var out bytes.Buffer

	if err := Run(context.Background(), Config{In: in, Out: &out, Backend: fakeBackend{}, DisableLockfile: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode response: %v; output=%q", err, out.String())
	}
	if resp.RequestID != "r1" || !resp.Final || resp.Error != nil {
		t.Fatalf("response = %#v", resp)
	}
	if string(resp.Result) != okJSON {
		t.Fatalf("result = %s", resp.Result)
	}
}

func TestRunCancelRequest(t *testing.T) {
	resetRegistryForTest()
	started := make(chan struct{})
	Register(HandlerFunc{
		Operation: "wait",
		Fn: func(ctx context.Context, _ Request, _ Deps) (json.RawMessage, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})

	in := strings.NewReader(
		`{"request_id":"r1","op":"wait"}` + "\n" +
			`{"request_id":"r1","cancel":true}` + "\n",
	)
	var out bytes.Buffer

	if err := Run(context.Background(), Config{In: in, Out: &out, Backend: fakeBackend{}, DisableLockfile: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	<-started

	var resp Response
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode response: %v; output=%q", err, out.String())
	}
	if resp.Error == nil || resp.Error.Type != llmclient.ErrTypeCancelled {
		t.Fatalf("response error = %#v, want cancelled", resp.Error)
	}
}

func TestRunUnknownOpReturnsBadRequest(t *testing.T) {
	resetRegistryForTest()
	in := strings.NewReader(`{"request_id":"r1","op":"missing"}` + "\n")
	var out bytes.Buffer

	if err := Run(context.Background(), Config{In: in, Out: &out, Backend: fakeBackend{}, DisableLockfile: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil || resp.Error.Type != llmclient.ErrTypeBadRequest {
		t.Fatalf("response error = %#v, want bad_request", resp.Error)
	}
}
