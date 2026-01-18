// Package domain contains repository port interfaces for the Market Information service.
package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// DataSetRepository defines the persistence port for DataSetDefinition aggregates.
// This interface follows hexagonal architecture patterns, allowing the domain
// to remain independent of specific persistence implementations.
//
// Implementations must be thread-safe and handle tenant context from ctx.
type DataSetRepository interface {
	// Save persists a new or updated dataset definition.
	// For new datasets, returns ErrDuplicateDataSetCode if the code already exists.
	// For updates, returns ErrVersionMismatch on optimistic lock failure.
	Save(ctx context.Context, dataset DataSetDefinition) error

	// FindByCode retrieves the current version of a dataset by its unique code.
	// Returns ErrDataSetNotFound if the dataset does not exist.
	FindByCode(ctx context.Context, code string) (DataSetDefinition, error)

	// FindByCodeAndVersion retrieves a specific version of a dataset.
	// Returns ErrDataSetNotFound if the dataset or version does not exist.
	FindByCodeAndVersion(ctx context.Context, code string, version int) (DataSetDefinition, error)

	// List returns datasets matching the filter criteria.
	// Returns an empty slice if no datasets match the filter.
	List(ctx context.Context, filters DataSetFilters) ([]DataSetDefinition, error)

	// ExistsByCode checks if a dataset with the given code exists.
	ExistsByCode(ctx context.Context, code string) (bool, error)
}

// DataSetFilters specifies criteria for listing dataset definitions.
// Nil pointer fields are treated as "match all" for that criterion.
type DataSetFilters struct {
	// Category filters by data category. Nil matches all categories.
	Category *DataCategory

	// Status filters by dataset status. Nil matches all statuses.
	Status *DataSetStatus

	// Limit specifies the maximum number of results to return.
	// Zero or negative values use the implementation's default limit.
	Limit int

	// Offset specifies the number of results to skip for pagination.
	Offset int
}

// ObservationRepository defines the persistence port for MarketPriceObservation aggregates.
// This interface follows hexagonal architecture patterns, allowing the domain
// to remain independent of specific persistence implementations.
//
// The repository supports bi-temporal queries through the ObservationQuery struct,
// enabling queries across both valid time and transaction time dimensions.
//
// Implementations must be thread-safe and handle tenant context from ctx.
type ObservationRepository interface {
	// Record persists a new market price observation.
	// This is an append-only operation - observations are never updated in place.
	// When superseding an existing observation, use the Supersede domain method
	// followed by saving both the superseded and new observations.
	Record(ctx context.Context, obs MarketPriceObservation) error

	// FindByID retrieves an observation by its unique identifier.
	// Returns ErrObservationNotFound if the observation does not exist.
	FindByID(ctx context.Context, id uuid.UUID) (MarketPriceObservation, error)

	// Query retrieves observations matching the query criteria.
	// Returns an empty slice if no observations match.
	// Results are ordered by ObservedAt descending (most recent first).
	Query(ctx context.Context, query ObservationQuery) ([]MarketPriceObservation, error)

	// GetLatest retrieves the most recent non-superseded observation
	// for a specific dataset and resolution key combination.
	// Returns ErrObservationNotFound if no matching observation exists.
	GetLatest(ctx context.Context, dataSetCode string, resolutionKey string) (MarketPriceObservation, error)
}

// ObservationQuery specifies criteria for querying market price observations.
// Nil pointer fields are treated as "match all" for that criterion.
type ObservationQuery struct {
	// DataSetCode filters by dataset code. Required field.
	DataSetCode string

	// ResolutionKey filters by resolution key. Nil matches all keys.
	ResolutionKey *string

	// ObservedAfter filters observations taken after this time. Nil includes all times.
	ObservedAfter *time.Time

	// ObservedBefore filters observations taken before this time. Nil includes all times.
	ObservedBefore *time.Time

	// QualityLevel filters by quality tier. Nil matches all levels.
	QualityLevel *QualityLevel

	// IncludeSuperseded when true includes observations that have been superseded.
	// When false (default), only active (non-superseded) observations are returned.
	IncludeSuperseded bool

	// Limit specifies the maximum number of results to return.
	// Zero or negative values use the implementation's default limit.
	Limit int
}

// SourceRepository defines the persistence port for DataSource entities.
// This interface follows hexagonal architecture patterns, allowing the domain
// to remain independent of specific persistence implementations.
//
// Implementations must be thread-safe and handle tenant context from ctx.
type SourceRepository interface {
	// Save persists a new or updated data source.
	// For new sources, returns ErrDuplicateDataSourceCode if the code already exists.
	Save(ctx context.Context, source DataSource) error

	// FindByID retrieves a data source by its unique identifier.
	// Returns ErrDataSourceNotFound if the source does not exist.
	FindByID(ctx context.Context, id uuid.UUID) (DataSource, error)

	// FindByCode retrieves a data source by its unique business code.
	// Returns ErrDataSourceNotFound if the source does not exist.
	FindByCode(ctx context.Context, code string) (DataSource, error)

	// List returns all data sources, optionally filtering to only active sources.
	// Returns an empty slice if no sources exist.
	List(ctx context.Context, activeOnly bool) ([]DataSource, error)
}
