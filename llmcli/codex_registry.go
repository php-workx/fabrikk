package llmcli

import "github.com/php-workx/fabrikk/llmclient"

func init() {
	registerBackendFactory(backendFactory{
		Name:   "codex-exec",
		Binary: "codex",
		// PreferCodex places this backend after the higher-fidelity
		// codex-appserver (when available) but ahead of the opencode backends.
		// Registration order within the same Preference level gives codex-exec
		// lower priority than codex-appserver (which registers first).
		Preference:   PreferCodex,
		Capabilities: codexExecStaticCapabilities(""),
		New: func(info CliInfo) llmclient.Backend {
			return NewCodexBackend(info)
		},
	})
}
