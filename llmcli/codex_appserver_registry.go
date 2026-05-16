package llmcli

import "github.com/php-workx/fabrikk/llmclient"

func init() {
	// codex-appserver is registered with PreferCodex priority and must be added
	// to the registry before codex-exec (when that backend is implemented) so
	// that the stable sort in registeredBackendFactories returns it first when
	// both share the same Preference tier.
	registerBackendFactory(backendFactory{
		Name:         "codex-appserver",
		Binary:       "codex",
		Preference:   PreferCodex,
		Capabilities: codexAppServerStaticCapabilities(""),
		New: func(info CliInfo) llmclient.Backend {
			return NewCodexAppServerBackend(info)
		},
	})
}
