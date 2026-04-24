package llmcli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/php-workx/fabrikk/llmclient"
)

// probePipeStreaming reports whether the binary at path produces incremental
// stdout output when stdout is a pipe rather than a TTY. A return value of
// false means StreamingBufferedOnly is the appropriate fidelity; true means
// StreamingTextChunk is safe to use.
//
// The current implementation conservatively returns false: reliably detecting
// incremental output without making a live LLM request (which incurs latency
// and cost) is not feasible. Callers must use StreamingBufferedOnly unless
// out-of-band evidence of incremental pipe output is available.
func probePipeStreaming(_ context.Context, _ string, _ []string) bool {
	return false
}

// streamTextProcess starts the subprocess described by spec, reads its stdout
// as plain text, and delivers a normalized Event stream on the returned channel.
// cleanup (may be nil) is called exactly once after the background goroutine
// exits — use it to remove temporary files that were created before the call.
//
// fidelity controls how text events are emitted:
//   - StreamingBufferedOnly: stdout is fully drained before emitting a single
//     text_start → text_delta → text_end triple.
//   - StreamingTextChunk: stdout is read in small chunks as they arrive; each
//     non-empty chunk produces an immediate text_delta event. Only use this
//     when probePipeStreaming has confirmed incremental output for the binary.
//
// The returned channel carries exactly one terminal event (EventDone or
// EventError) and is then closed. Cancelling ctx terminates the subprocess
// and produces a StopCancelled done event.
func streamTextProcess(
	ctx context.Context,
	spec processSpec,
	fidelity *llmclient.Fidelity,
	cleanup func(),
) (<-chan llmclient.Event, error) {
	s, err := startSupervised(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("llmcli: start text process: %w", err)
	}

	ch := make(chan llmclient.Event, 16)
	te := newTerminalEmitter(ch)

	go func() {
		if cleanup != nil {
			defer cleanup()
		}
		defer te.close()

		effectiveFidelity := fidelity
		if effectiveFidelity == nil {
			effectiveFidelity = &llmclient.Fidelity{
				Streaming:   llmclient.StreamingBufferedOnly,
				ToolControl: llmclient.ToolControlNone,
			}
		}
		streaming := effectiveFidelity.Streaming
		if streaming == "" {
			streaming = llmclient.StreamingBufferedOnly
			effectiveFidelity.Streaming = streaming
		}
		if effectiveFidelity.ToolControl == "" {
			effectiveFidelity.ToolControl = llmclient.ToolControlNone
		}
		emit(ctx, ch, startEvent("", effectiveFidelity))

		var accText string
		if streaming == llmclient.StreamingTextChunk {
			accText = emitTextChunked(ctx, s.Stdout, ch)
		} else {
			accText = drainTextBuffered(s.Stdout)
			events := textSequence(0, accText)
			for i := range events {
				emit(ctx, ch, events[i])
			}
		}

		// Drain any remaining bytes so cmd.Wait does not deadlock.
		_, _ = io.Copy(io.Discard, s.Stdout)
		waitErr := s.wait()

		switch {
		case ctx.Err() != nil:
			te.done(ctx, nil, nil, llmclient.StopCancelled)
		case waitErr != nil:
			te.error(ctx, fmt.Errorf("llmcli: subprocess: %w; stderr: %s", waitErr, s.stderrTail()))
		default:
			msg := &llmclient.AssistantMessage{
				Role: "assistant",
				Content: []llmclient.ContentBlock{{
					Type: llmclient.ContentText,
					Text: accText,
				}},
				StopReason: llmclient.StopEndTurn,
			}
			te.done(ctx, msg, nil, llmclient.StopEndTurn)
		}
	}()

	return ch, nil
}

// drainTextBuffered reads all of r into a buffer and returns the accumulated
// text with a trailing newline trimmed. Read errors are silently ignored;
// process exit status (via supervisor.wait) is the authoritative error signal.
func drainTextBuffered(r io.Reader) string {
	var buf strings.Builder
	_, _ = io.Copy(&buf, r)
	return strings.TrimRight(buf.String(), "\n")
}

// emitTextChunked reads r in 4 KiB chunks and emits one text_delta event per
// chunk, bracketed by text_start and text_end. It returns the accumulated text
// with a trailing newline trimmed. Context cancellation and read errors are
// handled gracefully: the function exits early, and the caller detects
// ctx.Err() or the subprocess exit status in the outer switch.
func emitTextChunked(ctx context.Context, r io.Reader, ch chan<- llmclient.Event) string {
	emit(ctx, ch, llmclient.Event{Type: llmclient.EventTextStart, ContentIndex: 0})

	var sb strings.Builder
	buf := make([]byte, 4096)

	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			sb.WriteString(chunk)
			emit(ctx, ch, llmclient.Event{
				Type:         llmclient.EventTextDelta,
				ContentIndex: 0,
				Delta:        chunk,
			})
		}
		if err != nil {
			break
		}
	}

	text := strings.TrimRight(sb.String(), "\n")
	emit(ctx, ch, llmclient.Event{
		Type:         llmclient.EventTextEnd,
		ContentIndex: 0,
		Content:      text,
	})

	return text
}
