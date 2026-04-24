package llmcli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/php-workx/fabrikk/llmclient"
)

// ─── IsOllamaAvailable ───────────────────────────────────────────────────────

// TestIsOllamaAvailable_UsesDefaultBaseURL verifies two things:
//  1. ollamaEffectiveBaseURL with an empty OllamaConfig returns the expected
//     default local address.
//  2. IsOllamaAvailable returns true when a server at the given BaseURL is
//     reachable and returns 200 from GET /api/tags.
func TestIsOllamaAvailable_UsesDefaultBaseURL(t *testing.T) {
	// Part 1: URL resolution — empty config must use the local default.
	empty := llmclient.OllamaConfig{}
	if got := ollamaEffectiveBaseURL(empty); got != ollamaLocalBaseURL {
		t.Errorf("ollamaEffectiveBaseURL(empty) = %q, want %q", got, ollamaLocalBaseURL)
	}

	// Part 2: probe hits /api/tags and returns true on 200.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ollamaTagsPath {
			t.Errorf("unexpected path: %q, want %q", r.URL.Path, ollamaTagsPath)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := llmclient.OllamaConfig{BaseURL: srv.URL}
	if !IsOllamaAvailable(context.Background(), cfg) {
		t.Error("IsOllamaAvailable should return true when server returns 200")
	}
}

// TestIsOllamaAvailable_ReturnsFalseOnError verifies that IsOllamaAvailable
// returns false when the server is unreachable.
func TestIsOllamaAvailable_ReturnsFalseOnError(t *testing.T) {
	// Use a closed server so connections are refused immediately.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	addr := srv.URL
	srv.Close() // close before probing

	cfg := llmclient.OllamaConfig{BaseURL: addr}
	if IsOllamaAvailable(context.Background(), cfg) {
		t.Error("IsOllamaAvailable should return false when server is unreachable")
	}
}

// TestIsOllamaAvailable_ReturnsFalseOnNon200 verifies that a non-200 response
// (e.g. 503) is treated as unavailable.
func TestIsOllamaAvailable_ReturnsFalseOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cfg := llmclient.OllamaConfig{BaseURL: srv.URL}
	if IsOllamaAvailable(context.Background(), cfg) {
		t.Error("IsOllamaAvailable should return false on non-200 response")
	}
}

// ─── OllamaLocalModels ───────────────────────────────────────────────────────

// TestOllamaLocalModels_ParsesTags verifies that OllamaLocalModels correctly
// parses the /api/tags JSON response and returns the model names in order.
func TestOllamaLocalModels_ParsesTags(t *testing.T) {
	payload := `{"models":[{"name":"qwen3.5"},{"name":"glm-5:cloud"},{"name":"gpt-oss:120b"}]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ollamaTagsPath {
			t.Errorf("unexpected path: %q", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, payload)
	}))
	defer srv.Close()

	cfg := llmclient.OllamaConfig{BaseURL: srv.URL}
	models, err := OllamaLocalModels(context.Background(), cfg)
	if err != nil {
		t.Fatalf("OllamaLocalModels: %v", err)
	}

	want := []string{"qwen3.5", "glm-5:cloud", "gpt-oss:120b"}
	if len(models) != len(want) {
		t.Fatalf("models = %v (len %d), want len %d", models, len(models), len(want))
	}
	for i, w := range want {
		if models[i] != w {
			t.Errorf("models[%d] = %q, want %q", i, models[i], w)
		}
	}
}

// TestOllamaLocalModels_EmptyList verifies that OllamaLocalModels returns an
// empty (non-nil) slice when the server reports no models.
func TestOllamaLocalModels_EmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"models":[]}`)
	}))
	defer srv.Close()

	cfg := llmclient.OllamaConfig{BaseURL: srv.URL}
	models, err := OllamaLocalModels(context.Background(), cfg)
	if err != nil {
		t.Fatalf("OllamaLocalModels: %v", err)
	}
	if models == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(models) != 0 {
		t.Errorf("expected 0 models, got %d: %v", len(models), models)
	}
}

// TestOllamaLocalModels_ErrorOnNon200 verifies that OllamaLocalModels returns
// an error when the server responds with a non-200 status.
func TestOllamaLocalModels_ErrorOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := llmclient.OllamaConfig{BaseURL: srv.URL}
	_, err := OllamaLocalModels(context.Background(), cfg)
	if err == nil {
		t.Error("expected error for non-200 response, got nil")
	}
}

// ─── Cloud URL resolution ────────────────────────────────────────────────────

// TestOllamaCloud_UsesAPIKeyAndCloudBaseURL verifies that setting APIKey in
// OllamaConfig (with empty BaseURL) selects ollamaCloudBaseURL automatically.
func TestOllamaCloud_UsesAPIKeyAndCloudBaseURL(t *testing.T) {
	cfg := llmclient.OllamaConfig{APIKey: "sk-cloud-key-123"} //nolint:gosec // test fixture value

	// URL resolution: empty BaseURL + non-empty APIKey → cloud URL.
	got := ollamaEffectiveBaseURL(cfg)
	if got != ollamaCloudBaseURL {
		t.Errorf("ollamaEffectiveBaseURL(cloud) = %q, want %q", got, ollamaCloudBaseURL)
	}

	// Env injection must include OLLAMA_API_KEY for cloud mode.
	env := applyClaudeOllamaEnv([]string{}, cfg)
	assertEnvContains(t, env, "ANTHROPIC_BASE_URL="+ollamaCloudBaseURL)
	assertEnvContains(t, env, "OLLAMA_API_KEY=sk-cloud-key-123")
}

// TestOllamaCloud_ExplicitBaseURLTakesPrecedence verifies that an explicit
// BaseURL overrides the cloud URL even when APIKey is also set.
func TestOllamaCloud_ExplicitBaseURLTakesPrecedence(t *testing.T) {
	cfg := llmclient.OllamaConfig{
		BaseURL: "http://my-custom-ollama:11434",
		APIKey:  "some-key",
	}
	got := ollamaEffectiveBaseURL(cfg)
	if got != "http://my-custom-ollama:11434" {
		t.Errorf("ollamaEffectiveBaseURL = %q, want explicit BaseURL", got)
	}
}

// ─── applyClaudeOllamaEnv ───────────────────────────────────────────────────

// TestApplyClaudeOllamaEnv verifies that the Anthropic API endpoint-override
// variables are injected correctly for both local and cloud Ollama routing.
func TestApplyClaudeOllamaEnv(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/root"}

	t.Run("LocalDefault", func(t *testing.T) {
		cfg := llmclient.OllamaConfig{} // empty → defaults to localhost
		env := applyClaudeOllamaEnv(base, cfg)

		assertEnvContains(t, env, "ANTHROPIC_BASE_URL="+ollamaLocalBaseURL)
		assertEnvContains(t, env, "ANTHROPIC_AUTH_TOKEN=ollama")
		assertEnvContains(t, env, "ANTHROPIC_API_KEY=")
		assertEnvAbsent(t, env)
	})

	t.Run("LocalExplicit", func(t *testing.T) {
		cfg := llmclient.OllamaConfig{BaseURL: "http://localhost:11434"}
		env := applyClaudeOllamaEnv(base, cfg)

		assertEnvContains(t, env, "ANTHROPIC_BASE_URL=http://localhost:11434")
		assertEnvContains(t, env, "ANTHROPIC_AUTH_TOKEN=ollama")
		assertEnvContains(t, env, "ANTHROPIC_API_KEY=")
		assertEnvAbsent(t, env)
	})

	t.Run("Cloud", func(t *testing.T) {
		cfg := llmclient.OllamaConfig{
			BaseURL: "https://ollama.com",
			APIKey:  "cloud-key-abc",
		}
		env := applyClaudeOllamaEnv(base, cfg)

		assertEnvContains(t, env, "ANTHROPIC_BASE_URL=https://ollama.com")
		assertEnvContains(t, env, "ANTHROPIC_AUTH_TOKEN=ollama")
		assertEnvContains(t, env, "ANTHROPIC_API_KEY=")
		assertEnvContains(t, env, "OLLAMA_API_KEY=cloud-key-abc")
	})

	t.Run("DoesNotMutateBase", func(t *testing.T) {
		original := make([]string, len(base))
		copy(original, base)

		cfg := llmclient.OllamaConfig{}
		_ = applyClaudeOllamaEnv(base, cfg)

		for i, got := range base {
			if got != original[i] {
				t.Errorf("base[%d] was mutated: %q → %q", i, original[i], got)
			}
		}
	})
}

// ─── applyCodexAppServerOllamaEnv ──────────────────────────────────────────

// TestApplyCodexAppServerOllamaEnv verifies that the OpenAI API
// endpoint-override variables are injected for Codex app-server Ollama routing.
func TestApplyCodexAppServerOllamaEnv(t *testing.T) {
	base := []string{"PATH=/usr/bin"}

	t.Run("LocalDefault", func(t *testing.T) {
		cfg := llmclient.OllamaConfig{}
		env := applyCodexAppServerOllamaEnv(base, cfg)

		// Codex uses the OpenAI-compatible endpoint (/v1 suffix).
		assertEnvContains(t, env, "OPENAI_BASE_URL="+ollamaLocalBaseURL+"/v1")
		assertEnvContains(t, env, "OPENAI_API_KEY=ollama")
		assertEnvAbsent(t, env)
	})

	t.Run("Cloud", func(t *testing.T) {
		cfg := llmclient.OllamaConfig{APIKey: "cloud-key"}
		env := applyCodexAppServerOllamaEnv(base, cfg)

		assertEnvContains(t, env, "OPENAI_BASE_URL="+ollamaCloudBaseURL+"/v1")
		assertEnvContains(t, env, "OPENAI_API_KEY=cloud-key")
		assertEnvContains(t, env, "OLLAMA_API_KEY=cloud-key")
	})

	t.Run("ExplicitBaseURLWithV1", func(t *testing.T) {
		cfg := llmclient.OllamaConfig{BaseURL: "http://custom:11434/v1"}
		env := applyCodexAppServerOllamaEnv(base, cfg)

		// Already has /v1 — must not be doubled.
		assertEnvContains(t, env, "OPENAI_BASE_URL=http://custom:11434/v1")
	})

	t.Run("ExplicitBaseURLWithoutV1", func(t *testing.T) {
		cfg := llmclient.OllamaConfig{BaseURL: "http://custom:11434"}
		env := applyCodexAppServerOllamaEnv(base, cfg)

		assertEnvContains(t, env, "OPENAI_BASE_URL=http://custom:11434/v1")
	})

	t.Run("DoesNotMutateBase", func(t *testing.T) {
		original := make([]string, len(base))
		copy(original, base)

		cfg := llmclient.OllamaConfig{}
		_ = applyCodexAppServerOllamaEnv(base, cfg)

		for i, got := range base {
			if got != original[i] {
				t.Errorf("base[%d] was mutated: %q → %q", i, original[i], got)
			}
		}
	})
}

// ─── applyOmpOllamaEnv ──────────────────────────────────────────────────────

// TestApplyOmpOllamaEnv verifies that omp routing uses the same Anthropic API
// env variables as the Claude backend.
func TestApplyOmpOllamaEnv(t *testing.T) {
	base := []string{"PATH=/usr/bin"}

	t.Run("LocalDefault", func(t *testing.T) {
		cfg := llmclient.OllamaConfig{}
		env := applyOmpOllamaEnv(base, cfg)

		assertEnvContains(t, env, "ANTHROPIC_BASE_URL="+ollamaLocalBaseURL)
		assertEnvContains(t, env, "ANTHROPIC_AUTH_TOKEN=ollama")
		assertEnvContains(t, env, "ANTHROPIC_API_KEY=")
		assertEnvAbsent(t, env)
	})

	t.Run("Cloud", func(t *testing.T) {
		cfg := llmclient.OllamaConfig{APIKey: "ompkey"}
		env := applyOmpOllamaEnv(base, cfg)

		assertEnvContains(t, env, "ANTHROPIC_BASE_URL="+ollamaCloudBaseURL)
		assertEnvContains(t, env, "OLLAMA_API_KEY=ompkey")
	})

	t.Run("MatchesClaudeEnv", func(t *testing.T) {
		// omp uses the Anthropic API format; its env must be identical to Claude's.
		cfg := llmclient.OllamaConfig{BaseURL: "http://localhost:11434", APIKey: "k"}
		claudeEnv := applyClaudeOllamaEnv(base, cfg)
		ompEnv := applyOmpOllamaEnv(base, cfg)

		if len(claudeEnv) != len(ompEnv) {
			t.Errorf("env lengths differ: claude=%d omp=%d", len(claudeEnv), len(ompEnv))
		}
		for i := range claudeEnv {
			if i < len(ompEnv) && claudeEnv[i] != ompEnv[i] {
				t.Errorf("env[%d]: claude=%q omp=%q", i, claudeEnv[i], ompEnv[i])
			}
		}
	})
}

// ─── writeCodexOllamaProfile ────────────────────────────────────────────────

// TestWriteCodexOllamaProfile_DoesNotTouchHomeConfig verifies that
// writeCodexOllamaProfile creates its temp directory outside user config paths,
// writes a TOML file with the ollama-launch profile, and removes the directory
// on cleanup.
func TestWriteCodexOllamaProfile_DoesNotTouchHomeConfig(t *testing.T) {
	cfg := llmclient.OllamaConfig{BaseURL: "http://localhost:11434"}

	path, cleanup, err := writeCodexOllamaProfile(cfg)
	if err != nil {
		t.Fatalf("writeCodexOllamaProfile: %v", err)
	}

	// Must not reside under user config directories.
	homeDir, homeErr := os.UserHomeDir()
	if homeErr == nil {
		assertNotUnderDir(t, path, filepath.Join(homeDir, ".codex"))
		assertNotUnderDir(t, path, filepath.Join(homeDir, ".config", "opencode"))
		assertNotUnderDir(t, path, filepath.Join(homeDir, ".fabrikk"))
	}

	// The config.toml must exist at path/codex/config.toml.
	configPath := filepath.Join(path, "codex", "config.toml")
	content, readErr := os.ReadFile(configPath)
	if readErr != nil {
		cleanup()
		t.Fatalf("read config.toml: %v", readErr)
	}

	// Verify the profile key and required fields.
	toml := string(content)
	if !strings.Contains(toml, "[model_providers."+codexOllamaProfileName+"]") {
		cleanup()
		t.Errorf("config.toml missing [model_providers.%s] section:\n%s", codexOllamaProfileName, toml)
	}
	if !strings.Contains(toml, "base_url") {
		cleanup()
		t.Errorf("config.toml missing base_url field:\n%s", toml)
	}
	// Local config must NOT contain an api_key line.
	if strings.Contains(toml, "api_key") {
		cleanup()
		t.Errorf("config.toml should not contain api_key for local config:\n%s", toml)
	}

	// After cleanup the temp directory must be gone.
	cleanup()
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("temp dir %q should not exist after cleanup; stat: %v", path, statErr)
	}
}

// TestWriteCodexOllamaProfile_CloudIncludesAPIKey verifies that the api_key
// field is present when cfg.APIKey is non-empty.
func TestWriteCodexOllamaProfile_CloudIncludesAPIKey(t *testing.T) {
	cfg := llmclient.OllamaConfig{APIKey: "cloud-secret"}

	path, cleanup, err := writeCodexOllamaProfile(cfg)
	if err != nil {
		t.Fatalf("writeCodexOllamaProfile: %v", err)
	}
	defer cleanup()

	configPath := filepath.Join(path, "codex", "config.toml")
	content, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read config.toml: %v", readErr)
	}

	toml := string(content)
	if !strings.Contains(toml, "api_key") {
		t.Errorf("config.toml should contain api_key for cloud config:\n%s", toml)
	}
	if !strings.Contains(toml, "cloud-secret") {
		t.Errorf("config.toml should contain the API key value:\n%s", toml)
	}

	// Cloud base URL must include /v1.
	if !strings.Contains(toml, ollamaCloudBaseURL+"/v1") {
		t.Errorf("config.toml base_url should be %q:\n%s", ollamaCloudBaseURL+"/v1", toml)
	}
}

// TestWriteCodexOllamaProfile_BaseURLAppended verifies that /v1 is appended
// to the base URL in the TOML profile.
func TestWriteCodexOllamaProfile_BaseURLAppended(t *testing.T) {
	cfg := llmclient.OllamaConfig{BaseURL: "http://localhost:11434"}

	path, cleanup, err := writeCodexOllamaProfile(cfg)
	if err != nil {
		t.Fatalf("writeCodexOllamaProfile: %v", err)
	}
	defer cleanup()

	configPath := filepath.Join(path, "codex", "config.toml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}

	if !strings.Contains(string(content), "http://localhost:11434/v1") {
		t.Errorf("expected base_url to contain /v1 suffix; got:\n%s", content)
	}
}

// ─── writeOpenCodeOllamaConfig ──────────────────────────────────────────────

// TestWriteOpenCodeOllamaConfig_DoesNotTouchHomeConfig verifies that
// writeOpenCodeOllamaConfig creates its temp directory outside user config
// paths, writes a valid JSON config, and removes the directory on cleanup.
func TestWriteOpenCodeOllamaConfig_DoesNotTouchHomeConfig(t *testing.T) {
	cfg := llmclient.OllamaConfig{
		BaseURL: "http://localhost:11434",
		Model:   "qwen3.5",
	}

	path, cleanup, err := writeOpenCodeOllamaConfig(cfg)
	if err != nil {
		t.Fatalf("writeOpenCodeOllamaConfig: %v", err)
	}

	// Must not reside under user config directories.
	homeDir, homeErr := os.UserHomeDir()
	if homeErr == nil {
		assertNotUnderDir(t, path, filepath.Join(homeDir, ".config", "opencode"))
		assertNotUnderDir(t, path, filepath.Join(homeDir, ".codex"))
		assertNotUnderDir(t, path, filepath.Join(homeDir, ".fabrikk"))
	}

	// The config.json must exist at path/opencode/config.json.
	configPath := filepath.Join(path, "opencode", "config.json")
	raw, readErr := os.ReadFile(configPath)
	if readErr != nil {
		cleanup()
		t.Fatalf("read config.json: %v", readErr)
	}

	// Must be valid JSON.
	var parsed openCodeOllamaConfigJSON
	if jsonErr := json.Unmarshal(raw, &parsed); jsonErr != nil {
		cleanup()
		t.Fatalf("config.json is not valid JSON: %v\ncontents: %s", jsonErr, raw)
	}

	// Verify provider is present.
	if _, ok := parsed.Providers["ollama"]; !ok {
		cleanup()
		t.Errorf("config.json missing 'ollama' provider; providers: %v", parsed.Providers)
	}

	// Verify model is written in the expected format.
	if parsed.Model != "ollama/qwen3.5" {
		cleanup()
		t.Errorf("config.json model = %q, want %q", parsed.Model, "ollama/qwen3.5")
	}

	// After cleanup the temp directory must be gone.
	cleanup()
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("temp dir %q should not exist after cleanup; stat: %v", path, statErr)
	}
}

// TestWriteOpenCodeOllamaConfig_CloudAPIKey verifies that the API key is set
// in the provider block when cfg.APIKey is non-empty.
func TestWriteOpenCodeOllamaConfig_CloudAPIKey(t *testing.T) {
	cfg := llmclient.OllamaConfig{APIKey: "cloud-opencode-key"}

	path, cleanup, err := writeOpenCodeOllamaConfig(cfg)
	if err != nil {
		t.Fatalf("writeOpenCodeOllamaConfig: %v", err)
	}
	defer cleanup()

	configPath := filepath.Join(path, "opencode", "config.json")
	raw, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read config.json: %v", readErr)
	}

	var parsed openCodeOllamaConfigJSON
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse config.json: %v", err)
	}

	provider, ok := parsed.Providers["ollama"]
	if !ok {
		t.Fatal("config.json missing 'ollama' provider")
	}
	if provider.APIKey != "cloud-opencode-key" {
		t.Errorf("provider.APIKey = %q, want %q", provider.APIKey, "cloud-opencode-key")
	}

	// BaseURL should be the Ollama Cloud endpoint with /v1.
	wantURL := ollamaCloudBaseURL + "/v1"
	if provider.BaseURL != wantURL {
		t.Errorf("provider.BaseURL = %q, want %q", provider.BaseURL, wantURL)
	}
}

// TestWriteOpenCodeOllamaConfig_NoModelOmitsField verifies that when cfg.Model
// is empty, the model field is absent from the JSON output.
func TestWriteOpenCodeOllamaConfig_NoModelOmitsField(t *testing.T) {
	cfg := llmclient.OllamaConfig{} // no model

	path, cleanup, err := writeOpenCodeOllamaConfig(cfg)
	if err != nil {
		t.Fatalf("writeOpenCodeOllamaConfig: %v", err)
	}
	defer cleanup()

	configPath := filepath.Join(path, "opencode", "config.json")
	raw, _ := os.ReadFile(configPath)

	var parsed openCodeOllamaConfigJSON
	_ = json.Unmarshal(raw, &parsed)

	if parsed.Model != "" {
		t.Errorf("model field should be empty when cfg.Model is unset; got %q", parsed.Model)
	}
}

// ─── buildCodexOllamaProfileTOML ────────────────────────────────────────────

// TestBuildCodexOllamaProfileTOML_LocalDefaults verifies that local Ollama
// config contains the expected TOML structure with /v1 URL.
func TestBuildCodexOllamaProfileTOML_LocalDefaults(t *testing.T) {
	cfg := llmclient.OllamaConfig{}
	toml := buildCodexOllamaProfileTOML(cfg)

	wantSection := "[model_providers." + codexOllamaProfileName + "]"
	if !strings.Contains(toml, wantSection) {
		t.Errorf("TOML missing section %q:\n%s", wantSection, toml)
	}
	wantURL := ollamaLocalBaseURL + "/v1"
	if !strings.Contains(toml, wantURL) {
		t.Errorf("TOML should contain %q:\n%s", wantURL, toml)
	}
}

// ─── ollamaOpenAIBaseURL ─────────────────────────────────────────────────────

// TestOllamaOpenAIBaseURL verifies that /v1 is appended exactly once to the
// base URL, regardless of trailing slash or existing /v1 suffix.
func TestOllamaOpenAIBaseURL(t *testing.T) {
	cases := []struct {
		cfg  llmclient.OllamaConfig
		want string
	}{
		{llmclient.OllamaConfig{}, ollamaLocalBaseURL + "/v1"},
		{llmclient.OllamaConfig{BaseURL: "http://localhost:11434"}, "http://localhost:11434/v1"},
		{llmclient.OllamaConfig{BaseURL: "http://localhost:11434/"}, "http://localhost:11434/v1"},
		{llmclient.OllamaConfig{BaseURL: "http://localhost:11434/v1"}, "http://localhost:11434/v1"},
		{llmclient.OllamaConfig{APIKey: "key"}, ollamaCloudBaseURL + "/v1"},
		{llmclient.OllamaConfig{BaseURL: "https://ollama.com"}, "https://ollama.com/v1"},
	}

	for _, tc := range cases {
		got := ollamaOpenAIBaseURL(tc.cfg)
		if got != tc.want {
			t.Errorf("ollamaOpenAIBaseURL(%+v) = %q, want %q", tc.cfg, got, tc.want)
		}
	}
}

// ─── Criterion 4: Option support matrix ─────────────────────────────────────

// TestOptionSupportMatrix_WithOllama verifies that backends correctly declare
// their Ollama routing support in static capabilities.
func TestOptionSupportMatrix_WithOllama(t *testing.T) {
	cases := []struct {
		backendName       string
		wantOllamaRouting bool
		wantOptionSupport llmclient.OptionSupport
	}{
		{"claude", true, llmclient.OptionSupportFull},
		{"codex-exec", true, llmclient.OptionSupportFull},
		{"codex-appserver", true, llmclient.OptionSupportFull},
		{"opencode-run", true, llmclient.OptionSupportFull},
		{"omp", true, llmclient.OptionSupportFull},
		{"omp-rpc", true, llmclient.OptionSupportFull},
	}

	for _, tc := range cases {
		t.Run(tc.backendName, func(t *testing.T) {
			f, ok := factoryByName(tc.backendName)
			if !ok {
				t.Skipf("backend %q not registered (may not be compiled)", tc.backendName)
			}

			caps := f.Capabilities
			if caps.OllamaRouting != tc.wantOllamaRouting {
				t.Errorf("OllamaRouting = %v, want %v", caps.OllamaRouting, tc.wantOllamaRouting)
			}

			got := caps.OptionSupport[llmclient.OptionOllama]
			if got != tc.wantOptionSupport {
				t.Errorf("OptionSupport[ollama] = %q, want %q", got, tc.wantOptionSupport)
			}
		})
	}
}

// TestOllamaRequirementFiltersBackends verifies that Requirements.NeedsOllama
// filters out all backends that do not advertise OllamaRouting, so the
// selection logic correctly rejects non-Ollama-capable backends.
func TestOllamaRequirementFiltersBackends(t *testing.T) {
	req := Requirements{NeedsOllama: true}

	// All registered factories.
	allFactories := registeredBackendFactories()

	for _, f := range allFactories {
		satisfies := satisfiesStaticRequirements(f.Capabilities, req)
		if satisfies != f.Capabilities.OllamaRouting {
			t.Errorf("backend %q: satisfies(NeedsOllama)=%v but OllamaRouting=%v",
				f.Name, satisfies, f.Capabilities.OllamaRouting)
		}
	}
}
