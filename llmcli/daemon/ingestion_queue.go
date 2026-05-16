package daemon

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// ErrQueueClosed is returned when Push or Pop is called after Close.
var ErrQueueClosed = errors.New("daemon: ingestion queue closed")

// Event represents a generic event in the ingestion queue.
type Event struct {
	Timestamp time.Time
	Payload   interface{}
	Type      string
}

// IngestionQueue is a bounded FIFO queue for ingestion events.
// When the queue is full, it drops the oldest events (not newest).
// It logs a warning when the queue exceeds 75% capacity.
type IngestionQueue struct {
	logger        *slog.Logger
	events        []Event
	maxSize       int
	warnThreshold int
	totalDropped  int64
	totalEnqueued int64
	closed        bool
	mu            sync.Mutex
	cond          *sync.Cond
	warned        bool
}

// NewIngestionQueue creates a new IngestionQueue with the specified maximum size.
// If maxSize <= 0, it defaults to 8192.
func NewIngestionQueue(maxSize int, loggers ...*slog.Logger) *IngestionQueue {
	if maxSize <= 0 {
		maxSize = 8192
	}
	var logger *slog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	if logger == nil {
		logger = slog.Default()
	}

	q := &IngestionQueue{
		events:        make([]Event, 0, maxSize),
		maxSize:       maxSize,
		logger:        logger,
		warnThreshold: (maxSize * 3) / 4, // 75%
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Enqueue adds an event to the queue. If the queue is full, the oldest event
// is dropped to make room for the new one. Returns true if an event was dropped.
func (q *IngestionQueue) Enqueue(event Event) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	dropped := false

	// Check if queue is full
	if len(q.events) >= q.maxSize {
		// Drop oldest event (shift left)
		q.events = q.events[1:]
		q.totalDropped++
		dropped = true

		q.logger.Warn("ingestion queue full, dropping oldest event",
			"queue_size", q.maxSize,
			"total_dropped", q.totalDropped,
		)
	}

	q.events = append(q.events, event)
	q.totalEnqueued++
	q.cond.Signal()

	// Check 75% threshold
	if len(q.events) >= q.warnThreshold && !q.warned {
		q.warned = true
		q.logger.Warn("ingestion queue exceeds 75% capacity",
			"current_size", len(q.events),
			"max_size", q.maxSize,
			"threshold", q.warnThreshold,
		)
	} else if len(q.events) < q.warnThreshold {
		q.warned = false // Reset warning when below threshold
	}

	return dropped
}

// Push adds item to the queue, respecting ctx cancellation.
func (q *IngestionQueue) Push(ctx context.Context, item interface{}) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	q.Enqueue(Event{Timestamp: time.Now(), Payload: item})
	return nil
}

// Pop removes and returns the oldest queued payload, blocking until an item is
// available, the queue is closed, or ctx is cancelled.
func (q *IngestionQueue) Pop(ctx context.Context) (interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cancelled := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			q.mu.Lock()
			q.cond.Broadcast()
			q.mu.Unlock()
		case <-cancelled:
		}
	}()
	defer close(cancelled)

	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.events) == 0 && !q.closed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		q.cond.Wait()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if q.closed && len(q.events) == 0 {
		return nil, ErrQueueClosed
	}
	event := q.events[0]
	q.events = q.events[1:]
	return event.Payload, nil
}

// Close unblocks pending Pop calls.
func (q *IngestionQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cond.Broadcast()
}

// Dequeue removes and returns the oldest event from the queue.
// Returns the event and true if successful, or zero Event and false if empty.
func (q *IngestionQueue) Dequeue() (Event, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.events) == 0 {
		return Event{}, false
	}

	event := q.events[0]
	q.events = q.events[1:]

	return event, true
}

// DequeueN removes and returns up to n events from the queue.
func (q *IngestionQueue) DequeueN(n int) []Event {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.events) == 0 {
		return nil
	}

	if n > len(q.events) {
		n = len(q.events)
	}

	batch := make([]Event, n)
	copy(batch, q.events[:n])
	q.events = q.events[n:]

	return batch
}

// Len returns the current number of events in the queue.
func (q *IngestionQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.events)
}

// Cap returns the maximum capacity of the queue.
func (q *IngestionQueue) Cap() int {
	return q.maxSize
}

// Stats returns queue statistics.
func (q *IngestionQueue) Stats() IngestionQueueStats {
	q.mu.Lock()
	defer q.mu.Unlock()
	return IngestionQueueStats{
		CurrentSize:   len(q.events),
		MaxSize:       q.maxSize,
		TotalEnqueued: q.totalEnqueued,
		TotalDropped:  q.totalDropped,
	}
}

// IngestionQueueStats holds queue statistics.
type IngestionQueueStats struct {
	CurrentSize   int
	MaxSize       int
	TotalEnqueued int64
	TotalDropped  int64
}

// Clear empties the queue.
func (q *IngestionQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.events = q.events[:0]
	q.warned = false
}
