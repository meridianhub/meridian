package shared

import (
	"context"
	"sync"
	"time"
)

// ProgressEvent represents a progress update during bulk operations.
type ProgressEvent struct {
	// Type indicates the kind of progress event.
	Type ProgressEventType

	// BatchNumber is the current batch number (1-indexed).
	BatchNumber int

	// PositionsInBatch is the number of positions in this batch.
	PositionsInBatch int

	// TotalProcessed is the cumulative count of positions processed.
	TotalProcessed int

	// TotalExpected is the total number of positions to process (if known, 0 otherwise).
	TotalExpected int

	// Duration is the time elapsed since the operation started.
	Duration time.Duration

	// Message provides additional context for the event.
	Message string

	// Error is set for error events.
	Error error

	// Timestamp when this event occurred.
	Timestamp time.Time
}

// ProgressEventType indicates the type of progress event.
type ProgressEventType int

const (
	// ProgressEventStarted indicates the operation has started.
	ProgressEventStarted ProgressEventType = iota
	// ProgressEventBatchComplete indicates a batch was successfully processed.
	ProgressEventBatchComplete
	// ProgressEventError indicates an error occurred.
	ProgressEventError
	// ProgressEventComplete indicates the operation completed successfully.
	ProgressEventComplete
)

// String returns a human-readable name for the event type.
func (t ProgressEventType) String() string {
	switch t {
	case ProgressEventStarted:
		return "started"
	case ProgressEventBatchComplete:
		return "batch_complete"
	case ProgressEventError:
		return "error"
	case ProgressEventComplete:
		return "complete"
	default:
		return "unknown"
	}
}

// ProgressTracker tracks progress of bulk operations and provides
// channel-based callbacks for progress updates.
//
// The tracker supports both callback-based and channel-based progress
// notifications, as well as a dry-run mode where operations are simulated
// without actual database writes.
type ProgressTracker struct {
	mu sync.RWMutex

	// Channel for progress events (optional)
	eventChan chan<- ProgressEvent

	// Callback for progress events (optional)
	onProgress func(ProgressEvent)

	// Configuration
	dryRun        bool
	totalExpected int

	// State
	startTime      time.Time
	totalProcessed int
	batchCount     int
	lastError      error
	started        bool
	completed      bool
}

// ProgressTrackerConfig contains configuration for creating a ProgressTracker.
type ProgressTrackerConfig struct {
	// EventChan is an optional channel to receive progress events.
	// The channel should have sufficient buffer to avoid blocking.
	EventChan chan<- ProgressEvent

	// OnProgress is an optional callback function called for each progress event.
	// Called synchronously, so it should not block.
	OnProgress func(ProgressEvent)

	// DryRun indicates whether this is a dry-run (no actual writes).
	DryRun bool

	// TotalExpected is the expected total number of items to process (if known).
	TotalExpected int
}

// NewProgressTracker creates a new progress tracker with the given configuration.
func NewProgressTracker(config ProgressTrackerConfig) *ProgressTracker {
	return &ProgressTracker{
		eventChan:     config.EventChan,
		onProgress:    config.OnProgress,
		dryRun:        config.DryRun,
		totalExpected: config.TotalExpected,
	}
}

// Start marks the beginning of the operation.
// Should be called once before any batch processing.
func (pt *ProgressTracker) Start(_ context.Context, message string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	pt.startTime = time.Now()
	pt.started = true

	pt.emit(ProgressEvent{
		Type:          ProgressEventStarted,
		TotalExpected: pt.totalExpected,
		Message:       message,
		Timestamp:     pt.startTime,
	})
}

// BatchComplete records completion of a batch.
// Should be called after each batch is successfully processed.
func (pt *ProgressTracker) BatchComplete(_ context.Context, positionsInBatch int, message string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	pt.batchCount++
	pt.totalProcessed += positionsInBatch

	pt.emit(ProgressEvent{
		Type:             ProgressEventBatchComplete,
		BatchNumber:      pt.batchCount,
		PositionsInBatch: positionsInBatch,
		TotalProcessed:   pt.totalProcessed,
		TotalExpected:    pt.totalExpected,
		Duration:         time.Since(pt.startTime),
		Message:          message,
		Timestamp:        time.Now(),
	})
}

// Error records an error during processing.
// The operation may continue after an error depending on the caller's policy.
func (pt *ProgressTracker) Error(_ context.Context, err error, message string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	pt.lastError = err

	pt.emit(ProgressEvent{
		Type:           ProgressEventError,
		BatchNumber:    pt.batchCount + 1, // The failing batch
		TotalProcessed: pt.totalProcessed,
		TotalExpected:  pt.totalExpected,
		Duration:       time.Since(pt.startTime),
		Message:        message,
		Error:          err,
		Timestamp:      time.Now(),
	})
}

// Complete marks the operation as finished.
// Should be called once after all processing is done.
func (pt *ProgressTracker) Complete(_ context.Context, message string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	pt.completed = true

	pt.emit(ProgressEvent{
		Type:           ProgressEventComplete,
		BatchNumber:    pt.batchCount,
		TotalProcessed: pt.totalProcessed,
		TotalExpected:  pt.totalExpected,
		Duration:       time.Since(pt.startTime),
		Message:        message,
		Timestamp:      time.Now(),
	})
}

// emit sends a progress event to configured listeners.
func (pt *ProgressTracker) emit(event ProgressEvent) {
	// Send to channel if configured
	if pt.eventChan != nil {
		select {
		case pt.eventChan <- event:
		default:
			// Channel is full or closed - skip to avoid blocking
		}
	}

	// Call callback if configured
	if pt.onProgress != nil {
		pt.onProgress(event)
	}
}

// Stats returns the current progress statistics.
func (pt *ProgressTracker) Stats() ProgressStats {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	var duration time.Duration
	if pt.started {
		duration = time.Since(pt.startTime)
	}

	var percentComplete float64
	if pt.totalExpected > 0 {
		percentComplete = float64(pt.totalProcessed) / float64(pt.totalExpected) * 100
	}

	return ProgressStats{
		TotalProcessed:  pt.totalProcessed,
		TotalExpected:   pt.totalExpected,
		BatchCount:      pt.batchCount,
		Duration:        duration,
		PercentComplete: percentComplete,
		DryRun:          pt.dryRun,
		Started:         pt.started,
		Completed:       pt.completed,
		LastError:       pt.lastError,
	}
}

// IsDryRun returns whether this is a dry-run operation.
func (pt *ProgressTracker) IsDryRun() bool {
	return pt.dryRun
}

// ProgressStats contains statistics about the current progress.
type ProgressStats struct {
	// TotalProcessed is the total number of items processed so far.
	TotalProcessed int

	// TotalExpected is the expected total (0 if unknown).
	TotalExpected int

	// BatchCount is the number of batches completed.
	BatchCount int

	// Duration is the elapsed time since start.
	Duration time.Duration

	// PercentComplete is the completion percentage (0-100, 0 if total unknown).
	PercentComplete float64

	// DryRun indicates whether this is a dry-run.
	DryRun bool

	// Started indicates whether the operation has started.
	Started bool

	// Completed indicates whether the operation has completed.
	Completed bool

	// LastError is the most recent error (if any).
	LastError error
}

// PositionsPerSecond calculates the processing rate.
// Returns 0 if duration is 0 or no positions have been processed.
func (s ProgressStats) PositionsPerSecond() float64 {
	if s.Duration == 0 || s.TotalProcessed == 0 {
		return 0
	}
	return float64(s.TotalProcessed) / s.Duration.Seconds()
}

// EstimatedRemaining calculates the estimated remaining time.
// Returns 0 if rate cannot be calculated or total is unknown.
func (s ProgressStats) EstimatedRemaining() time.Duration {
	if s.TotalExpected == 0 || s.TotalProcessed == 0 || s.Duration == 0 {
		return 0
	}

	remaining := s.TotalExpected - s.TotalProcessed
	if remaining <= 0 {
		return 0
	}

	rate := s.PositionsPerSecond()
	if rate == 0 {
		return 0
	}

	return time.Duration(float64(remaining)/rate) * time.Second
}
