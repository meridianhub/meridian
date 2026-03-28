// Package persistence provides CockroachDB persistence implementations for the Forecasting service.
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
	"github.com/meridianhub/meridian/services/forecasting/domain"
)

// StrategyRepository implements domain.StrategyRepository using CockroachDB.
type StrategyRepository struct {
	pool *pgxpool.Pool
}

// NewStrategyRepository creates a new CockroachDB strategy repository.
func NewStrategyRepository(pool *pgxpool.Pool) *StrategyRepository {
	return &StrategyRepository{pool: pool}
}

// Save persists a new or updated forecasting strategy.
func (r *StrategyRepository) Save(ctx context.Context, strategy domain.ForecastingStrategy) error {
	entity := StrategyToEntity(strategy)

	// Check if this is an insert or update
	var existingVersion int64
	checkQuery := `SELECT version FROM forecasting_strategy WHERE id = $1`
	err := r.pool.QueryRow(ctx, checkQuery, entity.ID).Scan(&existingVersion)

	if errors.Is(err, pgx.ErrNoRows) {
		return r.insert(ctx, entity)
	} else if err != nil {
		return fmt.Errorf("failed to check existing strategy: %w", err)
	}

	return r.update(ctx, entity, existingVersion)
}

func (r *StrategyRepository) insert(ctx context.Context, entity ForecastingStrategyEntity) error {
	query := `
		INSERT INTO forecasting_strategy (
			id, tenant_id, name, description, starlark_code,
			horizon_hours, granularity_hours, schedule,
			input_dataset_codes, output_dataset_code,
			reference_data_resolution_key,
			status, version, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10,
			$11,
			$12, $13, $14, $15
		)`

	_, err := r.pool.Exec(ctx, query,
		entity.ID,
		entity.TenantID,
		entity.Name,
		nullStringPtr(entity.Description),
		entity.StarlarkCode,
		entity.HorizonHours,
		entity.GranularityHours,
		entity.Schedule,
		entity.InputDatasetCodes,
		entity.OutputDatasetCode,
		nullStringPtr(entity.ReferenceDataResolutionKey),
		entity.Status,
		entity.Version,
		entity.CreatedAt,
		entity.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrDuplicateActiveStrategy
		}
		return fmt.Errorf("failed to insert forecasting strategy: %w", err)
	}

	return nil
}

func (r *StrategyRepository) update(ctx context.Context, entity ForecastingStrategyEntity, expectedVersion int64) error {
	// Optimistic locking: version is incremented by the domain layer before save
	previousVersion := entity.Version - 1

	if previousVersion != expectedVersion {
		return domain.ErrVersionMismatch
	}

	query := `
		UPDATE forecasting_strategy SET
			name = $1,
			description = $2,
			starlark_code = $3,
			horizon_hours = $4,
			granularity_hours = $5,
			schedule = $6,
			input_dataset_codes = $7,
			output_dataset_code = $8,
			reference_data_resolution_key = $9,
			status = $10,
			version = $11,
			updated_at = $12
		WHERE id = $13 AND version = $14`

	result, err := r.pool.Exec(ctx, query,
		entity.Name,
		nullStringPtr(entity.Description),
		entity.StarlarkCode,
		entity.HorizonHours,
		entity.GranularityHours,
		entity.Schedule,
		entity.InputDatasetCodes,
		entity.OutputDatasetCode,
		nullStringPtr(entity.ReferenceDataResolutionKey),
		entity.Status,
		entity.Version,
		entity.UpdatedAt,
		entity.ID,
		previousVersion,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrDuplicateActiveStrategy
		}
		return fmt.Errorf("failed to update forecasting strategy: %w", err)
	}

	if result.RowsAffected() == 0 {
		return domain.ErrVersionMismatch
	}

	return nil
}

// FindByID retrieves a strategy by its unique identifier.
func (r *StrategyRepository) FindByID(ctx context.Context, id uuid.UUID) (domain.ForecastingStrategy, error) {
	query := `
		SELECT id, tenant_id, name, description, starlark_code,
			horizon_hours, granularity_hours, schedule,
			input_dataset_codes, output_dataset_code,
			reference_data_resolution_key,
			status, version, created_at, updated_at
		FROM forecasting_strategy
		WHERE id = $1`

	entity, err := r.scanStrategy(ctx, query, id)
	if err != nil {
		return domain.ForecastingStrategy{}, err
	}

	return EntityToStrategy(entity), nil
}

// FindByTenantAndName retrieves an active strategy by tenant and name.
func (r *StrategyRepository) FindByTenantAndName(ctx context.Context, tenantID string, name string) (domain.ForecastingStrategy, error) {
	query := `
		SELECT id, tenant_id, name, description, starlark_code,
			horizon_hours, granularity_hours, schedule,
			input_dataset_codes, output_dataset_code,
			reference_data_resolution_key,
			status, version, created_at, updated_at
		FROM forecasting_strategy
		WHERE tenant_id = $1 AND name = $2 AND status = 'ACTIVE'`

	entity, err := r.scanStrategy(ctx, query, tenantID, name)
	if err != nil {
		return domain.ForecastingStrategy{}, err
	}

	return EntityToStrategy(entity), nil
}

// ListByTenant returns strategies for a tenant matching the filter criteria.
func (r *StrategyRepository) ListByTenant(ctx context.Context, tenantID string, filters domain.StrategyFilters) ([]domain.ForecastingStrategy, string, error) {
	pageSize := clampPageSize(filters.Limit)

	cursorTime, cursorID, err := parseCursorToken(filters.PageToken)
	if err != nil {
		return nil, "", domain.ErrInvalidPageToken
	}

	query, args := buildListQuery(tenantID, filters, cursorTime, cursorID, pageSize)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list strategies: %w", err)
	}
	defer rows.Close()

	entities, err := r.scanAllStrategies(rows)
	if err != nil {
		return nil, "", err
	}

	return paginateResults(entities, pageSize)
}

// clampPageSize applies default and maximum bounds to the requested page size.
func clampPageSize(limit int) int {
	if limit <= 0 {
		return DefaultPageSize
	}
	if limit > MaxPageSize {
		return MaxPageSize
	}
	return limit
}

// buildListQuery constructs the SQL query and arguments for listing strategies
// with optional status and cursor filters.
func buildListQuery(tenantID string, filters domain.StrategyFilters, cursorTime time.Time, cursorID uuid.UUID, pageSize int) (string, []interface{}) {
	baseQuery := `
		SELECT id, tenant_id, name, description, starlark_code,
			horizon_hours, granularity_hours, schedule,
			input_dataset_codes, output_dataset_code,
			reference_data_resolution_key,
			status, version, created_at, updated_at
		FROM forecasting_strategy
		WHERE tenant_id = $1`

	args := []interface{}{tenantID}
	argPos := 2

	if filters.Status != nil {
		baseQuery += fmt.Sprintf(" AND status = $%d", argPos)
		args = append(args, filters.Status.String())
		argPos++
	}

	if !cursorTime.IsZero() {
		baseQuery += fmt.Sprintf(" AND (created_at < $%d OR (created_at = $%d AND id < $%d))",
			argPos, argPos+1, argPos+2)
		args = append(args, cursorTime, cursorTime, cursorID)
		argPos += 3
	}

	baseQuery += " ORDER BY created_at DESC, id DESC"
	baseQuery += fmt.Sprintf(" LIMIT $%d", argPos)
	args = append(args, pageSize+1)

	return baseQuery, args
}

// scanAllStrategies reads all strategy rows from the query result.
func (r *StrategyRepository) scanAllStrategies(rows pgx.Rows) ([]ForecastingStrategyEntity, error) {
	var entities []ForecastingStrategyEntity
	for rows.Next() {
		entity, err := r.scanStrategyFromRows(rows)
		if err != nil {
			return nil, err
		}
		entities = append(entities, entity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating strategies: %w", err)
	}
	return entities, nil
}

// paginateResults applies cursor pagination to the entity results, returning
// domain objects and a next-page token (empty string if no more pages).
func paginateResults(entities []ForecastingStrategyEntity, pageSize int) ([]domain.ForecastingStrategy, string, error) {
	var nextPageToken string
	hasMore := len(entities) > pageSize
	if hasMore {
		entities = entities[:pageSize]
		lastEntity := entities[len(entities)-1]
		nextPageToken = formatCursorToken(lastEntity.CreatedAt, lastEntity.ID)
	}

	results := make([]domain.ForecastingStrategy, 0, len(entities))
	for _, entity := range entities {
		results = append(results, EntityToStrategy(entity))
	}

	return results, nextPageToken, nil
}

// ListAllActive returns all strategies with ACTIVE status across all tenants.
func (r *StrategyRepository) ListAllActive(ctx context.Context) ([]domain.ForecastingStrategy, error) {
	query := `
		SELECT id, tenant_id, name, description, starlark_code,
			horizon_hours, granularity_hours, schedule,
			input_dataset_codes, output_dataset_code,
			reference_data_resolution_key,
			status, version, created_at, updated_at
		FROM forecasting_strategy
		WHERE status = 'ACTIVE'
		ORDER BY tenant_id, name`

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list all active strategies: %w", err)
	}
	defer rows.Close()

	var results []domain.ForecastingStrategy
	for rows.Next() {
		entity, err := r.scanStrategyFromRows(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, EntityToStrategy(entity))
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating active strategies: %w", err)
	}

	return results, nil
}

func (r *StrategyRepository) scanStrategy(ctx context.Context, query string, args ...interface{}) (ForecastingStrategyEntity, error) {
	var entity ForecastingStrategyEntity

	err := r.pool.QueryRow(ctx, query, args...).Scan(
		&entity.ID,
		&entity.TenantID,
		&entity.Name,
		&entity.Description,
		&entity.StarlarkCode,
		&entity.HorizonHours,
		&entity.GranularityHours,
		&entity.Schedule,
		&entity.InputDatasetCodes,
		&entity.OutputDatasetCode,
		&entity.ReferenceDataResolutionKey,
		&entity.Status,
		&entity.Version,
		&entity.CreatedAt,
		&entity.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity, domain.ErrStrategyNotFound
		}
		return entity, fmt.Errorf("failed to scan strategy: %w", err)
	}

	return entity, nil
}

func (r *StrategyRepository) scanStrategyFromRows(rows pgx.Rows) (ForecastingStrategyEntity, error) {
	var entity ForecastingStrategyEntity

	err := rows.Scan(
		&entity.ID,
		&entity.TenantID,
		&entity.Name,
		&entity.Description,
		&entity.StarlarkCode,
		&entity.HorizonHours,
		&entity.GranularityHours,
		&entity.Schedule,
		&entity.InputDatasetCodes,
		&entity.OutputDatasetCode,
		&entity.ReferenceDataResolutionKey,
		&entity.Status,
		&entity.Version,
		&entity.CreatedAt,
		&entity.UpdatedAt,
	)
	if err != nil {
		return entity, fmt.Errorf("failed to scan strategy row: %w", err)
	}

	return entity, nil
}

// Ensure StrategyRepository implements domain.StrategyRepository.
var _ domain.StrategyRepository = (*StrategyRepository)(nil)
