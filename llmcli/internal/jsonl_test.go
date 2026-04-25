package internal

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

// TestReadBoundedLine_Normal verifies that a typical short line ending in '\n'
// is returned correctly (Criterion 4: normal case).
func TestReadBoundedLine_Normal(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(`{"type":"text_delta","delta":"hello"}` + "\n"))

	got, err := ReadBoundedLine(r, 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	const want = `{"type":"text_delta","delta":"hello"}`
	if string(got) != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

// TestReadBoundedLine_LongPayloadWithinLimit verifies that a line whose content
// is exactly maxBytes bytes (excluding the newline) is returned without error
// (Criterion 4: long-within-limit case).
func TestReadBoundedLine_LongPayloadWithinLimit(t *testing.T) {
	const maxBytes = 100
	payload := strings.Repeat("x", maxBytes)

	r := bufio.NewReader(strings.NewReader(payload + "\n"))

	got, err := ReadBoundedLine(r, maxBytes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != maxBytes {
		t.Errorf("got %d bytes; want %d", len(got), maxBytes)
	}

	if string(got) != payload {
		t.Error("content mismatch")
	}
}

// TestReadBoundedLine_OversizeDrainsToNextLine verifies that an oversized line
// returns ErrLineTooLong and that the subsequent call reads the next line
// correctly (Criterion 4: oversize-with-drain case).
func TestReadBoundedLine_OversizeDrainsToNextLine(t *testing.T) {
	oversize := strings.Repeat("x", 200) + "\n"
	const nextLine = `{"type":"done"}` + "\n"

	r := bufio.NewReader(strings.NewReader(oversize + nextLine))

	// First call: line is 200 bytes, limit is 100 → ErrLineTooLong.
	got, err := ReadBoundedLine(r, 100)
	if err != ErrLineTooLong {
		t.Fatalf("first call: got err=%v; want ErrLineTooLong", err)
	}

	if got != nil {
		t.Errorf("first call: got non-nil content with ErrLineTooLong: %q", got)
	}

	// Second call: reader must be positioned at the start of the next line.
	got2, err2 := ReadBoundedLine(r, 100)
	if err2 != nil {
		t.Fatalf("second call: unexpected error: %v", err2)
	}

	const want2 = `{"type":"done"}`
	if string(got2) != want2 {
		t.Errorf("second call: got %q; want %q", got2, want2)
	}
}

// TestReadBoundedLine_EOFPartial verifies that a partial line at EOF (no
// terminating newline) is returned along with io.EOF (Criterion 4: EOF-partial
// case).
func TestReadBoundedLine_EOFPartial(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(`{"partial":true`))

	got, err := ReadBoundedLine(r, 1024)
	if err != io.EOF {
		t.Fatalf("got err=%v; want io.EOF", err)
	}

	const want = `{"partial":true`
	if string(got) != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

// TestReadBoundedLine_EmptyStream verifies that an empty reader returns
// (nil, io.EOF) without panicking.
func TestReadBoundedLine_EmptyStream(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))

	got, err := ReadBoundedLine(r, 1024)
	if err != io.EOF {
		t.Fatalf("got err=%v; want io.EOF", err)
	}

	if got != nil {
		t.Errorf("got non-nil content from empty stream: %q", got)
	}
}

// TestReadBoundedLine_MultipleLines verifies that successive calls each return
// one line in order and that the final call returns (nil, io.EOF).
func TestReadBoundedLine_MultipleLines(t *testing.T) {
	input := "line1\nline2\nline3\n"
	r := bufio.NewReader(strings.NewReader(input))
	want := []string{"line1", "line2", "line3"}

	for _, w := range want {
		got, err := ReadBoundedLine(r, 1024)
		if err != nil {
			t.Fatalf("unexpected error reading %q: %v", w, err)
		}

		if string(got) != w {
			t.Errorf("got %q; want %q", got, w)
		}
	}

	// Next call: stream exhausted.
	got, err := ReadBoundedLine(r, 1024)
	if err != io.EOF {
		t.Fatalf("got err=%v after last line; want io.EOF", err)
	}

	if got != nil {
		t.Errorf("got non-nil content after last line: %q", got)
	}
}

// TestReadBoundedLine_CRLFStripping verifies that CRLF line endings are
// normalised to LF (the trailing '\r' is stripped from the content).
func TestReadBoundedLine_CRLFStripping(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("hello\r\n"))

	got, err := ReadBoundedLine(r, 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(got) != "hello" {
		t.Errorf("got %q; want %q (CRLF not stripped)", got, "hello")
	}
}

// TestReadBoundedLine_ExactlyOneBeyondLimit verifies that a line with exactly
// one byte beyond maxBytes triggers ErrLineTooLong and proper drain.
func TestReadBoundedLine_ExactlyOneBeyondLimit(t *testing.T) {
	const maxBytes = 10
	line := strings.Repeat("a", maxBytes+1) + "\n"
	next := "ok\n"

	r := bufio.NewReader(strings.NewReader(line + next))

	got, err := ReadBoundedLine(r, maxBytes)
	if err != ErrLineTooLong {
		t.Fatalf("got err=%v; want ErrLineTooLong", err)
	}

	if got != nil {
		t.Errorf("got non-nil content with ErrLineTooLong: %q", got)
	}

	// Drain must have consumed exactly the oversized line, leaving "ok".
	got2, err2 := ReadBoundedLine(r, maxBytes)
	if err2 != nil {
		t.Fatalf("second call: unexpected error: %v", err2)
	}

	if string(got2) != "ok" {
		t.Errorf("second call: got %q; want %q", got2, "ok")
	}
}

// TestDrainOversizeLine verifies that drainOversizeLine consumes bytes up to
// and including the next '\n', leaving subsequent content intact.
func TestDrainOversizeLine(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("garbage bytes\nnext line"))

	if err := drainOversizeLine(r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// After drain the reader should be at "next line" (no newline at end → EOF partial).
	got, err := ReadBoundedLine(r, 1024)
	if err != io.EOF {
		t.Fatalf("got err=%v; want io.EOF (partial line)", err)
	}

	if string(got) != "next line" {
		t.Errorf("got %q; want %q", got, "next line")
	}
}

// TestDrainOversizeLine_EOF verifies that drainOversizeLine returns io.EOF
// when the stream ends before a newline, without panicking.
func TestDrainOversizeLine_EOF(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("no newline here"))

	err := drainOversizeLine(r)
	if err != io.EOF {
		t.Fatalf("got err=%v; want io.EOF", err)
	}
}
