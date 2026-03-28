// Package worker provides background workers for the position-keeping service.
package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
)

// CompactionConfig holds configuration for the compaction worker.
type CompactionConfig struct {
	// RunInterval specifies how often to run compaction (e.g., 5 minutes)
	RunInterval time.Duration

	// FragmentThreshold is the minimum number of rows to trigger compaction
	// for a given (account_id, instrument_code, bucket_key) combination
	FragmentThreshold int

	// BatchSize is how many buckets to compact per run
	BatchSize int
}

// CompactionWorker is a background worker that consolidates fragmented position
// rows in the append-only positions table.
//
// The position-keeping service uses append-only writes for O(1) constant-time
// inserts. Over time, this creates fragmentation - multiple rows for the same
// (account_id, instrument_code, bucket_key) combination. The compaction worker
// runs periodically to consolidate these rows while preserving the audit trail.
type CompactionWorker struct {
	pool    *pgxpool.Pool
	config  CompactionConfig
	logger  *slog.Logger
	done    chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	running bool
	stopped bool // guards wg.Add/Wait race
}

// Errors returned by the compaction worker.
var (
	ErrNilPool                  = errors.New("pool cannot be nil")
	ErrNilLogger                = errors.New("logger cannot be nil")
	ErrInvalidRunInterval       = errors.New("run interval must be greater than zero")
	ErrInvalidFragmentThreshold = errors.New("fragment threshold must be greater than zero")
	ErrInvalidBatchSize         = errors.New("batch size must be greater than zero")
	ErrWorkerAlreadyRunning     = errors.New("worker is already running")
)

// FragmentedBucket represents a bucket that has fragmentation above the threshold.
type FragmentedBucket struct {
	AccountID      string
	InstrumentCode string
	BucketKey      string
	RowCount       int64
}

// PositionRow represents a single position row for compaction.
type PositionRow struct {
	ID          uuid.UUID
	Amount      decimal.Decimal
	Dimension   string
	Attributes  map[string]string
	ReferenceID uuid.UUID
	CreatedAt   time.Time
}

// NewCompactionWorker creates a new compaction worker.
//
// Parameters:
//   - pool: PostgreSQL connection pool
//   - config: Worker configuration
//   - logger: Structured logger
//
// Returns an error if any required parameter is invalid.
func NewCompactionWorker(
	pool *pgxpool.Pool,
	config CompactionConfig,
	logger *slog.Logger,
) (*CompactionWorker, error) {
	if pool == nil {
		return nil, ErrNilPool
	}
	if logger == nil {
		return nil, ErrNilLogger
	}
	if config.RunInterval <= 0 {
		return nil, ErrInvalidRunInterval
	}
	if config.FragmentThreshold <= 0 {
		return nil, ErrInvalidFragmentThreshold
	}
	if config.BatchSize <= 0 {
		return nil, ErrInvalidBatchSize
	}

	return &CompactionWorker{
		pool:   pool,
		config: config,
		logger: logger.With("component", "compaction_worker"),
		done:   make(chan struct{}),
	}, nil
}

// Start begins the background compaction loop.
// It runs until ctx is cancelled or Stop() is called.
// The method blocks and should be run in a separate goroutine.
//
// Returns ErrWorkerAlreadyRunning if Start is called while already running.
func (w *CompactionWorker) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return ErrWorkerAlreadyRunning
	}
	w.running = true
	w.mu.Unlock()

	w.logger.Info("compaction worker started",
		"run_interval", w.config.RunInterval,
		"fragment_threshold", w.config.FragmentThreshold,
		"batch_size", w.config.BatchSize)

	ticker := time.NewTicker(w.config.RunInterval)
	defer ticker.Stop()

	// Run initial compaction immediately
	// Use tryStartIteration to safely add to WaitGroup only if not stopped
	if w.tryStartIteration() {
		w.runCompactionIteration(ctx)
		w.wg.Done()
	}

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("compaction worker stopped: context cancelled")
			w.markStopped()
			return nil
		case <-w.done:
			w.logger.Info("compaction worker stopped: explicit shutdown")
			w.markStopped()
			return nil
		case <-ticker.C:
			// Use tryStartIteration to safely add to WaitGroup only if not stopped
			if w.tryStartIteration() {
				w.runCompactionIteration(ctx)
				w.wg.Done()
			} else {
				// Worker is stopping, exit the loop
				w.logger.Info("compaction worker stopped: explicit shutdown")
				w.markStopped()
				return nil
			}
		}
	}
}

// markStopped safely marks the worker as not running.
func (w *CompactionWorker) markStopped() {
	w.mu.Lock()
	w.running = false
	w.mu.Unlock()
}

// tryStartIteration attempts to start a new compaction iteration.
// Returns true if the iteration can proceed (wg.Add(1) was called).
// Returns false if the worker is stopping (do not proceed with iteration).
// This method prevents the race between wg.Add and wg.Wait.
func (w *CompactionWorker) tryStartIteration() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopped {
		return false
	}
	w.wg.Add(1)
	return true
}

// Stop signals the worker to shut down gracefully.
// It waits for the current compaction iteration to complete.
// It is safe to call Stop multiple times.
func (w *CompactionWorker) Stop() {
	// Mark as stopped under lock to prevent new iterations from starting
	w.mu.Lock()
	alreadyStopped := w.stopped
	w.stopped = true
	w.mu.Unlock()

	if !alreadyStopped {
		select {
		case <-w.done:
			// Already closed
		default:
			close(w.done)
		}
	}

	// Wait for in-flight compaction operations to complete
	// This is safe because tryStartIteration won't call wg.Add after stopped=true
	w.wg.Wait()
	w.logger.Info("compaction worker shutdown complete")
}

// runCompactionIteration performs one compaction pass.
// It finds fragmented buckets and consolidates them.
// The caller must manage WaitGroup to prevent races with Stop().
func (w *CompactionWorker) runCompactionIteration(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	w.logger.Debug("starting compaction iteration")
	start := time.Now()
	RecordCompactionRun()

	fragmentedBuckets, err := w.findFragmentedBuckets(ctx)
	if err != nil {
		w.logger.Error("failed to find fragmented buckets", "error", err)
		RecordCompactionError(ErrorTypeScan)
		w.logIterationComplete(start, 0, 0, []error{err})
		return
	}

	SetFragmentedBucketsCount(len(fragmentedBuckets))

	if len(fragmentedBuckets) == 0 {
		w.logger.Debug("compaction iteration complete: no fragmented buckets found", "duration", time.Since(start))
		ObserveCompactionDuration(time.Since(start).Seconds())
		return
	}

	w.logger.Info("found fragmented buckets", "count", len(fragmentedBuckets), "threshold", w.config.FragmentThreshold)

	totalProcessed, totalRowsConsolidated, iterationErrors := w.processFragmentedBuckets(ctx, fragmentedBuckets)
	w.logIterationComplete(start, totalProcessed, totalRowsConsolidated, iterationErrors)
}

// processFragmentedBuckets compacts each fragmented bucket, returning tallied results.
func (w *CompactionWorker) processFragmentedBuckets(ctx context.Context, buckets []FragmentedBucket) (int, int, []error) {
	var totalProcessed, totalRowsConsolidated int
	var iterationErrors []error

	for _, bucket := range buckets {
		select {
		case <-ctx.Done():
			return totalProcessed, totalRowsConsolidated, iterationErrors
		case <-w.done:
			return totalProcessed, totalRowsConsolidated, iterationErrors
		default:
		}

		rowsConsolidated, err := w.compactBucket(ctx, bucket.AccountID, bucket.InstrumentCode, bucket.BucketKey)
		if err != nil {
			w.logger.Error("failed to compact bucket",
				"account_id", bucket.AccountID, "instrument_code", bucket.InstrumentCode,
				"bucket_key", bucket.BucketKey, "error", err)
			RecordCompactionError(ErrorTypeTx)
			iterationErrors = append(iterationErrors, err)
			continue
		}

		totalProcessed++
		totalRowsConsolidated += rowsConsolidated
		RecordBucketCompacted()
		RecordRowsConsolidated(rowsConsolidated)

		w.logger.Debug("compacted bucket",
			"account_id", bucket.AccountID, "instrument_code", bucket.InstrumentCode,
			"bucket_key", bucket.BucketKey, "rows_consolidated", rowsConsolidated)
	}

	return totalProcessed, totalRowsConsolidated, iterationErrors
}

// findFragmentedBuckets finds buckets with more rows than the fragment threshold.
func (w *CompactionWorker) findFragmentedBuckets(ctx context.Context) ([]FragmentedBucket, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Set tenant scope if in multi-tenant mode
	if err := w.setSearchPath(ctx, tx); err != nil {
		return nil, err
	}

	query := `
		SELECT account_id, instrument_code, bucket_key, COUNT(*) as row_count
		FROM position
		WHERE deleted_at IS NULL
		GROUP BY account_id, instrument_code, bucket_key
		HAVING COUNT(*) > $1
		ORDER BY row_count DESC
		LIMIT $2`

	rows, err := tx.Query(ctx, query, w.config.FragmentThreshold, w.config.BatchSize)
	if err != nil {
		return nil, fmt.Errorf("failed to query fragmented buckets: %w", err)
	}
	defer rows.Close()

	var buckets []FragmentedBucket
	for rows.Next() {
		var bucket FragmentedBucket
		if err := rows.Scan(&bucket.AccountID, &bucket.InstrumentCode, &bucket.BucketKey, &bucket.RowCount); err != nil {
			return nil, fmt.Errorf("failed to scan fragmented bucket: %w", err)
		}
		buckets = append(buckets, bucket)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating fragmented buckets: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return buckets, nil
}

// consolidatedPosition holds the result of consolidating multiple position rows.
type consolidatedPosition struct {
	Amount      decimal.Decimal
	Dimension   string
	Attributes  map[string]string
	OriginalIDs []uuid.UUID
}

// consolidatePositions calculates the consolidated values from multiple position rows.
func consolidatePositions(positions []PositionRow) consolidatedPosition {
	result := consolidatedPosition{
		Amount:      decimal.Zero,
		OriginalIDs: make([]uuid.UUID, len(positions)),
	}
	var latestCreatedAt time.Time

	for i, pos := range positions {
		result.Amount = result.Amount.Add(pos.Amount)
		result.OriginalIDs[i] = pos.ID

		// Use dimension and attributes from most recent position
		if pos.CreatedAt.After(latestCreatedAt) {
			latestCreatedAt = pos.CreatedAt
			result.Dimension = pos.Dimension
			// Copy attributes to avoid mutation
			if pos.Attributes != nil {
				result.Attributes = make(map[string]string, len(pos.Attributes))
				for k, v := range pos.Attributes {
					result.Attributes[k] = v
				}
			}
		}
	}

	return result
}

// compactBucket consolidates all position rows for a specific bucket into a single row.
// Returns the number of original rows consolidated.
func (w *CompactionWorker) compactBucket(ctx context.Context, accountID, instrumentCode, bucketKey string) (int, error) {
	// Use RepeatableRead isolation for correctness with FOR UPDATE locks
	tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Set tenant scope if in multi-tenant mode
	if err := w.setSearchPath(ctx, tx); err != nil {
		return 0, err
	}

	// Lock and get all positions for this bucket
	positions, err := w.lockAndGetPositions(ctx, tx, accountID, instrumentCode, bucketKey)
	if err != nil {
		RecordCompactionError(ErrorTypeLock)
		return 0, err
	}

	// Need at least 2 rows to compact (otherwise nothing to consolidate)
	if len(positions) < 2 {
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("failed to commit transaction: %w", err)
		}
		return 0, nil
	}

	// Calculate consolidated values
	consolidated := consolidatePositions(positions)

	// Execute the compaction within the transaction
	if err := w.executeCompaction(ctx, tx, accountID, instrumentCode, bucketKey, consolidated); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		RecordCompactionError(ErrorTypeTx)
		return 0, fmt.Errorf("failed to commit compaction transaction: %w", err)
	}

	return len(positions), nil
}

// executeCompaction performs the actual compaction operations within a transaction.
func (w *CompactionWorker) executeCompaction(
	ctx context.Context,
	tx pgx.Tx,
	accountID, instrumentCode, bucketKey string,
	consolidated consolidatedPosition,
) error {
	consolidatedID := uuid.New()
	compactionRef := uuid.New()
	now := time.Now().UTC()

	// Prepare attributes with compaction metadata
	attrs := consolidated.Attributes
	if attrs == nil {
		attrs = make(map[string]string)
	}
	attrs["_compacted_from_count"] = fmt.Sprintf("%d", len(consolidated.OriginalIDs))
	attrs["_compaction_ref"] = compactionRef.String()

	attributesJSON, err := json.Marshal(attrs)
	if err != nil {
		return fmt.Errorf("failed to marshal attributes: %w", err)
	}

	// Insert consolidated row
	if err := w.insertConsolidatedPosition(ctx, tx, consolidatedID, now, accountID, instrumentCode,
		bucketKey, consolidated.Amount, consolidated.Dimension, attributesJSON, compactionRef); err != nil {
		return err
	}

	// Soft delete original positions
	if err := w.softDeletePositions(ctx, tx, consolidated.OriginalIDs); err != nil {
		return err
	}

	// Try to insert audit record (optional - may not exist)
	w.tryInsertAuditRecord(ctx, tx, now, compactionRef, consolidatedID,
		consolidated.OriginalIDs, accountID, instrumentCode, bucketKey)

	return nil
}

// insertConsolidatedPosition inserts the new consolidated position row.
func (w *CompactionWorker) insertConsolidatedPosition(
	ctx context.Context,
	tx pgx.Tx,
	id uuid.UUID,
	createdAt time.Time,
	accountID, instrumentCode, bucketKey string,
	amount decimal.Decimal,
	dimension string,
	attributesJSON []byte,
	referenceID uuid.UUID,
) error {
	query := `
		INSERT INTO position (
			id, created_at, created_by,
			account_id, instrument_code, bucket_key, amount, dimension, attributes, reference_id
		) VALUES (
			$1, $2, 'compaction_worker',
			$3, $4, $5, $6, $7, $8, $9
		)`

	_, err := tx.Exec(ctx, query, id, createdAt, accountID, instrumentCode, bucketKey,
		amount, dimension, attributesJSON, referenceID)
	if err != nil {
		RecordCompactionError(ErrorTypeInsert)
		return fmt.Errorf("failed to insert consolidated position: %w", err)
	}
	return nil
}

// softDeletePositions marks the original positions as deleted.
func (w *CompactionWorker) softDeletePositions(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) error {
	query := `
		UPDATE position
		SET deleted_at = NOW()
		WHERE id = ANY($1)`

	_, err := tx.Exec(ctx, query, ids)
	if err != nil {
		RecordCompactionError(ErrorTypeDelete)
		return fmt.Errorf("failed to soft delete original positions: %w", err)
	}
	return nil
}

// tryInsertAuditRecord attempts to insert a compaction audit record.
// Failures are logged but do not cause the compaction to fail.
func (w *CompactionWorker) tryInsertAuditRecord(
	ctx context.Context,
	tx pgx.Tx,
	createdAt time.Time,
	compactionRef, consolidatedID uuid.UUID,
	originalIDs []uuid.UUID,
	accountID, instrumentCode, bucketKey string,
) {
	query := `
		INSERT INTO position_compaction_audit (
			id, created_at, compaction_ref, consolidated_position_id,
			original_position_ids, original_count, account_id, instrument_code, bucket_key
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9
		)`

	originalIDsJSON, err := json.Marshal(originalIDs)
	if err != nil {
		w.logger.Warn("failed to marshal original IDs for audit",
			"error", err,
			"compaction_ref", compactionRef)
		return
	}

	_, err = tx.Exec(ctx, query,
		uuid.New(), createdAt, compactionRef, consolidatedID,
		originalIDsJSON, len(originalIDs), accountID, instrumentCode, bucketKey,
	)
	if err != nil {
		w.logger.Warn("failed to insert compaction audit record (audit table may not exist)",
			"error", err,
			"compaction_ref", compactionRef,
			"consolidated_position_id", consolidatedID)
	}
}

// lockAndGetPositions acquires row-level locks and returns all positions for a bucket.
func (w *CompactionWorker) lockAndGetPositions(ctx context.Context, tx pgx.Tx, accountID, instrumentCode, bucketKey string) ([]PositionRow, error) {
	query := `
		SELECT id, amount, dimension, attributes, reference_id, created_at
		FROM position
		WHERE account_id = $1 AND instrument_code = $2 AND bucket_key = $3 AND deleted_at IS NULL
		FOR UPDATE`

	rows, err := tx.Query(ctx, query, accountID, instrumentCode, bucketKey)
	if err != nil {
		return nil, fmt.Errorf("failed to lock and query positions: %w", err)
	}
	defer rows.Close()

	var positions []PositionRow
	for rows.Next() {
		var pos PositionRow
		var attributesJSON sql.NullString
		var refID sql.NullString

		if err := rows.Scan(&pos.ID, &pos.Amount, &pos.Dimension, &attributesJSON, &refID, &pos.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan position row: %w", err)
		}

		if attributesJSON.Valid && attributesJSON.String != "" {
			if err := json.Unmarshal([]byte(attributesJSON.String), &pos.Attributes); err != nil {
				return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
			}
		}

		if refID.Valid {
			pos.ReferenceID, err = uuid.Parse(refID.String)
			if err != nil {
				return nil, fmt.Errorf("failed to parse reference_id: %w", err)
			}
		}

		positions = append(positions, pos)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating positions: %w", err)
	}

	return positions, nil
}

// setSearchPath sets the PostgreSQL search_path for the transaction.
// In multi-tenant mode, it sets the search_path to the tenant's schema.
// In single-tenant mode (no tenant context), it does nothing.
func (w *CompactionWorker) setSearchPath(ctx context.Context, tx pgx.Tx) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}

	schemaName := pq.QuoteIdentifier(tenantID.SchemaName())
	query := fmt.Sprintf("SET LOCAL search_path TO %s, public", schemaName)
	_, err := tx.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to set tenant schema scope: %w", err)
	}

	return nil
}

// logIterationComplete logs the summary of a compaction iteration.
func (w *CompactionWorker) logIterationComplete(
	start time.Time,
	bucketsProcessed, rowsConsolidated int,
	errs []error,
) {
	duration := time.Since(start)
	ObserveCompactionDuration(duration.Seconds())

	if bucketsProcessed == 0 && len(errs) == 0 {
		w.logger.Debug("compaction iteration complete: no buckets processed",
			"duration", duration)
		return
	}

	if len(errs) > 0 {
		w.logger.Warn("compaction iteration complete with errors",
			"buckets_processed", bucketsProcessed,
			"rows_consolidated", rowsConsolidated,
			"error_count", len(errs),
			"duration", duration)
	} else {
		w.logger.Info("compaction iteration complete",
			"buckets_processed", bucketsProcessed,
			"rows_consolidated", rowsConsolidated,
			"duration", duration)
	}
}
