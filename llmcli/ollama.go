package llmcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// ollamaLocalBaseURL is the default base URL for a locally-running Ollama
// instance. Used when OllamaConfig.BaseURL is empty and no APIKey is set.
const ollamaLocalBaseURL = "http://localhost:11434"

// ollamaCloudBaseURL is the Ollama Cloud base URL. Used when
// OllamaConfig.BaseURL is empty and OllamaConfig.APIKey is non-empty.
const ollamaCloudBaseURL = "https://ollama.com"

// ollamaTagsPath is the native Ollama API path for listing available models.
const ollamaTagsPath = "/api/tags"

// ollamaAvailabilityTimeout is the per-probe HTTP deadline for IsOllamaAvailable.
const ollamaAvailabilityTimeout = 2 * time.Second

// ollamaEffectiveBaseURL resolves the Ollama base URL from cfg:
//   - If cfg.BaseURL is non-empty, it is returned as-is.
//   - If cfg.APIKey is non-empty (cloud mode), ollamaCloudBaseURL is returned.
//   - Otherwise, ollamaLocalBaseURL is returned.
func ollamaEffectiveBaseURL(cfg llmclient.OllamaConfig) string {
	if cfg.BaseURL != "" {
		return cfg.BaseURL
	}
	if cfg.APIKey != "" {
		return ollamaCloudBaseURL
	}
	return ollamaLocalBaseURL
}

// ollamaOpenAIBaseURL returns the OpenAI-compatible Ollama base URL for cfg.
// Ollama's OpenAI-compatible endpoint is at <base>/v1. When the effective base
// URL does not already end with "/v1", "/v1" is appended.
func ollamaOpenAIBaseURL(cfg llmclient.OllamaConfig) string {
	base := strings.TrimRight(ollamaEffectiveBaseURL(cfg), "/")
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}

// IsOllamaAvailable reports whether an Ollama instance is reachable at the
// base URL in cfg. It issues GET /api/tags with a 2-second timeout applied
// on top of ctx. Returns false on any network error or non-200 response.
func IsOllamaAvailable(ctx context.Context, cfg llmclient.OllamaConfig) bool {
	probeCtx, cancel := context.WithTimeout(ctx, ollamaAvailabilityTimeout)
	defer cancel()

	url := ollamaEffectiveBaseURL(cfg) + ollamaTagsPath
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return false
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ollamaTagsResponse is the JSON body returned by GET /api/tags.
type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// OllamaLocalModels returns the names of all models available at the Ollama
// instance configured in cfg by calling GET /api/tags. The ctx governs
// cancellation and deadline; no additional timeout is applied.
func OllamaLocalModels(ctx context.Context, cfg llmclient.OllamaConfig) ([]string, error) {
	url := ollamaEffectiveBaseURL(cfg) + ollamaTagsPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("ollama: build tags request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: GET /api/tags: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // body close error is non-actionable for a read-only probe

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("ollama: GET /api/tags returned %d", resp.StatusCode)
	}

	var data ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("ollama: decode tags response: %w", err)
	}

	names := make([]string, len(data.Models))
	for i, m := range data.Models {
		names[i] = m.Name
	}
	return names, nil
}

// applyClaudeOllamaEnv returns a new env slice (built from env) with Anthropic
// API endpoint-override variables injected for Ollama routing. The injected
// variables are:
//
//   - ANTHROPIC_BASE_URL — the effective Ollama base URL
//   - ANTHROPIC_AUTH_TOKEN=ollama — required sentinel for Ollama routing
//   - ANTHROPIC_API_KEY= — clears any existing key
//   - OLLAMA_API_KEY — only when cfg.APIKey is non-empty (Ollama Cloud)
//
// env is not mutated; a fresh slice is returned.
func applyClaudeOllamaEnv(env []string, cfg llmclient.OllamaConfig) []string {
	overrides := map[string]string{
		"ANTHROPIC_API_KEY":    "",
		"ANTHROPIC_AUTH_TOKEN": "ollama",
		"ANTHROPIC_BASE_URL":   ollamaEffectiveBaseURL(cfg),
	}
	if cfg.APIKey != "" {
		overrides["OLLAMA_API_KEY"] = cfg.APIKey
	}

	filtered := make([]string, 0, len(env)+len(overrides))
	for _, entry := range env {
		key := envKey(entry)
		if strings.HasPrefix(key, "ANTHROPIC_") || key == "OLLAMA_API_KEY" {
			continue
		}
		filtered = append(filtered, entry)
	}
	return applyEnvOverridesLast(filtered, overrides)
}

// applyCodexAppServerOllamaEnv returns a new env slice (built from env) with
// OpenAI API endpoint-override variables injected for routing the Codex
// app-server through Ollama. Codex uses the OpenAI-compatible API; the
// injected variables are:
//
//   - OPENAI_BASE_URL — the Ollama OpenAI-compatible endpoint (<base>/v1)
//   - OPENAI_API_KEY — set to cfg.APIKey if non-empty, otherwise "ollama"
//   - OLLAMA_API_KEY — only when cfg.APIKey is non-empty (Ollama Cloud)
//
// env is not mutated; a fresh slice is returned.
func applyCodexAppServerOllamaEnv(env []string, cfg llmclient.OllamaConfig) []string {
	return applyEnvOverridesLast(env, codexAppServerOllamaEnvOverrides(cfg))
}

func codexAppServerOllamaEnvOverrides(cfg llmclient.OllamaConfig) map[string]string {
	overrides := map[string]string{
		"OPENAI_BASE_URL": ollamaOpenAIBaseURL(cfg),
	}
	if cfg.APIKey != "" {
		overrides["OPENAI_API_KEY"] = cfg.APIKey
		overrides["OLLAMA_API_KEY"] = cfg.APIKey
	} else {
		overrides["OPENAI_API_KEY"] = "ollama"
	}
	return overrides
}

// applyOmpOllamaEnv returns a new env slice (built from env) with Anthropic
// API endpoint-override variables injected for routing omp through Ollama.
// omp uses the Anthropic Messages API internally; the routing variables match
// those applied to the Claude backend. See applyClaudeOllamaEnv for details.
//
// env is not mutated; a fresh slice is returned.
func applyOmpOllamaEnv(env []string, cfg llmclient.OllamaConfig) []string {
	return applyClaudeOllamaEnv(env, cfg)
}

// codexOllamaProfileName is the key used under [model_providers] in the
// temp Codex config.toml written by writeCodexOllamaProfile.
const codexOllamaProfileName = "ollama-launch"

// writeCodexOllamaProfile writes a temporary Codex config.toml containing a
// [model_providers.ollama-launch] profile for Ollama routing. The caller
// passes the returned path as XDG_CONFIG_HOME when spawning the codex binary
// and uses --profile ollama-launch to select the profile.
//
// The returned cleanup function removes the entire temporary directory and must
// be called exactly once after the subprocess exits.
//
// The temporary directory is placed in the OS-default temp location — never
// inside ~/.codex, ~/.config/opencode, or ~/.fabrikk.
func writeCodexOllamaProfile(cfg llmclient.OllamaConfig) (path string, cleanup func(), err error) {
	tmpDir, mkErr := os.MkdirTemp("", "fabrikk-codex-ollama-*")
	if mkErr != nil {
		return "", func() {}, fmt.Errorf("create temp dir: %w", mkErr)
	}
	cleanupFn := func() { _ = os.RemoveAll(tmpDir) }

	// Codex reads config from $XDG_CONFIG_HOME/codex/config.toml.
	codexDir := filepath.Join(tmpDir, "codex")
	if dirErr := os.Mkdir(codexDir, 0o700); dirErr != nil {
		cleanupFn()
		return "", func() {}, fmt.Errorf("create codex config dir: %w", dirErr)
	}

	content := buildCodexOllamaProfileTOML(cfg)
	configPath := filepath.Join(codexDir, "config.toml")
	if writeErr := os.WriteFile(configPath, []byte(content), 0o600); writeErr != nil {
		cleanupFn()
		return "", func() {}, fmt.Errorf("write codex config.toml: %w", writeErr)
	}

	return tmpDir, cleanupFn, nil
}

// buildCodexOllamaProfileTOML returns the TOML content for the ollama-launch
// profile. Codex expects the OpenAI-compatible endpoint (/v1 suffix).
func buildCodexOllamaProfileTOML(cfg llmclient.OllamaConfig) string {
	baseURL := ollamaOpenAIBaseURL(cfg)

	var sb strings.Builder
	fmt.Fprintf(&sb, "[model_providers.%s]\n", codexOllamaProfileName)
	fmt.Fprintf(&sb, "name = \"Ollama\"\n")
	fmt.Fprintf(&sb, "base_url = %q\n", baseURL)
	if cfg.APIKey != "" {
		fmt.Fprintf(&sb, "api_key = %q\n", cfg.APIKey)
	}
	return sb.String()
}

// openCodeOllamaProvider is the provider block written to the temporary
// OpenCode config file for Ollama routing.
type openCodeOllamaProvider struct {
	// Name is the human-readable provider name.
	Name string `json:"name"`
	// BaseURL is the OpenAI-compatible API endpoint.
	BaseURL string `json:"baseUrl"`
	// APIKey is optional; omitted for local Ollama instances.
	APIKey string `json:"apiKey,omitempty"`
}

// openCodeOllamaConfigJSON is the top-level JSON object written to
// $XDG_CONFIG_HOME/opencode/opencode.json for Ollama routing.
type openCodeOllamaConfigJSON struct {
	// Model is the model identifier in "ollama/<name>" format when cfg.Model
	// is non-empty; omitted otherwise so OpenCode uses its default.
	Model string `json:"model,omitempty"`
	// Providers maps provider IDs to their configuration.
	Providers map[string]openCodeOllamaProvider `json:"providers,omitempty"`
}

// writeOpenCodeOllamaConfig writes a temporary OpenCode opencode.json with an
// Ollama provider block. The caller passes the returned path as XDG_CONFIG_HOME
// when spawning opencode so that it reads the ephemeral config instead of the
// user's real config.
//
// The returned cleanup function removes the entire temporary directory and must
// be called exactly once after the subprocess exits.
//
// The temporary directory is placed in the OS-default temp location — never
// inside ~/.config/opencode, ~/.codex, or ~/.fabrikk.
func writeOpenCodeOllamaConfig(cfg llmclient.OllamaConfig) (path string, cleanup func(), err error) {
	tmpDir, mkErr := os.MkdirTemp("", "fabrikk-opencode-ollama-*")
	if mkErr != nil {
		return "", func() {}, fmt.Errorf("create temp dir: %w", mkErr)
	}
	cleanupFn := func() { _ = os.RemoveAll(tmpDir) }

	// OpenCode reads config from $XDG_CONFIG_HOME/opencode/opencode.json.
	openCodeDir := filepath.Join(tmpDir, "opencode")
	if dirErr := os.Mkdir(openCodeDir, 0o700); dirErr != nil {
		cleanupFn()
		return "", func() {}, fmt.Errorf("create opencode config dir: %w", dirErr)
	}

	ocCfg := buildOpenCodeOllamaConfigJSON(cfg)
	data, jsonErr := json.MarshalIndent(ocCfg, "", "  ")
	if jsonErr != nil {
		cleanupFn()
		return "", func() {}, fmt.Errorf("marshal opencode config: %w", jsonErr)
	}

	configPath := filepath.Join(openCodeDir, "opencode.json")
	if writeErr := os.WriteFile(configPath, data, 0o600); writeErr != nil {
		cleanupFn()
		return "", func() {}, fmt.Errorf("write opencode opencode.json: %w", writeErr)
	}

	return tmpDir, cleanupFn, nil
}

// buildOpenCodeOllamaConfigJSON constructs the openCodeOllamaConfigJSON struct
// for the given OllamaConfig. The model is expressed as "ollama/<name>" when
// cfg.Model is non-empty.
func buildOpenCodeOllamaConfigJSON(cfg llmclient.OllamaConfig) openCodeOllamaConfigJSON {
	ocCfg := openCodeOllamaConfigJSON{
		Providers: map[string]openCodeOllamaProvider{
			"ollama": {
				Name:    "Ollama",
				BaseURL: ollamaOpenAIBaseURL(cfg),
				APIKey:  cfg.APIKey,
			},
		},
	}
	if cfg.Model != "" {
		ocCfg.Model = "ollama/" + cfg.Model
	}
	return ocCfg
}
