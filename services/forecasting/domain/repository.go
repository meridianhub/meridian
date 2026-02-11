// Package domain contains repository port interfaces for the Forecasting service.
package domain

import (
	"context"

	"github.com/google/uuid"
)

// StrategyRepository defines the persistence port for ForecastingStrategy aggregates.
// This interface follows hexagonal architecture patterns, allowing the domain
// to remain independent of specific persistence implementations.
//
// Implementations must be thread-safe.
type StrategyRepository interface {
	// Save persists a new or updated forecasting strategy.
	// For new strategies, returns ErrDuplicateActiveStrategy if an active strategy
	// with the same name already exists for the tenant.
	// For updates, returns ErrVersionMismatch on optimistic lock failure.
	Save(ctx context.Context, strategy ForecastingStrategy) error

	// FindByID retrieves a strategy by its unique identifier.
	// Returns ErrStrategyNotFound if the strategy does not exist.
	FindByID(ctx context.Context, id uuid.UUID) (ForecastingStrategy, error)

	// FindByTenantAndName retrieves an active strategy by tenant and name.
	// Returns ErrStrategyNotFound if no matching active strategy exists.
	FindByTenantAndName(ctx context.Context, tenantID string, name string) (ForecastingStrategy, error)

	// ListByTenant returns strategies for a tenant matching the filter criteria.
	// Returns the strategies, a next page token (empty if no more results), and any error.
	ListByTenant(ctx context.Context, tenantID string, filters StrategyFilters) ([]ForecastingStrategy, string, error)

	// ListAllActive returns all strategies with ACTIVE status across all tenants.
	// Used by the cron scheduler to load strategies for scheduled execution.
	ListAllActive(ctx context.Context) ([]ForecastingStrategy, error)
}

// StrategyFilters specifies criteria for listing forecasting strategies.
type StrategyFilters struct {
	// Status filters by strategy status. Nil matches all statuses.
	Status *StrategyStatus

	// Limit specifies the maximum number of results to return.
	// Zero or negative values use the implementation's default limit.
	Limit int

	// PageToken is the cursor token from a previous List response.
	// Empty string indicates first page.
	PageToken string
}
