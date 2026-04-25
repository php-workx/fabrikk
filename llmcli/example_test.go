package llmcli

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// ─── Unit tests for metrics.go ───────────────────────────────────────────────

// TestNoopObserver verifies that all NoopObserver methods are callable with
// representative arguments and never panic.
func TestNoopObserver(t *testing.T) {
	var obs Observer = NoopObserver{}

	obs.OnStreamStart("claude", "claude-opus-4-5")
	obs.OnStreamStart("claude", "")

	obs.OnStreamEnd("claude", "claude-opus-4-5", true, LabelErrorType(nil))
	obs.OnStreamEnd("claude", "claude-opus-4-5", false, LabelErrorType(errors.New("fail")))

	obs.OnEventEmitted("claude", "claude-opus-4-5", llmclient.EventTextDelta)
	obs.OnEventEmitted("claude", "", llmclient.EventDone)

	obs.OnSpawnDuration("claude", "claude-opus-4-5", 50*time.Millisecond)
	obs.OnSpawnDuration("omp", "", 0)

	obs.OnBackendAvailability("claude", true)
	obs.OnBackendAvailability("claude", false)
}

// TestMetricLabelHelpers verifies that each label helper returns the expected
// stable string value for representative inputs.
func TestMetricLabelHelpers(t *testing.T) {
	t.Run("LabelBackend", func(t *testing.T) {
		cases := []string{"claude", "codex-exec", "omp", ""}
		for _, name := range cases {
			if got := LabelBackend(name); got != name {
				t.Errorf("LabelBackend(%q) = %q, want %q", name, got, name)
			}
		}
	})

	t.Run("LabelModel", func(t *testing.T) {
		cases := []struct {
			in   string
			want string
		}{
			{"claude-opus-4-5", "claude-opus-4-5"},
			{"gpt-4o", "gpt-4o"},
			{"", "default"},
		}
		for _, tc := range cases {
			if got := LabelModel(tc.in); got != tc.want {
				t.Errorf("LabelModel(%q) = %q, want %q", tc.in, got, tc.want)
			}
		}
	})

	t.Run("LabelSuccess", func(t *testing.T) {
		if got := LabelSuccess(true); got != "true" {
			t.Errorf("LabelSuccess(true) = %q, want \"true\"", got)
		}

		if got := LabelSuccess(false); got != "false" {
			t.Errorf("LabelSuccess(false) = %q, want \"false\"", got)
		}
	})

	t.Run("LabelEventType", func(t *testing.T) {
		cases := []struct {
			et   llmclient.EventType
			want string
		}{
			{llmclient.EventStart, "start"},
			{llmclient.EventTextDelta, "text_delta"},
			{llmclient.EventDone, "done"},
			{llmclient.EventError, "error"},
		}
		for _, tc := range cases {
			if got := LabelEventType(tc.et); got != tc.want {
				t.Errorf("LabelEventType(%q) = %q, want %q", tc.et, got, tc.want)
			}
		}
	})

	t.Run("LabelErrorType", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // immediately cancelled

		dlCtx, dlCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer dlCancel()

		cases := []struct {
			name string
			err  error
			want string
		}{
			{"nil", nil, "none"},
			{"canceled", ctx.Err(), "canceled"},
			{"deadline", dlCtx.Err(), "deadline_exceeded"},
			{"other", errors.New("some backend error"), "error"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := LabelErrorType(tc.err); got != tc.want {
					t.Errorf("LabelErrorType(%v) = %q, want %q", tc.err, got, tc.want)
				}
			})
		}
	})
}

// ─── Examples ────────────────────────────────────────────────────────────────

// ExampleSelectBackend demonstrates how to select the highest-priority
// backend that satisfies a set of requirements. When no LLM CLI tools are
// installed, [SelectBackend] returns [ErrNoBackendAvailable].
//
// No // Output: comment is provided because the output depends on which CLI
// tools are installed in the environment.
func ExampleSelectBackend() {
	ctx := context.Background()

	b, err := SelectBackend(ctx, Requirements{
		MinStreaming: llmclient.StreamingStructured,
	})
	if errors.Is(err, ErrNoBackendAvailable) {
		// No structured-streaming CLI installed in this environment.
		fmt.Println("no backend available")
		return
	}

	if err != nil {
		fmt.Println("unexpected error:", err)
		return
	}

	defer b.Close() //nolint:errcheck // example code; per-call backends return nil, persistent ones are closed best-effort

	fmt.Printf("selected: %s\n", b.Name())
}

// ExampleClaudeBackend_Stream demonstrates how to consume a streaming response
// from a [ClaudeBackend]. It handles the common case where no CLI tools are
// installed by exiting cleanly without error.
//
// No // Output: comment is provided because the response text is
// non-deterministic and depends on the installed claude CLI version.
func ExampleClaudeBackend_Stream() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	b, err := SelectBackendByName("claude")
	if errors.Is(err, ErrUnknownBackend) {
		// No CLI installed in this environment; skip gracefully.
		fmt.Println("claude backend unavailable; skipping stream example")
		return
	}

	if err != nil {
		fmt.Println("select error:", err)
		return
	}

	defer b.Close() //nolint:errcheck // example code; per-call backends return nil, persistent ones are closed best-effort

	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "Reply with exactly: pong"},
				},
			},
		},
	}

	ch, err := b.Stream(ctx, input)
	if err != nil {
		fmt.Println("stream error:", err)
		return
	}

	for ev := range ch {
		switch ev.Type {
		case llmclient.EventTextDelta:
			fmt.Print(ev.Delta)
		case llmclient.EventDone:
			fmt.Println()
		case llmclient.EventError:
			fmt.Println("stream error:", ev.ErrorMessage)
		}
	}
}
