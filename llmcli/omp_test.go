package llmcli

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// ompJSONLReader returns a *bufio.Reader that yields each line as a
// '\n'-terminated JSONL frame followed by io.EOF. Used to drive
// parseOmpStream / parseOmpRPCTurn without spawning a real subprocess.
func ompJSONLReader(lines ...string) *bufio.Reader {
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	return bufio.NewReader(strings.NewReader(sb.String()))
}

// ompTextSession returns a minimal valid omp JSONL session with a single text
// block and a success done event.
func ompTextSession(sessionID, text string) []string {
	return []string{
		`{"type":"ready","session_id":"` + sessionID + `"}`,
		`{"type":"text_delta","content":"` + escapeJSON(text) + `"}`,
		`{"type":"done","message":{"id":"msg1","model":"opus","usage":{"inputTokens":10,"outputTokens":5}}}`,
	}
}

// ompThinkingSession returns an omp JSONL session with a thinking block
// followed by a text block.
func ompThinkingSession(sessionID, thinking, text string) []string {
	return []string{
		`{"type":"ready","session_id":"` + sessionID + `"}`,
		`{"type":"thinking_delta","content":"` + escapeJSON(thinking) + `"}`,
		`{"type":"thinking_end","content":"` + escapeJSON(thinking) + `"}`,
		`{"type":"text_delta","content":"` + escapeJSON(text) + `"}`,
		`{"type":"done","message":{"id":"msg2","model":"opus"}}`,
	}
}

// ompToolUseSession returns an omp JSONL session with a tool call followed
// by a text response.
func ompToolUseSession(sessionID, toolID, toolName string) []string {
	return []string{
		`{"type":"ready","session_id":"` + sessionID + `"}`,
		`{"type":"toolcall_start","toolCall":{"id":"` + toolID + `","name":"` + toolName + `"}}`,
		`{"type":"toolcall_end","toolCall":{"id":"` + toolID + `","name":"` + toolName + `","arguments":{"path":"/tmp"}}}`,
		`{"type":"text_delta","content":"done"}`,
		`{"type":"done","message":{"id":"msg3","model":"opus"}}`,
	}
}

// ompErrorSession returns an omp JSONL session that terminates with an error.
func ompErrorSession(sessionID, errMsg string) []string {
	return []string{
		`{"type":"ready","session_id":"` + sessionID + `"}`,
		`{"type":"error","error":{"message":"` + escapeJSON(errMsg) + `"}}`,
	}
}

// ─── TestOmpPrint_MapsJSONLEvents ─────────────────────────────────────────────

// TestOmpPrint_MapsJSONLEvents verifies that omp JSONL events are mapped to
// the correct normalized llmclient.Event sequence (Criterion 1).
func TestOmpPrint_MapsJSONLEvents(t *testing.T) {
	t.Run("TextSession", testOmpTextSessionMapping)
	t.Run("ThinkingSession", testOmpThinkingSessionMapping)
	t.Run("ToolUseSession", testOmpToolUseSessionMapping)
	t.Run("ErrorSession", testOmpErrorSessionMapping)
}

func testOmpTextSessionMapping(t *testing.T) {
	t.Helper()
	events := parseOmpEventsForTest(t, ompTextSession("sid1", helloWorldText), 32)

	// EventStart is now emitted by structuredStream, not by the parser.
	// Parser-level tests see only the content events and the terminal.
	wantTypes := []llmclient.EventType{
		llmclient.EventTextStart,
		llmclient.EventTextDelta,
		llmclient.EventTextEnd,
		llmclient.EventDone,
	}
	assertEventSequence(t, events, wantTypes)

	if events[1].Delta != helloWorldText {
		t.Errorf("text_delta.Delta = %q, want %q", events[1].Delta, helloWorldText)
	}
	if events[2].Content != helloWorldText {
		t.Errorf("text_end.Content = %q, want %q", events[2].Content, helloWorldText)
	}
}

func testOmpThinkingSessionMapping(t *testing.T) {
	t.Helper()
	const thinkingText = "let me think"
	events := parseOmpEventsForTest(t, ompThinkingSession("sid2", thinkingText, "answer"), 32)

	// EventStart is emitted by structuredStream, not the parser.
	wantTypes := []llmclient.EventType{
		llmclient.EventThinkingStart,
		llmclient.EventThinkingDelta,
		llmclient.EventThinkingEnd,
		llmclient.EventTextStart,
		llmclient.EventTextDelta,
		llmclient.EventTextEnd,
		llmclient.EventDone,
	}
	assertEventSequence(t, events, wantTypes)

	thinkEnd := findEvent(t, events, llmclient.EventThinkingEnd)
	if thinkEnd.Content != thinkingText {
		t.Errorf("thinking_end.Content = %q, want %q", thinkEnd.Content, thinkingText)
	}
}

func testOmpToolUseSessionMapping(t *testing.T) {
	t.Helper()
	events := parseOmpEventsForTest(t, ompToolUseSession("sid3", "toolu_01", "Read"), 32)

	startIdx := -1
	for i := range events {
		if events[i].Type == llmclient.EventToolCallStart {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		t.Fatal("no toolcall_start event emitted")
	}
	if events[startIdx].ToolCall == nil {
		t.Fatal("toolcall_start.ToolCall is nil")
	}
	if events[startIdx].ToolCall.Name != "Read" {
		t.Errorf("toolcall_start.ToolCall.Name = %q, want %q", events[startIdx].ToolCall.Name, "Read")
	}
	if startIdx+1 >= len(events) || events[startIdx+1].Type != llmclient.EventToolCallEnd {
		t.Error("toolcall_end not immediately after toolcall_start")
	}
}

func testOmpErrorSessionMapping(t *testing.T) {
	t.Helper()
	events := parseOmpEventsForTest(t, ompErrorSession("sid4", "context limit reached"), 8)
	if len(events) == 0 {
		t.Fatal("no events emitted")
	}
	last := events[len(events)-1]
	if last.Type != llmclient.EventError {
		t.Errorf("last event.Type = %q, want EventError", last.Type)
	}
	if last.ErrorMessage == "" {
		t.Error("EventError.ErrorMessage is empty")
	}
}

func parseOmpEventsForTest(t *testing.T, lines []string, bufSize int) []llmclient.Event {
	t.Helper()
	r := ompJSONLReader(lines...)
	ch := make(chan llmclient.Event, bufSize)
	te := newTerminalEmitter(ch)

	err := parseOmpStream(context.Background(), r, ch, te)
	if err != nil {
		t.Fatalf("parseOmpStream: %v", err)
	}

	return drainChannel(ch)
}

// TestOmpPrint_ExactlyOneTerminalEvent verifies that each session scenario
// produces exactly one terminal event (done or error) as the last event.
func TestOmpPrint_ExactlyOneTerminalEvent(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
	}{
		{"text", ompTextSession("s1", "hello")},
		{"thinking", ompThinkingSession("s2", "think", "answer")},
		{"tool_use", ompToolUseSession("s3", "toolu_01", "Bash")},
		{"error", ompErrorSession("s4", "some error")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ompJSONLReader(tc.lines...)
			ch := make(chan llmclient.Event, 64)
			te := newTerminalEmitter(ch)

			if err := parseOmpStream(context.Background(), r, ch, te); err != nil {
				t.Fatalf("parseOmpStream: %v", err)
			}
			events := drainChannel(ch)
			if len(events) == 0 {
				t.Fatal("no events emitted")
			}
			var count int
			for _, ev := range events {
				if ev.Type == llmclient.EventDone || ev.Type == llmclient.EventError {
					count++
				}
			}
			if count != 1 {
				t.Errorf("terminal event count = %d, want 1", count)
			}
			last := events[len(events)-1]
			if last.Type != llmclient.EventDone && last.Type != llmclient.EventError {
				t.Errorf("last event = %q; want done or error", last.Type)
			}
		})
	}
}

// TestOmpPrint_ContentIndexIncrementsAcrossBlocks verifies that each content
// block receives an incrementing ContentIndex.
func TestOmpPrint_ContentIndexIncrementsAcrossBlocks(t *testing.T) {
	// thinking (idx 0) then text (idx 1)
	r := ompJSONLReader(ompThinkingSession("s1", "think", "text")...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	if err := parseOmpStream(context.Background(), r, ch, te); err != nil {
		t.Fatalf("parseOmpStream: %v", err)
	}
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

// TestOmpPrint_MalformedJSONLineSkipped verifies that malformed JSONL lines
// between valid frames do not abort the stream.
func TestOmpPrint_MalformedJSONLineSkipped(t *testing.T) {
	lines := []string{
		`{"type":"ready","session_id":"s1"}`,
		`not valid json {{{`,
		`{"type":"text_delta","content":"hello"}`,
		`{"type":"done","message":{"id":"m1"}}`,
	}
	r := ompJSONLReader(lines...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	err := parseOmpStream(context.Background(), r, ch, te)
	if err != nil {
		t.Fatalf("parseOmpStream: unexpected error: %v", err)
	}
	events := drainChannel(ch)
	last := events[len(events)-1]
	if last.Type != llmclient.EventDone {
		t.Errorf("last event = %q; want done (malformed line should be skipped)", last.Type)
	}
}

func TestOmpPrint_EOFBeforeTerminalFrameEmitsSyntheticDone(t *testing.T) {
	r := ompJSONLReader(
		`{"type":"ready","session_id":"s1"}`,
		`{"type":"text_delta","content":"partial"}`,
	)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	err := parseOmpStream(context.Background(), r, ch, te)
	if err != nil {
		t.Fatalf("parseOmpStream error = %v; want nil", err)
	}

	events := drainChannel(ch)
	done := findEvent(t, events, llmclient.EventDone)
	if done.Reason != llmclient.StopEndTurn {
		t.Fatalf("done.Reason = %v; want StopEndTurn", done.Reason)
	}
}

// TestOmpPrint_ContentBeforeReadyReturnsUnexpectedEOF verifies that a stream
// delivering bytes without a ready frame returns io.ErrUnexpectedEOF instead
// of emitting a synthetic done with no preceding start event.
func TestOmpPrint_ContentBeforeReadyReturnsUnexpectedEOF(t *testing.T) {
	r := ompJSONLReader(
		`{"type":"text_delta","content":"hook output"}`,
	)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	err := parseOmpStream(context.Background(), r, ch, te)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("parseOmpStream error = %v; want io.ErrUnexpectedEOF", err)
	}
}

// TestOmpPrint_AssembledMessageOnDone verifies that the AssistantMessage
// on the done event accumulates text blocks emitted during the session.
func TestOmpPrint_AssembledMessageOnDone(t *testing.T) {
	r := ompJSONLReader(ompTextSession("s1", helloWorldText)...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	if err := parseOmpStream(context.Background(), r, ch, te); err != nil {
		t.Fatalf("parseOmpStream: %v", err)
	}
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
		t.Errorf("Content[0].Type = %q, want text", block.Type)
	}
	if block.Text != helloWorldText {
		t.Errorf("Content[0].Text = %q, want %q", block.Text, helloWorldText)
	}
}

// TestOmpPrint_UsageOnDone verifies that token usage from the done frame
// is propagated to the done event.
func TestOmpPrint_UsageOnDone(t *testing.T) {
	lines := []string{
		`{"type":"ready","session_id":"s1"}`,
		`{"type":"text_delta","content":"hi"}`,
		`{"type":"done","message":{"id":"m1","usage":{"inputTokens":15,"outputTokens":30}}}`,
	}
	r := ompJSONLReader(lines...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	if err := parseOmpStream(context.Background(), r, ch, te); err != nil {
		t.Fatalf("parseOmpStream: %v", err)
	}
	events := drainChannel(ch)

	done := findEvent(t, events, llmclient.EventDone)
	if done.Usage == nil {
		t.Fatal("done.Usage is nil; want non-nil usage")
	}
	if done.Usage.InputTokens != 15 {
		t.Errorf("Usage.InputTokens = %d, want 15", done.Usage.InputTokens)
	}
	if done.Usage.OutputTokens != 30 {
		t.Errorf("Usage.OutputTokens = %d, want 30", done.Usage.OutputTokens)
	}
}

// TestOmpPrint_MultipleTextDeltas verifies that multiple text_delta events in
// a row are accumulated into a single text block with the correct concatenation.
func TestOmpPrint_MultipleTextDeltas(t *testing.T) {
	lines := []string{
		`{"type":"ready","session_id":"s1"}`,
		`{"type":"text_delta","content":"hello "}`,
		`{"type":"text_delta","content":"world"}`,
		`{"type":"done","message":{"id":"m1"}}`,
	}
	r := ompJSONLReader(lines...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	if err := parseOmpStream(context.Background(), r, ch, te); err != nil {
		t.Fatalf("parseOmpStream: %v", err)
	}
	events := drainChannel(ch)

	// Verify the text_end event carries the accumulated content.
	textEnd := findEvent(t, events, llmclient.EventTextEnd)
	if textEnd.Content != "hello world" {
		t.Errorf("text_end.Content = %q, want %q", textEnd.Content, "hello world")
	}

	// Verify the done message content reflects the full text.
	done := findEvent(t, events, llmclient.EventDone)
	if done.Message == nil || len(done.Message.Content) == 0 {
		t.Fatal("done.Message has no content blocks")
	}
	if done.Message.Content[0].Text != "hello world" {
		t.Errorf("assembled text = %q, want %q", done.Message.Content[0].Text, "hello world")
	}
}

// ─── TestOmpPrint_DoesNotSatisfyHostToolRequirements ──────────────────────────

// TestOmpPrint_DoesNotSatisfyHostToolRequirements verifies that the omp print
// backend's static capabilities do NOT declare host tool support (Criterion 4).
func TestOmpPrint_DoesNotSatisfyHostToolRequirements(t *testing.T) {
	caps := ompPrintStaticCapabilities("")

	if caps.HostToolDefs {
		t.Error("omp print: HostToolDefs should be false")
	}
	if caps.HostToolApproval {
		t.Error("omp print: HostToolApproval should be false")
	}
	if caps.ToolResultInjection {
		t.Error("omp print: ToolResultInjection should be false")
	}
}

// TestOmpPrint_RequiredHostToolsOptionErrors verifies that Stream returns an
// error before spawning when OptionHostTools is marked required.
func TestOmpPrint_RequiredHostToolsOptionErrors(t *testing.T) {
	b := NewOmpBackend(CliInfo{Path: "/does/not/matter"})
	_, err := b.Stream(
		context.Background(),
		&llmclient.Context{},
		llmclient.WithRequiredOptions(llmclient.OptionHostTools),
	)
	if err == nil {
		t.Fatal("expected error for required OptionHostTools, got nil")
	}
	if !errors.Is(err, llmclient.ErrUnsupportedOption) {
		t.Fatalf("Stream error = %v; want ErrUnsupportedOption", err)
	}
}

// ─── Registry / static capability tests ──────────────────────────────────────

// TestOmpRegistry_StaticCapabilities verifies the registered "omp" factory
// declares the correct static capabilities.
func TestOmpRegistry_StaticCapabilities(t *testing.T) {
	f, ok := factoryByName("omp")
	if !ok {
		t.Fatal("omp backend not registered")
	}
	caps := f.Capabilities
	if caps.Backend != "omp" {
		t.Errorf("Backend = %q, want %q", caps.Backend, "omp")
	}
	if caps.Streaming != llmclient.StreamingStructured {
		t.Errorf("Streaming = %q, want structured", caps.Streaming)
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
	if caps.HostToolDefs {
		t.Error("HostToolDefs should be false for omp print")
	}
	if caps.HostToolApproval {
		t.Error("HostToolApproval should be false for omp print")
	}
	if caps.ToolResultInjection {
		t.Error("ToolResultInjection should be false for omp print")
	}
	if f.Preference != PreferOmp {
		t.Errorf("Preference = %v, want PreferOmp", f.Preference)
	}
}

// TestOmpPrint_BuildArgs verifies buildOmpPrintArgs produces the correct flags.
func TestOmpPrint_BuildArgs(t *testing.T) {
	input := &llmclient.Context{
		SystemPrompt: "You are helpful.",
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "What is 2+2?"},
			}},
		},
	}
	cfg := llmclient.DefaultRequestConfig()
	cfg.Model = "opus"
	cfg.SessionID = "sess-abc"

	args := buildOmpPrintArgs(input, cfg)

	wantFlags := map[string]string{
		"-p":              "What is 2+2?",
		"--mode":          "json",
		"--system-prompt": "You are helpful.",
		"--model":         "opus",
		"--resume":        "sess-abc",
	}
	assertFlagsPresent(t, args, wantFlags)
}

// ─── fab-o4la: Ollama routing ─────────────────────────────────────────────────

// TestOmpPrint_Ollama_InjectsEnv verifies that when WithOllama is set,
// claudeOllamaEnvOverrides returns a map containing ANTHROPIC_BASE_URL, which
// is the env variable used to route omp (print) through an Ollama endpoint.
func TestOmpPrint_Ollama_InjectsEnv(t *testing.T) {
	cfg := llmclient.DefaultRequestConfig()
	cfg.Ollama = &llmclient.OllamaConfig{BaseURL: "http://localhost:11434"}

	overrides := claudeOllamaEnvOverrides(cfg)
	if _, ok := overrides["ANTHROPIC_BASE_URL"]; !ok {
		t.Errorf("ANTHROPIC_BASE_URL missing from overrides; got %v", overrides)
	}
}

// TestOmpPrint_Ollama_NilWhenUnset verifies that claudeOllamaEnvOverrides
// returns nil when no Ollama config is present, so no spurious env vars are
// injected into normal (non-Ollama) omp runs.
func TestOmpPrint_Ollama_NilWhenUnset(t *testing.T) {
	cfg := llmclient.DefaultRequestConfig()
	if got := claudeOllamaEnvOverrides(cfg); got != nil {
		t.Errorf("expected nil overrides when Ollama not set, got %v", got)
	}
}

// TestOmpRegistry_StaticCapabilities_Ollama verifies that the registered
// omp capabilities declare OllamaRouting=true and OptionOllama support.
func TestOmpRegistry_StaticCapabilities_Ollama(t *testing.T) {
	f, ok := factoryByName("omp")
	if !ok {
		t.Fatal("omp backend not registered")
	}
	if !f.Capabilities.OllamaRouting {
		t.Error("OllamaRouting should be true for omp")
	}
	if f.Capabilities.OptionSupport[llmclient.OptionOllama] == "" {
		t.Error("OptionSupport[OptionOllama] should be set for omp")
	}
}

// TestOmpPrint_RequiredOllamaOptionAccepted verifies that requiring OptionOllama
// is accepted (backend supports it fully).
func TestOmpPrint_RequiredOllamaOptionAccepted(t *testing.T) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), []llmclient.Option{
		llmclient.WithOllama(llmclient.OllamaConfig{BaseURL: "http://localhost:11434"}),
		llmclient.WithRequiredOptions(llmclient.OptionOllama),
	})
	if err := checkOmpPrintRequiredOptions(cfg); err != nil {
		t.Errorf("checkOmpPrintRequiredOptions rejected OptionOllama: %v", err)
	}
}

// TestOmpPrint_BuildArgs_NoSystemPrompt verifies that --system-prompt is omitted
// when the input has an empty system prompt.
func TestOmpPrint_BuildArgs_NoSystemPrompt(t *testing.T) {
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "hi"},
			}},
		},
	}
	args := buildOmpPrintArgs(input, llmclient.DefaultRequestConfig())
	if flagIndex(args, "--system-prompt") >= 0 {
		t.Error("--system-prompt should not appear when SystemPrompt is empty")
	}
}

// ─── fab-omss: structuredStream delegation ────────────────────────────────────

// TestOmpStream_ObserverFires verifies that when Stream delegates to
// structuredStream, the DefaultObserver hooks fire correctly:
//   - OnStreamStart once
//   - OnEventEmitted at least twice (EventStart + EventDone minimum)
//   - OnStreamEnd once with success=true
//
// The test uses the "omp_jsonl" fixture (registered in subprocess_test.go
// TestMain) to drive a full omp print-mode session without a real omp binary.
func TestOmpStream_ObserverFires(t *testing.T) {
	spy := &spyObserver{}
	orig := DefaultObserver
	DefaultObserver = spy
	t.Cleanup(func() { DefaultObserver = orig })

	exe := testExecutable(t)

	b := NewOmpBackend(CliInfo{
		Name:    "omp",
		Binary:  "omp",
		Path:    exe,
		Version: "0.0.0-test",
	})

	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "hello"},
			}},
		},
	}

	// Point the backend at the test executable with the omp_jsonl fixture env.
	// We override the executable path via CliInfo and inject the fixture env
	// through WithEnvironment so that baseEnv() stripping is applied correctly.
	ch, err := b.Stream(
		context.Background(),
		input,
		llmclient.WithEnvironment(
			append(baseEnv(), "LLMCLI_TEST_FIXTURE=omp_jsonl"),
		),
	)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := waitForEvents(t, ch, 5*time.Second)

	// Verify observer was invoked correctly.
	spy.mu.Lock()
	starts := spy.starts
	ends := spy.ends
	eventTypes := spy.eventTypes
	spy.mu.Unlock()

	if starts != 1 {
		t.Errorf("OnStreamStart called %d time(s), want 1", starts)
	}
	if len(ends) != 1 {
		t.Errorf("OnStreamEnd called %d time(s), want 1", len(ends))
	} else if !ends[0].success {
		t.Errorf("OnStreamEnd success=%v, want true", ends[0].success)
	}
	if len(eventTypes) < 2 {
		t.Errorf("OnEventEmitted called %d time(s), want >= 2 (at least EventStart + EventDone)", len(eventTypes))
	}

	// Sanity-check the stream itself.
	if len(events) == 0 {
		t.Fatal("no events received from Stream")
	}
	first := events[0]
	if first.Type != llmclient.EventStart {
		t.Errorf("events[0].Type = %v, want EventStart", first.Type)
	}
	last := events[len(events)-1]
	if last.Type != llmclient.EventDone {
		t.Errorf("last event.Type = %v, want EventDone", last.Type)
	}
	if last.Reason != llmclient.StopEndTurn {
		t.Errorf("last event.Reason = %v, want StopEndTurn", last.Reason)
	}
}
