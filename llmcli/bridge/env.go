package bridge

import (
	"os"
	"strings"
)

func backendNamesFromEnv() []string {
	value := strings.TrimSpace(os.Getenv("LLMCLI_BRIDGE_BACKEND"))
	if value == "" {
		return []string{"claude"}
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name != "" {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return []string{"claude"}
	}
	return out
}
