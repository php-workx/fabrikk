package internal

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"time"
)

// SSEEvent holds one dispatched event from a text/event-stream SSE stream.
// Fields correspond to the SSE spec (https://html.spec.whatwg.org/multipage/server-sent-events.html).
type SSEEvent struct {
	// Event is the event type from the "event:" field. Empty means "message".
	Event string

	// Data is the concatenated data from all "data:" fields in the event block,
	// joined by '\n' (the trailing '\n' is stripped per the SSE spec).
	Data []byte

	// ID is the last-event-id from the "id:" field.
	ID string

	// Retry is the reconnection delay from the "retry:" field (ms → Duration).
	// Zero means the field was absent or non-numeric.
	Retry time.Duration
}

// ReadEvent reads one complete SSE event from r and returns it.
//
// An SSE event is a block of colon-separated field lines terminated by an
// empty line. ReadEvent uses ReadBoundedLine to enforce maxBytes per line;
// oversize lines are drained and their field is silently dropped (the rest of
// the event is still accumulated). Multiple "data:" lines are joined with '\n'.
//
// Behaviour by case:
//   - Normal event: returns (event, nil).
//   - Stream ended before any data field: returns (zero, io.EOF).
//   - Stream ended mid-event (no trailing blank line): returns the accumulated
//     partial event with nil error; the next call returns io.EOF.
//   - Context-cancelled read: propagates the underlying error.
func ReadEvent(r *bufio.Reader, maxBytes int) (SSEEvent, error) {
	var ev SSEEvent
	var dataLines [][]byte
	dataSeen := false

	for {
		line, err := ReadBoundedLine(r, maxBytes)
		if err != nil {
			done, retEv, retErr := handleReadEventError(err, line, &ev, &dataLines, &dataSeen)
			if done {
				return retEv, retErr
			}
			if retErr != nil {
				return SSEEvent{}, retErr
			}
			continue
		}

		// An empty line dispatches the accumulated event.
		if len(line) == 0 {
			if !dataSeen {
				// Heartbeat / padding empty line before any event fields.
				ev = SSEEvent{}
				dataLines = nil
				continue
			}
			ev.Data = buildData(dataLines)
			return ev, nil
		}

		// Parse "field: value" or "field" (no colon → value is empty string).
		dataSeen = applySSEField(&ev, &dataLines, line) || dataSeen
	}
}

func handleReadEventError(
	err error,
	line []byte,
	ev *SSEEvent,
	dataLines *[][]byte,
	dataSeen *bool,
) (done bool, retEv SSEEvent, retErr error) {
	if errors.Is(err, ErrLineTooLong) {
		// Oversize line: field is dropped but does not make an otherwise-empty
		// event block dispatchable on its own.
		return false, SSEEvent{}, nil
	}
	if !errors.Is(err, io.EOF) {
		return true, SSEEvent{}, err
	}

	if len(line) > 0 {
		// ReadBoundedLine returned a partial line AND io.EOF together
		// (no trailing newline). Process the field before returning.
		*dataSeen = applySSEField(ev, dataLines, line) || *dataSeen
	}
	if !*dataSeen {
		// Empty stream or only blank lines seen.
		return true, SSEEvent{}, io.EOF
	}
	// Partial event at EOF: dispatch what we have.
	ev.Data = buildData(*dataLines)
	return true, *ev, nil
}

func applySSEField(ev *SSEEvent, dataLines *[][]byte, line []byte) bool {
	fieldName, value := splitSSEField(line)

	switch string(fieldName) {
	case "data":
		cp := make([]byte, len(value))
		copy(cp, value)
		*dataLines = append(*dataLines, cp)
		return true
	case "event":
		ev.Event = string(value)
		return false
	case "id":
		ev.ID = string(value)
		return false
	case "retry":
		if ms, parseErr := strconv.ParseInt(string(value), 10, 64); parseErr == nil {
			ev.Retry = time.Duration(ms) * time.Millisecond
		}
	}
	// Lines starting with ':' are SSE comments; ignored.
	// Unknown field names are also ignored per the spec.
	return false
}

// splitSSEField splits an SSE line into field name and value.
//
// Format: "field: value" or "field:value" or "field" (no colon).
// A single space after the colon is stripped per the SSE spec.
func splitSSEField(line []byte) (name, value []byte) {
	idx := bytes.IndexByte(line, ':')
	if idx < 0 {
		// No colon: entire line is the field name, value is empty.
		return line, nil
	}
	name = line[:idx]
	value = line[idx+1:]
	// Strip exactly one leading space per the SSE spec.
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	return name, value
}

// buildData joins data lines with '\n'. An empty data line (from a bare
// "data:" field) contributes an empty []byte element, producing the separator
// '\n' per the SSE spec. Returns nil when dataLines is empty.
func buildData(dataLines [][]byte) []byte {
	if len(dataLines) == 0 {
		return nil
	}
	return bytes.Join(dataLines, []byte("\n"))
}
