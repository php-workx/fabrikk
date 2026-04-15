# LLM Provider Authentication & Authorization Flows

Technical specification documenting how terminal-based AI coding agents authenticate with
LLM providers and make streaming API calls. Derived from analysis of the oh-my-pi (`omp`)
codebase (v14.0.4), which implements flows for 30+ providers.

**Date:** 2026-04-12
**Source:** `packages/ai/src/` in [can1357/oh-my-pi](https://github.com/can1357/oh-my-pi)
**Porting target:** Go package `github.com/php-workx/fabrikk/llmauth`
**Related specs:** [`llm-client.md`](./llm-client.md) (request assembly), [`llm-cli.md`](./llm-cli.md) (CLI shell-out alternative)

> **This is a porting spec.** Every section references the exact TypeScript source file
> and line number in the omp codebase. The Go implementation should mirror the omp
> structure as closely as possible. File references use the format
> `pkg/ai/src/path/file.ts:LINE` relative to the monorepo root at
> `/Users/runger/workspaces/oh-my-pi/`.

---

## Table of Contents

- [Architecture Overview](#architecture-overview)
- [Credential Storage](#credential-storage)
- [Authentication Patterns](#authentication-patterns)
  - [Pattern A: API Key (Environment Variable)](#pattern-a-api-key-environment-variable)
  - [Pattern B: OAuth 2.0 + PKCE (Browser Callback)](#pattern-b-oauth-20--pkce-browser-callback)
  - [Pattern C: Device Code Flow (Polling)](#pattern-c-device-code-flow-polling)
  - [Pattern D: Deep Link + Polling (Custom)](#pattern-d-deep-link--polling-custom)
  - [Pattern E: No Auth (Local)](#pattern-e-no-auth-local)
- [Provider Reference](#provider-reference)
  - [Anthropic (Claude)](#anthropic-claude)
  - [OpenAI / OpenAI Codex](#openai--openai-codex)
  - [Google (Gemini CLI / Vertex / Antigravity)](#google-gemini-cli--vertex--antigravity)
  - [GitHub Copilot](#github-copilot)
  - [Cursor](#cursor)
  - [Ollama](#ollama)
- [API Call Patterns](#api-call-patterns)
  - [Anthropic Messages API](#anthropic-messages-api)
  - [OpenAI Responses API](#openai-responses-api)
  - [OpenAI Completions API](#openai-completions-api)
  - [OpenAI Codex Responses API](#openai-codex-responses-api)
  - [Google Generative AI](#google-generative-ai)
  - [OpenAI-Compatible (Ollama, LM Studio, vLLM, etc.)](#openai-compatible-ollama-lm-studio-vllm-etc)
- [Token Lifecycle](#token-lifecycle)
- [Required Client Identity Headers](#required-client-identity-headers)
- [Error Handling & Retry](#error-handling--retry)
- [Suggested Go Package Structure](#suggested-go-package-structure)
- [Monorepo Layout](#monorepo-layout)
- [Implementation Checklist](#implementation-checklist)

---

## Source Reference Map

Quick-reference table of every file to port, with the key symbols and their line numbers.
All paths relative to `/Users/runger/workspaces/oh-my-pi/`.

### Core Plumbing

| File | Key Symbols | Lines |
|---|---|---|
| `packages/ai/src/stream.ts` | `stream()`, `streamSimple()`, `getEnvApiKey()`, `serviceProviderMap` | 163, 243, 155, 63 |
| `packages/ai/src/api-registry.ts` | `registerCustomApi()`, `getCustomApi()` | 58, 75 |
| `packages/ai/src/auth-storage.ts` | `AuthStorage` class, `AuthCredentialStore` class, SQL schema | 272, 2136, 2280 |
| `packages/ai/src/utils.ts` | `isAnthropicOAuthToken()` | 145 |
| `packages/ai/src/types.ts` | `KnownApi`, `KnownProvider`, `Model`, `Context`, `StreamOptions`, `AssistantMessage` | 33, 87 |
| `packages/ai/src/usage.ts` | `UsageProvider`, `UsageReport`, `UsageLimit`, `CredentialRankingStrategy` | 110, 67, 54, 117 |
| `packages/ai/src/utils/event-stream.ts` | `AssistantMessageEventStream` | 95 |
| `packages/ai/src/model-manager.ts` | `createModelManager()` | 68 |

### OAuth Flows

| File | Key Symbols | Lines |
|---|---|---|
| `packages/ai/src/utils/oauth/pkce.ts` | `generatePKCE()` | 5 |
| `packages/ai/src/utils/oauth/callback-server.ts` | `OAuthCallbackFlow` (abstract class), `CallbackResult` | 32, 20 |
| `packages/ai/src/utils/oauth/types.ts` | `OAuthCredentials`, `OAuthProvider`, `OAuthController`, `OAuthProviderInterface` | 1, 12, 52, 82 |
| `packages/ai/src/utils/oauth/index.ts` | `refreshOAuthToken()`, `getOAuthApiKey()`, `getOAuthProviders()` | 345, 444, 496 |
| `packages/ai/src/utils/oauth/anthropic.ts` | `AnthropicOAuthFlow`, `loginAnthropic()`, `refreshAnthropicToken()` | 16, 99, 107 |
| `packages/ai/src/utils/oauth/openai-codex.ts` | `OpenAICodexOAuthFlow`, `loginOpenAICodex()`, `refreshOpenAICodexToken()`, `decodeJwt()`, `getTokenProfile()` | 56, 138, 149, 28, 40 |
| `packages/ai/src/utils/oauth/google-gemini-cli.ts` | `GeminiCliOAuthFlow`, `loginGeminiCli()`, `refreshGoogleCloudToken()`, `discoverProject()`, `pollOperation()` | 229, 297, 305, 95, 65 |
| `packages/ai/src/utils/oauth/github-copilot.ts` | `loginGitHubCopilot()`, `startDeviceFlow()`, `pollForGitHubAccessToken()`, `refreshGitHubCopilotToken()`, `getBaseUrlFromToken()`, `enableAllGitHubCopilotModels()` | 306, 97, 149, 220, 68, 284 |
| `packages/ai/src/utils/oauth/cursor.ts` | `loginCursor()`, `generateCursorAuthParams()`, `pollCursorAuth()`, `refreshCursorToken()`, `getTokenExpiry()` | 78, 20, 36, 98, 127 |
| `packages/ai/src/utils/oauth/ollama.ts` | `loginOllama()` | 22 |

### Provider Streaming Modules

| File | Key Symbols | Lines |
|---|---|---|
| `packages/ai/src/providers/anthropic.ts` | `streamAnthropic`, `buildAnthropicHeaders()`, `buildAnthropicClientOptions()`, `buildParams()`, `buildAnthropicSystemBlocks()`, `createClaudeBillingHeader()`, `generateClaudeCloakingUserId()`, `applyClaudeToolPrefix()`, `stripClaudeToolPrefix()`, `convertAnthropicMessages()` | 639, 119, 1052, 1387, 1003, 251, 269, 287, 295, 1479 |
| `packages/ai/src/providers/anthropic.ts` | `claudeCodeVersion`, `claudeCodeSystemInstruction`, `claudeToolPrefix`, `claudeCodeHeaders`, `sharedHeaders` | 184, 186, 185, 221, 110 |
| `packages/ai/src/providers/openai-responses.ts` | `streamOpenAIResponses`, `createClient()` | 137, 256 |
| `packages/ai/src/providers/openai-completions.ts` | `streamOpenAICompletions` | 169 |
| `packages/ai/src/providers/openai-codex-responses.ts` | `streamOpenAICodexResponses`, `createCodexHeaders()` | 1286, 1911 |
| `packages/ai/src/providers/openai-codex/constants.ts` | `OPENAI_HEADERS`, `OPENAI_HEADER_VALUES`, `URL_PATHS`, `getCodexAccountId()` | 7, 15, 21, 32 |
| `packages/ai/src/providers/google.ts` | `streamGoogle` | 52 |
| `packages/ai/src/providers/google-gemini-cli.ts` | `streamGoogleGeminiCli`, `getGeminiCliUserAgent()`, `GEMINI_CLI_HEADERS()`, `ANTIGRAVITY_USER_AGENT`, `ANTIGRAVITY_AUTH_HEADERS`, `ANTIGRAVITY_STREAMING_HEADERS` | 455, 71, 90, 78, 100, 106 |
| `packages/ai/src/providers/github-copilot-headers.ts` | `buildCopilotDynamicHeaders()`, `inferCopilotInitiator()`, `resolveGitHubCopilotBaseUrl()` | 110, 22, 14 |
| `packages/ai/src/provider-models/openai-compat.ts` | `ollamaModelManagerOptions()`, `normalizeOllamaBaseUrl()`, `fetchOllamaNativeModels()` | 605, 215, 228 |

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                      CLI Application                     │
│                                                          │
│  ┌──────────┐   ┌─────────────┐   ┌──────────────────┐  │
│  │ Auth      │   │ Model       │   │ Stream Dispatch  │  │
│  │ Storage   │──▶│ Manager     │──▶│ (stream.ts)      │  │
│  │ (SQLite)  │   │             │   │                  │  │
│  └──────────┘   └─────────────┘   └────────┬─────────┘  │
│       ▲                                     │            │
│       │                                     ▼            │
│  ┌──────────┐                    ┌──────────────────┐    │
│  │ OAuth    │                    │ Provider Modules  │    │
│  │ Flows   │                    │ (per-provider     │    │
│  │          │                    │  streaming impl)  │    │
│  └──────────┘                    └────────┬─────────┘    │
└────────────────────────────────────────────┼─────────────┘
                                             │
                              ┌──────────────┼──────────────┐
                              ▼              ▼              ▼
                     ┌──────────┐   ┌──────────┐   ┌──────────┐
                     │ Anthropic│   │ OpenAI   │   │ Google   │
                     │ API      │   │ API      │   │ API      │
                     └──────────┘   └──────────┘   └──────────┘
```

The system has three layers:

1. **Auth Storage** — SQLite database persisting API keys and OAuth credentials
   (access tokens, refresh tokens, expiry timestamps)
2. **Model Manager** — discovers available models per provider, maps provider IDs to
   API types and base URLs
3. **Stream Dispatch** — routes requests to provider-specific streaming implementations
   based on the `model.api` field and Model Manager mappings

Stream Dispatch must abstract over provider-specific streaming transports rather
than assuming one protocol. Supported transports include HTTP streaming/SSE,
WebSockets, gRPC, and other provider protocols selected by the `model.api`
mapping. Known exceptions called out elsewhere in this spec include Codex
WebSocket support and Cursor's custom gRPC protocol. No provider CLIs are
shelled out to.

---

## Credential Storage

Credentials are persisted in a SQLite database via Bun's built-in `bun:sqlite`.

> **Port from:** `packages/ai/src/auth-storage.ts`
> - `AuthStorage` class (line 272) — high-level credential management with round-robin, usage tracking, OAuth refresh
> - `AuthCredentialStore` class (line 2136) — low-level SQLite CRUD operations
> - SQL table `auth_credentials` created at line 2280
> - Schema version: 4 (constant at line 1970)

### Default Storage Location

The credential database lives at a **user-level path** (not project-level) so all
fabrikk-aware tools (`plan`, `verk`, `vakt`, future tools) share the same credentials.
Log in once via any tool, and all of them have access immediately. Same model as `gh`
or `aws cli`.

**Default path:** `~/.fabrikk/auth.db`

**Layout:**
```
~/.fabrikk/                    (mode 0700, user-only)
├── auth.db                    (mode 0600, user-only read/write)
├── auth.db-wal                (mode 0600, SQLite WAL)
├── auth.db-shm                (mode 0600, SQLite shared memory)
└── (future: logs, cache, etc.)
```

**Permissions are strict:**
- Directory: `0700` (`drwx------`) — only the owning user can list/access
- Files: `0600` (`-rw-------`) — only the owning user can read/write

This is a hard requirement, not a recommendation. Refresh tokens stored in this
database can be exchanged for live API tokens; treating them like SSH private keys
is the only correct posture.

**Resolution order** for the database path:
1. `FABRIKK_AUTH_DB` environment variable (if set, used as-is)
2. `$XDG_DATA_HOME/fabrikk/auth.db` (if `XDG_DATA_HOME` is set)
3. `~/.fabrikk/auth.db` (default)

**Go implementation:**
```go
package llmauth

import (
    "errors"
    "fmt"
    "os"
    "path/filepath"
)

// DefaultDBPath returns the resolved credential database path,
// creating parent directories with strict permissions if needed.
func DefaultDBPath() (string, error) {
    if env := os.Getenv("FABRIKK_AUTH_DB"); env != "" {
        parent := filepath.Dir(env)
        if err := os.MkdirAll(parent, 0o700); err != nil {
            return "", fmt.Errorf("create %s: %w", parent, err)
        }
        // os.MkdirAll does not chmod existing directories — enforce explicitly.
        if err := os.Chmod(parent, 0o700); err != nil {
            return "", fmt.Errorf("chmod %s: %w", parent, err)
        }
        return env, nil
    }

    var base string
    if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
        base = filepath.Join(xdg, "fabrikk")
    } else {
        home, err := os.UserHomeDir()
        if err != nil {
            return "", fmt.Errorf("resolve home dir: %w", err)
        }
        base = filepath.Join(home, ".fabrikk")
    }

    if err := os.MkdirAll(base, 0o700); err != nil {
        return "", fmt.Errorf("create %s: %w", base, err)
    }
    // os.MkdirAll does not chmod existing directories — enforce explicitly.
    if err := os.Chmod(base, 0o700); err != nil {
        return "", fmt.Errorf("chmod %s: %w", base, err)
    }

    return filepath.Join(base, "auth.db"), nil
}

// VerifyDBPermissions checks the database file has mode 0600. Call this on
// every Open() — refuse to use a database with overly-permissive permissions,
// since another user could have read/written the credentials.
func VerifyDBPermissions(path string) error {
    info, err := os.Stat(path)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            return nil // first run, file will be created with the right mode
        }
        return err
    }
    mode := info.Mode().Perm()
    if mode&0o077 != 0 {
        return fmt.Errorf("auth database %s has insecure permissions %o (expected 0600)", path, mode)
    }
    return verifyDBOwner(path, info)
}
```

Unix builds must implement the ownership check behind build tags:

```go
//go:build unix

func verifyDBOwner(path string, info os.FileInfo) error {
    stat, ok := info.Sys().(*syscall.Stat_t)
    if !ok {
        return fmt.Errorf("auth database %s ownership metadata unavailable", path)
    }
    if stat.Uid != uint32(os.Geteuid()) {
        return fmt.Errorf("auth database %s is owned by uid %d, want current uid %d", path, stat.Uid, os.Geteuid())
    }
    return nil
}
```

Non-Unix builds must provide a separate `verifyDBOwner` implementation that
returns nil or uses the platform equivalent, and the limitation must be
documented in that file.

**Opening the SQLite database** must use `O_CREATE|O_RDWR` with mode `0600` so the
file is created with the right permissions on first run:

```go
import "modernc.org/sqlite"

func openDB(path string) (*sql.DB, error) {
    if err := VerifyDBPermissions(path); err != nil {
        return nil, err
    }
    // Set this before opening SQLite during startup so the main DB and WAL/SHM
    // sidecars inherit restrictive permissions. Umask is process-wide; do not
    // wrap this around concurrent startup work.
    oldUmask := syscall.Umask(0o077)
    defer syscall.Umask(oldUmask)

    // Touch with strict mode if missing, then VerifyDBPermissions confirms the
    // effective mode after umask has been applied.
    f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
    if err != nil {
        return nil, err
    }
    f.Close()

    return sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
}
```

> **Note on WAL files:** SQLite in WAL mode creates `auth.db-wal` and `auth.db-shm`
> alongside the main database. These contain copies of in-flight transactions,
> including credentials. Make sure they inherit the `0600` mode. Setting the umask
> to `0o077` must be done once at process startup before opening SQLite, because
> umask is process-wide rather than goroutine-scoped. If the process cannot own
> the umask globally, explicitly chmod `auth.db-wal` and `auth.db-shm` after SQLite
> creates them and before continuing credential operations.

**Concurrent access:** Multiple fabrikk-aware tools may open the database
simultaneously. SQLite's WAL mode supports concurrent readers + one writer, which
is sufficient for the credential read-mostly workload. Use `BEGIN IMMEDIATE` for
writes that depend on a prior read (e.g., refresh-then-update).

### Schema

```typescript
type ApiKeyCredential = {
  type: "api_key";
  key: string;
};

type OAuthCredential = {
  type: "oauth";
  refresh: string;       // Refresh token
  access: string;        // Access token (short-lived)
  expires: number;       // Expiry timestamp (ms since epoch)
  enterpriseUrl?: string; // For GitHub Enterprise
  projectId?: string;    // For Google Cloud
  email?: string;        // User email
  accountId?: string;    // For OpenAI Codex
};

type AuthCredential = ApiKeyCredential | OAuthCredential;
```

> **Port from:** `auth-storage.ts` lines 75-84 (credential types), lines 94-105 (`SerializedAuthStorage`), lines 111-116 (`StoredAuthCredential`)

**SQLite table** (port from `auth-storage.ts:2280`):
```sql
CREATE TABLE IF NOT EXISTS auth_credentials (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider TEXT NOT NULL,
    credential_type TEXT NOT NULL,     -- "api_key" or "oauth"
    data TEXT NOT NULL,                -- JSON blob
    disabled_cause TEXT DEFAULT NULL,  -- soft-delete marker
    identity_key TEXT DEFAULT NULL,    -- email/accountId for dedup
    created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER)),
    updated_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER))
);
CREATE INDEX IF NOT EXISTS idx_auth_provider ON auth_credentials(provider);
CREATE INDEX IF NOT EXISTS idx_auth_provider_identity ON auth_credentials(provider, identity_key)
    WHERE identity_key IS NOT NULL;
```

**Serialization** (port from `auth-storage.ts:1988`): Credentials are serialized to JSON
for the `data` column. API keys store `{"key": "..."}`, OAuth stores the full credential
object (access, refresh, expires, projectId, etc.) minus the `type` field.

**Deserialization** (port from `auth-storage.ts:2007`): Parse JSON from `data` column,
reconstruct typed credential based on `credential_type`.

Each provider can have multiple credentials (array). The system supports:
- **Round-robin** credential rotation
- **Usage tracking** per credential (to stay within rate/spend limits)
- **Ranking strategies** (e.g., prefer least-used credential)
- **Auto-refresh** of expired OAuth tokens before API calls

### Resolution Order

> **Port from:** `AuthStorage.getApiKey()` at `auth-storage.ts:1923`

For any API call, the credential is resolved in this order:

1. Runtime override (set programmatically)
2. Stored credentials from SQLite (with auto-refresh for OAuth)
3. OAuth credential store via `getOAuthApiKey()` (`utils/oauth/index.ts:444`)
4. Environment variable lookup via `getEnvApiKey()` (`stream.ts:155`)
5. Fallback config resolver

---

## Authentication Patterns

### Pattern A: API Key (Environment Variable)

> **Port from:** `stream.ts:63-147` (`serviceProviderMap`) and `stream.ts:155` (`getEnvApiKey()`)

The simplest pattern. The user sets an environment variable and the tool reads it.

**Flow:**
```
User sets ENV var → Tool reads from process.env → Passed as header on each request
```

**Env var resolution** checks multiple sources:
1. `process.env` / `Bun.env`
2. `.env` file in current working directory
3. `~/.env` in home directory

**Provider → Environment Variable mapping:**

| Provider | Env Var | Header Used |
|---|---|---|
| `anthropic` | `ANTHROPIC_API_KEY` or `ANTHROPIC_OAUTH_TOKEN` | `X-Api-Key` or `Authorization: Bearer` |
| `openai` | `OPENAI_API_KEY` | `Authorization: Bearer` |
| `google` | `GEMINI_API_KEY` | `x-goog-api-key` |
| `groq` | `GROQ_API_KEY` | `Authorization: Bearer` |
| `xai` | `XAI_API_KEY` | `Authorization: Bearer` |
| `openrouter` | `OPENROUTER_API_KEY` | `Authorization: Bearer` |
| `mistral` | `MISTRAL_API_KEY` | `Authorization: Bearer` |
| `together` | `TOGETHER_API_KEY` | `Authorization: Bearer` |
| `perplexity` | `PERPLEXITY_API_KEY` | `Authorization: Bearer` |
| `cerebras` | `CEREBRAS_API_KEY` | `Authorization: Bearer` |
| `github-copilot` | `COPILOT_GITHUB_TOKEN` / `GH_TOKEN` / `GITHUB_TOKEN` | `Authorization: Bearer` |
| `google-vertex` | `GOOGLE_CLOUD_API_KEY` or ADC file | varies |
| `amazon-bedrock` | `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` / `AWS_PROFILE` / `AWS_BEARER_TOKEN_BEDROCK` | AWS SigV4 |
| `ollama` | `OLLAMA_API_KEY` (optional) | `Authorization: Bearer` |

#### `.env` file loading

omp checks three sources in order for each env var lookup. The first match wins.

1. **Process environment** — `Bun.env` / `process.env` (Go: `os.Getenv`)
2. **`./.env`** — `.env` file in the current working directory
3. **`~/.env`** — `.env` file in the user's home directory

**`.env` file format:**
```bash
# Lines starting with # are comments
ANTHROPIC_API_KEY=sk-ant-api03-...
OPENAI_API_KEY="sk-proj-..."        # quotes are stripped
GEMINI_API_KEY='AIza...'            # single quotes too
EMPTY_VAR=                          # empty values allowed
export OLD_STYLE=value              # "export " prefix stripped
```

**Parsing rules:**
- Skip blank lines and lines starting with `#`
- Split on the first `=` only (values may contain `=`)
- Strip surrounding single or double quotes from values
- Strip optional `export ` prefix from keys
- Trim whitespace from keys; preserve internal whitespace in values

**Go implementation options:**
- `github.com/joho/godotenv` — battle-tested, handles quotes, interpolation, multi-line values
- Manual parser — ~30 lines of code using `bufio.Scanner` and `strings.SplitN(line, "=", 2)`

```go
// Minimal manual resolver
func GetEnvAPIKey(name string) string {
    if v := os.Getenv(name); v != "" {
        return v
    }
    for _, path := range []string{".env", filepath.Join(os.Getenv("HOME"), ".env")} {
        if v := readDotEnv(path, name); v != "" {
            return v
        }
    }
    return ""
}
```

#### API key validation

Before making a full streaming request, you can verify a key works with a lightweight
probe. This catches misconfiguration early (wrong env var, expired key, wrong org).

**Anthropic** — cheapest probe is a `count_tokens` call (free, no generation):
```http
POST https://api.anthropic.com/v1/messages/count_tokens
X-Api-Key: {api_key}
Anthropic-Version: 2023-06-01
Content-Type: application/json

{"model": "claude-3-5-haiku-20241022", "messages": [{"role": "user", "content": "hi"}]}
```
Returns `{"input_tokens": 8}` on success, `401` with `authentication_error` on failure.

**OpenAI** — `GET /v1/models`:
```http
GET https://api.openai.com/v1/models
Authorization: Bearer {api_key}
```
Returns a model list on success, `401` on failure. Also works for most
OpenAI-compatible endpoints (Groq, Together, OpenRouter, Perplexity, Cerebras).

**Google Gemini** — `GET /v1beta/models`:
```http
GET https://generativelanguage.googleapis.com/v1beta/models?key={api_key}
```

**Ollama** — `GET /api/tags` on the configured base URL:
```http
GET http://127.0.0.1:11434/api/tags
```
No auth required for local mode; returns the installed model list.

Port validation logic from `packages/ai/src/utils/oauth/api-key-validation.ts` in the
omp codebase for additional provider-specific probes.

#### Full HTTP request examples (API key mode)

Complete `curl`-style request samples for building your first working request with
just an API key — no OAuth, no subscription. These are the minimum viable requests.

**Anthropic** (`X-Api-Key` header, JSON body):
```http
POST https://api.anthropic.com/v1/messages
X-Api-Key: sk-ant-api03-...
Anthropic-Version: 2023-06-01
Anthropic-Beta: prompt-caching-2024-07-31
Content-Type: application/json
Accept: text/event-stream

{
  "model": "claude-sonnet-4-20250514",
  "max_tokens": 4096,
  "stream": true,
  "system": "You are a helpful assistant.",
  "messages": [
    {"role": "user", "content": "Hello"}
  ]
}
```

> Note: For API key mode, you do NOT need the Claude Code identity headers
> (`User-Agent: claude-cli/...`, Stainless headers, billing header, cloaking user ID).
> Those are only required for OAuth subscription mode. See "Required Client Identity
> Headers" section for the OAuth variant.

**OpenAI Responses API** (`Authorization: Bearer`):
```http
POST https://api.openai.com/v1/responses
Authorization: Bearer sk-proj-...
Content-Type: application/json
Accept: text/event-stream

{
  "model": "gpt-4o",
  "input": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello"}
  ],
  "stream": true,
  "store": false
}
```

**OpenAI Chat Completions API** (legacy, broadest compatibility):
```http
POST https://api.openai.com/v1/chat/completions
Authorization: Bearer sk-proj-...
Content-Type: application/json
Accept: text/event-stream

{
  "model": "gpt-4o",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello"}
  ],
  "stream": true
}
```
Use this shape for OpenAI-compatible providers (Groq, Together, OpenRouter,
Perplexity, Cerebras, vLLM, LM Studio, Ollama) by swapping the base URL and API key.

**Google Gemini** (`x-goog-api-key` header, different endpoint shape):
```http
POST https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse
x-goog-api-key: AIza...
Content-Type: application/json

{
  "systemInstruction": {"parts": [{"text": "You are a helpful assistant."}]},
  "contents": [
    {"role": "user", "parts": [{"text": "Hello"}]}
  ],
  "generationConfig": {"maxOutputTokens": 4096}
}
```

**Ollama** (optional bearer, OpenAI-compatible body):
```http
POST http://127.0.0.1:11434/v1/chat/completions
Authorization: Bearer {optional_api_key}
Content-Type: application/json
Accept: text/event-stream

{
  "model": "llama3.2",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello"}
  ],
  "stream": true
}
```

#### When to use API key vs OAuth

| Consideration | API Key | OAuth |
|---|---|---|
| Setup complexity | Low — one env var | Medium — browser flow, local server, token storage |
| Token refresh | None needed | Required (automatic) |
| Subscription access | ❌ No — pay-as-you-go billing only | ✅ Claude Pro/Max, ChatGPT Plus, Gemini CLI free tier |
| Rate limits | Standard API tier | Subscription tier (often higher) |
| Credential storage | Plain env var or `.env` file | SQLite with refresh tokens |
| Identity headers | None required | Must send Claude Code / Gemini CLI / Copilot identity |
| Offline | Works without browser | Requires browser for initial login |
| Portability | Works in CI, containers, servers | Harder in headless environments |
| Good for | CI pipelines, servers, quick prototypes | Developer workstations, subscription users |

**Default recommendation:** Support both. Check for an env var first (quick path,
works everywhere); fall back to OAuth if no key is set and the user wants to use
their subscription.

> **Alternative backend:** If the user already has a CLI tool installed (`claude`,
> `codex`, `opencode`, `omp`) that handles its own auth, you can skip this entire
> package and shell out to it instead. See [`llm-cli.md`](./llm-cli.md) for the
> CLI backend spec.

### Pattern B: OAuth 2.0 + PKCE (Browser Callback)

> **Port from:**
> - PKCE: `utils/oauth/pkce.ts:5` (`generatePKCE()`)
> - Callback server: `utils/oauth/callback-server.ts:32` (`OAuthCallbackFlow` abstract class)
> - HTML template: `utils/oauth/oauth.html` (imported as text)

Used by providers that offer subscription-based access (Claude Pro/Max, Google Cloud).

**Flow:**
```
1. Generate PKCE verifier + SHA-256 challenge
2. Start local HTTP server on localhost:{port}
3. Build authorize URL with client_id, redirect_uri, code_challenge, scopes
4. Open user's browser to authorize URL
5. User logs in and grants consent
6. Browser redirects to localhost:{port}/callback?code=XXX&state=YYY
7. Local server captures the authorization code
8. Exchange code + verifier for access_token + refresh_token via POST to token endpoint
9. Store credentials in SQLite
```

**PKCE implementation:**
```typescript
async function generatePKCE(): Promise<{ verifier: string; challenge: string }> {
  const verifierBytes = new Uint8Array(96);
  crypto.getRandomValues(verifierBytes);
  const verifier = Buffer.from(verifierBytes).toString("base64url");

  const data = new TextEncoder().encode(verifier);
  const hashBuffer = await crypto.subtle.digest("SHA-256", data);
  const challenge = Buffer.from(hashBuffer).toString("base64url");

  return { verifier, challenge };
}
```

**Callback server:**
- Tries the provider's preferred port first; falls back to a random port if occupied
- Serves an HTML page on callback that shows success/failure to the user
- Has a configurable timeout (default 300s)
- CSRF protection via `state` parameter (random UUID, verified on callback)

**Critical: Content-Type for token exchange varies by provider:**
- **Anthropic**: `application/json` (JSON body)
- **OpenAI Codex**: `application/x-www-form-urlencoded`
- **Google**: `application/x-www-form-urlencoded`
- **GitHub Copilot**: `application/x-www-form-urlencoded`

Getting this wrong will cause silent 400/415 errors.

**Providers using this pattern:**

| Provider | Port | Callback Path | Authorize URL | Token URL |
|---|---|---|---|---|
| Anthropic | 54545 | `/callback` | `https://claude.ai/oauth/authorize` | `https://api.anthropic.com/v1/oauth/token` |
| Google Gemini CLI | 8085 | `/oauth2callback` | `https://accounts.google.com/o/oauth2/v2/auth` | `https://oauth2.googleapis.com/token` |
| OpenAI Codex | 1455 | `/auth/callback` | `https://auth.openai.com/oauth/authorize` | `https://auth.openai.com/oauth/token` |

### Pattern C: Device Code Flow (Polling)

> **Port from:** `utils/oauth/github-copilot.ts` — `startDeviceFlow()` (line 97), `pollForGitHubAccessToken()` (line 149)

Used by GitHub Copilot. The user enters a code on a web page while the CLI polls for completion.

**Flow:**
```
1. POST to /login/device/code with client_id and scope
2. Receive device_code, user_code, verification_uri, interval
3. Display user_code and verification_uri to user
4. User visits verification_uri, enters user_code, authorizes
5. CLI polls POST /login/oauth/access_token with device_code
   - "authorization_pending" → keep polling
   - "slow_down" → increase interval
   - success → receive access_token
6. Exchange GitHub access_token for Copilot session token
   via GET /copilot_internal/v2/token
7. Store both tokens (GitHub token as refresh, Copilot token as access)
```

**Polling parameters:**
- Base interval: from server response (typically 5s)
- Initial multiplier: 1.2x
- Slow-down multiplier: 1.4x (after `slow_down` response)
- Maximum attempts: 150
- Maximum delay: 10s per poll

### Pattern D: Deep Link + Polling (Custom)

> **Port from:** `utils/oauth/cursor.ts` — `generateCursorAuthParams()` (line 20), `pollCursorAuth()` (line 36)

Used by Cursor. Similar to device code but uses PKCE + UUID instead of a device code.

**Flow:**
```
1. Generate PKCE challenge + random UUID
2. Build login URL: https://cursor.com/loginDeepControl?challenge={}&uuid={}&mode=login&redirectTarget=cli
3. Open browser to login URL
4. Poll GET https://api2.cursor.sh/auth/poll?uuid={}&verifier={}
   - 404 → not yet authorized, keep polling
   - 200 → receive accessToken + refreshToken
5. Extract expiry from JWT payload
6. Store credentials
```

**Refresh:** POST to `https://api2.cursor.sh/auth/exchange_user_api_key` with
`Authorization: Bearer {refreshToken}`.

### Pattern E: No Auth (Local)

> **Port from:** `utils/oauth/ollama.ts:22` (`loginOllama()`)

Used by Ollama and LM Studio for local deployments.

**Flow:**
```
1. Optionally prompt user for API key/token
2. If empty → use no-auth mode (no Authorization header)
3. Connect to local server (default: http://127.0.0.1:11434/v1 for Ollama)
```

No OAuth, no browser, no tokens. The API key field is optional and only used for
hosted/authenticated Ollama deployments.

---

## Provider Reference

### Anthropic (Claude)

> **Port from:**
> - Auth: `utils/oauth/anthropic.ts` — `AnthropicOAuthFlow` (line 16), `loginAnthropic()` (line 99), `refreshAnthropicToken()` (line 107)
> - Streaming: `providers/anthropic.ts` — `streamAnthropic` (line 639), `buildAnthropicHeaders()` (line 119), `buildParams()` (line 1387)
> - Identity: `providers/anthropic.ts` — `claudeCodeVersion` (line 184), `claudeCodeHeaders` (line 221), `claudeCodeSystemInstruction` (line 186), `claudeToolPrefix` (line 185)
> - System blocks: `providers/anthropic.ts` — `buildAnthropicSystemBlocks()` (line 1003), `createClaudeBillingHeader()` (line 251)
> - Cloaking: `providers/anthropic.ts` — `generateClaudeCloakingUserId()` (line 269)
> - Tool prefix: `providers/anthropic.ts` — `applyClaudeToolPrefix()` (line 287), `stripClaudeToolPrefix()` (line 295)
> - Token detection: `utils.ts` — `isAnthropicOAuthToken()` (line 145)

**Auth pattern:** OAuth 2.0 + PKCE (Pattern B) or API key (Pattern A)

**OAuth details:**
- Client ID: `<anthropic-oauth-client-id>`
- Scopes: `org:create_api_key user:profile user:inference`
- Authorize URL: `https://claude.ai/oauth/authorize`
- Token URL: `https://api.anthropic.com/v1/oauth/token`
- Callback: `localhost:54545/callback`
- Token type: Bearer (OAuth) or X-Api-Key (API key)
- Expiry buffer: tokens are considered expired 5 minutes before actual expiry

**Token exchange request:**
```http
POST https://api.anthropic.com/v1/oauth/token
Content-Type: application/json

{
  "grant_type": "authorization_code",
  "client_id": "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
  "code": "{authorization_code}",
  "state": "{csrf_state}",
  "redirect_uri": "http://localhost:54545/callback",
  "code_verifier": "{pkce_verifier}"
}
```

**Token refresh request:**
```http
POST https://api.anthropic.com/v1/oauth/token
Content-Type: application/json

{
  "grant_type": "refresh_token",
  "client_id": "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
  "refresh_token": "{refresh_token}"
}
```

**Response:**
```json
{
  "access_token": "...",
  "refresh_token": "...",
  "expires_in": 3600
}
```

**API call headers (OAuth mode — full set):**
```http
POST https://api.anthropic.com/v1/messages
Accept: text/event-stream
Accept-Encoding: gzip, deflate, br, zstd
Connection: keep-alive
Content-Type: application/json
Anthropic-Version: 2023-06-01
Anthropic-Dangerous-Direct-Browser-Access: true
Anthropic-Beta: claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05
Authorization: Bearer {access_token}
User-Agent: claude-cli/2.1.63 (external, cli)
X-App: cli
X-Stainless-Retry-Count: 0
X-Stainless-Runtime-Version: v24.3.0
X-Stainless-Package-Version: 0.74.0
X-Stainless-Runtime: node
X-Stainless-Lang: js
X-Stainless-Arch: arm64
X-Stainless-Os: MacOS
X-Stainless-Timeout: 600
```

**API call headers (API key mode — direct to api.anthropic.com):**
```http
POST https://api.anthropic.com/v1/messages
Accept: text/event-stream
Accept-Encoding: gzip, deflate, br, zstd
Connection: keep-alive
Content-Type: application/json
Anthropic-Version: 2023-06-01
Anthropic-Dangerous-Direct-Browser-Access: true
Anthropic-Beta: claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05
X-Api-Key: {api_key}
X-App: cli
```

**API call headers (API key mode — non-Anthropic proxy/gateway):**
```http
POST https://my-proxy.example.com/v1/messages
Accept: text/event-stream
Accept-Encoding: gzip, deflate, br, zstd
Connection: keep-alive
Content-Type: application/json
Anthropic-Version: 2023-06-01
Anthropic-Dangerous-Direct-Browser-Access: true
Anthropic-Beta: claude-code-20250219,...
Authorization: Bearer {api_key}
```
Note: Non-Anthropic base URLs use `Authorization: Bearer` instead of `X-Api-Key`,
and do NOT include Stainless/Claude Code identity headers.

**SDK used:** `@anthropic-ai/sdk` (`client.messages.create({ ...params, stream: true })`)

**OAuth token detection:**
The system auto-detects OAuth tokens by checking for the `sk-ant-oat` substring:
```typescript
function isAnthropicOAuthToken(key: string): boolean {
  return key.includes("sk-ant-oat");
}
```
This determines which header set to use (OAuth vs API key mode).

**Three header branches** — `buildAnthropicHeaders` selects headers based on auth type:
1. **OAuth token** → Full Claude Code identity (User-Agent, Stainless headers, `Authorization: Bearer`)
2. **API key + non-Anthropic base URL** (proxy/gateway) → Minimal headers, `Authorization: Bearer`, no Stainless/Claude Code identity
3. **API key + Anthropic base URL** → Minimal headers, `X-Api-Key` header (not Bearer)

**Cloaking user ID** — For OAuth requests, a synthetic `metadata.user_id` is generated:
```
user_{64-hex-chars}_account_{uuid}_session_{uuid}
```
This is sent in the request body's `metadata` field. Validation regex:
```
^user_[0-9a-fA-F]{64}_account_[0-9a-f-]{36}_session_[0-9a-f-]{36}$
```

**Claude Code system instruction injection:**
For OAuth tokens (except `claude-3-5-haiku` models), two blocks are prepended to the system prompt:
1. The billing header: `x-anthropic-billing-header: cc_version=2.1.63.{hash}; cc_entrypoint=cli; cch={hash};`
2. The system instruction: `"You are a Claude agent, built on Anthropic's Claude Agent SDK."`

**Tool name prefixing:**
For OAuth tokens, all tool names are prefixed with `proxy_` (except Anthropic built-in
tools: `web_search`, `code_execution`, `text_editor`, `computer`). Tool names in
`tool_choice` are also prefixed. Responses must be stripped of the prefix when parsing.

**Required identity headers:** The OAuth flow and API calls must use the Claude Code
client identity (client ID, User-Agent, beta headers, Stainless headers) to access
subscription-tier features. See [Required Client Identity Headers](#required-client-identity-headers).

---

### OpenAI / OpenAI Codex

> **Port from:**
> - Auth: `utils/oauth/openai-codex.ts` — `OpenAICodexOAuthFlow` (line 56), `loginOpenAICodex()` (line 138), `refreshOpenAICodexToken()` (line 149), `decodeJwt()` (line 28), `getTokenProfile()` (line 40)
> - Streaming (Responses): `providers/openai-responses.ts` — `streamOpenAIResponses` (line 137), `createClient()` (line 256)
> - Streaming (Completions): `providers/openai-completions.ts` — `streamOpenAICompletions` (line 169)
> - Streaming (Codex): `providers/openai-codex-responses.ts` — `streamOpenAICodexResponses` (line 1286), `createCodexHeaders()` (line 1911)
> - Constants: `providers/openai-codex/constants.ts` — `OPENAI_HEADERS` (line 7), `OPENAI_HEADER_VALUES` (line 15), `URL_PATHS` (line 21), `getCodexAccountId()` (line 32)

**Auth pattern (standard OpenAI):** API key via `OPENAI_API_KEY` (Pattern A)

**Auth pattern (Codex / ChatGPT Plus):** OAuth 2.0 + PKCE (Pattern B)

**Codex OAuth details:**
- Client ID: `<openai-oauth-client-id>`
- Scopes: `openid profile email offline_access`
- Authorize URL: `https://auth.openai.com/oauth/authorize`
- Token URL: `https://auth.openai.com/oauth/token`
- Callback: `localhost:1455/auth/callback`
- Extra authorize params: `codex_cli_simplified_flow=true`, `originator=opencode`, `id_token_add_organizations=true`
- Token exchange timeout: 15 seconds

**Codex token exchange:**
```http
POST https://auth.openai.com/oauth/token
Content-Type: application/x-www-form-urlencoded

grant_type=authorization_code&
client_id=<openai-oauth-client-id>&
code={code}&
code_verifier={verifier}&
redirect_uri=http://localhost:1455/auth/callback
```

**Codex API headers (SSE transport):**
```http
POST https://api.openai.com/v1/responses
Authorization: Bearer {access_token}
chatgpt-account-id: {account_id}
OpenAI-Beta: responses=experimental
originator: pi
User-Agent: pi/{version} ({platform} {release}; {arch})
Content-Type: application/json
Accept: text/event-stream
```

**Codex API headers (WebSocket transport):**
```
OpenAI-Beta: responses_websockets=2026-02-06
```
(replaces the SSE beta header; other headers remain the same)

**Codex session headers** (optional, for prompt caching):
```http
session_id: {prompt_cache_key}
conversation_id: {prompt_cache_key}
```

**Codex URL paths:**
- SSE: `POST {baseUrl}/responses`
- Codex-specific: `POST {baseUrl}/codex/responses`

**Standard OpenAI API headers:**
```http
POST https://api.openai.com/v1/responses
Authorization: Bearer {api_key}
Content-Type: application/json
```

**SDKs used:** `openai` npm package
- Standard: `client.responses.create(params)` (Responses API)
- Legacy: `client.chat.completions.create(params)` (Completions API)
- Codex: Custom SSE or WebSocket streaming with Codex-specific headers

**JWT profile extraction (Codex):**
The access token is a JWT. omp decodes it to extract:
- `https://api.openai.com/auth` → `chatgpt_account_id`
- `https://api.openai.com/profile` → `email`

The `accountId` is required for Codex API calls (sent as the `chatgpt-account-id` header).

---

### Google (Gemini CLI / Vertex / Antigravity)

> **Port from:**
> - Auth: `utils/oauth/google-gemini-cli.ts` — `GeminiCliOAuthFlow` (line 229), `loginGeminiCli()` (line 297), `refreshGoogleCloudToken()` (line 305), `discoverProject()` (line 95), `pollOperation()` (line 65), `getUserEmail()` (line 211)
> - Streaming (Gemini CLI): `providers/google-gemini-cli.ts` — `streamGoogleGeminiCli` (line 455), `getGeminiCliUserAgent()` (line 71), `GEMINI_CLI_HEADERS()` (line 90), `ANTIGRAVITY_USER_AGENT` (line 78), `ANTIGRAVITY_AUTH_HEADERS` (line 100), `ANTIGRAVITY_STREAMING_HEADERS` (line 106)
> - Streaming (standard): `providers/google.ts` — `streamGoogle` (line 52)

Google has three separate authentication paths:

#### Gemini CLI (Google Cloud Code Assist)

**Auth pattern:** OAuth 2.0 + PKCE with client secret (Pattern B)

**OAuth details:**
- Client ID: `<google-oauth-client-id>`
- Client secret: `<google-oauth-client-secret>`
- Scopes: `cloud-platform`, `userinfo.email`, `userinfo.profile`
- Authorize URL: `https://accounts.google.com/o/oauth2/v2/auth`
- Token URL: `https://oauth2.googleapis.com/token`
- Callback: `localhost:8085/oauth2callback`
- Extra params: `access_type=offline`, `prompt=consent`

**Token exchange request** (note: uses `application/x-www-form-urlencoded`, not JSON):
```http
POST https://oauth2.googleapis.com/token
Content-Type: application/x-www-form-urlencoded

client_id=<google-oauth-client-id>&
client_secret=<google-oauth-client-secret>&
code={authorization_code}&
grant_type=authorization_code&
redirect_uri=http://localhost:8085/oauth2callback
```

**Token refresh request:**
```http
POST https://oauth2.googleapis.com/token
Content-Type: application/x-www-form-urlencoded

client_id=<google-oauth-client-id>&
client_secret=<google-oauth-client-secret>&
refresh_token={refresh_token}&
grant_type=refresh_token
```

**Post-auth project discovery:**
After obtaining tokens, the flow discovers or provisions a Google Cloud project:

1. **Check existing project:**
```http
POST https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist
Authorization: Bearer {access_token}
Content-Type: application/json
User-Agent: GeminiCLI/0.35.3/{model} ({platform}; {arch}; terminal)
Client-Metadata: ideType=IDE_UNSPECIFIED,platform=PLATFORM_UNSPECIFIED,pluginType=GEMINI

{
  "cloudaicompanionProject": "{GOOGLE_CLOUD_PROJECT if set}",
  "metadata": {
    "ideType": "IDE_UNSPECIFIED",
    "platform": "PLATFORM_UNSPECIFIED",
    "pluginType": "GEMINI",
    "duetProject": "{GOOGLE_CLOUD_PROJECT if set}"
  }
}
```
If response has `currentTier` and `cloudaicompanionProject` → use that project ID.

2. **Provision new project** (if no existing project):
```http
POST https://cloudcode-pa.googleapis.com/v1internal:onboardUser
```
Body includes `tierId` (one of `free-tier`, `legacy-tier`, `standard-tier`) and same metadata.
Response is a long-running operation.

3. **Poll operation** (if provisioning):
```http
GET https://cloudcode-pa.googleapis.com/v1internal/{operation_name}
```
Poll every 5 seconds until `done: true`. Extract `response.cloudaicompanionProject.id`.

4. **Get user email** (optional):
```http
GET https://www.googleapis.com/oauth2/v1/userinfo?alt=json
Authorization: Bearer {access_token}
```

5. Store `projectId` and `email` alongside OAuth credentials.

**API call headers:**
```http
POST https://cloudcode-pa.googleapis.com/v1beta/models/{model}:streamGenerateContent
Authorization: Bearer {access_token}
User-Agent: GeminiCLI/0.35.3/{model} ({platform}; {arch}; terminal)
Client-Metadata: ideType=IDE_UNSPECIFIED,platform=PLATFORM_UNSPECIFIED,pluginType=GEMINI
Content-Type: application/json
```

#### Vertex AI

**Auth pattern:** Google Application Default Credentials (ADC) — no interactive OAuth

Reads credentials from:
1. `GOOGLE_CLOUD_API_KEY` env var
2. `GOOGLE_APPLICATION_CREDENTIALS` file path
3. `~/.config/gcloud/application_default_credentials.json`

Requires `GOOGLE_CLOUD_PROJECT` and `GOOGLE_CLOUD_LOCATION`.

#### Antigravity

**Auth pattern:** Same OAuth flow as Gemini CLI (same client ID, client secret, scopes, callback port)

- Primary API endpoint: `https://daily-cloudcode-pa.googleapis.com`
- Fallback endpoint: `https://daily-cloudcode-pa.sandbox.googleapis.com`
- User-Agent: `antigravity/1.104.0 {os}/{arch}`
- Same project discovery/provisioning flow as Gemini CLI
- Auth headers use `antigravity/` User-Agent (not GeminiCLI)
- Streaming headers also use `antigravity/` User-Agent only (no `Client-Metadata`)

---

### GitHub Copilot

> **Port from:**
> - Auth: `utils/oauth/github-copilot.ts` — `loginGitHubCopilot()` (line 306), `startDeviceFlow()` (line 97), `pollForGitHubAccessToken()` (line 149), `refreshGitHubCopilotToken()` (line 220), `getBaseUrlFromToken()` (line 68), `enableAllGitHubCopilotModels()` (line 284), `enableGitHubCopilotModel()` (line 258), `getUrls()` (line 51), `normalizeDomain()` (line 40)
> - Headers: `providers/github-copilot-headers.ts` — `buildCopilotDynamicHeaders()` (line 110), `inferCopilotInitiator()` (line 22), `resolveGitHubCopilotBaseUrl()` (line 14)
> - Static headers: `utils/oauth/github-copilot.ts` — `COPILOT_HEADERS` (line 11)

**Auth pattern:** Device Code Flow (Pattern C)

**OAuth details:**
- Client ID: `<github-copilot-oauth-client-id>`
- Flow: GitHub Device Authorization (RFC 8628)
- Scope: `read:user`
- Device code URL: `https://github.com/login/device/code`
- Access token URL: `https://github.com/login/oauth/access_token`
- Copilot token URL: `https://api.github.com/copilot_internal/v2/token`

**Device code request:**
```http
POST https://github.com/login/device/code
Accept: application/json
Content-Type: application/x-www-form-urlencoded
User-Agent: GitHubCopilotChat/0.35.0

client_id=<github-copilot-oauth-client-id>&scope=read:user
```

**Response:**
```json
{
  "device_code": "...",
  "user_code": "ABCD-1234",
  "verification_uri": "https://github.com/login/device",
  "interval": 5,
  "expires_in": 900
}
```

**Poll request:**
```http
POST https://github.com/login/oauth/access_token
Accept: application/json
Content-Type: application/x-www-form-urlencoded
User-Agent: GitHubCopilotChat/0.35.0

client_id=<github-copilot-oauth-client-id>&device_code={device_code}&grant_type=urn:ietf:params:oauth:grant-type:device_code
```

**Two-token system:**
1. **GitHub access token** — long-lived, stored as `refresh` token
2. **Copilot session token** — short-lived, stored as `access` token

The Copilot session token is obtained by presenting the GitHub access token to
`/copilot_internal/v2/token`. The session token contains a `proxy-ep` field that
determines the API base URL (e.g., `api.individual.githubcopilot.com`).

**Token refresh:**
```http
GET https://api.github.com/copilot_internal/v2/token
Accept: application/json
Authorization: Bearer {github_access_token}
User-Agent: GitHubCopilotChat/0.35.0
Editor-Version: vscode/1.107.0
Editor-Plugin-Version: copilot-chat/0.35.0
Copilot-Integration-Id: vscode-chat
```

**Response:**
```json
{
  "token": "tid=...;exp=...;proxy-ep=proxy.individual.githubcopilot.com;...",
  "expires_at": 1234567890
}
```

**API base URL extraction:**
The Copilot token's `proxy-ep` field (e.g., `proxy.individual.githubcopilot.com`) is
transformed to an API URL by replacing `proxy.` with `api.`:
→ `https://api.individual.githubcopilot.com`

**API call headers (static + dynamic):**
```http
POST https://api.individual.githubcopilot.com/chat/completions
Authorization: Bearer {copilot_session_token}
User-Agent: GitHubCopilotChat/0.35.0
Editor-Version: vscode/1.107.0
Editor-Plugin-Version: copilot-chat/0.35.0
Copilot-Integration-Id: vscode-chat
X-Initiator: user
Openai-Intent: conversation-edits
Content-Type: application/json
```

**Dynamic per-request headers:**
- `X-Initiator` — `user` if the last message is from the user, `agent` if it's a tool result
- `Copilot-Vision-Request: true` — added when conversation contains images

**Post-login model enablement:**
After login, the tool enables all known models by POSTing to each
`{baseUrl}/models/{modelId}/policy` (in parallel) with extra headers:
```http
POST {baseUrl}/models/{modelId}/policy
Authorization: Bearer {copilot_session_token}
User-Agent: GitHubCopilotChat/0.35.0
Editor-Version: vscode/1.107.0
Editor-Plugin-Version: copilot-chat/0.35.0
Copilot-Integration-Id: vscode-chat
openai-intent: chat-policy
x-interaction-type: chat-policy
Content-Type: application/json

{"state": "enabled"}
```

**Enterprise support:**
For GitHub Enterprise, the domain is customizable. URLs become:
- Device code: `https://{domain}/login/device/code`
- Copilot token: `https://api.{domain}/copilot_internal/v2/token`
- API: `https://copilot-api.{domain}`

---

### Cursor

> **Port from:** `utils/oauth/cursor.ts` — `loginCursor()` (line 78), `generateCursorAuthParams()` (line 20), `pollCursorAuth()` (line 36), `refreshCursorToken()` (line 98), `getTokenExpiry()` (line 127), `isCursorTokenExpiringSoon()` (line 147)

**Auth pattern:** Deep Link + Polling (Pattern D)

**Details:**
- Login URL: `https://cursor.com/loginDeepControl`
- Poll URL: `https://api2.cursor.sh/auth/poll`
- Refresh URL: `https://api2.cursor.sh/auth/exchange_user_api_key`
- Uses PKCE challenge + UUID for session binding

**Login flow:**
```
1. Generate PKCE (verifier + challenge) and UUID
2. Open browser: https://cursor.com/loginDeepControl?challenge={}&uuid={}&mode=login&redirectTarget=cli
3. Poll: GET https://api2.cursor.sh/auth/poll?uuid={uuid}&verifier={verifier}
   - 404 → not ready, back off and retry
   - 200 → { accessToken, refreshToken }
4. Extract expiry from JWT exp claim
5. Store credentials
```

**Polling parameters:**
- Max attempts: 150
- Base delay: 1000ms
- Max delay: 10000ms
- Backoff multiplier: 1.2x
- Consecutive error threshold: 3

**Token refresh:**
```http
POST https://api2.cursor.sh/auth/exchange_user_api_key
Authorization: Bearer {refresh_token}
Content-Type: application/json

{}
```

**API calls:**
Cursor uses a custom gRPC-based `cursor-agent` API protocol, not standard
OpenAI/Anthropic REST APIs.

---

### Ollama

> **Port from:**
> - Auth: `utils/oauth/ollama.ts` — `loginOllama()` (line 22)
> - Model discovery: `provider-models/openai-compat.ts` — `ollamaModelManagerOptions()` (line 605), `normalizeOllamaBaseUrl()` (line 215), `fetchOllamaNativeModels()` (line 228)

> **Cross-reference:** Ollama exposes **three compatibility layers** (Anthropic, OpenAI,
> native) on the same port. This means it can serve as a model backend for any of the
> CLI harnesses in `llmcli` — see [`llm-cli.md` § Routing Through Ollama](./llm-cli.md#routing-through-ollama-open-source-models)
> for the pattern. From `llmauth`'s perspective, Ollama looks like an OpenAI-compatible
> provider with a custom base URL.

**Auth pattern:** No auth / optional API key (Pattern E)

**Details:**
- Default base URL: `http://127.0.0.1:11434/v1`
- API key: optional (for hosted deployments only)
- Ollama Cloud: `https://ollama.com/v1` with `OLLAMA_API_KEY` env var
- No OAuth, no browser flow, no tokens
- Anthropic-compatible endpoint also available at `http://127.0.0.1:11434` (no `/v1`) — used by Claude Code via `ANTHROPIC_BASE_URL`

**Login flow:**
```
1. Optionally prompt for API key/token
2. Empty → local no-auth mode
3. Non-empty → stored as API key credential
```

**Model discovery:**
The tool discovers available models in two ways:
1. **OpenAI-compatible:** GET `{baseUrl}/models` (standard listing)
2. **Ollama-native fallback:** GET `{baseUrl}/api/tags` (Ollama's own format)

**API calls:**
Uses the OpenAI Chat Completions format over Ollama's OpenAI-compatible endpoint:
```http
POST http://127.0.0.1:11434/v1/chat/completions
Content-Type: application/json
Accept: application/json

{"model": "llama3", "messages": [...], "stream": true}
```

No special identity headers. Cost tracking is hardcoded to zero.

---

## API Call Patterns

> **Port from:** The stream dispatch is at `stream.ts:163` (`stream()` function).
> It switches on `model.api` (line 199) to route to provider-specific streaming functions.

### Anthropic Messages API

> **Port from:** `providers/anthropic.ts:639` (`streamAnthropic`), `providers/anthropic.ts:1387` (`buildParams`)

```
API type: "anthropic-messages"
SDK: @anthropic-ai/sdk
Method: client.messages.create({ ...params, stream: true })
Endpoint: POST {baseUrl}/v1/messages
Transport: SSE (Server-Sent Events)
```

**Request body shape (OAuth mode):**
```json
{
  "model": "claude-sonnet-4-20250514",
  "max_tokens": 16384,
  "messages": [{"role": "user", "content": "..."}],
  "system": [
    {"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.63.abc; cc_entrypoint=cli; cch=def12;"},
    {"type": "text", "text": "You are a Claude agent, built on Anthropic's Claude Agent SDK."},
    {"type": "text", "text": "{actual_system_prompt}", "cache_control": {"type": "ephemeral"}}
  ],
  "tools": [{"name": "proxy_my_tool", "...": "..."}],
  "metadata": {"user_id": "user_{64hex}_account_{uuid}_session_{uuid}"},
  "stream": true,
  "temperature": 1.0
}
```

**Key body differences in OAuth mode:**
- System prompt array has billing header + Claude Code instruction prepended as separate text blocks
- Tool names are prefixed with `proxy_` (except built-in tools)
- `metadata.user_id` contains a synthetic cloaking ID
- `cache_control: {"type": "ephemeral"}` applied to system and trailing message blocks (max 4 breakpoints)
- Cache TTL can be `"1h"` for long retention on `api.anthropic.com`

**Thinking/reasoning params** (when model supports it):
```json
{
  "thinking": {"type": "adaptive"},
  "output_config": {"effort": "high"}
}
```
Effort levels: `"low"`, `"medium"`, `"high"`, `"max"`. Older models use `{"type": "enabled", "budget_tokens": N}` instead.

### OpenAI Responses API

> **Port from:** `providers/openai-responses.ts:137` (`streamOpenAIResponses`)

```
API type: "openai-responses"
SDK: openai
Method: client.responses.create(params)
Endpoint: POST {baseUrl}/v1/responses
Transport: SSE
```

### OpenAI Completions API

> **Port from:** `providers/openai-completions.ts:169` (`streamOpenAICompletions`)

```
API type: "openai-completions"
SDK: openai
Method: client.chat.completions.create(params)
Endpoint: POST {baseUrl}/v1/chat/completions
Transport: SSE
```

### OpenAI Codex Responses API

> **Port from:** `providers/openai-codex-responses.ts:1286` (`streamOpenAICodexResponses`), `providers/openai-codex/constants.ts` (header constants)

```
API type: "openai-codex-responses"
SDK: Custom (raw fetch + SSE parsing, or WebSocket)
Endpoint: POST https://api.openai.com/v1/responses
Transport: SSE or WebSocket (negotiated)
```

Codex supports WebSocket transport for lower latency. The WebSocket connection
is kept alive across turns and reused.

### Google Generative AI

> **Port from:** `providers/google.ts:52` (`streamGoogle`), `providers/google-gemini-cli.ts:455` (`streamGoogleGeminiCli`)

```
API type: "google-generative-ai"
SDK: Custom (raw fetch)
Endpoint: POST {baseUrl}/v1beta/models/{model}:streamGenerateContent
Transport: SSE
```

### OpenAI-Compatible (Ollama, LM Studio, vLLM, etc.)

> **Port from:** `provider-models/openai-compat.ts:605` (`ollamaModelManagerOptions`) — uses the standard OpenAI SDK pointed at a custom base URL

```
API type: "openai-responses" or "openai-completions"
SDK: openai
Method: Responses API SDK method for "openai-responses"; Chat Completions SDK method for "openai-completions"
Endpoint: POST {customBaseUrl}/v1/responses for "openai-responses"
Endpoint: POST {customBaseUrl}/v1/chat/completions for "openai-completions"
Transport: SDK-managed HTTP streaming/SSE for the selected API type
```

These providers use the standard OpenAI SDK pointed at a custom `baseURL`.
Ollama-compatible implementations must map to the endpoint for the API type they
actually implement: `/v1/chat/completions` for `openai-completions`, or
`/v1/responses` only when the provider explicitly supports Responses semantics.

---

## Token Lifecycle

> **Port from:** `utils/oauth/index.ts:345` (`refreshOAuthToken()` — central refresh dispatcher), `utils/oauth/index.ts:444` (`getOAuthApiKey()` — auto-refresh before API call)

### Expiry Buffer

All providers apply a 5-minute safety buffer. Tokens are considered expired
5 minutes before their actual expiry:

```typescript
expires: Date.now() + tokenData.expires_in * 1000 - 5 * 60 * 1000
```

### Auto-Refresh Flow

```
1. Before each API call, check: Date.now() >= credential.expires?
2. If expired:
   a. Call provider-specific refresh function
   b. Update stored credentials in SQLite
   c. Return new access token
3. If not expired:
   a. Return current access token
```

### Provider Refresh Mechanisms

| Provider | Refresh Mechanism | Endpoint |
|---|---|---|
| Anthropic | `refresh_token` grant | `POST /v1/oauth/token` |
| OpenAI Codex | `refresh_token` grant | `POST /oauth/token` |
| Google | `refresh_token` grant (requires client_id + client_secret) | `POST https://oauth2.googleapis.com/token` |
| GitHub Copilot | Present GitHub token to get new Copilot token | `GET /copilot_internal/v2/token` |
| Cursor | Exchange refresh token for new pair | `POST /auth/exchange_user_api_key` |
| Ollama | N/A (API keys don't expire) | — |

### Non-Expiring Credentials

These providers use static API keys that never expire. The refresh function
returns credentials as-is:
- Ollama, LM Studio, vLLM
- Perplexity, Together, Cerebras
- HuggingFace, NVIDIA, NanoGPT

---

## Required Client Identity Headers

Each provider requires specific client identity headers to access subscription-tier
features and rate limits. These must be replicated exactly.

### Anthropic (Claude Code Identity)

> **Port from:** `providers/anthropic.ts:110` (`sharedHeaders`), `providers/anthropic.ts:221` (`claudeCodeHeaders`), `providers/anthropic.ts:119` (`buildAnthropicHeaders()`)

All headers required for OAuth-mode API calls:

| Header | Value |
|---|---|
| `User-Agent` | `claude-cli/2.1.63 (external, cli)` |
| `Anthropic-Beta` | `claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05` |
| `X-App` | `cli` |
| `Anthropic-Version` | `2023-06-01` |
| `Anthropic-Dangerous-Direct-Browser-Access` | `true` |
| `X-Stainless-Retry-Count` | `0` |
| `X-Stainless-Runtime-Version` | `v24.3.0` |
| `X-Stainless-Package-Version` | `0.74.0` |
| `X-Stainless-Runtime` | `node` |
| `X-Stainless-Lang` | `js` |
| `X-Stainless-Arch` | `arm64` / `x64` (map from `process.arch`) |
| `X-Stainless-Os` | `MacOS` / `Linux` / `Windows` (map from `process.platform`) |
| `X-Stainless-Timeout` | `600` |

**Billing header** (sent as part of request):
```
x-anthropic-billing-header: cc_version=2.1.63.{3-char-random-hex}; cc_entrypoint=cli; cch={5-char-sha256-of-payload};
```

**System instruction** (prepend to system prompt):
```
You are a Claude agent, built on Anthropic's Claude Agent SDK.
```

**Tool prefix:** Prefix all tool names with `proxy_`.

**Platform mapping functions:**
```typescript
function mapOs(platform: string): string {
  switch (platform) {
    case "darwin": return "MacOS";
    case "win32": return "Windows";
    case "linux": return "Linux";
    case "freebsd": return "FreeBSD";
    default: return `Other::${platform}`;
  }
}

function mapArch(arch: string): string {
  switch (arch) {
    case "x64": case "amd64": return "x64";
    case "arm64": case "aarch64": return "arm64";
    case "ia32": case "x86": case "386": return "x86";
    default: return `other::${arch}`;
  }
}
```

**Enforced header keys** (these must not be overridden by model config):
`User-Agent`, `X-App`, `Accept`, `Accept-Encoding`, `Connection`, `Content-Type`,
`Anthropic-Version`, `Anthropic-Dangerous-Direct-Browser-Access`, `Anthropic-Beta`,
`Authorization`, `X-Api-Key`, and all `X-Stainless-*` headers.

### GitHub Copilot (VS Code Copilot Chat Identity)

> **Port from:** `utils/oauth/github-copilot.ts:11` (`COPILOT_HEADERS`), `providers/github-copilot-headers.ts:110` (`buildCopilotDynamicHeaders()`)

| Header | Value |
|---|---|
| `User-Agent` | `GitHubCopilotChat/0.35.0` |
| `Editor-Version` | `vscode/1.107.0` |
| `Editor-Plugin-Version` | `copilot-chat/0.35.0` |
| `Copilot-Integration-Id` | `vscode-chat` |

These headers are required on all requests: device code flow, token refresh,
and API calls.

### Google Gemini CLI

> **Port from:** `providers/google-gemini-cli.ts:71` (`getGeminiCliUserAgent()`), `providers/google-gemini-cli.ts:90` (`GEMINI_CLI_HEADERS`)

**User-Agent format:**
```
GeminiCLI/{version}/{model} ({platform}; {arch}; terminal)
```

Default version: `0.35.3`. Configurable via `PI_AI_GEMINI_CLI_VERSION` env var.

Example: `GeminiCLI/0.35.3/gemini-3.1-pro-preview (darwin; arm64; terminal)`

**Additional header:**
```
Client-Metadata: ideType=IDE_UNSPECIFIED,platform=PLATFORM_UNSPECIFIED,pluginType=GEMINI
```

### Google Antigravity

> **Port from:** `providers/google-gemini-cli.ts:78` (`ANTIGRAVITY_USER_AGENT`), lines 100-107 (auth + streaming headers)

**User-Agent format:**
```
antigravity/{version} {os}/{arch}
```

Default version: `1.104.0`. Configurable via `PI_AI_ANTIGRAVITY_VERSION` env var.

Platform mapping: `process.platform` passes through except `win32` → `windows`.
Arch mapping: `x64` → `amd64`, `ia32` → `386`, others pass through.

Example: `antigravity/1.104.0 darwin/arm64`

### Kimi

| Header | Value |
|---|---|
| `User-Agent` | `KimiCLI/1.0` |

### OpenAI Codex

> **Port from:** `providers/openai-codex-responses.ts:1911` (`createCodexHeaders()`), `providers/openai-codex/constants.ts` (all constants)

Uses its own identity:

| Header | Value |
|---|---|
| `User-Agent` | `pi/{version} ({platform} {release}; {arch})` |
| `originator` | `pi` |
| `OpenAI-Beta` | `responses=experimental` (SSE) or `responses_websockets=2026-02-06` (WebSocket) |
| `chatgpt-account-id` | `{account_id_from_jwt}` |

The `chatgpt-account-id` is extracted from the JWT access token at claim path
`https://api.openai.com/auth` → `chatgpt_account_id`.

### Ollama

No special identity headers required. Standard HTTP headers only.

---

## Error Handling & Retry

### Streaming Retries (Anthropic)

The Anthropic provider has a retry loop for transient transport errors:
- Retries are only attempted **before any content has been streamed** to the caller
- Once text or tool-call events have been emitted, the stream is not retryable
- The SDK is configured with `maxRetries: 5`

### Stream Timeouts

Two timeout mechanisms protect against hung streams:

1. **First-event timeout** — If no SSE event arrives within a threshold after the
   request is sent, abort the stream. This catches connection-level failures.
2. **Idle timeout** — If the stream stalls between events for too long, abort.
   This catches mid-stream hangs.

Both timeouts abort the underlying request via `AbortController`.

### Token Refresh Failures

If an OAuth token refresh fails:
- The error is propagated to the caller as `"Failed to refresh OAuth token for {provider}: {reason}"`
- **Perplexity special case**: If refresh fails but the JWT `exp` claim hasn't passed yet,
  the existing token is used as a fallback

### GitHub Copilot Clock Drift

The device code polling tracks `slow_down` responses. If polling times out after
receiving slow_down responses, the error message explicitly mentions clock drift
(common in WSL/VM environments) and suggests restarting the VM clock.

---

## Suggested Go Package Structure

`llmauth` is a Go package inside the **`fabrikk` monorepo** at
`github.com/php-workx/fabrikk`. Other tools (`plan`, `verk`, `vakt`) import it
as `github.com/php-workx/fabrikk/llmauth`. See the
[Monorepo Layout](#monorepo-layout) section for the full repo structure.

Maps the omp TypeScript module structure to a Go package layout:

```
fabrikk/llmauth/
├── auth.go                    # AuthStorage equivalent (auth-storage.ts:272)
│                              # Credential types, SQLite storage, auto-refresh
├── auth_store.go              # AuthCredentialStore equivalent (auth-storage.ts:2136)
│                              # Low-level SQLite CRUD, schema init
├── pkce.go                    # generatePKCE() (utils/oauth/pkce.ts:5)
├── callback.go                # OAuthCallbackFlow equivalent (utils/oauth/callback-server.ts:32)
│                              # Local HTTP server, state verification
├── types.go                   # OAuthCredentials, OAuthProvider, Model, Context
│                              # (utils/oauth/types.ts + types.ts)
├── stream.go                  # stream() dispatch, AssistantMessageEventStream
│                              # (stream.ts:163, utils/event-stream.ts:95)
├── env.go                     # getEnvApiKey(), serviceProviderMap
│                              # (stream.ts:63-155)
│
├── provider/
│   ├── anthropic.go           # streamAnthropic, buildAnthropicHeaders, buildParams,
│   │                          # billing header, cloaking ID, tool prefix
│   │                          # (providers/anthropic.ts — ~1500 lines, the largest file)
│   ├── openai_responses.go    # streamOpenAIResponses (providers/openai-responses.ts:137)
│   ├── openai_completions.go  # streamOpenAICompletions (providers/openai-completions.ts:169)
│   ├── openai_codex.go        # streamOpenAICodexResponses, createCodexHeaders, constants
│   │                          # (providers/openai-codex-responses.ts + providers/openai-codex/constants.ts)
│   ├── google.go              # streamGoogle (providers/google.ts:52)
│   ├── google_gemini_cli.go   # streamGoogleGeminiCli, Gemini CLI + Antigravity identity
│   │                          # (providers/google-gemini-cli.ts:455)
│   ├── copilot.go             # GitHub Copilot headers + dynamic header builder
│   │                          # (providers/github-copilot-headers.ts)
│   └── cursor.go              # Cursor gRPC protocol (providers/cursor.ts)
│
├── oauth/
│   ├── anthropic.go           # AnthropicOAuthFlow (utils/oauth/anthropic.ts)
│   ├── openai_codex.go        # OpenAICodexOAuthFlow, JWT decode (utils/oauth/openai-codex.ts)
│   ├── google_gemini.go       # GeminiCliOAuthFlow, project discovery (utils/oauth/google-gemini-cli.ts)
│   ├── github_copilot.go      # Device code flow, two-token system (utils/oauth/github-copilot.ts)
│   ├── cursor.go              # Deep link + polling (utils/oauth/cursor.ts)
│   ├── ollama.go              # Optional API key (utils/oauth/ollama.ts)
│   └── refresh.go             # refreshOAuthToken() dispatcher, getOAuthApiKey()
│                              # (utils/oauth/index.ts:345, 444)
│
└── discovery/
    └── ollama.go              # Model discovery for OpenAI-compatible providers
                               # (provider-models/openai-compat.ts:605)
```

**Key porting notes for Go:**

- **SQLite:** Use `modernc.org/sqlite` (pure Go) or `github.com/mattn/go-sqlite3` (CGo)
- **HTTP client:** Use `net/http` with custom `Transport` for header injection — no SDK needed for most providers. Only Anthropic and OpenAI have official Go SDKs (`github.com/anthropics/anthropic-sdk-go`, `github.com/openai/openai-go`)
- **SSE parsing:** Use `bufio.Scanner` on the response body, split on `\n\n`, parse `data:` lines
- **PKCE:** `crypto/rand` for verifier, `crypto/sha256` for challenge, `encoding/base64` (RawURLEncoding) for encoding
- **Local callback server:** `net/http.ListenAndServe` on localhost, read `code` and `state` from query params
- **JWT decode:** Split on `.`, base64url-decode middle segment, `json.Unmarshal` — no library needed
- **WebSocket (Codex):** Use `nhooyr.io/websocket` or `github.com/gorilla/websocket`
- **Concurrent model enablement (Copilot):** Use `errgroup.Group` for parallel POST requests

---

## Monorepo Layout

`llmauth` lives in the `fabrikk` monorepo alongside `llmclient` and `llmcli` (and
any future shared packages). One `go.mod` at the repo root, packages as
subdirectories.

```
github.com/php-workx/fabrikk/
├── go.mod                    # module github.com/php-workx/fabrikk
├── go.sum
├── go.work                   # optional, for IDE / cross-repo dev
├── README.md
├── specs/
│   ├── llm-auth.md           # this spec
│   ├── llm-client.md
│   └── llm-cli.md
│
├── llmauth/                  # this package
│   └── ...
├── llmclient/                # request assembly + HTTP backend
│   └── ...
├── llmcli/                   # CLI shell-out backend
│   └── ...
│
└── (future shared packages)
```

### Imports from external repos

`plan`, `verk`, and `vakt` are developed in separate repos and consume `fabrikk`
packages as Go module dependencies:

```go
// in github.com/php-workx/plan/main.go
import (
    "github.com/php-workx/fabrikk/llmauth"
    "github.com/php-workx/fabrikk/llmclient"
)

func main() {
    dbPath, _ := llmauth.DefaultDBPath()
    store, _ := llmauth.OpenStore(dbPath)
    backend := llmclient.New(store)
    stream, _ := backend.Stream(ctx, &llmclient.Context{...})
}
```

**Adding fabrikk as a dependency** (in the consuming repo, e.g. `plan`):

```bash
GOPRIVATE=github.com/php-workx/* go get github.com/php-workx/fabrikk/llmauth@latest
```

`GOPRIVATE` is required because `fabrikk` is a private repo — it tells Go to skip
the public module proxy and fetch directly via git using your SSH/HTTPS credentials.
Add `GOPRIVATE=github.com/php-workx/*` to your shell rc file once and forget about it.

### Local development across repos

When iterating on `fabrikk` and a consumer repo (e.g. `plan`) at the same time, use
`go.work` to point both at local checkouts without committing `replace` directives:

```
~/workspaces/
├── fabrikk/
├── plan/
└── go.work
```

```
// ~/workspaces/go.work
go 1.22

use ./fabrikk
use ./plan
```

With this in place, edits to `fabrikk/llmauth/*.go` are immediately visible to
`plan` without `go get`. Production builds ignore `go.work` (or use
`GOWORK=off go build`) and resolve dependencies from `go.sum`.

### Internalizing a tool into fabrikk later

If `plan`, `verk`, or `vakt` ever needs to live inside the monorepo (e.g. to share
internal types with `llmauth` that you don't want to export), the migration is
straightforward:

```
fabrikk/
├── llmauth/
├── llmclient/
├── llmcli/
└── cmd/
    ├── plan/                 # was github.com/php-workx/plan
    │   └── main.go
    ├── verk/
    │   └── main.go
    └── vakt/
        └── main.go
```

Each `cmd/<tool>/main.go` builds a separate binary with `go build ./cmd/plan`.
This is the same pattern Go's standard library uses (`cmd/go`, `cmd/gofmt`, etc.).

The decision of whether to keep tools as separate repos or internalize them is a
**versioning question**: separate repos let each tool release independently,
internalized tools all move in lockstep with the shared packages. Start separate;
internalize later if the cross-repo coordination overhead becomes painful.

---

## Implementation Checklist

### OAuth Flows to Implement

- [ ] **PKCE generator** — 96-byte random verifier, SHA-256 challenge, base64url encoding
- [ ] **Local callback server** — preferred port with random fallback, CSRF state verification, HTML success page, 300s timeout
- [ ] **Anthropic OAuth** — PKCE flow, port 54545, JSON Content-Type for token exchange
- [ ] **OpenAI Codex OAuth** — PKCE flow, port 1455, form-urlencoded Content-Type, JWT profile extraction (accountId + email), 15s token exchange timeout
- [ ] **Google Gemini CLI OAuth** — PKCE flow with client secret, port 8085, form-urlencoded Content-Type, project discovery/provisioning with LRO polling
- [ ] **GitHub Copilot Device Code** — device flow with polling + backoff, form-urlencoded Content-Type, two-token system (GitHub token → Copilot session token), model enablement POST
- [ ] **Cursor Deep Link** — PKCE + UUID, polling `api2.cursor.sh`, JWT expiry extraction

### Credential Storage

- [ ] SQLite database with `provider`, `type` (api_key/oauth), `data` (JSON blob) columns
- [ ] 5-minute expiry buffer on all tokens (`expires_in * 1000 - 300_000`)
- [ ] Auto-refresh before each API call (check `Date.now() >= credential.expires`)
- [ ] Round-robin support for multiple credentials per provider
- [ ] Environment variable fallback (multi-source: `os.Getenv`, `cwd/.env`, `~/.env`)
- [ ] Credential disabled/enabled state tracking
- [ ] **Default DB path** at `~/.fabrikk/auth.db` (with `XDG_DATA_HOME` and `FABRIKK_AUTH_DB` overrides)
- [ ] **Strict permissions** — directory `0700`, files `0600`, enforced via `os.MkdirAll` + `os.Chmod`
- [ ] **Permission verification on Open()** — refuse to use a database with mode `0077` bits set
- [ ] **WAL mode** with `journal_mode=WAL` pragma (concurrent readers + one writer)
- [ ] **WAL/SHM file permissions** — set umask `0o077` so SQLite-created sidecar files inherit `0600`

### Environment & Configuration

- [ ] `.env` parser with quote-stripping, `export ` prefix removal, blank/comment line handling, and no shell evaluation
- [ ] Multi-source environment resolution order: explicit process env, `cwd/.env`, then `~/.env`
- [ ] `getEnvApiKey()` implemented for every provider referenced in `serviceProviderMap`, including provider aliases
- [ ] Missing and malformed env entries return typed errors that identify the provider and source path without logging secret values

### API Key Validation

- [ ] Anthropic API key validation probe: `count_tokens` endpoint, successful auth without creating a real message
- [ ] OpenAI API key validation probe: `GET /v1/models`, with 401/403 mapped to credential failure and network errors kept retryable
- [ ] Google API key validation probe: `GET /v1beta/models`, with project/quota errors surfaced distinctly from invalid key errors
- [ ] Ollama validation probe: `GET /api/tags`, with no-auth local failures reported as connectivity/configuration issues

### Client Identity Headers

- [ ] Platform mapping helpers: `mapOs` and `mapArch`, with explicit handling for unknown platforms
- [ ] Version handling for CLI identity headers, including default/fallback version behavior
- [ ] Anthropic header set: Stainless runtime/platform/arch/language headers plus billing identity headers
- [ ] GitHub Copilot header set: VS Code editor/plugin/integration headers and initiator/vision dynamic headers
- [ ] Google header variants: Gemini CLI and Antigravity User-Agent/metadata differences
- [ ] OpenAI Codex header set: account/session headers and `originator: pi`

### Anthropic Provider Module

- [ ] OAuth token detection (`sk-ant-oat` substring check)
- [ ] Three-branch header selection (OAuth / proxy / API key)
- [ ] Full Claude Code identity headers (Stainless, billing, User-Agent)
- [ ] Cloaking user ID generation (`user_{64hex}_account_{uuid}_session_{uuid}`)
- [ ] Tool name prefixing (`proxy_` prefix, excluding built-in tools)
- [ ] Tool name stripping on response parsing
- [ ] System prompt injection (billing header block + Claude Code instruction)
- [ ] Prompt caching with `cache_control: {"type": "ephemeral"}` (max 4 breakpoints)
- [ ] Thinking/reasoning params (adaptive vs budget modes)
- [ ] Pre-content retry loop (retry before any events streamed, fail after)

### OpenAI Provider Modules

- [ ] **Responses API** — `openai` SDK, `client.responses.create(params)`, SSE
- [ ] **Completions API** — `openai` SDK, `client.chat.completions.create(params)`, SSE
- [ ] **Codex** — custom SSE + WebSocket transport, `chatgpt-account-id` from JWT, `originator: pi`, session/conversation ID headers for prompt caching

### Google Provider Module

- [ ] Gemini CLI identity (User-Agent + Client-Metadata headers)
- [ ] Antigravity identity (different User-Agent, different endpoints, no Client-Metadata)
- [ ] Post-auth project discovery (`loadCodeAssist` → `onboardUser` → poll LRO)
- [ ] Vertex AI ADC support (no interactive OAuth)

### GitHub Copilot Provider Module

- [ ] Static headers (User-Agent, Editor-Version, Editor-Plugin-Version, Copilot-Integration-Id)
- [ ] Dynamic headers (X-Initiator inferred from last message role, Copilot-Vision-Request)
- [ ] Base URL extraction from Copilot token `proxy-ep` field
- [ ] Enterprise domain support (all URLs parameterized by domain)
- [ ] Post-login model enablement (parallel POST to each model's policy endpoint)

### Cursor Provider Module

- [ ] Deep link + polling auth flow
- [ ] gRPC-based `cursor-agent` protocol (not REST)
- [ ] JWT expiry extraction for token management

### Ollama / OpenAI-Compatible Module

- [ ] Optional API key, default to no-auth
- [ ] Model discovery via `/models` endpoint, fallback to `/api/tags`
- [ ] Base URL normalization (append `/v1` if missing)
- [ ] Zero-cost tracking

### Unified Stream Abstraction

- [ ] Common `AssistantMessageEventStream` with events: `start`, `text`, `tool_call`, `thinking`, `done`, `error`
- [ ] Per-provider SSE parser that normalizes into common events
- [ ] First-event timeout (abort if no SSE event within threshold)
- [ ] Idle timeout (abort if stream stalls between events)
- [ ] Usage/cost tracking per response (input, output, cache read/write tokens + cost)
- [ ] Stop reason normalization (`stop`, `tool_use`, `max_tokens`, `error`, `aborted`)
