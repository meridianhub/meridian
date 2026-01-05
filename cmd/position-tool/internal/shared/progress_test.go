package shared

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgressEventType_String(t *testing.T) {
	tests := []struct {
		eventType ProgressEventType
		expected  string
	}{
		{ProgressEventStarted, "started"},
		{ProgressEventBatchComplete, "batch_complete"},
		{ProgressEventError, "error"},
		{ProgressEventComplete, "complete"},
		{ProgressEventType(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.eventType.String())
		})
	}
}

func TestNewProgressTracker(t *testing.T) {
	t.Run("creates tracker with default config", func(t *testing.T) {
		pt := NewProgressTracker(ProgressTrackerConfig{})
		require.NotNil(t, pt)

		stats := pt.Stats()
		assert.False(t, stats.Started)
		assert.False(t, stats.Completed)
		assert.False(t, stats.DryRun)
		assert.Equal(t, 0, stats.TotalProcessed)
	})

	t.Run("creates tracker with dry-run mode", func(t *testing.T) {
		pt := NewProgressTracker(ProgressTrackerConfig{
			DryRun: true,
		})
		require.NotNil(t, pt)
		assert.True(t, pt.IsDryRun())
	})

	t.Run("creates tracker with total expected", func(t *testing.T) {
		pt := NewProgressTracker(ProgressTrackerConfig{
			TotalExpected: 1000,
		})
		require.NotNil(t, pt)

		stats := pt.Stats()
		assert.Equal(t, 1000, stats.TotalExpected)
	})
}

func TestProgressTracker_Lifecycle(t *testing.T) {
	ctx := context.Background()
	pt := NewProgressTracker(ProgressTrackerConfig{
		TotalExpected: 100,
	})

	// Start
	pt.Start(ctx, "Starting import")
	stats := pt.Stats()
	assert.True(t, stats.Started)
	assert.False(t, stats.Completed)

	// Process batches
	pt.BatchComplete(ctx, 25, "Batch 1 complete")
	stats = pt.Stats()
	assert.Equal(t, 25, stats.TotalProcessed)
	assert.Equal(t, 1, stats.BatchCount)

	pt.BatchComplete(ctx, 25, "Batch 2 complete")
	stats = pt.Stats()
	assert.Equal(t, 50, stats.TotalProcessed)
	assert.Equal(t, 2, stats.BatchCount)

	// Complete
	pt.Complete(ctx, "Import finished")
	stats = pt.Stats()
	assert.True(t, stats.Started)
	assert.True(t, stats.Completed)
	assert.Equal(t, 50, stats.TotalProcessed)
}

func TestProgressTracker_WithCallback(t *testing.T) {
	ctx := context.Background()
	events := make([]ProgressEvent, 0, 3)
	var mu sync.Mutex

	pt := NewProgressTracker(ProgressTrackerConfig{
		TotalExpected: 100,
		OnProgress: func(event ProgressEvent) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		},
	})

	pt.Start(ctx, "Starting")
	pt.BatchComplete(ctx, 50, "Half done")
	pt.Complete(ctx, "Done")

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, events, 3)
	assert.Equal(t, ProgressEventStarted, events[0].Type)
	assert.Equal(t, ProgressEventBatchComplete, events[1].Type)
	assert.Equal(t, ProgressEventComplete, events[2].Type)
}

func TestProgressTracker_WithChannel(t *testing.T) {
	ctx := context.Background()
	eventChan := make(chan ProgressEvent, 10)

	pt := NewProgressTracker(ProgressTrackerConfig{
		EventChan: eventChan,
	})

	pt.Start(ctx, "Starting")
	pt.BatchComplete(ctx, 100, "Done")
	pt.Complete(ctx, "Finished")

	close(eventChan)

	events := make([]ProgressEvent, 0, 3)
	for event := range eventChan {
		events = append(events, event)
	}

	require.Len(t, events, 3)
	assert.Equal(t, ProgressEventStarted, events[0].Type)
	assert.Equal(t, ProgressEventBatchComplete, events[1].Type)
	assert.Equal(t, ProgressEventComplete, events[2].Type)
}

// errTestProgress is a sentinel error for testing progress tracker error handling.
var errTestProgress = errors.New("test error")

func TestProgressTracker_Error(t *testing.T) {
	ctx := context.Background()

	var capturedEvent ProgressEvent
	pt := NewProgressTracker(ProgressTrackerConfig{
		OnProgress: func(event ProgressEvent) {
			if event.Type == ProgressEventError {
				capturedEvent = event
			}
		},
	})

	pt.Start(ctx, "Starting")
	pt.Error(ctx, errTestProgress, "Something went wrong")

	stats := pt.Stats()
	assert.Equal(t, errTestProgress, stats.LastError)

	assert.Equal(t, ProgressEventError, capturedEvent.Type)
	assert.Equal(t, errTestProgress, capturedEvent.Error)
	assert.Equal(t, "Something went wrong", capturedEvent.Message)
}

func TestProgressTracker_ChannelFull(*testing.T) {
	ctx := context.Background()
	// Unbuffered channel - will block if not handled
	eventChan := make(chan ProgressEvent)

	pt := NewProgressTracker(ProgressTrackerConfig{
		EventChan: eventChan,
	})

	// This should not block even though channel is full
	pt.Start(ctx, "Starting")
	pt.BatchComplete(ctx, 100, "Done")

	// If we got here without deadlock, the test passes
	close(eventChan)
}

func TestProgressStats_PositionsPerSecond(t *testing.T) {
	tests := []struct {
		name           string
		totalProcessed int
		duration       time.Duration
		expected       float64
	}{
		{
			name:           "normal rate",
			totalProcessed: 1000,
			duration:       time.Second,
			expected:       1000.0,
		},
		{
			name:           "zero duration",
			totalProcessed: 1000,
			duration:       0,
			expected:       0,
		},
		{
			name:           "zero positions",
			totalProcessed: 0,
			duration:       time.Second,
			expected:       0,
		},
		{
			name:           "slow rate",
			totalProcessed: 100,
			duration:       10 * time.Second,
			expected:       10.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := ProgressStats{
				TotalProcessed: tt.totalProcessed,
				Duration:       tt.duration,
			}
			assert.InDelta(t, tt.expected, stats.PositionsPerSecond(), 0.001)
		})
	}
}

func TestProgressStats_EstimatedRemaining(t *testing.T) {
	tests := []struct {
		name           string
		totalExpected  int
		totalProcessed int
		duration       time.Duration
		expectedRange  [2]time.Duration // min, max acceptable values
	}{
		{
			name:           "halfway done",
			totalExpected:  1000,
			totalProcessed: 500,
			duration:       10 * time.Second,
			expectedRange:  [2]time.Duration{9 * time.Second, 11 * time.Second},
		},
		{
			name:           "unknown total",
			totalExpected:  0,
			totalProcessed: 500,
			duration:       10 * time.Second,
			expectedRange:  [2]time.Duration{0, 0},
		},
		{
			name:           "no progress",
			totalExpected:  1000,
			totalProcessed: 0,
			duration:       10 * time.Second,
			expectedRange:  [2]time.Duration{0, 0},
		},
		{
			name:           "completed",
			totalExpected:  1000,
			totalProcessed: 1000,
			duration:       10 * time.Second,
			expectedRange:  [2]time.Duration{0, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := ProgressStats{
				TotalExpected:  tt.totalExpected,
				TotalProcessed: tt.totalProcessed,
				Duration:       tt.duration,
			}
			remaining := stats.EstimatedRemaining()
			assert.GreaterOrEqual(t, remaining, tt.expectedRange[0])
			assert.LessOrEqual(t, remaining, tt.expectedRange[1])
		})
	}
}

func TestProgressStats_PercentComplete(t *testing.T) {
	ctx := context.Background()

	pt := NewProgressTracker(ProgressTrackerConfig{
		TotalExpected: 1000,
	})

	pt.Start(ctx, "Starting")

	// 0%
	stats := pt.Stats()
	assert.Equal(t, 0.0, stats.PercentComplete)

	// 25%
	pt.BatchComplete(ctx, 250, "")
	stats = pt.Stats()
	assert.InDelta(t, 25.0, stats.PercentComplete, 0.01)

	// 50%
	pt.BatchComplete(ctx, 250, "")
	stats = pt.Stats()
	assert.InDelta(t, 50.0, stats.PercentComplete, 0.01)

	// 100%
	pt.BatchComplete(ctx, 500, "")
	stats = pt.Stats()
	assert.InDelta(t, 100.0, stats.PercentComplete, 0.01)
}

func TestProgressEvent_Fields(t *testing.T) {
	now := time.Now()

	event := ProgressEvent{
		Type:             ProgressEventBatchComplete,
		BatchNumber:      5,
		PositionsInBatch: 500,
		TotalProcessed:   2500,
		TotalExpected:    10000,
		Duration:         5 * time.Second,
		Message:          "Batch complete",
		Error:            errTestProgress,
		Timestamp:        now,
	}

	assert.Equal(t, ProgressEventBatchComplete, event.Type)
	assert.Equal(t, 5, event.BatchNumber)
	assert.Equal(t, 500, event.PositionsInBatch)
	assert.Equal(t, 2500, event.TotalProcessed)
	assert.Equal(t, 10000, event.TotalExpected)
	assert.Equal(t, 5*time.Second, event.Duration)
	assert.Equal(t, "Batch complete", event.Message)
	assert.Equal(t, errTestProgress, event.Error)
	assert.Equal(t, now, event.Timestamp)
}

func BenchmarkProgressTracker_BatchComplete(b *testing.B) {
	ctx := context.Background()
	pt := NewProgressTracker(ProgressTrackerConfig{
		TotalExpected: b.N * 100,
	})

	pt.Start(ctx, "Starting")

	b.ResetTimer()
	for b.Loop() {
		pt.BatchComplete(ctx, 100, "")
	}
}

func BenchmarkProgressTracker_WithCallback(b *testing.B) {
	ctx := context.Background()
	pt := NewProgressTracker(ProgressTrackerConfig{
		TotalExpected: b.N * 100,
		OnProgress:    func(_ ProgressEvent) {},
	})

	pt.Start(ctx, "Starting")

	b.ResetTimer()
	for b.Loop() {
		pt.BatchComplete(ctx, 100, "")
	}
}
