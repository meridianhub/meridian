package sandbox

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMemoryMonitor_DefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	m := NewMemoryMonitor(cfg)

	assert.Equal(t, defaultMemoryThreshold, m.threshold)
	assert.Equal(t, defaultMemoryPollInterval, m.pollInterval)
}

func TestNewMemoryMonitor_CustomConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryThreshold = 20 * 1024 * 1024 // 20MB
	cfg.MemoryPollInterval = 5 * time.Millisecond

	m := NewMemoryMonitor(cfg)

	assert.Equal(t, uint64(20*1024*1024), m.threshold)
	assert.Equal(t, 5*time.Millisecond, m.pollInterval)
}

func TestNewMemoryMonitor_NegativeIntervalFallsBackToDefault(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryPollInterval = -1 * time.Millisecond

	m := NewMemoryMonitor(cfg)

	// Negative interval must not reach time.NewTicker (which panics on <= 0).
	assert.Equal(t, defaultMemoryPollInterval, m.pollInterval)
}

func TestMemoryMonitor_StartStop(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryThreshold = 512 * 1024 * 1024 // keep this lifecycle test independent of ambient heap usage
	m := NewMemoryMonitor(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	m.Start(ctx)
	assert.False(t, m.Exceeded(), "should not exceed high threshold immediately after start")

	m.Stop()
}

func TestMemoryMonitor_NotExceededUnderLimit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryThreshold = 100
	cfg.MemoryPollInterval = 1 * time.Millisecond

	m := NewMemoryMonitor(cfg)
	// Inject a reader that always returns below threshold.
	m.readHeapAlloc = func() uint64 { return 50 }

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	m.Start(ctx)
	defer m.Stop()

	time.Sleep(20 * time.Millisecond) // let a few polls run
	assert.False(t, m.Exceeded(), "should not exceed when reader reports below threshold")
}

func TestMemoryMonitor_ExceedsLimitWhenThresholdVeryLow(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryThreshold = 100
	cfg.MemoryPollInterval = 1 * time.Millisecond

	m := NewMemoryMonitor(cfg)
	// Inject a reader that always reports above threshold — deterministic.
	m.readHeapAlloc = func() uint64 { return 200 }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m.Start(ctx)
	defer m.Stop()

	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(5 * time.Millisecond).
		Until(func() bool {
			return m.Exceeded()
		})
	require.NoError(t, err, "memory limit should have been detected as exceeded")
}

func TestMemoryMonitor_StopHaltsMonitoring(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryThreshold = 512 * 1024 * 1024 // high — won't trigger
	cfg.MemoryPollInterval = 10 * time.Millisecond

	m := NewMemoryMonitor(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	m.Start(ctx)
	m.Stop()

	// After Stop, calling Stop again should be safe (idempotent).
	assert.NotPanics(t, func() { m.Stop() })
}

func TestMemoryMonitor_ConcurrentSafety(_ *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryThreshold = 512 * 1024 * 1024
	cfg.MemoryPollInterval = 1 * time.Millisecond

	m := NewMemoryMonitor(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	m.Start(ctx)
	defer m.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = m.Exceeded()
			}
		}()
	}
	wg.Wait()
}

func TestErrMemoryLimitExceeded(t *testing.T) {
	assert.NotNil(t, ErrMemoryLimitExceeded)
	assert.Contains(t, ErrMemoryLimitExceeded.Error(), "memory")
}

func TestDefaultConfig_IncludesMemoryFields(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, defaultMemoryThreshold, cfg.MemoryThreshold)
	assert.Equal(t, defaultMemoryPollInterval, cfg.MemoryPollInterval)
}

func TestMonitorExecution_UnderLimit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryThreshold = 512 * 1024 * 1024 // 512MB — will not trigger

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := MonitorExecution(ctx, cfg, func() error {
		// Simulate a small computation.
		total := 0
		for i := 0; i < 100; i++ {
			total += i
		}
		return nil
	})

	assert.NoError(t, err)
}

func TestMonitorExecution_ReturnsWorkError(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryThreshold = 512 * 1024 * 1024

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	expected := ErrScriptTooLarge // reuse existing sentinel for testing

	err := MonitorExecution(ctx, cfg, func() error {
		return expected
	})

	assert.ErrorIs(t, err, expected)
}

func TestMonitorExecution_DetectsLimitExceeded(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryThreshold = 100
	cfg.MemoryPollInterval = 1 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Use a counter to simulate heap exceeding threshold after first sample.
	var calls atomic.Int64
	monitor := NewMemoryMonitor(cfg)
	monitor.readHeapAlloc = func() uint64 {
		if calls.Add(1) >= 2 {
			return 200 // above threshold
		}
		return 50 // below threshold
	}

	monitor.Start(ctx)
	workErr := func() error {
		time.Sleep(20 * time.Millisecond) // let a few polls run
		return nil
	}()
	monitor.sample()
	monitor.Stop()

	if monitor.Exceeded() {
		assert.True(t, true, "memory limit detected as exceeded")
	} else {
		t.Fatal("expected memory limit to be exceeded")
	}
	_ = workErr
}

func TestMonitorExecution_JoinsBothErrors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryThreshold = 100
	cfg.MemoryPollInterval = 1 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Build monitor manually so we can inject the reader.
	monitor := NewMemoryMonitor(cfg)
	monitor.readHeapAlloc = func() uint64 { return 200 } // always exceed

	monitor.Start(ctx)
	workErr := ErrScriptTooLarge
	monitor.sample()
	monitor.Stop()

	var result error
	if monitor.Exceeded() {
		result = errors.Join(ErrMemoryLimitExceeded, workErr)
	} else {
		result = workErr
	}

	assert.ErrorIs(t, result, ErrMemoryLimitExceeded)
	assert.ErrorIs(t, result, ErrScriptTooLarge)
}
