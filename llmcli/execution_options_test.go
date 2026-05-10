package llmcli

import (
	"testing"

	"github.com/php-workx/fabrikk/llmclient"
)

func TestAddJSONSchemaFidelityReportsAdvisory(t *testing.T) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), []llmclient.Option{
		llmclient.WithJSONSchema(map[string]interface{}{"type": "object"}),
	})
	fidelity := populateJSONSchemaFidelity(&llmclient.Fidelity{
		Streaming:   llmclient.StreamingStructured,
		ToolControl: llmclient.ToolControlNone,
	}, cfg)

	if fidelity.JSONSchemaMode != llmclient.JSONSchemaAdvisory {
		t.Fatalf("JSONSchemaMode = %q, want %q", fidelity.JSONSchemaMode, llmclient.JSONSchemaAdvisory)
	}
	if fidelity.OptionResults[llmclient.OptionJSONSchema] != llmclient.OptionDegraded {
		t.Fatalf("OptionJSONSchema result = %q, want %q", fidelity.OptionResults[llmclient.OptionJSONSchema], llmclient.OptionDegraded)
	}
	if len(fidelity.Warnings) == 0 {
		t.Fatal("expected advisory warning")
	}
}
