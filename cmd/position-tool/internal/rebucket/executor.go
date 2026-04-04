// Package rebucket provides position rebucketing functionality for the position-tool CLI.
// It handles recalculating bucket keys for existing positions after instrument definition
// changes, maintaining append-only semantics with full audit logging.
package rebucket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Batch size limits.
const (
	// DefaultBatchSize is the default number of positions to process per batch.
	DefaultBatchSize = 500
	// MaxBatchSize is the maximum allowed batch size to prevent memory issues.
	MaxBatchSize = 10000
)

// Executor errors.
var (
	// ErrNilPool indicates the database pool is nil.
	ErrNilPool = errors.New("database pool cannot be nil")
	// ErrInvalidBatchSize indicates the batch size is not positive.
	ErrInvalidBatchSize = errors.New("batch size must be positive")
	// ErrBatchSizeTooLarge indicates the batch size exceeds the maximum allowed.
	ErrBatchSizeTooLarge = fmt.Errorf("batch size cannot exceed %d", MaxBatchSize)
	// ErrNilPlan indicates the rebucketing plan is nil.
	ErrNilPlan = errors.New("rebucketing plan cannot be nil")
	// ErrPositionNotFound indicates a position was not found or already deleted.
	ErrPositionNotFound = errors.New("position not found or already deleted")
	// ErrInsertCountMismatch indicates the inserted count doesn't match expected.
	ErrInsertCountMismatch = errors.New("position insert count mismatch")
)

// Config holds configuration for the rebucketing executor.
type Config struct {
	// BatchSize is the number of positions to process per batch (default: 500).
	BatchSize int

	// DryRun mode shows plan without executing.
	DryRun bool
}

// DefaultConfig returns the default executor configuration.
func DefaultConfig() *Config {
	return &Config{
		BatchSize: DefaultBatchSize,
		DryRun:    false,
	}
}

// Validate validates the executor configuration.
func (c *Config) Validate() error {
	if c.BatchSize <= 0 {
		return ErrInvalidBatchSize
	}
	if c.BatchSize > MaxBatchSize {
		return ErrBatchSizeTooLarge
	}
	return nil
}

// AffectedPosition represents a position that will be affected by rebucketing.
type AffectedPosition struct {
	// PositionID is the database ID of the existing position.
	PositionID uuid.UUID

	// AccountID identifies the account.
	AccountID string

	// InstrumentCode identifies the instrument.
	InstrumentCode string

	// OldBucketKey is the current bucket_key.
	OldBucketKey string

	// NewBucketKey is the target bucket_key after rebucketing.
	NewBucketKey string

	// Amount is the position amount.
	Amount decimal.Decimal

	// Dimension classifies the asset type.
	Dimension string

	// Attributes stores flexible metadata.
	Attributes map[string]string

	// ReferenceID links to the source event.
	ReferenceID uuid.UUID

	// CreatedAt is when the original position was created.
	CreatedAt time.Time

	// CreatedBy is who created the original position.
	CreatedBy string
}

// RebucketingPlan represents the plan for rebucketing positions.
type RebucketingPlan struct {
	// InstrumentCode identifies the instrument being rebucketed.
	InstrumentCode string

	// OldInstrumentVersion is the version hash before rebucketing.
	OldInstrumentVersion string

	// NewInstrumentVersion is the version hash after rebucketing.
	NewInstrumentVersion string

	// BucketMappings maps old bucket keys to new bucket keys.
	BucketMappings map[string]string

	// AffectedPositions lists all positions that need rebucketing.
	AffectedPositions []AffectedPosition
}

// ExecutionResult contains the results of a rebucketing execution.
type ExecutionResult struct {
	// Success indicates whether the execution completed successfully.
	Success bool

	// PositionsUpdated is the count of positions that were rebucketed.
	PositionsUpdated int64

	// BucketsAffected is the count of unique bucket mappings applied.
	BucketsAffected int

	// AuditLogEntries is the total number of audit log entries created
	// (2 per position: SOFT_DELETE + INSERT_NEW).
	AuditLogEntries int64

	// Duration is the total execution time.
	Duration time.Duration

	// DryRun indicates if this was a dry-run execution.
	DryRun bool

	// Error contains any error that occurred during execution.
	Error error
}

// Executor handles position rebucketing operations.
type Executor struct {
	pool      *pgxpool.Pool
	config    *Config
	logger    *slog.Logger
	batchSize int
}

// NewExecutor creates a new rebucketing executor.
func NewExecutor(pool *pgxpool.Pool, config *Config, logger *slog.Logger) (*Executor, error) {
	if pool == nil {
		return nil, ErrNilPool
	}
	if config == nil {
		config = DefaultConfig()
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Executor{
		pool:      pool,
		config:    config,
		logger:    logger,
		batchSize: config.BatchSize,
	}, nil
}

// Execute performs the rebucketing operation according to the provided plan.
func (e *Executor) Execute(ctx context.Context, plan *RebucketingPlan, adminUserID string) (*ExecutionResult, error) {
	startTime := time.Now()

	// Validate input
	if plan == nil {
		return &ExecutionResult{
			Success:  false,
			Duration: time.Since(startTime),
			Error:    ErrNilPlan,
		}, ErrNilPlan
	}

	if len(plan.AffectedPositions) == 0 {
		return &ExecutionResult{
			Success:  true,
			Duration: time.Since(startTime),
		}, nil
	}

	e.logger.Info("starting rebucketing execution",
		"admin_user", adminUserID,
		"instrument", plan.InstrumentCode,
		"total_positions", len(plan.AffectedPositions),
		"batch_size", e.batchSize,
	)

	// Handle dry-run mode
	if e.config.DryRun {
		return e.executeDryRun(plan, startTime)
	}

	// Execute the actual rebucketing
	return e.executeRebucketing(ctx, plan, adminUserID, startTime)
}

// executeDryRun returns results for a dry-run execution.
func (e *Executor) executeDryRun(plan *RebucketingPlan, startTime time.Time) (*ExecutionResult, error) {
	batches := e.splitIntoBatches(plan.AffectedPositions)

	e.logger.Info("dry-run completed",
		"instrument", plan.InstrumentCode,
		"position_count", len(plan.AffectedPositions),
		"batch_count", len(batches),
	)

	return &ExecutionResult{
		Success:          true,
		PositionsUpdated: int64(len(plan.AffectedPositions)),
		BucketsAffected:  len(plan.BucketMappings),
		AuditLogEntries:  int64(len(plan.AffectedPositions) * 2),
		Duration:         time.Since(startTime),
		DryRun:           true,
	}, nil
}

// executeRebucketing performs the actual rebucketing operation.
func (e *Executor) executeRebucketing(
	ctx context.Context,
	plan *RebucketingPlan,
	adminUserID string,
	startTime time.Time,
) (*ExecutionResult, error) {
	batches := e.splitIntoBatches(plan.AffectedPositions)
	totalPositions := int64(len(plan.AffectedPositions))

	e.logger.Info("processing rebucketing batches",
		"admin_user", adminUserID,
		"instrument", plan.InstrumentCode,
		"total_positions", totalPositions,
		"batch_count", len(batches),
	)

	// Process all batches in a single transaction for atomicity
	tx, err := e.beginTx(ctx)
	if err != nil {
		return &ExecutionResult{
			Success:  false,
			Duration: time.Since(startTime),
			Error:    fmt.Errorf("failed to begin transaction: %w", err),
		}, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var processedCount int64
	for batchNum, batch := range batches {
		e.logger.Debug("processing batch",
			"batch_number", batchNum+1,
			"batch_size", len(batch),
		)

		// Process positions in batch
		if err := e.processBatch(ctx, tx, batch, adminUserID); err != nil {
			e.logger.Error("batch processing failed, rolling back",
				"batch_number", batchNum+1,
				"positions_processed", processedCount,
				"error", err,
			)
			return &ExecutionResult{
				Success:  false,
				Duration: time.Since(startTime),
				Error:    err,
			}, err
		}

		processedCount += int64(len(batch))
		e.logger.Debug("batch completed",
			"batch_number", batchNum+1,
			"total_processed", processedCount,
		)
	}

	// Commit the transaction
	if err := tx.Commit(ctx); err != nil {
		e.logger.Error("transaction commit failed",
			"positions_processed", processedCount,
			"error", err,
		)
		return &ExecutionResult{
			Success:  false,
			Duration: time.Since(startTime),
			Error:    fmt.Errorf("failed to commit transaction: %w", err),
		}, err
	}

	duration := time.Since(startTime)
	auditEntries := totalPositions * 2 // SOFT_DELETE + INSERT_NEW per position

	e.logger.Info("rebucketing completed successfully",
		"admin_user", adminUserID,
		"instrument", plan.InstrumentCode,
		"positions_updated", totalPositions,
		"buckets_affected", len(plan.BucketMappings),
		"audit_entries", auditEntries,
		"duration_ms", duration.Milliseconds(),
	)

	return &ExecutionResult{
		Success:          true,
		PositionsUpdated: totalPositions,
		BucketsAffected:  len(plan.BucketMappings),
		AuditLogEntries:  auditEntries,
		Duration:         duration,
		DryRun:           false,
	}, nil
}

// beginTx starts a new transaction with tenant scoping.
func (e *Executor) beginTx(ctx context.Context) (pgx.Tx, error) {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	if err := e.setSearchPath(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}

	return tx, nil
}

// setSearchPath sets the PostgreSQL search_path for multi-tenant isolation.
// Security: TenantID is validated at construction (NewTenantID) to contain only
// alphanumeric characters and underscores (1-50 chars), and pgx.Identifier.Sanitize()
// provides proper PostgreSQL identifier quoting. This double-validation approach
// prevents SQL injection in the search_path.
func (e *Executor) setSearchPath(ctx context.Context, tx pgx.Tx) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		// Single-tenant mode: no scoping needed
		return nil
	}

	// SchemaName() returns "org_" + lowercase(tenantID), validated at construction
	schemaName := pgx.Identifier{tenantID.SchemaName()}.Sanitize()
	query := fmt.Sprintf("SET LOCAL search_path TO %s", schemaName)
	_, err := tx.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to set tenant schema scope: %w", err)
	}

	return nil
}

// processBatch processes a batch of positions within the given transaction.
func (e *Executor) processBatch(
	ctx context.Context,
	tx pgx.Tx,
	positions []AffectedPosition,
	createdBy string,
) error {
	if len(positions) == 0 {
		return nil
	}

	// Step 1: Soft-delete all old positions in batch
	if err := e.softDeleteBatch(ctx, tx, positions); err != nil {
		return err
	}

	// Step 2: Insert new positions with corrected bucket keys
	return e.insertNewBatch(ctx, tx, positions, createdBy)
}

// softDeleteBatch marks old positions as deleted using batched UPDATE.
func (e *Executor) softDeleteBatch(ctx context.Context, tx pgx.Tx, positions []AffectedPosition) error {
	batch := &pgx.Batch{}
	now := time.Now().UTC()

	query := `UPDATE position SET deleted_at = $1 WHERE id = $2 AND deleted_at IS NULL`

	for _, pos := range positions {
		batch.Queue(query, now, pos.PositionID)
	}

	br := tx.SendBatch(ctx, batch)
	defer func() {
		_ = br.Close()
	}()

	for i, pos := range positions {
		ct, err := br.Exec()
		if err != nil {
			return fmt.Errorf("soft delete failed for position %s at index %d: %w",
				pos.PositionID, i, err)
		}
		if ct.RowsAffected() == 0 {
			return fmt.Errorf("%w: %s", ErrPositionNotFound, pos.PositionID)
		}
	}

	return nil
}

// insertNewBatch creates new positions with corrected bucket keys using COPY.
func (e *Executor) insertNewBatch(
	ctx context.Context,
	tx pgx.Tx,
	positions []AffectedPosition,
	createdBy string,
) error {
	copyCount, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"position"},
		[]string{
			"id", "created_at", "created_by",
			"account_id", "instrument_code", "bucket_key",
			"amount", "dimension", "attributes", "reference_id",
		},
		pgx.CopyFromSlice(len(positions), func(i int) ([]any, error) {
			pos := positions[i]

			// Marshal attributes to JSON
			var attrsJSON []byte
			if pos.Attributes != nil {
				var marshalErr error
				attrsJSON, marshalErr = json.Marshal(pos.Attributes)
				if marshalErr != nil {
					return nil, fmt.Errorf("failed to marshal attributes for position %d: %w", i, marshalErr)
				}
			}

			// Handle nil reference_id
			var refID interface{}
			if pos.ReferenceID != uuid.Nil {
				refID = pos.ReferenceID
			}

			return []any{
				uuid.New(),         // New ID for the new position
				time.Now().UTC(),   // New created_at timestamp
				createdBy,          // Admin who authorized rebucketing
				pos.AccountID,      // Same account
				pos.InstrumentCode, // Same instrument
				pos.NewBucketKey,   // NEW bucket key (the whole point!)
				pos.Amount,         // Same amount
				pos.Dimension,      // Same dimension
				attrsJSON,          // Same attributes
				refID,              // Same reference (for traceability)
			}, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("failed to insert new positions: %w", err)
	}

	if copyCount != int64(len(positions)) {
		return fmt.Errorf("%w: expected %d but inserted %d",
			ErrInsertCountMismatch, len(positions), copyCount)
	}

	return nil
}

// splitIntoBatches splits positions into batches of the configured size.
func (e *Executor) splitIntoBatches(positions []AffectedPosition) [][]AffectedPosition {
	if len(positions) == 0 {
		return nil
	}

	var batches [][]AffectedPosition
	for i := 0; i < len(positions); i += e.batchSize {
		end := i + e.batchSize
		if end > len(positions) {
			end = len(positions)
		}
		batches = append(batches, positions[i:end])
	}

	return batches
}
