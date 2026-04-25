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

	for {
		fragment, err := r.ReadSlice('\n')
		content := fragment
		if err == nil && len(content) > 0 {
			content = content[:len(content)-1]
		}

		if len(buf)+len(content) > maxBytes {
			if errors.Is(err, bufio.ErrBufferFull) {
				if drainErr := drainOversizeLine(r); drainErr != nil && !errors.Is(drainErr, io.EOF) {
					return nil, drainErr
				}
			}
			return nil, ErrLineTooLong
		}

		buf = append(buf, content...)

		switch {
		case err == nil:
			if len(buf) > 0 && buf[len(buf)-1] == '\r' {
				buf = buf[:len(buf)-1]
			}
			return buf, nil
		case errors.Is(err, io.EOF):
			if len(buf) == 0 {
				return nil, io.EOF
			}
			return buf, io.EOF
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return nil, err
		}
	}
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
