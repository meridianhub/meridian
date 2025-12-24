package audit

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"
)

// Errors are defined in errors.go for centralized error management.
// See: ErrMaxRetriesExceeded, ErrWorkerShutdown, ErrBatchProcessingFailed, ErrSimulatedProcessingFailure

const (
	// Default configuration values
	defaultBatchSize     = 100
	defaultPollInterval  = 5 * time.Second
	defaultMaxRetries    = 3
	defaultProcessingAge = 5 * time.Minute // Consider 'processing' entries stuck after this duration

	// Adaptive polling configuration
	defaultMinPollInterval = 100 * time.Millisecond // Minimum poll interval when busy
	defaultMaxPollInterval = 30 * time.Second       // Maximum poll interval when idle
)

// Status and table name constants are defined in status.go for centralized management.
// Use StatusPending, StatusProcessing, StatusFailed, StatusCompleted for status values.
// Use TableAuditOutbox, TableAuditLog for table names.

// Worker is a background processor that moves audit records from the outbox to the audit log.
// It implements graceful shutdown and parallel processing within batches.
// Each worker processes a single schema, supporting the per-service audit worker pattern (ADR-0020).
type Worker struct {
	db           *gorm.DB
	schema       string // PostgreSQL schema name (e.g., "party_audit", "current_account_audit")
	batchSize    int
	pollInterval time.Duration
	maxRetries   int
	logger       *slog.Logger
	shutdown     chan struct{}
	shutdownOnce sync.Once
	wg           sync.WaitGroup

	// Adaptive polling configuration
	adaptivePolling bool          // Enable adaptive poll intervals based on load
	minPollInterval time.Duration // Minimum poll interval (when busy)
	maxPollInterval time.Duration // Maximum poll interval (when idle)
	emptyPollCount  int           // Consecutive empty polls (for adaptive interval calculation)
}

// WorkerOption is a functional option for configuring a Worker.
type WorkerOption func(*Worker)

// WithBatchSize sets the maximum number of entries to process per batch.
// Default: 100
func WithBatchSize(size int) WorkerOption {
	return func(w *Worker) {
		if size > 0 {
			w.batchSize = size
		}
	}
}

// WithPollInterval sets the base polling interval.
// Default: 5 seconds
func WithPollInterval(interval time.Duration) WorkerOption {
	return func(w *Worker) {
		if interval > 0 {
			w.pollInterval = interval
		}
	}
}

// WithMaxRetries sets the maximum number of retry attempts for failed entries.
// Default: 3
func WithMaxRetries(retries int) WorkerOption {
	return func(w *Worker) {
		if retries >= 0 {
			w.maxRetries = retries
		}
	}
}

// WithAdaptivePolling enables adaptive poll intervals that adjust based on load.
// When the outbox is empty, the interval increases (up to maxInterval).
// When entries are present, the interval decreases (down to minInterval).
// This reduces database load during idle periods while maintaining responsiveness under load.
func WithAdaptivePolling(minInterval, maxInterval time.Duration) WorkerOption {
	return func(w *Worker) {
		w.adaptivePolling = true
		if minInterval > 0 {
			w.minPollInterval = minInterval
		}
		if maxInterval > 0 {
			w.maxPollInterval = maxInterval
		}
	}
}

// NewAuditWorker creates a new audit worker for a specific service schema.
// The worker processes audit outbox entries in batches and moves them to the audit log.
// Per ADR-0020, each service runs its own embedded worker processing its local schema.
//
// Parameters:
//   - db: GORM database connection
//   - schema: PostgreSQL schema name (e.g., "party_audit", "current_account_audit")
//   - logger: Structured logger (uses slog.Default() if nil)
//   - opts: Optional functional options for configuration
//
// Returns a configured Worker ready to start processing.
//
// Example:
//
//	// Basic usage with defaults
//	auditWorker := audit.NewAuditWorker(db, "party_audit", logger)
//
//	// With custom configuration
//	auditWorker := audit.NewAuditWorker(db, "party_audit", logger,
//	    audit.WithBatchSize(200),
//	    audit.WithPollInterval(10*time.Second),
//	    audit.WithAdaptivePolling(100*time.Millisecond, 30*time.Second),
//	)
//	auditWorker.Start(ctx)
func NewAuditWorker(db *gorm.DB, schema string, logger *slog.Logger, opts ...WorkerOption) *Worker {
	if logger == nil {
		logger = slog.Default()
	}

	w := &Worker{
		db:              db,
		schema:          schema,
		batchSize:       defaultBatchSize,
		pollInterval:    defaultPollInterval,
		maxRetries:      defaultMaxRetries,
		minPollInterval: defaultMinPollInterval,
		maxPollInterval: defaultMaxPollInterval,
		logger:          logger.With("schema", schema),
		shutdown:        make(chan struct{}),
	}

	// Apply functional options
	for _, opt := range opts {
		opt(w)
	}

	return w
}

// Start begins background processing of audit outbox entries.
// This method spawns a goroutine and returns immediately.
// The worker will continue processing until Stop() is called or the context is cancelled.
//
// The worker:
// - Polls the outbox table at regular intervals
// - Processes entries in batches
// - Handles retries and failure states
// - Respects context cancellation for graceful shutdown
func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	go w.run(ctx)
	// Note: schema is already in logger context from NewAuditWorker
	w.logger.Info("audit worker started",
		"batch_size", w.batchSize,
		"poll_interval", w.pollInterval,
		"max_retries", w.maxRetries)
}

// Stop initiates graceful shutdown of the worker.
// It signals the worker to stop and waits for the current batch to complete.
// Safe to call multiple times - subsequent calls will block until shutdown completes.
func (w *Worker) Stop() {
	w.shutdownOnce.Do(func() {
		w.logger.Info("audit worker stopping")
		close(w.shutdown)
	})
	w.wg.Wait()
	w.logger.Info("audit worker stopped")
}

// outboxTable returns the schema-qualified audit_outbox table name.
func (w *Worker) outboxTable() string {
	if w.schema == "" {
		return TableAuditOutbox
	}
	return w.schema + "." + TableAuditOutbox
}

// auditLogTable returns the schema-qualified audit_log table name.
func (w *Worker) auditLogTable() string {
	if w.schema == "" {
		return TableAuditLog
	}
	return w.schema + "." + TableAuditLog
}

// run is the main processing loop that polls the outbox and processes batches.
// It runs until the context is cancelled or the shutdown channel is closed.
func (w *Worker) run(ctx context.Context) {
	defer w.wg.Done()

	currentInterval := w.pollInterval
	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()

	w.logger.Info("audit worker processing loop started")

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("audit worker context cancelled")
			return
		case <-w.shutdown:
			w.logger.Info("audit worker shutdown signal received")
			return
		case <-ticker.C:
			// Reset stuck 'processing' entries before processing new batch
			if err := w.resetStuckEntries(ctx); err != nil {
				w.logger.Error("failed to reset stuck entries",
					"error", err)
			}

			// Process a batch of pending entries
			processedCount, err := w.processBatchWithCount(ctx)
			if err != nil {
				w.logger.Error("batch processing failed",
					"error", err)
			}

			// Adjust poll interval based on load (if adaptive polling is enabled)
			if w.adaptivePolling {
				newInterval := w.calculateAdaptiveInterval(processedCount)
				if newInterval != currentInterval {
					currentInterval = newInterval
					ticker.Reset(currentInterval)
					RecordPollInterval(w.schema, currentInterval.Seconds())
					w.logger.Debug("adaptive poll interval adjusted",
						"interval", currentInterval,
						"empty_polls", w.emptyPollCount)
				}
			}
		}
	}
}

// calculateAdaptiveInterval determines the next poll interval based on recent activity.
// When entries are being processed, the interval decreases toward minPollInterval.
// When the outbox is empty, the interval gradually increases toward maxPollInterval.
func (w *Worker) calculateAdaptiveInterval(processedCount int) time.Duration {
	if processedCount > 0 {
		// Work found - reset to minimum interval for responsiveness
		w.emptyPollCount = 0
		RecordEmptyPolls(w.schema, 0)
		return w.minPollInterval
	}

	// No work - increase interval exponentially (up to max)
	w.emptyPollCount++
	RecordEmptyPolls(w.schema, w.emptyPollCount)

	// Exponential backoff: double the base interval for each consecutive empty poll
	// Cap at maxPollInterval
	backoffMultiplier := 1 << min(w.emptyPollCount, 10) // Cap multiplier at 1024x
	newInterval := time.Duration(backoffMultiplier) * w.minPollInterval

	if newInterval > w.maxPollInterval {
		return w.maxPollInterval
	}
	return newInterval
}

// resetStuckEntries resets entries that have been in 'processing' state for too long.
// This handles cases where the worker crashed or was killed while processing.
func (w *Worker) resetStuckEntries(ctx context.Context) error {
	stuckThreshold := time.Now().Add(-defaultProcessingAge)

	result := w.db.WithContext(ctx).
		Table(w.outboxTable()).
		Where("status = ?", StatusProcessing).
		Where("created_at < ?", stuckThreshold).
		Update("status", StatusPending)

	if result.Error != nil {
		return fmt.Errorf("failed to reset stuck entries: %w", result.Error)
	}

	if result.RowsAffected > 0 {
		w.logger.Warn("reset stuck processing entries",
			"count", result.RowsAffected,
			"threshold", stuckThreshold)
	}

	return nil
}

// processBatch fetches and processes a batch of pending audit entries.
// Entries are processed sequentially within the batch to maintain order.
// Failed entries are marked for retry or moved to failed state.
//
// Returns an error if the database query fails, but continues processing
// even if individual entries fail.
func (w *Worker) processBatch(ctx context.Context) error {
	_, err := w.processBatchWithCount(ctx)
	return err
}

// processBatchWithCount fetches and processes a batch of pending audit entries,
// returning the number of successfully processed entries along with any error.
// This is useful for adaptive polling to know if work was done.
func (w *Worker) processBatchWithCount(ctx context.Context) (int, error) {
	start := time.Now()
	defer func() {
		RecordProcessingDuration(time.Since(start).Seconds())
	}()

	var entries []AuditOutbox

	// Fetch pending entries, ordered by creation time for FIFO processing
	result := w.db.WithContext(ctx).
		Table(w.outboxTable()).
		Where("status = ?", StatusPending).
		Order("created_at ASC").
		Limit(w.batchSize).
		Find(&entries)

	if result.Error != nil {
		return 0, fmt.Errorf("failed to fetch pending entries: %w", result.Error)
	}

	// Record batch size and outbox depth metrics
	RecordBatchSize(len(entries))

	var pendingCount int64
	if err := w.db.WithContext(ctx).
		Table(w.outboxTable()).
		Where("status = ?", StatusPending).
		Count(&pendingCount).Error; err == nil {
		RecordOutboxDepthBySchema(w.schema, int(pendingCount))
	}

	if len(entries) == 0 {
		// No entries to process, this is normal
		return 0, nil
	}

	w.logger.Info("processing audit batch",
		"count", len(entries))

	// Track failures for aggregate reporting
	var processedCount, failedCount int

	// Process entries sequentially to maintain order
	for i := range entries {
		entry := &entries[i]

		if err := w.processEntry(ctx, entry); err != nil {
			w.logger.Error("failed to process audit entry",
				"entry_id", entry.ID,
				"table", entry.Table,
				"operation", entry.Operation,
				"record_id", entry.RecordID,
				"retry_count", entry.RetryCount,
				"error", err)
			failedCount++
		} else {
			processedCount++
		}
	}

	w.logger.Info("batch processing completed",
		"processed", processedCount,
		"failed", failedCount,
		"total", len(entries))

	if failedCount > 0 {
		return processedCount, fmt.Errorf("%w: %d failures out of %d entries", ErrBatchProcessingFailed, failedCount, len(entries))
	}

	return processedCount, nil
}

// processEntry processes a single audit outbox entry.
// It updates the entry status to 'processing', performs the audit operation,
// and updates the final status based on the result.
//
// On success: Status is set to 'completed'
// On failure with retries remaining: Status is set back to 'pending', RetryCount incremented
// On failure with retries exhausted: Status is set to 'failed'
func (w *Worker) processEntry(ctx context.Context, entry *AuditOutbox) error {
	// Record entry age (time from creation to processing)
	entryAge := time.Since(entry.CreatedAt).Seconds()
	RecordEntryAge(entryAge)

	// Mark as processing
	if err := w.updateStatus(ctx, entry, StatusProcessing); err != nil {
		return fmt.Errorf("failed to mark entry as processing: %w", err)
	}

	// Insert audit log entry into permanent audit_log table
	err := w.insertAuditLog(ctx, entry)
	if err != nil {
		return w.handleProcessingError(ctx, entry, err)
	}

	// Success - mark as completed
	if err := w.updateStatus(ctx, entry, StatusCompleted); err != nil {
		return fmt.Errorf("failed to mark entry as completed: %w", err)
	}

	// Record successful processing
	RecordProcessedBySchema(w.schema)

	w.logger.Debug("audit entry processed successfully",
		"entry_id", entry.ID,
		"table", entry.Table,
		"operation", entry.Operation,
		"record_id", entry.RecordID)

	return nil
}

// insertAuditLog creates a permanent audit log entry from an outbox entry.
// This completes the async audit flow: business operation → audit_outbox → worker → audit_log.
//
// The function copies all relevant fields from the outbox entry to the audit log,
// excluding worker-specific fields (status, retry_count, last_error).
func (w *Worker) insertAuditLog(ctx context.Context, entry *AuditOutbox) error {
	auditLog := &AuditLog{
		Table:         entry.Table,
		Operation:     entry.Operation,
		RecordID:      entry.RecordID,
		OldValues:     entry.OldValues,
		NewValues:     entry.NewValues,
		ChangedBy:     entry.ChangedBy,
		TransactionID: entry.TransactionID,
		ClientIP:      entry.ClientIP,
		UserAgent:     entry.UserAgent,
		CreatedAt:     time.Now(),
	}

	if err := w.db.WithContext(ctx).Table(w.auditLogTable()).Create(auditLog).Error; err != nil {
		return fmt.Errorf("failed to insert audit log entry: %w", err)
	}

	return nil
}

// handleProcessingError handles failures during entry processing.
// It implements retry logic with exponential backoff and failure state management.
func (w *Worker) handleProcessingError(ctx context.Context, entry *AuditOutbox, processingErr error) error {
	entry.RetryCount++
	errorMsg := processingErr.Error()
	entry.LastError = &errorMsg

	// Check if we've exceeded max retries
	if entry.RetryCount >= w.maxRetries {
		entry.Status = StatusFailed
		// Record failed entry (retries exhausted)
		RecordFailedBySchema(w.schema)
		w.logger.Error("audit entry moved to failed state",
			"entry_id", entry.ID,
			"table", entry.Table,
			"operation", entry.Operation,
			"record_id", entry.RecordID,
			"retry_count", entry.RetryCount,
			"error", processingErr)
	} else {
		// Retry available - set back to pending
		entry.Status = StatusPending
		w.logger.Warn("audit entry marked for retry",
			"entry_id", entry.ID,
			"table", entry.Table,
			"operation", entry.Operation,
			"record_id", entry.RecordID,
			"retry_count", entry.RetryCount,
			"max_retries", w.maxRetries,
			"error", processingErr)
	}

	// Update the entry with new status and retry information
	result := w.db.WithContext(ctx).
		Table(w.outboxTable()).
		Where("id = ?", entry.ID).
		Updates(map[string]interface{}{
			"status":      entry.Status,
			"retry_count": entry.RetryCount,
			"last_error":  entry.LastError,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to update entry after processing error: %w", result.Error)
	}

	return processingErr
}

// updateStatus updates the status of an audit entry.
// This is a helper method to encapsulate status updates with error handling.
func (w *Worker) updateStatus(ctx context.Context, entry *AuditOutbox, status string) error {
	entry.Status = status
	result := w.db.WithContext(ctx).
		Table(w.outboxTable()).
		Where("id = ?", entry.ID).
		Update("status", status)

	if result.Error != nil {
		return fmt.Errorf("failed to update status to %s: %w", status, result.Error)
	}

	return nil
}
