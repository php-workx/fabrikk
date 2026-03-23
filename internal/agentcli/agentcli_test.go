package agentcli

import "testing"

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
