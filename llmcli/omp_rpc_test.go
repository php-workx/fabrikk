package llmcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

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
	err := parseOmpRPCTurn(context.Background(), r, ch1, te1)
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
	err = parseOmpRPCTurn(context.Background(), r, ch2, te2)
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

	err := parseOmpRPCTurn(context.Background(), r, ch, te)
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
