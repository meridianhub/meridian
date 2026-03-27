package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
)

// GetAggregatedPosition retrieves the consolidated position for a specific
// (AccountID, InstrumentCode, BucketKey) combination by summing all records.
// Returns nil if no positions exist for the combination.
//
// NOTE: Transaction required for SET LOCAL search_path (multi-tenant schema isolation).
func (r *PositionRepository) GetAggregatedPosition(ctx context.Context, accountID, instrumentCode, bucketKey string) (*domain.AggregatedPosition, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return nil, err
	}

	query := `
		SELECT
			account_id,
			instrument_code,
			bucket_key,
			SUM(amount) as total_amount,
			MAX(dimension) as dimension,
			COUNT(*) as record_count,
			MAX(created_at) as last_updated
		FROM position
		WHERE account_id = $1
			AND instrument_code = $2
			AND bucket_key = $3
			AND deleted_at IS NULL
		GROUP BY account_id, instrument_code, bucket_key`

	var agg domain.AggregatedPosition
	err = tx.QueryRow(ctx, query, accountID, instrumentCode, bucketKey).Scan(
		&agg.AccountID, &agg.InstrumentCode, &agg.BucketKey,
		&agg.TotalAmount, &agg.Dimension, &agg.RecordCount, &agg.LastUpdated,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No positions exist for this combination - valid empty result
			return nil, nil //nolint:nilnil // nil result with nil error is valid contract for "not found"
		}
		return nil, fmt.Errorf("failed to query aggregated position: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &agg, nil
}

// ListByAccount retrieves all position records for an account with pagination.
// Returns an empty slice if no positions exist.
//
// NOTE: Transaction required for SET LOCAL search_path (multi-tenant schema isolation).
func (r *PositionRepository) ListByAccount(ctx context.Context, accountID string, limit, offset int) ([]*domain.Position, error) {
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return nil, err
	}

	query := `
		SELECT id, created_at, created_by, account_id, instrument_code, bucket_key,
			amount, dimension, attributes, reference_id
		FROM position
		WHERE account_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`

	rows, err := tx.Query(ctx, query, accountID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query positions: %w", err)
	}
	defer rows.Close()

	positions, err := r.scanPositions(rows)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return positions, nil
}

// ListAggregatedByAccount retrieves all aggregated positions for an account.
// Groups by (InstrumentCode, BucketKey) and sums amounts.
// Returns an empty slice if no positions exist.
//
// NOTE: Transaction required for SET LOCAL search_path (multi-tenant schema isolation).
func (r *PositionRepository) ListAggregatedByAccount(ctx context.Context, accountID string) ([]*domain.AggregatedPosition, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return nil, err
	}

	query := `
		SELECT
			account_id,
			instrument_code,
			bucket_key,
			SUM(amount) as total_amount,
			MAX(dimension) as dimension,
			COUNT(*) as record_count,
			MAX(created_at) as last_updated
		FROM position
		WHERE account_id = $1 AND deleted_at IS NULL
		GROUP BY account_id, instrument_code, bucket_key
		ORDER BY instrument_code, bucket_key`

	rows, err := tx.Query(ctx, query, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to query aggregated positions: %w", err)
	}
	defer rows.Close()

	var positions []*domain.AggregatedPosition
	for rows.Next() {
		var agg domain.AggregatedPosition
		err := rows.Scan(
			&agg.AccountID, &agg.InstrumentCode, &agg.BucketKey,
			&agg.TotalAmount, &agg.Dimension, &agg.RecordCount, &agg.LastUpdated,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan aggregated position: %w", err)
		}
		positions = append(positions, &agg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating aggregated positions: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return positions, nil
}

// GetPositionCount returns the count of positions matching the criteria.
// This is useful for pagination and monitoring.
//
// NOTE: Transaction required for SET LOCAL search_path (multi-tenant schema isolation).
func (r *PositionRepository) GetPositionCount(ctx context.Context, accountID string) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return 0, err
	}

	var count int64
	err = tx.QueryRow(ctx,
		"SELECT COUNT(*) FROM position WHERE account_id = $1 AND deleted_at IS NULL",
		accountID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count positions: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return count, nil
}

// GetAggregatedPositions retrieves all aggregated positions for an account/instrument
// combination, grouped by BucketKey using pure SQL GROUP BY.
//
// This is a READ-ONLY operation with no side effects - it does NOT trigger compaction.
// Read operations are intentionally decoupled from write load to prevent DOS vectors.
//
// NOTE: Transaction required for SET LOCAL search_path (multi-tenant schema isolation).
func (r *PositionRepository) GetAggregatedPositions(ctx context.Context, accountID, instrumentCode string) ([]*domain.AggregatedPosition, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return nil, err
	}

	// Pure SQL aggregation using B-tree index on bucket_key
	// No CEL evaluation - all aggregation happens in PostgreSQL
	query := `
		SELECT
			account_id,
			instrument_code,
			bucket_key,
			SUM(amount) as total_amount,
			MAX(dimension) as dimension,
			COUNT(*) as record_count,
			MAX(created_at) as last_updated
		FROM position
		WHERE account_id = $1
			AND instrument_code = $2
			AND deleted_at IS NULL
		GROUP BY account_id, instrument_code, bucket_key
		ORDER BY bucket_key`

	rows, err := tx.Query(ctx, query, accountID, instrumentCode)
	if err != nil {
		return nil, fmt.Errorf("failed to query aggregated positions: %w", err)
	}
	defer rows.Close()

	var positions []*domain.AggregatedPosition
	for rows.Next() {
		var agg domain.AggregatedPosition
		err := rows.Scan(
			&agg.AccountID, &agg.InstrumentCode, &agg.BucketKey,
			&agg.TotalAmount, &agg.Dimension, &agg.RecordCount, &agg.LastUpdated,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan aggregated position: %w", err)
		}
		positions = append(positions, &agg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating aggregated positions: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return positions, nil
}

// GetBucketDetails retrieves all raw position records for a specific bucket.
//
// This is a READ-ONLY operation with no side effects - it does NOT trigger compaction.
// Returns positions sorted by CreatedAt descending for most recent first.
//
// NOTE: Transaction required for SET LOCAL search_path (multi-tenant schema isolation).
func (r *PositionRepository) GetBucketDetails(ctx context.Context, accountID, instrumentCode, bucketKey string, limit, offset int) ([]*domain.Position, error) {
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return nil, err
	}

	query := `
		SELECT id, created_at, created_by, account_id, instrument_code, bucket_key,
			amount, dimension, attributes, reference_id
		FROM position
		WHERE account_id = $1
			AND instrument_code = $2
			AND bucket_key = $3
			AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT $4 OFFSET $5`

	rows, err := tx.Query(ctx, query, accountID, instrumentCode, bucketKey, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query bucket details: %w", err)
	}
	defer rows.Close()

	positions, err := r.scanPositions(rows)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return positions, nil
}
