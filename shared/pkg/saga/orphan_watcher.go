// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/meridianhub/meridian/shared/platform/env"
	"gorm.io/gorm"
)

// DefaultScanInterval is the default interval for scanning orphaned sagas.
// This provides a balance between timely detection and database load.
// For faster detection, configure SAGA_ORPHAN_SCAN_INTERVAL to a lower value.
const DefaultScanInterval = 10 * time.Second

// OrphanWatcherConfig holds configuration for the orphan watcher.
type OrphanWatcherConfig struct {
	// ScanInterval is how often to scan for orphaned sagas.
	// Default: 10 seconds (SAGA_ORPHAN_SCAN_INTERVAL)
	ScanInterval time.Duration
}

// NewOrphanWatcherConfig creates an OrphanWatcherConfig from environment variables.
func NewOrphanWatcherConfig() *OrphanWatcherConfig {
	return &OrphanWatcherConfig{
		ScanInterval: env.GetEnvAsDuration("SAGA_ORPHAN_SCAN_INTERVAL", DefaultScanInterval),
	}
}

// OrphanWatcher periodically scans for orphaned sagas (sagas with expired leases
// or NULL claimed_by_pod) and claims them for the current pod.
//
// This implements a polling-based approach compatible with CockroachDB, which
// does not support PostgreSQL's LISTEN/NOTIFY mechanism.
//
// Usage:
//
//	watcher := saga.NewOrphanWatcher(db, claimConfig, logger)
//	watcher.Start(ctx)
//	defer watcher.Stop()
type OrphanWatcher struct {
	db           *gorm.DB
	claimConfig  *ClaimConfig
	watchConfig  *OrphanWatcherConfig
	claimService *ClaimService
	logger       *slog.Logger

	// callback is called after each scan (for testing)
	callback func()

	// state management
	mu       sync.Mutex
	running  bool
	done     chan struct{}
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// OrphanWatcherOption is a functional option for configuring an OrphanWatcher.
type OrphanWatcherOption func(*OrphanWatcher)

// WithOrphanScanCallback sets a callback function called after each orphan scan.
// Useful for testing to track scan frequency.
func WithOrphanScanCallback(callback func()) OrphanWatcherOption {
	return func(w *OrphanWatcher) {
		w.callback = callback
	}
}

// WithScanInterval sets the periodic scan interval.
func WithScanInterval(interval time.Duration) OrphanWatcherOption {
	return func(w *OrphanWatcher) {
		if interval > 0 {
			w.watchConfig.ScanInterval = interval
		}
	}
}

// NewOrphanWatcher creates a new OrphanWatcher.
//
// Parameters:
//   - db: GORM database connection
//   - claimConfig: Configuration for the ClaimService (includes PodID, batch size, etc.)
//   - logger: Structured logger (uses slog.Default() if nil)
//   - opts: Optional functional options
func NewOrphanWatcher(db *gorm.DB, claimConfig *ClaimConfig, logger *slog.Logger, opts ...OrphanWatcherOption) *OrphanWatcher {
	if logger == nil {
		logger = slog.Default()
	}

	w := &OrphanWatcher{
		db:           db,
		claimConfig:  claimConfig,
		watchConfig:  NewOrphanWatcherConfig(),
		claimService: NewClaimService(db, claimConfig).WithLogger(logger),
		logger:       logger.With("component", "orphan_watcher"),
		done:         make(chan struct{}),
	}

	for _, opt := range opts {
		opt(w)
	}

	return w
}

// Start begins the orphan watching process with periodic scanning.
//
// It is safe to call Start multiple times, but only the first call will have effect.
func (w *OrphanWatcher) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running {
		w.logger.Debug("orphan watcher already running, ignoring Start() call")
		return
	}

	w.running = true
	w.wg.Add(1)

	go w.run(ctx)

	w.logger.Info("orphan watcher started",
		"scan_interval", w.watchConfig.ScanInterval,
		"pod_id", w.claimConfig.PodID,
	)
}

// Stop signals the watcher to stop and waits for goroutines to exit.
// It is safe to call Stop multiple times.
func (w *OrphanWatcher) Stop() {
	w.stopOnce.Do(func() {
		w.logger.Debug("stopping orphan watcher")
		close(w.done)
	})

	w.wg.Wait()

	w.mu.Lock()
	w.running = false
	w.mu.Unlock()

	w.logger.Info("orphan watcher stopped")
}

// run is the main event loop that periodically scans for orphaned sagas.
func (w *OrphanWatcher) run(ctx context.Context) {
	defer func() {
		w.mu.Lock()
		w.running = false
		w.mu.Unlock()
		w.wg.Done()
	}()

	// Set up periodic ticker
	ticker := time.NewTicker(w.watchConfig.ScanInterval)
	defer ticker.Stop()

	// Initial scan on startup
	w.performScan(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Debug("orphan watcher stopping due to context cancellation")
			return

		case <-w.done:
			w.logger.Debug("orphan watcher stopping due to Stop() call")
			return

		case <-ticker.C:
			w.performScan(ctx)
		}
	}
}

// performScan executes an orphan claiming scan.
func (w *OrphanWatcher) performScan(ctx context.Context) {
	start := time.Now()

	claimedIDs, err := w.claimService.ClaimOrphanedSagas(ctx)
	if err != nil {
		w.logger.Error("failed to claim orphaned sagas",
			"error", err,
		)
		RecordOrphanScanError()
	} else if len(claimedIDs) > 0 {
		w.logger.Info("claimed orphaned sagas",
			"count", len(claimedIDs),
			"saga_ids", claimedIDs,
			"duration", time.Since(start),
		)
		RecordOrphansClaimed(len(claimedIDs))
	} else {
		w.logger.Debug("no orphaned sagas found",
			"duration", time.Since(start),
		)
	}

	RecordOrphanScan(time.Since(start))

	// Call test callback if set
	if w.callback != nil {
		w.callback()
	}
}
