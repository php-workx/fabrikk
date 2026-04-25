package internal

import (
	"bufio"
	"io"
	"strings"
	"testing"
	"time"
)

const sseTestPayload = "payload"

// sseReader returns a *bufio.Reader over the given raw SSE stream text.
func sseReader(s string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(s))
}

// TestReadEvent_DataOnly verifies that a minimal event with a single data line
// is parsed correctly (Criterion 1: single-line data).
func TestReadEvent_DataOnly(t *testing.T) {
	const raw = "data: hello world\n\n"

	ev, err := ReadEvent(sseReader(raw), 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(ev.Data) != "hello world" {
		t.Errorf("Data: got %q; want %q", ev.Data, "hello world")
	}
	if ev.Event != "" {
		t.Errorf("Event: got %q; want empty", ev.Event)
	}
	if ev.ID != "" {
		t.Errorf("ID: got %q; want empty", ev.ID)
	}
}

// TestReadEvent_EventAndID verifies that event: and id: fields are parsed
// correctly (Criterion 1: event and id).
func TestReadEvent_EventAndID(t *testing.T) {
	const raw = "event: message.delta\nid: evt-42\ndata: {\"text\":\"hi\"}\n\n"

	ev, err := ReadEvent(sseReader(raw), 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Event != "message.delta" {
		t.Errorf("Event: got %q; want %q", ev.Event, "message.delta")
	}
	if ev.ID != "evt-42" {
		t.Errorf("ID: got %q; want %q", ev.ID, "evt-42")
	}
	if string(ev.Data) != `{"text":"hi"}` {
		t.Errorf("Data: got %q; want %q", ev.Data, `{"text":"hi"}`)
	}
}

// TestReadEvent_RetryField verifies that the retry: field is parsed as a
// Duration in milliseconds (Criterion 1).
func TestReadEvent_RetryField(t *testing.T) {
	const raw = "retry: 3000\ndata: ok\n\n"

	ev, err := ReadEvent(sseReader(raw), 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Retry != 3*time.Second {
		t.Errorf("Retry: got %v; want 3s", ev.Retry)
	}
}

// TestReadEvent_MultilineData verifies that multiple data: lines are joined
// with '\n' (Criterion 1: multiline data).
func TestReadEvent_MultilineData(t *testing.T) {
	const raw = "data: line one\ndata: line two\ndata: line three\n\n"

	ev, err := ReadEvent(sseReader(raw), 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const want = "line one\nline two\nline three"
	if string(ev.Data) != want {
		t.Errorf("Data: got %q; want %q", ev.Data, want)
	}
}

// TestReadEvent_OversizeData verifies that a data line exceeding maxBytes is
// silently dropped (the line is drained) and the remaining event fields are
// still parsed (Criterion 1: oversize handling).
func TestReadEvent_OversizeData(t *testing.T) {
	// Build a raw stream with an oversize data line followed by a normal field,
	// then the blank-line event terminator.
	oversizeLine := "data: " + strings.Repeat("x", 200) + "\n"
	raw := oversizeLine + "event: big\n\n"

	ev, err := ReadEvent(sseReader(raw), 64) // limit is 64 bytes
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Oversize data line should be dropped.
	if len(ev.Data) != 0 {
		t.Errorf("Data: got %q; want empty (oversize dropped)", ev.Data)
	}
	// The event: field after the oversize line must still be present.
	if ev.Event != "big" {
		t.Errorf("Event: got %q; want %q", ev.Event, "big")
	}
}

// TestReadEvent_EmptyStream verifies that an empty reader returns (zero, io.EOF).
func TestReadEvent_EmptyStream(t *testing.T) {
	ev, err := ReadEvent(sseReader(""), 4096)
	if err != io.EOF {
		t.Fatalf("err: got %v; want io.EOF", err)
	}
	if ev.Event != "" || ev.ID != "" || len(ev.Data) != 0 {
		t.Errorf("expected zero event on empty stream, got %+v", ev)
	}
}

// TestReadEvent_HeartbeatThenEvent verifies that padding empty lines before a
// real event are skipped (Criterion 1: blank-line handling).
func TestReadEvent_HeartbeatThenEvent(t *testing.T) {
	// Two empty heartbeat lines, then a real event.
	const raw = "\n\ndata: after heartbeat\n\n"

	ev, err := ReadEvent(sseReader(raw), 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(ev.Data) != "after heartbeat" {
		t.Errorf("Data: got %q; want %q", ev.Data, "after heartbeat")
	}
}

// TestReadEvent_CommentLinesIgnored verifies that SSE comment lines (starting
// with ':') are silently ignored.
func TestReadEvent_CommentLinesIgnored(t *testing.T) {
	const raw = ": this is a comment\ndata: " + sseTestPayload + "\n\n"

	ev, err := ReadEvent(sseReader(raw), 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(ev.Data) != sseTestPayload {
		t.Errorf("Data: got %q; want %q", ev.Data, sseTestPayload)
	}
}

func TestReadEvent_CommentOnlyBlockSkipped(t *testing.T) {
	const raw = ": keepalive\n\ndata: " + sseTestPayload + "\n\n"

	ev, err := ReadEvent(sseReader(raw), 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(ev.Data) != sseTestPayload {
		t.Errorf("Data: got %q; want %q", ev.Data, sseTestPayload)
	}
}

func TestReadEvent_OversizeOnlyBlockSkipped(t *testing.T) {
	oversizeLine := "data: " + strings.Repeat("x", 200) + "\n\n"
	raw := oversizeLine + "data: " + sseTestPayload + "\n\n"

	ev, err := ReadEvent(sseReader(raw), 64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(ev.Data) != sseTestPayload {
		t.Errorf("Data: got %q; want %q", ev.Data, sseTestPayload)
	}
}

// TestReadEvent_PartialEventAtEOF verifies that a partial event (no trailing
// blank line) is dispatched with io.EOF (Criterion 1).
func TestReadEvent_PartialEventAtEOF(t *testing.T) {
	const raw = "data: incomplete" // no trailing \n\n

	ev, err := ReadEvent(sseReader(raw), 4096)
	if err != io.EOF {
		t.Fatalf("err: got %v; want io.EOF", err)
	}
	if string(ev.Data) != "incomplete" {
		t.Errorf("Data: got %q; want %q", ev.Data, "incomplete")
	}
}

// TestReadEvent_MultipleEvents verifies that successive calls each return one
// event in order.
func TestReadEvent_MultipleEvents(t *testing.T) {
	const raw = "data: first\n\ndata: second\n\n"
	r := sseReader(raw)

	ev1, err1 := ReadEvent(r, 4096)
	if err1 != nil {
		t.Fatalf("first event error: %v", err1)
	}
	if string(ev1.Data) != "first" {
		t.Errorf("first event data: got %q; want %q", ev1.Data, "first")
	}

	ev2, err2 := ReadEvent(r, 4096)
	if err2 != nil {
		t.Fatalf("second event error: %v", err2)
	}
	if string(ev2.Data) != "second" {
		t.Errorf("second event data: got %q; want %q", ev2.Data, "second")
	}

	_, err3 := ReadEvent(r, 4096)
	if err3 != io.EOF {
		t.Fatalf("third call: got %v; want io.EOF", err3)
	}
}

// TestReadEvent_NoSpaceAfterColon verifies that the optional single space after
// the colon separator is stripped; lines without a space are also handled.
func TestReadEvent_NoSpaceAfterColon(t *testing.T) {
	const raw = "data:nospace\n\n"

	ev, err := ReadEvent(sseReader(raw), 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(ev.Data) != "nospace" {
		t.Errorf("Data: got %q; want %q", ev.Data, "nospace")
	}
}
