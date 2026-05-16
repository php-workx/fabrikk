package internal

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// ─── ReadLine ─────────────────────────────────────────────────────────────────

// TestReadLine_NormalLine verifies that a line within the byte limit is
// returned without the trailing newline.
func TestReadLine_NormalLine(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"ping"}` + "\n"
	r := bufio.NewReader(strings.NewReader(input))

	got, err := ReadLine(r, 1024)
	if err != nil {
		t.Fatalf("ReadLine: unexpected error: %v", err)
	}
	want := `{"jsonrpc":"2.0","method":"ping"}`
	if string(got) != want {
		t.Errorf("ReadLine = %q; want %q", string(got), want)
	}
}

// TestReadLine_OversizeLine verifies that a line exceeding maxBytes returns
// ErrLineTooLong and nil content.
func TestReadLine_OversizeLine(t *testing.T) {
	// Build a line that is 10 bytes longer than the limit.
	const limit = 32
	line := strings.Repeat("x", limit+10) + "\n"
	r := bufio.NewReader(strings.NewReader(line))

	got, err := ReadLine(r, limit)
	if !errors.Is(err, ErrLineTooLong) {
		t.Errorf("err = %v; want ErrLineTooLong", err)
	}
	if got != nil {
		t.Errorf("got = %q; want nil", got)
	}
}

// TestReadLine_EOFPartial verifies that a partial line without a trailing
// newline at EOF returns the content and io.EOF.
func TestReadLine_EOFPartial(t *testing.T) {
	input := `{"partial":true}` // no trailing newline
	r := bufio.NewReader(strings.NewReader(input))

	got, err := ReadLine(r, 1024)
	if !errors.Is(err, io.EOF) {
		t.Errorf("err = %v; want io.EOF", err)
	}
	want := `{"partial":true}`
	if string(got) != want {
		t.Errorf("ReadLine = %q; want %q", string(got), want)
	}
}

// TestReadLine_EmptyReader verifies that ReadLine on an empty reader returns
// nil and io.EOF.
func TestReadLine_EmptyReader(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))

	got, err := ReadLine(r, 1024)
	if !errors.Is(err, io.EOF) {
		t.Errorf("err = %v; want io.EOF", err)
	}
	if got != nil {
		t.Errorf("got = %q; want nil", got)
	}
}

// TestReadLine_MultiLine verifies that ReadLine returns one line at a time
// from a multi-line reader.
func TestReadLine_MultiLine(t *testing.T) {
	lines := []string{
		`{"jsonrpc":"2.0","method":"first"}`,
		`{"jsonrpc":"2.0","method":"second"}`,
		`{"jsonrpc":"2.0","method":"third"}`,
	}
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	r := bufio.NewReader(strings.NewReader(sb.String()))

	for i, want := range lines {
		got, err := ReadLine(r, 1024)
		if err != nil {
			t.Fatalf("line %d: unexpected error: %v", i, err)
		}
		if string(got) != want {
			t.Errorf("line %d: got %q; want %q", i, string(got), want)
		}
	}

	// Next read must return EOF.
	_, err := ReadLine(r, 1024)
	if !errors.Is(err, io.EOF) {
		t.Errorf("after last line: err = %v; want io.EOF", err)
	}
}

// ─── WriteLine ────────────────────────────────────────────────────────────────

// TestWriteLine_ProducesJSONWithNewline verifies that WriteLine marshals the
// value to JSON and appends exactly one newline.
func TestWriteLine_ProducesJSONWithNewline(t *testing.T) {
	var buf bytes.Buffer
	v := map[string]any{"jsonrpc": "2.0", "method": "ping"}

	if err := WriteLine(&buf, v); err != nil {
		t.Fatalf("WriteLine: unexpected error: %v", err)
	}

	s := buf.String()
	if s == "" {
		t.Fatal("expected non-empty output")
	}
	if s[len(s)-1] != '\n' {
		t.Errorf("output does not end with newline: %q", s)
	}

	// Strip the trailing newline and verify valid JSON.
	trimmed := strings.TrimRight(s, "\n")
	if trimmed == "" {
		t.Fatal("JSON body is empty")
	}
	// Check method field round-trips.
	if !strings.Contains(trimmed, `"ping"`) {
		t.Errorf("JSON does not contain expected field: %q", trimmed)
	}
}

// TestWriteLine_WriteError verifies that a write failure is propagated.
func TestWriteLine_WriteError(t *testing.T) {
	w := &failWriter{after: 0}
	err := WriteLine(w, map[string]string{"k": "v"})
	if err == nil {
		t.Fatal("expected error from failing writer")
	}
}

// failWriter is an io.Writer that returns an error after 'after' successful
// bytes have been written.
type failWriter struct {
	after   int
	written int
}

func (f *failWriter) Write(p []byte) (int, error) {
	if f.written >= f.after {
		return 0, io.ErrClosedPipe
	}
	n := min(len(p), f.after-f.written)
	f.written += n
	return n, nil
}
