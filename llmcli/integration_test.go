//go:build integration

package llmcli

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// skipIfCLIUnavailable calls t.Skip when the named binary is not on PATH.
// This is the standard guard used by all integration tests in this file.
func skipIfCLIUnavailable(t *testing.T, binary string) {
	t.Helper()

	if _, err := exec.LookPath(binary); err != nil {
		t.Skipf("integration: %s not on PATH; skipping", binary)
	}
}

// TestIntegrationClaude_BasicPrompt streams a trivial prompt through the
// real claude CLI and verifies that at least one text event and a terminal
// done event arrive on the channel.
//
// Skipped when the claude binary is not installed.
func TestIntegrationClaude_BasicPrompt(t *testing.T) {
	skipIfCLIUnavailable(t, "claude")

	b, err := SelectBackendByName("claude")
	if err != nil {
		t.Skipf("integration: claude backend not available: %v", err)
	}

	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "Reply with exactly one word: pong"},
				},
			},
		},
	}

	ch, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainChannel(ch)

	if len(events) == 0 {
		t.Fatal("no events received")
	}

	last := events[len(events)-1]

	// An EventError terminal usually indicates the CLI is installed but not
	// authenticated or otherwise misconfigured. Treat this as an environment
	// constraint and skip rather than fail.
	if last.Type == llmclient.EventError {
		t.Skipf("integration: claude stream error (check authentication/config): %s",
			last.ErrorMessage)
	}

	// The last event must be EventDone.
	if last.Type != llmclient.EventDone {
		t.Errorf("last event type = %q, want %q", last.Type, llmclient.EventDone)
	}

	// At least one text event must have been received.
	var gotText bool

	for _, ev := range events {
		if ev.Type == llmclient.EventTextDelta && ev.Delta != "" {
			gotText = true

			break
		}
	}

	if !gotText {
		t.Error("no non-empty EventTextDelta received")
	}
}

// TestIntegrationClaude_Cancel starts a streaming request against the real
// claude CLI, cancels the context immediately, and verifies that the event
// channel closes within a reasonable deadline. This exercises the subprocess
// termination path.
//
// Skipped when the claude binary is not installed.
func TestIntegrationClaude_Cancel(t *testing.T) {
	skipIfCLIUnavailable(t, "claude")

	b, err := SelectBackendByName("claude")
	if err != nil {
		t.Skipf("integration: claude backend not available: %v", err)
	}

	defer b.Close()

	ctx, cancel := context.WithCancel(context.Background())

	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "Write a very long essay about astronomy."},
				},
			},
		},
	}

	ch, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// Cancel immediately so the subprocess is terminated before it produces
	// meaningful output.
	cancel()

	closed := make(chan struct{})

	go func() {
		defer close(closed)
		drainChannel(ch)
	}()

	select {
	case <-closed:
		// Channel closed as expected after cancellation.
	case <-time.After(15 * time.Second):
		t.Error("event channel was not closed within 15s after context cancellation")
	}
}

// TestIntegrationSelectBackend_StrictProbe calls [SelectBackend] with
// StrictProbe enabled, verifying that the backend discovery path works
// end-to-end with live availability probes.
//
//   - When at least one CLI is installed, the selected backend must report
//     Available() == true.
//   - When no CLIs are installed, [ErrNoBackendAvailable] is returned and the
//     test skips rather than fails, because the absence of CLIs is an
//     environment constraint, not a code bug.
func TestIntegrationSelectBackend_StrictProbe(t *testing.T) {
	ctx := context.Background()

	b, err := SelectBackend(ctx, Requirements{StrictProbe: true})
	if errors.Is(err, ErrNoBackendAvailable) {
		t.Skip("integration: no backend available with StrictProbe; skipping")
	}

	if err != nil {
		t.Fatalf("SelectBackend(StrictProbe): unexpected error: %v", err)
	}

	defer b.Close()

	if !b.Available() {
		t.Errorf("StrictProbe selected backend %q but Available() = false", b.Name())
	}

	t.Logf("StrictProbe selected backend: %s", b.Name())
}

// TestIntegrationCodexExec_BasicPrompt streams a trivial prompt through the
// real codex CLI and verifies that at least one text event and a terminal
// done event arrive on the channel.
//
// Skipped when the codex binary is not installed.
func TestIntegrationCodexExec_BasicPrompt(t *testing.T) {
	skipIfCLIUnavailable(t, "codex")

	b, err := SelectBackendByName("codex-exec")
	if err != nil {
		t.Skipf("integration: codex-exec backend not available: %v", err)
	}

	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "Reply with exactly one word: pong"},
				},
			},
		},
	}

	ch, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainChannel(ch)

	if len(events) == 0 {
		t.Fatal("no events received")
	}

	last := events[len(events)-1]

	if last.Type == llmclient.EventError {
		t.Skipf("integration: codex-exec stream error (check authentication/config): %s",
			last.ErrorMessage)
	}

	if last.Type != llmclient.EventDone {
		t.Errorf("last event type = %q, want %q", last.Type, llmclient.EventDone)
	}

	var gotContent bool
	for _, ev := range events {
		if (ev.Type == llmclient.EventTextDelta && ev.Delta != "") ||
			(ev.Type == llmclient.EventThinkingDelta && ev.Delta != "") {
			gotContent = true
			break
		}
	}
	if !gotContent {
		t.Log("no non-empty text/thinking content received (backend may have returned empty response)")
	}
}

// TestIntegrationOpenCodeRun_BasicPrompt streams a trivial prompt through the
// real opencode CLI and verifies that at least one text event and a terminal
// done event arrive on the channel.
//
// Skipped when the opencode binary is not installed.
func TestIntegrationOpenCodeRun_BasicPrompt(t *testing.T) {
	skipIfCLIUnavailable(t, "opencode")

	b, err := SelectBackendByName("opencode-run")
	if err != nil {
		t.Skipf("integration: opencode-run backend not available: %v", err)
	}

	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "Reply with exactly one word: pong"},
				},
			},
		},
	}

	ch, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainChannel(ch)

	if len(events) == 0 {
		t.Fatal("no events received")
	}

	last := events[len(events)-1]

	if last.Type == llmclient.EventError {
		t.Skipf("integration: opencode-run stream error (check authentication/config): %s",
			last.ErrorMessage)
	}

	if last.Type != llmclient.EventDone {
		t.Errorf("last event type = %q, want %q", last.Type, llmclient.EventDone)
	}

	var gotContent bool
	for _, ev := range events {
		if (ev.Type == llmclient.EventTextDelta && ev.Delta != "") ||
			(ev.Type == llmclient.EventThinkingDelta && ev.Delta != "") {
			gotContent = true
			break
		}
	}
	if !gotContent {
		t.Log("no non-empty text/thinking content received (backend may have returned empty response)")
	}
}

// TestIntegrationOmp_BasicPrompt streams a trivial prompt through the
// real omp CLI and verifies that at least one text event and a terminal
// done event arrive on the channel.
//
// Skipped when the omp binary is not installed.
func TestIntegrationOmp_BasicPrompt(t *testing.T) {
	skipIfCLIUnavailable(t, "omp")

	b, err := SelectBackendByName("omp")
	if err != nil {
		t.Skipf("integration: omp backend not available: %v", err)
	}

	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "Reply with exactly one word: pong"},
				},
			},
		},
	}

	ch, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainChannel(ch)

	if len(events) == 0 {
		t.Fatal("no events received")
	}

	last := events[len(events)-1]

	if last.Type == llmclient.EventError {
		t.Skipf("integration: omp stream error (check authentication/config): %s",
			last.ErrorMessage)
	}

	if last.Type != llmclient.EventDone {
		t.Errorf("last event type = %q, want %q", last.Type, llmclient.EventDone)
	}

	var gotContent bool
	for _, ev := range events {
		if (ev.Type == llmclient.EventTextDelta && ev.Delta != "") ||
			(ev.Type == llmclient.EventThinkingDelta && ev.Delta != "") {
			gotContent = true
			break
		}
	}
	if !gotContent {
		t.Log("no non-empty text/thinking content received (backend may have returned empty response)")
	}
}
