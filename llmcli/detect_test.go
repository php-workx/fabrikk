package llmcli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

const windowsOS = "windows"

// fakeCLIDir creates a temporary directory containing symlinks named after
// the given CLI names, all pointing to the current test binary. Returns the
// directory path.
//
// The test binary's TestMain inspects env vars set on the parent process to
// decide what output to produce.
//
// Skips the test on Windows because creating symlinks requires elevated
// privileges there.
func fakeCLIDir(t *testing.T, names ...string) string {
	t.Helper()
	if runtime.GOOS == windowsOS {
		t.Skip("symlink-based CLI fixture tests are not supported on Windows")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	dir := t.TempDir()
	for _, name := range names {
		link := filepath.Join(dir, name)
		if err := os.Symlink(exe, link); err != nil {
			t.Fatalf("symlink %s → %s: %v", exe, link, err)
		}
	}
	return dir
}

// prependPath prepends dir to the PATH environment variable for the duration
// of the test, using t.Setenv for automatic cleanup.
func prependPath(t *testing.T, dir string) {
	t.Helper()
	orig := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+orig)
}

// TestDetectAvailable_FindsExecutableOnPath verifies that DetectAvailable
// returns a CliInfo for each known binary that is present on PATH.
func TestDetectAvailable_FindsExecutableOnPath(t *testing.T) {
	dir := fakeCLIDir(t, "claude", "codex")
	prependPath(t, dir)
	// Make the fake binaries output a version string.
	t.Setenv("FABRIKK_LLMCLI_TEST_VERSION", "1.0.0")

	infos := DetectAvailable()

	found := make(map[string]CliInfo)
	for _, info := range infos {
		found[info.Name] = info
	}

	for _, name := range []string{"claude", "codex"} {
		info, ok := found[name]
		if !ok {
			t.Errorf("DetectAvailable: want %q in result, not found; got %v", name, infos)
			continue
		}
		if info.Path == "" {
			t.Errorf("DetectAvailable: %q has empty Path", name)
		}
		if info.Binary != name {
			t.Errorf("DetectAvailable: %q Binary=%q, want %q", name, info.Binary, name)
		}
		if info.Version != "1.0.0" {
			t.Errorf("DetectAvailable: %q Version=%q, want %q", name, info.Version, "1.0.0")
		}
	}
}

// TestProbeVersion_Timeout verifies that probeVersion returns an empty string
// when the context deadline is exceeded before the subprocess exits.
func TestProbeVersion_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hang-based timeout test is not supported on Windows")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	// Instruct the child process to hang indefinitely.
	t.Setenv("FABRIKK_LLMCLI_TEST_HANG", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	version := probeVersion(ctx, exe)
	elapsed := time.Since(start)

	if version != "" {
		t.Errorf("probeVersion: got %q, want empty string on timeout", version)
	}
	// The timeout is 150 ms; the function must return well within 2 s.
	if elapsed > 2*time.Second {
		t.Errorf("probeVersion: returned after %v, expected < 2s (timeout was 150ms)", elapsed)
	}
}

// TestProbeVersion_TrimsOutput verifies that probeVersion strips leading and
// trailing whitespace (including newlines) from the subprocess output.
func TestProbeVersion_TrimsOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink-based CLI fixture tests are not supported on Windows")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	// The raw output has leading and trailing whitespace; probeVersion must
	// return the trimmed value.
	t.Setenv("FABRIKK_LLMCLI_TEST_VERSION", "  1.2.3  \n")

	ctx := context.Background()
	version := probeVersion(ctx, exe)

	if version != "1.2.3" {
		t.Errorf("probeVersion: got %q, want %q", version, "1.2.3")
	}
}
