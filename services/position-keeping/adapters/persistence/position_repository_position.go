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
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
)

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

	if err := r.InsertWithTx(ctx, tx, position); err != nil {
		return err
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

	if err := r.bulkCopyPositions(ctx, tx, positions); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit batch transaction: %w", err)
	}

	return nil
}

// bulkCopyPositions uses COPY to bulk insert position rows.
func (r *PositionRepository) bulkCopyPositions(ctx context.Context, tx pgx.Tx, positions []*domain.Position) error {
	userID := audit.GetUserFromContext(ctx)

	copyCount, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"position"},
		[]string{
			"id", "created_at", "created_by",
			"account_id", "instrument_code", "bucket_key", "amount", "dimension", "attributes", "reference_id",
		},
		pgx.CopyFromSlice(len(positions), func(i int) ([]any, error) {
			return buildPositionCopyRow(positions[i], userID)
		}),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrConflict
		}
		return fmt.Errorf("failed to bulk insert positions: %w", err)
	}

	if copyCount != int64(len(positions)) {
		return fmt.Errorf("%w: expected %d positions but inserted %d", ErrPositionBulkInsertMismatch, len(positions), copyCount)
	}

	return nil
}

// buildPositionCopyRow builds a COPY row for a single position.
func buildPositionCopyRow(pos *domain.Position, userID string) ([]any, error) {
	var attrsJSON []byte
	if pos.Attributes != nil {
		var err error
		attrsJSON, err = json.Marshal(pos.Attributes)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal attributes: %w", err)
		}
	}

	var refID interface{}
	if pos.ReferenceID != uuid.Nil {
		refID = pos.ReferenceID
	}

	return []any{
		pos.ID, pos.CreatedAt, userID,
		pos.AccountID, pos.InstrumentCode, pos.BucketKey, pos.Amount, pos.Dimension, attrsJSON, refID,
	}, nil
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
