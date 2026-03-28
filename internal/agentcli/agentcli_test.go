package agentcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveCommand_Fallback(t *testing.T) {
	// An unknown command that doesn't exist anywhere should return the raw name.
	got := ResolveCommand("nonexistent-tool-xyz-12345")
	if got != "nonexistent-tool-xyz-12345" {
		t.Errorf("ResolveCommand(unknown) = %q, want raw name", got)
	}
}

func TestKnownBackends_AllPresent(t *testing.T) {
	for _, name := range []string{BackendClaude, BackendCodex, BackendGemini} {
		if _, ok := KnownBackends[name]; !ok {
			t.Errorf("KnownBackends missing %q", name)
		}
	}
}

func TestExtractFromCodeFence_Variants(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"json fence", "before\n```json\n{\"key\": \"val\"}\n```\nafter", `{"key": "val"}`},
		{"markdown fence", "before\n```markdown\n# Heading\ncontent\n```\nafter", "# Heading\ncontent"},
		{"generic fence", "before\n```\ncontent\n```\nafter", "content"},
		{"no fence", "plain text", ""},
		{"empty fence", "```\n```", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractFromCodeFence(tt.input)
			if got != tt.want {
				t.Errorf("ExtractFromCodeFence() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBackendFor_DisabledFallsBackToClaude(t *testing.T) {
	t.Setenv("FABRIKK_DISABLED_BACKENDS", "gemini")

	got := BackendFor(BackendGemini, "")
	want := KnownBackends[BackendClaude]
	if got.Command != want.Command {
		t.Errorf("disabled gemini: got command %q, want %q (claude fallback)", got.Command, want.Command)
	}

	// Claude itself should NOT be affected.
	gotClaude := BackendFor(BackendClaude, "")
	if gotClaude.Command != want.Command {
		t.Errorf("claude should not be disabled: got %q", gotClaude.Command)
	}
}

func TestBackendFor_MultipleDisabled(t *testing.T) {
	t.Setenv("FABRIKK_DISABLED_BACKENDS", "gemini,codex")

	gotGemini := BackendFor(BackendGemini, "")
	gotCodex := BackendFor(BackendCodex, "")
	wantClaude := KnownBackends[BackendClaude]

	if gotGemini.Command != wantClaude.Command {
		t.Errorf("disabled gemini: got %q, want claude", gotGemini.Command)
	}
	if gotCodex.Command != wantClaude.Command {
		t.Errorf("disabled codex: got %q, want claude", gotCodex.Command)
	}
}

func TestBackendFor_NoneDisabled(t *testing.T) {
	t.Setenv("FABRIKK_DISABLED_BACKENDS", "")

	got := BackendFor(BackendGemini, "")
	want := KnownBackends[BackendGemini]
	if got.Command != want.Command {
		t.Errorf("no disabled backends: gemini should work, got %q", got.Command)
	}
}

func TestBackendFor_DisabledViaConfigFile(t *testing.T) {
	// Env var must be unset so config file is consulted.
	t.Setenv("FABRIKK_DISABLED_BACKENDS", "")

	// Create a temp dir with fabrikk.yaml and chdir into it.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fabrikk.yaml"), []byte("disabled_backends:\n  - gemini\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir) //nolint:errcheck // best-effort restore

	got := BackendFor(BackendGemini, "")
	wantClaude := KnownBackends[BackendClaude]
	if got.Command != wantClaude.Command {
		t.Errorf("config-disabled gemini: got %q, want %q", got.Command, wantClaude.Command)
	}

	// Codex should still work (not in config).
	gotCodex := BackendFor(BackendCodex, "")
	wantCodex := KnownBackends[BackendCodex]
	if gotCodex.Command != wantCodex.Command {
		t.Errorf("codex should not be disabled: got %q", gotCodex.Command)
	}
}

func TestBuildInvokeArgs_ShortPrompt(t *testing.T) {
	backend := &CLIBackend{Command: "claude", Args: []string{"-p"}}
	args, useStdin := BuildInvokeArgs(backend, "hello world")
	if useStdin {
		t.Error("short prompt should not use stdin")
	}
	// Prompt should appear as last arg.
	if len(args) < 2 || args[len(args)-1] != "hello world" {
		t.Errorf("args = %v, want prompt as last element", args)
	}
}

func TestBuildInvokeArgs_ShortPromptWithFlag(t *testing.T) {
	backend := &CLIBackend{Command: "gemini", Args: []string{"-m", "model"}, PromptFlag: "-p"}
	args, useStdin := BuildInvokeArgs(backend, "hello")
	if useStdin {
		t.Error("short prompt should not use stdin")
	}
	// Should contain -p followed by prompt.
	found := false
	for i, arg := range args {
		if arg == "-p" && i+1 < len(args) && args[i+1] == "hello" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("args = %v, want -p hello", args)
	}
}

func TestBuildInvokeArgs_LongPromptUsesStdin(t *testing.T) {
	backend := &CLIBackend{Command: "claude", Args: []string{"-p"}}
	longPrompt := strings.Repeat("x", StdinThreshold+1)
	args, useStdin := BuildInvokeArgs(backend, longPrompt)
	if !useStdin {
		t.Error("prompt exceeding threshold should use stdin")
	}
	// Prompt must NOT appear in args.
	for _, arg := range args {
		if arg == longPrompt {
			t.Error("long prompt should not be in args — it should be piped via stdin")
		}
	}
}

func TestBuildInvokeArgs_BoundaryExact(t *testing.T) {
	backend := &CLIBackend{Command: "claude", Args: []string{"-p"}}
	// Exactly at threshold: should NOT use stdin (threshold is >, not >=).
	prompt := strings.Repeat("x", StdinThreshold)
	_, useStdin := BuildInvokeArgs(backend, prompt)
	if useStdin {
		t.Errorf("prompt at exact threshold (%d bytes) should not use stdin", StdinThreshold)
	}
}

func TestBuildInvokeArgs_BoundaryPlusOne(t *testing.T) {
	backend := &CLIBackend{Command: "claude", Args: []string{"-p"}}
	prompt := strings.Repeat("x", StdinThreshold+1)
	_, useStdin := BuildInvokeArgs(backend, prompt)
	if !useStdin {
		t.Errorf("prompt at threshold+1 (%d bytes) should use stdin", StdinThreshold+1)
	}
}
