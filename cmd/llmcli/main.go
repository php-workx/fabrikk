package main

import (
	"context"
	"fmt"
	"os"

	"github.com/php-workx/fabrikk/llmcli/bridge"
)

func main() {
	bridge.RegisterBuiltinHandlers()
	if err := bridge.Run(context.Background(), bridge.Config{}); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
