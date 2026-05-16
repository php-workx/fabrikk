package llmclient

import "context"

// ReadyState classifies whether a backend is usable for a Stream call.
type ReadyState int

const (
	// ReadyOK means the backend is installed and ready to accept Stream calls.
	ReadyOK ReadyState = iota
	// ReadyMissingBinary means the backend CLI binary is missing or gone.
	ReadyMissingBinary
	// ReadyNotAuthed means the binary exists but authentication was not found.
	ReadyNotAuthed
	// ReadyUnknown means readiness could not be determined conclusively.
	ReadyUnknown
)

// String returns the stable wire/debug value for a ReadyState.
func (s ReadyState) String() string {
	switch s {
	case ReadyOK:
		return "ok"
	case ReadyMissingBinary:
		return "missing_binary"
	case ReadyNotAuthed:
		return "not_authed"
	case ReadyUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// ReadyReport describes the result of a lightweight backend readiness probe.
type ReadyReport struct {
	State  ReadyState
	Detail string
}

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

	// Ready performs a lightweight probe and reports why the backend is or is
	// not usable. Implementations should avoid expensive model calls here.
	Ready(ctx context.Context) ReadyReport

	// Close releases all resources held by the backend.
	// For persistent backends (omp RPC, Codex app-server, OpenCode serve),
	// this terminates the subprocess. For per-call backends, this is a
	// no-op. Safe to call multiple times.
	Close() error
}
