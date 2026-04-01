package accounttype

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetDefinitionByID retrieves a specific account type by its UUID.
func (r *PostgresRegistry) GetDefinitionByID(ctx context.Context, id uuid.UUID) (*Definition, error) {
	var result *Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, code, version, display_name, description,
				normal_balance, behavior_class, instrument_code,
				default_saga_prefix, default_conversion_method_id, default_conversion_method_version,
				validation_cel, bucketing_cel, eligibility_cel,
				attribute_schema, attributes,
				status, is_system, successor_id,
				created_at, updated_at, activated_at, deprecated_at
			FROM account_type_definitions
			WHERE id = $1`

		row := tx.QueryRow(ctx, query, id)
		def, err := r.scanDefinition(row)
		if err != nil {
			return err
		}

		methods, err := r.loadValuationMethods(ctx, tx, def.ID)
		if err != nil {
			return err
		}
		def.ValuationMethods = methods

		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// GetDefinition retrieves a specific account type by code and version.
func (r *PostgresRegistry) GetDefinition(ctx context.Context, code string, version int) (*Definition, error) {
	var result *Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		def, err := r.getDefinitionInTx(ctx, tx, code, version)
		if err != nil {
			return err
		}

		// Load valuation methods
		methods, err := r.loadValuationMethods(ctx, tx, def.ID)
		if err != nil {
			return err
		}
		def.ValuationMethods = methods

		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (r *PostgresRegistry) getDefinitionInTx(ctx context.Context, tx pgx.Tx, code string, version int) (*Definition, error) {
	query := `
		SELECT id, code, version, display_name, description,
			normal_balance, behavior_class, instrument_code,
			default_saga_prefix, default_conversion_method_id, default_conversion_method_version,
			validation_cel, bucketing_cel, eligibility_cel,
			attribute_schema, attributes,
			status, is_system, successor_id,
			created_at, updated_at, activated_at, deprecated_at
		FROM account_type_definitions
		WHERE code = $1 AND version = $2`

	row := tx.QueryRow(ctx, query, code, version)
	return r.scanDefinition(row)
}

// GetActiveDefinition retrieves the latest ACTIVE version of an account type.
func (r *PostgresRegistry) GetActiveDefinition(ctx context.Context, code string) (*Definition, error) {
	var result *Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, code, version, display_name, description,
				normal_balance, behavior_class, instrument_code,
				default_saga_prefix, default_conversion_method_id, default_conversion_method_version,
				validation_cel, bucketing_cel, eligibility_cel,
				attribute_schema, attributes,
				status, is_system, successor_id,
				created_at, updated_at, activated_at, deprecated_at
			FROM account_type_definitions
			WHERE code = $1 AND status = 'ACTIVE'
			ORDER BY version DESC
			LIMIT 1`

		row := tx.QueryRow(ctx, query, code)
		def, err := r.scanDefinition(row)
		if err != nil {
			return err
		}

		methods, err := r.loadValuationMethods(ctx, tx, def.ID)
		if err != nil {
			return err
		}
		def.ValuationMethods = methods

		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// ListActive retrieves all account type definitions with ACTIVE status.
func (r *PostgresRegistry) ListActive(ctx context.Context) ([]*Definition, error) {
	var result []*Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, code, version, display_name, description,
				normal_balance, behavior_class, instrument_code,
				default_saga_prefix, default_conversion_method_id, default_conversion_method_version,
				validation_cel, bucketing_cel, eligibility_cel,
				attribute_schema, attributes,
				status, is_system, successor_id,
				created_at, updated_at, activated_at, deprecated_at
			FROM account_type_definitions
			WHERE status = 'ACTIVE'
			ORDER BY code, version DESC`

		rows, err := tx.Query(ctx, query)
		if err != nil {
			return fmt.Errorf("failed to query active account types: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			def, err := r.scanDefinitionFromRows(rows)
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

// ListAll retrieves account type definitions across all statuses.
// Pass statusFilter to restrict results; an empty slice returns all statuses.
func (r *PostgresRegistry) ListAll(ctx context.Context, statusFilter []Status) ([]*Definition, error) {
	var result []*Definition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		var query string
		var args []any

		if len(statusFilter) == 0 {
			query = `
				SELECT id, code, version, display_name, description,
					normal_balance, behavior_class, instrument_code,
					default_saga_prefix, default_conversion_method_id, default_conversion_method_version,
					validation_cel, bucketing_cel, eligibility_cel,
					attribute_schema, attributes,
					status, is_system, successor_id,
					created_at, updated_at, activated_at, deprecated_at
				FROM account_type_definitions
				ORDER BY code, version DESC`
		} else {
			statuses := make([]string, len(statusFilter))
			for i, s := range statusFilter {
				statuses[i] = string(s)
			}
			query = `
				SELECT id, code, version, display_name, description,
					normal_balance, behavior_class, instrument_code,
					default_saga_prefix, default_conversion_method_id, default_conversion_method_version,
					validation_cel, bucketing_cel, eligibility_cel,
					attribute_schema, attributes,
					status, is_system, successor_id,
					created_at, updated_at, activated_at, deprecated_at
				FROM account_type_definitions
				WHERE status = ANY($1)
				ORDER BY code, version DESC`
			args = []any{statuses}
		}

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("failed to query account types: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			def, err := r.scanDefinitionFromRows(rows)
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
