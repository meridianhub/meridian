// Package persistence provides PostgreSQL persistence implementations for the Market Information service.
package persistence

import (
	"context"
	"errors"
	"fmt"

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
				id, code, name, description, trust_level,
				created_at, created_by, updated_at, updated_by, version
			) VALUES (
				$1, $2, $3, $4, $5,
				$6, $7, $8, $9, 1
			)
			ON CONFLICT (code) DO UPDATE SET
				name = EXCLUDED.name,
				description = EXCLUDED.description,
				trust_level = EXCLUDED.trust_level,
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

// FindByID retrieves a data source by its unique identifier.
// Returns ErrDataSourceNotFound if the source does not exist.
func (r *SourceRepository) FindByID(ctx context.Context, id uuid.UUID) (domain.DataSource, error) {
	var result domain.DataSource

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, code, name, description, trust_level, created_at, updated_at, version
			FROM data_source
			WHERE id = $1 AND deleted_at IS NULL`

		var entity DataSourceEntity
		err := tx.QueryRow(ctx, query, id).Scan(
			&entity.ID,
			&entity.Code,
			&entity.Name,
			&entity.Description,
			&entity.TrustLevel,
			&entity.CreatedAt,
			&entity.UpdatedAt,
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
			SELECT id, code, name, description, trust_level, created_at, updated_at, version
			FROM data_source
			WHERE code = $1 AND deleted_at IS NULL`

		var entity DataSourceEntity
		err := tx.QueryRow(ctx, query, code).Scan(
			&entity.ID,
			&entity.Code,
			&entity.Name,
			&entity.Description,
			&entity.TrustLevel,
			&entity.CreatedAt,
			&entity.UpdatedAt,
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

// List returns all data sources, optionally filtering to only active sources.
// Returns an empty slice if no sources exist.
func (r *SourceRepository) List(ctx context.Context, _ bool) ([]domain.DataSource, error) {
	var results []domain.DataSource

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, code, name, description, trust_level, created_at, updated_at, version
			FROM data_source
			WHERE deleted_at IS NULL
			ORDER BY trust_level DESC, name ASC`

		rows, err := tx.Query(ctx, query)
		if err != nil {
			return fmt.Errorf("failed to list data sources: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var entity DataSourceEntity
			err := rows.Scan(
				&entity.ID,
				&entity.Code,
				&entity.Name,
				&entity.Description,
				&entity.TrustLevel,
				&entity.CreatedAt,
				&entity.UpdatedAt,
				&entity.Version,
			)
			if err != nil {
				return fmt.Errorf("failed to scan data source: %w", err)
			}

			results = append(results, EntityToDataSource(entity))
		}

		if err := rows.Err(); err != nil {
			return fmt.Errorf("error iterating data sources: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return results, nil
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

// Ensure SourceRepository implements domain.SourceRepository.
var _ domain.SourceRepository = (*SourceRepository)(nil)
