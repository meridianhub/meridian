package sandbox

import (
	"context"
	"sync"
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
	cfg.MemoryThreshold = 512 * 1024 * 1024
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
	m.readHeapAlloc = func() uint64 { return 50 }

	// Verify directly via sample — no timing dependency.
	m.sample()
	assert.False(t, m.Exceeded(), "should not exceed when reader reports below threshold")
}

func TestMemoryMonitor_ExceedsLimitWhenThresholdVeryLow(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryThreshold = 100
	cfg.MemoryPollInterval = 1 * time.Millisecond

	m := NewMemoryMonitor(cfg)
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
	cfg.MemoryThreshold = 512 * 1024 * 1024
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
	cfg.MemoryThreshold = 512 * 1024 * 1024

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := MonitorExecution(ctx, cfg, func() error {
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
	expected := ErrScriptTooLarge

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

	err := MonitorExecution(ctx, cfg, func() error {
		return nil
	}, WithHeapReader(func() uint64 { return 200 }))

	assert.ErrorIs(t, err, ErrMemoryLimitExceeded)
}

func TestMonitorExecution_JoinsBothErrors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryThreshold = 100
	cfg.MemoryPollInterval = 1 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := MonitorExecution(ctx, cfg, func() error {
		return ErrScriptTooLarge
	}, WithHeapReader(func() uint64 { return 200 }))

	assert.ErrorIs(t, err, ErrMemoryLimitExceeded)
	assert.ErrorIs(t, err, ErrScriptTooLarge)
}
