// Package internal provides low-level parsing primitives shared by llmcli
// backends. It is importable only within the llmcli module tree.
package internal

import (
	"bufio"
	"errors"
	"io"
)

// ErrLineTooLong is returned by ReadBoundedLine when a line exceeds the
// caller-specified maximum byte count. After returning ErrLineTooLong the
// reader is positioned at the beginning of the next line — the oversize bytes
// have been drained by drainOversizeLine.
var ErrLineTooLong = errors.New("llmcli/internal/jsonl: line exceeds maximum allowed length")

// ReadBoundedLine reads a single '\n'-terminated line from r and returns its
// content without the trailing newline (CRLF endings are also handled). It
// enforces a hard upper bound of maxBytes on the line content.
//
// Behaviour by case:
//   - Normal line (≤ maxBytes, ends with '\n'): returns content and nil.
//   - Partial line at EOF (no trailing '\n'): returns partial content and io.EOF.
//   - Line longer than maxBytes: drains remaining line bytes so the reader
//     is positioned at the next line, then returns (nil, ErrLineTooLong).
//   - Empty reader: returns (nil, io.EOF).
//
// Parser paths must use ReadBoundedLine instead of bufio.Scanner to avoid
// the fixed-1 MB cap and to support deterministic drain-on-oversize behaviour.
func ReadBoundedLine(r *bufio.Reader, maxBytes int) ([]byte, error) {
	// Preallocate conservatively; grows on demand up to maxBytes.
	buf := make([]byte, 0, min(maxBytes, 4096))

	for len(buf) < maxBytes {
		b, err := r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(buf) > 0 {
					// Partial line at EOF: no terminating newline.
					return buf, io.EOF
				}

				return nil, io.EOF
			}

			return nil, err
		}

		if b == '\n' {
			// Strip a trailing '\r' to handle CRLF line endings.
			if len(buf) > 0 && buf[len(buf)-1] == '\r' {
				buf = buf[:len(buf)-1]
			}

			return buf, nil
		}

		buf = append(buf, b)
	}

	// We have consumed exactly maxBytes without seeing '\n'. Peek at the next
	// byte to determine whether the line is exactly maxBytes long (terminates
	// immediately) or exceeds the limit (needs drain).
	b, err := r.ReadByte()
	if err != nil {
		if errors.Is(err, io.EOF) {
			// maxBytes then EOF with no newline: partial line within limit.
			return buf, io.EOF
		}

		return nil, err
	}

	if b == '\n' {
		// Line content is exactly maxBytes; the newline is the (maxBytes+1)th byte.
		if len(buf) > 0 && buf[len(buf)-1] == '\r' {
			buf = buf[:len(buf)-1]
		}

		return buf, nil
	}

	// Line exceeds maxBytes. The byte we just read is part of the overrun;
	// drain any remaining bytes so the next call starts at the next line.
	if err := drainOversizeLine(r); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	return nil, ErrLineTooLong
}

// drainOversizeLine reads and discards bytes from r until a '\n' is found or
// EOF is reached. It is called by ReadBoundedLine after the per-line length
// limit has been exceeded, restoring the reader to a known state at the start
// of the next line.
func drainOversizeLine(r *bufio.Reader) error {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return err // includes io.EOF
		}

		if b == '\n' {
			return nil
		}
	}
}
