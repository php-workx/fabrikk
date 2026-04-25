package llmcli

import (
	"context"
	"os"
	"sort"
	"strings"

	"github.com/php-workx/fabrikk/llmclient"
)

func contextWithRequestTimeout(ctx context.Context, cfg llmclient.RequestConfig) (context.Context, context.CancelFunc) { //nolint:gocritic // RequestConfig value mirrors ApplyOptions return semantics.
	if cfg.Timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, cfg.Timeout)
}

func resolveProcessEnv(cfg llmclient.RequestConfig, backendOverrides map[string]string) []string { //nolint:gocritic // RequestConfig value mirrors ApplyOptions return semantics.
	var base []string
	if cfg.EnvironmentSet {
		base = append([]string(nil), cfg.Environment...)
	} else {
		base = os.Environ()
	}

	env := applyEnvOverlay(base, cfg.EnvironmentOverlay)
	return applyEnvOverridesLast(env, backendOverrides)
}

func applyEnvOverlay(base []string, overlay map[string]string) []string {
	env := append([]string(nil), base...)
	if len(overlay) == 0 {
		return env
	}

	seen := make(map[string]struct{}, len(env))
	applied := make(map[string]struct{}, len(overlay))
	out := make([]string, 0, len(env)+len(overlay))
	for _, entry := range env {
		key := envKey(entry)
		if key == "" {
			out = append(out, entry)
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		if value, ok := overlay[key]; ok {
			out = append(out, key+"="+value)
			applied[key] = struct{}{}
			continue
		}
		out = append(out, entry)
	}

	var keys []string
	for key := range overlay {
		if _, ok := applied[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, key+"="+overlay[key])
	}
	return out
}

func applyEnvOverridesLast(base []string, overrides map[string]string) []string {
	env := append([]string(nil), base...)
	if len(overrides) == 0 {
		return env
	}

	overrideKeys := make(map[string]struct{}, len(overrides))
	for key := range overrides {
		overrideKeys[key] = struct{}{}
	}

	out := make([]string, 0, len(env)+len(overrides))
	for _, entry := range env {
		key := envKey(entry)
		if _, overridden := overrideKeys[key]; overridden {
			continue
		}
		out = append(out, entry)
	}

	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, key+"="+overrides[key])
	}
	return out
}

func envKey(entry string) string {
	idx := strings.IndexByte(entry, '=')
	if idx <= 0 {
		return ""
	}
	return entry[:idx]
}

func executionOptionResults(cfg llmclient.RequestConfig, caps llmclient.Capabilities) map[llmclient.OptionName]llmclient.OptionResult { //nolint:gocritic // RequestConfig value mirrors fidelity helper style.
	results := make(map[llmclient.OptionName]llmclient.OptionResult)
	for _, name := range providedExecutionOptions(cfg) {
		switch caps.OptionSupport[name] {
		case llmclient.OptionSupportFull:
			results[name] = llmclient.OptionApplied
		case llmclient.OptionSupportPartial:
			results[name] = llmclient.OptionDegraded
		default:
			results[name] = llmclient.OptionUnsupported
		}
	}
	return results
}

func mergeOptionResults(a, b map[llmclient.OptionName]llmclient.OptionResult) map[llmclient.OptionName]llmclient.OptionResult {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[llmclient.OptionName]llmclient.OptionResult, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func providedExecutionOptions(cfg llmclient.RequestConfig) []llmclient.OptionName { //nolint:gocritic // RequestConfig value mirrors fidelity helper style.
	var names []llmclient.OptionName
	if cfg.WorkingDirectory != "" {
		names = append(names, llmclient.OptionWorkingDirectory)
	}
	if cfg.EnvironmentSet {
		names = append(names, llmclient.OptionEnvironment)
	}
	if len(cfg.EnvironmentOverlay) > 0 {
		names = append(names, llmclient.OptionEnvironmentOverlay)
	}
	if cfg.Timeout > 0 {
		names = append(names, llmclient.OptionTimeout)
	}
	if cfg.ReasoningEffort != "" {
		names = append(names, llmclient.OptionReasoningEffort)
	}
	if cfg.CodexJSONL {
		names = append(names, llmclient.OptionCodexJSONL)
	}
	if cfg.RawCapture != nil {
		names = append(names, llmclient.OptionRawCapture)
	}
	return names
}
