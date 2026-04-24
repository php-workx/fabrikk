package llmcli

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// drainChannel reads all events from ch until it is closed.
func drainChannel(ch <-chan llmclient.Event) []llmclient.Event {
	var events []llmclient.Event
	for ev := range ch {
		events = append(events, ev)
	}

	return events
}

// drainWithTimeout reads all events from ch until it is closed, or fails the
// test if the channel is not closed within one second.
func drainWithTimeout(t *testing.T, ch <-chan llmclient.Event) []llmclient.Event {
	t.Helper()

	result := make(chan []llmclient.Event, 1)

	go func() {
		result <- drainChannel(ch)
	}()

	select {
	case evs := <-result:
		return evs
	case <-time.After(time.Second):
		t.Fatal("channel was not closed within timeout")
		return nil
	}
}

// TestTextSequence_Order verifies that textSequence returns exactly three events
// in the required order (start, delta, end) sharing the same ContentIndex, and
// that Delta/Content carry the text correctly.
func TestTextSequence_Order(t *testing.T) {
	const (
		idx  = 2
		text = "hello world"
	)

	events := textSequence(idx, text)

	if len(events) != 3 {
		t.Fatalf("textSequence: want 3 events, got %d", len(events))
	}

	want := []llmclient.EventType{
		llmclient.EventTextStart,
		llmclient.EventTextDelta,
		llmclient.EventTextEnd,
	}
	for i, wt := range want {
		if events[i].Type != wt {
			t.Errorf("events[%d].Type = %q, want %q", i, events[i].Type, wt)
		}

		if events[i].ContentIndex != idx {
			t.Errorf("events[%d].ContentIndex = %d, want %d", i, events[i].ContentIndex, idx)
		}
	}

	if events[1].Delta != text {
		t.Errorf("EventTextDelta.Delta = %q, want %q", events[1].Delta, text)
	}

	if events[2].Content != text {
		t.Errorf("EventTextEnd.Content = %q, want %q", events[2].Content, text)
	}

	// EventTextStart and EventTextEnd must not carry Delta or Content that
	// belongs to the other event.
	if events[0].Delta != "" || events[0].Content != "" {
		t.Errorf("EventTextStart must not carry Delta/Content, got Delta=%q Content=%q",
			events[0].Delta, events[0].Content)
	}
}

// TestTextSequence_ZeroIndex verifies that textSequence handles index 0 correctly
// (ContentIndex 0 must not be mistaken for "unset").
func TestTextSequence_ZeroIndex(t *testing.T) {
	events := textSequence(0, "abc")

	for i, ev := range events {
		if ev.ContentIndex != 0 {
			t.Errorf("events[%d].ContentIndex = %d, want 0", i, ev.ContentIndex)
		}
	}
}

// TestTerminalEmitter_EmitsExactlyOneDone verifies that concurrent calls to done
// result in exactly one EventDone event in the channel.
func TestTerminalEmitter_EmitsExactlyOneDone(t *testing.T) {
	const concurrency = 8

	ch := make(chan llmclient.Event, concurrency)
	te := newTerminalEmitter(ch)
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			te.done(ctx, nil, nil, llmclient.StopEndTurn)
		}()
	}

	wg.Wait()

	events := drainWithTimeout(t, ch)

	if len(events) != 1 {
		t.Fatalf("want exactly 1 event, got %d: %v", len(events), events)
	}

	if events[0].Type != llmclient.EventDone {
		t.Errorf("event.Type = %q, want %q", events[0].Type, llmclient.EventDone)
	}

	if events[0].Reason != llmclient.StopEndTurn {
		t.Errorf("event.Reason = %q, want %q", events[0].Reason, llmclient.StopEndTurn)
	}
}

// TestTerminalEmitter_DoneCarriesMessage verifies that the AssistantMessage and
// Usage passed to done are forwarded onto the EventDone event.
func TestTerminalEmitter_DoneCarriesMessage(t *testing.T) {
	ch := make(chan llmclient.Event, 2)
	te := newTerminalEmitter(ch)
	ctx := context.Background()

	msg := &llmclient.AssistantMessage{
		ID:         "msg-42",
		Role:       "assistant",
		StopReason: llmclient.StopEndTurn,
	}
	usage := &llmclient.Usage{InputTokens: 15, OutputTokens: 30}

	te.done(ctx, msg, usage, llmclient.StopEndTurn)

	events := drainWithTimeout(t, ch)

	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}

	ev := events[0]
	if ev.Type != llmclient.EventDone {
		t.Errorf("Type = %q, want %q", ev.Type, llmclient.EventDone)
	}

	if ev.Message != msg {
		t.Error("Message pointer not forwarded to EventDone")
	}

	if ev.Usage != usage {
		t.Error("Usage pointer not forwarded to EventDone")
	}
}

// TestTerminalEmitter_ErrorWinsOnlyWhenFirst verifies the ordering guarantee:
// the first terminal call wins; subsequent calls are no-ops regardless of type.
func TestTerminalEmitter_ErrorWinsOnlyWhenFirst(t *testing.T) {
	t.Run("ErrorBeforeDone", func(t *testing.T) {
		ch := make(chan llmclient.Event, 4)
		te := newTerminalEmitter(ch)
		ctx := context.Background()

		testErr := errors.New("parse failure")
		te.error(ctx, testErr) //nolint:predeclared // method call on te; not a predeclared identifier
		te.done(ctx, nil, nil, llmclient.StopEndTurn)

		events := drainWithTimeout(t, ch)

		if len(events) != 1 {
			t.Fatalf("want 1 event, got %d: %v", len(events), events)
		}

		if events[0].Type != llmclient.EventError {
			t.Errorf("event.Type = %q, want %q", events[0].Type, llmclient.EventError)
		}

		if events[0].ErrorMessage != testErr.Error() {
			t.Errorf("event.ErrorMessage = %q, want %q", events[0].ErrorMessage, testErr.Error())
		}
	})

	t.Run("DoneBeforeError", func(t *testing.T) {
		ch := make(chan llmclient.Event, 4)
		te := newTerminalEmitter(ch)
		ctx := context.Background()

		te.done(ctx, nil, nil, llmclient.StopEndTurn)
		te.error(ctx, errors.New("should be suppressed")) //nolint:predeclared // method call on te

		events := drainWithTimeout(t, ch)

		if len(events) != 1 {
			t.Fatalf("want 1 event, got %d: %v", len(events), events)
		}

		if events[0].Type != llmclient.EventDone {
			t.Errorf("event.Type = %q, want %q", events[0].Type, llmclient.EventDone)
		}
	})

	t.Run("ErrorBeforeClose", func(t *testing.T) {
		ch := make(chan llmclient.Event, 4)
		te := newTerminalEmitter(ch)
		ctx := context.Background()

		te.error(ctx, errors.New("first terminal")) //nolint:predeclared // method call on te
		te.close()                                  //nolint:predeclared // method call on te

		events := drainWithTimeout(t, ch)

		if len(events) != 1 {
			t.Fatalf("want 1 event, got %d: %v", len(events), events)
		}

		if events[0].Type != llmclient.EventError {
			t.Errorf("event.Type = %q, want %q", events[0].Type, llmclient.EventError)
		}
	})

	t.Run("CloseBeforeError", func(t *testing.T) {
		ch := make(chan llmclient.Event, 4)
		te := newTerminalEmitter(ch)
		ctx := context.Background()

		te.close()                                        //nolint:predeclared // method call on te
		te.error(ctx, errors.New("should be suppressed")) //nolint:predeclared // method call on te

		events := drainWithTimeout(t, ch)

		if len(events) != 0 {
			t.Fatalf("want 0 events after close-first, got %d: %v", len(events), events)
		}
	})
}

// TestTerminalEmitter_CancellationVariant verifies that cancellation of the
// request context does not cause duplicate terminal events and always results
// in a closed channel.
func TestTerminalEmitter_CancellationVariant(t *testing.T) {
	t.Run("ErrorAfterCancellation", func(t *testing.T) {
		// Buffered channel so the send can succeed even with a cancelled ctx.
		ch := make(chan llmclient.Event, 2)
		te := newTerminalEmitter(ch)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // pre-cancel

		te.error(ctx, errors.New("context cancelled")) //nolint:predeclared // method call on te

		// Regardless of whether the event was sent (the select may have picked
		// ctx.Done()), the channel must be closed.
		events := drainWithTimeout(t, ch)

		// At most one error event. Zero is also acceptable when ctx.Done() won.
		if len(events) > 1 {
			t.Fatalf("want at most 1 event, got %d: %v", len(events), events)
		}

		for _, ev := range events {
			if ev.Type != llmclient.EventError {
				t.Errorf("event.Type = %q, want %q", ev.Type, llmclient.EventError)
			}
		}
	})

	t.Run("DoneAfterCancellation", func(t *testing.T) {
		ch := make(chan llmclient.Event, 2)
		te := newTerminalEmitter(ch)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // pre-cancel

		te.done(ctx, nil, nil, llmclient.StopCancelled)

		// With a pre-cancelled ctx, emit returns false and no event is sent,
		// but the channel is still closed.
		events := drainWithTimeout(t, ch)

		if len(events) > 1 {
			t.Fatalf("want at most 1 event, got %d: %v", len(events), events)
		}
	})
}

// TestTerminalEmitter_ParserError simulates a parser error mid-stream: some
// text events are emitted, then error fires, then done is (incorrectly) called
// by a separate goroutine. Exactly one terminal event must appear.
func TestTerminalEmitter_ParserError(t *testing.T) {
	const textBuf = 8

	ch := make(chan llmclient.Event, textBuf)
	te := newTerminalEmitter(ch)
	ctx := context.Background()

	// Emit normal text events before the parser error.
	for _, ev := range textSequence(0, "partial response") {
		if !emit(ctx, ch, ev) {
			t.Fatal("emit returned false unexpectedly")
		}
	}

	parseErr := errors.New("unexpected EOF in JSONL frame")
	te.error(ctx, parseErr) //nolint:predeclared // method call on te

	// Subprocess wait goroutine may also fire done; it must be suppressed.
	te.done(ctx, nil, nil, llmclient.StopEndTurn)

	events := drainWithTimeout(t, ch)

	// Expect: 3 text events + 1 error terminal.
	const wantLen = 4
	if len(events) != wantLen {
		t.Fatalf("want %d events, got %d: %v", wantLen, len(events), events)
	}

	last := events[len(events)-1]
	if last.Type != llmclient.EventError {
		t.Errorf("last event.Type = %q, want %q", last.Type, llmclient.EventError)
	}

	if last.ErrorMessage != parseErr.Error() {
		t.Errorf("last event.ErrorMessage = %q, want %q", last.ErrorMessage, parseErr.Error())
	}
}

// TestTerminalEmitter_Close verifies that calling close without done or error
// closes the channel and that subsequent close/done/error calls are no-ops.
func TestTerminalEmitter_Close(t *testing.T) {
	ch := make(chan llmclient.Event, 2)
	te := newTerminalEmitter(ch)

	te.close() //nolint:predeclared // method call on te
	te.close() //nolint:predeclared // must not panic; Once guards this

	ctx := context.Background()
	te.done(ctx, nil, nil, llmclient.StopEndTurn) // must also be a no-op

	events := drainWithTimeout(t, ch)

	if len(events) != 0 {
		t.Fatalf("close-only should produce no events, got %d: %v", len(events), events)
	}
}

// TestEmit_StopsOnContextCancel verifies that emit returns false immediately
// when the context is cancelled and no goroutine is reading the channel.
func TestEmit_StopsOnContextCancel(t *testing.T) {
	ch := make(chan llmclient.Event) // unbuffered; no reader

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	result := emit(ctx, ch, startEvent("sid", nil))
	if result {
		t.Error("emit should return false when ctx is already cancelled")
	}

	// Verify the channel is still empty (no event was sent).
	select {
	case ev := <-ch:
		t.Errorf("channel should be empty but got event: %v", ev)
	default:
	}
}

// TestEmit_SucceedsWithBuffer verifies that emit returns true when the channel
// has buffer space.
func TestEmit_SucceedsWithBuffer(t *testing.T) {
	ch := make(chan llmclient.Event, 1)
	ctx := context.Background()

	ev := startEvent("session-abc", nil)
	if !emit(ctx, ch, ev) {
		t.Error("emit should return true when the channel accepts the event")
	}

	got := <-ch
	if got.Type != llmclient.EventStart {
		t.Errorf("event.Type = %q, want %q", got.Type, llmclient.EventStart)
	}

	if got.SessionID != "session-abc" {
		t.Errorf("event.SessionID = %q, want %q", got.SessionID, "session-abc")
	}
}

// TestEmit_BlocksUntilSent verifies that emit blocks when the channel is full
// and returns true once a reader consumes the event.
func TestEmit_BlocksUntilSent(t *testing.T) {
	ch := make(chan llmclient.Event) // unbuffered
	ctx := context.Background()

	ev := startEvent("s1", nil)

	sent := make(chan bool, 1)

	go func() {
		sent <- emit(ctx, ch, ev)
	}()

	// Give the goroutine time to block on the send.
	time.Sleep(5 * time.Millisecond)

	got := <-ch // unblock the sender

	select {
	case result := <-sent:
		if !result {
			t.Error("emit should return true after successful send")
		}
	case <-time.After(time.Second):
		t.Fatal("emit did not unblock after the event was consumed")
	}

	if got.Type != llmclient.EventStart {
		t.Errorf("event.Type = %q, want %q", got.Type, llmclient.EventStart)
	}
}

// TestStartEvent verifies that startEvent populates Type, SessionID, and
// Fidelity correctly.
func TestStartEvent(t *testing.T) {
	fidelity := &llmclient.Fidelity{
		Streaming:   llmclient.StreamingTextChunk,
		ToolControl: llmclient.ToolControlNone,
	}

	ev := startEvent("test-session-123", fidelity)

	if ev.Type != llmclient.EventStart {
		t.Errorf("Type = %q, want %q", ev.Type, llmclient.EventStart)
	}

	if ev.SessionID != "test-session-123" {
		t.Errorf("SessionID = %q, want %q", ev.SessionID, "test-session-123")
	}

	if ev.Fidelity != fidelity {
		t.Error("Fidelity pointer not preserved")
	}

	// startEvent must not set any other fields.
	if ev.Delta != "" || ev.Content != "" || ev.ErrorMessage != "" {
		t.Errorf("unexpected non-empty field: Delta=%q Content=%q Error=%q",
			ev.Delta, ev.Content, ev.ErrorMessage)
	}
}

// TestStartEvent_NilFidelity verifies that startEvent accepts a nil fidelity
// without panicking.
func TestStartEvent_NilFidelity(t *testing.T) {
	ev := startEvent("", nil)

	if ev.Fidelity != nil {
		t.Errorf("expected nil Fidelity, got %v", ev.Fidelity)
	}
}

// TestDoneEvent verifies that doneEvent populates Type, Message, Usage, and
// Reason correctly.
func TestDoneEvent(t *testing.T) {
	msg := &llmclient.AssistantMessage{
		ID:   "msg-abc",
		Role: "assistant",
	}
	usage := &llmclient.Usage{InputTokens: 10, OutputTokens: 20}

	ev := doneEvent(msg, usage, llmclient.StopMaxTokens)

	if ev.Type != llmclient.EventDone {
		t.Errorf("Type = %q, want %q", ev.Type, llmclient.EventDone)
	}

	if ev.Message != msg {
		t.Error("Message pointer not preserved")
	}

	if ev.Usage != usage {
		t.Error("Usage pointer not preserved")
	}

	if ev.Reason != llmclient.StopMaxTokens {
		t.Errorf("Reason = %q, want %q", ev.Reason, llmclient.StopMaxTokens)
	}
}

// TestDoneEvent_NilFields verifies that doneEvent accepts nil msg and usage.
func TestDoneEvent_NilFields(t *testing.T) {
	ev := doneEvent(nil, nil, llmclient.StopEndTurn)

	if ev.Type != llmclient.EventDone {
		t.Errorf("Type = %q, want %q", ev.Type, llmclient.EventDone)
	}

	if ev.Message != nil {
		t.Errorf("expected nil Message, got %v", ev.Message)
	}

	if ev.Usage != nil {
		t.Errorf("expected nil Usage, got %v", ev.Usage)
	}
}

// TestErrorEvent verifies that errorEvent populates Type and ErrorMessage from
// the error string.
func TestErrorEvent(t *testing.T) {
	err := errors.New("something went wrong")
	ev := errorEvent(err)

	if ev.Type != llmclient.EventError {
		t.Errorf("Type = %q, want %q", ev.Type, llmclient.EventError)
	}

	if ev.ErrorMessage != err.Error() {
		t.Errorf("ErrorMessage = %q, want %q", ev.ErrorMessage, err.Error())
	}
}

// TestErrorEvent_NilError verifies that errorEvent with a nil error produces an
// empty ErrorMessage and does not panic.
func TestErrorEvent_NilError(t *testing.T) {
	ev := errorEvent(nil)

	if ev.Type != llmclient.EventError {
		t.Errorf("Type = %q, want %q", ev.Type, llmclient.EventError)
	}

	if ev.ErrorMessage != "" {
		t.Errorf("nil error should produce empty ErrorMessage, got %q", ev.ErrorMessage)
	}
}
