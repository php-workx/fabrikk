// Package internal provides low-level parsing primitives shared by llmcli
// backends. It is importable only within the llmcli module tree.
package internal

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Sentinel errors returned by ReadFrame. Callers should use errors.Is to match
// them; each error carries the specific violation as its message.
var (
	// ErrMissingContentLength is returned when the header block ends without a
	// Content-Length header.
	ErrMissingContentLength = errors.New("llmcli/internal/jsonrpc: missing Content-Length header")

	// ErrDuplicateContentLength is returned when more than one Content-Length
	// header appears in a single header block.
	ErrDuplicateContentLength = errors.New("llmcli/internal/jsonrpc: duplicate Content-Length header")

	// ErrInvalidContentLength is returned when the Content-Length value is
	// non-numeric, negative, or zero.
	ErrInvalidContentLength = errors.New("llmcli/internal/jsonrpc: invalid Content-Length value")

	// ErrOversizedBody is returned when Content-Length exceeds the caller's
	// maxBodyBytes limit.
	ErrOversizedBody = errors.New("llmcli/internal/jsonrpc: Content-Length exceeds maximum allowed body size")

	// ErrHeaderTooLarge is returned when the header section consumes more than
	// maxHeaderBytes bytes before the blank separator line is reached.
	ErrHeaderTooLarge = errors.New("llmcli/internal/jsonrpc: header section exceeds maximum allowed size")
)

// ReadFrame reads one LSP-style framed message from r:
//
//	Content-Length: <n>\r\n
//	\r\n
//	<n bytes of body>
//
// maxHeaderBytes limits the total bytes that may be consumed while reading the
// header block (before the blank line). maxBodyBytes limits the Content-Length
// value that may be advertised. Both limits guard against unbounded memory
// allocation from a corrupt or malicious peer.
//
// ReadFrame rejects and returns a typed error for each of the following
// Content-Length violations: missing, duplicate, non-numeric, negative, zero,
// and oversized.
func ReadFrame(r *bufio.Reader, maxHeaderBytes, maxBodyBytes int) ([]byte, error) {
	contentLength := -1
	headerBytesRead := 0

	for {
		line, err := readFrameHeaderLine(r, &headerBytesRead, maxHeaderBytes)
		if err != nil {
			return nil, err
		}

		// Strip trailing CRLF or bare LF.
		stripped := strings.TrimRight(line, "\r\n")

		if stripped == "" {
			// Blank line: end of header block.
			break
		}

		if key, value, ok := strings.Cut(stripped, ":"); ok && strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			if contentLength != -1 {
				return nil, ErrDuplicateContentLength
			}

			valStr := strings.TrimSpace(value)
			n, parseErr := strconv.Atoi(valStr)
			if parseErr != nil {
				return nil, fmt.Errorf("%w: %q", ErrInvalidContentLength, stripped)
			}

			if n <= 0 {
				return nil, fmt.Errorf("%w: %q", ErrInvalidContentLength, stripped)
			}

			if n > maxBodyBytes {
				return nil, fmt.Errorf("%w: %d > %d", ErrOversizedBody, n, maxBodyBytes)
			}

			contentLength = n
		}
		// Other header lines (e.g. Content-Type) are accepted but ignored.
	}

	if contentLength == -1 {
		return nil, ErrMissingContentLength
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}

	return body, nil
}

func readFrameHeaderLine(r *bufio.Reader, headerBytesRead *int, maxHeaderBytes int) (string, error) {
	var line strings.Builder
	for {
		fragment, err := r.ReadSlice('\n')
		*headerBytesRead += len(fragment)
		if *headerBytesRead > maxHeaderBytes {
			return "", ErrHeaderTooLarge
		}
		if len(fragment) > 0 {
			line.Write(fragment)
		}
		if err == nil {
			return line.String(), nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return "", err
	}
}

// jsonrpcRequest is the wire format for an outbound JSON-RPC 2.0 request.
type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// WriteRequest encodes a JSON-RPC 2.0 request and writes it to w using
// LSP-style Content-Length framing. id may be any JSON-serializable value
// (int, string, nil); params may be nil, a map, or any struct.
//
// The write is not atomic: a partial write on error leaves w in an undefined
// state. Callers using a persistent transport should close and restart the
// connection when WriteRequest returns a non-nil error.
func WriteRequest(w io.Writer, id any, method string, params any) error {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("llmcli/internal/jsonrpc: marshal request: %w", err)
	}

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))

	if _, err := io.WriteString(w, header); err != nil {
		return fmt.Errorf("llmcli/internal/jsonrpc: write header: %w", err)
	}

	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("llmcli/internal/jsonrpc: write body: %w", err)
	}

	return nil
}
