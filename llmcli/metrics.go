package llmcli

import (
	"context"
	"errors"
	"fmt"
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

// DefaultObserver is the package-level [Observer] used when no custom
// observer is configured. It is a [NoopObserver] by default.
var DefaultObserver Observer = NoopObserver{} //nolint:gochecknoglobals // intentional package-level default; callers swap it for custom observers.

const (
	defaultModelLabel = "default"
	genericErrorType  = "error"
)

func effectiveObservedModel(cfg llmclient.RequestConfig) string { //nolint:gocritic // RequestConfig value mirrors Stream option handling.
	if cfg.Ollama != nil && cfg.Ollama.Model != "" {
		return cfg.Ollama.Model
	}

	return cfg.Model
}

func observeAvailability(backend string, available bool) bool {
	observer := DefaultObserver
	observer.OnBackendAvailability(backend, available)
	return available
}

func observeStreamStart(backend string, cfg llmclient.RequestConfig) (string, time.Time) { //nolint:gocritic // RequestConfig value mirrors Stream option handling.
	model := effectiveObservedModel(cfg)
	DefaultObserver.OnStreamStart(backend, model)
	return model, time.Now()
}

func observeStream(backend, model string, started time.Time, in <-chan llmclient.Event) <-chan llmclient.Event {
	out := make(chan llmclient.Event, 16)
	observer := DefaultObserver

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
				errType = LabelErrorType(fmt.Errorf("%s", ev.ErrorMessage))
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
//   - context.Canceled → "canceled"
//   - context.DeadlineExceeded → "deadline_exceeded"
//   - anything else → "error"
func LabelErrorType(err error) string {
	if err == nil {
		return "none"
	}

	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "error"
	}
}
