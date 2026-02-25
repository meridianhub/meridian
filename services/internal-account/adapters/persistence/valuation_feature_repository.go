package persistence

import (
	vf "github.com/meridianhub/meridian/shared/pkg/valuationfeature"
	"gorm.io/gorm"
)

// ValuationFeature repository error aliases.
// These maintain backwards compatibility for code within the internal-account service.
var (
	ErrValuationFeatureNotFound        = vf.ErrNotFound
	ErrValuationFeatureVersionConflict = vf.ErrVersionConflict
	ErrValuationFeatureAlreadyExists   = vf.ErrAlreadyExists
)

// ValuationFeatureRepository is a type alias re-exporting the shared repository.
type ValuationFeatureRepository = vf.Repository

// NewValuationFeatureRepository creates a new valuation feature repository using the shared implementation.
func NewValuationFeatureRepository(db *gorm.DB) *ValuationFeatureRepository {
	return vf.NewRepository(db)
}
