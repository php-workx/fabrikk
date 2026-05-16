package llmcli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// Observer is the hook interface for llmcli observability. Implementations
// may record Prometheus counters, OpenTelemetry spans, or structured logs
// without coupling llmcli to any specific monitoring library.
//
// All methods are called synchronously on the goroutine that drives the
// stream. Implementations must not block; use non-blocking channels or
// fire-and-forget goroutines for expensive work.
//
// The default implementation is [NoopObserver], which discards all
// observations so that llmcli has no metrics library dependency.
type Observer interface {
	// OnStreamStart is called immediately before a backend spawns a subprocess
	// or sends a streaming request. backend is the stable backend name (e.g.
	// "claude"); model is the model identifier from the request config, or
	// empty when the caller did not set one.
	OnStreamStart(backend, model string)

	// OnStreamEnd is called after the event channel is closed. success is true
	// when the terminal event was EventDone and false when it was EventError.
	// errType is the canonical error-type label produced by [LabelErrorType];
	// it is "none" on success.
	OnStreamEnd(backend, model string, success bool, errType string)

	// OnEventEmitted is called once for each event successfully delivered on
	// the stream channel.
	OnEventEmitted(backend, model string, eventType llmclient.EventType)

	// OnSpawnDuration records the elapsed time between the Stream call and the
	// first event appearing on the channel, capturing subprocess spawn latency
	// plus CLI startup overhead.
	OnSpawnDuration(backend, model string, dur time.Duration)

	// OnBackendAvailability is called whenever a backend's
	// [llmclient.Backend.Available] method is invoked. available is the result
	// of that call.
	OnBackendAvailability(backend string, available bool)
}

// NoopObserver is an [Observer] that discards all observations. It is the
// built-in default so that llmcli does not incur any metrics library
// dependency when the caller does not configure a custom observer.
type NoopObserver struct{}

// OnStreamStart is a no-op.
func (NoopObserver) OnStreamStart(_, _ string) {}

// OnStreamEnd is a no-op.
func (NoopObserver) OnStreamEnd(_, _ string, _ bool, _ string) {}

// OnEventEmitted is a no-op.
func (NoopObserver) OnEventEmitted(_, _ string, _ llmclient.EventType) {}

// OnSpawnDuration is a no-op.
func (NoopObserver) OnSpawnDuration(_, _ string, _ time.Duration) {}

// OnBackendAvailability is a no-op.
func (NoopObserver) OnBackendAvailability(_ string, _ bool) {}

// Compile-time assertion: NoopObserver must satisfy Observer.
var _ Observer = NoopObserver{}

// observerHolder wraps Observer so atomic.Value always stores the same type.
type observerHolder struct{ v Observer }

// _defaultObserver holds the package-level Observer atomically.
var _defaultObserver atomic.Value //nolint:gochecknoglobals // backing store for GetDefaultObserver/SetDefaultObserver

func init() {
	_defaultObserver.Store(observerHolder{NoopObserver{}})
}

// SetDefaultObserver atomically sets the package-level observer.
// Passing nil normalizes to [NoopObserver] so callers never receive a nil Observer.
func SetDefaultObserver(o Observer) {
	if o == nil {
		o = NoopObserver{}
	}
	_defaultObserver.Store(observerHolder{o})
}

// GetDefaultObserver atomically returns the package-level observer.
func GetDefaultObserver() Observer {
	return _defaultObserver.Load().(observerHolder).v //nolint:forcetypeassert // always an observerHolder
}

const (
	defaultModelLabel = "default"
	genericErrorType  = llmclient.ErrTypeInternal
	noErrorType       = "none"
)

func effectiveObservedModel(cfg llmclient.RequestConfig) string { //nolint:gocritic // RequestConfig value mirrors Stream option handling.
	if cfg.Ollama != nil && cfg.Ollama.Model != "" {
		return cfg.Ollama.Model
	}

	return cfg.Model
}

func observeAvailability(backend string, available bool) bool {
	observer := GetDefaultObserver()
	observer.OnBackendAvailability(backend, available)
	return available
}

func observeStreamStart(backend string, cfg llmclient.RequestConfig) (string, time.Time) { //nolint:gocritic // RequestConfig value mirrors Stream option handling.
	model := effectiveObservedModel(cfg)
	started := time.Now()
	GetDefaultObserver().OnStreamStart(backend, model)
	return model, started
}

func observeStream(backend, model string, started time.Time, in <-chan llmclient.Event) <-chan llmclient.Event {
	out := make(chan llmclient.Event, 16)
	observer := GetDefaultObserver()

	go func() {
		defer close(out)

		firstEvent := true
		success := false
		errType := genericErrorType
		terminalSeen := false

		for ev := range in {
			if firstEvent {
				observer.OnSpawnDuration(backend, model, time.Since(started))
				firstEvent = false
			}

			out <- ev
			observer.OnEventEmitted(backend, model, ev.Type)

			switch ev.Type {
			case llmclient.EventDone:
				success = true
				errType = LabelErrorType(nil)
				terminalSeen = true
			case llmclient.EventError:
				success = false
				errType = ev.ErrorType
				if errType == "" {
					errType = LabelErrorType(fmt.Errorf("%s", ev.ErrorMessage))
				}
				terminalSeen = true
			}
		}

		if !terminalSeen {
			success = false
			errType = genericErrorType
		}

		observer.OnStreamEnd(backend, model, success, errType)
	}()

	return out
}

// — Label helpers -------------------------------------------------------------

// LabelBackend returns the stable metric label value for the given backend
// name. The name is returned unchanged; the helper exists to make label
// construction uniform across metrics.
func LabelBackend(backend string) string { return backend }

// LabelModel returns the stable metric label value for the given model
// identifier. An empty model string is normalised to "default" so that all
// time-series carry a non-empty model label.
func LabelModel(model string) string {
	if model == "" {
		return defaultModelLabel
	}

	return model
}

// LabelSuccess returns "true" or "false" as stable metric label values for
// the given success flag.
func LabelSuccess(success bool) string {
	if success {
		return "true"
	}

	return "false"
}

// LabelEventType returns the string form of an [llmclient.EventType] for use
// as a metric label. The EventType constants are already stable string values
// (e.g. "text_delta", "done") so no additional normalisation is needed.
func LabelEventType(et llmclient.EventType) string { return string(et) }

// LabelErrorType returns a canonical metric label for the given error.
//
//   - nil  → "none"
//   - context.Canceled → "cancelled"
//   - context.DeadlineExceeded → "timeout"
//   - anything else → a stable llmclient.ErrType* value
//
// Sentinel check: errors.Is/errors.As are tried first for context errors.
// The llmclient.ErrType* constants are plain strings, not typed sentinel
// errors, so all other classification falls back to substring matching on
// the lowercased error message. This is a known limitation: messages that
// mention these words in unrelated context (e.g. "neural network", "rate
// this response") can be misclassified. More-specific patterns (multi-word
// phrases) are checked before shorter, broader ones to reduce false hits.
func LabelErrorType(err error) string {
	if err == nil {
		return noErrorType
	}

	// Prefer typed error checks before any string matching.
	switch {
	case errors.Is(err, context.Canceled):
		return llmclient.ErrTypeCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return llmclient.ErrTypeTimeout
	}

	// Substring fallback: check multi-word / specific phrases first so that
	// they win over shorter substrings that appear in the same message.
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not logged in") || strings.Contains(msg, "login") || strings.Contains(msg, "auth"):
		return llmclient.ErrTypeAuth
	case strings.Contains(msg, "rate_limit") || strings.Contains(msg, "rate limit") || strings.Contains(msg, "quota"):
		return llmclient.ErrTypeRateLimit
	case strings.Contains(msg, "bad request") || strings.Contains(msg, "invalid request") || strings.Contains(msg, "malformed"):
		return llmclient.ErrTypeBadRequest
	case strings.Contains(msg, "connection refused") || strings.Contains(msg, "no such host") || strings.Contains(msg, "network"):
		return llmclient.ErrTypeNetwork
	case strings.Contains(msg, "upstream") || strings.Contains(msg, "provider"):
		return llmclient.ErrTypeProvider
	default:
		return llmclient.ErrTypeInternal
	}
}
