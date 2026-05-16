// Package internal provides low-level parsing primitives shared by llmcli
// backends. It is importable only within the llmcli module tree.
package internal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// ReadLine reads one newline-terminated JSON line from r and returns the raw
// bytes ready for json.Unmarshal. It is a thin wrapper over ReadBoundedLine
// that enforces maxBytes and reuses the ErrLineTooLong sentinel from jsonl.go.
//
// Behaviour:
//   - Normal line (≤ maxBytes, ends with '\n'): returns content and nil.
//   - Partial line at EOF (no trailing '\n'): returns partial content and io.EOF.
//   - Line longer than maxBytes: returns (nil, ErrLineTooLong).
//   - Empty reader: returns (nil, io.EOF).
func ReadLine(r *bufio.Reader, maxBytes int) ([]byte, error) {
	return ReadBoundedLine(r, maxBytes)
}

// WriteLine marshals v to JSON and writes the resulting bytes followed by a
// single newline to w. The write is not atomic; a partial write leaves w in an
// undefined state and the caller should close/restart the connection.
func WriteLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("llmcli/internal/ndjson: marshal: %w", err)
	}
	b = append(b, '\n')
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("llmcli/internal/ndjson: write: %w", err)
	}
	return nil
}
