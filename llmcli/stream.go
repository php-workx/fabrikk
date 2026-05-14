package llmcli

import (
	"context"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// parserFunc reads from the backend-owned reader and emits events via out.
// It must not emit terminal events (done/error); the structuredStream wrapper
// does that via te based on the returned error. Returning a non-nil error
// causes the wrapper to emit EventError. Returning nil causes the wrapper to
// emit EventDone(StopEndTurn), or EventDone(StopCancelled) if ctx.Err() != nil.
//
// Exception: parseFn may call te.done or te.error directly for in-band
// terminal signals (e.g. a protocol-level "done" frame). structuredStream
// detects this via te.fired() and skips the fallback terminal emission so
// exactly one terminal is always sent.
type parserFunc func(ctx context.Context, out chan<- llmclient.Event, te *terminalEmitter) error

// structuredStream returns a channel of events for a single backend turn.
// It emits the start event carrying the provided fidelity, invokes parseFn in
// a goroutine, then emits exactly one terminal event based on parseFn's outcome.
// Observer hooks (via DefaultObserver) fire at stream start, per-event, and at
// stream end.
//
// onClose is called (when non-nil) after parseFn returns and after exactly one
// terminal event has been emitted. Use it for cleanup such as releasing
// semaphores or closing SSE response bodies.
func structuredStream(
	ctx context.Context,
	backend string,
	sessionID string,
	model string,
	fidelity *llmclient.Fidelity,
	parseFn parserFunc,
	onClose func(),
) <-chan llmclient.Event {
	ch := make(chan llmclient.Event, 16)
	te := newTerminalEmitter(ch)

	DefaultObserver.OnStreamStart(backend, model)
	started := time.Now()

	go func() {
		if onClose != nil {
			defer onClose()
		}

		emit(ctx, ch, startEvent(sessionID, fidelity))
		parseErr := parseFn(ctx, ch, te)

		if !te.fired() {
			switch {
			case parseErr != nil:
				te.error(ctx, parseErr)
			case ctx.Err() != nil:
				te.done(ctx, nil, nil, llmclient.StopCancelled)
			default:
				te.done(ctx, nil, nil, llmclient.StopEndTurn)
			}
		}
	}()

	return observeStream(backend, model, started, ch)
}
