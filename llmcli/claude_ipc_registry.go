package llmcli

import "github.com/php-workx/fabrikk/llmclient"

func init() {
	registerBackendFactory(backendFactory{
		Name:         "claude-ipc",
		Binary:       "claude",
		Preference:   PreferClaude,
		Capabilities: claudeIPCStaticCapabilities(""),
		New: func(info CliInfo) llmclient.Backend {
			return NewClaudeIPCBackend(info)
		},
	})
}
