// Package llmcli provides a streaming LLM backend that shells out to installed
// AI coding CLI tools (claude, codex, opencode, omp) and emits a normalized
// event stream compatible with the llmclient.Backend interface.
//
// Each CLI tool is represented by a concrete backend type that wraps a
// subprocess. Backends are selected at runtime via [SelectBackend] or
// [SelectBackendByName] based on availability and caller requirements.
package llmcli

import (
	"os"
)

// CliBackend is the embedded base struct for per-call CLI backends. It holds
// the common detected-CLI metadata and provides default implementations of
// [llmclient.Backend] methods that concrete backends can promote or override.
//
// Concrete backends embed CliBackend and supply their own Stream method; they
// must not call Stream on CliBackend directly.
type CliBackend struct {
	// name is the stable backend identifier registered in the factory, e.g.
	// "claude", "codex-exec", "codex-appserver". This may differ from
	// CliInfo.Name when multiple backends share the same binary.
	name string

	// info holds the resolved path and version discovered during detection.
	info CliInfo
}

// NewCliBackend constructs a CliBackend with the given stable backend name and
// the CliInfo discovered for its binary. Concrete backend implementations call
// this from their New constructor passed to [registerBackendFactory].
func NewCliBackend(name string, info CliInfo) CliBackend {
	return CliBackend{name: name, info: info}
}

// Name returns the stable backend identifier, e.g. "claude" or "codex-exec".
func (b *CliBackend) Name() string {
	return b.name
}

func (b *CliBackend) binaryAvailable() bool {
	if b.info.Path == "" {
		return false
	}
	_, err := os.Stat(b.info.Path)
	return err == nil
}

// Available reports whether the resolved binary still exists on disk.
// For per-call backends this is a lightweight stat check; it does not verify
// auth state, version compatibility, or subprocess liveness.
func (b *CliBackend) Available() bool {
	return observeAvailability(b.name, b.binaryAvailable())
}

// Close is a no-op for per-call backends that spawn a fresh subprocess per
// request. Persistent backends override this to terminate the subprocess.
func (b *CliBackend) Close() error {
	return nil
}

// Info returns the detected CLI info associated with this backend.
func (b *CliBackend) Info() CliInfo {
	return b.info
}
