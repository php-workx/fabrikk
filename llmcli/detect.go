package llmcli

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"time"
)

// CliInfo describes an AI coding CLI tool that was detected on the system.
type CliInfo struct {
	// Name is the logical backend name, e.g. "claude", "codex", "opencode", "omp".
	Name string

	// Binary is the binary name passed to exec.LookPath, e.g. "claude".
	Binary string

	// Path is the resolved absolute path returned by exec.LookPath.
	Path string

	// Version is the trimmed output of `<binary> --version`, or empty if
	// the probe failed or timed out.
	Version string
}

// knownCLIs is the ordered list of AI coding CLI tools to detect, in the
// canonical priority order used by [DetectAvailable].
var knownCLIs = []struct{ name, binary string }{
	{"claude", "claude"},
	{"codex", "codex"},
	{"opencode", "opencode"},
	{"omp", "omp"},
}

// DetectAvailable scans for known AI coding CLI tools on PATH and returns the
// ones that are available, in the canonical priority order (claude, codex,
// opencode, omp). Version probing is attempted for each found binary using a
// per-binary 5-second deadline.
//
// DetectAvailable never returns an error. CLIs that are not found are omitted.
// If a version probe fails or times out, the CLI is still returned with
// CliInfo.Version set to the empty string.
func DetectAvailable() []CliInfo {
	return DetectAvailableContext(context.Background())
}

// DetectAvailableContext scans for known AI coding CLI tools on PATH and
// returns the ones that are available, in the canonical priority order. Version
// probes inherit ctx and also use a per-binary 5-second deadline.
func DetectAvailableContext(ctx context.Context) []CliInfo {
	if ctx == nil {
		ctx = context.Background()
	}
	var out []CliInfo
	for _, c := range knownCLIs {
		if ctx.Err() != nil {
			return out
		}
		path, err := exec.LookPath(c.binary)
		if err != nil {
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		version := probeVersion(probeCtx, path)
		cancel()
		out = append(out, CliInfo{
			Name:    c.name,
			Binary:  c.binary,
			Path:    path,
			Version: version,
		})
	}
	return out
}

// probeTimeout is the per-binary timeout for version probing.
const probeTimeout = 5 * time.Second

// probeVersion runs `path --version` and returns the trimmed output.
// Returns an empty string if the command fails, produces no output, or the
// context deadline is exceeded.
func probeVersion(ctx context.Context, path string) string {
	output, err := probeCommand(ctx, path, "--version")
	if err != nil {
		return ""
	}
	return output
}

// probeCommand executes path with the given args, captures stdout, and
// returns the trimmed output. Stderr is discarded. Returns an error if the
// command exits non-zero or the context is cancelled.
func probeCommand(ctx context.Context, path string, args ...string) (string, error) {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // path is from exec.LookPath or trusted config, not user input
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}
