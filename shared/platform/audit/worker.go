package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"
)

var (
	// ErrMaxRetriesExceeded is returned when an entry has exceeded the maximum retry count
	ErrMaxRetriesExceeded = errors.New("max retries exceeded")

	// ErrWorkerShutdown is returned when the worker is shutting down
	ErrWorkerShutdown = errors.New("worker is shutting down")

	// ErrBatchProcessingFailed is returned when batch processing completes with failures
	ErrBatchProcessingFailed = errors.New("batch processing completed with failures")

	// ErrSimulatedProcessingFailure is a test error for simulating processing failures
	ErrSimulatedProcessingFailure = errors.New("simulated processing error")
)

const (
	// Default configuration values
	defaultBatchSize     = 100
	defaultPollInterval  = 5 * time.Second
	defaultMaxRetries    = 3
	defaultProcessingAge = 5 * time.Minute // Consider 'processing' entries stuck after this duration

	// Status values
	statusPending    = "pending"
	statusProcessing = "processing"
	statusFailed     = "failed"
	statusCompleted  = "completed"
)

// Worker is a background processor that moves audit records from the outbox to the audit log.
// It implements graceful shutdown and parallel processing within batches.
type Worker struct {
	db           *gorm.DB
	batchSize    int
	pollInterval time.Duration
	maxRetries   int
	logger       *slog.Logger
	shutdown     chan struct{}
	shutdownOnce sync.Once
	wg           sync.WaitGroup
}

// NewAuditWorker creates a new audit worker with default settings.
// The worker processes audit outbox entries in batches and moves them to the audit log.
//
// Parameters:
//   - db: GORM database connection
//   - logger: Structured logger (uses slog.Default() if nil)
//
// Returns a configured Worker ready to start processing.
func NewAuditWorker(db *gorm.DB, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}

	return &Worker{
		db:           db,
		batchSize:    defaultBatchSize,
		pollInterval: defaultPollInterval,
		maxRetries:   defaultMaxRetries,
		logger:       logger,
		shutdown:     make(chan struct{}),
	}
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

// run is the main processing loop that polls the outbox and processes batches.
// It runs until the context is cancelled or the shutdown channel is closed.
func (w *Worker) run(ctx context.Context) {
	defer w.wg.Done()

	ticker := time.NewTicker(w.pollInterval)
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
			if err := w.processBatch(ctx); err != nil {
				w.logger.Error("batch processing failed",
					"error", err)
			}
		}
	}
}

// resetStuckEntries resets entries that have been in 'processing' state for too long.
// This handles cases where the worker crashed or was killed while processing.
func (w *Worker) resetStuckEntries(ctx context.Context) error {
	stuckThreshold := time.Now().Add(-defaultProcessingAge)

	result := w.db.WithContext(ctx).
		Model(&AuditOutbox{}).
		Where("status = ?", statusProcessing).
		Where("created_at < ?", stuckThreshold).
		Update("status", statusPending)

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
	start := time.Now()
	defer func() {
		RecordProcessingDuration(time.Since(start).Seconds())
	}()

	var entries []AuditOutbox

	// Fetch pending entries, ordered by creation time for FIFO processing
	result := w.db.WithContext(ctx).
		Where("status = ?", statusPending).
		Order("created_at ASC").
		Limit(w.batchSize).
		Find(&entries)

	if result.Error != nil {
		return fmt.Errorf("failed to fetch pending entries: %w", result.Error)
	}

	// Record outbox depth (pending entries count)
	var pendingCount int64
	if err := w.db.WithContext(ctx).
		Model(&AuditOutbox{}).
		Where("status = ?", statusPending).
		Count(&pendingCount).Error; err == nil {
		RecordOutboxDepth(int(pendingCount))
	}

	if len(entries) == 0 {
		// No entries to process, this is normal
		return nil
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
		return fmt.Errorf("%w: %d failures out of %d entries", ErrBatchProcessingFailed, failedCount, len(entries))
	}

	return nil
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
	if err := w.updateStatus(ctx, entry, statusProcessing); err != nil {
		return fmt.Errorf("failed to mark entry as processing: %w", err)
	}

	// Insert audit log entry into permanent audit_log table
	err := w.insertAuditLog(ctx, entry)
	if err != nil {
		return w.handleProcessingError(ctx, entry, err)
	}

	// Success - mark as completed
	if err := w.updateStatus(ctx, entry, statusCompleted); err != nil {
		return fmt.Errorf("failed to mark entry as completed: %w", err)
	}

	// Record successful processing
	RecordProcessed()

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

	if err := w.db.WithContext(ctx).Create(auditLog).Error; err != nil {
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
		entry.Status = statusFailed
		// Record failed entry (retries exhausted)
		RecordFailed()
		w.logger.Error("audit entry moved to failed state",
			"entry_id", entry.ID,
			"table", entry.Table,
			"operation", entry.Operation,
			"record_id", entry.RecordID,
			"retry_count", entry.RetryCount,
			"error", processingErr)
	} else {
		// Retry available - set back to pending
		entry.Status = statusPending
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
	result := w.db.WithContext(ctx).Save(entry)
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
		Model(entry).
		Update("status", status)

	if result.Error != nil {
		return fmt.Errorf("failed to update status to %s: %w", status, result.Error)
	}

	return nil
}
