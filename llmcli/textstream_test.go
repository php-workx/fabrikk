package llmcli

import (
	"context"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// TestProbePipeStreaming_EnvVarOverride verifies that when
// FABRIKK_PIPE_STREAMING contains the binary name, probePipeStreaming returns
// true, and that it returns false when the binary name is not listed.
func TestProbePipeStreaming_EnvVarOverride(t *testing.T) {
	t.Run("matches binary name", func(t *testing.T) {
		t.Setenv("FABRIKK_PIPE_STREAMING", "mycodex,anotherbinary")
		if !probePipeStreaming(context.Background(), "/usr/local/bin/mycodex", nil) {
			t.Error("probePipeStreaming returned false for matching binary name, want true")
		}
	})

	t.Run("does not match", func(t *testing.T) {
		t.Setenv("FABRIKK_PIPE_STREAMING", "otherbinary")
		if probePipeStreaming(context.Background(), "/usr/local/bin/mycodex", nil) {
			t.Error("probePipeStreaming returned true for non-matching binary, want false")
		}
	})

	t.Run("env var unset returns false", func(t *testing.T) {
		t.Setenv("FABRIKK_PIPE_STREAMING", "")
		if probePipeStreaming(context.Background(), "/any/path", nil) {
			t.Error("probePipeStreaming returned true with empty env var, want false")
		}
	})
}

// TestStreamTextProcess_UsesCallerFidelity verifies that when a non-nil
// *llmclient.Fidelity is passed to streamTextProcess, the first event on the
// returned channel is EventStart carrying that exact fidelity pointer content
// (OptionResults and Warnings copied through).
func TestStreamTextProcess_UsesCallerFidelity(t *testing.T) {
	exe := testExecutable(t)

	fidelity := &llmclient.Fidelity{
		Streaming:   llmclient.StreamingBufferedOnly,
		ToolControl: llmclient.ToolControlNone,
		OptionResults: map[llmclient.OptionName]llmclient.OptionResult{
			llmclient.OptionModel: llmclient.OptionApplied,
		},
		Warnings: []string{"test-warning"},
	}

	spec := processSpec{
		Command: exe,
		Args:    []string{exe},
		Env:     []string{"LLMCLI_TEST_FIXTURE=exit_zero"},
	}

	ch, err := streamTextProcess(context.Background(), spec, fidelity, nil)
	if err != nil {
		t.Fatalf("streamTextProcess: %v", err)
	}

	events := waitForEvents(t, ch, 5*time.Second)
	if len(events) == 0 {
		t.Fatal("no events received")
	}

	ev := events[0]
	if ev.Type != llmclient.EventStart {
		t.Fatalf("events[0].Type = %v, want EventStart", ev.Type)
	}
	if ev.Fidelity == nil {
		t.Fatal("events[0].Fidelity is nil, want non-nil")
	}
	if got := ev.Fidelity.OptionResults[llmclient.OptionModel]; got != llmclient.OptionApplied {
		t.Errorf("Fidelity.OptionResults[OptionModel] = %v, want OptionApplied", got)
	}
	if len(ev.Fidelity.Warnings) == 0 || ev.Fidelity.Warnings[0] != "test-warning" {
		t.Errorf("Fidelity.Warnings = %v, want [test-warning]", ev.Fidelity.Warnings)
	}
}
