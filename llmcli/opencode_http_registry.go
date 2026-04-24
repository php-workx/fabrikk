package llmcli

import "github.com/php-workx/fabrikk/llmclient"

func init() {
	registerBackendFactory(backendFactory{
		Name:       "opencode-serve",
		Binary:     "opencode",
		Preference: PreferOpenCode,
		Capabilities: llmclient.Capabilities{
			Backend: "opencode-serve",
			// StreamingStructuredUnknown: SSE transport is confirmed, but the
			// event schema is not yet pinned. Full-fidelity (tool events, thinking,
			// usage) is not claimed until /doc schema mapping is verified by tests.
			Streaming: llmclient.StreamingStructuredUnknown,
			// Multi-turn is supported (sessions persist across prompt_async calls).
			MultiTurn: true,
			OptionSupport: map[llmclient.OptionName]llmclient.OptionSupport{
				llmclient.OptionOpenCodePort: llmclient.OptionSupportFull,
			},
		},
		New: func(info CliInfo) llmclient.Backend {
			return NewOpenCodeHTTPBackend(info)
		},
	})
}
