package internal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// ─── ReadFrame helpers ─────────────────────────────────────────────────────────

// lspFrame builds a valid LSP-framed message from body.
func lspFrame(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

// lspReader returns a bufio.Reader that yields the given raw string.
func lspReader(raw string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(raw))
}

const (
	testMaxHeader = 4096
	testMaxBody   = 8 * 1024 * 1024 // 8 MiB
)

// ─── Criterion 1: valid Content-Length ────────────────────────────────────────

// TestReadFrame_ValidContentLength verifies that a well-formed LSP frame is
// decoded correctly: the body bytes are returned and no error is produced.
func TestReadFrame_ValidContentLength(t *testing.T) {
	const body = `{"jsonrpc":"2.0","id":1,"method":"turn","params":{}}`
	r := lspReader(lspFrame(body))

	got, err := ReadFrame(r, testMaxHeader, testMaxBody)
	if err != nil {
		t.Fatalf("ReadFrame: unexpected error: %v", err)
	}

	if string(got) != body {
		t.Errorf("body = %q; want %q", got, body)
	}
}

// TestReadFrame_ValidContentLength_CRLF_Headers verifies that CRLF-terminated
// headers are handled correctly (the CRLF is stripped before parsing).
func TestReadFrame_ValidContentLength_CRLF_Headers(t *testing.T) {
	const body = `{"ok":true}`
	raw := fmt.Sprintf("Content-Length: %d\r\nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n%s", len(body), body)
	r := lspReader(raw)

	got, err := ReadFrame(r, testMaxHeader, testMaxBody)
	if err != nil {
		t.Fatalf("ReadFrame: unexpected error: %v", err)
	}

	if string(got) != body {
		t.Errorf("body = %q; want %q", got, body)
	}
}

// TestReadFrame_ValidContentLength_MinimalBody verifies that a single-byte body
// is accepted without error.
func TestReadFrame_ValidContentLength_MinimalBody(t *testing.T) {
	r := lspReader(lspFrame("x"))

	got, err := ReadFrame(r, testMaxHeader, testMaxBody)
	if err != nil {
		t.Fatalf("ReadFrame: unexpected error: %v", err)
	}

	if string(got) != "x" {
		t.Errorf("body = %q; want %q", got, "x")
	}
}

// ─── Criterion 1: missing Content-Length ──────────────────────────────────────

// TestReadFrame_RejectsMissingContentLength verifies that a header block with
// no Content-Length header returns ErrMissingContentLength.
func TestReadFrame_RejectsMissingContentLength(t *testing.T) {
	// Header block with only a non-Content-Length header, then blank line.
	raw := "X-Custom-Header: value\r\n\r\nsome body"
	r := lspReader(raw)

	_, err := ReadFrame(r, testMaxHeader, testMaxBody)
	if !errors.Is(err, ErrMissingContentLength) {
		t.Fatalf("ReadFrame err = %v; want ErrMissingContentLength", err)
	}
}

// TestReadFrame_RejectsMissingContentLength_EmptyHeaders verifies that an
// immediately blank header section (no headers at all) returns
// ErrMissingContentLength.
func TestReadFrame_RejectsMissingContentLength_EmptyHeaders(t *testing.T) {
	// Blank line immediately → no Content-Length.
	raw := "\r\nsome body"
	r := lspReader(raw)

	_, err := ReadFrame(r, testMaxHeader, testMaxBody)
	if !errors.Is(err, ErrMissingContentLength) {
		t.Fatalf("ReadFrame err = %v; want ErrMissingContentLength", err)
	}
}

// ─── Criterion 1: duplicate Content-Length ────────────────────────────────────

// TestReadFrame_RejectsDuplicateContentLength verifies that two Content-Length
// headers in the same block return ErrDuplicateContentLength.
func TestReadFrame_RejectsDuplicateContentLength(t *testing.T) {
	raw := "Content-Length: 5\r\nContent-Length: 5\r\n\r\nhello"
	r := lspReader(raw)

	_, err := ReadFrame(r, testMaxHeader, testMaxBody)
	if !errors.Is(err, ErrDuplicateContentLength) {
		t.Fatalf("ReadFrame err = %v; want ErrDuplicateContentLength", err)
	}
}

// TestReadFrame_RejectsDuplicateContentLength_DifferentValues verifies that
// two Content-Length headers with different values also return
// ErrDuplicateContentLength (not ErrInvalidContentLength).
func TestReadFrame_RejectsDuplicateContentLength_DifferentValues(t *testing.T) {
	raw := "Content-Length: 3\r\nContent-Length: 7\r\n\r\nabc"
	r := lspReader(raw)

	_, err := ReadFrame(r, testMaxHeader, testMaxBody)
	if !errors.Is(err, ErrDuplicateContentLength) {
		t.Fatalf("ReadFrame err = %v; want ErrDuplicateContentLength", err)
	}
}

// ─── Criterion 1: invalid (non-numeric) Content-Length ────────────────────────

// TestReadFrame_RejectsInvalidContentLength_NaN verifies that a non-numeric
// Content-Length value returns ErrInvalidContentLength.
func TestReadFrame_RejectsInvalidContentLength(t *testing.T) {
	cases := []struct {
		name string
		val  string
	}{
		{"alpha", "abc"},
		{"hex", "0x10"},
		{"float", "1.5"},
		{"empty", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := fmt.Sprintf("Content-Length: %s\r\n\r\nbody", tc.val)
			r := lspReader(raw)

			_, err := ReadFrame(r, testMaxHeader, testMaxBody)
			if !errors.Is(err, ErrInvalidContentLength) {
				t.Fatalf("ReadFrame (val=%q) err = %v; want ErrInvalidContentLength", tc.val, err)
			}
		})
	}
}

func TestReadFrame_ContentLengthCaseInsensitive(t *testing.T) {
	const body = `{"ok":true}`
	raw := fmt.Sprintf("content-length: %d\r\n\r\n%s", len(body), body)

	got, err := ReadFrame(lspReader(raw), testMaxHeader, testMaxBody)
	if err != nil {
		t.Fatalf("ReadFrame: unexpected error: %v", err)
	}
	if string(got) != body {
		t.Errorf("body = %q; want %q", got, body)
	}
}

func TestReadFrame_ContentLengthAllowsWhitespace(t *testing.T) {
	const body = `{"ok":true}`
	raw := fmt.Sprintf("CONTENT-LENGTH:  %d \r\n\r\n%s", len(body), body)

	got, err := ReadFrame(lspReader(raw), testMaxHeader, testMaxBody)
	if err != nil {
		t.Fatalf("ReadFrame: unexpected error: %v", err)
	}
	if string(got) != body {
		t.Errorf("body = %q; want %q", got, body)
	}
}

// TestReadFrame_RejectsNegativeContentLength verifies that a negative
// Content-Length value returns ErrInvalidContentLength.
func TestReadFrame_RejectsNegativeContentLength_Negative(t *testing.T) {
	raw := "Content-Length: -1\r\n\r\n"
	r := lspReader(raw)

	_, err := ReadFrame(r, testMaxHeader, testMaxBody)
	if !errors.Is(err, ErrInvalidContentLength) {
		t.Fatalf("ReadFrame err = %v; want ErrInvalidContentLength", err)
	}
}

// TestReadFrame_RejectsZeroContentLength verifies that Content-Length: 0
// returns ErrInvalidContentLength (zero-length bodies are not valid).
func TestReadFrame_RejectsZeroContentLength(t *testing.T) {
	raw := "Content-Length: 0\r\n\r\n"
	r := lspReader(raw)

	_, err := ReadFrame(r, testMaxHeader, testMaxBody)
	if !errors.Is(err, ErrInvalidContentLength) {
		t.Fatalf("ReadFrame err = %v; want ErrInvalidContentLength", err)
	}
}

// ─── Criterion 1: oversized body ──────────────────────────────────────────────

// TestReadFrame_RejectsOversizedBody verifies that a Content-Length value
// exceeding maxBodyBytes returns ErrOversizedBody without allocating the body.
func TestReadFrame_RejectsOversizedBody(t *testing.T) {
	const limit = 1024
	raw := fmt.Sprintf("Content-Length: %d\r\n\r\n", limit+1)
	r := lspReader(raw)

	_, err := ReadFrame(r, testMaxHeader, limit)
	if !errors.Is(err, ErrOversizedBody) {
		t.Fatalf("ReadFrame err = %v; want ErrOversizedBody", err)
	}
}

// TestReadFrame_AcceptsBodyAtExactLimit verifies that
// Content-Length == maxBodyBytes is accepted (boundary value).
func TestReadFrame_AcceptsBodyAtExactLimit(t *testing.T) {
	const limit = 10
	body := strings.Repeat("x", limit)
	r := lspReader(lspFrame(body))

	got, err := ReadFrame(r, testMaxHeader, limit)
	if err != nil {
		t.Fatalf("ReadFrame at limit: unexpected error: %v", err)
	}

	if len(got) != limit {
		t.Errorf("body length = %d; want %d", len(got), limit)
	}
}

// TestReadFrame_RejectsOversizedBody_OneBeyondLimit verifies that
// Content-Length == maxBodyBytes+1 is rejected.
func TestReadFrame_RejectsOversizedBody_OneBeyondLimit(t *testing.T) {
	const limit = 10
	raw := fmt.Sprintf("Content-Length: %d\r\n\r\n", limit+1)
	r := lspReader(raw)

	_, err := ReadFrame(r, testMaxHeader, limit)
	if !errors.Is(err, ErrOversizedBody) {
		t.Fatalf("ReadFrame one-beyond-limit err = %v; want ErrOversizedBody", err)
	}
}

// ─── Header size limit ────────────────────────────────────────────────────────

// TestReadFrame_RejectsOversizedHeader verifies that a header section exceeding
// maxHeaderBytes returns ErrHeaderTooLarge.
func TestReadFrame_RejectsOversizedHeader(t *testing.T) {
	// 10-byte limit; a single header line already exceeds it.
	const limit = 10
	raw := "Content-Length: 5\r\n\r\nhello"
	r := lspReader(raw)

	_, err := ReadFrame(r, limit, testMaxBody)
	if !errors.Is(err, ErrHeaderTooLarge) {
		t.Fatalf("ReadFrame err = %v; want ErrHeaderTooLarge", err)
	}
}

func TestReadFrame_RejectsOversizedHeaderLineIncrementally(t *testing.T) {
	const limit = 64
	raw := strings.Repeat("X", 1024) + "\n"
	r := bufio.NewReaderSize(strings.NewReader(raw), 16)

	_, err := ReadFrame(r, limit, testMaxBody)
	if !errors.Is(err, ErrHeaderTooLarge) {
		t.Fatalf("ReadFrame err = %v; want ErrHeaderTooLarge", err)
	}
}

// ─── Multi-frame sequential reads ────────────────────────────────────────────

// TestReadFrame_SequentialFrames verifies that ReadFrame can be called multiple
// times on the same bufio.Reader to consume a sequence of frames.
func TestReadFrame_SequentialFrames(t *testing.T) {
	body1 := `{"id":1}`
	body2 := `{"id":2}`

	var buf bytes.Buffer
	buf.WriteString(lspFrame(body1))
	buf.WriteString(lspFrame(body2))
	r := bufio.NewReader(&buf)

	got1, err := ReadFrame(r, testMaxHeader, testMaxBody)
	if err != nil {
		t.Fatalf("frame 1: ReadFrame error: %v", err)
	}

	if string(got1) != body1 {
		t.Errorf("frame 1 body = %q; want %q", got1, body1)
	}

	got2, err := ReadFrame(r, testMaxHeader, testMaxBody)
	if err != nil {
		t.Fatalf("frame 2: ReadFrame error: %v", err)
	}

	if string(got2) != body2 {
		t.Errorf("frame 2 body = %q; want %q", got2, body2)
	}

	// Third call: EOF.
	_, err = ReadFrame(r, testMaxHeader, testMaxBody)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("after last frame: err = %v; want io.EOF", err)
	}
}

// ─── WriteRequest ─────────────────────────────────────────────────────────────

// TestWriteRequest_ValidOutput verifies that WriteRequest produces a
// Content-Length-framed JSON-RPC 2.0 request with the expected fields.
func TestWriteRequest_ValidOutput(t *testing.T) {
	type params struct {
		Message string `json:"message"`
	}

	var buf bytes.Buffer
	err := WriteRequest(&buf, 42, "turn", params{Message: "hello"})
	if err != nil {
		t.Fatalf("WriteRequest: unexpected error: %v", err)
	}

	// Parse the output using ReadFrame to verify framing round-trips.
	r := bufio.NewReader(&buf)
	body, err := ReadFrame(r, testMaxHeader, testMaxBody)
	if err != nil {
		t.Fatalf("ReadFrame on WriteRequest output: %v", err)
	}

	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}

	// jsonrpc field must be "2.0"
	if string(req["jsonrpc"]) != `"2.0"` {
		t.Errorf("jsonrpc = %s; want %q", req["jsonrpc"], "2.0")
	}

	// id must be 42
	if string(req["id"]) != "42" {
		t.Errorf("id = %s; want 42", req["id"])
	}

	// method must be "turn"
	if string(req["method"]) != `"turn"` {
		t.Errorf("method = %s; want %q", req["method"], "turn")
	}

	// params.message must be "hello"
	var p params
	if err := json.Unmarshal(req["params"], &p); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}

	if p.Message != "hello" {
		t.Errorf("params.message = %q; want %q", p.Message, "hello")
	}
}

// TestWriteRequest_NilParams verifies that WriteRequest omits the params field
// when params is nil, producing a valid JSON-RPC 2.0 request body.
func TestWriteRequest_NilParams(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteRequest(&buf, 1, "ping", nil); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	r := bufio.NewReader(&buf)
	body, err := ReadFrame(r, testMaxHeader, testMaxBody)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}

	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := req["params"]; ok {
		t.Error("params field should be absent when params is nil")
	}
}

// TestWriteRequest_StringID verifies that WriteRequest handles a string ID
// correctly.
func TestWriteRequest_StringID(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteRequest(&buf, "req-abc", "method", nil); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	r := bufio.NewReader(&buf)
	body, err := ReadFrame(r, testMaxHeader, testMaxBody)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}

	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if string(req["id"]) != `"req-abc"` {
		t.Errorf("id = %s; want %q", req["id"], "req-abc")
	}
}

// TestWriteRequest_RoundTrip verifies that WriteRequest + ReadFrame is a
// lossless round-trip for a realistic turn request payload.
func TestWriteRequest_RoundTrip(t *testing.T) {
	type turnParams struct {
		Message   string `json:"message"`
		SessionID string `json:"session_id,omitempty"`
	}

	input := turnParams{Message: "What is Go?", SessionID: "sess-xyz"}
	var buf bytes.Buffer

	if err := WriteRequest(&buf, 7, "turn", input); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	r := bufio.NewReader(&buf)
	body, err := ReadFrame(r, testMaxHeader, testMaxBody)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}

	var req struct {
		JSONRPC string     `json:"jsonrpc"`
		ID      int        `json:"id"`
		Method  string     `json:"method"`
		Params  turnParams `json:"params"`
	}

	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if req.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q; want %q", req.JSONRPC, "2.0")
	}

	if req.ID != 7 {
		t.Errorf("id = %d; want 7", req.ID)
	}

	if req.Method != "turn" {
		t.Errorf("method = %q; want %q", req.Method, "turn")
	}

	if req.Params.Message != input.Message {
		t.Errorf("params.message = %q; want %q", req.Params.Message, input.Message)
	}

	if req.Params.SessionID != input.SessionID {
		t.Errorf("params.session_id = %q; want %q", req.Params.SessionID, input.SessionID)
	}
}
