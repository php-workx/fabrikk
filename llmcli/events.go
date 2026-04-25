package llmcli

import (
	"context"
	"sync"

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
	ch   chan<- llmclient.Event
	once sync.Once
}

// newTerminalEmitter returns a terminalEmitter that targets ch.
func newTerminalEmitter(ch chan<- llmclient.Event) *terminalEmitter {
	return &terminalEmitter{ch: ch}
}

// done emits an EventDone event then closes the channel. If a terminal event
// has already been emitted, done is a no-op.
func (te *terminalEmitter) done(ctx context.Context, msg *llmclient.AssistantMessage, usage *llmclient.Usage, reason llmclient.StopReason) {
	te.once.Do(func() {
		defer close(te.ch)
		ev := doneEvent(msg, usage, reason)
		select {
		case te.ch <- ev:
			return
		default:
		}
		emit(ctx, te.ch, ev)
	})
}

// error emits an EventError event then closes the channel. If a terminal event
// has already been emitted, error is a no-op. The send is attempted even when
// ctx is already cancelled so that consumers receive error context; the channel
// is always closed regardless.
func (te *terminalEmitter) error(ctx context.Context, err error) { //nolint:predeclared // method on unexported type; no package-level shadow
	te.once.Do(func() {
		defer close(te.ch)
		select {
		case te.ch <- errorEvent(err):
		case <-ctx.Done():
			// Consumer may have exited due to cancellation; skip the send but
			// always close via the deferred close above.
		}
	})
}

// close closes the channel without emitting a terminal event. Use this only as
// a last-resort deferred guard when done or error may not have fired. If a
// terminal event has already been emitted and the channel is already closed,
// close is a no-op.
func (te *terminalEmitter) close() { //nolint:predeclared // method on unexported type; no package-level shadow
	te.once.Do(func() {
		close(te.ch)
	})
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
	}
}
