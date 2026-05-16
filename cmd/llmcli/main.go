package main

import (
	"context"
	"fmt"
	"os"

	"github.com/php-workx/fabrikk/llmcli/bridge"
)

// Version, GitCommit, and BuildDate are injected at build time via -ldflags.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func main() {
	bridge.RegisterBuiltinHandlers()
	if err := bridge.Run(context.Background(), bridge.Config{}); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
