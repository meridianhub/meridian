package sandbox

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// defaultMemoryThreshold is the default HeapAlloc limit (10MB).
	// Measurements are process-wide, not per-script (see Known Limitations below).
	defaultMemoryThreshold = uint64(10 * 1024 * 1024)

	// defaultMemoryPollInterval is how often HeapAlloc is sampled.
	// 10ms balances responsiveness (catching short-lived spikes before a script
	// finishes) against the ~50-200µs stop-the-world cost of ReadMemStats.
	// At 10ms this adds roughly 1-2% CPU overhead — acceptable for sandboxed
	// script execution that is itself bounded by the execution timeout.
	defaultMemoryPollInterval = 10 * time.Millisecond
)

// ErrMemoryLimitExceeded is returned when the Starlark sandbox exceeds the
// configured heap-allocation threshold.
var ErrMemoryLimitExceeded = errors.New("memory limit exceeded in sandbox")

// MemoryMonitor periodically samples runtime.MemStats.HeapAlloc and sets an
// exceeded flag when the value surpasses the configured threshold.
//
// # Known Limitations
//
//  1. Global heap measurement — HeapAlloc reflects the entire Go process, not
//     just the Starlark script under evaluation. Other goroutines may contribute
//     to the reading, making the monitor conservative (false positives possible).
//
//  2. Sampling granularity — the monitor polls at the configured interval
//     (default 10ms). Allocations that spike and are freed within a single
//     poll window may go undetected.
//
//  3. GC timing fluctuations — a garbage-collection cycle can cause HeapAlloc
//     to drop temporarily between polls, potentially masking a true overage.
type MemoryMonitor struct {
	threshold     uint64
	pollInterval  time.Duration
	exceeded      atomic.Bool
	stopOnce      sync.Once
	stopCh        chan struct{}
	readHeapAlloc func() uint64 // injectable for testing; nil uses runtime.ReadMemStats
}

// NewMemoryMonitor constructs a MemoryMonitor from a sandbox Config.
// If Config.MemoryThreshold is zero the default (10MB) is used.
// If Config.MemoryPollInterval is non-positive the default (10ms) is used.
func NewMemoryMonitor(cfg Config) *MemoryMonitor {
	threshold := cfg.MemoryThreshold
	if threshold == 0 {
		threshold = defaultMemoryThreshold
	}

	interval := cfg.MemoryPollInterval
	if interval <= 0 {
		interval = defaultMemoryPollInterval
	}

	return &MemoryMonitor{
		threshold:    threshold,
		pollInterval: interval,
		stopCh:       make(chan struct{}),
	}
}

// Start begins the monitoring goroutine.
// The goroutine exits when Stop is called or ctx is cancelled.
func (m *MemoryMonitor) Start(ctx context.Context) {
	go m.run(ctx)
}

// Stop halts the monitoring goroutine. It is safe to call multiple times.
func (m *MemoryMonitor) Stop() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
}

// Exceeded reports whether the heap-allocation threshold has been breached
// at any point since Start was called.
func (m *MemoryMonitor) Exceeded() bool {
	return m.exceeded.Load()
}

// sample performs one synchronous HeapAlloc measurement and updates the
// exceeded flag if the threshold is surpassed.
func (m *MemoryMonitor) sample() {
	heap := m.heapAlloc()
	if heap > m.threshold {
		m.exceeded.Store(true)
	}
}

// heapAlloc returns the current HeapAlloc value. It uses the injected
// readHeapAlloc function when set (tests), falling back to runtime.ReadMemStats.
func (m *MemoryMonitor) heapAlloc() uint64 {
	if m.readHeapAlloc != nil {
		return m.readHeapAlloc()
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// run is the monitoring goroutine body.
func (m *MemoryMonitor) run(ctx context.Context) {
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.sample()
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// MonitorOption configures optional behavior for MonitorExecution.
type MonitorOption func(*MemoryMonitor)

// WithHeapReader injects a custom heap-allocation reader, replacing the
// default runtime.ReadMemStats call. Intended for deterministic testing.
func WithHeapReader(fn func() uint64) MonitorOption {
	return func(m *MemoryMonitor) {
		m.readHeapAlloc = fn
	}
}

// MonitorExecution runs work under active memory monitoring.
// It starts the monitor, calls work(), performs a final synchronous sample to
// catch any breach near execution end, then stops the monitor.
// If the memory threshold is exceeded during execution,
// ErrMemoryLimitExceeded is returned (even if work returned nil).
// Errors from work take precedence only when no memory breach is detected.
func MonitorExecution(ctx context.Context, cfg Config, work func() error, opts ...MonitorOption) error {
	monitor := NewMemoryMonitor(cfg)
	for _, opt := range opts {
		opt(monitor)
	}
	monitor.Start(ctx)
	defer monitor.Stop()

	workErr := work()

	// Final synchronous sample catches breaches that fell between the last
	// ticker tick and work completion.
	monitor.sample()
	monitor.Stop()

	if monitor.Exceeded() {
		// Preserve the work error alongside the memory breach so callers
		// retain diagnostics (e.g. which Starlark line was executing).
		return errors.Join(ErrMemoryLimitExceeded, workErr)
	}
	return workErr
}
