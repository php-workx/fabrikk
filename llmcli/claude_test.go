package llmcli

import (
	"context"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// claudeBackendName is the registered name for the Claude backend.
const (
	claudeBackendName  = "claude"
	claudeTestModel    = "claude-opus-4-5"
	claudeThinkingText = "let me think"
)

// helloWorldText is used as a fixture text value in multiple tests.
const helloWorldText = "hello world"

// ─── buildClaudeArgs ─────────────────────────────────────────────────────────

// TestBuildClaudeArgs_ModelSystemPromptResume verifies that buildClaudeArgs
// produces the correct flag sequence for the common case: model override,
// system prompt, and session resume all set simultaneously.
func TestBuildClaudeArgs_ModelSystemPromptResume(t *testing.T) {
	input := &llmclient.Context{
		SystemPrompt: "You are helpful.",
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
	cfg.Model = claudeTestModel
	cfg.SessionID = "session-abc"

	args := buildClaudeArgs(input, cfg, false)

	want := map[string]string{
		"-p":              "What is 2+2?",
		"--output-format": "stream-json",
		"--system-prompt": "You are helpful.",
		"--model":         claudeTestModel,
		"--resume":        "session-abc",
	}
	assertFlagsPresent(t, args, want)
}

// TestBuildClaudeArgs_NoSystemPrompt verifies that --system-prompt is omitted
// when the input has an empty system prompt.
func TestBuildClaudeArgs_NoSystemPrompt(t *testing.T) {
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "hi"},
			}},
		},
	}
	cfg := llmclient.DefaultRequestConfig()
	args := buildClaudeArgs(input, cfg, false)

	if flagIndex(args, "--system-prompt") >= 0 {
		t.Error("--system-prompt should not be present when SystemPrompt is empty")
	}
}

// TestBuildClaudeArgs_OllamaModelOverrides verifies that when WithOllama sets
// a model, that model replaces cfg.Model in the --model flag.
func TestBuildClaudeArgs_OllamaModelOverrides(t *testing.T) {
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "hello"},
			}},
		},
	}
	cfg := llmclient.DefaultRequestConfig()
	cfg.Model = claudeTestModel
	cfg.Ollama = &llmclient.OllamaConfig{
		BaseURL: "http://localhost:11434",
		Model:   "glm-5:cloud",
	}

	args := buildClaudeArgs(input, cfg, false)

	// Ollama model should appear, not the original cfg.Model.
	idx := flagIndex(args, "--model")
	if idx < 0 || idx+1 >= len(args) {
		t.Fatal("--model flag missing")
	}
	if args[idx+1] != "glm-5:cloud" {
		t.Errorf("--model = %q, want %q", args[idx+1], "glm-5:cloud")
	}
}

// TestBuildClaudeArgs_UseStdin verifies that when useStdin is true the prompt
// is not embedded in the -p argument (empty string), so the caller can pipe it.
func TestBuildClaudeArgs_UseStdin(t *testing.T) {
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "large prompt text"},
			}},
		},
	}
	cfg := llmclient.DefaultRequestConfig()

	args := buildClaudeArgs(input, cfg, true)

	idx := flagIndex(args, "-p")
	if idx < 0 {
		t.Fatal("-p flag missing")
	}
	if idx+1 >= len(args) {
		t.Fatal("-p flag has no value")
	}
	if args[idx+1] != "" {
		t.Errorf("-p value = %q; want empty string when useStdin=true", args[idx+1])
	}

	// --output-format stream-json must always be present.
	if flagIndex(args, "--output-format") < 0 {
		t.Error("--output-format missing")
	}
}

// ─── buildClaudeEnv ──────────────────────────────────────────────────────────

// TestBuildClaudeEnv_Ollama verifies that ANTHROPIC_BASE_URL,
// ANTHROPIC_AUTH_TOKEN, and ANTHROPIC_API_KEY are injected when cfg.Ollama is
// set. OLLAMA_API_KEY is included only when APIKey is non-empty.
func TestBuildClaudeEnv_Ollama(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/root"}

	t.Run("LocalOllama", func(t *testing.T) {
		cfg := llmclient.DefaultRequestConfig()
		cfg.Ollama = &llmclient.OllamaConfig{
			BaseURL: "http://localhost:11434",
		}
		env := buildClaudeEnv(base, cfg)

		assertEnvContains(t, env, "ANTHROPIC_BASE_URL=http://localhost:11434")
		assertEnvContains(t, env, "ANTHROPIC_AUTH_TOKEN=ollama")
		assertEnvContains(t, env, "ANTHROPIC_API_KEY=")
		assertEnvAbsent(t, env)
	})

	t.Run("OllamaCloud", func(t *testing.T) {
		cfg := llmclient.DefaultRequestConfig()
		cfg.Ollama = &llmclient.OllamaConfig{
			BaseURL: "https://ollama.com",
			APIKey:  "mykey",
		}
		env := buildClaudeEnv(base, cfg)

		assertEnvContains(t, env, "ANTHROPIC_BASE_URL=https://ollama.com")
		assertEnvContains(t, env, "ANTHROPIC_AUTH_TOKEN=ollama")
		assertEnvContains(t, env, "OLLAMA_API_KEY=mykey")
	})

	t.Run("DefaultBaseURL", func(t *testing.T) {
		cfg := llmclient.DefaultRequestConfig()
		cfg.Ollama = &llmclient.OllamaConfig{} // empty BaseURL → default

		env := buildClaudeEnv(base, cfg)
		assertEnvContains(t, env, "ANTHROPIC_BASE_URL=http://localhost:11434")
	})

	t.Run("NoOllama", func(t *testing.T) {
		cfg := llmclient.DefaultRequestConfig()
		env := buildClaudeEnv(base, cfg)

		if len(env) != len(base) {
			t.Errorf("env length changed without Ollama: got %d, want %d", len(env), len(base))
		}
	})
}

// ─── Registry capabilities ───────────────────────────────────────────────────

// TestClaudeRegistry_StaticCapabilities verifies that the registered "claude"
// factory declares the correct static capabilities.
func TestClaudeRegistry_StaticCapabilities(t *testing.T) {
	f, ok := factoryByName(claudeBackendName)
	if !ok {
		t.Fatalf("%s backend not registered", claudeBackendName)
	}

	caps := f.Capabilities

	if caps.Backend != claudeBackendName {
		t.Errorf("Backend = %q, want %q", caps.Backend, claudeBackendName)
	}
	if caps.Streaming != llmclient.StreamingStructured {
		t.Errorf("Streaming = %q, want %q", caps.Streaming, llmclient.StreamingStructured)
	}
	if !caps.ToolEvents {
		t.Error("ToolEvents should be true")
	}
	if !caps.MultiTurn {
		t.Error("MultiTurn should be true")
	}
	if !caps.Thinking {
		t.Error("Thinking should be true")
	}
	if !caps.Usage {
		t.Error("Usage should be true")
	}
	if !caps.OllamaRouting {
		t.Error("OllamaRouting should be true")
	}
	if f.Preference != PreferClaude {
		t.Errorf("Preference = %v, want PreferClaude", f.Preference)
	}
	if f.Binary != claudeBackendName {
		t.Errorf("Binary = %q, want %q", f.Binary, claudeBackendName)
	}
	if f.Name != claudeBackendName {
		t.Errorf("Name = %q, want %q", f.Name, claudeBackendName)
	}

	wantOptions := []llmclient.OptionName{
		llmclient.OptionModel,
		llmclient.OptionSession,
		llmclient.OptionOllama,
	}
	for _, opt := range wantOptions {
		if caps.OptionSupport[opt] == "" {
			t.Errorf("OptionSupport[%q] missing", opt)
		}
	}
}

func TestClaudeInitFidelity_OnlyReportsPassedOptions(t *testing.T) {
	cfg := llmclient.DefaultRequestConfig()
	cfg.Model = "claude-sonnet"

	fidelity := claudeInitFidelity(cfg)
	if fidelity.OptionResults[llmclient.OptionModel] != llmclient.OptionApplied {
		t.Fatalf("OptionModel result = %q, want applied", fidelity.OptionResults[llmclient.OptionModel])
	}
	if _, ok := fidelity.OptionResults[llmclient.OptionSession]; ok {
		t.Fatal("OptionSession should be omitted when not passed")
	}
	if _, ok := fidelity.OptionResults[llmclient.OptionOllama]; ok {
		t.Fatal("OptionOllama should be omitted when not passed")
	}
}

// ─── Stream parser tests ─────────────────────────────────────────────────────

// TestClaudeStream_MapsTextEvents verifies that a Claude text block is mapped
// to text_start → text_delta → text_end, followed by a done terminal event.
func TestClaudeStream_MapsTextEvents(t *testing.T) {
	r := claudeJSONLReader(claudeTextSession("sid1", helloWorldText)...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	err := parseClaudeStream(context.Background(), r, ch, te, nil)
	if err != nil {
		t.Fatalf("parseClaudeStream: %v", err)
	}

	events := drainChannel(ch)

	// Expect: start, text_start, text_delta, text_end, done
	wantTypes := []llmclient.EventType{
		llmclient.EventStart,
		llmclient.EventTextStart,
		llmclient.EventTextDelta,
		llmclient.EventTextEnd,
		llmclient.EventDone,
	}
	assertEventSequence(t, events, wantTypes)

	// start event carries the session ID
	if events[0].SessionID != "sid1" {
		t.Errorf("start.SessionID = %q, want %q", events[0].SessionID, "sid1")
	}

	// text_delta carries the text
	if events[2].Delta != helloWorldText {
		t.Errorf("text_delta.Delta = %q, want %q", events[2].Delta, helloWorldText)
	}

	// text_end carries the full content
	if events[3].Content != helloWorldText {
		t.Errorf("text_end.Content = %q, want %q", events[3].Content, helloWorldText)
	}
}

// TestClaudeStream_MapsToolUse verifies that a tool_use block produces
// toolcall_start → toolcall_end events, each carrying the ToolCall details.
func TestClaudeStream_MapsToolUse(t *testing.T) {
	r := claudeJSONLReader(claudeToolUseSession("sid2", "toolu_01", "Read")...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	err := parseClaudeStream(context.Background(), r, ch, te, nil)
	if err != nil {
		t.Fatalf("parseClaudeStream: %v", err)
	}

	events := drainChannel(ch)

	// Locate toolcall_start
	startIdx := -1
	for i, ev := range events {
		if ev.Type == llmclient.EventToolCallStart {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		t.Fatal("no toolcall_start event emitted")
	}

	tcStart := events[startIdx]
	if tcStart.ToolCall == nil {
		t.Fatal("toolcall_start.ToolCall is nil")
	}
	if tcStart.ToolCall.ID != "toolu_01" {
		t.Errorf("ToolCall.ID = %q, want %q", tcStart.ToolCall.ID, "toolu_01")
	}
	if tcStart.ToolCall.Name != "Read" {
		t.Errorf("ToolCall.Name = %q, want %q", tcStart.ToolCall.Name, "Read")
	}

	// toolcall_end must follow immediately with the same ToolCall
	if startIdx+1 >= len(events) || events[startIdx+1].Type != llmclient.EventToolCallEnd {
		t.Error("toolcall_end not immediately after toolcall_start")
	}
	if events[startIdx+1].ToolCall == nil {
		t.Fatal("toolcall_end.ToolCall is nil")
	}
	if events[startIdx+1].ToolCall.ID != "toolu_01" {
		t.Errorf("toolcall_end.ToolCall.ID = %q, want %q", events[startIdx+1].ToolCall.ID, "toolu_01")
	}

	// Both start and end share the same ContentIndex.
	if tcStart.ContentIndex != events[startIdx+1].ContentIndex {
		t.Errorf("toolcall_start.ContentIndex(%d) != toolcall_end.ContentIndex(%d)",
			tcStart.ContentIndex, events[startIdx+1].ContentIndex)
	}
}

// TestClaudeStream_MapsThinkingEvents verifies that a thinking block is mapped
// to thinking_start → thinking_delta → thinking_end events.
func TestClaudeStream_MapsThinkingEvents(t *testing.T) {
	r := claudeJSONLReader(claudeThinkingSession("sid3", claudeThinkingText, "answer")...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	err := parseClaudeStream(context.Background(), r, ch, te, nil)
	if err != nil {
		t.Fatalf("parseClaudeStream: %v", err)
	}

	events := drainChannel(ch)

	wantTypes := []llmclient.EventType{
		llmclient.EventStart,
		llmclient.EventThinkingStart,
		llmclient.EventThinkingDelta,
		llmclient.EventThinkingEnd,
		llmclient.EventTextStart,
		llmclient.EventTextDelta,
		llmclient.EventTextEnd,
		llmclient.EventDone,
	}
	assertEventSequence(t, events, wantTypes)

	// thinking_delta carries the thinking text
	if events[2].Delta != claudeThinkingText {
		t.Errorf("thinking_delta.Delta = %q, want %q", events[2].Delta, claudeThinkingText)
	}
	// thinking_end carries the full content
	if events[3].Content != claudeThinkingText {
		t.Errorf("thinking_end.Content = %q, want %q", events[3].Content, claudeThinkingText)
	}
}

// TestClaudeStream_ErrorResultProducesErrorEvent verifies that a
// result/error_max_turns frame maps to an EventError terminal event.
func TestClaudeStream_ErrorResultProducesErrorEvent(t *testing.T) {
	r := claudeJSONLReader(claudeErrorSession("sid4", "error_max_turns")...)
	ch := make(chan llmclient.Event, 8)
	te := newTerminalEmitter(ch)

	err := parseClaudeStream(context.Background(), r, ch, te, nil)
	if err != nil {
		t.Fatalf("parseClaudeStream: unexpected non-nil error: %v", err)
	}

	events := drainChannel(ch)

	if len(events) < 1 {
		t.Fatal("no events emitted")
	}
	last := events[len(events)-1]
	if last.Type != llmclient.EventError {
		t.Errorf("last event.Type = %q, want %q", last.Type, llmclient.EventError)
	}
	if last.ErrorMessage == "" {
		t.Error("EventError.ErrorMessage is empty")
	}
}

// TestClaudeStream_ExactlyOneTerminalEvent verifies that each of the session
// scenarios produces exactly one terminal event (done or error) and that it is
// always the last event in the channel.
func TestClaudeStream_ExactlyOneTerminalEvent(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
	}{
		{"text_success", claudeTextSession("s1", "hello")},
		{"tool_use", claudeToolUseSession("s2", "toolu_01", "Bash")},
		{"thinking", claudeThinkingSession("s3", "think", "answer")},
		{"error_max_turns", claudeErrorSession("s4", "error_max_turns")},
		{"error_during_execution", claudeErrorSession("s5", "error_during_execution")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := claudeJSONLReader(tc.lines...)
			ch := make(chan llmclient.Event, 64)
			te := newTerminalEmitter(ch)

			err := parseClaudeStream(context.Background(), r, ch, te, nil)
			if err != nil {
				t.Fatalf("parseClaudeStream: %v", err)
			}

			events := drainChannel(ch)
			if len(events) == 0 {
				t.Fatal("no events emitted")
			}

			var terminalCount int
			for _, ev := range events {
				if ev.Type == llmclient.EventDone || ev.Type == llmclient.EventError {
					terminalCount++
				}
			}
			if terminalCount != 1 {
				t.Errorf("terminal event count = %d, want exactly 1", terminalCount)
			}

			// Terminal event must be last.
			last := events[len(events)-1]
			if last.Type != llmclient.EventDone && last.Type != llmclient.EventError {
				t.Errorf("last event type = %q; want done or error", last.Type)
			}
		})
	}
}

// TestClaudeStream_ContentIndexIncrementsAcrossBlocks verifies that each
// content block gets an incremented ContentIndex, so multi-block sessions
// can be correlated by the consumer.
func TestClaudeStream_ContentIndexIncrementsAcrossBlocks(t *testing.T) {
	// thinking block (idx 0) + text block (idx 1)
	r := claudeJSONLReader(claudeThinkingSession("s1", "think", "text")...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	_ = parseClaudeStream(context.Background(), r, ch, te, nil)
	events := drainChannel(ch)

	thinkStart := findEvent(t, events, llmclient.EventThinkingStart)
	textStart := findEvent(t, events, llmclient.EventTextStart)

	if thinkStart.ContentIndex != 0 {
		t.Errorf("thinking block ContentIndex = %d, want 0", thinkStart.ContentIndex)
	}
	if textStart.ContentIndex != 1 {
		t.Errorf("text block ContentIndex = %d, want 1", textStart.ContentIndex)
	}
}

// TestClaudeStream_MalformedJSONLineIsSkipped verifies that a malformed JSONL
// line between valid frames does not abort the stream; parsing continues.
func TestClaudeStream_MalformedJSONLineIsSkipped(t *testing.T) {
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"s1","model":"claude-opus-4-5"}`,
		`not valid json at all {{{`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"result","subtype":"success","total_cost_usd":0.001}`,
	}
	r := claudeJSONLReader(lines...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	err := parseClaudeStream(context.Background(), r, ch, te, nil)
	if err != nil {
		t.Fatalf("parseClaudeStream: unexpected error on malformed line: %v", err)
	}

	events := drainChannel(ch)
	last := events[len(events)-1]
	if last.Type != llmclient.EventDone {
		t.Errorf("last event = %q, want %q (malformed line should be skipped)", last.Type, llmclient.EventDone)
	}
}

// TestClaudeStream_AssembledMessageOnDone verifies that the AssistantMessage
// on the done event accumulates all text blocks emitted during the session.
func TestClaudeStream_AssembledMessageOnDone(t *testing.T) {
	r := claudeJSONLReader(claudeTextSession("s1", helloWorldText)...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	_ = parseClaudeStream(context.Background(), r, ch, te, nil)
	events := drainChannel(ch)

	done := findEvent(t, events, llmclient.EventDone)
	if done.Message == nil {
		t.Fatal("done.Message is nil")
	}
	if len(done.Message.Content) == 0 {
		t.Fatal("done.Message.Content is empty")
	}
	block := done.Message.Content[0]
	if block.Type != llmclient.ContentText {
		t.Errorf("Content[0].Type = %q, want %q", block.Type, llmclient.ContentText)
	}
	if block.Text != helloWorldText {
		t.Errorf("Content[0].Text = %q, want %q", block.Text, helloWorldText)
	}
}

// TestClaudeStream_Cancel verifies that cancelling the context closes the
// event channel within a reasonable time (the subprocess is killed).
func TestClaudeStream_Cancel(t *testing.T) {
	exe := testExecutable(t)

	// Use the "sleep" fixture: it hangs indefinitely, producing no stdout.
	// The ClaudeBackend will spawn it and block on ReadBoundedLine. Cancellation
	// must terminate the subprocess and close the channel.
	b := NewClaudeBackend(CliInfo{
		Name:    "claude",
		Binary:  "claude",
		Path:    exe,
		Version: "0.0.0-test",
	})

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

	// Cancel the context so the subprocess is killed.
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
		t.Error("channel was not closed within 5s after context cancellation")
	}
}

// TestClaudeStream_RequiredUnsupportedOptionReturnsError verifies that when
// the caller marks an unsupported option as required, Stream returns an error
// before spawning any subprocess.
func TestClaudeStream_RequiredUnsupportedOptionReturnsError(t *testing.T) {
	b := NewClaudeBackend(CliInfo{Path: "/does/not/matter"})
	input := &llmclient.Context{}

	_, err := b.Stream(
		context.Background(),
		input,
		llmclient.WithRequiredOptions(llmclient.OptionHostTools),
	)
	if err == nil {
		t.Fatal("expected error for required unsupported option, got nil")
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// assertFlagsPresent verifies that every key/value pair in want appears
// consecutively in args (key at index i, value at i+1).
func assertFlagsPresent(t *testing.T, args []string, want map[string]string) {
	t.Helper()
	for flag, wantVal := range want {
		idx := flagIndex(args, flag)
		if idx < 0 {
			t.Errorf("flag %q not found in args %v", flag, args)
			continue
		}
		if idx+1 >= len(args) {
			t.Errorf("flag %q has no value in args %v", flag, args)
			continue
		}
		if args[idx+1] != wantVal {
			t.Errorf("flag %q value = %q, want %q", flag, args[idx+1], wantVal)
		}
	}
}

// flagIndex returns the index of flag in args, or -1 if not found.
func flagIndex(args []string, flag string) int {
	for i, a := range args {
		if a == flag {
			return i
		}
	}
	return -1
}

// assertEnvContains verifies that entry appears in env.
func assertEnvContains(t *testing.T, env []string, entry string) {
	t.Helper()
	for _, e := range env {
		if e == entry {
			return
		}
	}
	t.Errorf("env does not contain %q; env = %v", entry, env)
}

// assertEnvAbsent verifies that no entry with the given prefix appears in env.
func assertEnvAbsent(t *testing.T, env []string) {
	t.Helper()
	const prefix = "OLLAMA_API_KEY="
	for _, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			t.Errorf("env unexpectedly contains %q", e)
			return
		}
	}
}

// assertEventSequence verifies that the event types in events match wantTypes
// exactly, in order.
func assertEventSequence(t *testing.T, events []llmclient.Event, wantTypes []llmclient.EventType) {
	t.Helper()
	if len(events) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d\ngot types:  %v\nwant types: %v",
			len(events), len(wantTypes), eventTypes(events), wantTypes)
		return
	}
	for i, wt := range wantTypes {
		if events[i].Type != wt {
			t.Errorf("events[%d].Type = %q, want %q", i, events[i].Type, wt)
		}
	}
}

// eventTypes returns the Type field of each event for use in error messages.
func eventTypes(events []llmclient.Event) []llmclient.EventType {
	types := make([]llmclient.EventType, len(events))
	for i := range events {
		types[i] = events[i].Type
	}
	return types
}

// findEvent returns the first event with the given type, failing the test if
// none is found.
func findEvent(t *testing.T, events []llmclient.Event, et llmclient.EventType) llmclient.Event {
	t.Helper()
	for i := range events {
		if events[i].Type == et {
			return events[i]
		}
	}
	t.Fatalf("event type %q not found in events: %v", et, eventTypes(events))
	return llmclient.Event{}
}
