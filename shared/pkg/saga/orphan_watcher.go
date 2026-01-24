// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/env"
	"gorm.io/gorm"
)

// OrphanNotifyChannel is the PostgreSQL channel name for LISTEN/NOTIFY.
const OrphanNotifyChannel = "saga_orphaned"

// DefaultFallbackScanInterval is the default fallback scan interval when LISTEN is not available.
const DefaultFallbackScanInterval = 5 * time.Minute

// OrphanWatcherConfig holds configuration for the orphan watcher.
type OrphanWatcherConfig struct {
	// FallbackScanInterval is how often to scan for orphans when LISTEN fails.
	// Default: 5 minutes (SAGA_ORPHAN_SCAN_INTERVAL)
	FallbackScanInterval time.Duration

	// NotificationDebounce is the minimum time between scans triggered by notifications.
	// Prevents thundering herd when multiple pods crash simultaneously.
	// Default: 500ms (SAGA_ORPHAN_DEBOUNCE_MS)
	NotificationDebounce time.Duration
}

// NewOrphanWatcherConfig creates an OrphanWatcherConfig from environment variables.
func NewOrphanWatcherConfig() *OrphanWatcherConfig {
	return &OrphanWatcherConfig{
		FallbackScanInterval: env.GetEnvAsDuration("SAGA_ORPHAN_SCAN_INTERVAL", DefaultFallbackScanInterval),
		NotificationDebounce: env.GetEnvAsDuration("SAGA_ORPHAN_DEBOUNCE_MS", 500*time.Millisecond),
	}
}

// OrphanWatcher listens for PostgreSQL NOTIFY events when sagas become orphaned
// (i.e., when claimed_by_pod is set to NULL). This enables fast-path resumption
// with a target latency of <10 seconds from pod crash to saga resume.
//
// When LISTEN fails or is unavailable, the watcher falls back to periodic scanning
// every 5 minutes (configurable).
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

	// listener is the PostgreSQL LISTEN connection
	listener *pq.Listener

	// state management
	mu       sync.Mutex
	running  bool
	done     chan struct{}
	wg       sync.WaitGroup
	stopOnce sync.Once

	// debounce state
	lastScan   time.Time
	scanMu     sync.Mutex
	scanQueued bool
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

// WithFallbackScanInterval sets the fallback periodic scan interval.
func WithFallbackScanInterval(interval time.Duration) OrphanWatcherOption {
	return func(w *OrphanWatcher) {
		if interval > 0 {
			w.watchConfig.FallbackScanInterval = interval
		}
	}
}

// WithNotificationDebounce sets the minimum time between notification-triggered scans.
func WithNotificationDebounce(duration time.Duration) OrphanWatcherOption {
	return func(w *OrphanWatcher) {
		if duration > 0 {
			w.watchConfig.NotificationDebounce = duration
		}
	}
}

// NewOrphanWatcher creates a new OrphanWatcher.
//
// Parameters:
//   - db: GORM database connection (used for claims and to extract connection string)
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

// Start begins the orphan watching process.
// It attempts to set up PostgreSQL LISTEN/NOTIFY, falling back to periodic scanning
// if LISTEN is not available.
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
		"fallback_interval", w.watchConfig.FallbackScanInterval,
		"debounce", w.watchConfig.NotificationDebounce,
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
	if w.listener != nil {
		_ = w.listener.Close()
		w.listener = nil
	}
	w.running = false
	w.mu.Unlock()

	w.logger.Info("orphan watcher stopped")
}

// run is the main event loop.
func (w *OrphanWatcher) run(ctx context.Context) {
	defer func() {
		w.mu.Lock()
		w.running = false
		w.mu.Unlock()
		w.wg.Done()
	}()

	// Try to set up LISTEN/NOTIFY
	listenOK := w.setupListener(ctx)

	// Set up fallback periodic ticker
	ticker := time.NewTicker(w.watchConfig.FallbackScanInterval)
	defer ticker.Stop()

	// Initial scan
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
			// Periodic fallback scan
			w.logger.Debug("periodic fallback scan triggered")
			w.performScan(ctx)

		default:
			// Check for notifications (non-blocking)
			if listenOK && w.listener != nil {
				select {
				case notification := <-w.listener.Notify:
					if notification != nil {
						w.handleNotification(ctx, notification)
					}
				case <-ctx.Done():
					return
				case <-w.done:
					return
				case <-time.After(100 * time.Millisecond):
					// Continue loop
				}
			} else {
				// No listener, just wait a bit before next iteration
				select {
				case <-ctx.Done():
					return
				case <-w.done:
					return
				case <-time.After(100 * time.Millisecond):
					// Continue loop
				}
			}
		}
	}
}

// setupListener attempts to create a PostgreSQL LISTEN connection.
// Returns true if successful, false if LISTEN is not available.
func (w *OrphanWatcher) setupListener(ctx context.Context) bool {
	// Get connection string from GORM
	sqlDB, err := w.db.DB()
	if err != nil {
		w.logger.Warn("failed to get sql.DB from GORM, falling back to periodic scan",
			"error", err)
		return false
	}

	// Get DSN - we need to extract it from the existing connection
	// Since GORM doesn't expose the DSN directly, we'll get it from an env var
	dsn := env.GetEnvOrDefault("DATABASE_URL", "")
	if dsn == "" {
		// Try to construct from individual components
		dsn = constructDSNFromEnv()
	}

	if dsn == "" {
		w.logger.Warn("DATABASE_URL not set, falling back to periodic scan")
		return false
	}

	// Create pq.Listener with reconnection logic
	minReconn := 10 * time.Second
	maxReconn := time.Minute

	listener := pq.NewListener(dsn, minReconn, maxReconn, func(ev pq.ListenerEventType, err error) {
		switch ev {
		case pq.ListenerEventConnected:
			w.logger.Info("PostgreSQL LISTEN connected")
		case pq.ListenerEventDisconnected:
			w.logger.Warn("PostgreSQL LISTEN disconnected", "error", err)
		case pq.ListenerEventReconnected:
			w.logger.Info("PostgreSQL LISTEN reconnected")
		case pq.ListenerEventConnectionAttemptFailed:
			w.logger.Warn("PostgreSQL LISTEN connection attempt failed", "error", err)
		}
	})

	// Subscribe to orphan channel
	if err := listener.Listen(OrphanNotifyChannel); err != nil {
		w.logger.Warn("failed to LISTEN on saga_orphaned channel, falling back to periodic scan",
			"error", err)
		_ = listener.Close()
		return false
	}

	// Ping to verify connection
	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := listener.Ping(); err != nil {
		w.logger.Warn("failed to ping LISTEN connection, falling back to periodic scan",
			"error", err)
		_ = listener.Close()
		return false
	}
	_ = ctx2 // suppress unused variable warning

	w.mu.Lock()
	w.listener = listener
	w.mu.Unlock()

	_ = sqlDB // suppress unused variable warning

	w.logger.Info("PostgreSQL LISTEN/NOTIFY enabled for orphan detection",
		"channel", OrphanNotifyChannel)
	return true
}

// constructDSNFromEnv builds a DSN from common environment variables.
func constructDSNFromEnv() string {
	host := env.GetEnvOrDefault("DB_HOST", "")
	port := env.GetEnvOrDefault("DB_PORT", "5432")
	user := env.GetEnvOrDefault("DB_USER", "")
	password := env.GetEnvOrDefault("DB_PASSWORD", "")
	dbname := env.GetEnvOrDefault("DB_NAME", "")
	sslmode := env.GetEnvOrDefault("DB_SSLMODE", "disable")

	if host == "" || user == "" || dbname == "" {
		return ""
	}

	dsn := "host=" + host + " port=" + port + " user=" + user
	if password != "" {
		dsn += " password=" + password
	}
	dsn += " dbname=" + dbname + " sslmode=" + sslmode

	return dsn
}

// handleNotification processes an incoming NOTIFY event.
func (w *OrphanWatcher) handleNotification(ctx context.Context, notification *pq.Notification) {
	w.logger.Debug("received orphan notification",
		"saga_id", notification.Extra,
		"channel", notification.Channel,
	)

	RecordOrphanNotificationReceived()

	// Debounce: don't scan too frequently
	w.scanMu.Lock()
	timeSinceLastScan := time.Since(w.lastScan)
	if timeSinceLastScan < w.watchConfig.NotificationDebounce {
		// Too soon - queue a scan for later if not already queued
		if !w.scanQueued {
			w.scanQueued = true
			delay := w.watchConfig.NotificationDebounce - timeSinceLastScan
			// Intentionally not passing ctx - the goroutine will use context.Background()
			// since the original ctx may be cancelled by the time the debounce delay completes.
			//nolint:contextcheck // Intentional: uses fresh context.Background() for delayed scan
			go func() {
				// Wait for debounce delay or shutdown signal
				select {
				case <-time.After(delay):
				case <-w.done:
					w.scanMu.Lock()
					w.scanQueued = false
					w.scanMu.Unlock()
					return
				}
				w.scanMu.Lock()
				w.scanQueued = false
				w.scanMu.Unlock()
				// Check if watcher is still running before scanning
				select {
				case <-w.done:
					return
				default:
					// Use fresh context since original ctx may be cancelled
					w.performScan(context.Background())
				}
			}()
		}
		w.scanMu.Unlock()
		return
	}
	w.scanMu.Unlock()

	w.performScan(ctx)
}

// performScan executes an orphan claiming scan.
func (w *OrphanWatcher) performScan(ctx context.Context) {
	w.scanMu.Lock()
	w.lastScan = time.Now()
	w.scanMu.Unlock()

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
