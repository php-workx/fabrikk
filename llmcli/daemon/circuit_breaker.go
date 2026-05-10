package daemon

import (
	"log/slog"
	"sync"
	"time"
)

// CircuitBreakerState represents the state of the circuit breaker.
type CircuitBreakerState int

const (
	// CircuitClosed is the normal state - all events are processed.
	CircuitClosed CircuitBreakerState = iota
	// CircuitOpen means the burst threshold was exceeded - events are sampled.
	CircuitOpen
)

// CircuitBreaker implements a burst mode circuit breaker for ingestion.
// When the ingestion rate exceeds the configured threshold, it engages
// and starts sampling events (processing every Nth event) rather than
// all of them. It auto-resets after the burst subsides.
type CircuitBreaker struct {
	lastTrip            time.Time
	logger              *slog.Logger
	eventTimes          []time.Time
	burstThreshold      int
	window              time.Duration
	quietPeriod         time.Duration
	state               CircuitBreakerState
	totalAccepted       int64
	totalRejected       int64
	sampleCounter       int
	sampleRate          int
	failureThreshold    int
	consecutiveFailures int
	resetInterval       time.Duration
	mu                  sync.Mutex
}

// CircuitBreakerConfig holds configuration for the circuit breaker.
type CircuitBreakerConfig struct {
	Logger           *slog.Logger
	BurstThreshold   int
	Window           time.Duration
	QuietPeriod      time.Duration
	FailureThreshold int
	ResetInterval    time.Duration
}

// NewCircuitBreaker creates a new CircuitBreaker with the given configuration.
func NewCircuitBreaker(cfg *CircuitBreakerConfig) *CircuitBreaker {
	if cfg == nil {
		cfg = &CircuitBreakerConfig{}
	}

	threshold := cfg.BurstThreshold
	if threshold <= 0 {
		threshold = 200
	}

	window := cfg.Window
	if window <= 0 {
		window = 1 * time.Second
	}

	quiet := cfg.QuietPeriod
	if quiet <= 0 {
		quiet = 500 * time.Millisecond
	}
	failureThreshold := cfg.FailureThreshold
	if failureThreshold <= 0 {
		failureThreshold = 3
	}
	resetInterval := cfg.ResetInterval
	if resetInterval <= 0 {
		resetInterval = quiet
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Sample rate: when burst threshold is exceeded, process every Nth event
	// where N is roughly burst/threshold to bring rate back to threshold
	sampleRate := 4 // Default: process 1 in 4

	return &CircuitBreaker{
		burstThreshold:   threshold,
		window:           window,
		quietPeriod:      quiet,
		logger:           logger,
		state:            CircuitClosed,
		eventTimes:       make([]time.Time, 0, threshold*2),
		sampleRate:       sampleRate,
		failureThreshold: failureThreshold,
		resetInterval:    resetInterval,
	}
}

// Allow checks whether an event should be processed.
// Returns true if the event should be processed, false if it should be dropped.
// This method also tracks the event for rate calculation.
func (cb *CircuitBreaker) Allow() bool {
	return cb.AllowAt(time.Now())
}

// AllowAt checks whether an event at the given time should be processed.
// This variant is useful for testing with controlled timestamps.
func (cb *CircuitBreaker) AllowAt(now time.Time) bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Prune old events outside the window
	cb.pruneOldEvents(now)

	// Record this event
	cb.eventTimes = append(cb.eventTimes, now)

	switch cb.state {
	case CircuitClosed:
		// Check if we should trip
		if len(cb.eventTimes) > cb.burstThreshold {
			cb.state = CircuitOpen
			cb.lastTrip = now
			cb.sampleCounter = 0
			cb.logger.Warn("circuit breaker tripped: burst detected",
				"events_in_window", len(cb.eventTimes),
				"threshold", cb.burstThreshold,
				"sample_rate", cb.sampleRate,
			)
		}
		cb.totalAccepted++
		return true

	case CircuitOpen:
		// Check if we should reset
		if len(cb.eventTimes) <= cb.burstThreshold && now.Sub(cb.lastTrip) >= cb.quietPeriod {
			cb.state = CircuitClosed
			cb.sampleCounter = 0
			cb.logger.Info("circuit breaker reset: burst subsided",
				"events_in_window", len(cb.eventTimes),
				"threshold", cb.burstThreshold,
			)
			cb.totalAccepted++
			return true
		}

		// Sample: process every Nth event
		cb.sampleCounter++
		if cb.sampleCounter >= cb.sampleRate {
			cb.sampleCounter = 0
			cb.totalAccepted++
			return true
		}

		cb.totalRejected++
		return false
	}

	cb.totalAccepted++
	return true
}

// pruneOldEvents removes events outside the detection window.
func (cb *CircuitBreaker) pruneOldEvents(now time.Time) {
	cutoff := now.Add(-cb.window)
	idx := 0
	for idx < len(cb.eventTimes) && cb.eventTimes[idx].Before(cutoff) {
		idx++
	}
	if idx > 0 {
		cb.eventTimes = cb.eventTimes[idx:]
	}
}

// State returns the current state of the circuit breaker.
func (cb *CircuitBreaker) State() CircuitBreakerState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Stats returns circuit breaker statistics.
func (cb *CircuitBreaker) Stats() CircuitBreakerStats {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return CircuitBreakerStats{
		State:          cb.state,
		TotalAccepted:  cb.totalAccepted,
		TotalRejected:  cb.totalRejected,
		EventsInWindow: len(cb.eventTimes),
		Threshold:      cb.burstThreshold,
	}
}

// CircuitBreakerStats holds circuit breaker statistics.
type CircuitBreakerStats struct {
	State          CircuitBreakerState
	TotalAccepted  int64
	TotalRejected  int64
	EventsInWindow int
	Threshold      int
}

// Reset manually resets the circuit breaker to closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = CircuitClosed
	cb.eventTimes = cb.eventTimes[:0]
	cb.sampleCounter = 0
}

// RecordSuccess records a successful downstream operation and closes the
// breaker after a failure-open state.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFailures = 0
	if cb.state == CircuitOpen {
		cb.state = CircuitClosed
		cb.eventTimes = cb.eventTimes[:0]
		cb.sampleCounter = 0
	}
}

// RecordFailure records a downstream failure and opens the breaker after the
// configured failure threshold.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFailures++
	if cb.consecutiveFailures >= cb.failureThreshold {
		cb.state = CircuitOpen
		cb.lastTrip = time.Now()
	}
}

// String returns a human-readable state description.
func (s CircuitBreakerState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	default:
		return "unknown"
	}
}
