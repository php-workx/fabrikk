package llmclient

import (
	"encoding/json"
	"fmt"
	"strings"
)

// BuildPromptFromContext converts the full Context (system prompt + all
// messages including tool calls and tool results) into a single text prompt
// suitable for CLIs that only accept a single prompt string (e.g.
// `claude -p`, `omp print`, `codex exec`).
//
// The output is structured as:
//
//	[System prompt, if present]
//	User: <text>
//	Assistant: <text>
//	Assistant called tool: name(args)
//	Tool result: <text>
//	...
//
// This preserves the full conversation history so the model can maintain
// context across turns even without native multi-turn API support.
func BuildPromptFromContext(input *Context) string {
	if input == nil {
		return ""
	}

	var sb strings.Builder

	// System prompt first
	if input.SystemPrompt != "" {
		sb.WriteString(input.SystemPrompt)
		sb.WriteString("\n\n")
	}

	for i, msg := range input.Messages {
		switch msg.Role {
		case RoleUser:
			text := blocksToText(msg.Content)
			if text != "" {
				if i > 0 {
					sb.WriteString("\n\n")
				}
				sb.WriteString("User: ")
				sb.WriteString(text)
			}

		case RoleAssistant:
			text, toolCalls := splitBlocks(msg.Content)
			if text != "" {
				sb.WriteString("\n\nAssistant: ")
				sb.WriteString(text)
			}
			for _, tc := range toolCalls {
				sb.WriteString("\n\nAssistant called tool: ")
				sb.WriteString(formatToolCall(tc))
			}

		case RoleToolResult:
			text := blocksToText(msg.Content)
			sb.WriteString("\n\nTool result")
			if msg.ToolCallID != "" {
				sb.WriteString(" for ")
				sb.WriteString(msg.ToolCallID)
			}
			sb.WriteString(": ")
			sb.WriteString(text)

		case RoleDeveloper:
			text := blocksToText(msg.Content)
			if text != "" {
				sb.WriteString("\n\n[Developer]: ")
				sb.WriteString(text)
			}
		}
	}

	return sb.String()
}

// blocksToText extracts the concatenated text from content blocks (type "text").
func blocksToText(blocks []ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == ContentText && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// splitBlocks separates content blocks into text and tool_use blocks.
func splitBlocks(blocks []ContentBlock) (text string, toolCalls []ContentBlock) {
	var textParts []string
	for _, b := range blocks {
		switch b.Type {
		case ContentText:
			if b.Text != "" {
				textParts = append(textParts, b.Text)
			}
		case ContentToolUse:
			toolCalls = append(toolCalls, b)
		}
	}
	return strings.Join(textParts, "\n"), toolCalls
}

// formatToolCall formats a tool_use content block as a human-readable string.
func formatToolCall(b ContentBlock) string {
	args, err := json.Marshal(b.Arguments)
	if err != nil {
		args = []byte("{}")
	}
	return fmt.Sprintf("%s(%s)", b.ToolName, string(args))
}

// LastUserMessage returns the concatenated text of the last user-role message
// in input. Returns empty string when input is nil or no user text is present.
func LastUserMessage(input *Context) string {
	if input == nil {
		return ""
	}
	for i := len(input.Messages) - 1; i >= 0; i-- {
		msg := input.Messages[i]
		if msg.Role != RoleUser {
			continue
		}
		text := blocksToText(msg.Content)
		if text != "" {
			return text
		}
	}
	return ""
}
