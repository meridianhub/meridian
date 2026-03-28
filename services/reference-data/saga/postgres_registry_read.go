package saga

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetByID retrieves a specific saga by its UUID, resolving platform fallback if needed.
func (r *PostgresRegistry) GetByID(ctx context.Context, id uuid.UUID) (*Definition, error) {
	var result *Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT sd.id, sd.name, sd.version,
				COALESCE(NULLIF(sd.script, ''), psd.script, '') AS resolved_script,
				sd.script,
				sd.status, sd.is_system,
				sd.preconditions_expression, sd.display_name, sd.description,
				sd.created_at, sd.updated_at, sd.activated_at, sd.deprecated_at, sd.successor_id,
				sd.platform_ref, sd.override_reason, sd.platform_version_at_override,
				psd.script IS NOT NULL AND (sd.script IS NULL OR sd.script = '') AS used_platform_fallback,
				sd.validation_status, sd.complexity_score, sd.handler_call_count, sd.validated_at
			FROM saga_definition sd
			LEFT JOIN public.platform_saga_definition psd ON sd.platform_ref = psd.id
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

// GetDefinition retrieves a specific saga by name and version, resolving platform fallback if needed.
func (r *PostgresRegistry) GetDefinition(ctx context.Context, name string, version int) (*Definition, error) {
	var result *Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT sd.id, sd.name, sd.version,
				COALESCE(NULLIF(sd.script, ''), psd.script, '') AS resolved_script,
				sd.script,
				sd.status, sd.is_system,
				sd.preconditions_expression, sd.display_name, sd.description,
				sd.created_at, sd.updated_at, sd.activated_at, sd.deprecated_at, sd.successor_id,
				sd.platform_ref, sd.override_reason, sd.platform_version_at_override,
				psd.script IS NOT NULL AND (sd.script IS NULL OR sd.script = '') AS used_platform_fallback,
				sd.validation_status, sd.complexity_score, sd.handler_call_count, sd.validated_at
			FROM saga_definition sd
			LEFT JOIN public.platform_saga_definition psd ON sd.platform_ref = psd.id
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

// GetActive retrieves the active saga for a name using tenant resolution with platform fallback.
// Resolution order:
//  1. Tenant override (is_system=FALSE, status=ACTIVE, highest version)
//     - If tenant saga has platform_ref, resolve script via COALESCE with platform
//  2. Platform default (is_system=TRUE, status=ACTIVE, highest version)
//     - System sagas may also have platform_ref for script inheritance
func (r *PostgresRegistry) GetActive(ctx context.Context, name string) (*Definition, error) {
	var result *Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		// Step 1: Try tenant override (is_system=FALSE) with platform fallback via LEFT JOIN
		row := tx.QueryRow(ctx, activeSagaQuery(false), name)
		def, err := r.scanDefinitionWithFallback(row)
		if err == nil {
			if def.UsedPlatformFallback {
				r.logger.Debug("resolved saga using platform fallback",
					"name", name, "saga_id", def.ID, "platform_ref", def.PlatformRef)
			} else {
				r.logger.Debug("resolved saga using tenant override",
					"name", name, "saga_id", def.ID)
			}
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

// activeSagaQuery returns the SQL query for fetching active saga definitions with platform fallback.
// The isSystem parameter controls whether to query tenant overrides (false) or platform defaults (true).
func activeSagaQuery(isSystem bool) string {
	return `
		SELECT sd.id, sd.name, sd.version,
			COALESCE(NULLIF(sd.script, ''), psd.script, '') AS resolved_script,
			sd.script,
			sd.status, sd.is_system,
			sd.preconditions_expression, sd.display_name, sd.description,
			sd.created_at, sd.updated_at, sd.activated_at, sd.deprecated_at, sd.successor_id,
			sd.platform_ref, sd.override_reason, sd.platform_version_at_override,
			psd.script IS NOT NULL AND (sd.script IS NULL OR sd.script = '') AS used_platform_fallback,
			sd.validation_status, sd.complexity_score, sd.handler_call_count, sd.validated_at
		FROM saga_definition sd
		LEFT JOIN public.platform_saga_definition psd ON sd.platform_ref = psd.id
		WHERE sd.name = $1 AND sd.status = 'ACTIVE' AND sd.is_system = ` + fmt.Sprintf("%t", isSystem) + `
		ORDER BY sd.version DESC
		LIMIT 1`
}

// ListByStatus retrieves all sagas with the specified status, resolving platform fallback.
func (r *PostgresRegistry) ListByStatus(ctx context.Context, status Status) ([]*Definition, error) {
	var result []*Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT sd.id, sd.name, sd.version,
				COALESCE(NULLIF(sd.script, ''), psd.script, '') AS resolved_script,
				sd.script,
				sd.status, sd.is_system,
				sd.preconditions_expression, sd.display_name, sd.description,
				sd.created_at, sd.updated_at, sd.activated_at, sd.deprecated_at, sd.successor_id,
				sd.platform_ref, sd.override_reason, sd.platform_version_at_override,
				psd.script IS NOT NULL AND (sd.script IS NULL OR sd.script = '') AS used_platform_fallback,
				sd.validation_status, sd.complexity_score, sd.handler_call_count, sd.validated_at
			FROM saga_definition sd
			LEFT JOIN public.platform_saga_definition psd ON sd.platform_ref = psd.id
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
