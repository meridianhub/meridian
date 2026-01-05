package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// PositionUpdater handles batched position updates for rebucketing.
// It maintains append-only semantics by soft-deleting old positions
// and inserting new positions with corrected bucket keys.
type PositionUpdater struct {
	pool      *pgxpool.Pool
	batchSize int
}

// NewPositionUpdater creates a new position updater with the given connection pool.
func NewPositionUpdater(pool *pgxpool.Pool, batchSize int) (*PositionUpdater, error) {
	if pool == nil {
		return nil, ErrNilPool
	}
	if batchSize <= 0 {
		return nil, ErrInvalidBatchSize
	}
	if batchSize > 10000 {
		return nil, ErrBatchSizeTooLarge
	}

	return &PositionUpdater{
		pool:      pool,
		batchSize: batchSize,
	}, nil
}

// setSearchPath sets the PostgreSQL search_path for multi-tenant isolation.
func (u *PositionUpdater) setSearchPath(ctx context.Context, tx pgx.Tx) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		// Single-tenant mode: no scoping needed
		return nil
	}

	schemaName := pgx.Identifier{tenantID.SchemaName()}.Sanitize()
	query := fmt.Sprintf("SET LOCAL search_path TO %s, public", schemaName)
	_, err := tx.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to set tenant schema scope: %w", err)
	}

	return nil
}

// BeginTx starts a new transaction with tenant scoping.
func (u *PositionUpdater) BeginTx(ctx context.Context) (pgx.Tx, error) {
	tx, err := u.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	if err := u.setSearchPath(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}

	return tx, nil
}

// ProcessBatch processes a batch of positions within the given transaction.
// For each position, it:
// 1. Soft-deletes the old position (sets deleted_at)
// 2. Inserts a new position with the corrected bucket_key
func (u *PositionUpdater) ProcessBatch(
	ctx context.Context,
	tx pgx.Tx,
	positions []AffectedPosition,
	createdBy string,
) error {
	if len(positions) == 0 {
		return nil
	}

	// Step 1: Soft-delete all old positions in batch
	if err := u.softDeleteBatch(ctx, tx, positions); err != nil {
		return err
	}

	// Step 2: Insert new positions with corrected bucket keys
	return u.insertNewBatch(ctx, tx, positions, createdBy)
}

// softDeleteBatch marks old positions as deleted using batched UPDATE.
func (u *PositionUpdater) softDeleteBatch(ctx context.Context, tx pgx.Tx, positions []AffectedPosition) error {
	batch := &pgx.Batch{}
	now := time.Now().UTC()

	// The append-only trigger allows UPDATE on deleted_at column
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
			return fmt.Errorf("%w: position %s at index %d: %w",
				ErrPositionSoftDelete, pos.PositionID, i, err)
		}
		if ct.RowsAffected() == 0 {
			// Position was already deleted or doesn't exist - this is unexpected
			return fmt.Errorf("%w: position %s not found or already deleted",
				ErrPositionSoftDelete, pos.PositionID)
		}
	}

	return nil
}

// insertNewBatch creates new positions with corrected bucket keys using COPY.
func (u *PositionUpdater) insertNewBatch(
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
		return fmt.Errorf("%w: %w", ErrPositionInsert, err)
	}

	if copyCount != int64(len(positions)) {
		return fmt.Errorf("%w: expected %d positions but inserted %d",
			ErrPositionInsert, len(positions), copyCount)
	}

	return nil
}

// GetBatchSize returns the configured batch size.
func (u *PositionUpdater) GetBatchSize() int {
	return u.batchSize
}

// SplitIntoBatches splits positions into batches of the configured size.
func (u *PositionUpdater) SplitIntoBatches(positions []AffectedPosition) [][]AffectedPosition {
	if len(positions) == 0 {
		return nil
	}

	var batches [][]AffectedPosition
	for i := 0; i < len(positions); i += u.batchSize {
		end := i + u.batchSize
		if end > len(positions) {
			end = len(positions)
		}
		batches = append(batches, positions[i:end])
	}

	return batches
}
