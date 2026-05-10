package bridge

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// Request is one JSON-lines request frame.
type Request struct {
	RequestID string          `json:"request_id"`
	Op        string          `json:"op,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Context   json.RawMessage `json:"context,omitempty"`
	Cancel    bool            `json:"cancel,omitempty"`
}

// Response is one JSON-lines response frame.
type Response struct {
	RequestID string          `json:"request_id"`
	Final     bool            `json:"final"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *WireError      `json:"error,omitempty"`
}

// WireError is the op-agnostic error shape returned on stderr-safe stdout.
type WireError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Handler processes one operation.
type Handler interface {
	Op() string
	Handle(ctx context.Context, req Request, deps Deps) (json.RawMessage, error)
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc struct {
	Operation string
	Fn        func(context.Context, Request, Deps) (json.RawMessage, error)
}

// Op returns the operation name handled by h.
func (h HandlerFunc) Op() string { return h.Operation }

// Handle calls h.Fn.
func (h HandlerFunc) Handle(ctx context.Context, req Request, deps Deps) (json.RawMessage, error) {
	return h.Fn(ctx, req, deps)
}

// Deps are generic dependencies exposed to registered handlers.
type Deps struct {
	Backend llmclient.Backend
}

// Config controls bridge runtime behavior.
type Config struct {
	In              io.Reader
	Out             io.Writer
	Backend         llmclient.Backend
	BackendNames    []string
	LockfilePath    string
	DisableLockfile bool
	ShutdownTimeout time.Duration
}
