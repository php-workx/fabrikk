package llmcli

import "github.com/php-workx/fabrikk/llmclient"

func init() {
	registerBackendFactory(backendFactory{
		Name:         "claude",
		Binary:       "claude",
		Preference:   PreferClaude,
		Capabilities: claudeStaticCapabilities(""),
		New: func(info CliInfo) llmclient.Backend {
			return NewClaudeBackend(info)
		},
	})
}
