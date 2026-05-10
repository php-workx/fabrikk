package llmclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var markdownJSONFenceRE = regexp.MustCompile("(?s)^```(?:json)?\\s*\\n(.*)\\n```$") //nolint:gochecknoglobals // compiled once for CollectJSON.

const jsonRetryInstruction = "Previous response was not valid JSON. Parse error: %v. Respond with only valid JSON matching the requested shape. No code fences, no prose."

type jsonParseError struct {
	err error
}

func (e *jsonParseError) Error() string { return e.err.Error() }
func (e *jsonParseError) Unwrap() error { return e.err }

// CollectJSON streams one turn, treats assistant text as JSON, and unmarshals
// into v. On parse failure it retries once with the invalid response and parse
// error appended to a cloned conversation.
func CollectJSON(
	ctx context.Context,
	backend Backend,
	base *Context,
	v interface{},
	opts ...Option,
) (raw string, err error) {
	raw, err = collectJSONOnce(ctx, backend, base, v, opts...)
	if err == nil {
		return raw, nil
	}
	var parseErr *jsonParseError
	if !errors.As(err, &parseErr) {
		return raw, err
	}

	retryCtx := cloneContext(base)
	retryCtx.Messages = append(retryCtx.Messages,
		Message{
			Role:    RoleAssistant,
			Content: []ContentBlock{{Type: ContentText, Text: raw}},
		},
		Message{
			Role:    RoleDeveloper,
			Content: []ContentBlock{{Type: ContentText, Text: fmt.Sprintf(jsonRetryInstruction, parseErr.err)}},
		},
	)

	raw, err = collectJSONOnce(ctx, backend, retryCtx, v, opts...)
	if err != nil {
		return raw, &EventErrorError{Type: ErrTypeBadRequest, Message: err.Error()}
	}
	return raw, nil
}

func collectJSONOnce(ctx context.Context, backend Backend, input *Context, v interface{}, opts ...Option) (string, error) {
	ch, err := backend.Stream(ctx, input, opts...)
	if err != nil {
		return "", err
	}
	raw, _, err := Collect(ctx, ch)
	if err != nil {
		return raw, err
	}
	if err := unmarshalJSONText(raw, v); err != nil {
		return raw, &jsonParseError{err: err}
	}
	return raw, nil
}

func unmarshalJSONText(raw string, v interface{}) error {
	if err := json.Unmarshal([]byte(stripMarkdownJSONFence(raw)), v); err == nil {
		return nil
	}
	var lastErr error
	for _, candidate := range embeddedJSONCandidates(raw) {
		err := json.Unmarshal([]byte(candidate), v)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return lastErr
	}
	return json.Unmarshal([]byte(stripMarkdownJSONFence(raw)), v)
}

func stripMarkdownJSONFence(raw string) string {
	trimmed := strings.TrimSpace(raw)
	matches := markdownJSONFenceRE.FindStringSubmatch(trimmed)
	if len(matches) != 2 {
		return raw
	}
	return matches[1]
}

func embeddedJSONCandidates(raw string) []string {
	var candidates []string
	for i, r := range raw {
		if r != '{' && r != '[' {
			continue
		}
		if end, ok := balancedJSONEnd(raw[i:]); ok {
			candidates = append(candidates, raw[i:i+end])
		}
	}
	return candidates
}

func balancedJSONEnd(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	var stack []byte
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) == 0 || stack[len(stack)-1] != ch {
				return 0, false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

func cloneContext(in *Context) *Context {
	if in == nil {
		return &Context{}
	}
	out := &Context{
		SystemPrompt: in.SystemPrompt,
		Messages:     cloneMessages(in.Messages),
		Tools:        cloneTools(in.Tools),
		Metadata:     copyStringMap(in.Metadata),
	}
	return out
}

func cloneMessages(messages []Message) []Message {
	if messages == nil {
		return nil
	}
	out := make([]Message, len(messages))
	for i := range messages {
		out[i] = messages[i]
		out[i].Content = cloneContentBlocks(messages[i].Content)
	}
	return out
}

func cloneContentBlocks(blocks []ContentBlock) []ContentBlock {
	if blocks == nil {
		return nil
	}
	out := make([]ContentBlock, len(blocks))
	for i := range blocks {
		out[i] = blocks[i]
		if blocks[i].Arguments != nil {
			out[i].Arguments = cloneInterfaceMap(blocks[i].Arguments)
		}
	}
	return out
}
