package bridge

import (
	"fmt"
	"sync"
)

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Handler)
)

// Register adds h to the process-global handler registry.
func Register(h Handler) {
	if h == nil || h.Op() == "" {
		panic("llmcli bridge: handler must have an op")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[h.Op()]; exists {
		panic(fmt.Sprintf("llmcli bridge: handler already registered for op %q", h.Op()))
	}
	registry[h.Op()] = h
}

func registeredHandler(op string) (Handler, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	h, ok := registry[op]
	return h, ok
}

func resetRegistryForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = make(map[string]Handler)
}
