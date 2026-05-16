package llmcli

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/php-workx/fabrikk/llmclient"
)

// emit sends ev to out and returns true. If ctx is cancelled before the send
// completes, emit discards the event and returns false. emit never closes the
// channel.
func emit(ctx context.Context, out chan<- llmclient.Event, ev llmclient.Event) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// terminalEmitter enforces the invariant that exactly one terminal event
// (EventDone or EventError) is sent to a stream channel, and that the channel
// is closed exactly once after that terminal event.  Backends create one
// terminalEmitter per Stream call and share it across all goroutines that may
// produce terminal events (parser, subprocess wait, context cancellation).
type terminalEmitter struct {
	ch       chan<- llmclient.Event
	once     sync.Once
	hasFired atomic.Bool
}

// newTerminalEmitter returns a terminalEmitter that targets ch.
func newTerminalEmitter(ch chan<- llmclient.Event) *terminalEmitter {
	return &terminalEmitter{ch: ch}
}

// done emits an EventDone event then closes the channel. If a terminal event
// has already been emitted, done is a no-op.
func (te *terminalEmitter) done(_ context.Context, msg *llmclient.AssistantMessage, usage *llmclient.Usage, reason llmclient.StopReason) {
	te.once.Do(func() {
		te.hasFired.Store(true)
		defer close(te.ch)
		ev := doneEvent(msg, usage, reason)
		select {
		case te.ch <- ev:
			return
		default:
		}
		// Channel full on fast path; fall back to a blocking send that ignores
		// ctx cancellation so the terminal event is always delivered before close.
		// observeStream always drains the channel, so this unblocks promptly.
		emit(context.Background(), te.ch, ev)
	})
}

// error emits an EventError event then closes the channel. If a terminal event
// has already been emitted, error is a no-op. The send always completes
// regardless of ctx cancellation so that exactly one terminal event is
// delivered before close.
func (te *terminalEmitter) error(_ context.Context, err error) { //nolint:predeclared // method on unexported type; no package-level shadow
	te.once.Do(func() {
		te.hasFired.Store(true)
		defer close(te.ch)
		ev := errorEvent(err)
		select {
		case te.ch <- ev:
			return
		default:
		}
		// Channel full on fast path; fall back to a blocking send ignoring ctx
		// cancellation so the terminal event is always delivered before close.
		// observeStream always drains the channel, so this unblocks promptly.
		emit(context.Background(), te.ch, ev)
	})
}

// close closes the channel without emitting a terminal event. Use this only as
// a last-resort deferred guard when done or error may not have fired. If a
// terminal event has already been emitted and the channel is already closed,
// close is a no-op.
func (te *terminalEmitter) close() { //nolint:predeclared // method on unexported type; no package-level shadow
	te.once.Do(func() {
		te.hasFired.Store(true)
		close(te.ch)
	})
}

// fired reports whether a terminal event has been emitted. It returns true
// after the first call to done, error, or close completes. This is used by
// structuredStream to detect when a parserFunc has already emitted a terminal
// and skip the fallback terminal emission.
func (te *terminalEmitter) fired() bool {
	return te.hasFired.Load()
}

// startEvent returns an EventStart event carrying sessionID and fidelity.
// fidelity may be nil when the backend cannot determine capabilities before
// streaming begins.
func startEvent(sessionID string, fidelity *llmclient.Fidelity) llmclient.Event {
	return llmclient.Event{
		Type:      llmclient.EventStart,
		SessionID: sessionID,
		Fidelity:  fidelity,
	}
}

// textSequence returns the three events that represent a complete text content
// block: EventTextStart, EventTextDelta (carrying the incremental text), and
// EventTextEnd (carrying the full accumulated text). index is the ContentIndex
// shared by all three events and must match the index used for any earlier
// start/delta events in the same block.
func textSequence(index int, text string) []llmclient.Event {
	return []llmclient.Event{
		{Type: llmclient.EventTextStart, ContentIndex: index},
		{Type: llmclient.EventTextDelta, ContentIndex: index, Delta: text},
		{Type: llmclient.EventTextEnd, ContentIndex: index, Content: text},
	}
}

// doneEvent returns an EventDone event. msg and usage may be nil when the
// backend does not provide them.
func doneEvent(msg *llmclient.AssistantMessage, usage *llmclient.Usage, reason llmclient.StopReason) llmclient.Event {
	return llmclient.Event{
		Type:    llmclient.EventDone,
		Message: msg,
		Usage:   usage,
		Reason:  reason,
	}
}

// errorEvent returns an EventError event whose ErrorMessage is the string
// representation of err. A nil err produces an empty ErrorMessage.
func errorEvent(err error) llmclient.Event {
	msg := ""
	if err != nil {
		msg = err.Error()
	}

	return llmclient.Event{
		Type:         llmclient.EventError,
		ErrorMessage: msg,
		ErrorType:    LabelErrorType(err),
	}
}
