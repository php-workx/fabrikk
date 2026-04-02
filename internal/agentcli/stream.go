package agentcli

// StreamMessage is sent to Claude via stdin in stream-json format.
type StreamMessage struct {
	Type    string `json:"type"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
}

// StreamResponse is received from Claude via stdout in stream-json format.
type StreamResponse struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`
	Result  string `json:"result,omitempty"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message,omitempty"`
}

// DaemonRequest is sent by callers over the Unix socket.
type DaemonRequest struct {
	Prompt string `json:"prompt"`
}

// DaemonResponse is returned to callers over the Unix socket.
type DaemonResponse struct {
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// extractResponseText extracts text content from a stream response message.
func extractResponseText(resp StreamResponse) string {
	var text string
	for _, content := range resp.Message.Content {
		if content.Type == "text" {
			text += content.Text
		}
	}
	return text
}
