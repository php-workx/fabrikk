// Package agentcli provides CLI backend invocation for agent tools (claude, codex, gemini).
package agentcli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
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
// If the backend name is unknown or disabled via FABRIKK_DISABLED_BACKENDS, it defaults to Claude.
func BackendFor(backendName, modelPref string) CLIBackend {
	base, ok := KnownBackends[backendName]
	if !ok || isBackendDisabled(backendName) {
		// Unknown or disabled backend: fall back to Claude with its default model.
		// Don't apply modelPref — it was intended for the other backend
		// and may be incompatible with Claude (e.g., "gpt-5.4").
		return KnownBackends[BackendClaude]
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

// disabledBackendsOnce caches the disabled backends list for the process lifetime.
var (
	disabledBackendsOnce   sync.Once
	disabledBackendsCached []string
)

// isBackendDisabled checks if a backend is disabled via:
// 1. FABRIKK_DISABLED_BACKENDS env var (comma-separated, e.g., "gemini,codex")
// 2. fabrikk.yaml disabled_backends list in the working directory
// Env var takes precedence when set (even if empty — empty clears all restrictions).
// The result is cached for the process lifetime via sync.Once.
func isBackendDisabled(name string) bool {
	disabledBackendsOnce.Do(func() {
		disabledBackendsCached = resolveDisabledBackends()
	})
	for _, d := range disabledBackendsCached {
		if d == name {
			return true
		}
	}
	return false
}

// resolveDisabledBackends loads the disabled backends list from env or config file.
// Uses os.LookupEnv to distinguish "not set" from "set to empty" — an empty
// env var clears config-file restrictions (returns nil, not fallthrough).
func resolveDisabledBackends() []string {
	// Env var takes precedence when set (including empty = no restrictions).
	if env, present := os.LookupEnv("FABRIKK_DISABLED_BACKENDS"); present {
		var result []string
		for _, d := range strings.Split(env, ",") {
			if s := strings.TrimSpace(d); s != "" {
				result = append(result, s)
			}
		}
		return result
	}

	// Fall back to config file.
	return loadDisabledBackendsFromConfig()
}

// configDisabledBackends is the YAML structure for fabrikk.yaml.
type configDisabledBackends struct {
	DisabledBackends []string `yaml:"disabled_backends"`
}

// loadDisabledBackendsFromConfig reads fabrikk.yaml from the working directory.
// Returns nil if the file doesn't exist or can't be parsed.
func loadDisabledBackendsFromConfig() []string {
	data, err := os.ReadFile("fabrikk.yaml")
	if err != nil {
		return nil
	}
	var cfg configDisabledBackends
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: fabrikk.yaml parse error: %v\n", err)
		return nil
	}
	return cfg.DisabledBackends
}

// InvokeFn is the function signature for invoking a CLI backend.
// Tests replace InvokeFunc with a stub that returns predetermined responses.
type InvokeFn func(ctx context.Context, backend *CLIBackend, prompt string, timeoutSec int) (string, error)

// InvokeFunc is the package-level backend invocation function.
// Replace in tests with a stub to avoid real CLI calls.
var InvokeFunc InvokeFn = Invoke

// StdinThreshold is the prompt size (bytes) above which the prompt is piped
// via stdin instead of passed as a CLI argument. This avoids OS arg size limits.
const StdinThreshold = 32000

// BuildInvokeArgs constructs the CLI argument list and determines whether
// the prompt should be piped via stdin (for prompts exceeding StdinThreshold).
func BuildInvokeArgs(backend *CLIBackend, prompt string) (args []string, useStdin bool) {
	args = append([]string(nil), backend.Args...)
	useStdin = len(prompt) > StdinThreshold
	if !useStdin {
		if backend.PromptFlag != "" {
			args = append(args, backend.PromptFlag, prompt)
		} else {
			args = append(args, prompt)
		}
	}
	return args, useStdin
}

// Invoke executes a CLI backend with the given prompt and timeout.
func Invoke(ctx context.Context, backend *CLIBackend, prompt string, timeoutSec int) (string, error) {
	timeout := time.Duration(timeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args, useStdin := BuildInvokeArgs(backend, prompt)

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
// Skips the optional info-string (e.g., ```json, ```markdown) on the opening line.
func ExtractFromCodeFence(s string) string {
	idx := strings.Index(s, "```")
	if idx < 0 {
		return ""
	}
	start := idx + len("```")
	// Skip the info-string line (everything up to and including the first newline after ```).
	if nl := strings.IndexByte(s[start:], '\n'); nl >= 0 {
		start += nl + 1
	}
	if end := strings.Index(s[start:], "```"); end >= 0 {
		return strings.TrimSpace(s[start : start+end])
	}
	return ""
}

// ExtractJSONBlock extracts a balanced JSON object or array starting at index idx.
// Returns "" for invalid indices.
func ExtractJSONBlock(s string, idx int) string {
	if idx < 0 || idx >= len(s) {
		return ""
	}
	open := s[idx]
	closeCh := byte('}')
	if open == '[' {
		closeCh = ']'
	}
	return scanBalanced(s, idx, open, closeCh)
}

// scanBalanced scans for a balanced pair of open/close delimiters, respecting JSON strings.
func scanBalanced(s string, start int, open, closeCh byte) string {
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
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
				return s[start : i+1]
			}
		}
	}
	return ""
}
