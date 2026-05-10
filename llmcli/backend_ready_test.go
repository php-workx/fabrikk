package llmcli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/php-workx/fabrikk/llmclient"
)

func TestCliBackendReadyReportsMissingBinary(t *testing.T) {
	b := NewCliBackend("missing", CliInfo{Binary: "missing", Path: "/does/not/exist"})

	report := b.Ready(context.Background())
	if report.State != llmclient.ReadyMissingBinary {
		t.Fatalf("Ready state = %v, want %v", report.State, llmclient.ReadyMissingBinary)
	}
	if report.Detail == "" {
		t.Fatal("Ready detail is empty")
	}
}

func TestClaudeReadyReportsNotAuthedWithoutKnownMarkers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	b := NewClaudeBackend(CliInfo{Path: readyTestBinaryPath(t)})

	report := b.Ready(context.Background())
	if report.State != llmclient.ReadyNotAuthed {
		t.Fatalf("Ready state = %v, want %v", report.State, llmclient.ReadyNotAuthed)
	}
}

func TestCodexReadyReportsOKWithAuthFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	authPath := filepath.Join(home, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(authPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	b := NewCodexBackend(CliInfo{Path: readyTestBinaryPath(t)})

	report := b.Ready(context.Background())
	if report.State != llmclient.ReadyOK {
		t.Fatalf("Ready state = %v, want %v", report.State, llmclient.ReadyOK)
	}
}

func readyTestBinaryPath(t *testing.T) string {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), "ready-executable-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return file.Name()
}

func TestClaudeReadyReportsOKWithMarkerFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	// create .claude.json marker
	if err := os.WriteFile(filepath.Join(tmpDir, ".claude.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := NewClaudeBackend(CliInfo{Path: readyTestBinaryPath(t)})
	report := b.Ready(context.Background())
	if report.State != llmclient.ReadyOK {
		t.Fatalf("expected ReadyOK, got %v: %s", report.State, report.Detail)
	}
}

func TestCodexReadyReportsNotAuthed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	b := NewCodexBackend(CliInfo{Path: readyTestBinaryPath(t)})
	report := b.Ready(context.Background())
	if report.State != llmclient.ReadyNotAuthed {
		t.Fatalf("expected ReadyNotAuthed, got %v: %s", report.State, report.Detail)
	}
}

func TestOmpBackendReadyOK(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	configDir := filepath.Join(tmpDir, ".omp", "agent")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	b := NewOmpBackend(CliInfo{Path: readyTestBinaryPath(t)})
	report := b.Ready(context.Background())
	if report.State != llmclient.ReadyOK {
		t.Fatalf("expected ReadyOK, got %v: %s", report.State, report.Detail)
	}
}

func TestOmpBackendReadyNotAuthed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	b := NewOmpBackend(CliInfo{Path: readyTestBinaryPath(t)})
	report := b.Ready(context.Background())
	if report.State != llmclient.ReadyNotAuthed {
		t.Fatalf("expected ReadyNotAuthed, got %v: %s", report.State, report.Detail)
	}
}

func TestOmpRPCBackendReadyNotAuthed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	b := NewOmpRPCBackend(CliInfo{Path: readyTestBinaryPath(t)})
	report := b.Ready(context.Background())
	if report.State != llmclient.ReadyNotAuthed {
		t.Fatalf("expected ReadyNotAuthed, got %v: %s", report.State, report.Detail)
	}
}

func TestOpenCodeRunBackendReadyOK(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configDir := filepath.Join(tmpDir, "opencode")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "opencode.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := NewOpenCodeRunBackend(CliInfo{Path: readyTestBinaryPath(t)})
	report := b.Ready(context.Background())
	if report.State != llmclient.ReadyOK {
		t.Fatalf("expected ReadyOK, got %v: %s", report.State, report.Detail)
	}
}

func TestOpenCodeRunBackendReadyNotAuthed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("HOME", tmpDir)
	b := NewOpenCodeRunBackend(CliInfo{Path: readyTestBinaryPath(t)})
	report := b.Ready(context.Background())
	if report.State != llmclient.ReadyNotAuthed {
		t.Fatalf("expected ReadyNotAuthed, got %v: %s", report.State, report.Detail)
	}
}

func TestOpenCodeHTTPBackendReadyNotAuthed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("HOME", tmpDir)
	b := NewOpenCodeHTTPBackend(CliInfo{Path: readyTestBinaryPath(t)})
	report := b.Ready(context.Background())
	if report.State != llmclient.ReadyNotAuthed {
		t.Fatalf("expected ReadyNotAuthed, got %v: %s", report.State, report.Detail)
	}
}

func TestXdgConfigPathUsesEnvOverride(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	got := xdgConfigPath("a", "b")
	want := filepath.Join(tmpDir, "a", "b")
	if got != want {
		t.Fatalf("xdgConfigPath: got %q, want %q", got, want)
	}
}

func TestXdgConfigPathFallsBackToHomeConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")
	got := xdgConfigPath("a", "b")
	want := filepath.Join(tmpDir, ".config", "a", "b")
	if got != want {
		t.Fatalf("xdgConfigPath: got %q, want %q", got, want)
	}
}
