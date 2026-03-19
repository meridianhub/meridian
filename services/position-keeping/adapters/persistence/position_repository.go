// Package persistence provides PostgreSQL persistence implementation for Position Keeping domain.
//
//meridian:large-file - known oversized file; split tracked in backlog
package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

var (
	// ErrNilPosition is returned when a nil position is passed to a repository method
	ErrNilPosition = errors.New("position cannot be nil")
	// ErrPositionBulkInsertMismatch is returned when bulk insert count doesn't match expected
	ErrPositionBulkInsertMismatch = errors.New("position bulk insert count mismatch")
)

// PositionRepository implements domain.PositionRepository using PostgreSQL.
// This repository enforces append-only semantics: Insert() is the ONLY write method.
// No Update() or Upsert() methods are provided - this is by design.
type PositionRepository struct {
	pool *pgxpool.Pool
}

// NewPositionRepository creates a new PostgreSQL position repository with the given connection pool.
// The pool should be pre-configured with appropriate connection limits and timeouts.
func NewPositionRepository(pool *pgxpool.Pool) *PositionRepository {
	return &PositionRepository{pool: pool}
}

// setSearchPath sets the PostgreSQL search_path for the transaction.
// In multi-tenant mode, it sets the search_path to the tenant's schema.
// In single-tenant mode (no tenant context), it does nothing.
func (r *PositionRepository) setSearchPath(ctx context.Context, tx pgx.Tx) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
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

// Insert persists a new Position record to the database.
// This is the ONLY write method - append-only semantics are enforced.
// Returns domain.ErrConflict if a position with the same ID already exists.
func (r *PositionRepository) Insert(ctx context.Context, position *domain.Position) error {
	if position == nil {
		return ErrNilPosition
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}

	// Marshal attributes to JSON
	var attributesJSON []byte
	if position.Attributes != nil {
		attributesJSON, err = json.Marshal(position.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}
	}

	userID := audit.GetUserFromContext(ctx)

	query := `
		INSERT INTO position (
			id, created_at, created_by,
			account_id, instrument_code, bucket_key, amount, dimension, attributes, reference_id
		) VALUES (
			$1, $2, $3,
			$4, $5, $6, $7, $8, $9, $10
		)`

	// Handle nil reference_id
	var refID interface{}
	if position.ReferenceID != uuid.Nil {
		refID = position.ReferenceID
	}

	_, err = tx.Exec(ctx, query,
		position.ID, position.CreatedAt, userID,
		position.AccountID, position.InstrumentCode, position.BucketKey,
		position.Amount, position.Dimension, attributesJSON, refID,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return domain.ErrConflict
		}
		return fmt.Errorf("failed to insert position: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// InsertBatch persists multiple Position records atomically using efficient bulk operations.
// If any position fails to persist, the entire batch is rolled back.
// Returns domain.ErrConflict if any position has a duplicate ID.
func (r *PositionRepository) InsertBatch(ctx context.Context, positions []*domain.Position) error {
	if len(positions) == 0 {
		return nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}

	userID := audit.GetUserFromContext(ctx)

	// Use COPY for bulk insert
	copyCount, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"position"},
		[]string{
			"id", "created_at", "created_by",
			"account_id", "instrument_code", "bucket_key", "amount", "dimension", "attributes", "reference_id",
		},
		pgx.CopyFromSlice(len(positions), func(i int) ([]any, error) {
			pos := positions[i]

			// Marshal attributes
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
				pos.ID, pos.CreatedAt, userID,
				pos.AccountID, pos.InstrumentCode, pos.BucketKey, pos.Amount, pos.Dimension, attrsJSON, refID,
			}, nil
		}),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return domain.ErrConflict
		}
		return fmt.Errorf("failed to bulk insert positions: %w", err)
	}

	if copyCount != int64(len(positions)) {
		return fmt.Errorf("%w: expected %d positions but inserted %d", ErrPositionBulkInsertMismatch, len(positions), copyCount)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit batch transaction: %w", err)
	}

	return nil
}

// FindByID retrieves a Position by its ID.
// Returns domain.ErrNotFound if the position doesn't exist.
//
// NOTE: This method uses a transaction for multi-tenant schema isolation.
// SET LOCAL search_path only works within a transaction boundary.
// See ADR-0016 for schema-per-tenant architecture details.
func (r *PositionRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.Position, error) {
	// Transaction required for SET LOCAL search_path (multi-tenant schema isolation)
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
		WHERE id = $1 AND deleted_at IS NULL`

	var pos domain.Position
	var attributesJSON sql.NullString
	var refID sql.NullString

	err = tx.QueryRow(ctx, query, id).Scan(
		&pos.ID, &pos.CreatedAt, &pos.CreatedBy, &pos.AccountID, &pos.InstrumentCode, &pos.BucketKey,
		&pos.Amount, &pos.Dimension, &attributesJSON, &refID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to query position: %w", err)
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

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &pos, nil
}

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

// scanPositions scans position rows into domain objects.
func (r *PositionRepository) scanPositions(rows pgx.Rows) ([]*domain.Position, error) {
	var positions []*domain.Position
	for rows.Next() {
		var pos domain.Position
		var attributesJSON sql.NullString
		var refID sql.NullString

		err := rows.Scan(
			&pos.ID, &pos.CreatedAt, &pos.CreatedBy, &pos.AccountID, &pos.InstrumentCode, &pos.BucketKey,
			&pos.Amount, &pos.Dimension, &attributesJSON, &refID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan position: %w", err)
		}

		if attributesJSON.Valid && attributesJSON.String != "" {
			if err := json.Unmarshal([]byte(attributesJSON.String), &pos.Attributes); err != nil {
				return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
			}
		}

		if refID.Valid {
			var parseErr error
			pos.ReferenceID, parseErr = uuid.Parse(refID.String)
			if parseErr != nil {
				return nil, fmt.Errorf("failed to parse reference_id: %w", parseErr)
			}
		}

		positions = append(positions, &pos)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating positions: %w", err)
	}

	return positions, nil
}

// InsertWithTx persists a new Position to the database within an existing transaction.
// This is useful for transactional operations where position creation
// should be atomic with other database operations.
func (r *PositionRepository) InsertWithTx(ctx context.Context, tx pgx.Tx, position *domain.Position) error {
	if position == nil {
		return ErrNilPosition
	}

	// Marshal attributes to JSON
	var attributesJSON []byte
	var err error
	if position.Attributes != nil {
		attributesJSON, err = json.Marshal(position.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}
	}

	userID := audit.GetUserFromContext(ctx)

	query := `
		INSERT INTO position (
			id, created_at, created_by,
			account_id, instrument_code, bucket_key, amount, dimension, attributes, reference_id
		) VALUES (
			$1, $2, $3,
			$4, $5, $6, $7, $8, $9, $10
		)`

	// Handle nil reference_id
	var refID interface{}
	if position.ReferenceID != uuid.Nil {
		refID = position.ReferenceID
	}

	_, err = tx.Exec(ctx, query,
		position.ID, position.CreatedAt, userID,
		position.AccountID, position.InstrumentCode, position.BucketKey,
		position.Amount, position.Dimension, attributesJSON, refID,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return domain.ErrConflict
		}
		return fmt.Errorf("failed to insert position: %w", err)
	}

	return nil
}

// BeginTx starts a new transaction with tenant scoping.
func (r *PositionRepository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	if err := r.setSearchPath(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}

	return tx, nil
}

// SoftDelete marks a position as deleted by setting deleted_at = NOW().
// This is an allowed UPDATE operation on the append-only position table
// since deleted_at is explicitly excluded from immutable field enforcement.
// Returns domain.ErrNotFound if the position doesn't exist or is already deleted.
func (r *PositionRepository) SoftDelete(ctx context.Context, id uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}

	result, err := tx.Exec(ctx,
		"UPDATE position SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL",
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to soft delete position: %w", err)
	}

	if result.RowsAffected() == 0 {
		return domain.ErrNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// SoftDeleteBatch marks multiple positions as deleted atomically.
// This is an allowed UPDATE operation on the append-only position table.
// Positions that are already deleted or don't exist are silently skipped.
func (r *PositionRepository) SoftDeleteBatch(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}

	_, err = tx.Exec(ctx,
		"UPDATE position SET deleted_at = NOW() WHERE id = ANY($1) AND deleted_at IS NULL",
		ids,
	)
	if err != nil {
		return fmt.Errorf("failed to soft delete positions: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// UpdateAttributes updates only the attributes JSONB field for a position.
// This is an allowed UPDATE operation on the append-only position table
// since attributes is explicitly excluded from immutable field enforcement.
// Returns domain.ErrNotFound if the position doesn't exist or is deleted.
func (r *PositionRepository) UpdateAttributes(ctx context.Context, id uuid.UUID, attributes map[string]string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}

	var attributesJSON []byte
	if attributes != nil {
		attributesJSON, err = json.Marshal(attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}
	}

	result, err := tx.Exec(ctx,
		"UPDATE position SET attributes = $1 WHERE id = $2 AND deleted_at IS NULL",
		attributesJSON, id,
	)
	if err != nil {
		return fmt.Errorf("failed to update attributes: %w", err)
	}

	if result.RowsAffected() == 0 {
		return domain.ErrNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// Ensure PositionRepository implements the interface at compile time
var _ domain.PositionRepository = (*PositionRepository)(nil)

// NOTE: No general Update(), Upsert(), or Merge() methods are implemented.
// This is intentional - the repository enforces append-only semantics.
// Only SoftDelete (deleted_at) and UpdateAttributes (attributes) are allowed.
// Position consolidation is handled at read time via GetAggregatedPosition().

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
