package llmcli

import (
	"bufio"
	"strings"
)

// claudeJSONLReader returns a *bufio.Reader that yields each line in lines as
// a '\n'-terminated JSONL frame, followed by io.EOF. Used by parser tests to
// drive parseClaudeStream without spawning a real subprocess.
func claudeJSONLReader(lines ...string) *bufio.Reader {
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	return bufio.NewReader(strings.NewReader(sb.String()))
}

// claudeTextSession returns a minimal valid Claude stream-json session that
// emits a single text content block and a success result.
func claudeTextSession(sessionID, text string) []string {
	return []string{
		`{"type":"system","subtype":"init","session_id":"` + sessionID + `","model":"claude-opus-4-5"}`,
		`{"type":"assistant","message":{"id":"msg1","role":"assistant","content":[{"type":"text","text":"` + escapeJSON(text) + `"}]}}`,
		`{"type":"result","subtype":"success","total_cost_usd":0.001,"duration_ms":100,"num_turns":1,"session_id":"` + sessionID + `"}`,
	}
}

// claudeThinkingSession returns a Claude stream-json session with a thinking
// block followed by a text block.
func claudeThinkingSession(sessionID, thinking, text string) []string {
	return []string{
		`{"type":"system","subtype":"init","session_id":"` + sessionID + `","model":"claude-opus-4-5"}`,
		`{"type":"assistant","message":{"id":"msg1","content":[{"type":"thinking","text":"` + escapeJSON(thinking) + `"}]}}`,
		`{"type":"assistant","message":{"id":"msg1","content":[{"type":"text","text":"` + escapeJSON(text) + `"}]}}`,
		`{"type":"result","subtype":"success","total_cost_usd":0.002,"duration_ms":200,"session_id":"` + sessionID + `"}`,
	}
}

// claudeToolUseSession returns a Claude stream-json session that includes one
// tool_use block followed by a tool_result from the user, then a final text.
func claudeToolUseSession(sessionID, toolID, toolName string) []string {
	return []string{
		`{"type":"system","subtype":"init","session_id":"` + sessionID + `","model":"claude-opus-4-5"}`,
		`{"type":"assistant","message":{"id":"msg1","content":[{"type":"tool_use","id":"` + toolID + `","name":"` + toolName + `","input":{"path":"/tmp/test"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + toolID + `","content":"file contents"}]}}`,
		`{"type":"assistant","message":{"id":"msg2","content":[{"type":"text","text":"done"}]}}`,
		`{"type":"result","subtype":"success","total_cost_usd":0.003,"session_id":"` + sessionID + `"}`,
	}
}

// claudeErrorSession returns a Claude stream-json session that ends with an
// error result of the given subtype (e.g. "error_max_turns",
// "error_during_execution").
func claudeErrorSession(sessionID, subtype string) []string {
	return []string{
		`{"type":"system","subtype":"init","session_id":"` + sessionID + `","model":"claude-opus-4-5"}`,
		`{"type":"result","subtype":"` + subtype + `","session_id":"` + sessionID + `"}`,
	}
}

// escapeJSON escapes a plain string for safe embedding in a JSON string value.
// It only handles the common cases needed by the fixture helpers above.
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}
