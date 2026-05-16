package daemon

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIngestionQueue_NewDefaults(t *testing.T) {
	t.Parallel()

	q := NewIngestionQueue(0, nil)
	if q.Cap() != 8192 {
		t.Errorf("expected default capacity 8192, got %d", q.Cap())
	}
	if q.Len() != 0 {
		t.Errorf("expected empty queue, got %d", q.Len())
	}
}

func TestIngestionQueue_NewCustomSize(t *testing.T) {
	t.Parallel()

	q := NewIngestionQueue(100, nil)
	if q.Cap() != 100 {
		t.Errorf("expected capacity 100, got %d", q.Cap())
	}
}

func TestIngestionQueue_EnqueueDequeue(t *testing.T) {
	t.Parallel()

	q := NewIngestionQueue(10, nil)

	// Enqueue events
	for i := 0; i < 5; i++ {
		event := Event{Type: "test", Payload: i, Timestamp: time.Now()}
		dropped := q.Enqueue(event)
		if dropped {
			t.Errorf("should not drop when queue has capacity (i=%d)", i)
		}
	}

	if q.Len() != 5 {
		t.Errorf("expected 5 events, got %d", q.Len())
	}

	// Dequeue events
	for i := 0; i < 5; i++ {
		event, ok := q.Dequeue()
		if !ok {
			t.Fatalf("Dequeue should succeed (i=%d)", i)
		}
		if event.Payload.(int) != i {
			t.Errorf("expected payload %d, got %d", i, event.Payload.(int))
		}
	}

	if q.Len() != 0 {
		t.Errorf("expected empty queue, got %d", q.Len())
	}
}

func TestIngestionQueue_DequeueEmpty(t *testing.T) {
	t.Parallel()

	q := NewIngestionQueue(10, nil)

	event, ok := q.Dequeue()
	if ok {
		t.Error("Dequeue should return false for empty queue")
	}
	if event.Type != "" {
		t.Error("event should be zero value for empty queue")
	}
}

func TestIngestionQueue_DropsOldest(t *testing.T) {
	t.Parallel()

	q := NewIngestionQueue(3, nil)

	// Fill queue
	for i := 0; i < 3; i++ {
		q.Enqueue(Event{Type: "test", Payload: i})
	}

	// Enqueue one more - should drop oldest (0)
	dropped := q.Enqueue(Event{Type: "test", Payload: 3})
	if !dropped {
		t.Error("should have dropped oldest event")
	}

	if q.Len() != 3 {
		t.Errorf("expected 3 events, got %d", q.Len())
	}

	// First event should now be 1 (0 was dropped)
	event, ok := q.Dequeue()
	if !ok {
		t.Fatal("Dequeue should succeed")
	}
	if event.Payload.(int) != 1 {
		t.Errorf("expected payload 1 (oldest dropped), got %d", event.Payload.(int))
	}
}

func TestIngestionQueue_DropsMultipleOldest(t *testing.T) {
	t.Parallel()

	q := NewIngestionQueue(3, nil)

	// Fill queue with 0, 1, 2
	for i := 0; i < 3; i++ {
		q.Enqueue(Event{Type: "test", Payload: i})
	}

	// Enqueue 3 more - should drop 0, 1, 2 sequentially
	for i := 3; i < 6; i++ {
		q.Enqueue(Event{Type: "test", Payload: i})
	}

	// Queue should contain 3, 4, 5
	for i := 3; i < 6; i++ {
		event, ok := q.Dequeue()
		if !ok {
			t.Fatalf("Dequeue should succeed (i=%d)", i)
		}
		if event.Payload.(int) != i {
			t.Errorf("expected payload %d, got %d", i, event.Payload.(int))
		}
	}
}

func TestIngestionQueue_75PercentWarning(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	q := NewIngestionQueue(100, logger)

	// Enqueue to 74% - no warning
	for i := 0; i < 74; i++ {
		q.Enqueue(Event{Type: "test", Payload: i})
	}

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "75% capacity") {
		t.Error("should not warn at 74%")
	}

	// Enqueue to 75% - should warn
	q.Enqueue(Event{Type: "test", Payload: 74})

	logOutput = logBuf.String()
	if !strings.Contains(logOutput, "75% capacity") {
		t.Error("should warn at 75%")
	}
}

func TestIngestionQueue_WarningResetsWhenDrained(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	q := NewIngestionQueue(10, logger)

	// Fill to 75%+
	for i := 0; i < 8; i++ {
		q.Enqueue(Event{Type: "test", Payload: i})
	}

	// Drain below 75%
	for i := 0; i < 4; i++ {
		q.Dequeue()
	}

	// Reset log
	logBuf.Reset()

	// Fill to 75% again - should warn again
	for i := 0; i < 4; i++ {
		q.Enqueue(Event{Type: "test", Payload: i})
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "75% capacity") {
		t.Error("should warn again after draining below threshold")
	}
}

func TestIngestionQueue_Stats(t *testing.T) {
	t.Parallel()

	q := NewIngestionQueue(5, nil)

	// Enqueue 7 events into a queue of 5
	for i := 0; i < 7; i++ {
		q.Enqueue(Event{Type: "test", Payload: i})
	}

	stats := q.Stats()
	if stats.CurrentSize != 5 {
		t.Errorf("expected current size 5, got %d", stats.CurrentSize)
	}
	if stats.MaxSize != 5 {
		t.Errorf("expected max size 5, got %d", stats.MaxSize)
	}
	if stats.TotalEnqueued != 7 {
		t.Errorf("expected total enqueued 7, got %d", stats.TotalEnqueued)
	}
	if stats.TotalDropped != 2 {
		t.Errorf("expected total dropped 2, got %d", stats.TotalDropped)
	}
}

func TestIngestionQueue_DequeueN(t *testing.T) {
	t.Parallel()

	q := NewIngestionQueue(10, nil)

	for i := 0; i < 5; i++ {
		q.Enqueue(Event{Type: "test", Payload: i})
	}

	// Dequeue 3
	batch := q.DequeueN(3)
	if len(batch) != 3 {
		t.Errorf("expected 3 events, got %d", len(batch))
	}
	for i := 0; i < 3; i++ {
		if batch[i].Payload.(int) != i {
			t.Errorf("expected payload %d, got %d", i, batch[i].Payload.(int))
		}
	}

	if q.Len() != 2 {
		t.Errorf("expected 2 remaining, got %d", q.Len())
	}
}

func TestIngestionQueue_DequeueN_MoreThanAvailable(t *testing.T) {
	t.Parallel()

	q := NewIngestionQueue(10, nil)

	for i := 0; i < 3; i++ {
		q.Enqueue(Event{Type: "test", Payload: i})
	}

	batch := q.DequeueN(10)
	if len(batch) != 3 {
		t.Errorf("expected 3 events (all available), got %d", len(batch))
	}

	if q.Len() != 0 {
		t.Errorf("expected empty queue, got %d", q.Len())
	}
}

func TestIngestionQueue_DequeueN_Empty(t *testing.T) {
	t.Parallel()

	q := NewIngestionQueue(10, nil)

	batch := q.DequeueN(5)
	if batch != nil {
		t.Errorf("expected nil for empty queue, got %v", batch)
	}
}

func TestIngestionQueue_Clear(t *testing.T) {
	t.Parallel()

	q := NewIngestionQueue(10, nil)

	for i := 0; i < 5; i++ {
		q.Enqueue(Event{Type: "test", Payload: i})
	}

	q.Clear()

	if q.Len() != 0 {
		t.Errorf("expected empty queue after Clear, got %d", q.Len())
	}
}

func TestIngestionQueue_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	q := NewIngestionQueue(100, nil)

	const numWriters = 10
	const numReaders = 10
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numWriters + numReaders)

	// Writers
	for i := 0; i < numWriters; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				q.Enqueue(Event{
					Type:    "test",
					Payload: id*opsPerGoroutine + j,
				})
			}
		}(i)
	}

	// Readers
	for i := 0; i < numReaders; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				q.Dequeue()
			}
		}()
	}

	wg.Wait()

	// Should not panic or deadlock
	// Final size should be consistent
	stats := q.Stats()
	if stats.CurrentSize < 0 {
		t.Errorf("queue size should not be negative, got %d", stats.CurrentSize)
	}
}

func TestIngestionQueue_FIFO_Order(t *testing.T) {
	t.Parallel()

	q := NewIngestionQueue(100, nil)

	// Enqueue in order
	for i := 0; i < 10; i++ {
		q.Enqueue(Event{Type: "test", Payload: i})
	}

	// Dequeue should be in same order (FIFO)
	for i := 0; i < 10; i++ {
		event, ok := q.Dequeue()
		if !ok {
			t.Fatalf("Dequeue should succeed at i=%d", i)
		}
		if event.Payload.(int) != i {
			t.Errorf("expected FIFO order: payload %d at position %d, got %d",
				i, i, event.Payload.(int))
		}
	}
}
