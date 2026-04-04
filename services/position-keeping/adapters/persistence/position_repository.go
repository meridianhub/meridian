// Package persistence provides PostgreSQL persistence implementation for Position Keeping domain.
package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
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
	query := fmt.Sprintf("SET LOCAL search_path TO %s", schemaName)
	_, err := tx.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to set tenant schema scope: %w", err)
	}

	return nil
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

// Ensure PositionRepository implements the interface at compile time
var _ domain.PositionRepository = (*PositionRepository)(nil)

// NOTE: No general Update(), Upsert(), or Merge() methods are implemented.
// This is intentional - the repository enforces append-only semantics.
// Only SoftDelete (deleted_at) and UpdateAttributes (attributes) are allowed.
// Position consolidation is handled at read time via GetAggregatedPosition().
