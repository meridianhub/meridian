// Package persistence provides PostgreSQL persistence implementations for the Market Information service.
package persistence

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/market-information/domain"
)

// Repositories holds all repository implementations for the Market Information service.
// All repositories share the same connection pool for efficiency.
type Repositories struct {
	DataSet     domain.DataSetRepository
	Observation domain.ObservationRepository
	Source      domain.SourceRepository
}

// NewRepositories creates all repository implementations with a shared connection pool.
// This is the recommended way to create repositories for production use.
// masterTenantID is the tenant ID that hosts shared/public market data (e.g., "master").
// Panics if masterTenantID is empty - this is a fatal configuration error that should be caught at startup.
func NewRepositories(pool *pgxpool.Pool, masterTenantID string) *Repositories {
	// Validate masterTenantID upfront - fail fast if misconfigured
	if masterTenantID == "" {
		panic("masterTenantID cannot be empty - this is a required configuration parameter for multi-tenant data access")
	}

	datasetRepo := NewDataSetRepository(pool)
	return &Repositories{
		DataSet:     datasetRepo,
		Observation: NewObservationRepository(pool, datasetRepo, masterTenantID),
		Source:      NewSourceRepository(pool),
	}
}
