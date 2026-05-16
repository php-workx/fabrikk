package llmclient_test

import (
	"encoding/json"
	"testing"

	"github.com/php-workx/fabrikk/llmclient"
)

func TestStreamingFidelityConstants(t *testing.T) {
	tests := []struct {
		sf   llmclient.StreamingFidelity
		want string
	}{
		{llmclient.StreamingBufferedOnly, "buffered_only"},
		{llmclient.StreamingTextChunk, "text_chunk"},
		{llmclient.StreamingStructured, "structured"},
		{llmclient.StreamingStructuredUnknown, "structured_unknown"},
	}

	for _, tt := range tests {
		if string(tt.sf) != tt.want {
			t.Errorf("StreamingFidelity %q: expected %q, got %q", tt.sf, tt.want, string(tt.sf))
		}
	}
}

func TestToolControlModeConstants(t *testing.T) {
	tests := []struct {
		tc   llmclient.ToolControlMode
		want string
	}{
		{llmclient.ToolControlNone, "none"},
		{llmclient.ToolControlBuiltIn, "built_in_cli_tools"},
		{llmclient.ToolControlHost, "host_controlled"},
	}

	for _, tt := range tests {
		if string(tt.tc) != tt.want {
			t.Errorf("ToolControlMode %q: expected %q, got %q", tt.tc, tt.want, string(tt.tc))
		}
	}
}

func TestCapabilitiesJSONShape(t *testing.T) {
	caps := llmclient.Capabilities{
		Backend:          "claude",
		Version:          "1.2.3",
		Streaming:        llmclient.StreamingStructured,
		ToolEvents:       true,
		HostToolDefs:     false,
		MultiTurn:        true,
		Thinking:         true,
		Usage:            true,
		Models:           []string{"claude-opus-4-5", "claude-sonnet-4-5"},
		MaxContextTokens: 200000,
		OllamaRouting:    true,
		OptionSupport: map[llmclient.OptionName]llmclient.OptionSupport{
			llmclient.OptionModel:   llmclient.OptionSupportFull,
			llmclient.OptionOllama:  llmclient.OptionSupportFull,
			llmclient.OptionSession: llmclient.OptionSupportFull,
		},
		ProbeWarnings: []string{"version below recommended minimum"},
	}

	data, err := json.Marshal(caps)
	if err != nil {
		t.Fatalf("marshal Capabilities: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	checkKey(t, m, "backend", "claude")
	checkKey(t, m, "version", "1.2.3")
	checkKey(t, m, "streaming", "structured")

	if m["toolEvents"] != true {
		t.Errorf("toolEvents: want true, got %v", m["toolEvents"])
	}
	if m["multiTurn"] != true {
		t.Errorf("multiTurn: want true, got %v", m["multiTurn"])
	}
	if m["thinking"] != true {
		t.Errorf("thinking: want true, got %v", m["thinking"])
	}
	if m["usage"] != true {
		t.Errorf("usage: want true, got %v", m["usage"])
	}
	if m["ollamaRouting"] != true {
		t.Errorf("ollamaRouting: want true, got %v", m["ollamaRouting"])
	}

	optSupport, ok := m["optionSupport"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'optionSupport' object in JSON")
	}
	checkKey(t, optSupport, "model", "full")
	checkKey(t, optSupport, "ollama", "full")
}

func TestFidelityOptionResultsJSONShape(t *testing.T) {
	fid := llmclient.Fidelity{
		Streaming:   llmclient.StreamingStructured,
		ToolControl: llmclient.ToolControlHost,
		OptionResults: map[llmclient.OptionName]llmclient.OptionResult{
			llmclient.OptionModel:        llmclient.OptionApplied,
			llmclient.OptionTemperature:  llmclient.OptionIgnored,
			llmclient.OptionCodexProfile: llmclient.OptionUnsupported,
		},
		Warnings: []string{"temperature ignored"},
	}

	data, err := json.Marshal(fid)
	if err != nil {
		t.Fatalf("marshal Fidelity: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	checkKey(t, m, "streaming", "structured")
	checkKey(t, m, "toolControl", "host_controlled")

	optResults, ok := m["optionResults"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'optionResults' object in Fidelity JSON")
	}
	checkKey(t, optResults, "model", "applied")
	checkKey(t, optResults, "temperature", "ignored")
	checkKey(t, optResults, "codexProfile", "unsupported")

	warnings, ok := m["warnings"].([]interface{})
	if !ok || len(warnings) == 0 {
		t.Error("expected non-empty 'warnings' array in Fidelity JSON")
	}
}

func TestCapabilitiesOmitsOptionalFields(t *testing.T) {
	// A minimal Capabilities with only required fields should not emit
	// optional zero-value fields in JSON.
	caps := llmclient.Capabilities{
		Backend:   "stub",
		Streaming: llmclient.StreamingBufferedOnly,
	}

	data, err := json.Marshal(caps)
	if err != nil {
		t.Fatalf("marshal minimal Capabilities: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	// These optional fields should be absent when zero.
	for _, key := range []string{"version", "models", "maxContextTokens", "optionSupport", "probeWarnings"} {
		if _, ok := m[key]; ok {
			t.Errorf("key %q should be omitted when zero, but was present", key)
		}
	}
}

func TestFidelityOmitsOptionalFields(t *testing.T) {
	fid := llmclient.Fidelity{
		Streaming:   llmclient.StreamingBufferedOnly,
		ToolControl: llmclient.ToolControlNone,
	}

	data, err := json.Marshal(fid)
	if err != nil {
		t.Fatalf("marshal minimal Fidelity: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	for _, key := range []string{"optionResults", "warnings"} {
		if _, ok := m[key]; ok {
			t.Errorf("key %q should be omitted when nil/empty, but was present", key)
		}
	}
}

func TestOptionResultConstants(t *testing.T) {
	tests := []struct {
		or   llmclient.OptionResult
		want string
	}{
		{llmclient.OptionApplied, "applied"},
		{llmclient.OptionIgnored, "ignored"},
		{llmclient.OptionDegraded, "degraded"},
		{llmclient.OptionUnsupported, "unsupported"},
	}

	for _, tt := range tests {
		if string(tt.or) != tt.want {
			t.Errorf("OptionResult %q: expected %q, got %q", tt.or, tt.want, string(tt.or))
		}
	}
}

func TestOptionSupportConstants(t *testing.T) {
	tests := []struct {
		os   llmclient.OptionSupport
		want string
	}{
		{llmclient.OptionSupportFull, "full"},
		{llmclient.OptionSupportPartial, "partial"},
		{llmclient.OptionSupportNone, "none"},
	}

	for _, tt := range tests {
		if string(tt.os) != tt.want {
			t.Errorf("OptionSupport %q: expected %q, got %q", tt.os, tt.want, string(tt.os))
		}
	}
}
