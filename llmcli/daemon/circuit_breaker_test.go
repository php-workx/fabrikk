package daemon

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCircuitBreaker_DefaultConfig(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(nil)
	if cb.burstThreshold != 200 {
		t.Errorf("expected default threshold 200, got %d", cb.burstThreshold)
	}
	if cb.window != 1*time.Second {
		t.Errorf("expected default window 1s, got %v", cb.window)
	}
	if cb.quietPeriod != 500*time.Millisecond {
		t.Errorf("expected default quiet period 500ms, got %v", cb.quietPeriod)
	}
	if cb.State() != CircuitClosed {
		t.Errorf("expected initial state closed, got %s", cb.State())
	}
}

func TestCircuitBreaker_CustomConfig(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		BurstThreshold: 100,
		Window:         2 * time.Second,
		QuietPeriod:    1 * time.Second,
	})
	if cb.burstThreshold != 100 {
		t.Errorf("expected threshold 100, got %d", cb.burstThreshold)
	}
	if cb.window != 2*time.Second {
		t.Errorf("expected window 2s, got %v", cb.window)
	}
	if cb.quietPeriod != 1*time.Second {
		t.Errorf("expected quiet period 1s, got %v", cb.quietPeriod)
	}
}

func TestCircuitBreaker_AllowsUnderThreshold(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		BurstThreshold: 10,
		Window:         1 * time.Second,
	})

	now := time.Now()

	// Under threshold - all should be allowed
	for i := 0; i < 10; i++ {
		allowed := cb.AllowAt(now.Add(time.Duration(i) * time.Millisecond))
		if !allowed {
			t.Errorf("event %d should be allowed (under threshold)", i)
		}
	}

	if cb.State() != CircuitClosed {
		t.Error("circuit should remain closed under threshold")
	}
}

func TestCircuitBreaker_TripsOnBurst(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		BurstThreshold: 5,
		Window:         1 * time.Second,
		Logger:         logger,
	})

	now := time.Now()

	// Send events up to threshold (these are all allowed)
	for i := 0; i < 5; i++ {
		cb.AllowAt(now.Add(time.Duration(i) * time.Millisecond))
	}

	if cb.State() != CircuitClosed {
		t.Error("circuit should still be closed at exactly threshold")
	}

	// One more event trips the breaker (the tripping event itself is allowed)
	allowed := cb.AllowAt(now.Add(5 * time.Millisecond))
	if !allowed {
		t.Error("the event that trips the breaker should still be allowed")
	}

	if cb.State() != CircuitOpen {
		t.Error("circuit should be open after exceeding threshold")
	}

	// Verify warning was logged
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "circuit breaker tripped") {
		t.Error("expected circuit breaker trip warning in log")
	}
}

func TestCircuitBreaker_SamplesWhenOpen(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		BurstThreshold: 5,
		Window:         1 * time.Second,
		QuietPeriod:    10 * time.Second, // Long quiet period so we stay open
	})

	now := time.Now()

	// Trip the breaker
	for i := 0; i < 6; i++ {
		cb.AllowAt(now.Add(time.Duration(i) * time.Millisecond))
	}

	if cb.State() != CircuitOpen {
		t.Fatal("circuit should be open")
	}

	// In open state, every 4th event should be allowed (sampleRate=4)
	allowedCount := 0
	rejectedCount := 0
	for i := 0; i < 20; i++ {
		if cb.AllowAt(now.Add(time.Duration(10+i) * time.Millisecond)) {
			allowedCount++
		} else {
			rejectedCount++
		}
	}

	// With sample rate 4, roughly 1/4 of events should be allowed
	if allowedCount == 0 {
		t.Error("some events should be allowed during sampling")
	}
	if rejectedCount == 0 {
		t.Error("some events should be rejected during sampling")
	}

	// Exactly: 20 events / 4 sample rate = 5 allowed
	if allowedCount != 5 {
		t.Errorf("expected 5 allowed events with sample rate 4, got %d", allowedCount)
	}
}

func TestCircuitBreaker_AutoResets(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		BurstThreshold: 5,
		Window:         100 * time.Millisecond,
		QuietPeriod:    200 * time.Millisecond,
		Logger:         logger,
	})

	now := time.Now()

	// Trip the breaker with burst
	for i := 0; i < 6; i++ {
		cb.AllowAt(now.Add(time.Duration(i) * time.Millisecond))
	}

	if cb.State() != CircuitOpen {
		t.Fatal("circuit should be open")
	}

	// Wait for window + quiet period to elapse, then send a low-rate event
	futureTime := now.Add(500 * time.Millisecond) // Well past window + quiet period
	allowed := cb.AllowAt(futureTime)
	if !allowed {
		t.Error("event should be allowed after reset")
	}

	if cb.State() != CircuitClosed {
		t.Error("circuit should be closed after quiet period")
	}

	// Verify reset was logged
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "circuit breaker reset") {
		t.Error("expected circuit breaker reset log message")
	}
}

func TestCircuitBreaker_Stats(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		BurstThreshold: 3,
		Window:         1 * time.Second,
		QuietPeriod:    10 * time.Second,
	})

	now := time.Now()

	// Send 4 events to trip + 8 more in open state
	for i := 0; i < 12; i++ {
		cb.AllowAt(now.Add(time.Duration(i) * time.Millisecond))
	}

	stats := cb.Stats()
	if stats.State != CircuitOpen {
		t.Errorf("expected open state, got %s", stats.State)
	}
	if stats.TotalAccepted == 0 {
		t.Error("expected some accepted events")
	}
	if stats.TotalRejected == 0 {
		t.Error("expected some rejected events")
	}
	if stats.TotalAccepted+stats.TotalRejected != 12 {
		t.Errorf("total accepted + rejected should be 12, got %d",
			stats.TotalAccepted+stats.TotalRejected)
	}
	if stats.Threshold != 3 {
		t.Errorf("expected threshold 3, got %d", stats.Threshold)
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		BurstThreshold: 3,
		Window:         1 * time.Second,
		QuietPeriod:    10 * time.Second,
	})

	now := time.Now()

	// Trip the breaker
	for i := 0; i < 5; i++ {
		cb.AllowAt(now.Add(time.Duration(i) * time.Millisecond))
	}

	if cb.State() != CircuitOpen {
		t.Fatal("circuit should be open")
	}

	// Manual reset
	cb.Reset()

	if cb.State() != CircuitClosed {
		t.Error("circuit should be closed after Reset")
	}

	// All events should be allowed again
	allowed := cb.AllowAt(now.Add(10 * time.Millisecond))
	if !allowed {
		t.Error("event should be allowed after Reset")
	}
}

func TestCircuitBreaker_StateString(t *testing.T) {
	t.Parallel()

	if CircuitClosed.String() != "closed" {
		t.Errorf("expected 'closed', got %s", CircuitClosed.String())
	}
	if CircuitOpen.String() != "open" {
		t.Errorf("expected 'open', got %s", CircuitOpen.String())
	}
	if CircuitBreakerState(99).String() != "unknown" {
		t.Errorf("expected 'unknown', got %s", CircuitBreakerState(99).String())
	}
}

func TestCircuitBreaker_WindowPruning(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		BurstThreshold: 5,
		Window:         100 * time.Millisecond,
	})

	now := time.Now()

	// Send events at time 0
	for i := 0; i < 4; i++ {
		cb.AllowAt(now)
	}

	// After the window, old events should be pruned
	future := now.Add(200 * time.Millisecond)
	cb.AllowAt(future)

	stats := cb.Stats()
	// Only the latest event should be in the window
	if stats.EventsInWindow != 1 {
		t.Errorf("expected 1 event in window after pruning, got %d", stats.EventsInWindow)
	}
}

func TestCircuitBreaker_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		BurstThreshold: 50,
		Window:         1 * time.Second,
	})

	const numGoroutines = 20
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				cb.Allow()
			}
		}()
	}

	wg.Wait()

	// Should not panic or deadlock
	stats := cb.Stats()
	total := stats.TotalAccepted + stats.TotalRejected
	if total != int64(numGoroutines*opsPerGoroutine) {
		t.Errorf("expected total %d, got %d", numGoroutines*opsPerGoroutine, total)
	}
}

func TestCircuitBreaker_DoesNotTripWithSpreadEvents(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		BurstThreshold: 5,
		Window:         100 * time.Millisecond,
	})

	now := time.Now()

	// Send events spread across different windows
	for i := 0; i < 20; i++ {
		// Each event is 50ms apart, so at most 2-3 events in any 100ms window
		allowed := cb.AllowAt(now.Add(time.Duration(i*50) * time.Millisecond))
		if !allowed {
			t.Errorf("event %d should be allowed with spread events", i)
		}
	}

	if cb.State() != CircuitClosed {
		t.Error("circuit should remain closed with spread events")
	}
}

func TestCircuitBreaker_Allow_UsesCurrentTime(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		BurstThreshold: 1000, // High threshold - won't trip
		Window:         1 * time.Second,
	})

	// Allow() should work without panicking
	allowed := cb.Allow()
	if !allowed {
		t.Error("Allow should return true under threshold")
	}
}
