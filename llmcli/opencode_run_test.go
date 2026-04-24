package llmcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// Compile-time assertion: OpenCodeRunBackend must implement llmclient.Backend.
var _ llmclient.Backend = (*OpenCodeRunBackend)(nil)

// openCodeRunBackendName is the registered name for the opencode run backend.
const openCodeRunBackendName = "opencode-run"

// ─── buildOpenCodeRunArgs ─────────────────────────────────────────────────────

// TestBuildOpenCodeRunArgs_RunSubcommandAndPrompt verifies that "run" is the
// first argument and the last user message becomes the second.
func TestBuildOpenCodeRunArgs_RunSubcommandAndPrompt(t *testing.T) {
	const promptText = "summarise this code"
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: promptText},
			}},
		},
	}
	cfg := llmclient.DefaultRequestConfig()

	args := buildOpenCodeRunArgs(input, cfg)

	if len(args) < 2 {
		t.Fatalf("args too short: %v", args)
	}
	if args[0] != "run" {
		t.Errorf("args[0] = %q, want %q", args[0], "run")
	}
	if args[1] != promptText {
		t.Errorf("args[1] = %q, want %q", args[1], promptText)
	}
	// Model should NOT appear in args — it is handled via config file.
	if flagIndex(args, "--model") >= 0 {
		t.Error("--model should not appear in opencode run args; model is set via config file")
	}
}

// ─── writeTempOpenCodeConfig ──────────────────────────────────────────────────

// TestOpenCodeRun_ConfigCleanup verifies that writeTempOpenCodeConfig creates
// a config.json under <tmpDir>/opencode/, that the config is outside user
// config directories, and that the directory is removed by the cleanup
// function.
func TestOpenCodeRun_ConfigCleanup(t *testing.T) {
	const model = "gpt-4o"
	const sysPrompt = "You are a code reviewer."

	input := &llmclient.Context{SystemPrompt: sysPrompt}
	cfg := llmclient.DefaultRequestConfig()
	cfg.Model = model

	xdgHome, cleanup, err := writeTempOpenCodeConfig(input, cfg)
	if err != nil {
		t.Fatalf("writeTempOpenCodeConfig: %v", err)
	}

	// config.json must exist at the expected location.
	configPath := filepath.Join(xdgHome, "opencode", "config.json")
	data, readErr := os.ReadFile(configPath)
	if readErr != nil {
		cleanup()
		t.Fatalf("read config.json: %v", readErr)
	}

	// Config must be valid JSON containing model and systemPrompt.
	var parsed openCodeConfig
	if jsonErr := json.Unmarshal(data, &parsed); jsonErr != nil {
		cleanup()
		t.Fatalf("unmarshal config.json: %v", jsonErr)
	}
	if parsed.Model != model {
		t.Errorf("config.Model = %q, want %q", parsed.Model, model)
	}
	if parsed.SystemPrompt != sysPrompt {
		t.Errorf("config.SystemPrompt = %q, want %q", parsed.SystemPrompt, sysPrompt)
	}

	// xdgHome must not reside inside user config directories.
	homeDir, homeErr := os.UserHomeDir()
	if homeErr == nil {
		assertNotUnderDir(t, xdgHome, filepath.Join(homeDir, ".config", "opencode"))
		assertNotUnderDir(t, xdgHome, filepath.Join(homeDir, ".codex"))
		assertNotUnderDir(t, xdgHome, filepath.Join(homeDir, ".fabrikk"))
	}

	// After cleanup the directory must not exist.
	cleanup()
	if _, statErr := os.Stat(xdgHome); !os.IsNotExist(statErr) {
		t.Errorf("temp dir %q should not exist after cleanup, stat err: %v", xdgHome, statErr)
	}
}

// TestOpenCodeRun_ConfigCleanup_EmptyFields verifies that writeTempOpenCodeConfig
// succeeds with a nil input and empty model, writing a minimal config.
func TestOpenCodeRun_ConfigCleanup_EmptyFields(t *testing.T) {
	xdgHome, cleanup, err := writeTempOpenCodeConfig(nil, llmclient.DefaultRequestConfig())
	if err != nil {
		t.Fatalf("writeTempOpenCodeConfig(nil, {}): %v", err)
	}
	defer cleanup()

	configPath := filepath.Join(xdgHome, "opencode", "config.json")
	data, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read config.json: %v", readErr)
	}

	var parsed openCodeConfig
	if jsonErr := json.Unmarshal(data, &parsed); jsonErr != nil {
		t.Fatalf("unmarshal config.json: %v", jsonErr)
	}
	if parsed.Model != "" {
		t.Errorf("model should be empty, got %q", parsed.Model)
	}
	if parsed.SystemPrompt != "" {
		t.Errorf("systemPrompt should be empty, got %q", parsed.SystemPrompt)
	}
}

// ─── buildOpenCodeRunEnv ──────────────────────────────────────────────────────

// TestBuildOpenCodeRunEnv_OverridesXDGConfigHome verifies that the returned
// environment replaces any existing XDG_CONFIG_HOME with the supplied path.
func TestBuildOpenCodeRunEnv_OverridesXDGConfigHome(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"HOME=/root",
		"XDG_CONFIG_HOME=/old/path",
	}

	env := buildOpenCodeRunEnv(base, "/new/xdg/home")

	// The new value must be present.
	found := false
	for _, e := range env {
		if e == "XDG_CONFIG_HOME=/new/xdg/home" {
			found = true
		}
		if e == "XDG_CONFIG_HOME=/old/path" {
			t.Error("old XDG_CONFIG_HOME should be removed")
		}
	}
	if !found {
		t.Errorf("XDG_CONFIG_HOME=/new/xdg/home not found in env: %v", env)
	}
}

// TestBuildOpenCodeRunEnv_AddsXDGWhenAbsent verifies that XDG_CONFIG_HOME is
// added when it is not present in the base environment.
func TestBuildOpenCodeRunEnv_AddsXDGWhenAbsent(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/root"}

	env := buildOpenCodeRunEnv(base, "/my/xdg")

	found := false
	for _, e := range env {
		if e == "XDG_CONFIG_HOME=/my/xdg" {
			found = true
		}
	}
	if !found {
		t.Errorf("XDG_CONFIG_HOME=/my/xdg not found in env: %v", env)
	}

	// Original entries must be preserved.
	if !envContains(env, "PATH=/usr/bin") {
		t.Error("PATH lost from environment")
	}
}

// ─── Registry capabilities ────────────────────────────────────────────────────

// TestOpenCodeRunRegistry_StaticCapabilities verifies that the registered
// "opencode-run" factory declares the correct static capabilities: buffered-only
// streaming, no tool events, no multi-turn, no thinking, no usage, and Ollama
// routing through temp config.
func TestOpenCodeRunRegistry_StaticCapabilities(t *testing.T) {
	f, ok := factoryByName(openCodeRunBackendName)
	if !ok {
		t.Fatalf("%s backend not registered", openCodeRunBackendName)
	}

	caps := f.Capabilities

	if caps.Backend != openCodeRunBackendName {
		t.Errorf("Backend = %q, want %q", caps.Backend, openCodeRunBackendName)
	}
	if caps.Streaming != llmclient.StreamingBufferedOnly {
		t.Errorf("Streaming = %q, want %q", caps.Streaming, llmclient.StreamingBufferedOnly)
	}
	if caps.ToolEvents {
		t.Error("ToolEvents should be false for text-only backend")
	}
	if caps.MultiTurn {
		t.Error("MultiTurn should be false for text-only backend")
	}
	if caps.Thinking {
		t.Error("Thinking should be false for text-only backend")
	}
	if caps.Usage {
		t.Error("Usage should be false for text-only backend")
	}
	if !caps.OllamaRouting {
		t.Error("OllamaRouting should be true for opencode-run")
	}
	if caps.HostToolDefs {
		t.Error("HostToolDefs should be false for text-only backend")
	}
	if f.Preference != PreferOpenCode {
		t.Errorf("Preference = %v, want PreferOpenCode", f.Preference)
	}
	if f.Binary != "opencode" {
		t.Errorf("Binary = %q, want %q", f.Binary, "opencode")
	}
	if f.Name != openCodeRunBackendName {
		t.Errorf("Name = %q, want %q", f.Name, openCodeRunBackendName)
	}
	if caps.OptionSupport[llmclient.OptionModel] == "" {
		t.Error("OptionSupport[OptionModel] should be set")
	}
	if caps.OptionSupport[llmclient.OptionOllama] == "" {
		t.Error("OptionSupport[OptionOllama] should be set")
	}
}

// TestOpenCodeRunRegistry_RefusesStructuredRequirements verifies that the
// opencode-run backend is filtered out when structured streaming, tool events,
// thinking, or usage are required.
func TestOpenCodeRunRegistry_RefusesStructuredRequirements(t *testing.T) {
	f, ok := factoryByName(openCodeRunBackendName)
	if !ok {
		t.Fatalf("%s backend not registered", openCodeRunBackendName)
	}

	cases := []struct {
		name string
		req  Requirements
	}{
		{"MinStreaming=structured", Requirements{MinStreaming: llmclient.StreamingStructured}},
		{"MinStreaming=text_chunk", Requirements{MinStreaming: llmclient.StreamingTextChunk}},
		{"NeedsToolEvents", Requirements{NeedsToolEvents: true}},
		{"NeedsMultiTurn", Requirements{NeedsMultiTurn: true}},
		{"NeedsThinking", Requirements{NeedsThinking: true}},
		{"NeedsUsage", Requirements{NeedsUsage: true}},
		{"NeedsHostToolDefs", Requirements{NeedsHostToolDefs: true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if satisfiesStaticRequirements(f.Capabilities, tc.req) {
				t.Errorf("opencode-run should NOT satisfy requirement %s", tc.name)
			}
		})
	}
}

// TestOpenCodeRun_RequiredUnsupportedOptionReturnsError verifies that when the
// caller marks an unsupported option as required, Stream returns
// ErrUnsupportedOption before spawning any subprocess.
func TestOpenCodeRun_RequiredUnsupportedOptionReturnsError(t *testing.T) {
	b := NewOpenCodeRunBackend(CliInfo{Path: "/does/not/matter"})
	input := &llmclient.Context{}

	unsupportedOpts := []llmclient.OptionName{
		llmclient.OptionSession,
		llmclient.OptionHostTools,
	}

	for _, opt := range unsupportedOpts {
		t.Run(string(opt), func(t *testing.T) {
			_, err := b.Stream(
				context.Background(),
				input,
				llmclient.WithRequiredOptions(opt),
			)
			if err == nil {
				t.Fatalf("expected ErrUnsupportedOption for %q, got nil", opt)
			}
		})
	}
}

func TestOpenCodeRun_RequiredOllamaOptionAccepted(t *testing.T) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), []llmclient.Option{
		llmclient.WithOllama(llmclient.OllamaConfig{Model: "qwen3.5"}),
		llmclient.WithRequiredOptions(llmclient.OptionOllama),
	})
	err := checkOpenCodeRunRequiredOptions(cfg)
	if err != nil {
		t.Fatalf("expected required OptionOllama to be accepted, got %v", err)
	}
}

func TestOpenCodeRun_ConfigCleanup_Ollama(t *testing.T) {
	input := &llmclient.Context{SystemPrompt: "system"}
	cfg := llmclient.DefaultRequestConfig()
	cfg.Ollama = &llmclient.OllamaConfig{
		BaseURL: "http://localhost:11434",
		Model:   "qwen3.5",
	}

	xdgHome, cleanup, err := writeTempOpenCodeConfig(input, cfg)
	if err != nil {
		t.Fatalf("writeTempOpenCodeConfig with Ollama: %v", err)
	}
	defer cleanup()

	configPath := filepath.Join(xdgHome, "opencode", "config.json")
	data, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read config.json: %v", readErr)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal config.json: %v", err)
	}
	if parsed["model"] != "ollama/qwen3.5" {
		t.Fatalf("config model = %v, want %q", parsed["model"], "ollama/qwen3.5")
	}
	providers, ok := parsed["providers"].(map[string]any)
	if !ok {
		t.Fatal("config providers block missing for Ollama")
	}
	if _, ok := providers["ollama"]; !ok {
		t.Fatalf("config providers missing ollama entry: %v", providers)
	}
}

// ─── Text fallback fidelity ───────────────────────────────────────────────────

// TestOpenCodeRun_TextFallbackFidelity verifies that an opencode-run Stream
// call emits a start event with StreamingBufferedOnly fidelity and
// ToolControlNone, followed by text events and a done event with StopEndTurn.
//
// Uses the test binary as a fake opencode subprocess: when
// FABRIKK_LLMCLI_TEST_VERSION is set the test binary prints its value to
// stdout and exits 0, simulating a successful opencode run invocation.
func TestOpenCodeRun_TextFallbackFidelity(t *testing.T) {
	const fakeOutput = "the answer is 42"
	t.Setenv("FABRIKK_LLMCLI_TEST_VERSION", fakeOutput)

	exe := testExecutable(t)
	b := NewOpenCodeRunBackend(CliInfo{
		Name:    "opencode",
		Binary:  "opencode",
		Path:    exe,
		Version: "0.0.0-test",
	})

	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "What is 6 × 7?"},
			}},
		},
		SystemPrompt: "You are a math tutor.",
	}

	ch, err := b.Stream(context.Background(), input)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainSubprocessOpenCodeChan(t, ch)

	if len(events) == 0 {
		t.Fatal("no events received")
	}

	// Start event must carry StreamingBufferedOnly fidelity and ToolControlNone.
	start := events[0]
	if start.Type != llmclient.EventStart {
		t.Fatalf("events[0].Type = %q, want %q", start.Type, llmclient.EventStart)
	}
	if start.Fidelity == nil {
		t.Fatal("start event has nil Fidelity")
	}
	if start.Fidelity.Streaming != llmclient.StreamingBufferedOnly {
		t.Errorf("Fidelity.Streaming = %q, want %q",
			start.Fidelity.Streaming, llmclient.StreamingBufferedOnly)
	}
	if start.Fidelity.ToolControl != llmclient.ToolControlNone {
		t.Errorf("Fidelity.ToolControl = %q, want %q",
			start.Fidelity.ToolControl, llmclient.ToolControlNone)
	}
	if len(start.Fidelity.OptionResults) != 0 {
		t.Errorf("Fidelity.OptionResults = %v, want empty map when no options are passed", start.Fidelity.OptionResults)
	}

	// Last event must be done with StopEndTurn.
	last := events[len(events)-1]
	if last.Type != llmclient.EventDone {
		t.Errorf("last event type = %q, want %q", last.Type, llmclient.EventDone)
	}
	if last.Reason != llmclient.StopEndTurn {
		t.Errorf("done.Reason = %q, want %q", last.Reason, llmclient.StopEndTurn)
	}

	// The done event must carry an AssistantMessage.
	if last.Message == nil {
		t.Fatal("done.Message is nil")
	}
	if last.Message.Role != "assistant" {
		t.Errorf("done.Message.Role = %q, want %q", last.Message.Role, "assistant")
	}

	// Text events must appear between start and done.
	assertContainsEventType(t, events, llmclient.EventTextStart)
	assertContainsEventType(t, events, llmclient.EventTextDelta)
	assertContainsEventType(t, events, llmclient.EventTextEnd)
}

// TestOpenCodeRun_ExactlyOneTerminalEvent verifies that exactly one terminal
// event (done or error) is emitted and is always the last event.
func TestOpenCodeRun_ExactlyOneTerminalEvent(t *testing.T) {
	t.Setenv("FABRIKK_LLMCLI_TEST_VERSION", "ok")

	exe := testExecutable(t)
	b := NewOpenCodeRunBackend(CliInfo{Path: exe})

	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "hello"},
			}},
		},
	}

	ch, err := b.Stream(context.Background(), input)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainSubprocessOpenCodeChan(t, ch)

	var terminalCount int
	for _, ev := range events {
		if ev.Type == llmclient.EventDone || ev.Type == llmclient.EventError {
			terminalCount++
		}
	}
	if terminalCount != 1 {
		t.Errorf("terminal event count = %d, want 1", terminalCount)
	}

	last := events[len(events)-1]
	if last.Type != llmclient.EventDone && last.Type != llmclient.EventError {
		t.Errorf("last event type = %q; want done or error", last.Type)
	}
}

// TestOpenCodeRun_Cancel verifies that cancelling the context terminates the
// subprocess and closes the channel within a reasonable time.
func TestOpenCodeRun_Cancel(t *testing.T) {
	exe := testExecutable(t)

	// Use the "sleep" fixture: it hangs indefinitely, producing no stdout.
	t.Setenv("LLMCLI_TEST_FIXTURE", "sleep")

	b := NewOpenCodeRunBackend(CliInfo{Path: exe})
	ctx, cancel := context.WithCancel(context.Background())

	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "hello"},
			}},
		},
	}

	ch, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		drainChannel(ch)
	}()

	select {
	case <-done:
		// Channel closed as expected after cancellation.
	case <-time.After(5 * time.Second):
		t.Error("channel not closed within 5s after context cancellation")
	}
}

// TestOpenCodeRun_XDGConfigHomeIsIsolated verifies that the subprocess
// environment built for opencode run contains an XDG_CONFIG_HOME that
// does not point to the user's real config directory.
func TestOpenCodeRun_XDGConfigHomeIsIsolated(t *testing.T) {
	input := &llmclient.Context{}
	cfg := llmclient.DefaultRequestConfig()

	xdgHome, cleanup, err := writeTempOpenCodeConfig(input, cfg)
	if err != nil {
		t.Fatalf("writeTempOpenCodeConfig: %v", err)
	}
	defer cleanup()

	env := buildOpenCodeRunEnv(os.Environ(), xdgHome)

	// Find XDG_CONFIG_HOME in the built env.
	var xdgValue string
	for _, e := range env {
		if strings.HasPrefix(e, "XDG_CONFIG_HOME=") {
			xdgValue = strings.TrimPrefix(e, "XDG_CONFIG_HOME=")
			break
		}
	}
	if xdgValue == "" {
		t.Fatal("XDG_CONFIG_HOME not set in subprocess environment")
	}

	// Must point to the temp dir, not to the user's real config directory.
	homeDir, homeErr := os.UserHomeDir()
	if homeErr == nil {
		realOpenCodeConfig := filepath.Join(homeDir, ".config", "opencode")
		if strings.HasPrefix(xdgValue, realOpenCodeConfig) {
			t.Errorf("XDG_CONFIG_HOME %q points inside user opencode config dir %q",
				xdgValue, realOpenCodeConfig)
		}
	}

	// Must point to a path that exists (the temp dir we created).
	if _, statErr := os.Stat(xdgValue); statErr != nil {
		t.Errorf("XDG_CONFIG_HOME %q does not exist: %v", xdgValue, statErr)
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// drainSubprocessOpenCodeChan collects all events from ch with a generous
// timeout for race-detected subprocess invocations.
func drainSubprocessOpenCodeChan(t *testing.T, ch <-chan llmclient.Event) []llmclient.Event {
	t.Helper()
	result := make(chan []llmclient.Event, 1)
	go func() { result <- drainChannel(ch) }()
	select {
	case evs := <-result:
		return evs
	case <-time.After(10 * time.Second):
		t.Fatal("subprocess channel was not closed within 10s")
		return nil
	}
}

// envContains reports whether entry appears verbatim in env.
func envContains(env []string, entry string) bool {
	for _, e := range env {
		if e == entry {
			return true
		}
	}
	return false
}
