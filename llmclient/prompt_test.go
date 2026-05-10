package llmclient_test

import (
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/llmclient"
)

func TestBuildPromptFromContextNilInput(t *testing.T) {
	result := llmclient.BuildPromptFromContext(nil)
	if result != "" {
		t.Errorf("BuildPromptFromContext(nil) = %q, want empty", result)
	}
}

func TestBuildPromptFromContextEmptyMessages(t *testing.T) {
	input := &llmclient.Context{
		SystemPrompt: "Be helpful.",
	}
	result := llmclient.BuildPromptFromContext(input)
	if !strings.HasPrefix(result, "Be helpful.") {
		t.Errorf("prompt = %q, want system prompt prefix", result)
	}
}

func TestBuildPromptFromContextSingleUserMessage(t *testing.T) {
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "hello"},
				},
			},
		},
	}
	result := llmclient.BuildPromptFromContext(input)
	if result != "User: hello" {
		t.Errorf("prompt = %q, want %q", result, "User: hello")
	}
}

func TestBuildPromptFromContextMultiTurn(t *testing.T) {
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "What is 2+2?"},
				},
			},
			{
				Role: llmclient.RoleAssistant,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "The answer is 4."},
				},
			},
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "Are you sure?"},
				},
			},
		},
	}
	result := llmclient.BuildPromptFromContext(input)
	if !strings.Contains(result, "User: What is 2+2?") {
		t.Errorf("missing first user message in: %q", result)
	}
	if !strings.Contains(result, "Assistant: The answer is 4.") {
		t.Errorf("missing assistant message in: %q", result)
	}
	if !strings.Contains(result, "User: Are you sure?") {
		t.Errorf("missing last user message in: %q", result)
	}
}

func TestBuildPromptFromContextToolCallAndResult(t *testing.T) {
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "run ls"},
				},
			},
			{
				Role: llmclient.RoleAssistant,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentToolUse, ToolCallID: "toolu_01", ToolName: "Bash", Arguments: map[string]interface{}{"command": "ls"}},
				},
			},
			{
				Role:       llmclient.RoleToolResult,
				ToolCallID: "toolu_01",
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "file1.txt\nfile2.txt"},
				},
			},
		},
	}
	result := llmclient.BuildPromptFromContext(input)

	if !strings.Contains(result, "Assistant called tool: Bash") {
		t.Errorf("missing tool call in: %q", result)
	}
	if !strings.Contains(result, `"command":"ls"`) {
		t.Errorf("missing tool args in: %q", result)
	}
	if !strings.Contains(result, "Tool result for toolu_01") {
		t.Errorf("missing tool result in: %q", result)
	}
	if !strings.Contains(result, "file1.txt") {
		t.Errorf("missing tool result content in: %q", result)
	}
}

func TestBuildPromptFromContextSystemPrompt(t *testing.T) {
	input := &llmclient.Context{
		SystemPrompt: "You are a helpful coding assistant.",
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "help"},
				},
			},
		},
	}
	result := llmclient.BuildPromptFromContext(input)
	if !strings.HasPrefix(result, "You are a helpful coding assistant.") {
		t.Errorf("prompt doesn't start with system prompt: %q", result)
	}
	if !strings.Contains(result, "User: help") {
		t.Errorf("missing user message in: %q", result)
	}
}

func TestBuildPromptFromContextDeveloperMessage(t *testing.T) {
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleDeveloper,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "internal note"},
				},
			},
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "ok"},
				},
			},
		},
	}
	result := llmclient.BuildPromptFromContext(input)
	if !strings.Contains(result, "[Developer]: internal note") {
		t.Errorf("missing developer message in: %q", result)
	}
}

func TestBuildPromptFromContextMultiBlockContent(t *testing.T) {
	// Assistant message with both text and a tool call
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "list files"},
				},
			},
			{
				Role: llmclient.RoleAssistant,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: "Let me check that."},
					{Type: llmclient.ContentToolUse, ToolCallID: "tc1", ToolName: "Bash", Arguments: map[string]interface{}{"command": "ls"}},
				},
			},
		},
	}
	result := llmclient.BuildPromptFromContext(input)

	if !strings.Contains(result, "Assistant: Let me check that.") {
		t.Errorf("missing assistant text in: %q", result)
	}
	if !strings.Contains(result, "Assistant called tool: Bash") {
		t.Errorf("missing tool call in: %q", result)
	}
}

func TestLastUserMessageEmpty(t *testing.T) {
	result := llmclient.LastUserMessage(nil)
	if result != "" {
		t.Errorf("LastUserMessage(nil) = %q, want empty", result)
	}

	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleAssistant, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "hello"},
			}},
		},
	}
	result = llmclient.LastUserMessage(input)
	if result != "" {
		t.Errorf("LastUserMessage with no user messages = %q, want empty", result)
	}
}

func TestLastUserMessageReturnsLastOnly(t *testing.T) {
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "first"},
			}},
			{Role: llmclient.RoleAssistant, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "ok"},
			}},
			{Role: llmclient.RoleUser, Content: []llmclient.ContentBlock{
				{Type: llmclient.ContentText, Text: "second"},
			}},
		},
	}
	result := llmclient.LastUserMessage(input)
	if result != "second" {
		t.Errorf("LastUserMessage = %q, want %q", result, "second")
	}
}

func TestBuildPromptFromContextThinkingBlocksIgnoredInText(t *testing.T) {
	// Thinking blocks should not appear as text content
	input := &llmclient.Context{
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleAssistant,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentThinking, Text: "hmm let me think"},
					{Type: llmclient.ContentText, Text: "answer"},
				},
			},
		},
	}
	result := llmclient.BuildPromptFromContext(input)

	if strings.Contains(result, "hmm let me think") {
		t.Errorf("thinking content leaked into prompt: %q", result)
	}
	if !strings.Contains(result, "Assistant: answer") {
		t.Errorf("missing assistant answer in: %q", result)
	}
}
