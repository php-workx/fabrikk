package llmcli

import (
	"errors"
	"fmt"
)

// ErrCliNotFound is the sentinel error for a CLI binary that is not on PATH.
// Use [errors.Is] to match typed [CliNotFoundError] values.
var ErrCliNotFound = errors.New("llmcli: CLI binary not found on PATH")

// ErrNoBackendAvailable is returned by [SelectBackend] when no installed CLI
// meets the caller's [Requirements].
var ErrNoBackendAvailable = errors.New("llmcli: no backend available that meets requirements")

// ErrUnknownBackend is the sentinel error for an unrecognised backend name.
// Use [errors.Is] to match typed [BackendNotFoundError] values.
var ErrUnknownBackend = errors.New("llmcli: unknown backend name")

// CliNotFoundError is a typed error that carries the name of the binary that
// was not found on PATH. It compares equal to [ErrCliNotFound] via errors.Is.
type CliNotFoundError struct {
	// Binary is the binary name that was looked up, e.g. "claude".
	Binary string
}

func (e *CliNotFoundError) Error() string {
	return fmt.Sprintf("llmcli: CLI binary %q not found on PATH", e.Binary)
}

// Is implements the errors.Is target-matching convention so that
// errors.Is(err, ErrCliNotFound) returns true for any *CliNotFoundError.
func (e *CliNotFoundError) Is(target error) bool {
	return target == ErrCliNotFound
}

// BackendNotFoundError is a typed error returned by [SelectBackendByName] when
// the named backend is not registered or its CLI binary is not on PATH. It
// compares equal to [ErrUnknownBackend] via errors.Is.
type BackendNotFoundError struct {
	// Name is the backend name that was requested, e.g. "codex-exec".
	Name string
}

func (e *BackendNotFoundError) Error() string {
	return fmt.Sprintf("llmcli: backend %q not found (not registered or CLI not on PATH)", e.Name)
}

// Is implements the errors.Is target-matching convention so that
// errors.Is(err, ErrUnknownBackend) returns true for any *BackendNotFoundError.
func (e *BackendNotFoundError) Is(target error) bool {
	return target == ErrUnknownBackend
}
