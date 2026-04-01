// Package persistence provides PostgreSQL persistence implementations for the Market Information service.
package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/market-information/domain"
)

// SourceRepository implements domain.SourceRepository using PostgreSQL.
type SourceRepository struct {
	baseRepository
}

// NewSourceRepository creates a new PostgreSQL source repository.
func NewSourceRepository(pool *pgxpool.Pool) *SourceRepository {
	return &SourceRepository{
		baseRepository: newBaseRepository(pool),
	}
}

// Save persists a new or updated data source.
// For new sources, returns ErrDuplicateDataSourceCode if the code already exists.
func (r *SourceRepository) Save(ctx context.Context, source domain.DataSource) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		userID := getUserFromContext(ctx)

		// Try to insert, on conflict update
		query := `
			INSERT INTO data_source (
				id, code, name, description, trust_level, status,
				created_at, created_by, updated_at, updated_by, version
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				$7, $8, $9, $10, 1
			)
			ON CONFLICT (code) DO UPDATE SET
				name = EXCLUDED.name,
				description = EXCLUDED.description,
				trust_level = EXCLUDED.trust_level,
				status = EXCLUDED.status,
				deprecated_at = EXCLUDED.deprecated_at,
				updated_at = EXCLUDED.updated_at,
				updated_by = EXCLUDED.updated_by,
				version = data_source.version + 1
			WHERE data_source.deleted_at IS NULL`

		entity := DataSourceToEntity(source)
		_, err := tx.Exec(ctx, query,
			entity.ID,
			entity.Code,
			entity.Name,
			entity.Description,
			entity.TrustLevel,
			entity.Status,
			entity.CreatedAt,
			userID,
			entity.UpdatedAt,
			userID,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return domain.ErrDuplicateDataSourceCode
			}
			return fmt.Errorf("failed to save data source: %w", err)
		}

		return nil
	})
}

// Delete soft-deletes a data source by setting deleted_at.
// Returns ErrDataSourceNotFound if the source does not exist.
func (r *SourceRepository) Delete(ctx context.Context, code string) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		userID := getUserFromContext(ctx)

		query := `
			UPDATE data_source
			SET deleted_at = NOW(), updated_at = NOW(), updated_by = $2
			WHERE code = $1 AND deleted_at IS NULL`

		result, err := tx.Exec(ctx, query, code, userID)
		if err != nil {
			return fmt.Errorf("failed to delete data source: %w", err)
		}

		if result.RowsAffected() == 0 {
			return domain.ErrDataSourceNotFound
		}

		return nil
	})
}

// FindByID retrieves a data source by its unique identifier.
// Returns ErrDataSourceNotFound if the source does not exist.
func (r *SourceRepository) FindByID(ctx context.Context, id uuid.UUID) (domain.DataSource, error) {
	var result domain.DataSource

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, code, name, description, trust_level, status, created_at, updated_at, deprecated_at, version
			FROM data_source
			WHERE id = $1 AND deleted_at IS NULL`

		var entity DataSourceEntity
		err := tx.QueryRow(ctx, query, id).Scan(
			&entity.ID,
			&entity.Code,
			&entity.Name,
			&entity.Description,
			&entity.TrustLevel,
			&entity.Status,
			&entity.CreatedAt,
			&entity.UpdatedAt,
			&entity.DeprecatedAt,
			&entity.Version,
		)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrDataSourceNotFound
			}
			return fmt.Errorf("failed to find data source by ID: %w", err)
		}

		result = EntityToDataSource(entity)
		return nil
	})

	return result, err
}

// FindByCode retrieves a data source by its unique business code.
// Returns ErrDataSourceNotFound if the source does not exist.
func (r *SourceRepository) FindByCode(ctx context.Context, code string) (domain.DataSource, error) {
	var result domain.DataSource

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, code, name, description, trust_level, status, created_at, updated_at, deprecated_at, version
			FROM data_source
			WHERE code = $1 AND deleted_at IS NULL`

		var entity DataSourceEntity
		err := tx.QueryRow(ctx, query, code).Scan(
			&entity.ID,
			&entity.Code,
			&entity.Name,
			&entity.Description,
			&entity.TrustLevel,
			&entity.Status,
			&entity.CreatedAt,
			&entity.UpdatedAt,
			&entity.DeprecatedAt,
			&entity.Version,
		)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrDataSourceNotFound
			}
			return fmt.Errorf("failed to find data source by code: %w", err)
		}

		result = EntityToDataSource(entity)
		return nil
	})

	return result, err
}

// List returns data sources with cursor-based pagination.
// Parameters:
//   - activeOnly: currently unused (all returned sources are non-deleted)
//   - pageSize: maximum number of results to return (0 uses default, capped at MaxPageSize)
//   - pageToken: cursor token from previous response (empty for first page)
//
// Returns the sources, a next page token (empty if no more results), and any error.
// Returns ErrInvalidPageToken (wrapped as domain.ErrInvalidPageToken) if the pageToken format is invalid.
func (r *SourceRepository) List(ctx context.Context, _ bool, pageSize int, pageToken string) ([]domain.DataSource, string, error) {
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	cursorTime, cursorID, err := parseCursorToken(pageToken)
	if err != nil {
		return nil, "", domain.ErrInvalidPageToken
	}

	var results []domain.DataSource
	var nextPageToken string

	err = r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query, args := buildSourceListQuery(cursorTime, cursorID, pageSize)

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("failed to list data sources: %w", err)
		}
		defer rows.Close()

		entities, err := scanSourceEntities(rows)
		if err != nil {
			return err
		}

		results, nextPageToken = paginateSources(entities, pageSize)
		return nil
	})
	if err != nil {
		return nil, "", err
	}

	return results, nextPageToken, nil
}

// buildSourceListQuery constructs the SQL query and args for source listing,
// choosing the appropriate query based on whether a cursor is present.
func buildSourceListQuery(cursorTime time.Time, cursorID uuid.UUID, pageSize int) (string, []interface{}) {
	if cursorTime.IsZero() {
		return `
			SELECT id, code, name, description, trust_level, status, created_at, updated_at, deprecated_at, version
			FROM data_source
			WHERE deleted_at IS NULL
			ORDER BY date_trunc('second', created_at) DESC, id DESC
			LIMIT $1`, []interface{}{pageSize + 1}
	}

	return `
		SELECT id, code, name, description, trust_level, status, created_at, updated_at, deprecated_at, version
		FROM data_source
		WHERE deleted_at IS NULL
			AND (
				date_trunc('second', created_at) < $1
				OR (date_trunc('second', created_at) = $1 AND id < $2)
			)
		ORDER BY date_trunc('second', created_at) DESC, id DESC
		LIMIT $3`, []interface{}{cursorTime, cursorID, pageSize + 1}
}

// scanSourceEntities scans all rows into DataSourceEntity slices.
func scanSourceEntities(rows pgx.Rows) ([]DataSourceEntity, error) {
	var entities []DataSourceEntity
	for rows.Next() {
		var entity DataSourceEntity
		err := rows.Scan(
			&entity.ID,
			&entity.Code,
			&entity.Name,
			&entity.Description,
			&entity.TrustLevel,
			&entity.Status,
			&entity.CreatedAt,
			&entity.UpdatedAt,
			&entity.DeprecatedAt,
			&entity.Version,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan data source: %w", err)
		}
		entities = append(entities, entity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating data sources: %w", err)
	}
	return entities, nil
}

// paginateSources trims results to pageSize, generates a next page token, and converts to domain.
func paginateSources(entities []DataSourceEntity, pageSize int) ([]domain.DataSource, string) {
	var nextPageToken string
	hasMore := len(entities) > pageSize
	if hasMore {
		entities = entities[:pageSize]
		lastEntity := entities[len(entities)-1]
		nextPageToken = formatCursorToken(lastEntity.CreatedAt, lastEntity.ID)
	}

	results := make([]domain.DataSource, 0, len(entities))
	for _, entity := range entities {
		results = append(results, EntityToDataSource(entity))
	}
	return results, nextPageToken
}

// GetTrustLevel retrieves the trust level for a data source by ID.
// This is a helper method used by observation queries to get trust level for quality ordering.
func (r *SourceRepository) GetTrustLevel(ctx context.Context, sourceID uuid.UUID) (int, error) {
	var trustLevel int

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `SELECT trust_level FROM data_source WHERE id = $1 AND deleted_at IS NULL`
		err := tx.QueryRow(ctx, query, sourceID).Scan(&trustLevel)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrDataSourceNotFound
			}
			return fmt.Errorf("failed to get trust level: %w", err)
		}
		return nil
	})

	return trustLevel, err
}

// Deprecate transitions a data source from ACTIVE to DEPRECATED.
// Returns ErrDataSourceNotFound if the source does not exist.
// Returns ErrDataSourceNotActive if the source is not in ACTIVE status.
func (r *SourceRepository) Deprecate(ctx context.Context, code string) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		userID := getUserFromContext(ctx)

		// Check current status
		var currentStatus string
		checkQuery := `SELECT status FROM data_source WHERE code = $1 AND deleted_at IS NULL`
		err := tx.QueryRow(ctx, checkQuery, code).Scan(&currentStatus)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrDataSourceNotFound
			}
			return fmt.Errorf("failed to check data source status: %w", err)
		}

		if currentStatus == "DEPRECATED" {
			return nil // idempotent
		}
		if currentStatus != "ACTIVE" {
			return domain.ErrDataSourceNotActive
		}

		// Transition to DEPRECATED
		now := time.Now().UTC()
		updateQuery := `
			UPDATE data_source SET
				status = 'DEPRECATED',
				deprecated_at = $1,
				updated_at = $1,
				updated_by = $3
			WHERE code = $2 AND deleted_at IS NULL`

		_, err = tx.Exec(ctx, updateQuery, now, code, userID)
		if err != nil {
			return fmt.Errorf("failed to deprecate data source: %w", err)
		}

		return nil
	})
}

// Ensure SourceRepository implements domain.SourceRepository.
var _ domain.SourceRepository = (*SourceRepository)(nil)
