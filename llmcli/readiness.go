package llmcli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/php-workx/fabrikk/llmclient"
)

func readyMissingBinary(name, path string) llmclient.ReadyReport {
	if path == "" {
		return llmclient.ReadyReport{State: llmclient.ReadyMissingBinary, Detail: fmt.Sprintf("%s binary is not on PATH", name)}
	}
	return llmclient.ReadyReport{State: llmclient.ReadyMissingBinary, Detail: fmt.Sprintf("%s binary is not available at %s", name, path)}
}

func readyNotAuthed(name, hint string) llmclient.ReadyReport {
	return llmclient.ReadyReport{State: llmclient.ReadyNotAuthed, Detail: fmt.Sprintf("%s is installed but authentication was not found; %s", name, hint)}
}

func homePath(parts ...string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	all := append([]string{home}, parts...)
	return filepath.Join(all...)
}

func anyPathExists(paths ...string) bool {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// xdgConfigPath returns the XDG config path for the given sub-path components.
// Falls back to ~/.config if XDG_CONFIG_HOME is unset.
func xdgConfigPath(parts ...string) string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = homePath(".config")
	}
	if base == "" {
		return ""
	}
	return filepath.Join(append([]string{base}, parts...)...)
}
