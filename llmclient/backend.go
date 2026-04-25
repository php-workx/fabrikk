package llmclient

import "context"

// Backend is the unified streaming interface shared by llmclient (HTTP) and
// llmcli (subprocess) backends. Both backends emit the same Event stream so
// callers can swap them at runtime without changing event-handling code.
type Backend interface {
	// Stream processes a single request and returns a channel of events.
	//
	// For per-call backends (Claude -p, Codex exec, omp print), each call
	// spawns a fresh subprocess. For persistent backends (omp RPC, Codex
	// app-server, OpenCode serve), it reuses the existing process.
	//
	// Stream returns immediately after spawning (per-call) or after sending
	// the request (persistent). Events arrive on the channel as they are
	// produced. The channel is closed once a terminal event (done or error)
	// has been delivered.
	Stream(ctx context.Context, input *Context, opts ...Option) (<-chan Event, error)

	// Name returns a stable identifier for this backend, e.g. "claude",
	// "codex", "codex-appserver", "opencode-run", "opencode-serve", "omp",
	// "omp-rpc".
	Name() string

	// Available reports whether the backend can accept requests right now.
	// For per-call backends, this checks that the binary is on PATH. For
	// persistent backends, this also verifies that the subprocess is alive.
	Available() bool

	// Close releases all resources held by the backend.
	// For persistent backends (omp RPC, Codex app-server, OpenCode serve),
	// this terminates the subprocess. For per-call backends, this is a
	// no-op. Safe to call multiple times.
	Close() error
}
