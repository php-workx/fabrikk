// Package agentcli provides CLI backend invocation for agent tools (claude, codex, gemini).
package agentcli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Backend name constants for CLI tool routing.
const (
	BackendClaude = "claude"
	BackendCodex  = "codex"
	BackendGemini = "gemini"
)

// CLIBackend describes how to invoke a reviewer via a CLI tool.
type CLIBackend struct {
	Command    string   // e.g., "claude", "codex", "gemini"
	Args       []string // base args before the prompt
	PromptFlag string   // if set, prompt is passed as --flag=value instead of positional
}

// KnownBackends maps backend names to their default CLI invocation config.
// Commands are resolved to absolute paths at init time to work regardless
// of the subprocess PATH environment.
var KnownBackends = map[string]CLIBackend{
	BackendClaude: {Command: ResolveCommand("claude"), Args: []string{"-p", "--model", "opus"}},
	BackendCodex:  {Command: ResolveCommand("codex"), Args: []string{"exec", "-m", "gpt-5.4", "-c", "reasoning_effort=high"}},
	BackendGemini: {Command: ResolveCommand("gemini"), Args: []string{"-m", "gemini-3-pro-preview"}, PromptFlag: "-p"},
}

// ResolveCommand finds the absolute path for a CLI command name.
func ResolveCommand(name string) string {
	// Try standard PATH first.
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	// Search common tool directories not always in PATH.
	home, _ := os.UserHomeDir()
	if home != "" {
		candidates := []string{
			filepath.Join(home, ".local", "bin", name),
			filepath.Join(home, "go", "bin", name),
		}
		// Check nvm node directories.
		nvmDir := filepath.Join(home, ".nvm", "versions", "node")
		if entries, err := os.ReadDir(nvmDir); err == nil {
			for _, e := range entries {
				candidates = append(candidates, filepath.Join(nvmDir, e.Name(), "bin", name))
			}
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	return name
}

// BackendFor returns the CLI backend for a persona with a given backend name and model preference.
// If the backend name is unknown, it defaults to Claude.
func BackendFor(backendName, modelPref string) CLIBackend {
	base, ok := KnownBackends[backendName]
	if !ok {
		base = KnownBackends[BackendClaude]
	}
	if modelPref == "" {
		return base
	}

	// Override the model in the args.
	args := make([]string, len(base.Args))
	copy(args, base.Args)
	args = overrideModelArg(backendName, args, modelPref)
	return CLIBackend{Command: base.Command, Args: args, PromptFlag: base.PromptFlag}
}

// overrideModelArg replaces the model argument in a CLI args slice.
func overrideModelArg(backend string, args []string, model string) []string {
	switch backend {
	case BackendClaude:
		return replaceArgValue(args, "--model", model)
	case BackendCodex:
		return replaceArgValue(args, "-m", model)
	case BackendGemini:
		return replaceArgValue(args, "-m", model)
	default:
		return args
	}
}

func replaceArgValue(args []string, flag, value string) []string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			args[i+1] = value
			return args
		}
	}
	return append(args, flag, value)
}

// InvokeFn is the function signature for invoking a CLI backend.
// Tests replace InvokeFunc with a stub that returns predetermined responses.
type InvokeFn func(ctx context.Context, backend *CLIBackend, prompt string, timeoutSec int) (string, error)

// InvokeFunc is the package-level backend invocation function.
// Replace in tests with a stub to avoid real CLI calls.
var InvokeFunc InvokeFn = Invoke

// Invoke executes a CLI backend with the given prompt and timeout.
func Invoke(ctx context.Context, backend *CLIBackend, prompt string, timeoutSec int) (string, error) {
	timeout := time.Duration(timeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append([]string(nil), backend.Args...)

	// For large prompts, pipe via stdin to avoid OS arg size limits.
	// Claude and Gemini read from stdin when prompt is "-" or piped.
	// Codex reads from stdin when no positional prompt is given.
	useStdin := len(prompt) > 32000

	if !useStdin {
		if backend.PromptFlag != "" {
			args = append(args, backend.PromptFlag, prompt)
		} else {
			args = append(args, prompt)
		}
	}

	cmd := exec.CommandContext(ctx, backend.Command, args...) //nolint:gosec // command is from trusted CLIBackend config, not user input
	cmd.Stderr = os.Stderr

	if useStdin {
		cmd.Stdin = strings.NewReader(prompt)
	}

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("execute %s: %w", backend.Command, err)
	}

	return string(out), nil
}

// ExtractFromCodeFence extracts content from a markdown code fence.
func ExtractFromCodeFence(s string) string {
	if idx := strings.Index(s, "```json"); idx >= 0 {
		start := idx + len("```json")
		if end := strings.Index(s[start:], "```"); end >= 0 {
			return strings.TrimSpace(s[start : start+end])
		}
	}
	if idx := strings.Index(s, "```"); idx >= 0 {
		start := idx + len("```")
		if end := strings.Index(s[start:], "```"); end >= 0 {
			return strings.TrimSpace(s[start : start+end])
		}
	}
	return ""
}

// ExtractJSONBlock extracts a balanced JSON object or array starting at index idx.
func ExtractJSONBlock(s string, idx int) string {
	open := s[idx]
	closeCh := byte('}')
	if open == '[' {
		closeCh = ']'
	}
	depth := 0
	inStr := false
	esc := false
	for i := idx; i < len(s); i++ {
		ch := s[i]
		if esc {
			esc = false
			continue
		}
		if ch == '\\' && inStr {
			esc = true
			continue
		}
		if ch == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch ch {
		case open:
			depth++
		case closeCh:
			depth--
			if depth == 0 {
				return s[idx : i+1]
			}
		}
	}
	return ""
}
