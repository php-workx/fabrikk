package llmcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// ─── Command encoding helpers ─────────────────────────────────────────────────

// newEncoderBuf returns a json.Encoder that writes into the returned buffer.
// Tests use this to verify command JSON without spawning a real process.
func newEncoderBuf() (*json.Encoder, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return json.NewEncoder(buf), buf
}

// decodeLines decodes all newline-separated JSON objects from b into a slice
// of maps. Stops at the first blank line or decode error.
func decodeLines(b *bytes.Buffer) []map[string]interface{} {
	var result []map[string]interface{}
	dec := json.NewDecoder(b)
	for dec.More() {
		var m map[string]interface{}
		if err := dec.Decode(&m); err != nil {
			break
		}
		result = append(result, m)
	}
	return result
}

// ─── TestOmpRPC_SendHostTools ─────────────────────────────────────────────────

// TestOmpRPC_SendHostTools verifies that sendSetHostTools encodes the
// set_host_tools command with the correct type and tools payload (Criterion 2).
func TestOmpRPC_SendHostTools(t *testing.T) {
	enc, buf := newEncoderBuf()
	proc := &ompRPCProc{enc: enc}

	tools := []llmclient.Tool{
		{Name: "ReadFile", Description: "read a file", InputSchema: map[string]interface{}{"type": "object"}},
		{Name: "Bash", Description: "run bash"},
	}

	if err := sendSetHostTools(proc, tools); err != nil {
		t.Fatalf("sendSetHostTools: %v", err)
	}

	msgs := decodeLines(buf)
	if len(msgs) != 1 {
		t.Fatalf("want 1 encoded line, got %d", len(msgs))
	}

	msg := msgs[0]
	if msg["type"] != "set_host_tools" {
		t.Errorf("type = %q, want set_host_tools", msg["type"])
	}
	toolsVal, ok := msg["tools"].([]interface{})
	if !ok {
		t.Fatalf("tools field missing or wrong type: %v", msg["tools"])
	}
	if len(toolsVal) != 2 {
		t.Errorf("tools length = %d, want 2", len(toolsVal))
	}
	first, ok := toolsVal[0].(map[string]interface{})
	if !ok {
		t.Fatal("first tool is not a JSON object")
	}
	if first["name"] != "ReadFile" {
		t.Errorf("tools[0].name = %q, want ReadFile", first["name"])
	}
}

// ─── TestOmpRPC_Abort ─────────────────────────────────────────────────────────

// TestOmpRPC_Abort verifies that sendAbort encodes the abort command
// correctly (Criterion 2).
func TestOmpRPC_Abort(t *testing.T) {
	enc, buf := newEncoderBuf()
	proc := &ompRPCProc{enc: enc}

	if err := sendAbort(proc); err != nil {
		t.Fatalf("sendAbort: %v", err)
	}

	msgs := decodeLines(buf)
	if len(msgs) != 1 {
		t.Fatalf("want 1 encoded line, got %d", len(msgs))
	}
	if msgs[0]["type"] != "abort" {
		t.Errorf("type = %q, want abort", msgs[0]["type"])
	}
	// abort has no extra fields
	if _, hasText := msgs[0]["text"]; hasText {
		t.Error("abort command should not have a text field")
	}
}

// TestOmpRPC_SendPrompt verifies that sendPrompt encodes the prompt command
// with the correct text field.
func TestOmpRPC_SendPrompt(t *testing.T) {
	enc, buf := newEncoderBuf()
	proc := &ompRPCProc{enc: enc}

	const promptText = "What is the capital of France?"
	if err := sendPrompt(proc, promptText); err != nil {
		t.Fatalf("sendPrompt: %v", err)
	}

	msgs := decodeLines(buf)
	if len(msgs) != 1 {
		t.Fatalf("want 1 encoded line, got %d", len(msgs))
	}
	if msgs[0]["type"] != "prompt" {
		t.Errorf("type = %q, want prompt", msgs[0]["type"])
	}
	if msgs[0]["text"] != promptText {
		t.Errorf("text = %q, want %q", msgs[0]["text"], promptText)
	}
}

// TestOmpRPC_SendSetModel verifies that sendSetModel encodes the set_model
// command with the correct model field.
func TestOmpRPC_SendSetModel(t *testing.T) {
	enc, buf := newEncoderBuf()
	proc := &ompRPCProc{enc: enc}

	if err := sendSetModel(proc, "claude-opus-4-5"); err != nil {
		t.Fatalf("sendSetModel: %v", err)
	}

	msgs := decodeLines(buf)
	if len(msgs) != 1 {
		t.Fatalf("want 1 encoded line, got %d", len(msgs))
	}
	if msgs[0]["type"] != "set_model" {
		t.Errorf("type = %q, want set_model", msgs[0]["type"])
	}
	if msgs[0]["model"] != "claude-opus-4-5" {
		t.Errorf("model = %q, want claude-opus-4-5", msgs[0]["model"])
	}
}

// TestOmpRPC_SendCompact verifies that sendCompact encodes the compact command.
func TestOmpRPC_SendCompact(t *testing.T) {
	enc, buf := newEncoderBuf()
	proc := &ompRPCProc{enc: enc}

	if err := sendCompact(proc); err != nil {
		t.Fatalf("sendCompact: %v", err)
	}

	msgs := decodeLines(buf)
	if len(msgs) != 1 {
		t.Fatalf("want 1 encoded line, got %d", len(msgs))
	}
	if msgs[0]["type"] != "compact" {
		t.Errorf("type = %q, want compact", msgs[0]["type"])
	}
}

// ─── TestOmpRPC_SerializesTurns ───────────────────────────────────────────────

// TestOmpRPC_SerializesTurns verifies that parseOmpRPCTurn stops after the
// first terminal event and does not read beyond the done frame, which is the
// property that enables safe turn serialization over a persistent pipe
// (Criterion 2).
func TestOmpRPC_SerializesTurns(t *testing.T) {
	// Build a reader that contains two complete turns back-to-back.
	// The first turn ends with done; the second with error.
	// parseOmpRPCTurn must stop after the first done and leave the second turn
	// in the reader.
	turn1 := []string{
		`{"type":"text_delta","content":"hello"}`,
		`{"type":"done","message":{"id":"t1"}}`,
	}
	turn2 := []string{
		`{"type":"error","error":{"message":"second turn error"}}`,
	}

	var sb strings.Builder
	for _, l := range append(turn1, turn2...) {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	r := bufio.NewReader(strings.NewReader(sb.String()))

	// Parse first turn — should stop at done and return nil.
	ch1 := make(chan llmclient.Event, 32)
	te1 := newTerminalEmitter(ch1)
	err := parseOmpRPCTurn(context.Background(), r, ch1, te1, nil, llmclient.DefaultRequestConfig())
	if err != nil {
		t.Fatalf("parseOmpRPCTurn (turn 1): unexpected error: %v", err)
	}
	events1 := drainChannel(ch1)
	if len(events1) == 0 {
		t.Fatal("turn 1: no events emitted")
	}
	if events1[len(events1)-1].Type != llmclient.EventDone {
		t.Errorf("turn 1 last event = %q, want done", events1[len(events1)-1].Type)
	}

	// Parse second turn from the same reader — should get the error event.
	ch2 := make(chan llmclient.Event, 32)
	te2 := newTerminalEmitter(ch2)
	err = parseOmpRPCTurn(context.Background(), r, ch2, te2, nil, llmclient.DefaultRequestConfig())
	if err != nil {
		t.Fatalf("parseOmpRPCTurn (turn 2): unexpected error: %v", err)
	}
	events2 := drainChannel(ch2)
	if len(events2) == 0 {
		t.Fatal("turn 2: no events emitted")
	}
	if events2[len(events2)-1].Type != llmclient.EventError {
		t.Errorf("turn 2 last event = %q, want error", events2[len(events2)-1].Type)
	}
}

// ─── TestOmpRPC_SatisfiesHostToolRequirements ─────────────────────────────────

// TestOmpRPC_SatisfiesHostToolRequirements verifies that omp-rpc static
// capabilities declare all host tool requirements (Criterion 4).
func TestOmpRPC_SatisfiesHostToolRequirements(t *testing.T) {
	caps := ompRPCStaticCapabilities("")

	if !caps.HostToolDefs {
		t.Error("omp-rpc: HostToolDefs should be true")
	}
	if !caps.HostToolApproval {
		t.Error("omp-rpc: HostToolApproval should be true")
	}
	if !caps.ToolResultInjection {
		t.Error("omp-rpc: ToolResultInjection should be true")
	}
	if caps.Backend != "omp-rpc" {
		t.Errorf("Backend = %q, want omp-rpc", caps.Backend)
	}
	if caps.Streaming != llmclient.StreamingStructured {
		t.Errorf("Streaming = %q, want structured", caps.Streaming)
	}
}

// TestOmpRPC_HostToolOptionSupported verifies that OptionHostTools is listed
// in the capability OptionSupport map for omp-rpc.
func TestOmpRPC_HostToolOptionSupported(t *testing.T) {
	caps := ompRPCStaticCapabilities("")
	support, ok := caps.OptionSupport[llmclient.OptionHostTools]
	if !ok {
		t.Fatal("OptionHostTools not present in OptionSupport map")
	}
	if support != llmclient.OptionSupportFull {
		t.Errorf("OptionHostTools support = %q, want full", support)
	}
}

// TestOmpRPC_RequiredUnsupportedOptionErrors verifies that Stream returns an
// error when an unsupported required option is requested.
func TestOmpRPC_RequiredUnsupportedOptionErrors(t *testing.T) {
	b := NewOmpRPCBackend(CliInfo{Path: "/does/not/matter"})
	_, err := b.Stream(
		context.Background(),
		&llmclient.Context{},
		llmclient.WithRequiredOptions(llmclient.OptionCodexProfile),
	)
	if err == nil {
		t.Fatal("expected error for unsupported required option, got nil")
	}
}

func TestOmpRPC_RequiredOllamaOptionAccepted(t *testing.T) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), []llmclient.Option{
		llmclient.WithOllama(llmclient.OllamaConfig{Model: "qwen3.5"}),
		llmclient.WithRequiredOptions(llmclient.OptionOllama),
	})
	err := checkOmpRPCRequiredOptions(cfg)
	if err != nil {
		t.Fatalf("expected required OptionOllama to be accepted, got %v", err)
	}
}

// ─── TestOmpRegistry_PriorityAfterPublicCLIs ─────────────────────────────────

// TestOmpRegistry_PriorityAfterPublicCLIs verifies that both omp and omp-rpc
// are registered with PreferOmp, which is strictly greater (lower priority)
// than PreferClaude, PreferCodex, and PreferOpenCode (Criterion 3).
func TestOmpRegistry_PriorityAfterPublicCLIs(t *testing.T) {
	fOmp, ok := factoryByName("omp")
	if !ok {
		t.Fatal("omp backend not registered")
	}
	fRPC, ok := factoryByName("omp-rpc")
	if !ok {
		t.Fatal("omp-rpc backend not registered")
	}

	if fOmp.Preference != PreferOmp {
		t.Errorf("omp Preference = %v, want PreferOmp", fOmp.Preference)
	}
	if fRPC.Preference != PreferOmp {
		t.Errorf("omp-rpc Preference = %v, want PreferOmp", fRPC.Preference)
	}

	// Verify PreferOmp is strictly greater than all public CLI preferences
	// (i.e. omp is lower priority than public CLIs in the default sort order).
	if PreferOmp <= PreferClaude {
		t.Errorf("PreferOmp (%d) should be > PreferClaude (%d)", PreferOmp, PreferClaude)
	}
	if PreferOmp <= PreferCodex {
		t.Errorf("PreferOmp (%d) should be > PreferCodex (%d)", PreferOmp, PreferCodex)
	}
	if PreferOmp <= PreferOpenCode {
		t.Errorf("PreferOmp (%d) should be > PreferOpenCode (%d)", PreferOmp, PreferOpenCode)
	}
}

// TestOmpRegistry_OmpComesBeforeOmpRPC verifies that in the sorted factory
// list for the PreferOmp family, the print backend is ordered before omp-rpc
// (registration order breaks ties within the same Preference level).
func TestOmpRegistry_OmpComesBeforeOmpRPC(t *testing.T) {
	ompIdx := -1
	rpcIdx := -1
	const ompBackendName = "omp"
	for i, f := range registeredBackendFactories() {
		switch f.Name {
		case ompBackendName:
			ompIdx = i
		case "omp-rpc":
			rpcIdx = i
		}
	}
	if ompIdx < 0 {
		t.Fatal("omp not found in registered factories")
	}
	if rpcIdx < 0 {
		t.Fatal("omp-rpc not found in registered factories")
	}
	if ompIdx >= rpcIdx {
		t.Errorf("omp should appear before omp-rpc in sorted factories: omp=%d omp-rpc=%d", ompIdx, rpcIdx)
	}
}

// TestOmpRPC_FidelityReflectsHostTools verifies that ompRPCFidelity sets
// ToolControl to ToolControlHost and includes HostTools in OptionResults
// when host tools are present.
func TestOmpRPC_FidelityReflectsHostTools(t *testing.T) {
	cfg := llmclient.DefaultRequestConfig()
	cfg.HostTools = []llmclient.Tool{{Name: "Bash"}}

	fidelity := ompRPCFidelity(cfg)

	if fidelity.ToolControl != llmclient.ToolControlHost {
		t.Errorf("ToolControl = %q, want host_controlled", fidelity.ToolControl)
	}
	if fidelity.OptionResults[llmclient.OptionHostTools] != llmclient.OptionApplied {
		t.Errorf("OptionHostTools result = %q, want applied", fidelity.OptionResults[llmclient.OptionHostTools])
	}
	if _, ok := fidelity.OptionResults[llmclient.OptionModel]; ok {
		t.Errorf("OptionModel result should be omitted when not passed, got %q", fidelity.OptionResults[llmclient.OptionModel])
	}
}

// TestOmpRPC_FidelityWithoutHostTools verifies that ompRPCFidelity omits
// HostTools from OptionResults when no host tools are configured.
func TestOmpRPC_FidelityWithoutHostTools(t *testing.T) {
	cfg := llmclient.DefaultRequestConfig()
	fidelity := ompRPCFidelity(cfg)

	if _, ok := fidelity.OptionResults[llmclient.OptionHostTools]; ok {
		t.Error("OptionHostTools should not be in OptionResults when no tools are set")
	}
	if _, ok := fidelity.OptionResults[llmclient.OptionModel]; ok {
		t.Error("OptionModel should not be in OptionResults when not set")
	}
	if _, ok := fidelity.OptionResults[llmclient.OptionSession]; ok {
		t.Error("OptionSession should not be in OptionResults when not set")
	}
}

// TestOmpRPC_WaitForReady verifies that waitForOmpRPCReady returns the
// session_id from the first ready frame, skipping preceding non-ready lines.
func TestOmpRPC_WaitForReady(t *testing.T) {
	lines := []string{
		`{"type":"info","message":"starting up"}`,
		`{"type":"ready","session_id":"rpc-sess-42"}`,
		`{"type":"text_delta","content":"should not be consumed"}`,
	}
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	r := bufio.NewReader(strings.NewReader(sb.String()))

	sessionID, err := waitForOmpRPCReady(context.Background(), r)
	if err != nil {
		t.Fatalf("waitForOmpRPCReady: %v", err)
	}
	if sessionID != "rpc-sess-42" {
		t.Errorf("sessionID = %q, want rpc-sess-42", sessionID)
	}

	// The text_delta line must still be available in the reader.
	line, _ := r.ReadString('\n')
	if !strings.Contains(line, "text_delta") {
		t.Errorf("text_delta line was consumed by waitForOmpRPCReady: got %q", line)
	}
}

// TestOmpRPC_WaitForReadyCancelledCtx verifies that waitForOmpRPCReady
// respects context cancellation.
func TestOmpRPC_WaitForReadyCancelledCtx(t *testing.T) {
	// Reader with no ready event — would block forever without cancellation.
	lines := []string{
		`{"type":"info","message":"still starting"}`,
	}
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	// Wrap in a reader that returns EOF immediately so waitForOmpRPCReady
	// encounters io.ErrUnexpectedEOF rather than hanging.
	r := bufio.NewReader(strings.NewReader(sb.String()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	_, err := waitForOmpRPCReady(ctx, r)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

// TestOmpRPC_ParseTurnIgnoresReadyEvents verifies that parseOmpRPCTurn
// silently ignores "ready" events that may arrive mid-stream in RPC mode.
func TestOmpRPC_ParseTurnIgnoresReadyEvents(t *testing.T) {
	lines := []string{
		`{"type":"ready","session_id":"should-be-ignored"}`,
		`{"type":"text_delta","content":"hello"}`,
		`{"type":"done","message":{"id":"m1"}}`,
	}
	r := ompJSONLReader(lines...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	err := parseOmpRPCTurn(context.Background(), r, ch, te, nil, llmclient.DefaultRequestConfig())
	if err != nil {
		t.Fatalf("parseOmpRPCTurn: %v", err)
	}
	events := drainChannel(ch)
	// Should not see a start event (parseOmpRPCTurn starts with startEmitted=true).
	for _, ev := range events {
		if ev.Type == llmclient.EventStart {
			t.Error("parseOmpRPCTurn emitted an EventStart; start should come from OmpRPCBackend.Stream")
		}
	}
	// Should still get text and done events.
	if findEventOr(events, llmclient.EventDone) == nil {
		t.Error("no done event emitted")
	}
}

// findEventOr returns the first event of type et, or nil if not found.
func findEventOr(events []llmclient.Event, et llmclient.EventType) *llmclient.Event {
	for i := range events {
		if events[i].Type == et {
			return &events[i]
		}
	}
	return nil
}

// TestOmpRPC_StaticCapabilitiesCompleteness verifies all required capability
// fields for the omp RPC backend.
func TestOmpRPC_StaticCapabilitiesCompleteness(t *testing.T) {
	f, ok := factoryByName("omp-rpc")
	if !ok {
		t.Fatal("omp-rpc backend not registered")
	}
	caps := f.Capabilities
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
	if f.Binary != "omp" {
		t.Errorf("Binary = %q, want omp", f.Binary)
	}
}

// ─── fab-o5la: Ollama routing ─────────────────────────────────────────────────

// TestOmpRPC_Ollama_InjectsEnv verifies that applyOmpOllamaEnv (wired into
// startOmpRPCProcess) injects ANTHROPIC_BASE_URL into the process environment.
func TestOmpRPC_Ollama_InjectsEnv(t *testing.T) {
	ollamaCfg := llmclient.OllamaConfig{BaseURL: "http://localhost:11434"}
	env := applyOmpOllamaEnv([]string{}, ollamaCfg)

	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_BASE_URL=") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ANTHROPIC_BASE_URL not found in env after applyOmpOllamaEnv; got %v", env)
	}
}

// ─── TestOmpRPC_SetHostTools_PayloadShape ─────────────────────────────────────

// TestOmpRPC_SetHostTools_PayloadShape verifies that sendSetHostTools encodes
// tools using the "parameters" key (not "inputSchema") and includes a non-empty
// top-level "id" field, matching the omp protocol v14.0.0+ wire shape.
func TestOmpRPC_SetHostTools_PayloadShape(t *testing.T) {
	enc, buf := newEncoderBuf()
	proc := &ompRPCProc{enc: enc}

	tools := []llmclient.Tool{
		{
			Name:        "ReadFile",
			Description: "read a file from disk",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string"},
				},
			},
		},
	}

	if err := sendSetHostTools(proc, tools); err != nil {
		t.Fatalf("sendSetHostTools: %v", err)
	}

	msgs := decodeLines(buf)
	if len(msgs) != 1 {
		t.Fatalf("want 1 encoded line, got %d", len(msgs))
	}

	msg := msgs[0]

	// top-level id must be present and non-empty
	id, ok := msg["id"].(string)
	if !ok || id == "" {
		t.Errorf("top-level id field missing or empty: %v", msg["id"])
	}

	toolsVal, ok := msg["tools"].([]interface{})
	if !ok || len(toolsVal) == 0 {
		t.Fatalf("tools field missing or empty: %v", msg["tools"])
	}

	first, ok := toolsVal[0].(map[string]interface{})
	if !ok {
		t.Fatal("first tool is not a JSON object")
	}

	// "parameters" key must be present
	if _, ok := first["parameters"]; !ok {
		t.Error(`tools[0] must have "parameters" key`)
	}

	// "inputSchema" key must NOT be present
	if _, ok := first["inputSchema"]; ok {
		t.Error(`tools[0] must NOT have "inputSchema" key`)
	}
}

// ─── TestOmpRPC_SetHostToolsResponse ─────────────────────────────────────────

// TestOmpRPC_SetHostToolsResponse verifies that a success response frame for
// set_host_tools is silently consumed and does not produce an EventError.
func TestOmpRPC_SetHostToolsResponse(t *testing.T) {
	// Build a fake omp stdout that contains a success response frame followed
	// by a normal done turn so parseOmpRPCTurn terminates cleanly.
	lines := []string{
		`{"type":"response","command":"set_host_tools","id":"ht-1-abcd","success":true,"data":{}}`,
		`{"type":"text_delta","content":"ok"}`,
		`{"type":"done","message":{"id":"m1"}}`,
	}
	r := ompJSONLReader(lines...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	err := parseOmpRPCTurn(context.Background(), r, ch, te, nil, llmclient.DefaultRequestConfig())
	if err != nil {
		t.Fatalf("parseOmpRPCTurn: unexpected error: %v", err)
	}

	events := drainChannel(ch)
	for _, ev := range events {
		if ev.Type == llmclient.EventError {
			t.Errorf("unexpected EventError in events: %s", ev.ErrorMessage)
		}
	}
	if findEventOr(events, llmclient.EventDone) == nil {
		t.Error("expected done event, got none")
	}
}

// TestOmpRPCRegistry_StaticCapabilities_Ollama verifies that the registered
// omp-rpc capabilities declare OllamaRouting=true.
func TestOmpRPCRegistry_StaticCapabilities_Ollama(t *testing.T) {
	f, ok := factoryByName("omp-rpc")
	if !ok {
		t.Fatal("omp-rpc backend not registered")
	}
	if !f.Capabilities.OllamaRouting {
		t.Error("OllamaRouting should be true for omp-rpc")
	}
	if f.Capabilities.OptionSupport[llmclient.OptionOllama] == "" {
		t.Error("OptionSupport[OptionOllama] should be set for omp-rpc")
	}
}

// ─── fab-omth: host_tool_call → host_tool_result round-trip ──────────────────

// newMockRPCProc returns an ompRPCProc whose encoder writes into a pipe that
// the caller can read. The write end is closed when cancel is called, which
// unblocks any readers on the returned bufio.Reader.
func newMockRPCProc() (*ompRPCProc, *bufio.Reader, context.CancelFunc) {
	r, w := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	proc := &ompRPCProc{
		enc:    json.NewEncoder(w),
		stdinW: w,
		cancel: cancel,
	}
	// Close the write end when ctx is cancelled so readers unblock.
	go func() {
		<-ctx.Done()
		_ = w.Close()
	}()
	return proc, bufio.NewReader(r), cancel
}

// collectHostToolResults reads host_tool_result frames from r and sends them on
// the returned channel. It stops reading when the pipe is closed (EOF/error).
func collectHostToolResults(r *bufio.Reader) <-chan map[string]interface{} {
	ch := make(chan map[string]interface{}, 16)
	go func() {
		defer close(ch)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				return
			}
			var m map[string]interface{}
			if json.Unmarshal(line, &m) == nil && m["type"] == "host_tool_result" {
				ch <- m
			}
		}
	}()
	return ch
}

// waitForNResults collects n items from ch within 2 seconds, fataling on timeout.
func waitForNResults(t *testing.T, ch <-chan map[string]interface{}, n int) []map[string]interface{} {
	t.Helper()
	results := make([]map[string]interface{}, 0, n)
	deadline := time.After(2 * time.Second)
	for len(results) < n {
		select {
		case m, ok := <-ch:
			if !ok {
				t.Fatalf("results channel closed before receiving %d items (got %d)", n, len(results))
			}
			results = append(results, m)
		case <-deadline:
			t.Fatalf("timed out waiting for %d host_tool_result(s); got %d", n, len(results))
		}
	}
	return results
}

// TestOmpRPC_HostToolRoundTrip verifies that when omp emits a host_tool_call
// and cfg.HostToolResponder is set, the backend invokes the responder, writes a
// host_tool_result frame to stdin with the correct id, and the stream terminates
// with EventDone.
func TestOmpRPC_HostToolRoundTrip(t *testing.T) {
	proc, stdinReader, cancel := newMockRPCProc()
	defer cancel()

	// Collect host_tool_result frames written to the mock stdin pipe.
	resultsCh := collectHostToolResults(stdinReader)

	// Mock omp stdout: host_tool_call then done.
	lines := []string{
		`{"type":"host_tool_call","id":"s1","toolCallId":"tc-1","toolName":"echo","arguments":{"msg":"hi"}}`,
		`{"type":"done","message":{"id":"m1"}}`,
	}
	r := ompJSONLReader(lines...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), []llmclient.Option{
		llmclient.WithHostToolResponder(func(_ context.Context, _ llmclient.ToolCall) ([]llmclient.ContentBlock, bool, error) {
			return []llmclient.ContentBlock{{Type: llmclient.ContentText, Text: "HI"}}, false, nil
		}),
	})

	if err := parseOmpRPCTurn(context.Background(), r, ch, te, proc, cfg); err != nil {
		t.Fatalf("parseOmpRPCTurn: %v", err)
	}

	// Verify the stream emitted EventDone.
	events := drainChannel(ch)
	if findEventOr(events, llmclient.EventDone) == nil {
		t.Error("expected EventDone, got none")
	}

	// Verify EventToolCallStart and EventToolCallEnd were emitted.
	if findEventOr(events, llmclient.EventToolCallStart) == nil {
		t.Error("expected EventToolCallStart, got none")
	}
	if findEventOr(events, llmclient.EventToolCallEnd) == nil {
		t.Error("expected EventToolCallEnd, got none")
	}

	// Wait for the host_tool_result frame to be written to mock stdin.
	results := waitForNResults(t, resultsCh, 1)
	res := results[0]

	if res["id"] != "s1" {
		t.Errorf("host_tool_result id = %q, want s1", res["id"])
	}
	if isErr, _ := res["isError"].(bool); isErr {
		t.Error("expected isError=false for successful responder")
	}
	result, ok := res["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result field missing or wrong type: %v", res["result"])
	}
	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		t.Fatalf("result.content missing or empty: %v", result["content"])
	}
	first, ok := content[0].(map[string]interface{})
	if !ok {
		t.Fatal("result.content[0] is not an object")
	}
	if first["text"] != "HI" {
		t.Errorf("result.content[0].text = %q, want HI", first["text"])
	}
}

// TestOmpRPC_HostToolNoResponder verifies that when no HostToolResponder is
// configured and a host_tool_call arrives, the backend sends a host_tool_result
// with isError:true and a descriptive message so omp can complete the turn.
func TestOmpRPC_HostToolNoResponder(t *testing.T) {
	proc, stdinReader, cancel := newMockRPCProc()
	defer cancel()

	resultsCh := collectHostToolResults(stdinReader)

	lines := []string{
		`{"type":"host_tool_call","id":"s1","toolCallId":"tc-1","toolName":"echo","arguments":{"msg":"hi"}}`,
		`{"type":"done","message":{"id":"m1"}}`,
	}
	r := ompJSONLReader(lines...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	// No WithHostToolResponder — cfg.HostToolResponder is nil.
	cfg := llmclient.DefaultRequestConfig()

	if err := parseOmpRPCTurn(context.Background(), r, ch, te, proc, cfg); err != nil {
		t.Fatalf("parseOmpRPCTurn: %v", err)
	}

	events := drainChannel(ch)
	if findEventOr(events, llmclient.EventDone) == nil {
		t.Error("expected EventDone, got none")
	}

	results := waitForNResults(t, resultsCh, 1)
	res := results[0]

	if res["id"] != "s1" {
		t.Errorf("host_tool_result id = %q, want s1", res["id"])
	}
	isErr, _ := res["isError"].(bool)
	if !isErr {
		t.Error("expected isError=true when no responder is configured")
	}
	// Verify the error message mentions "no host tool responder configured".
	result, ok := res["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result field missing or wrong type: %v", res["result"])
	}
	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		t.Fatalf("result.content missing or empty: %v", result["content"])
	}
	first, ok := content[0].(map[string]interface{})
	if !ok {
		t.Fatal("result.content[0] is not an object")
	}
	if text, _ := first["text"].(string); !strings.Contains(text, "no host tool responder configured") {
		t.Errorf("error message = %q, want to contain 'no host tool responder configured'", text)
	}
}

// TestOmpRPC_HostToolResultCorrelation verifies that two sequential host_tool_call
// frames with distinct ids produce host_tool_result frames whose ids match the
// originating call ids without cross-wiring.
func TestOmpRPC_HostToolResultCorrelation(t *testing.T) {
	proc, stdinReader, cancel := newMockRPCProc()
	defer cancel()

	resultsCh := collectHostToolResults(stdinReader)

	lines := []string{
		`{"type":"host_tool_call","id":"id-A","toolCallId":"tc-A","toolName":"toolA","arguments":{}}`,
		`{"type":"host_tool_call","id":"id-B","toolCallId":"tc-B","toolName":"toolB","arguments":{}}`,
		`{"type":"done","message":{"id":"m1"}}`,
	}
	r := ompJSONLReader(lines...)
	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), []llmclient.Option{
		llmclient.WithHostToolResponder(func(_ context.Context, tc llmclient.ToolCall) ([]llmclient.ContentBlock, bool, error) {
			// Echo the tool name as the result text so we can correlate.
			return []llmclient.ContentBlock{{Type: llmclient.ContentText, Text: "result-for-" + tc.Name}}, false, nil
		}),
	})

	if err := parseOmpRPCTurn(context.Background(), r, ch, te, proc, cfg); err != nil {
		t.Fatalf("parseOmpRPCTurn: %v", err)
	}

	events := drainChannel(ch)
	if findEventOr(events, llmclient.EventDone) == nil {
		t.Error("expected EventDone, got none")
	}

	// Collect both results.
	results := waitForNResults(t, resultsCh, 2)

	// Build a map from id → result frame.
	byID := make(map[string]map[string]interface{}, 2)
	for _, res := range results {
		id, _ := res["id"].(string)
		byID[id] = res
	}

	for _, wantID := range []string{"id-A", "id-B"} {
		res, ok := byID[wantID]
		if !ok {
			t.Errorf("no host_tool_result with id=%q", wantID)
			continue
		}
		if isErr, _ := res["isError"].(bool); isErr {
			t.Errorf("id=%q: unexpected isError=true", wantID)
		}
		result, ok := res["result"].(map[string]interface{})
		if !ok {
			t.Errorf("id=%q: result field missing: %v", wantID, res["result"])
			continue
		}
		content, ok := result["content"].([]interface{})
		if !ok || len(content) == 0 {
			t.Errorf("id=%q: content missing: %v", wantID, result["content"])
			continue
		}
		first, _ := content[0].(map[string]interface{})
		text, _ := first["text"].(string)
		// id-A → toolA → "result-for-toolA"; id-B → toolB → "result-for-toolB"
		var wantTool string
		if wantID == "id-A" {
			wantTool = "result-for-toolA"
		} else {
			wantTool = "result-for-toolB"
		}
		if text != wantTool {
			t.Errorf("id=%q: content text = %q, want %q", wantID, text, wantTool)
		}
	}
}
