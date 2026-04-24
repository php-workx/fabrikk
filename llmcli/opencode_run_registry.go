package llmcli

import "github.com/php-workx/fabrikk/llmclient"

func init() {
	registerBackendFactory(backendFactory{
		Name:   "opencode-run",
		Binary: "opencode",
		// PreferOpenCode places this backend after the higher-fidelity
		// opencode-serve (when available). Registration order within the same
		// Preference level gives opencode-run lower priority than opencode-serve
		// (which registers first).
		Preference:   PreferOpenCode,
		Capabilities: openCodeRunStaticCapabilities(""),
		New: func(info CliInfo) llmclient.Backend {
			return NewOpenCodeRunBackend(info)
		},
	})
}
