package llmcli

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

type observedAvailability struct {
	backend   string
	available bool
}

type observedEnd struct {
	backend string
	model   string
	success bool
	errType string
}

type recordingObserver struct {
	mu            sync.Mutex
	starts        []struct{ backend, model string }
	ends          []observedEnd
	eventTypes    []llmclient.EventType
	spawnDuration []time.Duration
	availability  []observedAvailability
}

func (r *recordingObserver) OnStreamStart(backend, model string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, struct{ backend, model string }{backend: backend, model: model})
}

func (r *recordingObserver) OnStreamEnd(backend, model string, success bool, errType string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ends = append(r.ends, observedEnd{
		backend: backend,
		model:   model,
		success: success,
		errType: errType,
	})
}

func (r *recordingObserver) OnEventEmitted(_, _ string, eventType llmclient.EventType) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.eventTypes = append(r.eventTypes, eventType)
}

func (r *recordingObserver) OnSpawnDuration(_, _ string, dur time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spawnDuration = append(r.spawnDuration, dur)
}

func (r *recordingObserver) OnBackendAvailability(backend string, available bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.availability = append(r.availability, observedAvailability{
		backend:   backend,
		available: available,
	})
}

func TestCliBackendAvailable_ReportsObserver(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "llmcli-available-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	_ = file.Close()

	rec := &recordingObserver{}
	old := DefaultObserver
	DefaultObserver = rec
	t.Cleanup(func() { DefaultObserver = old })

	b := NewCliBackend("test-backend", CliInfo{Path: file.Name()})
	if !b.Available() {
		t.Fatal("Available() = false, want true")
	}

	if len(rec.availability) != 1 {
		t.Fatalf("availability calls = %d, want 1", len(rec.availability))
	}
	if rec.availability[0].backend != "test-backend" {
		t.Errorf("availability backend = %q, want %q", rec.availability[0].backend, "test-backend")
	}
	if !rec.availability[0].available {
		t.Error("availability = false, want true")
	}
}

func TestOmpRPCAvailable_ReportsObserver(t *testing.T) {
	rec := &recordingObserver{}
	old := DefaultObserver
	DefaultObserver = rec
	t.Cleanup(func() { DefaultObserver = old })

	b := NewOmpRPCBackend(CliInfo{Path: testExecutable(t)})
	if !b.Available() {
		t.Fatal("Available() = false, want true")
	}

	if len(rec.availability) != 1 {
		t.Fatalf("availability calls = %d, want 1", len(rec.availability))
	}
	if rec.availability[0].backend != ompRPCBackend {
		t.Errorf("availability backend = %q, want %q", rec.availability[0].backend, ompRPCBackend)
	}
	if !rec.availability[0].available {
		t.Error("availability = false, want true")
	}
}

func TestCodexBackendStream_ReportsObserverHooks(t *testing.T) {
	t.Setenv("FABRIKK_LLMCLI_TEST_VERSION", "ok")

	rec := &recordingObserver{}
	old := DefaultObserver
	DefaultObserver = rec
	t.Cleanup(func() { DefaultObserver = old })

	b := NewCodexBackend(CliInfo{Path: testExecutable(t)})
	ch, err := b.Stream(
		context.Background(),
		simpleUserInput("hello"),
		llmclient.WithModel("gpt-oss"),
	)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainEventsWithTimeout(t, ch, 5*time.Second)
	if len(events) == 0 {
		t.Fatal("no events emitted")
	}

	if len(rec.starts) != 1 {
		t.Fatalf("stream starts = %d, want 1", len(rec.starts))
	}
	if rec.starts[0].backend != codexExecBackendName {
		t.Errorf("stream start backend = %q, want %q", rec.starts[0].backend, codexExecBackendName)
	}
	if rec.starts[0].model != "gpt-oss" {
		t.Errorf("stream start model = %q, want %q", rec.starts[0].model, "gpt-oss")
	}

	if len(rec.spawnDuration) != 1 {
		t.Fatalf("spawn duration count = %d, want 1", len(rec.spawnDuration))
	}
	if rec.spawnDuration[0] < 0 {
		t.Errorf("spawn duration = %v, want >= 0", rec.spawnDuration[0])
	}

	if len(rec.eventTypes) != len(events) {
		t.Fatalf("observer event count = %d, want %d", len(rec.eventTypes), len(events))
	}
	for i, ev := range events {
		if rec.eventTypes[i] != ev.Type {
			t.Fatalf("observer event %d = %q, want %q", i, rec.eventTypes[i], ev.Type)
		}
	}

	if len(rec.ends) != 1 {
		t.Fatalf("stream ends = %d, want 1", len(rec.ends))
	}
	if !rec.ends[0].success {
		t.Error("stream end success = false, want true")
	}
	if rec.ends[0].errType != "none" {
		t.Errorf("stream end errType = %q, want %q", rec.ends[0].errType, "none")
	}
}

func drainEventsWithTimeout(t *testing.T, ch <-chan llmclient.Event, timeout time.Duration) []llmclient.Event {
	t.Helper()

	result := make(chan []llmclient.Event, 1)
	go func() {
		result <- drainChannel(ch)
	}()

	select {
	case evs := <-result:
		return evs
	case <-time.After(timeout):
		t.Fatalf("channel was not closed within timeout %s", timeout)
		return nil
	}
}
