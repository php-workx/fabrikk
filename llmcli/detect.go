package llmcli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
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

	// VersionWarning is non-empty when the detected version is below the
	// minimum known-good version for this CLI. Empty when no minimum is defined
	// or when the version probe produced no output.
	VersionWarning string
}

// minVersions maps CLI name → minimum version string. Versions below this
// threshold cause DetectAvailableContext to populate CliInfo.VersionWarning.
var minVersions = map[string]string{
	"claude":   "1.0.0",
	"codex":    "0.1.0",
	"opencode": "0.3.0",
	"omp":      "14.0.0",
}

// versionWarning returns a human-readable warning when version is below the
// minimum for name, or empty string if no warning is warranted.
func versionWarning(name, version string) string {
	minVer, ok := minVersions[name]
	if !ok || version == "" {
		return ""
	}
	if compareSemver(version, minVer) < 0 {
		return fmt.Sprintf("%s version %s below minimum %s", name, version, minVer)
	}
	return ""
}

// compareSemver compares two semantic version strings. It strips pre-release
// and build-metadata suffixes (anything after '-' or '+'), splits on '.',
// and compares up to 3 numeric parts. Returns -1 if a < b, 0 if equal, 1 if
// a > b. Non-numeric or unparseable segments are treated as 0.
func compareSemver(a, b string) int {
	normalize := func(v string) string {
		if i := strings.IndexAny(v, "-+"); i >= 0 {
			v = v[:i]
		}
		return v
	}
	parseParts := func(v string) [3]int {
		parts := strings.SplitN(normalize(v), ".", 4)
		var out [3]int
		for i := range out {
			if i < len(parts) {
				n, err := strconv.Atoi(parts[i])
				if err != nil {
					return [3]int{}
				}
				out[i] = n
			}
		}
		return out
	}
	ap, bp := parseParts(a), parseParts(b)
	for i := range ap {
		if ap[i] < bp[i] {
			return -1
		}
		if ap[i] > bp[i] {
			return 1
		}
	}
	return 0
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
			Name:           c.name,
			Binary:         c.binary,
			Path:           path,
			Version:        version,
			VersionWarning: versionWarning(c.name, version),
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
