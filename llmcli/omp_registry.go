package llmcli

import "github.com/php-workx/fabrikk/llmclient"

func init() {
	// Register omp (print mode) with PreferOmp so it sorts after all public
	// CLI backends (Claude/Codex/OpenCode). omp is internal-only; public users
	// will never have it installed and it must not win over public CLIs by default.
	registerBackendFactory(backendFactory{
		Name:         "omp",
		Binary:       "omp",
		Preference:   PreferOmp,
		Capabilities: ompPrintStaticCapabilities(""),
		New: func(info CliInfo) llmclient.Backend {
			return NewOmpBackend(info)
		},
	})

	// Register omp-rpc (bidirectional RPC) also at PreferOmp, after omp print.
	// Registration order within the same Preference value breaks ties, so
	// omp-rpc sorts after omp-print in the default priority list.
	//
	// omp-rpc is the only backend that satisfies HostToolDefs, HostToolApproval,
	// and ToolResultInjection requirements; all other backends are filtered out
	// by satisfiesStaticRequirements when those requirements are set.
	registerBackendFactory(backendFactory{
		Name:         "omp-rpc",
		Binary:       "omp",
		Preference:   PreferOmp,
		Capabilities: ompRPCStaticCapabilities(""),
		New: func(info CliInfo) llmclient.Backend {
			return NewOmpRPCBackend(info)
		},
	})
}
