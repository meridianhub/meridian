package saga

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// sagaSelectColumns is the standard column list for saga definition queries.
const sagaSelectColumns = `
	sd.id, sd.name, sd.version,
	sd.script,
	sd.status, sd.is_system,
	sd.preconditions_expression, sd.display_name, sd.description,
	sd.created_at, sd.updated_at, sd.activated_at, sd.deprecated_at, sd.successor_id,
	sd.validation_status, sd.complexity_score, sd.handler_call_count, sd.validated_at`

// GetByID retrieves a specific saga by its UUID.
func (r *PostgresRegistry) GetByID(ctx context.Context, id uuid.UUID) (*Definition, error) {
	var result *Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `SELECT ` + sagaSelectColumns + `
			FROM saga_definition sd
			WHERE sd.id = $1`

		row := tx.QueryRow(ctx, query, id)
		def, err := r.scanDefinitionWithFallback(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query saga definition: %w", err)
		}

		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// GetDefinition retrieves a specific saga by name and version.
func (r *PostgresRegistry) GetDefinition(ctx context.Context, name string, version int) (*Definition, error) {
	var result *Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `SELECT ` + sagaSelectColumns + `
			FROM saga_definition sd
			WHERE sd.name = $1 AND sd.version = $2`

		row := tx.QueryRow(ctx, query, name, version)
		def, err := r.scanDefinitionWithFallback(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query saga definition: %w", err)
		}

		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// GetActive retrieves the active saga for a name using tenant resolution.
// Resolution order:
//  1. Tenant override (is_system=FALSE, status=ACTIVE, highest version)
//  2. Platform default (is_system=TRUE, status=ACTIVE, highest version)
func (r *PostgresRegistry) GetActive(ctx context.Context, name string) (*Definition, error) {
	var result *Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		// Step 1: Try tenant override (is_system=FALSE)
		row := tx.QueryRow(ctx, activeSagaQuery(false), name)
		def, err := r.scanDefinitionWithFallback(row)
		if err == nil {
			r.logger.Debug("resolved saga using tenant override",
				"name", name, "saga_id", def.ID)
			result = def
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("failed to query tenant saga: %w", err)
		}

		// Step 2: Fall back to platform default (is_system=TRUE)
		row = tx.QueryRow(ctx, activeSagaQuery(true), name)
		def, err = r.scanDefinitionWithFallback(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query platform saga: %w", err)
		}

		r.logger.Debug("resolved saga using platform default",
			"name", name, "saga_id", def.ID, "is_system", true)
		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// activeSagaQuery returns the SQL query for fetching active saga definitions.
// The isSystem parameter controls whether to query tenant overrides (false) or platform defaults (true).
func activeSagaQuery(isSystem bool) string {
	return `SELECT ` + sagaSelectColumns + `
		FROM saga_definition sd
		WHERE sd.name = $1 AND sd.status = 'ACTIVE' AND sd.is_system = ` + fmt.Sprintf("%t", isSystem) + `
		ORDER BY sd.version DESC
		LIMIT 1`
}

// ListByStatus retrieves all sagas with the specified status.
func (r *PostgresRegistry) ListByStatus(ctx context.Context, status Status) ([]*Definition, error) {
	var result []*Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `SELECT ` + sagaSelectColumns + `
			FROM saga_definition sd
			WHERE sd.status = $1
			ORDER BY sd.name, sd.version DESC`

		rows, err := tx.Query(ctx, query, string(status))
		if err != nil {
			return fmt.Errorf("failed to query sagas by status: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			def, err := r.scanDefinitionWithFallbackFromRows(rows)
			if err != nil {
				return err
			}
			result = append(result, def)
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}
