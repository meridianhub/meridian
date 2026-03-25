// Package persistence provides PostgreSQL persistence implementations for the Market Information service.
package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/market-information/domain"
)

// DataSetRepository implements domain.DataSetRepository using PostgreSQL.
type DataSetRepository struct {
	baseRepository
}

// NewDataSetRepository creates a new PostgreSQL dataset repository.
func NewDataSetRepository(pool *pgxpool.Pool) *DataSetRepository {
	return &DataSetRepository{
		baseRepository: newBaseRepository(pool),
	}
}

// Save persists a new or updated dataset definition.
// For new datasets, returns ErrDuplicateDataSetCode if the code already exists.
// For updates, returns ErrVersionMismatch on optimistic lock failure.
func (r *DataSetRepository) Save(ctx context.Context, dataset domain.DataSetDefinition) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		userID := getUserFromContext(ctx)
		entity := DataSetDefinitionToEntity(dataset)

		// Check if this is an insert or update by looking for existing record
		var existingVersion int
		checkQuery := `SELECT version FROM dataset_definition WHERE id = $1 AND deleted_at IS NULL`
		err := tx.QueryRow(ctx, checkQuery, entity.ID).Scan(&existingVersion)

		if errors.Is(err, pgx.ErrNoRows) {
			// New record - insert
			return r.insertDataSet(ctx, tx, entity, userID)
		} else if err != nil {
			return fmt.Errorf("failed to check existing dataset: %w", err)
		}

		// Existing record - update with optimistic locking
		return r.updateDataSet(ctx, tx, entity, userID, existingVersion)
	})
}

func (r *DataSetRepository) insertDataSet(ctx context.Context, tx pgx.Tx, entity DataSetDefinitionEntity, userID string) error {
	query := `
		INSERT INTO dataset_definition (
			id, code, version, name, description, data_category,
			validation_expression, resolution_key_expression, error_message_expression,
			status, is_shared, access_level, created_at, created_by, updated_at, updated_by,
			activated_at, deprecated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9,
			$10, $11, $12, $13, $14, $15, $16,
			$17, $18
		)
		ON CONFLICT (code, version) DO NOTHING`

	_, err := tx.Exec(ctx, query,
		entity.ID,
		entity.Code,
		entity.Version,
		entity.Name,
		nullStringPtr(entity.Description),
		nullStringPtr(entity.DataCategory),
		nullStringPtr(entity.ValidationExpression),
		entity.ResolutionKeyExpression,
		nullStringPtr(entity.ErrorMessageExpression),
		entity.Status,
		entity.IsShared,
		entity.AccessLevel,
		entity.CreatedAt,
		userID,
		entity.UpdatedAt,
		userID,
		nullTimePtr(entity.ActivatedAt),
		nullTimePtr(entity.DeprecatedAt),
	)
	if err != nil {
		return fmt.Errorf("failed to insert dataset definition: %w", err)
	}

	return nil
}

func (r *DataSetRepository) updateDataSet(ctx context.Context, tx pgx.Tx, entity DataSetDefinitionEntity, userID string, expectedVersion int) error {
	// Optimistic locking: only update if version matches
	// Note: version is incremented by the domain layer before save
	previousVersion := entity.Version - 1

	if previousVersion != expectedVersion {
		return domain.ErrVersionMismatch
	}

	query := `
		UPDATE dataset_definition SET
			name = $1,
			description = $2,
			data_category = $3,
			validation_expression = $4,
			resolution_key_expression = $5,
			error_message_expression = $6,
			status = $7,
			is_shared = $8,
			access_level = $9,
			version = $10,
			updated_at = $11,
			updated_by = $12,
			activated_at = $13,
			deprecated_at = $14
		WHERE id = $15 AND version = $16 AND deleted_at IS NULL`

	result, err := tx.Exec(ctx, query,
		entity.Name,
		nullStringPtr(entity.Description),
		nullStringPtr(entity.DataCategory),
		nullStringPtr(entity.ValidationExpression),
		entity.ResolutionKeyExpression,
		nullStringPtr(entity.ErrorMessageExpression),
		entity.Status,
		entity.IsShared,
		entity.AccessLevel,
		entity.Version,
		entity.UpdatedAt,
		userID,
		nullTimePtr(entity.ActivatedAt),
		nullTimePtr(entity.DeprecatedAt),
		entity.ID,
		previousVersion,
	)
	if err != nil {
		return fmt.Errorf("failed to update dataset definition: %w", err)
	}

	if result.RowsAffected() == 0 {
		return domain.ErrVersionMismatch
	}

	return nil
}

// FindByCode retrieves the current version of a dataset by its unique code.
// Returns the latest ACTIVE version, or the highest version if no ACTIVE version exists.
// Returns ErrDataSetNotFound if the dataset does not exist.
func (r *DataSetRepository) FindByCode(ctx context.Context, code string) (domain.DataSetDefinition, error) {
	var result domain.DataSetDefinition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		// Try to find ACTIVE version first, then fall back to highest version
		query := `
			SELECT id, code, version, name, description, data_category,
				validation_expression, resolution_key_expression, error_message_expression,
				status, is_shared, access_level, created_at, updated_at, activated_at, deprecated_at
			FROM dataset_definition
			WHERE code = $1 AND deleted_at IS NULL
			ORDER BY
				CASE WHEN status = 'ACTIVE' THEN 0 ELSE 1 END,
				version DESC
			LIMIT 1`

		entity, err := r.scanDataSetDefinition(ctx, tx, query, code)
		if err != nil {
			return err
		}

		result = EntityToDataSetDefinition(entity)
		return nil
	})

	return result, err
}

// FindByCodeAndVersion retrieves a specific version of a dataset.
// Returns ErrDataSetNotFound if the dataset or version does not exist.
func (r *DataSetRepository) FindByCodeAndVersion(ctx context.Context, code string, version int) (domain.DataSetDefinition, error) {
	var result domain.DataSetDefinition

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, code, version, name, description, data_category,
				validation_expression, resolution_key_expression, error_message_expression,
				status, is_shared, access_level, created_at, updated_at, activated_at, deprecated_at
			FROM dataset_definition
			WHERE code = $1 AND version = $2 AND deleted_at IS NULL`

		entity, err := r.scanDataSetDefinition(ctx, tx, query, code, version)
		if err != nil {
			return err
		}

		result = EntityToDataSetDefinition(entity)
		return nil
	})

	return result, err
}

// List returns datasets matching the filter criteria with cursor-based pagination.
// Returns the datasets, a next page token (empty if no more results), and any error.
// Returns ErrInvalidPageToken (wrapped as domain.ErrInvalidPageToken) if the pageToken format is invalid.
func (r *DataSetRepository) List(ctx context.Context, filters domain.DataSetFilters) ([]domain.DataSetDefinition, string, error) {
	// Apply pagination defaults and limits
	pageSize := filters.Limit
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	// Parse cursor token
	cursorTime, cursorID, err := parseCursorToken(filters.PageToken)
	if err != nil {
		return nil, "", domain.ErrInvalidPageToken
	}

	var results []domain.DataSetDefinition
	var nextPageToken string

	err = r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		// Build dynamic query with filters
		baseQuery := `
			SELECT id, code, version, name, description, data_category,
				validation_expression, resolution_key_expression, error_message_expression,
				status, is_shared, access_level, created_at, updated_at, activated_at, deprecated_at
			FROM dataset_definition
			WHERE deleted_at IS NULL`

		args := []interface{}{}
		argPos := 1

		// Apply category filter
		if filters.Category != nil {
			baseQuery += fmt.Sprintf(" AND data_category = $%d", argPos)
			args = append(args, filters.Category.String())
			argPos++
		}

		// Apply status filter
		if filters.Status != nil {
			baseQuery += fmt.Sprintf(" AND status = $%d", argPos)
			args = append(args, filters.Status.String())
			argPos++
		}

		// Apply cursor pagination if not first page
		if !cursorTime.IsZero() {
			baseQuery += fmt.Sprintf(" AND (date_trunc('second', created_at) < $%d OR (date_trunc('second', created_at) = $%d AND id < $%d))",
				argPos, argPos+1, argPos+2)
			args = append(args, cursorTime, cursorTime, cursorID)
			argPos += 3
		}

		// Order by created_at DESC, id DESC for consistent cursor pagination
		baseQuery += " ORDER BY date_trunc('second', created_at) DESC, id DESC"

		// Fetch one extra to detect if there's a next page
		baseQuery += fmt.Sprintf(" LIMIT $%d", argPos)
		args = append(args, pageSize+1)

		rows, err := tx.Query(ctx, baseQuery, args...)
		if err != nil {
			return fmt.Errorf("failed to list dataset definitions: %w", err)
		}
		defer rows.Close()

		var entities []DataSetDefinitionEntity
		for rows.Next() {
			entity, err := r.scanDataSetDefinitionFromRows(rows)
			if err != nil {
				return err
			}
			entities = append(entities, entity)
		}

		if err := rows.Err(); err != nil {
			return fmt.Errorf("error iterating dataset definitions: %w", err)
		}

		// Check for more results
		hasMore := len(entities) > pageSize
		if hasMore {
			entities = entities[:pageSize]
			lastEntity := entities[len(entities)-1]
			nextPageToken = formatCursorToken(lastEntity.CreatedAt, lastEntity.ID)
		}

		// Convert to domain
		for _, entity := range entities {
			results = append(results, EntityToDataSetDefinition(entity))
		}

		return nil
	})
	if err != nil {
		return nil, "", err
	}

	return results, nextPageToken, nil
}

// ExistsByCode checks if a dataset with the given code exists.
func (r *DataSetRepository) ExistsByCode(ctx context.Context, code string) (bool, error) {
	var exists bool

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `SELECT EXISTS(SELECT 1 FROM dataset_definition WHERE code = $1 AND deleted_at IS NULL)`
		return tx.QueryRow(ctx, query, code).Scan(&exists)
	})

	return exists, err
}

// scanDataSetDefinition executes a query and scans a single DataSetDefinitionEntity.
func (r *DataSetRepository) scanDataSetDefinition(ctx context.Context, tx pgx.Tx, query string, args ...interface{}) (DataSetDefinitionEntity, error) {
	var entity DataSetDefinitionEntity

	err := tx.QueryRow(ctx, query, args...).Scan(
		&entity.ID,
		&entity.Code,
		&entity.Version,
		&entity.Name,
		&entity.Description,
		&entity.DataCategory,
		&entity.ValidationExpression,
		&entity.ResolutionKeyExpression,
		&entity.ErrorMessageExpression,
		&entity.Status,
		&entity.IsShared,
		&entity.AccessLevel,
		&entity.CreatedAt,
		&entity.UpdatedAt,
		&entity.ActivatedAt,
		&entity.DeprecatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity, domain.ErrDataSetNotFound
		}
		return entity, fmt.Errorf("failed to scan dataset definition: %w", err)
	}

	return entity, nil
}

// scanDataSetDefinitionFromRows scans a DataSetDefinitionEntity from rows.
func (r *DataSetRepository) scanDataSetDefinitionFromRows(rows pgx.Rows) (DataSetDefinitionEntity, error) {
	var entity DataSetDefinitionEntity

	err := rows.Scan(
		&entity.ID,
		&entity.Code,
		&entity.Version,
		&entity.Name,
		&entity.Description,
		&entity.DataCategory,
		&entity.ValidationExpression,
		&entity.ResolutionKeyExpression,
		&entity.ErrorMessageExpression,
		&entity.Status,
		&entity.IsShared,
		&entity.AccessLevel,
		&entity.CreatedAt,
		&entity.UpdatedAt,
		&entity.ActivatedAt,
		&entity.DeprecatedAt,
	)
	if err != nil {
		return entity, fmt.Errorf("failed to scan dataset definition row: %w", err)
	}

	return entity, nil
}

// Ensure DataSetRepository implements domain.DataSetRepository.
var _ domain.DataSetRepository = (*DataSetRepository)(nil)
