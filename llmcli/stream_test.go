package llmcli

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// ─── fab-sswr: structuredStream ───────────────────────────────────────────────

// TestStructuredStream_EmitsStartAndDone verifies that structuredStream emits
// EventStart first, then all events from parseFn, then EventDone(StopEndTurn)
// when parseFn returns nil.
func TestStructuredStream_EmitsStartAndDone(t *testing.T) {
	parseFn := func(ctx context.Context, out chan<- llmclient.Event, _ *terminalEmitter) error {
		emit(ctx, out, llmclient.Event{Type: llmclient.EventTextDelta, Delta: "hello"})
		emit(ctx, out, llmclient.Event{Type: llmclient.EventTextDelta, Delta: " world"})
		return nil
	}

	ch := structuredStream(context.Background(), "test", "sess-1", "m", nil, parseFn, nil)
	events := waitForEvents(t, ch, 3*time.Second)

	if len(events) < 3 {
		t.Fatalf("got %d events, want at least 3 (start + 2 text + done)", len(events))
	}
	if events[0].Type != llmclient.EventStart {
		t.Errorf("events[0]: got %v, want EventStart", events[0].Type)
	}
	last := events[len(events)-1]
	if last.Type != llmclient.EventDone {
		t.Errorf("last event: got %v, want EventDone", last.Type)
	}
	if last.Reason != llmclient.StopEndTurn {
		t.Errorf("Reason: got %v, want StopEndTurn", last.Reason)
	}
}

// TestStructuredStream_EmitsError verifies that when parseFn returns a non-nil
// error the wrapper emits EventError (not EventDone).
func TestStructuredStream_EmitsError(t *testing.T) {
	sentinel := errors.New("parser failed")

	parseFn := func(_ context.Context, _ chan<- llmclient.Event, _ *terminalEmitter) error {
		return sentinel
	}

	ch := structuredStream(context.Background(), "test", "", "", nil, parseFn, nil)
	events := waitForEvents(t, ch, 2*time.Second)

	if len(events) == 0 {
		t.Fatal("no events received")
	}
	last := events[len(events)-1]
	if last.Type != llmclient.EventError {
		t.Errorf("last event: got %v, want EventError", last.Type)
	}
	if last.ErrorMessage != sentinel.Error() {
		t.Errorf("ErrorMessage: got %q, want %q", last.ErrorMessage, sentinel.Error())
	}
}

// spyObserver records every Observer call for assertion in tests.
type spyObserver struct {
	mu     sync.Mutex
	starts int
	ends   []struct {
		success bool
		errType string
	}
	eventTypes []llmclient.EventType
}

func (s *spyObserver) OnStreamStart(_, _ string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts++
}

func (s *spyObserver) OnStreamEnd(_, _ string, success bool, errType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ends = append(s.ends, struct {
		success bool
		errType string
	}{success, errType})
}

func (s *spyObserver) OnEventEmitted(_, _ string, et llmclient.EventType) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eventTypes = append(s.eventTypes, et)
}

func (s *spyObserver) OnSpawnDuration(_, _ string, _ time.Duration) {}
func (s *spyObserver) OnBackendAvailability(_ string, _ bool)       {}

// TestStructuredStream_InvokesObserver verifies that DefaultObserver hooks fire
// at the correct points: OnStreamStart once, OnEventEmitted once per event
// (including EventStart and EventDone), OnStreamEnd once with success=true.
func TestStructuredStream_InvokesObserver(t *testing.T) {
	spy := &spyObserver{}
	orig := GetDefaultObserver()
	SetDefaultObserver(spy)
	t.Cleanup(func() { SetDefaultObserver(orig) })

	parseFn := func(ctx context.Context, out chan<- llmclient.Event, _ *terminalEmitter) error {
		emit(ctx, out, llmclient.Event{Type: llmclient.EventTextDelta, Delta: "a"})
		emit(ctx, out, llmclient.Event{Type: llmclient.EventTextDelta, Delta: "b"})
		emit(ctx, out, llmclient.Event{Type: llmclient.EventTextDelta, Delta: "c"})
		return nil
	}

	ch := structuredStream(context.Background(), "mybackend", "", "mymodel", nil, parseFn, nil)
	waitForEvents(t, ch, 3*time.Second)

	spy.mu.Lock()
	starts := spy.starts
	ends := spy.ends
	eventCount := len(spy.eventTypes)
	spy.mu.Unlock()

	if starts != 1 {
		t.Errorf("OnStreamStart: called %d times, want 1", starts)
	}
	// start + 3 text deltas + done = at least 4 (done is also observed)
	if eventCount < 4 {
		t.Errorf("OnEventEmitted: called %d times, want >= 4 (start + 3 parser events)", eventCount)
	}
	if len(ends) != 1 {
		t.Errorf("OnStreamEnd: called %d times, want 1", len(ends))
	} else if !ends[0].success {
		t.Errorf("OnStreamEnd: success=%v, want true", ends[0].success)
	}
}

// TestStructuredStream_EnforcesSingleTerminal verifies that when parseFn emits
// a terminal via te.done and then returns a non-nil error, structuredStream does
// NOT emit a second terminal — terminalEmitter.once guards it.
func TestStructuredStream_EnforcesSingleTerminal(t *testing.T) {
	parseFn := func(ctx context.Context, out chan<- llmclient.Event, te *terminalEmitter) error {
		emit(ctx, out, llmclient.Event{Type: llmclient.EventTextDelta, Delta: "x"})
		emit(ctx, out, llmclient.Event{Type: llmclient.EventTextDelta, Delta: "y"})
		te.done(ctx, nil, nil, llmclient.StopEndTurn) // parseFn emits terminal
		return errors.New("error after terminal")     // structuredStream must ignore this
	}

	ch := structuredStream(context.Background(), "test", "", "", nil, parseFn, nil)
	events := waitForEvents(t, ch, 2*time.Second)

	terminalCount := 0
	for _, ev := range events {
		if ev.Type == llmclient.EventDone || ev.Type == llmclient.EventError {
			terminalCount++
		}
	}
	if terminalCount != 1 {
		t.Errorf("terminal event count: got %d, want exactly 1; events: %v", terminalCount, events)
	}
	last := events[len(events)-1]
	if last.Type != llmclient.EventDone {
		t.Errorf("terminal event: got %v, want EventDone (from parseFn's te.done)", last.Type)
	}
}

// TestStructuredStream_CtxCancelStopsCancelled verifies that when ctx is
// cancelled while parseFn is blocking, the wrapper emits EventDone with
// Reason=StopCancelled (not EventError).
func TestStructuredStream_CtxCancelStopsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	parseFn := func(ctx context.Context, _ chan<- llmclient.Event, _ *terminalEmitter) error {
		<-ctx.Done() // blocks until cancel()
		return nil   // returns nil; ctx.Err() != nil drives StopCancelled
	}

	ch := structuredStream(ctx, "test", "", "", nil, parseFn, nil)

	// Give the goroutine time to start and block on ctx.Done().
	time.Sleep(20 * time.Millisecond)
	cancel()

	events := waitForEvents(t, ch, 2*time.Second)

	if len(events) == 0 {
		t.Fatal("no events received")
	}
	if events[0].Type != llmclient.EventStart {
		t.Errorf("events[0]: got %v, want EventStart", events[0].Type)
	}
	last := events[len(events)-1]
	if last.Type != llmclient.EventDone {
		t.Errorf("terminal: got %v, want EventDone", last.Type)
	}
	if last.Reason != llmclient.StopCancelled {
		t.Errorf("Reason: got %v, want StopCancelled", last.Reason)
	}
}
