package llmclient

import (
	"context"
	"fmt"
	"strings"
)

// EventErrorError is returned by Collect when the stream emits EventError.
type EventErrorError struct {
	Type    string
	Message string
}

func (e *EventErrorError) Error() string {
	if e == nil {
		return ""
	}
	if e.Type == "" {
		return e.Message
	}
	if e.Message == "" {
		return e.Type
	}
	return e.Type + ": " + e.Message
}

// Collect drains ch until EventDone, returning the concatenated assistant text
// and final stop reason. Tool calls and thinking blocks are ignored.
func Collect(ctx context.Context, ch <-chan Event) (text string, reason StopReason, err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var sb strings.Builder
	sawDelta := false
	for {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case ev, ok := <-ch:
			if !ok {
				return sb.String(), "", fmt.Errorf("%s: stream closed before done", ErrTypeInternal)
			}
			switch ev.Type {
			case EventTextDelta:
				sawDelta = true
				sb.WriteString(ev.Delta)
			case EventTextEnd:
				if !sawDelta {
					sb.Reset()
					sb.WriteString(ev.Content)
				}
			case EventError:
				errType := ev.ErrorType
				if errType == "" {
					errType = ErrTypeInternal
				}
				return sb.String(), "", &EventErrorError{Type: errType, Message: ev.ErrorMessage}
			case EventDone:
				return sb.String(), ev.Reason, nil
			}
		}
	}
}

// CollectStructuredResult is the structured response from CollectStructured.
type CollectStructuredResult struct {
	Message   AssistantMessage `json:"message"`
	Usage     *Usage           `json:"usage,omitempty"`
	Cost      *Cost            `json:"cost,omitempty"`
	SessionID string           `json:"session_id,omitempty"`
}

// CollectStructured drains ch until EventDone, collecting the complete
// AssistantMessage (including tool calls and thinking blocks), usage metadata,
// cost (when the backend reports it), and session ID.
func CollectStructured(ctx context.Context, ch <-chan Event) (*CollectStructuredResult, error) { //nolint:gocognit // collect function inherently handles many event types; extracting sub-handlers would add more lines without reducing real complexity.
	if ctx == nil {
		ctx = context.Background()
	}

	var sessionID string

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case ev, ok := <-ch:
			if !ok {
				return nil, fmt.Errorf("%s: stream closed before done", ErrTypeInternal)
			}
			switch ev.Type {
			case EventStart:
				if ev.SessionID != "" {
					sessionID = ev.SessionID
				}
			case EventError:
				errType := ev.ErrorType
				if errType == "" {
					errType = ErrTypeInternal
				}
				return nil, &EventErrorError{Type: errType, Message: ev.ErrorMessage}
			case EventDone:
				result := &CollectStructuredResult{
					SessionID: sessionID,
				}
				if ev.Message != nil {
					result.Message = *ev.Message
				}
				if ev.Usage != nil {
					result.Usage = ev.Usage
				}
				if ev.Message != nil && ev.Message.Cost != nil {
					result.Cost = ev.Message.Cost
				}
				return result, nil
			}
		}
	}
}
