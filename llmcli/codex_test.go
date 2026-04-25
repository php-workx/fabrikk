package llmcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// Compile-time assertion: CodexBackend must implement llmclient.Backend.
var _ llmclient.Backend = (*CodexBackend)(nil)

// codexExecBackendName is the registered name for the codex exec backend.
const codexExecBackendName = "codex-exec"

// ─── buildCodexExecArgs ───────────────────────────────────────────────────────

// TestBuildCodexExecArgs_ModelFlag verifies that --model is included when
// cfg.Model is set.
func TestBuildCodexExecArgs_ModelFlag(t *testing.T) {
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "What is 2+2?"},
				},
			},
		},
	}
	cfg := llmclient.DefaultRequestConfig()
	cfg.Model = "gpt-5.4"

	args := buildCodexExecArgs(input, cfg)

	want := map[string]string{
		"--model": "gpt-5.4",
	}
	assertFlagsPresent(t, args, want)
}

// TestBuildCodexExecArgs_NoModelFlag verifies that --model is absent when
// cfg.Model is empty.
func TestBuildCodexExecArgs_NoModelFlag(t *testing.T) {
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "hello"},
			}},
		},
	}
	cfg := llmclient.DefaultRequestConfig()

	args := buildCodexExecArgs(input, cfg)

	if flagIndex(args, "--model") >= 0 {
		t.Error("--model should not be present when cfg.Model is empty")
	}
}

// TestBuildCodexExecArgs_Ollama verifies that WithOllama appends --oss and
// prefers the Ollama model over cfg.Model.
func TestBuildCodexExecArgs_Ollama(t *testing.T) {
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "hello"},
			}},
		},
	}
	cfg := llmclient.DefaultRequestConfig()
	cfg.Model = "gpt-5.4"
	cfg.Ollama = &llmclient.OllamaConfig{
		BaseURL: "http://localhost:11434",
		Model:   "gpt-oss:120b",
	}

	args := buildCodexExecArgs(input, cfg)

	want := map[string]string{
		"--approval-policy": "full-auto",
		"--model":           "gpt-oss:120b",
	}
	assertFlagsPresent(t, args, want)
	if flagIndex(args, "--oss") < 0 {
		t.Fatal("--oss flag missing when cfg.Ollama is set")
	}
}

// TestBuildCodexExecArgs_ApprovalPolicy verifies that --approval-policy
// full-auto is always included to suppress interactive prompts.
func TestBuildCodexExecArgs_ApprovalPolicy(t *testing.T) {
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "hello"},
			}},
		},
	}
	cfg := llmclient.DefaultRequestConfig()

	args := buildCodexExecArgs(input, cfg)

	want := map[string]string{
		"--approval-policy": "full-auto",
	}
	assertFlagsPresent(t, args, want)
}

// TestBuildCodexExecArgs_ExecSubcommandAndPrompt verifies that "exec" is the
// first positional argument and that the prompt is the second.
func TestBuildCodexExecArgs_ExecSubcommandAndPrompt(t *testing.T) {
	const promptText = "count to five"
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: promptText},
			}},
		},
	}
	cfg := llmclient.DefaultRequestConfig()

	args := buildCodexExecArgs(input, cfg)

	if len(args) < 2 {
		t.Fatalf("args too short: %v", args)
	}
	if args[0] != "exec" {
		t.Errorf("args[0] = %q, want %q", args[0], "exec")
	}
	if args[1] != promptText {
		t.Errorf("args[1] = %q, want %q", args[1], promptText)
	}
}

func TestBuildCodexExecArgs_WorkingDirectoryAndReasoningEffort(t *testing.T) {
	input := &llmclient.Context{
		SystemPrompt: "Follow the repo rules.",
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "implement feature"},
			}},
		},
	}
	cfg := llmclient.DefaultRequestConfig()
	cfg.WorkingDirectory = "/tmp/worktree"
	cfg.ReasoningEffort = "high"

	args := buildCodexExecArgs(input, cfg)

	assertFlagsPresent(t, args, map[string]string{
		"-C": "/tmp/worktree",
		"-c": "model_reasoning_effort=high",
	})
	execIdx := flagIndex(args, "exec")
	if execIdx < 0 || execIdx+1 >= len(args) {
		t.Fatalf("exec prompt not found in args %v", args)
	}
	prompt := args[execIdx+1]
	if !strings.Contains(prompt, "System instructions:\nFollow the repo rules.") {
		t.Errorf("prompt = %q; want injected system instructions", prompt)
	}
	if !strings.Contains(prompt, "User request:\nimplement feature") {
		t.Errorf("prompt = %q; want user request", prompt)
	}
}

func TestBuildCodexExecArgs_CodexJSONL(t *testing.T) {
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "hello"},
			}},
		},
	}
	cfg := llmclient.DefaultRequestConfig()
	cfg.CodexJSONL = true

	args := buildCodexExecArgs(input, cfg)

	if flagIndex(args, "--json") < 0 {
		t.Fatalf("--json flag missing when cfg.CodexJSONL is true; args=%v", args)
	}
}

// ─── writeTempAgentsFile ──────────────────────────────────────────────────────

// TestCodexExec_WritesTempAgentsFile verifies that writeTempAgentsFile creates
// an AGENTS.md in a temp directory outside user config paths, writes the
// system prompt content, and removes the directory on cleanup.
func TestCodexExec_WritesTempAgentsFile(t *testing.T) {
	const sysPrompt = "You are a helpful assistant."

	input := &llmclient.Context{SystemPrompt: sysPrompt}

	dir, cleanup, err := writeTempAgentsFile(input)
	if err != nil {
		t.Fatalf("writeTempAgentsFile: %v", err)
	}

	// AGENTS.md must exist and contain the system prompt.
	agentsPath := filepath.Join(dir, "AGENTS.md")
	content, readErr := os.ReadFile(agentsPath)
	if readErr != nil {
		cleanup()
		t.Fatalf("read AGENTS.md: %v", readErr)
	}
	if string(content) != sysPrompt {
		cleanup()
		t.Errorf("AGENTS.md content = %q, want %q", string(content), sysPrompt)
	}

	// The temp dir must not reside inside user config directories.
	homeDir, homeErr := os.UserHomeDir()
	if homeErr == nil {
		assertNotUnderDir(t, dir, filepath.Join(homeDir, ".codex"))
		assertNotUnderDir(t, dir, filepath.Join(homeDir, ".config", "opencode"))
		assertNotUnderDir(t, dir, filepath.Join(homeDir, ".fabrikk"))
	}

	// After cleanup the directory must not exist.
	cleanup()
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("temp dir %q should not exist after cleanup, stat err: %v", dir, statErr)
	}
}

// TestCodexExec_WritesTempAgentsFile_NilInput verifies that writeTempAgentsFile
// succeeds with a nil input (empty system prompt) and writes an empty AGENTS.md.
func TestCodexExec_WritesTempAgentsFile_NilInput(t *testing.T) {
	dir, cleanup, err := writeTempAgentsFile(nil)
	if err != nil {
		t.Fatalf("writeTempAgentsFile(nil): %v", err)
	}
	defer cleanup()

	agentsPath := filepath.Join(dir, "AGENTS.md")
	content, readErr := os.ReadFile(agentsPath)
	if readErr != nil {
		t.Fatalf("read AGENTS.md: %v", readErr)
	}
	if len(content) != 0 {
		t.Errorf("AGENTS.md should be empty for nil input, got %q", string(content))
	}
}

// ─── Registry capabilities ────────────────────────────────────────────────────

// TestCodexExecRegistry_StaticCapabilities verifies that the registered
// "codex-exec" factory declares the correct static capabilities: buffered-only
// streaming, no tool events, no multi-turn, no thinking, no usage, and Ollama
// routing via --oss.
func TestCodexExecRegistry_StaticCapabilities(t *testing.T) {
	f, ok := factoryByName(codexExecBackendName)
	if !ok {
		t.Fatalf("%s backend not registered", codexExecBackendName)
	}

	caps := f.Capabilities

	if caps.Backend != codexExecBackendName {
		t.Errorf("Backend = %q, want %q", caps.Backend, codexExecBackendName)
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
		t.Error("OllamaRouting should be true for codex-exec")
	}
	if caps.HostToolDefs {
		t.Error("HostToolDefs should be false for text-only backend")
	}
	if f.Preference != PreferCodex {
		t.Errorf("Preference = %v, want PreferCodex", f.Preference)
	}
	if f.Binary != "codex" {
		t.Errorf("Binary = %q, want %q", f.Binary, "codex")
	}
	if f.Name != codexExecBackendName {
		t.Errorf("Name = %q, want %q", f.Name, codexExecBackendName)
	}
	if caps.OptionSupport[llmclient.OptionModel] == "" {
		t.Error("OptionSupport[OptionModel] should be set")
	}
	if caps.OptionSupport[llmclient.OptionOllama] == "" {
		t.Error("OptionSupport[OptionOllama] should be set")
	}
}

// TestCodexExecRegistry_RefusesStructuredRequirements verifies that the
// codex-exec backend is filtered out when structured streaming, tool events,
// thinking, or usage are required.
func TestCodexExecRegistry_RefusesStructuredRequirements(t *testing.T) {
	f, ok := factoryByName(codexExecBackendName)
	if !ok {
		t.Fatalf("%s backend not registered", codexExecBackendName)
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
				t.Errorf("codex-exec should NOT satisfy requirement %s", tc.name)
			}
		})
	}
}

// TestCodexExec_RequiredUnsupportedOptionReturnsError verifies that when the
// caller marks an unsupported option as required, Stream returns
// ErrUnsupportedOption before spawning any subprocess.
func TestCodexExec_RequiredUnsupportedOptionReturnsError(t *testing.T) {
	b := NewCodexBackend(CliInfo{Path: "/does/not/matter"})
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

func TestCodexExec_RequiredReasoningEffortAccepted(t *testing.T) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), []llmclient.Option{
		llmclient.WithReasoningEffort("high"),
		llmclient.WithRequiredOptions(llmclient.OptionReasoningEffort),
	})
	if err := checkCodexExecRequiredOptions(cfg); err != nil {
		t.Fatalf("expected required OptionReasoningEffort to be accepted, got %v", err)
	}
}

func TestCodexExec_RequiredCodexJSONLAccepted(t *testing.T) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), []llmclient.Option{
		llmclient.WithCodexJSONL(true),
		llmclient.WithRequiredOptions(llmclient.OptionCodexJSONL),
	})
	if err := checkCodexExecRequiredOptions(cfg); err != nil {
		t.Fatalf("expected required OptionCodexJSONL to be accepted, got %v", err)
	}
}

func TestCodexExec_RequiredOllamaOptionAccepted(t *testing.T) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), []llmclient.Option{
		llmclient.WithOllama(llmclient.OllamaConfig{Model: "gpt-oss:120b"}),
		llmclient.WithRequiredOptions(llmclient.OptionOllama),
	})
	err := checkCodexExecRequiredOptions(cfg)
	if err != nil {
		t.Fatalf("expected required OptionOllama to be accepted, got %v", err)
	}
}

// ─── Text fallback fidelity ───────────────────────────────────────────────────

// TestCodexExec_TextFallbackFidelity verifies that a codex-exec Stream call
// emits a start event with StreamingBufferedOnly fidelity and ToolControlNone,
// followed by text events and a done event with StopEndTurn.
//
// Uses the test binary as a fake codex subprocess: when
// FABRIKK_LLMCLI_TEST_VERSION is set the test binary prints its value to
// stdout and exits 0, simulating a successful codex exec invocation.
func TestCodexExec_TextFallbackFidelity(t *testing.T) {
	const fakeOutput = "four"
	t.Setenv("FABRIKK_LLMCLI_TEST_VERSION", fakeOutput)

	exe := testExecutable(t)
	b := NewCodexBackend(CliInfo{
		Name:    "codex",
		Binary:  "codex",
		Path:    exe,
		Version: "0.0.0-test",
	})

	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "What is 2+2?"},
			}},
		},
	}

	ch, err := b.Stream(context.Background(), input)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainSubprocessChan(t, ch)

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

	// The done event must carry an AssistantMessage with the captured text.
	if last.Message == nil {
		t.Fatal("done.Message is nil")
	}
	if last.Message.Role != string(llmclient.RoleAssistant) {
		t.Errorf("done.Message.Role = %q, want %q", last.Message.Role, llmclient.RoleAssistant)
	}

	// Text events must appear between start and done.
	assertContainsEventType(t, events, llmclient.EventTextStart)
	assertContainsEventType(t, events, llmclient.EventTextDelta)
	assertContainsEventType(t, events, llmclient.EventTextEnd)
}

// TestCodexExec_ExactlyOneTerminalEvent verifies that exactly one terminal
// event (done or error) is emitted and is the last event in the channel.
func TestCodexExec_ExactlyOneTerminalEvent(t *testing.T) {
	t.Setenv("FABRIKK_LLMCLI_TEST_VERSION", "ok")

	exe := testExecutable(t)
	b := NewCodexBackend(CliInfo{Path: exe})

	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "hi"},
			}},
		},
	}

	ch, err := b.Stream(context.Background(), input)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainSubprocessChan(t, ch)

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

// TestCodexExec_Cancel verifies that cancelling the context terminates the
// subprocess and closes the channel within a reasonable time.
func TestCodexExec_Cancel(t *testing.T) {
	exe := testExecutable(t)

	// Use the "sleep" fixture: it hangs indefinitely, producing no stdout.
	b := NewCodexBackend(CliInfo{Path: exe})

	ctx, cancel := context.WithCancel(context.Background())

	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "hello"},
			}},
		},
		SystemPrompt: "",
	}

	// Override LLMCLI_TEST_FIXTURE so the subprocess hangs.
	t.Setenv("LLMCLI_TEST_FIXTURE", "sleep")

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

func TestCodexExec_TimeoutCancelsSubprocess(t *testing.T) {
	exe := testExecutable(t)
	b := NewCodexBackend(CliInfo{Path: exe})
	t.Setenv("LLMCLI_TEST_FIXTURE", "sleep")

	ch, err := b.Stream(
		context.Background(),
		&llmclient.Context{Messages: []llmclient.Message{{
			Role: llmclient.RoleUser,
			Content: []llmclient.ContentBlock{{
				Type: llmclient.ContentText,
				Text: "hello",
			}},
		}}},
		llmclient.WithTimeout(150*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainSubprocessChan(t, ch)
	done := findEvent(t, events, llmclient.EventDone)
	if done.Reason != llmclient.StopCancelled {
		t.Errorf("done.Reason = %q, want %q", done.Reason, llmclient.StopCancelled)
	}
}

func TestCodexExec_EnvironmentAndRawCapture(t *testing.T) {
	exe := testExecutable(t)
	b := NewCodexBackend(CliInfo{Path: exe})
	wantCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	var mu sync.Mutex
	captured := map[llmclient.RawStream][]byte{}
	capture := func(stream llmclient.RawStream, data []byte) {
		mu.Lock()
		defer mu.Unlock()
		captured[stream] = append(captured[stream], data...)
	}

	env := append(baseEnv(),
		"LLMCLI_TEST_FIXTURE=inspect_process",
		"LLMCLI_TEST_ENV_VALUE=codex-env",
	)
	ch, err := b.Stream(
		context.Background(),
		&llmclient.Context{Messages: []llmclient.Message{{
			Role: llmclient.RoleUser,
			Content: []llmclient.ContentBlock{{
				Type: llmclient.ContentText,
				Text: "hello",
			}},
		}}},
		llmclient.WithEnvironment(env),
		llmclient.WithRawCapture(capture),
	)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainSubprocessChan(t, ch)
	start := findEvent(t, events, llmclient.EventStart)
	if got := start.Fidelity.OptionResults[llmclient.OptionEnvironment]; got != llmclient.OptionApplied {
		t.Errorf("OptionEnvironment result = %q, want applied", got)
	}
	if got := start.Fidelity.OptionResults[llmclient.OptionRawCapture]; got != llmclient.OptionApplied {
		t.Errorf("OptionRawCapture result = %q, want applied", got)
	}
	done := findEvent(t, events, llmclient.EventDone)
	if done.Message == nil || !strings.Contains(done.Message.Content[0].Text, "env=codex-env") {
		t.Fatalf("done message = %#v; want env output", done.Message)
	}
	if !strings.Contains(done.Message.Content[0].Text, "cwd="+wantCWD) {
		t.Fatalf("done message = %#v; want inherited cwd %q", done.Message, wantCWD)
	}

	mu.Lock()
	rawStdout := string(captured[llmclient.RawStreamStdout])
	rawStderr := string(captured[llmclient.RawStreamStderr])
	mu.Unlock()
	if !strings.Contains(rawStdout, "env=codex-env") {
		t.Errorf("raw stdout = %q; want env output", rawStdout)
	}
	if !strings.Contains(rawStderr, "inspect-stderr") {
		t.Errorf("raw stderr = %q; want inspect-stderr", rawStderr)
	}
}

func TestCodexExec_JSONLModePreservesRawEventsAndNormalizesText(t *testing.T) {
	exe := testExecutable(t)
	b := NewCodexBackend(CliInfo{Path: exe})

	var mu sync.Mutex
	captured := map[llmclient.RawStream][]byte{}
	capture := func(stream llmclient.RawStream, data []byte) {
		mu.Lock()
		defer mu.Unlock()
		captured[stream] = append(captured[stream], data...)
	}

	env := append(baseEnv(), "LLMCLI_TEST_FIXTURE=codex_jsonl")
	ch, err := b.Stream(
		context.Background(),
		&llmclient.Context{Messages: []llmclient.Message{{
			Role: llmclient.RoleUser,
			Content: []llmclient.ContentBlock{{
				Type: llmclient.ContentText,
				Text: "hello",
			}},
		}}},
		llmclient.WithEnvironment(env),
		llmclient.WithCodexJSONL(true),
		llmclient.WithRawCapture(capture),
	)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainSubprocessChan(t, ch)
	start := findEvent(t, events, llmclient.EventStart)
	if got := start.Fidelity.OptionResults[llmclient.OptionCodexJSONL]; got != llmclient.OptionApplied {
		t.Errorf("OptionCodexJSONL result = %q, want applied", got)
	}

	delta := findEvent(t, events, llmclient.EventTextDelta)
	if !strings.Contains(delta.Delta, `VERK_RESULT: {"status":"done","completion_code":"ok"}`) {
		t.Errorf("text delta = %q; want normalized agent message text", delta.Delta)
	}

	done := findEvent(t, events, llmclient.EventDone)
	if done.Message == nil || len(done.Message.Content) == 0 {
		t.Fatalf("done message = %#v; want content", done.Message)
	}
	if done.Message.Content[0].Text != delta.Delta {
		t.Errorf("done text = %q, want %q", done.Message.Content[0].Text, delta.Delta)
	}
	if done.Usage == nil {
		t.Fatal("done usage is nil")
	}
	if done.Usage.InputTokens != 11 || done.Usage.CacheReadTokens != 3 || done.Usage.OutputTokens != 7 {
		t.Errorf("usage = %#v; want input=11 cached=3 output=7", done.Usage)
	}

	mu.Lock()
	rawStdout := string(captured[llmclient.RawStreamStdout])
	rawStderr := string(captured[llmclient.RawStreamStderr])
	mu.Unlock()
	if !strings.Contains(rawStdout, `"type":"turn.completed"`) {
		t.Errorf("raw stdout = %q; want JSONL turn.completed", rawStdout)
	}
	if !strings.Contains(rawStdout, `"type":"command_execution"`) {
		t.Errorf("raw stdout = %q; want activity event preserved", rawStdout)
	}
	if !strings.Contains(rawStderr, "jsonl-stderr") {
		t.Errorf("raw stderr = %q; want jsonl-stderr", rawStderr)
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// drainSubprocessChan collects all events from ch with a generous timeout
// suitable for race-detected subprocess invocations. The 1-second limit used
// by drainWithTimeout is too short when the race detector is enabled, as the
// subprocess binary takes longer to initialize.
func drainSubprocessChan(t *testing.T, ch <-chan llmclient.Event) []llmclient.Event {
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

// assertNotUnderDir fails the test if path is inside parent.
func assertNotUnderDir(t *testing.T, path, parent string) {
	t.Helper()
	rel, err := filepath.Rel(parent, path)
	cleanRel := filepath.Clean(rel)
	if err == nil && (cleanRel == "." || (cleanRel != ".." && !strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)))) {
		t.Errorf("path %q is inside restricted directory %q", path, parent)
	}
}

// assertContainsEventType fails the test if no event with the given type is
// found in events.
func assertContainsEventType(t *testing.T, events []llmclient.Event, et llmclient.EventType) {
	t.Helper()
	for i := range events {
		if events[i].Type == et {
			return
		}
	}
	t.Errorf("no event of type %q found; event types: %v", et, eventTypes(events))
}
