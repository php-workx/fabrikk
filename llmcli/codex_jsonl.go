package llmcli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/php-workx/fabrikk/llmcli/internal"
	"github.com/php-workx/fabrikk/llmclient"
)

const maxCodexJSONLLineBytes = 4 * 1024 * 1024

type codexJSONLEvent struct {
	Type    string          `json:"type"`
	Message string          `json:"message,omitempty"`
	Item    codexJSONLItem  `json:"item,omitempty"`
	Usage   codexJSONLUsage `json:"usage,omitempty"`
	Error   struct {
		Message string `json:"message,omitempty"`
	} `json:"error,omitempty"`
}

type codexJSONLItem struct {
	Type    string          `json:"type,omitempty"`
	Text    string          `json:"text,omitempty"`
	Message string          `json:"message,omitempty"`
	Output  string          `json:"output,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}

type codexJSONLUsage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	TotalTokens       int `json:"total_tokens"`
}

func streamCodexJSONLProcess(
	ctx context.Context,
	spec processSpec,
	fidelity *llmclient.Fidelity,
	cleanup func(),
) (<-chan llmclient.Event, error) {
	s, err := startSupervised(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("llmcli: start codex jsonl process: %w", err)
	}

	ch := make(chan llmclient.Event, 16)
	te := newTerminalEmitter(ch)

	go func() {
		if cleanup != nil {
			defer cleanup()
		}
		defer te.close()

		parseResult := parseCodexJSONL(ctx, s.Stdout, ch, fidelity)

		_, _ = io.Copy(io.Discard, s.Stdout)
		waitErr := s.wait()

		switch {
		case ctx.Err() != nil:
			te.done(ctx, nil, nil, llmclient.StopCancelled)
		case parseResult.err != nil:
			te.error(ctx, fmt.Errorf("llmcli codex jsonl: parse: %w", parseResult.err))
		case waitErr != nil:
			te.error(ctx, fmt.Errorf("llmcli codex jsonl: subprocess: %w; stderr: %s", waitErr, s.stderrTail()))
		default:
			msg := &llmclient.AssistantMessage{
				Role: string(llmclient.RoleAssistant),
				Content: []llmclient.ContentBlock{{
					Type: llmclient.ContentText,
					Text: parseResult.text,
				}},
				StopReason: llmclient.StopEndTurn,
				Usage:      parseResult.usage,
			}
			te.done(ctx, msg, parseResult.usage, llmclient.StopEndTurn)
		}
	}()

	return ch, nil
}

type codexJSONLParseResult struct {
	text  string
	usage *llmclient.Usage
	err   error
}

func parseCodexJSONL(
	ctx context.Context,
	r *bufio.Reader,
	out chan<- llmclient.Event,
	fidelity *llmclient.Fidelity,
) codexJSONLParseResult {
	effectiveFidelity := fidelity
	if effectiveFidelity == nil {
		effectiveFidelity = &llmclient.Fidelity{
			Streaming:   llmclient.StreamingBufferedOnly,
			ToolControl: llmclient.ToolControlNone,
		}
	}
	if effectiveFidelity.Streaming == "" {
		effectiveFidelity.Streaming = llmclient.StreamingBufferedOnly
	}
	if effectiveFidelity.ToolControl == "" {
		effectiveFidelity.ToolControl = llmclient.ToolControlNone
	}
	if !emit(ctx, out, startEvent("", effectiveFidelity)) {
		return codexJSONLParseResult{err: ctx.Err()}
	}

	var text strings.Builder
	var usage *llmclient.Usage
	for {
		line, readErr := internal.ReadBoundedLine(r, maxCodexJSONLLineBytes)
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			if readErr == internal.ErrLineTooLong {
				continue
			}
			return codexJSONLParseResult{text: text.String(), usage: usage, err: readErr}
		}
		if len(line) == 0 {
			continue
		}

		var ev codexJSONLEvent
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if msg := codexJSONLText(ev); msg != "" {
			if text.Len() > 0 {
				text.WriteByte('\n')
			}
			text.WriteString(msg)
		}
		if u := codexJSONLUsageToClient(ev.Usage); u != nil {
			usage = u
		}
	}

	accText := strings.TrimRight(text.String(), "\n")
	events := textSequence(0, accText)
	for i := range events {
		if !emit(ctx, out, events[i]) {
			return codexJSONLParseResult{text: accText, usage: usage, err: ctx.Err()}
		}
	}
	return codexJSONLParseResult{text: accText, usage: usage}
}

func codexJSONLText(ev codexJSONLEvent) string {
	switch ev.Type {
	case "item.completed":
		if ev.Item.Type != "agent_message" {
			return ""
		}
		return strings.TrimSpace(firstNonEmpty(
			ev.Item.Text,
			ev.Item.Message,
			ev.Item.Output,
			textFromCodexContent(ev.Item.Content),
		))
	case "agent_message":
		return strings.TrimSpace(firstNonEmpty(ev.Message, ev.Item.Text, textFromCodexContent(ev.Item.Content)))
	default:
		return ""
	}
}

func textFromCodexContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var out []string
	for _, part := range parts {
		if part.Text != "" {
			out = append(out, part.Text)
		}
	}
	return strings.Join(out, "")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func codexJSONLUsageToClient(u codexJSONLUsage) *llmclient.Usage {
	if u.InputTokens == 0 && u.CachedInputTokens == 0 && u.OutputTokens == 0 && u.TotalTokens == 0 {
		return nil
	}
	return &llmclient.Usage{
		InputTokens:     u.InputTokens,
		OutputTokens:    u.OutputTokens,
		CacheReadTokens: u.CachedInputTokens,
	}
}
