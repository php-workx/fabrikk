package llmcli

import "github.com/php-workx/fabrikk/llmclient"

func init() {
	registerBackendFactory(backendFactory{
		Name:         "opencode-serve",
		Binary:       "opencode",
		Preference:   PreferOpenCode,
		Capabilities: openCodeHTTPStaticCapabilities(""),
		New: func(info CliInfo) llmclient.Backend {
			return NewOpenCodeHTTPBackend(info)
		},
	})
}
